package controllers

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"cascata-backend/internal/middleware"
	"cascata-backend/internal/services"
	"cascata-backend/internal/types"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/hkdf"
)

type AdminController struct {
	AuthSvc   services.AuthService
	CryptoSvc services.CryptoService
	DbSvc     services.DatabaseService
	CertSvc   *services.CertificateService
}

// Handshake starts an ephemeral ECDH X25519 session for encrypted login
func (c *AdminController) Handshake(w http.ResponseWriter, r *http.Request) {
	// 1. Gerar par de chaves ECDH P-256 efêmero
	curve := ecdh.P256()
	privateKey, err := curve.GenerateKey(rand.Reader)
	if err != nil {
		http.Error(w, `{"error":"Key generation failed"}`, 500)
		return
	}

	// 2. Persistir chave privada no Dragonfly (TTL 5 min)
	sessionId := uuid.New().String()
	privBytes := privateKey.Bytes()
	
	err = services.GetDragonfly().Set(r.Context(), "handshake:"+sessionId, hex.EncodeToString(privBytes), 5*time.Minute).Err()
	if err != nil {
		http.Error(w, `{"error":"Session persistence failed"}`, 500)
		return
	}

	// 3. Obter Fingerprint do Crypto Engine (Sinergia Sistêmica)
	fingerprint := "static-cascata-v2"
	
	// Tenta buscar o fingerprint real do motor
	resp, err := http.Get(os.Getenv("CRYPTO_ENGINE_URL") + "/v1/sys/fingerprint")
	if err == nil {
		defer resp.Body.Close()
		var res struct { Fingerprint string `json:"fingerprint"` }
		json.NewDecoder(resp.Body).Decode(&res)
		if res.Fingerprint != "" {
			fingerprint = res.Fingerprint
		}
	}

	// 4. Exportar chave pública em formato SPKI para compatibilidade com Web Crypto API
	// O SubtleCrypto do navegador espera chaves no formato SPKI (Subject Public Key Info)
	pubBytes := privateKey.PublicKey().Bytes()
	
	// Converter de ecdh.PublicKey para ecdsa.PublicKey para marshaling SPKI
	ellipticCurve := elliptic.P256()
	x, y := elliptic.Unmarshal(ellipticCurve, pubBytes)
	ecdsaPub := &ecdsa.PublicKey{
		Curve: ellipticCurve,
		X:     x,
		Y:     y,
	}
	
	// Marshal para formato SPKI (PKIX)
	spkiBytes, err := x509.MarshalPKIXPublicKey(ecdsaPub)
	if err != nil {
		http.Error(w, `{"error":"Public key export failed"}`, 500)
		return
	}
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"sessionId":         sessionId,
		"serverPublicKey":   base64.StdEncoding.EncodeToString(spkiBytes),
		"serverFingerprint": fingerprint,
	})
}

func (c *AdminController) Login(w http.ResponseWriter, r *http.Request) {
	// 0. Variáveis para controle de sessão E2EE
	var e2eeEnabled bool
	var aesKey []byte
	
	// 1. Ler o corpo da requisição brutos para detecção dinâmica
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, `{"error":"Invalid request"}`, 400)
		return
	}

	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		OTPCode  string `json:"otp_code"`
		// Campos E2EE
		V               string `json:"v"`
		SessionID       string `json:"sessionId"`
		ClientPublicKey string `json:"clientPublicKey"`
		Iv              string `json:"iv"`
		AuthTag         string `json:"authTag"`
		Ciphertext      string `json:"ciphertext"`
	}

	if err := json.Unmarshal(bodyBytes, &body); err != nil {
		http.Error(w, `{"error":"Invalid payload structure"}`, 400)
		return
	}

	// 2. DETECÇÃO DE PROTOCOLO (SE 'V' existir, assume E2EE)
	if body.V != "" {
		// PROTOCOLO CRIPTOGRAFADO (ECDH P-256 + AES-GCM)
		// Recuperar chave privada do Dragonfly
		privHex, err := services.GetDragonfly().Get(r.Context(), "handshake:"+body.SessionID).Result()
		if err != nil {
			http.Error(w, `{"error":"Session expired or invalid (E2EE)"}`, 401)
			return
		}
		privBytes, _ := hex.DecodeString(privHex)
		serverPriv, _ := ecdh.P256().NewPrivateKey(privBytes)

		// Importar chave pública do cliente (formato SPKI do Web Crypto)
		clientPubBytes, _ := base64.StdEncoding.DecodeString(body.ClientPublicKey)
		
		// Parse SPKI para ECDSA public key
		parsedPub, err := x509.ParsePKIXPublicKey(clientPubBytes)
		if err != nil {
			log.Printf("[AdminLogin] Failed to parse client public key (SPKI): %v", err)
			http.Error(w, `{"error":"Invalid client public key format"}`, 400)
			return
		}
		
		// Converter para formato raw (X9.62 uncompressed) que o ecdh aceita
		var clientPub *ecdh.PublicKey
		if ecdsaPub, ok := parsedPub.(*ecdsa.PublicKey); ok {
			ellipticCurve := elliptic.P256()
			if !ecdsaPub.Curve.IsOnCurve(ecdsaPub.X, ecdsaPub.Y) {
				log.Printf("[AdminLogin] Client public key not on P-256 curve")
				http.Error(w, `{"error":"Invalid curve"}`, 400)
				return
			}
			// Marshal para X9.62 uncompressed: 0x04 || X || Y (65 bytes total)
			rawPub := elliptic.Marshal(ellipticCurve, ecdsaPub.X, ecdsaPub.Y)
			clientPub, err = ecdh.P256().NewPublicKey(rawPub)
			if err != nil {
				log.Printf("[AdminLogin] Failed to create ECDH public key: %v", err)
				http.Error(w, `{"error":"Invalid public key"}`, 400)
				return
			}
		} else {
			log.Printf("[AdminLogin] Parsed key is not ECDSA")
			http.Error(w, `{"error":"Unsupported key type"}`, 400)
			return
		}

		// Derivar Shared Key (ECDH)
		sharedSecret, _ := serverPriv.ECDH(clientPub)

		// HKDF Fingerprint Anchor
		fingerprint := "static-cascata-v2"
		resp, err := http.Get(os.Getenv("CRYPTO_ENGINE_URL") + "/v1/sys/fingerprint")
		if err == nil {
			defer resp.Body.Close()
			var res struct { Fingerprint string `json:"fingerprint"` }
			json.NewDecoder(resp.Body).Decode(&res)
			if res.Fingerprint != "" { fingerprint = res.Fingerprint }
		}

		hkdfReader := hkdf.New(sha256.New, sharedSecret, nil, []byte("cascata-v2-"+fingerprint))
		aesKey = make([]byte, 32) 
		io.ReadFull(hkdfReader, aesKey)
		e2eeEnabled = true  // Marcar que E2EE está ativo para cifrar a resposta

		// Decifrar Payload (AES-256-GCM)
		iv, _ := base64.StdEncoding.DecodeString(body.Iv)
		authTag, _ := base64.StdEncoding.DecodeString(body.AuthTag)
		ciphertext, _ := base64.StdEncoding.DecodeString(body.Ciphertext)
		
		fullCiphertext := append(ciphertext, authTag...)
		block, _ := aes.NewCipher(aesKey)
		aesGCM, _ := cipher.NewGCM(block)
		
		plaintext, err := aesGCM.Open(nil, iv, fullCiphertext, nil)
		if err != nil {
			log.Printf("[AdminLogin] E2EE Decryption Failed (Fingerprint mismatch?): %v", err)
			http.Error(w, `{"error":"Falha na integridade da sessão segura. Tente recarregar a página."}`, 401)
			return
		}

		// Sobrescrever body com o conteúdo decifrado
		var decrypted struct {
			Email    string `json:"email"`
			Password string `json:"password"`
			OTPCode  string `json:"otp_code"`
		}
		if err := json.Unmarshal(plaintext, &decrypted); err != nil {
			log.Printf("[AdminLogin] JSON Unmarshal Error from decrypted payload: %v", err)
			http.Error(w, `{"error":"Formato de payload inválido após decifragem"}`, 401)
			return
		}
		
		body.Email = strings.TrimSpace(decrypted.Email)
		body.Password = strings.TrimSpace(decrypted.Password)
		body.OTPCode = strings.TrimSpace(decrypted.OTPCode)
	} else {
		// Modo Standard: Garantir Trim para evitar espaços de copiar/colar
		body.Email = strings.TrimSpace(body.Email)
		body.Password = strings.TrimSpace(body.Password)
		body.OTPCode = strings.TrimSpace(body.OTPCode)
	}

	// 3. VALIDAÇÃO DE CREDENCIAIS (Texto Puro ou Decifrado)
	if body.Email == "" || body.Password == "" {
		http.Error(w, `{"error":"Email and Password required"}`, 401)
		return
	}

	// DIAGNÓSTICO DE SINERGIA: Prove que o \ no JSON vira \ literal no Go
	firstChar := ""
	if len(body.Password) > 0 {
		firstChar = fmt.Sprintf("%02x", body.Password[0])
	}
	log.Printf("[Sovereign:Auth] Login attempt for %s | PassLen: %d | FirstCharHex: %s", 
		body.Email, len(body.Password), firstChar)

	var admin struct {
		ID           string
		PasswordHash string
	}

	err = services.SystemPool.QueryRow(r.Context(), "SELECT id, password_hash FROM system.admin_users WHERE email = $1", body.Email).
		Scan(&admin.ID, &admin.PasswordHash)
	if err != nil {
		http.Error(w, `{"error":"Invalid credentials"}`, 401)
		return
	}

	if !c.AuthSvc.ComparePassword(admin.PasswordHash, body.Password) {
		http.Error(w, `{"error":"Invalid credentials"}`, 401)
		return
	}

	// 2. Multi-Factor (OTP) Logic
	// Se o sistema exige OTP para o admin (conforme .env), validamos aqui.
	if os.Getenv("ADMIN_OTP_ENABLED") == "true" {
		if body.OTPCode == "" {
			http.Error(w, `{"error":"OTP required","otp_required":true}`, 401)
			return
		}
		// verify logic with c.AuthSvc.VerifyTOTP(...) if needed
	}

	// 4. Issue Token
	token, _ := c.AuthSvc.CreateAdminToken(admin.ID, os.Getenv("SYSTEM_JWT_SECRET"))

	// Set HttpOnly Cookie
	http.SetCookie(w, &http.Cookie{
		Name:     "cascata_session",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   true, // Frontend exige HTTPS para SubtleCrypto, então Secure é obrigatório
		SameSite: http.SameSiteStrictMode,
		MaxAge:   3600 * 12,
	})

	// Preparar resposta
	responseData := map[string]interface{}{
		"success": true,
		"token":   token,
	}

	// Se E2EE estava habilitado, cifrar a resposta
	if e2eeEnabled && aesKey != nil {
		responseJSON, _ := json.Marshal(responseData)
		
		// Gerar IV aleatório
		iv := make([]byte, 12)
		rand.Read(iv)
		
		// Cifrar com AES-256-GCM
		block, _ := aes.NewCipher(aesKey)
		aesGCM, _ := cipher.NewGCM(block)
		encrypted := aesGCM.Seal(nil, iv, responseJSON, nil)
		
		// Separar ciphertext e authTag (últimos 16 bytes)
		ciphertext := encrypted[:len(encrypted)-16]
		authTag := encrypted[len(encrypted)-16:]
		
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"iv":         base64.StdEncoding.EncodeToString(iv),
			"authTag":    base64.StdEncoding.EncodeToString(authTag),
			"ciphertext": base64.StdEncoding.EncodeToString(ciphertext),
		})
	} else {
		// Resposta em texto puro (fallback/legado)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(responseData)
	}
}

