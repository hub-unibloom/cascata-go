package utils

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strings"
	"time"

	"golang.org/x/crypto/hkdf"
)

const (
	ReplayWindowMs = 90 * 1000 // ±90 seconds
)

type HandshakeSession struct {
	SessionID       string `json:"sessionId"`
	ServerPublicKey  string `json:"serverPublicKey"`  // Base64
	ServerPrivateKey string `json:"serverPrivateKey"` // Base64
	CreatedAt        int64  `json:"createdAt"`
}

type EncryptedPayload struct {
	SessionID       string `json:"sessionId"`
	ClientPublicKey  string `json:"clientPublicKey"` // Base64
	Iv              string `json:"iv"`              // Base64
	AuthTag         string `json:"authTag"`         // Base64
	Ciphertext      string `json:"ciphertext"`      // Base64
	Timestamp       int64  `json:"timestamp"`       // Unix ms
}

type EncryptedResponse struct {
	Iv         string `json:"iv"`
	AuthTag    string `json:"authTag"`
	Ciphertext string `json:"ciphertext"`
}

// GetServerFingerprint returns a unique server fingerprint
func GetServerFingerprint() string {
	secret := os.Getenv("INTERNAL_CTRL_SECRET")
	if secret == "" {
		secret = "fallback-cascata-secret-fingerprint"
	}
	hash := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(hash[:])[:16]
}

// CreateHandshakeSession generates an ephemeral P-256 ECDH keypair
func CreateHandshakeSession() (*HandshakeSession, error) {
	key, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}

	sessionID := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, sessionID); err != nil {
		return nil, err
	}

	pubKeyB64 := base64.StdEncoding.EncodeToString(key.PublicKey().Bytes())
	privKeyB64 := base64.StdEncoding.EncodeToString(key.Bytes())

	return &HandshakeSession{
		SessionID:       hex.EncodeToString(sessionID),
		ServerPublicKey:  pubKeyB64,
		ServerPrivateKey: privKeyB64,
		CreatedAt:        time.Now().UnixMilli(),
	}, nil
}

// DeriveSharedKey derives an AES-256 shared key using ECDH P-256 and HKDF
func DeriveSharedKey(serverPrivateKeyB64, clientPublicKeyB64 string) ([]byte, error) {
	serverPrivBytes, err := base64.StdEncoding.DecodeString(serverPrivateKeyB64)
	if err != nil {
		return nil, err
	}
	serverKey, err := ecdh.P256().NewPrivateKey(serverPrivBytes)
	if err != nil {
		return nil, err
	}

	clientPubBytes, err := base64.StdEncoding.DecodeString(clientPublicKeyB64)
	if err != nil {
		return nil, err
	}
	clientKey, err := ecdh.P256().NewPublicKey(clientPubBytes)
	if err != nil {
		return nil, err
	}

	sharedSecret, err := serverKey.ECDH(clientKey)
	if err != nil {
		return nil, err
	}

	fingerprint := GetServerFingerprint()
	info := []byte(fmt.Sprintf("cascata-v2-%s", fingerprint))

	// HKDF with SHA-256
	hk := hkdf.New(sha256.New, sharedSecret, nil, info)
	key := make([]byte, 32) // 256 bits for AES-256
	if _, err := io.ReadFull(hk, key); err != nil {
		return nil, err
	}

	return key, nil
}

// DecryptPayload decrypts a frontend payload with anti-replay and GCM integrity
func DecryptPayload(encrypted EncryptedPayload, sharedKey []byte) (map[string]interface{}, error) {
	now := time.Now().UnixMilli()
	diff := int64(math.Abs(float64(now - encrypted.Timestamp)))
	if diff > ReplayWindowMs {
		return nil, fmt.Errorf("payload expired or replay detected. Drift: %dms", diff)
	}

	iv, err := base64.StdEncoding.DecodeString(encrypted.Iv)
	if err != nil {
		return nil, err
	}

	authTag, err := base64.StdEncoding.DecodeString(encrypted.AuthTag)
	if err != nil {
		return nil, err
	}

	ciphertext, err := base64.StdEncoding.DecodeString(encrypted.Ciphertext)
	if err != nil {
		return nil, err
	}

	block, err := aes.NewCipher(sharedKey)
	if err != nil {
		return nil, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	// Reconstruct Go-standard ciphertext (data + tag)
	fullCiphertext := append(ciphertext, authTag...)

	plaintext, err := gcm.Open(nil, iv, fullCiphertext, nil)
	if err != nil {
		return nil, errors.New("decryption failed: integrity check or key mismatch")
	}

	var result map[string]interface{}
	if err := json.Unmarshal(plaintext, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal decrypted JSON: %v", err)
	}

	return result, nil
}

// EncryptResponse encrypts a server response for the frontend
func EncryptResponse(payload map[string]interface{}, sharedKey []byte) (*EncryptedResponse, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	block, err := aes.NewCipher(sharedKey)
	if err != nil {
		return nil, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	iv := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, iv); err != nil {
		return nil, err
	}

	sealed := gcm.Seal(nil, iv, data, nil)
	tagSize := gcm.Overhead()
	ciphertext := sealed[:len(sealed)-tagSize]
	authTag := sealed[len(sealed)-tagSize:]

	return &EncryptedResponse{
		Iv:         base64.StdEncoding.EncodeToString(iv),
		AuthTag:    base64.StdEncoding.EncodeToString(authTag),
		Ciphertext: base64.StdEncoding.EncodeToString(ciphertext),
	}, nil
}

// ValidateTOTP validates a 6-digit TOTP code (RFC 6238)
func ValidateTOTP(secretB32, userCode string) bool {
	if secretB32 == "" || userCode == "" {
		return false
	}
	if len(userCode) != 6 {
		return false
	}

	// Clean base32 input (remove padding and uppercase)
	secretB32 = strings.TrimRight(strings.ToUpper(secretB32), "=")
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secretB32)
	if err != nil {
		return false
	}

	now := time.Now().Unix()
	// Check window ±1 step (30s)
	for offset := -1; offset <= 1; offset++ {
		counter := uint64((now + int64(offset*30)) / 30)
		if Hotp(key, counter) == userCode {
			return true
		}
	}

	return false
}

// Hotp implements HMAC-based One-Time Password (RFC 4226)
func Hotp(key []byte, counter uint64) string {
	msg := make([]byte, 8)
	binary.BigEndian.PutUint64(msg, counter)

	mac := hmac.New(sha1.New, key)
	mac.Write(msg)
	h := mac.Sum(nil)

	offset := h[len(h)-1] & 0x0f
	code := (uint32(h[offset]&0x7f) << 24) |
		(uint32(h[offset+1]&0xff) << 16) |
		(uint32(h[offset+2]&0xff) << 8) |
		(uint32(h[offset+3] & 0xff))

	code = code % 1_000_000
	return fmt.Sprintf("%06d", code)
}
