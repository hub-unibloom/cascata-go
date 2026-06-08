package api

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/hub-unibloom/cascata/crypto-engine/internal/crypto"
	"github.com/hub-unibloom/cascata/crypto-engine/internal/keystore"
)

type Router struct {
	Manager        *keystore.Manager
	InternalSecret string
	Tarpit         *crypto.Tarpit
	mux            *http.ServeMux
}

func NewRouter(manager *keystore.Manager, internalSecret string, tarpit *crypto.Tarpit) *Router {
	r := &Router{
		Manager:        manager,
		InternalSecret: internalSecret,
		Tarpit:         tarpit,
		mux:            http.NewServeMux(),
	}

	r.mux.HandleFunc("/v1/encrypt", r.handleEncrypt)
	r.mux.HandleFunc("/v1/decrypt", r.handleDecrypt)
	r.mux.HandleFunc("/v1/encrypt-batch", r.handleEncryptBatch)
	r.mux.HandleFunc("/v1/decrypt-batch", r.handleDecryptBatch)
	r.mux.HandleFunc("/v1/keys/rotate", r.handleRotateKey)
	r.mux.HandleFunc("/v1/secrets/store/", r.handleStoreSecret)
	r.mux.HandleFunc("/v1/secrets/retrieve/", r.handleRetrieveSecret)
	r.mux.HandleFunc("/v1/sys/status", r.handleStatus)
	r.mux.HandleFunc("/v1/sys/unseal", r.handleUnseal)
	r.mux.HandleFunc("/v1/sys/rekey", r.handleRekey)
	r.mux.HandleFunc("/v1/sys/fingerprint", r.handleFingerprint)
	r.mux.HandleFunc("/v1/health", r.handleHealth)
	r.mux.HandleFunc("/v1/ready", r.handleReady)

	return r
}

type EncryptRequest struct {
	Key       string   `json:"key"`
	Plaintext string   `json:"plaintext"` // Base64
	Items     []string `json:"items,omitempty"`
}

type DecryptRequest struct {
	Ciphertext string   `json:"ciphertext"`
	Items      []string `json:"items,omitempty"`
}

func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	// Middleware: Auth
	// Handshake, Health e Ready são públicos para diagnósticos internos
	isPublic := req.URL.Path == "/v1/health" || req.URL.Path == "/v1/ready" || req.URL.Path == "/v1/handshake"
	if !isPublic && req.Header.Get("X-Crypto-Auth") != r.InternalSecret {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	r.mux.ServeHTTP(w, req)
}

func (r *Router) handleEncrypt(w http.ResponseWriter, req *http.Request) {
	var body EncryptRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		http.Error(w, "Invalid body", http.StatusBadRequest)
		return
	}

	if r.Manager.IsSealed() {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{"error": "engine_sealed"})
		return
	}

	plaintext, err := base64.StdEncoding.DecodeString(body.Plaintext)
	if err != nil {
		http.Error(w, "Invalid base64 plaintext", http.StatusBadRequest)
		return
	}

	keyName := body.Key
	version := r.Manager.GetLatestVersion(keyName)
	if version == 0 {
		version, _ = r.Manager.GenerateKey(keyName)
	}

	key, err := r.Manager.GetKey(keyName, version)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	ciphertext, err := crypto.EncryptAESGCM(plaintext, key)
	if err != nil {
		http.Error(w, "Encryption failed", http.StatusInternalServerError)
		return
	}

	final := fmt.Sprintf("cse:v1:%s:%d:%s", keyName, version, base64.StdEncoding.EncodeToString(ciphertext))
	json.NewEncoder(w).Encode(map[string]string{"ciphertext": final})
}

func (r *Router) handleDecrypt(w http.ResponseWriter, req *http.Request) {
	var body DecryptRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		http.Error(w, "Invalid body", http.StatusBadRequest)
		return
	}

	if r.Manager.IsSealed() {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{"error": "engine_sealed"})
		return
	}

	r.Tarpit.RecordAndDelay()

	plaintext, err := r.decryptOne(body.Ciphertext)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(map[string]string{"plaintext": base64.StdEncoding.EncodeToString(plaintext)})
}

func (r *Router) handleEncryptBatch(w http.ResponseWriter, req *http.Request) {
	var body EncryptRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		http.Error(w, "Invalid body", http.StatusBadRequest)
		return
	}

	if r.Manager.IsSealed() {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{"error": "engine_sealed"})
		return
	}
	
	keyName := body.Key
	version := r.Manager.GetLatestVersion(keyName)
	if version == 0 {
		var err error
		version, err = r.Manager.GenerateKey(keyName)
		if err != nil {
			http.Error(w, "Key generation failed", http.StatusInternalServerError)
			return
		}
	}
	key, err := r.Manager.GetKey(keyName, version)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	results := make([]string, len(body.Items))
	for i, ptBase64 := range body.Items {
		pt, err := base64.StdEncoding.DecodeString(ptBase64)
		if err != nil {
			results[i] = ""
			continue
		}
		ct, err := crypto.EncryptAESGCM(pt, key)
		if err != nil {
			results[i] = ""
			continue
		}
		results[i] = fmt.Sprintf("cse:v1:%s:%d:%s", keyName, version, base64.StdEncoding.EncodeToString(ct))
	}

	json.NewEncoder(w).Encode(map[string][]string{"items": results})
}