func (c *AdminController) Verify(w http.ResponseWriter, r *http.Request) {
	// Simple session verification parity
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"success":true}`))
}

func (c *AdminController) SovereignStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	status, err := c.CryptoSvc.GetSovereignStatus()
	if err != nil {
		// Engine offline ou inacessível — retorna JSON estruturado, nunca text/plain
		// O frontend distingue ENGINE_OFFLINE (container down) de ENGINE_SEALED (precisa unseal)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"sealed": true,
			"engine": "offline",
			"error":  "Crypto Engine inacessível — verifique se o container está rodando",
			"code":   "ENGINE_OFFLINE",
		})
		return
	}
	json.NewEncoder(w).Encode(status)
}

// Unseal injects the Master Secret into the RAM-only memory of the driver.
// This endpoint is intentionally PUBLIC (no JWT required) — it's the bootstrap
// security mechanism for Sovereign Elite mode after server reboots.
// Security is enforced by the crypto-engine itself (Argon2id KEK derivation).
func (c *AdminController) Unseal(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var body struct {
		MasterSecret string `json:"master_secret"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		w.WriteHeader(400)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "Corpo JSON inválido",
			"code":  "BAD_REQUEST",
		})
		return
	}

	// Validação de entrada: recusar antes de bater no crypto-engine
	if len(strings.TrimSpace(body.MasterSecret)) < 64 {
		w.WriteHeader(400)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "master_secret inválida: mínimo 64 caracteres hexadecimais",
			"code":  "INVALID_MASTER_SECRET",
		})
		return
	}

	if err := c.CryptoSvc.Unseal(body.MasterSecret); err != nil {
		w.WriteHeader(401)
		json.NewEncoder(w).Encode(map[string]string{
			"error": err.Error(),
			"code":  "UNSEAL_FAILED",
		})
		return
	}

	log.Println("[Sovereign] Engine desbloqueada com sucesso via unseal manual.")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Engine unsealed successfully",
	})
}

func (c *AdminController) GetSystemSettings(w http.ResponseWriter, r *http.Request) {
	var settingsStr string
	err := services.SystemPool.QueryRow(r.Context(), "SELECT settings FROM system.ui_settings WHERE project_slug = '_system_root_' AND table_name = 'system_config'").Scan(&settingsStr)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{}`))
		return
	}

	// Mascarar a API Key da IA antes de retornar ao frontend
	var settings map[string]interface{}
	if err := json.Unmarshal([]byte(settingsStr), &settings); err == nil {
		if aiConfigRaw, ok := settings["ai_config"]; ok {
			if aiConfig, ok := aiConfigRaw.(map[string]interface{}); ok {
				if apiKey, ok := aiConfig["api_key"].(string); ok && apiKey != "" {
					if len(apiKey) > 10 {
						aiConfig["api_key"] = apiKey[:7] + "*******************************" + apiKey[len(apiKey)-5:]
					} else {
						aiConfig["api_key"] = "********"
					}
				}
				if sttApiKey, ok := aiConfig["stt_api_key"].(string); ok && sttApiKey != "" {
					if len(sttApiKey) > 10 {
						aiConfig["stt_api_key"] = sttApiKey[:7] + "*******************************" + sttApiKey[len(sttApiKey)-5:]
					} else {
						aiConfig["stt_api_key"] = "********"
					}
				}
				if ttsApiKey, ok := aiConfig["tts_api_key"].(string); ok && ttsApiKey != "" {
					if len(ttsApiKey) > 10 {
						aiConfig["tts_api_key"] = ttsApiKey[:7] + "*******************************" + ttsApiKey[len(ttsApiKey)-5:]
					} else {
						aiConfig["tts_api_key"] = "********"
					}
				}
			}
		}
		if maskedBytes, err := json.Marshal(settings); err == nil {
			settingsStr = string(maskedBytes)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(settingsStr))
}

func (c *AdminController) UpdateSystemSettings(w http.ResponseWriter, r *http.Request) {
	var body map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"Invalid JSON payload"}`, 400)
		return
	}

	// Evitar sobrescrever a chave real com a máscara vinda do frontend
	if aiConfigRaw, ok := body["ai_config"]; ok {
		if aiConfig, ok := aiConfigRaw.(map[string]interface{}); ok {
			needsRestore := false
			apiKey, hasKey := aiConfig["api_key"].(string)
			if hasKey && strings.Contains(apiKey, "***") { needsRestore = true }
			
			sttApiKey, hasStt := aiConfig["stt_api_key"].(string)
			if hasStt && strings.Contains(sttApiKey, "***") { needsRestore = true }
			
			ttsApiKey, hasTts := aiConfig["tts_api_key"].(string)
			if hasTts && strings.Contains(ttsApiKey, "***") { needsRestore = true }

			if needsRestore {
				var existingSettingsStr string
				if err := services.SystemPool.QueryRow(r.Context(), "SELECT settings FROM system.ui_settings WHERE project_slug = '_system_root_' AND table_name = 'system_config'").Scan(&existingSettingsStr); err == nil {
					var existing map[string]interface{}
					if json.Unmarshal([]byte(existingSettingsStr), &existing) == nil {
						if exAiRaw, ok := existing["ai_config"]; ok {
							if exAi, ok := exAiRaw.(map[string]interface{}); ok {
								if hasKey && strings.Contains(apiKey, "***") {
									if exKey, ok := exAi["api_key"].(string); ok {
										aiConfig["api_key"] = exKey // Restaura a chave real
									}
								}
								if hasStt && strings.Contains(sttApiKey, "***") {
									if exSttKey, ok := exAi["stt_api_key"].(string); ok {
										aiConfig["stt_api_key"] = exSttKey // Restaura a chave real
									}
								}
								if hasTts && strings.Contains(ttsApiKey, "***") {
									if exTtsKey, ok := exAi["tts_api_key"].(string); ok {
										aiConfig["tts_api_key"] = exTtsKey // Restaura a chave real
									}
								}
							}
						}
					}
				}
			}
		}
	}

	// Normalizar domínio se presente (converter IDN para Punycode)
	if domain, ok := body["domain"].(string); ok && domain != "" {
		normalizedDomain, err := services.NormalizeDomain(domain)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"Invalid domain: %s"}`, err.Error()), 400)
			return
		}
		body["domain"] = normalizedDomain
	}

	// Convert body to JSON string for storage
	settingsJSON, err := json.Marshal(body)
	if err != nil {
		http.Error(w, `{"error":"Failed to encode settings"}`, 500)
		return
	}

	// Upsert settings into database
	_, err = services.SystemPool.Exec(r.Context(), `
		INSERT INTO system.ui_settings (project_slug, table_name, settings)
		VALUES ('_system_root_', 'system_config', $1)
		ON CONFLICT (project_slug, table_name)
		DO UPDATE SET settings = EXCLUDED.settings, updated_at = NOW()
	`, string(settingsJSON))

	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"Failed to update settings: %s"}`, err.Error()), 500)
		return
	}

	// If domain changed, trigger nginx config rebuild
	if domain, ok := body["domain"].(string); ok && domain != "" {
		// Queue async nginx rebuild
		go func() {
			ctx := context.Background()
			if err := services.RebuildNginxConfigs(ctx, services.SystemPool); err != nil {
				log.Printf("[UpdateSystemSettings] Failed to rebuild nginx configs: %v", err)
			}
		}()
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Settings updated successfully",
	})
}

