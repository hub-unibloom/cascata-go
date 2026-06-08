package middleware

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"cascata-backend/internal/services"
	"cascata-backend/internal/types"
)

type responseRecorder struct {
	http.ResponseWriter
	statusCode int
	size       int
}

func (r *responseRecorder) WriteHeader(statusCode int) {
	r.statusCode = statusCode
	r.ResponseWriter.WriteHeader(statusCode)
}

func (r *responseRecorder) Write(b []byte) (int, error) {
	size, err := r.ResponseWriter.Write(b)
	r.size += size
	return size, err
}

// AuditLogger performs PII-scrubbed logging and semantic action tracking
func AuditLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Bypass for health and streaming
		if r.URL.Path == "/health" || strings.Contains(r.URL.Path, "/realtime") {
			next.ServeHTTP(w, r)
			return
		}

		start := time.Now()
		
		// Verifica se é upload de arquivo - não capturar body binário
		contentType := r.Header.Get("Content-Type")
		isFileUpload := strings.Contains(contentType, "multipart/form-data") ||
			strings.Contains(contentType, "application/octet-stream") ||
			strings.Contains(r.URL.Path, "/storage/") && r.Method == "POST"
		
		var bodyBytes []byte
		var rawPayload interface{}
		
		if !isFileUpload {
			// Capture Body for processing (with safety limit)
			bodyBytes, _ = io.ReadAll(io.LimitReader(r.Body, 1*1024*1024)) // 1MB log limit
			r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
		} else {
			// Para uploads de arquivo, não captura o body, apenas marca como upload
			rawPayload = map[string]interface{}{
				"_note":         "File upload - body omitted",
				"_content_type": contentType,
			}
		}

		rr := &responseRecorder{ResponseWriter: w, statusCode: http.StatusOK}
		
		next.ServeHTTP(rr, r)

		val := r.Context().Value(types.CascataCtxKey)
		if val != nil {
			ctx := val.(*types.CascataRequest)
			if ctx.Project != nil {
				duration := time.Since(start)
				clientIP := getClientIPForLogging(r)
				
				// Detect Semantic Action
				action := detectSemanticAction(r.Method, r.URL.Path)

				// PII Scrubbing - MELHORADO: Suporte a múltiplos Content-Types
				// NOTA: rawPayload já pode ter sido definido acima para uploads de arquivo
				
				if rawPayload == nil && len(bodyBytes) > 0 {
					// Tentar JSON primeiro (mais comum)
					if strings.Contains(contentType, "application/json") || 
					   strings.HasPrefix(strings.TrimSpace(string(bodyBytes)), "{") ||
					   strings.HasPrefix(strings.TrimSpace(string(bodyBytes)), "[") {
						var jsonPayload interface{}
						if err := json.Unmarshal(bodyBytes, &jsonPayload); err == nil {
							rawPayload = jsonPayload
						} else {
							// JSON inválido, capturar como string
							rawPayload = map[string]interface{}{
								"_raw_body":   string(bodyBytes),
								"_parse_error": err.Error(),
								"_content_type": contentType,
							}
						}
					} else if strings.Contains(contentType, "application/x-www-form-urlencoded") {
						// Parse form data
						if parsedForm, err := url.ParseQuery(string(bodyBytes)); err == nil {
							formMap := map[string]interface{}{}
							for key, values := range parsedForm {
								if len(values) == 1 {
									formMap[key] = values[0]
								} else {
									formMap[key] = values
								}
							}
							rawPayload = formMap
						} else {
							rawPayload = map[string]interface{}{"_raw_body": string(bodyBytes)}
						}
					} else {
						// Outros tipos: capturar como string base64 ou raw
						rawPayload = map[string]interface{}{
							"_raw_body":     string(bodyBytes),
							"_content_type": contentType,
							"_size":         len(bodyBytes),
						}
					}
				}
				
				if rawPayload == nil {
					rawPayload = map[string]interface{}{}
				}
				
				// Scrubbing de PII - funciona com qualquer tipo
				scrubbedBody := scrubPayload(rawPayload)

				// Webhook Dispatch (Success Write Ops)
				if rr.statusCode >= 200 && rr.statusCode < 300 && isWriteMethod(r.Method) {
					services.DispatchWebhook(r.Context(), ctx.Project.Slug, "*", action, rawPayload, ctx.Project.JWTSecret)
				}

				// Detect if request is internal (dashboard or inter-container)
				geoInfo := buildGeoInfo(r, clientIP)

				// Firehose Buffer for persistent audit logs
				// CAPTURE: Todos os acessos (API externa, SQL interno, etc)
				services.BufferAuditLog(services.AuditLog{
					ProjectSlug:  ctx.Project.Slug,
					Method:       r.Method,
					Path:         r.URL.Path,
					Host:         r.Host, // NOVO: Qual domínio foi usado (custom vs default)
					RawQuery:     r.URL.RawQuery, // NOVO: Query params (?select=*&limit=10)
					StatusCode:   rr.statusCode,
					ClientIP:     clientIP,
					DurationMs:   duration.Milliseconds(),
					UserRole:     string(ctx.UserRole),
					IsSystemRole: ctx.IsSystemRequest, // NOVO: Se é service_role
					Payload:      scrubbedBody,
					Headers:      extractRelevantHeaders(r), // NOVO: Headers auditáveis
					GeoInfo:      geoInfo,
					ResponseSize: rr.size,
				})
			}
		}
	})
}