func (r *Router) handleDecryptBatch(w http.ResponseWriter, req *http.Request) {
	var body DecryptRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		http.Error(w, "Invalid body", http.StatusBadRequest)
		return
	}

	if r.Manager.IsSealed() {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{"error": "engine_sealed"})
		return
	}

	for range body.Items {
		r.Tarpit.RecordAndDelay()
	}

	results := make([]string, len(body.Items))
	for i, ct := range body.Items {
		pt, err := r.decryptOne(ct)
		if err != nil {
			results[i] = ""
		} else {
			results[i] = base64.StdEncoding.EncodeToString(pt)
		}
	}
	json.NewEncoder(w).Encode(map[string][]string{"items": results})
}

func (r *Router) decryptOne(ctStr string) ([]byte, error) {
	parts := strings.Split(ctStr, ":")
	if len(parts) != 5 || parts[0] != "cse" || parts[1] != "v1" {
		return nil, fmt.Errorf("invalid ciphertext format")
	}

	keyName := parts[2]
	var version int
	fmt.Sscanf(parts[3], "%d", &version)
	
	ctRaw, err := base64.StdEncoding.DecodeString(parts[4])
	if err != nil { return nil, err }

	key, err := r.Manager.GetKey(keyName, version)
	if err != nil { return nil, err }

	return crypto.DecryptAESGCM(ctRaw, key)
}

func (r *Router) handleHealth(w http.ResponseWriter, req *http.Request) {
	status := "ok"
	if r.Manager == nil {
		status = "error"
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  status,
		"sealed":  r.Manager.IsSealed(),
		"version": "1.0.0",
		"engine":  "go-cse-v1",
	})
}

func (r *Router) handleReady(w http.ResponseWriter, req *http.Request) {
	if r.Manager == nil || r.Manager.IsSealed() {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{"status": "sealed", "message": "Crypto Engine is sealed"})
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ready"})
}

type RotateKeyRequest struct {
	Key string `json:"key"`
}

func (r *Router) handleRotateKey(w http.ResponseWriter, req *http.Request) {
	var body RotateKeyRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		http.Error(w, "Invalid body", http.StatusBadRequest)
		return
	}

	if r.Manager.IsSealed() {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{"error": "engine_sealed"})
		return
	}

	newVersion, err := r.Manager.GenerateKey(body.Key)
	if err != nil {
		http.Error(w, "Key rotation failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(map[string]int{"new_version": newVersion})
}

func (r *Router) handleStatus(w http.ResponseWriter, req *http.Request) {
	json.NewEncoder(w).Encode(map[string]interface{}{
		"sealed":  r.Manager.IsSealed(),
		"version": "1.0.0",
		"engine":  "go-cse-v1-sovereign",
	})
}

type UnsealRequest struct {
	MasterSecret string `json:"master_secret"`
}

func (r *Router) handleUnseal(w http.ResponseWriter, req *http.Request) {
	var body UnsealRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		http.Error(w, "Invalid body", http.StatusBadRequest)
		return
	}

	// Se já estiver desbloqueado, retornamos sucesso (idempotência) para facilitar boots redundantes
	if !r.Manager.IsSealed() {
		json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "already_unsealed": true})
		return
	}

	err := r.Manager.Unlock(body.MasterSecret)
	if err != nil {
		// Se for erro de 'já desbloqueado' (caso de race condition), tratamos como sucesso
		if strings.Contains(err.Error(), "already unsealed") {
			json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "already_unsealed": true})
			return
		}

		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error(), "code": "UNSEAL_ERROR"})
		return
	}

	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

type RekeyRequest struct {
	OldMasterSecret string `json:"old_master_secret"`
	NewMasterSecret string `json:"new_master_secret"`
}

func (r *Router) handleRekey(w http.ResponseWriter, req *http.Request) {
	var body RekeyRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		http.Error(w, "Invalid body", http.StatusBadRequest)
		return
	}

	if body.OldMasterSecret == "" || body.NewMasterSecret == "" {
		http.Error(w, "Old and new secrets are required", http.StatusBadRequest)
		return
	}

	err := r.Manager.Rekey(body.OldMasterSecret, body.NewMasterSecret)
	if err != nil {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	json.NewEncoder(w).Encode(map[string]string{"success": "true", "message": "Master Secret rotated successfully. Store re-encrypted."})
}