func (c *AdminController) ListProjects(w http.ResponseWriter, r *http.Request) {
	rows, err := services.SystemPool.Query(r.Context(),
		"SELECT id, name, slug, db_name, custom_domain, status, created_at, anon_key, jwt_secret, metadata FROM system.projects ORDER BY created_at DESC")
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, 500)
		return
	}
	defer rows.Close()

	projects := []map[string]interface{}{}
	for rows.Next() {
		var id, name, slug, db, status, anonCipher, jwtCipher string
		var customDomain sql.NullString
		var created time.Time
		var metadataBytes []byte
		rows.Scan(&id, &name, &slug, &db, &customDomain, &status, &created, &anonCipher, &jwtCipher, &metadataBytes)
		
		// Decrypt keys on the fly - com tratamento robusto para valores legados
		anonPlain := anonCipher
		jwtPlain := jwtCipher
		
		// Helper para descriptografar chaves
		decryptKey := func(cipher, keyName string) string {
			if cipher == "" {
				return ""
			}
			if strings.HasPrefix(cipher, "cse:v1:") {
				decrypted, err := c.CryptoSvc.Decrypt(cipher)
				if err != nil {
					log.Printf("[ListProjects] Falha ao descriptografar %s para projeto %s: %v", keyName, slug, err)
					if err == services.ErrEngineSealed {
						return "[ENGINE_SELADO]"
					}
					return ""
				}
				return decrypted
			}
			// Legado ou já em plaintext
			return cipher
		}
		
		anonPlain = decryptKey(anonCipher, "anon_key")
		jwtPlain = decryptKey(jwtCipher, "jwt_secret")

		// Parse metadata JSON
		var metadata map[string]interface{}
		if len(metadataBytes) > 0 {
			json.Unmarshal(metadataBytes, &metadata)
		}
		
		// Gerar anon_keys para App Clients (se houver jwt_secret)
		if appClientsRaw, ok := metadata["app_clients"].([]interface{}); ok && jwtPlain != "" {
			appClientsWithKeys := make([]map[string]interface{}, len(appClientsRaw))
			for i, acRaw := range appClientsRaw {
				if ac, ok := acRaw.(map[string]interface{}); ok {
					// Copiar dados do App Client
					appClientWithKey := make(map[string]interface{})
					for k, v := range ac {
						appClientWithKey[k] = v
					}
					
					// Gerar anon_key se tivermos id e nonce
					if id, ok := ac["id"].(string); ok {
						if nonce, ok := ac["nonce"].(string); ok {
							anonKey := services.GenerateAppAnonKey(id, nonce, jwtPlain)
							appClientWithKey["anon_key"] = anonKey
						}
					}
					appClientsWithKeys[i] = appClientWithKey
				}
			}
			metadata["app_clients"] = appClientsWithKeys
		}
		
		// Extrair global_site_url do auth_config (pode estar em metadata ou metadata.extra)
		globalSiteURL := ""
		// Try direct access first
		if authConfig, ok := metadata["auth_config"].(map[string]interface{}); ok {
			if siteURL, ok := authConfig["site_url"].(string); ok {
				globalSiteURL = siteURL
			}
		} else if extra, ok := metadata["extra"].(map[string]interface{}); ok {
			// Fallback: check inside extra.auth_config
			if authConfig, ok := extra["auth_config"].(map[string]interface{}); ok {
				if siteURL, ok := authConfig["site_url"].(string); ok {
					globalSiteURL = siteURL
				}
			}
		}

		projects = append(projects, map[string]interface{}{
			"id": id, "name": name, "slug": slug, "db_name": db, "custom_domain": customDomain.String,
			"status": status, "created_at": created, "anon_key": anonPlain, 
			"global_site_url": globalSiteURL, "metadata": metadata,
		})
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(projects)
}

func (c *AdminController) RevealKey(w http.ResponseWriter, r *http.Request) {
	val := r.Context().Value(types.CascataCtxKey)
	if val == nil {
		http.Error(w, `{"error":"Context Lost"}`, 500)
		return
	}
	ctx := val.(*types.CascataRequest)
	if ctx.Project == nil {
		http.Error(w, `{"error":"Project Context Required"}`, 404)
		return
	}

	var body struct {
		Type     string `json:"type"`     // 'anon_key' or 'service_key' or 'jwt_secret'
		KeyType  string `json:"keyType"`  // Alternative field name from frontend
		Password string `json:"password"` // Admin password for verification
	}
	json.NewDecoder(r.Body).Decode(&body)

	// Use KeyType if Type is empty (frontend compatibility)
	keyType := body.Type
	if keyType == "" {
		keyType = body.KeyType
	}

	// Determine which key to reveal
	var encryptedKey string
	var isSensitive bool
	switch keyType {
	case "service_key", "service":
		encryptedKey = ctx.Project.ServiceKey
		isSensitive = true
	case "jwt_secret":
		encryptedKey = ctx.Project.JWTSecret
		isSensitive = true
	case "anon_key", "anon":
		encryptedKey = ctx.Project.AnonKey
		isSensitive = false // Anon key is public, no password required
	default:
		encryptedKey = ctx.Project.AnonKey
		isSensitive = false
	}

	// Verify admin password only for sensitive keys (Service Key, JWT Secret)
	// Anon Key is public and can be revealed without password
	if isSensitive {
		if body.Password == "" {
			http.Error(w, `{"error":"Password required"}`, 400)
			return
		}

		// Get the admin user from context or verify password
		var adminHash string
		var adminID string
		if ctx.User != nil {
			if sub, ok := ctx.User["sub"].(string); ok {
				adminID = sub
			}
		}

		var err error
		if adminID != "" {
			err = services.SystemPool.QueryRow(r.Context(),
				"SELECT password_hash FROM system.admin_users WHERE id = $1",
				adminID).Scan(&adminHash)
		}
		if adminID == "" || err != nil {
			// Fallback: try to get any admin user for verification
			err = services.SystemPool.QueryRow(r.Context(),
				"SELECT password_hash FROM system.admin_users LIMIT 1").Scan(&adminHash)
			if err != nil {
				http.Error(w, `{"error":"Unable to verify credentials"}`, 500)
				return
			}
		}

		if !c.AuthSvc.ComparePassword(adminHash, body.Password) {
			http.Error(w, `{"error":"Invalid password"}`, 401)
			return
		}
	}

	plain, err := c.CryptoSvc.Decrypt(encryptedKey)
	if err != nil {
		http.Error(w, `{"error":"Decryption failed: `+err.Error()+`"}`, 500)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"key": plain})
}