// Security Scrubbing
// NOTA: SQL queries em '/query' são preservados - não scrubbamos comandos SQL legítimos
var sensitiveKeys = []string{"password", "token", "secret", "api_key", "authorization", "cvv", "master_secret", "bearer"}

// extractRelevantHeaders extrai headers auditáveis (sem dados sensíveis)
func extractRelevantHeaders(r *http.Request) map[string]interface{} {
	headers := map[string]interface{}{}
	
	// Headers indicativos de tipo de acesso
	if r.Header.Get("x-cascata-client") == "dashboard" {
		headers["client_type"] = "dashboard" // SQL Editor, painel admin
	}
	if r.Header.Get("apikey") != "" {
		headers["auth_type"] = "api_key"
	}
	if r.Header.Get("authorization") != "" {
		headers["auth_type"] = "jwt_bearer"
	}
	
	// ADD ORIGIN AND USER-AGENT HERE
	if origin := r.Header.Get("Origin"); origin != "" {
		headers["origin"] = origin
	}
	if ua := r.UserAgent(); ua != "" {
		headers["user_agent"] = ua
	}
	if cfip := r.Header.Get("cf-connecting-ip"); cfip != "" {
		headers["cf_connecting_ip"] = cfip
	}
	
	// Indicador de acesso interno/externo
	if isPrivateOrDockerIP(r.RemoteAddr) {
		headers["network"] = "internal"
	} else {
		headers["network"] = "external"
	}
	
	return headers
}

func scrubPayload(m interface{}) interface{} {
	switch v := m.(type) {
	case map[string]interface{}:
		for key := range v {
			if isSensitive(key) {
				v[key] = "***REDACTED***"
			} else {
				v[key] = scrubPayload(v[key])
			}
		}
	case []interface{}:
		for i := range v {
			v[i] = scrubPayload(v[i])
		}
	}
	return m
}

func isSensitive(key string) bool {
	k := strings.ToLower(key)
	for _, s := range sensitiveKeys {
		if strings.Contains(k, s) { return true }
	}
	return false
}

func isWriteMethod(m string) bool {
	return m == http.MethodPost || m == http.MethodPut || m == http.MethodPatch || m == http.MethodDelete
}

func detectSemanticAction(method, path string) string {
	if strings.Contains(path, "/tables") {
		if method == http.MethodPost { return "CREATE_TABLE" }
		if method == http.MethodDelete { return "DROP_TABLE" }
	}
	if strings.Contains(path, "/signup") { return "SIGNUP" }
	if strings.Contains(path, "/token") { return "LOGIN" }
	return method // Fallback
}

func getClientIPForLogging(r *http.Request) string {
	if ip := r.Header.Get("X-Real-IP"); ip != "" { return ip }
	if ip := r.Header.Get("X-Forwarded-For"); ip != "" { return strings.Split(ip, ",")[0] }
	return r.RemoteAddr
}

// buildGeoInfo detecta se o request é interno (dashboard ou entre containers)
func buildGeoInfo(r *http.Request, clientIP string) map[string]interface{} {
	isInternal := false

	// 1. Check header x-cascata-client (dashboard indicator)
	if r.Header.Get("x-cascata-client") == "dashboard" {
		isInternal = true
	}

	// 2. Check if IP is private/Docker network
	if isPrivateOrDockerIP(clientIP) {
		isInternal = true
	}

	// 3. Check User-Agent for internal services
	userAgent := strings.ToLower(r.UserAgent())
	if strings.Contains(userAgent, "internal") || strings.Contains(userAgent, "cascata-api") {
		isInternal = true
	}

	return map[string]interface{}{
		"is_internal": isInternal,
		"client_ip":   clientIP,
		"user_agent":  r.UserAgent(),
	}
}

// isPrivateOrDockerIP verifica se o IP é privado ou da rede Docker
func isPrivateOrDockerIP(ip string) bool {
	// Remove port if present
	if strings.Contains(ip, ":") {
		ip = strings.Split(ip, ":")[0]
	}

	// Docker networks
	if strings.HasPrefix(ip, "172.") || strings.HasPrefix(ip, "10.") || strings.HasPrefix(ip, "192.168.") {
		return true
	}

	// Localhost
	if ip == "127.0.0.1" || ip == "::1" || ip == "localhost" {
		return true
	}

	return false
}