func (r *Router) handleFingerprint(w http.ResponseWriter, req *http.Request) {
	if r.Manager.IsSealed() {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{"error": "engine_sealed"})
		return
	}

	// O Fingerprint é o Checksum do KeyStore (estável enquanto a Master Secret não mudar)
	fingerprint := r.Manager.GetChecksum()
	json.NewEncoder(w).Encode(map[string]string{"fingerprint": fingerprint})
}

// StoreSecretRequest - Estrutura para armazenar segredo
// Exemplo: {"value": "minio123"} -> criptografado e armazenado no KeyStore
// O "name" vem do path da URL (ex: /v1/secrets/store/minio_user)
type StoreSecretRequest struct {
	Value string `json:"value"` // Valor em plain text (será criptografado pelo engine)
}

// handleStoreSecret - Armazena um segredo criptografado no KeyStore
// POST /v1/secrets/store/:name
// Header: X-Crypto-Auth: <INTERNAL_CTRL_SECRET>
// Body: {"value": "secret_value"}
func (r *Router) handleStoreSecret(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(map[string]string{"error": "method_not_allowed"})
		return
	}

	if r.Manager.IsSealed() {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{"error": "engine_sealed"})
		return
	}

	// Extrai o nome do segredo do path: /v1/secrets/store/:name
	// Remove prefixo da URL
	path := strings.TrimPrefix(req.URL.Path, "/v1/secrets/store/")
	if path == "" {
		http.Error(w, `{"error":"secret name required in path"}`, http.StatusBadRequest)
		return
	}

	// Sanitização do nome (evita caracteres perigosos)
	secretName := strings.TrimSpace(path)
	if strings.ContainsAny(secretName, "/\\..$#@!%*") {
		http.Error(w, `{"error":"invalid secret name"}`, http.StatusBadRequest)
		return
	}

	var body StoreSecretRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"invalid json body"}`, http.StatusBadRequest)
		return
	}

	if body.Value == "" {
		http.Error(w, `{"error":"value cannot be empty"}`, http.StatusBadRequest)
		return
	}

	// Criptografa o valor usando a chave "secrets" do KeyStore
	// Isso garante que o valor fica protegido pela Master Secret
	keyName := "secrets"
	version := r.Manager.GetLatestVersion(keyName)
	if version == 0 {
		var err error
		version, err = r.Manager.GenerateKey(keyName)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": "failed to generate secrets key: " + err.Error()})
			return
		}
	}

	key, err := r.Manager.GetKey(keyName, version)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "failed to get encryption key: " + err.Error()})
		return
	}

	// Criptografa o valor
	plaintext := []byte(body.Value)
	ciphertext, err := crypto.EncryptAESGCM(plaintext, key)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "encryption failed: " + err.Error()})
		return
	}

	// Armazena no KeyStore usando o nome como chave
	// O valor criptografado é armazenado no formato: cse:v1:secrets:<version>:<base64_ciphertext>
	encryptedValue := fmt.Sprintf("cse:v1:%s:%d:%s", keyName, version, base64.StdEncoding.EncodeToString(ciphertext))

	// Usa o SetSecret do Manager para armazenar o segredo criptografado
	err = r.Manager.SetSecret(secretName, encryptedValue)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "failed to store secret: " + err.Error()})
		return
	}

	// Retorna sucesso
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"name":    secretName,
		"stored":  true,
	})
}

// RetrieveSecretResponse - Resposta com o segredo descriptografado
type RetrieveSecretResponse struct {
	Value string `json:"value"`
}

// handleRetrieveSecret - Recupera um segredo do KeyStore
// GET /v1/secrets/retrieve/:name
// Header: X-Crypto-Auth: <INTERNAL_CTRL_SECRET>
func (r *Router) handleRetrieveSecret(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(map[string]string{"error": "method_not_allowed"})
		return
	}

	if r.Manager.IsSealed() {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{"error": "engine_sealed"})
		return
	}

	// Aplica delay de tarpit para evitar brute force
	r.Tarpit.RecordAndDelay()

	// Extrai o nome do segredo do path: /v1/secrets/retrieve/:name
	path := strings.TrimPrefix(req.URL.Path, "/v1/secrets/retrieve/")
	if path == "" {
		http.Error(w, `{"error":"secret name required in path"}`, http.StatusBadRequest)
		return
	}

	secretName := strings.TrimSpace(path)
	if strings.ContainsAny(secretName, "/\\..$#@!%*") {
		http.Error(w, `{"error":"invalid secret name"}`, http.StatusBadRequest)
		return
	}

	// Recupera o segredo criptografado do KeyStore
	encryptedValue, _, err := r.Manager.GetSecret(secretName)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	// Descriptografa o valor
	plaintext, err := r.decryptOne(encryptedValue)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "failed to decrypt secret: " + err.Error()})
		return
	}

	// Retorna o valor descriptografado
	json.NewEncoder(w).Encode(RetrieveSecretResponse{
		Value: string(plaintext),
	})
}