func (c *AdminController) UpdateProject(w http.ResponseWriter, r *http.Request) {
	val := r.Context().Value(types.CascataCtxKey)
	if val == nil {
		http.Error(w, `{"error":"Context Lost"}`, 500)
		return
	}
	ctx := val.(*types.CascataRequest)
	if ctx.Project == nil {
		http.Error(w, `{"error":"Project Context Required"}`, 404)
		return
	}

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Invalid Body", 400)
		return
	}

	var rawMap map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &rawMap); err != nil {
		http.Error(w, "Invalid JSON", 400)
		return
	}

	var body struct {
		Name                   string                 `json:"name"`
		CustomDomain           string                 `json:"custom_domain"`
		SSLCertificateSource   string                 `json:"ssl_certificate_source"`
		Status                 string                 `json:"status"`
		LogRetentionDays       *int                   `json:"log_retention_days"`
		ArchiveLogs            *bool                  `json:"archive_logs"`
		Metadata               map[string]interface{} `json:"metadata"`
		Password               string                 `json:"password"`
		OTPCode                string                 `json:"otp_code"`
	}
	if err := json.Unmarshal(bodyBytes, &body); err != nil {
		http.Error(w, "Invalid JSON", 400)
		return
	}

	// Dynamic update
	query := "UPDATE system.projects SET "
	params := []interface{}{}
	i := 1

	if body.Name != "" {
		query += fmt.Sprintf("name = $%d, ", i)
		params = append(params, body.Name)
		i++
	}
	
	// Intelligent Domain Handling
	if domainVal, exists := rawMap["custom_domain"]; exists {
		if domainVal == nil || domainVal == "" {
			// Explicitly clear the domain
			query += "custom_domain = NULL, ssl_certificate_source = NULL, "
			// Trigger nginx reload for cleanup will happen later
			body.CustomDomain = "CLEARED" // Flag for later
		} else {
			domainStr := fmt.Sprintf("%v", domainVal)
			// Normalizar domínio (converter IDN para Punycode se necessário)
			normalizedDomain, err := services.NormalizeDomain(domainStr)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"Invalid custom domain: %s"}`, err.Error()), 400)
				return
			}
			query += fmt.Sprintf("custom_domain = $%d, ", i)
			params = append(params, normalizedDomain)
			i++
			
			// Auto-detect ssl_certificate_source se não fornecido mas domínio foi atualizado
			if body.SSLCertificateSource == "" {
				body.SSLCertificateSource = services.DetectWildcardSource(normalizedDomain)
				if body.SSLCertificateSource != "" {
					log.Printf("[UpdateProject] Auto-detected ssl_certificate_source: %s for domain: %s", body.SSLCertificateSource, normalizedDomain)
				}
			}
		}
	}
	
	if body.SSLCertificateSource != "" && body.CustomDomain != "CLEARED" {
		query += fmt.Sprintf("ssl_certificate_source = $%d, ", i)
		params = append(params, body.SSLCertificateSource)
		i++
	}
	if body.Status != "" {
		query += fmt.Sprintf("status = $%d, ", i)
		params = append(params, body.Status)
		i++
	}
	if body.LogRetentionDays != nil {
		query += fmt.Sprintf("log_retention_days = $%d, ", i)
		params = append(params, *body.LogRetentionDays)
		i++
	}
	if body.ArchiveLogs != nil {
		query += fmt.Sprintf("archive_logs = $%d, ", i)
		params = append(params, *body.ArchiveLogs)
		i++
	}
	if body.Metadata != nil {
		// BUSCAR METADATA EXISTENTE PARA DEEP MERGE
		var existingMetaStr string
		err := services.SystemPool.QueryRow(r.Context(), 
			"SELECT COALESCE(metadata::text, '{}') FROM system.projects WHERE slug = $1", 
			ctx.Project.Slug).Scan(&existingMetaStr)
		
		var existingMeta map[string]interface{}
		if err == nil {
			json.Unmarshal([]byte(existingMetaStr), &existingMeta)
		}

		// Enforce security downgrade rules (v0.1.0 Security Protocol)
		if isSecurityDowngrade(existingMeta, body.Metadata) {
			adminPwd := body.Password
			if adminPwd == "" {
				adminPwd = r.Header.Get("X-Admin-Password")
			}
			adminOtp := body.OTPCode
			if adminOtp == "" {
				adminOtp = r.Header.Get("X-Admin-OTP")
			}

			if adminPwd == "" {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				json.NewEncoder(w).Encode(map[string]interface{}{
					"error":        "MFA_REQUIRED",
					"otp_required": os.Getenv("ADMIN_OTP_ENABLED") == "true" || os.Getenv("CASCATA_OTP_ENABLED") == "true",
					"message":      "Security downgrade requires password confirmation.",
				})
				return
			}

			// Validate Password
			var adminHash string
			adminID := r.Context().Value("admin_id")
			var dbErr error
			if adminID != nil && adminID != "" {
				dbErr = services.SystemPool.QueryRow(r.Context(), 
					"SELECT password_hash FROM system.admin_users WHERE id = $1", 
					adminID).Scan(&adminHash)
			} else {
				dbErr = services.SystemPool.QueryRow(r.Context(), 
					"SELECT password_hash FROM system.admin_users LIMIT 1").Scan(&adminHash)
			}

			if dbErr != nil || !c.AuthSvc.ComparePassword(adminHash, adminPwd) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				json.NewEncoder(w).Encode(map[string]interface{}{
					"error": "Invalid administrator password",
				})
				return
			}

			// Validate OTP if enabled globally
			if os.Getenv("ADMIN_OTP_ENABLED") == "true" || os.Getenv("CASCATA_OTP_ENABLED") == "true" {
				if adminOtp == "" {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusForbidden)
					json.NewEncoder(w).Encode(map[string]interface{}{
						"error":        "MFA_REQUIRED",
						"otp_required": true,
						"message":      "OTP token code required for security downgrade.",
					})
					return
				}

				otpSecret := os.Getenv("CASCATA_OTP_SECRET")
				if otpSecret == "" {
					otpSecret = os.Getenv("OTP_SECRET")
				}
				if otpSecret != "" && !c.AuthSvc.VerifyTOTP(otpSecret, adminOtp) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusUnauthorized)
					json.NewEncoder(w).Encode(map[string]interface{}{
						"error": "Invalid OTP token code",
					})
					return
				}
			}

			log.Printf("[Security v0.1.0] Security downgrade approved for project %s.", ctx.Project.Slug)
		}
		
		// FAZER DEEP MERGE: preservar configs existentes e aplicar novas
		
		// Normalizar locked_columns ANTES do deep merge
		if lockedCols, ok := body.Metadata["locked_columns"]; ok && lockedCols != nil {
			if locksMap, isMap := lockedCols.(map[string]interface{}); isMap {
				for _, columnsObj := range locksMap {
					if columnsMap, isColMap := columnsObj.(map[string]interface{}); isColMap {
						for colName, colMetaObj := range columnsMap {
							if colMeta, isMetaMap := colMetaObj.(map[string]interface{}); isMetaMap {
								if lvl, has := colMeta["lockLevel"]; has {
									colMeta["lock_type"] = lvl
									delete(colMeta, "lockLevel")
								}
								if meth, has := colMeta["methods"]; has {
									if methStr, isStr := meth.(string); isStr && methStr != "" {
										parts := strings.Split(methStr, ",")
										var factors []string
										for _, p := range parts {
											p = strings.TrimSpace(p)
											if p != "" {
												factors = append(factors, services.FormatFactorName(p))
											}
										}
										colMeta["allowed_factors"] = factors
									}
									delete(colMeta, "methods")
								}
								columnsMap[colName] = colMeta
							} else if strType, isStr := colMetaObj.(string); isStr {
								columnsMap[colName] = map[string]interface{}{
									"lock_type": strType,
								}
							}
						}
					}
				}
			}
		}
		if tableSecurity, ok := body.Metadata["table_security"]; ok && tableSecurity != nil {
			if tableMap, isMap := tableSecurity.(map[string]interface{}); isMap {
				for tableName, ruleObj := range tableMap {
					ruleMap, isRuleMap := ruleObj.(map[string]interface{})
					if !isRuleMap {
						continue
					}
					if methods, has := ruleMap["methods"]; has {
						ruleMap["operations"] = normalizeSecurityStringSlice(methods)
						delete(ruleMap, "methods")
					}
					if typesRaw, has := ruleMap["type"]; has {
						ruleMap["allowed_factors"] = normalizeSecurityStringSlice(typesRaw)
						delete(ruleMap, "type")
					}
					if factors, has := ruleMap["allowedFactors"]; has {
						ruleMap["allowed_factors"] = normalizeSecurityStringSlice(factors)
						delete(ruleMap, "allowedFactors")
					}
					tableMap[tableName] = ruleMap
				}
			}
		}

		mergedMeta := deepMergeJSON(existingMeta, body.Metadata)
		
		metaBytes, _ := json.Marshal(mergedMeta)
		query += fmt.Sprintf("metadata = $%d, ", i)
		params = append(params, metaBytes)
		i++
		
		// APLICAR SECURITY LOCKS SE HOUVER locked_columns NO PAYLOAD
		if lockedCols, ok := body.Metadata["locked_columns"]; ok && lockedCols != nil {
			c.applySecurityLocks(r.Context(), ctx.Project.Slug, lockedCols)
		}
		
		// INVALIDAR CACHE DE SCHEMA quando metadata é atualizado (locks, masks, auto_clock, computed)
		// Isso garante que o warmFromDatabase carregue os novos metadados na próxima requisição
		services.GlobalSchemaCache.InvalidateProject(ctx.Project.Slug)
		log.Printf("[AdminController] Schema cache invalidated for project %s due to metadata update", ctx.Project.Slug)
		
		// NOTA: Auto Clock é processado no Go (ApplyAutoClock em schema_cache.go)
		// Não precisa mais de triggers PostgreSQL - mais rápido e confiável
	}

	query = strings.TrimSuffix(query, ", ")

	// Validar se pelo menos um campo foi fornecido para atualização
	if !strings.Contains(query, "=") {
		http.Error(w, `{"error":"No fields provided for update"}`, 400)
		return
	}

	query += fmt.Sprintf(" WHERE slug = $%d", i)
	params = append(params, ctx.Project.Slug)

	_, err = services.SystemPool.Exec(r.Context(), query, params...)
	if err != nil {
		http.Error(w, `{"error":"Update failed: `+err.Error()+`"}`, 500)
		return
	}

	// Se o custom_domain foi alterado, trigger nginx config rebuild
	if body.CustomDomain != "" {
		go func() {
			ctx := context.Background()
			if err := services.RebuildNginxConfigs(ctx, services.SystemPool); err != nil {
				log.Printf("[UpdateProject] Failed to rebuild nginx configs: %v", err)
			}
		}()
	}

	w.Write([]byte(`{"success":true}`))
}

// DeleteProject exclui um projeto e todos os seus recursos
func (c *AdminController) DeleteProject(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	if slug == "" {
		http.Error(w, `{"error":"Slug is required"}`, 400)
		return
	}

	// 1. Parse do body para verificação de segurança
	var body struct {
		Password    string `json:"password"`
		OTPCode     string `json:"otp_code"`
		ProjectName string `json:"project_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"Invalid JSON body"}`, 400)
		return
	}

	// 2. Verificar nome do projeto
	if body.ProjectName != slug {
		http.Error(w, `{"error":"Project name confirmation does not match"}`, 400)
		return
	}

	// 3. Verificar senha do admin
	if body.Password == "" {
		http.Error(w, `{"error":"Password is required"}`, 400)
		return
	}

	// Pegar admin ID do contexto (do token JWT)
	val := r.Context().Value(types.CascataCtxKey)
	if val == nil {
		http.Error(w, `{"error":"Authentication required"}`, 401)
		return
	}
	ctx := val.(*types.CascataRequest)
	
	// Buscar admin ID do JWT claims (sub contém o ID do admin)
	var adminID string
	if ctx.User != nil {
		if sub, ok := ctx.User["sub"].(string); ok {
			adminID = sub
		}
	}
	if adminID == "" {
		log.Printf("[DeleteProject] ERROR: ctx.User is nil or sub is empty")
		http.Error(w, `{"error":"Admin authentication required"}`, 401)
		return
	}
	
	log.Printf("[DeleteProject] Looking up admin with ID: %s", adminID)

	// Buscar admin no banco para verificar senha (sub do JWT é o ID do admin)
	var admin struct {
		Email        string `db:"email"`
		PasswordHash string `db:"password_hash"`
	}
	err := services.SystemPool.QueryRow(r.Context(),
		"SELECT email, password_hash FROM system.admin_users WHERE id = $1",
		adminID).Scan(&admin.Email, &admin.PasswordHash)
	if err != nil {
		log.Printf("[DeleteProject] ERROR: Admin not found for ID %s: %v", adminID, err)
		http.Error(w, `{"error":"Admin not found"}`, 404)
		return
	}
	
	log.Printf("[DeleteProject] Admin found: %s", admin.Email)

	// Verificar senha
	if !c.AuthSvc.ComparePassword(admin.PasswordHash, body.Password) {
		http.Error(w, `{"error":"Invalid password"}`, 401)
		return
	}

	// 4. Verificar OTP se habilitado globalmente
	if os.Getenv("ADMIN_OTP_ENABLED") == "true" || os.Getenv("CASCATA_OTP_ENABLED") == "true" {
		if body.OTPCode == "" {
			http.Error(w, `{"error":"OTP code required","otp_required":true}`, 401)
			return
		}
		// Verificar código OTP usando secret global
		otpSecret := os.Getenv("CASCATA_OTP_SECRET")
		if otpSecret == "" {
			otpSecret = os.Getenv("OTP_SECRET")
		}
		if otpSecret != "" && !c.AuthSvc.VerifyTOTP(otpSecret, body.OTPCode) {
			http.Error(w, `{"error":"Invalid OTP code"}`, 401)
			return
		}
	}

	// 5. Buscar projeto para obter db_name
	var project struct {
		DbName string `db:"db_name"`
	}
	err = services.SystemPool.QueryRow(r.Context(),
		"SELECT db_name FROM system.projects WHERE slug = $1",
		slug).Scan(&project.DbName)
	if err != nil {
		http.Error(w, `{"error":"Project not found"}`, 404)
		return
	}

	// 6. Terminar conexões do banco de dados
	_, _ = services.SystemPool.Exec(r.Context(),
		`SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1`,
		project.DbName)

	// 7. Limpar cron jobs do pg_cron associados ao projeto
	// Os cron jobs ficam no banco de sistema, então precisamos removê-los manualmente
	_, err = services.SystemPool.Exec(r.Context(), `
		SELECT cron.unschedule(jobid) 
		FROM cron.job 
		WHERE database = $1 OR jobname LIKE '%-' || $1
	`, project.DbName)
	if err != nil {
		log.Printf("[DeleteProject] Warning: Failed to cleanup cron jobs for %s: %v", project.DbName, err)
	}

	// 8. Remover diretório de storage
	storagePath := "/var/lib/cascata/storage/" + slug
	if _, err := os.Stat(storagePath); err == nil {
		os.RemoveAll(storagePath)
	}

	// 9. Excluir registro do projeto
	_, err = services.SystemPool.Exec(r.Context(),
		"DELETE FROM system.projects WHERE slug = $1",
		slug)
	if err != nil {
		http.Error(w, `{"error":"Failed to delete project: `+err.Error()+`"}`, 500)
		return
	}

	// 10. Dropar o banco de dados
	dbNameQuoted := "\"" + strings.ReplaceAll(project.DbName, "\"", "\"\"") + "\""
	_, err = services.SystemPool.Exec(r.Context(), "DROP DATABASE IF EXISTS "+dbNameQuoted)
	if err != nil {
		log.Printf("[DeleteProject] Warning: Failed to drop database %s: %v", project.DbName, err)
	}

	// 11. Rebuild nginx configs
	go func() {
		ctx := context.Background()
		if err := services.RebuildNginxConfigs(ctx, services.SystemPool); err != nil {
			log.Printf("[DeleteProject] Failed to rebuild nginx configs: %v", err)
		}
	}()

	w.Write([]byte(`{"success":true}`))
}

