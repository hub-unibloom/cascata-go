package services

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"cascata-backend/internal/config"
)

var (
	ErrEngineSealed = errors.New("ENGINE_SEALED")
	cryptoClient    = &http.Client{
		Timeout: 5 * time.Second,
	}
)

// CryptoService provides all cryptographic operations via the Sovereign Engine
type CryptoService struct{}

type CryptoStatus struct {
	Sealed bool   `json:"sealed"`
	Engine string `json:"engine"`
}

type HandshakeSession struct {
	SessionID         string `json:"sessionId"`
	ServerPublicKey   string `json:"serverPublicKey"`
	ServerFingerprint string `json:"serverFingerprint"`
	ServerPrivateKey  string `json:"serverPrivateKey"`
}

// GenerateKey produces a 32-byte hex-encoded random key
func (c *CryptoService) GenerateKey() string {
	b := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return ""
	}
	return hex.EncodeToString(b)
}

// CreateHandshakeSession generates an ephemeral ECDH X25519 session
func (c *CryptoService) CreateHandshakeSession() (*HandshakeSession, error) {
	resp, err := doCryptoRequest("POST", "/v1/handshake", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var session HandshakeSession
	if err := json.NewDecoder(resp.Body).Decode(&session); err != nil {
		return nil, err
	}
	return &session, nil
}

// Encrypt plaintext string via Crypto Engine
func (c *CryptoService) Encrypt(keyName, plaintext string) (string, error) {
	b64 := base64.StdEncoding.EncodeToString([]byte(plaintext))

	reqBody, _ := json.Marshal(map[string]string{
		"key":       keyName,
		"plaintext": b64,
	})

	var lastErr error

	// Retry logic (3 attempts with exponential backoff)
	for i := 0; i < 3; i++ {
		if i > 0 {
			time.Sleep(time.Duration(500*(i+1)) * time.Millisecond)
		}

		resp, err := doCryptoRequest("POST", "/v1/encrypt", reqBody)
		if err != nil {
			lastErr = err
			if errors.Is(err, ErrEngineSealed) {
				return "", err
			}
			continue
		}
		defer resp.Body.Close()

		var res struct {
			Ciphertext string `json:"ciphertext"`
			Error      string `json:"error"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
			lastErr = err
			continue
		}

		if res.Error != "" {
			if res.Error == "engine_sealed" {
				return "", ErrEngineSealed
			}
			lastErr = errors.New(res.Error)
			continue
		}

		return res.Ciphertext, nil
	}

	return "", fmt.Errorf("Crypto Engine Error: %v", lastErr)
}

// Decrypt ciphertext via Crypto Engine
func (c *CryptoService) Decrypt(ciphertext string) (string, error) {
	if ciphertext == "" || !strings.HasPrefix(ciphertext, "cse:v1:") {
		return ciphertext, nil
	}

	reqBody, _ := json.Marshal(map[string]string{
		"ciphertext": ciphertext,
	})

	resp, err := doCryptoRequest("POST", "/v1/decrypt", reqBody)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var res struct {
		Plaintext string `json:"plaintext"`
		Error     string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return "", err
	}

	if res.Error != "" {
		if res.Error == "engine_sealed" {
			return "", ErrEngineSealed
		}
		return "", errors.New(res.Error)
	}

	decoded, err := base64.StdEncoding.DecodeString(res.Plaintext)
	if err != nil {
		return "", err
	}

	return string(decoded), nil
}

// EncryptBatch handles multiple encryptions at once
func (c *CryptoService) EncryptBatch(keyName string, items []string) ([]string, error) {
	b64Items := make([]string, len(items))
	for i, item := range items {
		b64Items[i] = base64.StdEncoding.EncodeToString([]byte(item))
	}

	reqBody, _ := json.Marshal(map[string]interface{}{
		"key":   keyName,
		"items": b64Items,
	})

	resp, err := doCryptoRequest("POST", "/v1/encrypt-batch", reqBody)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var res struct {
		Items []string `json:"items"`
		Error string   `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, err
	}

	if res.Error != "" {
		if res.Error == "engine_sealed" {
			return nil, ErrEngineSealed
		}
		return nil, errors.New(res.Error)
	}

	return res.Items, nil
}

// DecryptBatch handles multiple decryptions at once
func (c *CryptoService) DecryptBatch(items []string) ([]string, error) {
	reqBody, _ := json.Marshal(map[string]interface{}{
		"items": items,
	})

	resp, err := doCryptoRequest("POST", "/v1/decrypt-batch", reqBody)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var res struct {
		Items []string `json:"items"`
		Error string   `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, err
	}

	if res.Error != "" {
		if res.Error == "engine_sealed" {
			return nil, ErrEngineSealed
		}
		return nil, errors.New(res.Error)
	}

	final := make([]string, len(items))
	for i, b64 := range res.Items {
		if !strings.HasPrefix(items[i], "cse:v1:") {
			final[i] = items[i]
			continue
		}
		if b64 == "" {
			final[i] = "(decryption-failed)"
			continue
		}
		decoded, _ := base64.StdEncoding.DecodeString(b64)
		final[i] = string(decoded)
	}

	return final, nil
}

// GetSovereignStatus returns the current engine state
func (c *CryptoService) GetSovereignStatus() (CryptoStatus, error) {
	resp, err := doCryptoRequest("GET", "/v1/sys/status", nil)
	if err != nil {
		return CryptoStatus{Sealed: true, Engine: "offline"}, err
	}
	defer resp.Body.Close()

	var status CryptoStatus
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return CryptoStatus{Sealed: true, Engine: "error"}, err
	}
	return status, nil
}

// Unseal attempts to unlock the engine with the Master Secret
func (c *CryptoService) Unseal(masterSecret string) error {
	reqBody, _ := json.Marshal(map[string]string{
		"master_secret": masterSecret,
	})

	resp, err := doCryptoRequest("POST", "/v1/sys/unseal", reqBody)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var res struct {
		Success bool   `json:"success"`
		Error   string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return err
	}

	if res.Error != "" {
		return errors.New(res.Error)
	}

	return nil
}

// Rekey rotates the Master Secret
func (c *CryptoService) Rekey(oldSecret, newSecret string) error {
	reqBody, _ := json.Marshal(map[string]string{
		"old_master_secret": oldSecret,
		"new_master_secret": newSecret,
	})

	resp, err := doCryptoRequest("POST", "/v1/sys/rekey", reqBody)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var res struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
		Error   string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return err
	}

	if res.Error != "" {
		return errors.New(res.Error)
	}

	return nil
}

// StoreSecret armazena um segredo criptografado no crypto-engine
// POST /v1/secrets/store/:name
// Body: {"value": "secret_value"}
func (c *CryptoService) StoreSecret(name string, value string) error {
	reqBody, _ := json.Marshal(map[string]string{
		"value": value,
	})

	path := fmt.Sprintf("/v1/secrets/store/%s", name)
	resp, err := doCryptoRequest("POST", path, reqBody)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errRes struct {
			Error string `json:"error"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&errRes); err != nil {
			return fmt.Errorf("crypto-engine returned status %d", resp.StatusCode)
		}
		return errors.New(errRes.Error)
	}

	var res struct {
		Success bool `json:"success"`
		Stored  bool `json:"stored"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return err
	}

	if !res.Stored {
		return errors.New("failed to store secret")
	}

	return nil
}

// RetrieveSecret recupera um segredo do crypto-engine
// GET /v1/secrets/retrieve/:name
func (c *CryptoService) RetrieveSecret(name string) (string, error) {
	path := fmt.Sprintf("/v1/secrets/retrieve/%s", name)
	resp, err := doCryptoRequest("GET", path, nil)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return "", fmt.Errorf("secret '%s' not found", name)
	}

	if resp.StatusCode != http.StatusOK {
		var errRes struct {
			Error string `json:"error"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&errRes); err != nil {
			return "", fmt.Errorf("crypto-engine returned status %d", resp.StatusCode)
		}
		return "", errors.New(errRes.Error)
	}

	var res struct {
		Value string `json:"value"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return "", err
	}

	return res.Value, nil
}

func doCryptoRequest(method, path string, body []byte) (*http.Response, error) {
	url := fmt.Sprintf("%s%s", config.CryptoEngineURL, path)

	var reqBody io.Reader
	if body != nil {
		reqBody = bytes.NewBuffer(body)
	}

	req, err := http.NewRequest(method, url, reqBody)
	if err != nil {
		return nil, err
	}

	req.Header.Set("X-Crypto-Auth", config.InternalCtrlSecret)
	req.Header.Set("Content-Type", "application/json")

	resp, err := cryptoClient.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode == 503 {
		resp.Body.Close()
		return nil, ErrEngineSealed
	}

	if resp.StatusCode >= 400 && resp.StatusCode != 503 {
		defer resp.Body.Close()
		data, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("Crypto Engine HTTP Error %d: %s", resp.StatusCode, string(data))
	}

	return resp, nil
}