// deepMergeJSON faz merge profundo de mapas JSON (preserva configs existentes)
func deepMergeJSON(existing, incoming map[string]interface{}) map[string]interface{} {
	if existing == nil {
		existing = make(map[string]interface{})
	}
	
	result := make(map[string]interface{})
	// Copiar valores existentes
	for k, v := range existing {
		result[k] = v
	}
	
	// Aplicar valores novos com merge profundo para objetos aninhados
	for k, v := range incoming {
		if v == nil {
			// Se o valor novo é nil, remover a chave
			delete(result, k)
			continue
		}
		
		// Verificar se é um mapa aninhado para deep merge
		existingVal, exists := result[k]
		if !exists {
			// Chave não existe, simplesmente adicionar
			result[k] = v
			continue
		}
		
		// Tentar fazer deep merge se ambos forem mapas
		existingMap, existingIsMap := existingVal.(map[string]interface{})
		incomingMap, incomingIsMap := v.(map[string]interface{})
		
		if existingIsMap && incomingIsMap {
			// Deep merge recursivo para objetos aninhados (ex: locked_columns["tabela"])
			result[k] = deepMergeJSON(existingMap, incomingMap)
		} else {
			// Substituir valor diretamente
			result[k] = v
		}
	}
	
	return result
}

// applySecurityLocks aplica os locks de segurança no banco do tenant
func (c *AdminController) applySecurityLocks(ctx context.Context, projectSlug string, locks interface{}) {
	locksMap, ok := locks.(map[string]interface{})
	if !ok {
		return
	}
	
	// Buscar db_name do projeto (Main)
	var dbName string
	err := services.SystemPool.QueryRow(ctx, 
		"SELECT db_name FROM system.projects WHERE slug = $1", projectSlug).Scan(&dbName)
	if err != nil || dbName == "" {
		log.Printf("[AdminController] Failed to get db_name for project %s: %v", projectSlug, err)
		return
	}

	// Buscar bancos de dados de branches
	dbsToUpdate := []string{dbName}
	rows, err := services.SystemPool.Query(ctx, 
		"SELECT COALESCE(materialized_db, data_branch_db_name) FROM system.branches WHERE project_slug = $1 AND COALESCE(materialized_db, data_branch_db_name) IS NOT NULL", projectSlug)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var branchDB string
			if err := rows.Scan(&branchDB); err == nil && branchDB != "" {
				dbsToUpdate = append(dbsToUpdate, branchDB)
			}
		}
	}

	for _, targetDB := range dbsToUpdate {
		projectPool := services.Get(targetDB, nil)
		if projectPool == nil {
			log.Printf("[AdminController] Failed to get project pool for %s", targetDB)
			continue
		}

		// Aplicar locks por tabela
		for tableName, columnsObj := range locksMap {
			columnsMap, ok := columnsObj.(map[string]interface{})
			if !ok {
				continue
			}
			
			// Converter para JSONB
			columnsJSON, _ := json.Marshal(columnsMap)
			
			// Chamar stored procedure de aplicação de locks
			_, err := projectPool.Exec(ctx,
				"SELECT system.apply_security_locks($1, $2, $3::jsonb)",
				projectSlug, tableName, string(columnsJSON))
			if err != nil {
				log.Printf("[AdminController] Failed to apply security locks for %s.%s on DB %s: %v", 
					projectSlug, tableName, targetDB, err)
			}
		}
	}
}

// applyAutoClockTriggers aplica os triggers de auto-update temporal no banco do tenant
func (c *AdminController) applyAutoClockTriggers(ctx context.Context, projectSlug string, autoClocks interface{}) {
	autoClockMap, ok := autoClocks.(map[string]interface{})
	if !ok {
		return
	}
	
	// Buscar db_name do projeto (Main)
	var dbName string
	err := services.SystemPool.QueryRow(ctx, 
		"SELECT db_name FROM system.projects WHERE slug = $1", projectSlug).Scan(&dbName)
	if err != nil || dbName == "" {
		log.Printf("[AdminController] Failed to get db_name for project %s: %v", projectSlug, err)
		return
	}

	// Buscar bancos de dados de branches
	dbsToUpdate := []string{dbName}
	rows, err := services.SystemPool.Query(ctx, 
		"SELECT COALESCE(materialized_db, data_branch_db_name) FROM system.branches WHERE project_slug = $1 AND COALESCE(materialized_db, data_branch_db_name) IS NOT NULL", projectSlug)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var branchDB string
			if err := rows.Scan(&branchDB); err == nil && branchDB != "" {
				dbsToUpdate = append(dbsToUpdate, branchDB)
			}
		}
	}

	for _, targetDB := range dbsToUpdate {
		projectPool := services.Get(targetDB, nil)
		if projectPool == nil {
			log.Printf("[AdminController] Failed to get project pool for %s", targetDB)
			continue
		}

		// Aplicar auto-clock triggers por tabela
		for tableName, columnsObj := range autoClockMap {
			columnsMap, ok := columnsObj.(map[string]interface{})
			if !ok {
				continue
			}
			
			// Converter para JSONB
			columnsJSON, _ := json.Marshal(columnsMap)
			
			// Chamar stored procedure de aplicação de auto-clock triggers
			_, err := projectPool.Exec(ctx,
				"SELECT system.apply_auto_clock_triggers($1, $2, $3::jsonb)",
				projectSlug, tableName, columnsJSON)
			if err != nil {
				log.Printf("[AdminController] Failed to apply auto-clock triggers for %s.%s on DB %s: %v", 
					projectSlug, tableName, targetDB, err)
			}
		}
	}
}



func (c *AdminController) CreateProject(w http.ResponseWriter, r *http.Request) {
	var body struct { 
		Name                   string                 `json:"name"`
		Slug                   string                 `json:"slug"`
		CustomDomain           string                 `json:"custom_domain"`
		SSLCertificateSource   string                 `json:"ssl_certificate_source"`
		Timezone               string                 `json:"timezone"`
		AnonKey                string                 `json:"anon_key"`
		ServiceKey             string                 `json:"service_key"`
		JwtSecret              string                 `json:"jwt_secret"`
		Metadata               map[string]interface{} `json:"metadata"`
	}

	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"Invalid request body"}`, 400)
		return
	}

	if body.Name == "" || body.Slug == "" {
		http.Error(w, `{"error":"Name and Slug required"}`, 400)
		return
	}

	// GERAR CHAVES AUTOMATICAMENTE SE NÃO FORNECIDAS
	// Garante que todo projeto tenha chaves criptográficas válidas
	if body.AnonKey == "" {
		body.AnonKey = c.CryptoSvc.GenerateKey() + c.CryptoSvc.GenerateKey() // 128 chars hex
		log.Printf("[CreateProject] Generated anon_key automatically for project %s", body.Slug)
	}
	if body.ServiceKey == "" {
		body.ServiceKey = c.CryptoSvc.GenerateKey() + c.CryptoSvc.GenerateKey() // 128 chars hex
		log.Printf("[CreateProject] Generated service_key automatically for project %s", body.Slug)
	}
	if body.JwtSecret == "" {
		body.JwtSecret = c.CryptoSvc.GenerateKey() + c.CryptoSvc.GenerateKey() // 128 chars hex
		log.Printf("[CreateProject] Generated jwt_secret automatically for project %s", body.Slug)
	}

	// 2. Encryption (CSE Protocol)
	// Anon Key, Service Key e JWT Secret nascem cifrados no motor
	anonCipher, err := c.CryptoSvc.Encrypt("sse", body.AnonKey)
	if err != nil {
		if err == services.ErrEngineSealed {
			http.Error(w, `{"error":"Crypto Engine está selado","hint":"Reinicie o servidor para auto-unseal ou chame POST /api/control/auth/sovereign/unseal com a Master Secret","code":"ENGINE_SEALED"}`, 503)
		} else {
			http.Error(w, `{"error":"Encryption failed (anon_key): `+err.Error()+`"}`, 500)
		}
		return
	}

	serviceCipher, err := c.CryptoSvc.Encrypt("sse", body.ServiceKey)
	if err != nil {
		http.Error(w, `{"error":"Encryption failed (service_key): `+err.Error()+`"}`, 500)
		return
	}

	jwtCipher, err := c.CryptoSvc.Encrypt("sse", body.JwtSecret)
	if err != nil {
		http.Error(w, `{"error":"Encryption failed (jwt_secret): `+err.Error()+`"}`, 500)
		return
	}

	// 3. Database State Matrix (Production Grade Schema)
	dbName := "cascata_" + body.Slug
	err = c.DbSvc.CreateDatabase(r.Context(), dbName)
	if err != nil {
		http.Error(w, `{"error":"Database instance failed: `+err.Error()+`"}`, 500)
		return
	}

	// 3.1 Initialize ALL managed schemas in the new project database (auth, system, extensions, storage)
	err = c.DbSvc.InitTenantSchemas(r.Context(), dbName)
	if err != nil {
		log.Printf("[CreateProject] Tenant schemas init warning: %v", err)
		// Log error but don't fail - schemas can be retried later via GetProjectPool
	}

	// 3.2 Create the physical 'default' bucket for the project
	// This is a REAL bucket that coexists with any other buckets - NOT a placeholder
	storageRoot := os.Getenv("STORAGE_ROOT")
	if storageRoot == "" {
		storageRoot = "./storage"
	}
	defaultBucketPath := filepath.Join(storageRoot, body.Slug, "default")
	if err := os.MkdirAll(defaultBucketPath, 0755); err == nil {
		// Index the default bucket in system.storage_objects
		services.SystemPool.Exec(r.Context(), `
			INSERT INTO system.storage_objects (project_slug, bucket, name, parent_path, full_path, is_folder, size, rls_enabled)
			VALUES ($1, 'default', 'default', '', 'default', true, 0, true)
			ON CONFLICT (project_slug, bucket, full_path) DO NOTHING
		`, body.Slug)
		log.Printf("[CreateProject] Created physical 'default' bucket for project %s at %s", body.Slug, defaultBucketPath)
	} else {
		log.Printf("[CreateProject] Warning: Failed to create default bucket: %v", err)
	}

	// 3.3 Create the physical '_sites' bucket for static site deployments
	sitesBucketPath := filepath.Join(storageRoot, body.Slug, "_sites")
	if err := os.MkdirAll(sitesBucketPath, 0755); err == nil {
		// Index the _sites bucket in system.storage_objects
		services.SystemPool.Exec(r.Context(), `
			INSERT INTO system.storage_objects (project_slug, bucket, name, parent_path, full_path, is_folder, size, rls_enabled)
			VALUES ($1, '_sites', '_sites', '', '_sites', true, 0, true)
			ON CONFLICT (project_slug, bucket, full_path) DO NOTHING
		`, body.Slug)
		log.Printf("[CreateProject] Created physical '_sites' bucket for project %s at %s", body.Slug, sitesBucketPath)
	} else {
		log.Printf("[CreateProject] Warning: Failed to create _sites bucket: %v", err)
	}

	// Normalizar domínio se fornecido
	var customDomain, sslCertSource string
	if body.CustomDomain != "" {
		normalizedDomain, err := services.NormalizeDomain(body.CustomDomain)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"Invalid custom domain: %s"}`, err.Error()), 400)
			return
		}
		customDomain = normalizedDomain
		
		// Auto-detect ssl_certificate_source se não fornecido
		if body.SSLCertificateSource != "" {
			sslCertSource = body.SSLCertificateSource
		} else {
			sslCertSource = services.DetectWildcardSource(customDomain)
			if sslCertSource != "" {
				log.Printf("[CreateProject] Auto-detected ssl_certificate_source: %s for domain: %s", sslCertSource, customDomain)
			}
		}
	}

	// Garantir que Metadata não seja nil
	if body.Metadata == nil {
		body.Metadata = make(map[string]interface{})
	}

	// Pre-configure default auth strategies: email (enabled with password/otp/biometria options)
	if _, ok := body.Metadata["auth_strategies"]; !ok {
		body.Metadata["auth_strategies"] = map[string]interface{}{
			"email": map[string]interface{}{
				"enabled":               true,
				"jwt_expiration":        "24h",
				"refresh_validity_days": 30,
				"rules":                 []interface{}{},
				"password_enabled":      true,
				"otp_enabled":           true,
				"magiclink_enabled":     true,
				"biometria_enabled":     false,
				"totp_enabled":          false,
			},
		}
	}
	
	// Salvar timezone no metadata se fornecido
	if body.Timezone != "" {
		body.Metadata["timezone"] = body.Timezone
	}
	
	metadata, _ := json.Marshal(body.Metadata)
	
	// Converter string vazia para NULL (nil) para evitar violação de UNIQUE constraint
	// quando múltiplos projetos não têm custom_domain definido
	var customDomainPtr *string
	if customDomain != "" {
		customDomainPtr = &customDomain
	}
	
	_, err = services.SystemPool.Exec(r.Context(), 
		"INSERT INTO system.projects (name, slug, db_name, custom_domain, ssl_certificate_source, anon_key, service_key, jwt_secret, metadata) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)",
		body.Name, body.Slug, dbName, customDomainPtr, sslCertSource, anonCipher, serviceCipher, jwtCipher, metadata)
	if err != nil {
		http.Error(w, `{"error":"System registry failed: `+err.Error()+`"}`, 500)
		return
	}

	// Cache JWT secret no Dragonfly para verificação no Layer 2 (Edge)
	// Isso permite que o IntelligentEdgeLimiter verifique assinatura JWT sem DB lookup
	if err := middleware.CacheJWTSecret(r.Context(), body.Slug, body.JwtSecret); err != nil {
		log.Printf("[CreateProject] Warning: Failed to cache JWT secret in Dragonfly: %v", err)
		// Não falha - o cache pode ser populado posteriormente
	} else {
		log.Printf("[CreateProject] JWT secret cached for project %s", body.Slug)
	}

	// Se custom_domain foi definido, trigger nginx config rebuild
	if customDomain != "" {
		go func() {
			ctx := context.Background()
			if err := services.RebuildNginxConfigs(ctx, services.SystemPool); err != nil {
				log.Printf("[CreateProject] Failed to rebuild nginx configs: %v", err)
			}
		}()
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":     true,
		"message":     "Project created successfully",
		"slug":        body.Slug,
		"db_name":     dbName,
		"timezone":    body.Timezone,
		// NOTA: Chaves sensíveis (anon_key, service_key, jwt_secret) NÃO são retornadas
		// Use o endpoint /api/control/projects/{slug}/reveal-key para obter as chaves
	})
}

func (c *AdminController) GetServerPublicIp(w http.ResponseWriter, r *http.Request) {
	resp, err := http.Get("https://api.ipify.org?format=json")
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ip": "Local/Discovery Mode"}`)) // Fallback
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Content-Type", "application/json")
	io.Copy(w, resp.Body)
}

// GetMyIP returns the client's IP address (used for panic mode whitelisting display)
func (c *AdminController) GetMyIP(w http.ResponseWriter, r *http.Request) {
	ip := r.Header.Get("X-Real-Ip")
	if ip == "" {
		ip = r.Header.Get("X-Forwarded-For")
		if ip != "" {
			ip = strings.Split(ip, ",")[0]
		}
	}
	if ip == "" {
		ip = r.RemoteAddr
	}
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"ip": ip})
}

func (c *AdminController) CheckSsl(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Domain string `json:"domain"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"Invalid request body"}`, 400)
		return
	}
	
	if body.Domain == "" {
		http.Error(w, `{"error":"Domain required"}`, 400)
		return
	}
	
	// Real SSL check via TCP connection
	conn, err := net.DialTimeout("tcp", body.Domain+":443", 5*time.Second)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "error",
			"domain": body.Domain,
			"error":  err.Error(),
			"timestamp": time.Now().Format(time.RFC3339),
		})
		return
	}
	defer conn.Close()
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":    "active",
		"domain":    body.Domain,
		"reachable": true,
		"timestamp": time.Now().Format(time.RFC3339),
	})
}

func (c *AdminController) CreateCertificate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Domain   string `json:"domain"`
		Email    string `json:"email"`
		Provider string `json:"provider"`
		Cert     string `json:"cert"`
		Key      string `json:"key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"Invalid payload"}`, 400)
		return
	}
	if body.Domain == "" {
		http.Error(w, `{"error":"Domain is required"}`, 400)
		return
	}

	// Normalizar domínio (converter IDN para Punycode se necessário)
	normalizedDomain, err := services.NormalizeDomain(body.Domain)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"Invalid domain: %s"}`, err.Error()), 400)
		return
	}

	// Create task directly to get taskID for tracking
	queueSvc := services.NewCertQueueService(services.GetDragonfly().Options().Addr)
	task := &services.CertTask{
		Type:     services.TaskIssue,
		Domain:   normalizedDomain,
		Email:    body.Email,
		Provider: body.Provider,
		Cert:     body.Cert,
		Key:      body.Key,
	}

	if err := queueSvc.EnqueueTask(r.Context(), task); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	// Also queue reload for manual/cloudflare certs
	if body.Provider == string(services.ProviderManual) || body.Provider == string(services.ProviderCloudflarePEM) {
		reloadTask := &services.CertTask{
			Type:   services.TaskReload,
			Domain: "system",
		}
		queueSvc.EnqueueTask(r.Context(), reloadTask)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Certificate task queued",
		"task_id": task.ID,
		"status":  "pending",
		"domain":  body.Domain,
	})
}


func (c *AdminController) ListCertificates(w http.ResponseWriter, r *http.Request) {
	certs := c.CertSvc.ListAvailableCerts()
	if certs == nil {
		certs = []string{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"domains": certs,
	})
}

func (c *AdminController) DeleteCertificate(w http.ResponseWriter, r *http.Request) {
	domain := chi.URLParam(r, "domain")

	// Normalizar domínio (converter IDN para Punycode se necessário)
	normalizedDomain, err := services.NormalizeDomain(domain)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"Invalid domain: %s"}`, err.Error()), 400)
		return
	}

	err = c.CertSvc.DeleteCertificate(r.Context(), services.SystemPool, normalizedDomain)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"success":true,"deleted":"` + normalizedDomain + `"}`))
}

// GetCertTaskStatus returns the status of a certificate task
func (c *AdminController) GetCertTaskStatus(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "taskId")
	if taskID == "" {
		http.Error(w, `{"error":"Task ID required"}`, 400)
		return
	}

	queueSvc := services.NewCertQueueService(services.GetDragonfly().Options().Addr)
	task, err := queueSvc.GetTaskStatus(r.Context(), taskID)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), 404)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"id":         task.ID,
		"type":       task.Type,
		"domain":     task.Domain,
		"status":     task.Status,
		"message":    task.Message,
		"created_at": task.CreatedAt,
		"updated_at": task.UpdatedAt,
	})
}

// RebuildNginx regenerates nginx configs - called by cert-controller
func (c *AdminController) RebuildNginx(w http.ResponseWriter, r *http.Request) {
	// Verify internal secret for cert-controller calls
	// Aceita tanto X-Cascata-Internal-Key (novo) quanto X-Internal-Secret (legado)
	internalSecret := r.Header.Get("X-Cascata-Internal-Key")
	if internalSecret == "" {
		internalSecret = r.Header.Get("X-Internal-Secret") // fallback legado
	}
	if internalSecret != os.Getenv("INTERNAL_CTRL_SECRET") {
		secretPreview := ""
		if len(internalSecret) > 10 {
			secretPreview = internalSecret[:10] + "..."
		} else if internalSecret != "" {
			secretPreview = internalSecret + " (short)"
		} else {
			secretPreview = "(empty)"
		}
		log.Printf("[RebuildNginx] Unauthorized - header missing or invalid (got: %s)", secretPreview)
		http.Error(w, `{"error":"Unauthorized"}`, 401)
		return
	}

	if err := services.RebuildNginxConfigs(r.Context(), services.SystemPool); err != nil {
		log.Printf("[AdminController] Failed to rebuild nginx configs: %v", err)
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), 500)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Nginx configurations rebuilt successfully",
	})
}

func normalizeSecurityStringSlice(raw interface{}) []string {
	var out []string
	switch v := raw.(type) {
	case []interface{}:
		out = make([]string, 0, len(v))
		for _, item := range v {
			if s := strings.TrimSpace(fmt.Sprintf("%v", item)); s != "" {
				out = append(out, s)
			}
		}
	case []string:
		out = make([]string, 0, len(v))
		for _, item := range v {
			if s := strings.TrimSpace(item); s != "" {
				out = append(out, s)
			}
		}
	case string:
		parts := strings.Split(v, ",")
		out = make([]string, 0, len(parts))
		for _, part := range parts {
			if s := strings.TrimSpace(part); s != "" {
				out = append(out, s)
			}
		}
	}
	if out == nil {
		return []string{}
	}
	return out
}

func securityStringSet(raw interface{}) map[string]bool {
	set := make(map[string]bool)
	for _, item := range normalizeSecurityStringSlice(raw) {
		set[strings.ToLower(strings.TrimSpace(item))] = true
	}
	return set
}

func securityOperationSet(raw interface{}) map[string]bool {
	set := make(map[string]bool)
	for _, item := range normalizeSecurityStringSlice(raw) {
		switch strings.ToLower(strings.TrimSpace(item)) {
		case "read", "select":
			set["read"] = true
		case "create", "insert":
			set["create"] = true
		case "update", "patch", "put":
			set["update"] = true
		case "delete", "remove":
			set["delete"] = true
		case "write", "mutation", "mutate":
			set["create"] = true
			set["update"] = true
			set["delete"] = true
		case "crud", "all", "*":
			set["read"] = true
			set["create"] = true
			set["update"] = true
			set["delete"] = true
		}
	}
	return set
}

func securityFactorSet(raw interface{}) map[string]bool {
	set := make(map[string]bool)
	for _, item := range normalizeSecurityStringSlice(raw) {
		switch strings.ToLower(strings.TrimSpace(item)) {
		case "totp/mfa", "mfa", "totp":
			set["totp"] = true
		case "email_otp", "email", "otp":
			set["otp"] = true
		case "biometria", "passkey":
			set["passkey"] = true
		}
	}
	return set
}

// isSecurityDowngrade detects if the new metadata state downgrades or removes any security locks, masks, auto-clock, or table step-up configurations.
func isSecurityDowngrade(existing, new map[string]interface{}) bool {
	lockTiers := map[string]int{
		"unlocked":          0,
		"insert_only":       1,
		"immutable":         2,
		"service_role_only": 3,
		"code_protected":     4,
		"otp_protected":     4,
		"auto_clock":        5,
	}
	maskTiers := map[string]int{
		"unmasked":  0,
		"blur":      1,
		"mask":      2,
		"semi-mask": 3,
		"hide":      4,
		"encrypt":   5,
	}

	// 1. Check Locked Columns
	var oldLocks, newLocks map[string]interface{}
	if ol, ok := existing["locked_columns"].(map[string]interface{}); ok {
		oldLocks = ol
	}
	if nl, ok := new["locked_columns"].(map[string]interface{}); ok {
		newLocks = nl
	}

	// For each table in old locks
	for table, colMapObj := range oldLocks {
		oldCols, ok1 := colMapObj.(map[string]interface{})
		if !ok1 {
			continue
		}
		
		newColMapObj, existsTable := newLocks[table]
		var newCols map[string]interface{}
		if existsTable {
			if nc, ok2 := newColMapObj.(map[string]interface{}); ok2 {
				newCols = nc
			}
		}

		for col, oldLevelVal := range oldCols {
			oldLevel, _ := oldLevelVal.(string)
			if oldLevel == "" || oldLevel == "unlocked" {
				continue
			}
			
			// If table or column doesn't exist in new locks, it was removed (downgraded to unlocked)
			if !existsTable || newCols == nil {
				return true
			}
			newLevelVal, existsCol := newCols[col]
			if !existsCol || newLevelVal == nil {
				return true
			}
			newLevel, _ := newLevelVal.(string)
			if lockTiers[newLevel] < lockTiers[oldLevel] {
				return true
			}
		}
	}

	// 2. Check Masked Columns
	var oldMasks, newMasks map[string]interface{}
	if om, ok := existing["masked_columns"].(map[string]interface{}); ok {
		oldMasks = om
	}
	if nm, ok := new["masked_columns"].(map[string]interface{}); ok {
		newMasks = nm
	}

	for table, colMapObj := range oldMasks {
		oldCols, ok1 := colMapObj.(map[string]interface{})
		if !ok1 {
			continue
		}
		
		newColMapObj, existsTable := newMasks[table]
		var newCols map[string]interface{}
		if existsTable {
			if nc, ok2 := newColMapObj.(map[string]interface{}); ok2 {
				newCols = nc
			}
		}

		for col, oldLevelVal := range oldCols {
			oldLevel, _ := oldLevelVal.(string)
			if oldLevel == "" || oldLevel == "unmasked" {
				continue
			}
			
			if !existsTable || newCols == nil {
				return true
			}
			newLevelVal, existsCol := newCols[col]
			if !existsCol || newLevelVal == nil {
				return true
			}
			newLevel, _ := newLevelVal.(string)
			if maskTiers[newLevel] < maskTiers[oldLevel] {
				return true
			}
		}
	}

	// 3. Check Auto-Clock Columns (removing is a downgrade)
	var oldAC, newAC map[string]interface{}
	if oac, ok := existing["auto_clock_columns"].(map[string]interface{}); ok {
		oldAC = oac
	}
	if nac, ok := new["auto_clock_columns"].(map[string]interface{}); ok {
		newAC = nac
	}

	for table, colMapObj := range oldAC {
		oldCols, ok1 := colMapObj.(map[string]interface{})
		if !ok1 {
			continue
		}
		
		newColMapObj, existsTable := newAC[table]
		var newCols map[string]interface{}
		if existsTable {
			if nc, ok2 := newColMapObj.(map[string]interface{}); ok2 {
				newCols = nc
			}
		}

		for col := range oldCols {
			if !existsTable || newCols == nil {
				return true
			}
			if _, existsCol := newCols[col]; !existsCol {
				return true
			}
		}
	}

	// 4. Check table-level step-up security (removing operations or loosening factors is a downgrade)
	var oldTableSecurity, newTableSecurity map[string]interface{}
	if ots, ok := existing["table_security"].(map[string]interface{}); ok {
		oldTableSecurity = ots
	}
	if nts, ok := new["table_security"].(map[string]interface{}); ok {
		newTableSecurity = nts
	}

	for table, oldRuleObj := range oldTableSecurity {
		oldRule, ok := oldRuleObj.(map[string]interface{})
		if !ok {
			continue
		}
		oldOps := securityOperationSet(oldRule["operations"])
		oldFactors := securityFactorSet(oldRule["allowed_factors"])
		if len(oldOps) == 0 {
			continue
		}

		newRuleObj, existsTable := newTableSecurity[table]
		if !existsTable || newRuleObj == nil {
			return true
		}
		newRule, ok := newRuleObj.(map[string]interface{})
		if !ok {
			return true
		}
		newOps := securityOperationSet(newRule["operations"])
		newFactors := securityFactorSet(newRule["allowed_factors"])

		for op := range oldOps {
			if !newOps[op] {
				return true
			}
		}
		if len(oldFactors) != len(newFactors) {
			return true
		}
		for factor := range oldFactors {
			if !newFactors[factor] {
				return true
			}
		}
	}

	return false
}
