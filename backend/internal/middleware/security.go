package middleware

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"cascata-backend/internal/services"
	"cascata-backend/internal/types"
)

// DynamicCORS handles project-specific and client-specific CORS policies
func DynamicCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		
		// Standard Headers for Supabase/PostgREST compatibility
		w.Header().Set("Access-Control-Allow-Credentials", "true")
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PUT,PATCH,DELETE,OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type,Authorization,apikey,x-cascata-client,Prefer,Range,x-client-info,x-supabase-auth,content-profile,accept-profile,x-supabase-api-version,x-cascata-signature,x-cascata-event")
		w.Header().Set("Access-Control-Expose-Headers", "Content-Range, X-Total-Count, Link")

		// Always set Access-Control-Allow-Origin if there's an Origin header
		// This is critical for CORS to work with low-code tools like FlutterFlow
		if origin != "" {
			// Webhooks explicitly allow any origin (e.g. from Facebook, Postman, etc)
			isWebhook := strings.HasPrefix(r.URL.Path, "/webhook/") || strings.HasPrefix(r.URL.Path, "/api/webhooks/in/")
			if isWebhook {
				w.Header().Set("Access-Control-Allow-Origin", "*")
			} else {
				val := r.Context().Value(types.CascataCtxKey)
				if val == nil {
					// No project context - allow the requesting origin (dev mode behavior)
					w.Header().Set("Access-Control-Allow-Origin", origin)
				} else {
					ctx := val.(*types.CascataRequest)
				if ctx.Project != nil {
					// Priority: AppClient-specific CORS (Multi-App Architecture)
					// If request came via an App Client key, use that app's allowed origins
					if ctx.AppClient != nil && len(ctx.AppClient.AllowedOrigins) > 0 {
						originAllowed := false
						for _, o := range ctx.AppClient.AllowedOrigins {
							if o == "*" || o == origin || matchWildcardOrigin(o, origin) {
								originAllowed = true
								break
							}
						}
						if originAllowed {
							w.Header().Set("Access-Control-Allow-Origin", origin)
							// Log for debugging
							log.Printf("[DynamicCORS] AppClient %s allowed origin: %s", ctx.AppClient.ID, origin)
						}
						// Note: If origin not allowed, we don't set the header (browser will block)
					} else {
						// Fallback: Project-level CORS
						allowedOrigins := ctx.Project.Metadata.AllowedOrigins
						originAllowed := false
						if len(allowedOrigins) == 0 {
							// Dev mode: allow localhost
							if strings.HasPrefix(origin, "http://localhost") || strings.HasPrefix(origin, "http://127.0.0.1") {
								originAllowed = true
							}
						} else {
							// Check against allowed list
							for _, o := range allowedOrigins {
								if o == "*" || o == origin || matchWildcardOrigin(o, origin) {
									originAllowed = true
									break
								}
							}
						}
						if originAllowed {
							w.Header().Set("Access-Control-Allow-Origin", origin)
						}
						// Note: If origin not allowed, we don't set the header (browser will block)
					}
				} else {
					// System route with context but no project - allow the origin
					w.Header().Set("Access-Control-Allow-Origin", origin)
				}
			}
			}
		}

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// HostGuard performs 404 stealth blocking
func HostGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		val := r.Context().Value(types.CascataCtxKey)
		if val != nil {
			ctx := val.(*types.CascataRequest)
			if ctx.Project != nil {
				next.ServeHTTP(w, r)
				return
			}
		}
		
		// DEBUG: Log para investigar 404 em GET /rest/v1/
		log.Printf("[HostGuard] Project nil - Method: %s, Path: %s", r.Method, r.URL.Path)

		path := r.URL.Path
		if path == "/" || path == "/health" {
			next.ServeHTTP(w, r)
			return
		}

		host := r.Host
		if strings.Contains(host, ":") { host = strings.Split(host, ":")[0] }

		if isLocal(host) || isIP(host) || isInternalDockerHost(host) {
			next.ServeHTTP(w, r)
			return
		}

		// Stealth Block
		log.Printf("[HostGuard] 404 Stealth Block: %s via %s", path, host)
		http.Error(w, "Not Found", 404)
	})
}

// DynamicBodyParser applies project-specific payload limits
func DynamicBodyParser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Bypass para uploads de arquivo - não aplicar limite de JSON em multipart/form-data
		contentType := r.Header.Get("Content-Type")
		if strings.Contains(contentType, "multipart/form-data") {
			next.ServeHTTP(w, r)
			return
		}
		
		limit := int64(2 * 1024 * 1024) // 2MB Default
		
		val := r.Context().Value(types.CascataCtxKey)
		if val != nil {
			ctx := val.(*types.CascataRequest)
			if ctx.Project != nil && ctx.Project.Metadata.Security.MaxJsonSize != "" {
				limit = parseBytes(ctx.Project.Metadata.Security.MaxJsonSize)
			}
		}

		r.Body = http.MaxBytesReader(w, r.Body, limit)
		next.ServeHTTP(w, r)
	})
}

// DynamicRateLimiter applies logical resource rate limiting
func DynamicRateLimiter(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		val := r.Context().Value(types.CascataCtxKey)
		if val == nil {
			next.ServeHTTP(w, r)
			return
		}
		ctx := val.(*types.CascataRequest)
		if ctx.Project == nil {
			next.ServeHTTP(w, r)
			return
		}

		ip := getClientIP(r)
		
		// Track RPS for dashboard display (non-blocking, Dragonfly in-memory)
		services.TrackGlobalRPS(ctx.Project.Slug)
		
		// Path Normalization
		logicalResource := normalizePath(r.URL.Path, ctx.Project.Slug)
		
		apiKey := r.Header.Get("apikey")
		if apiKey == "" { apiKey = r.URL.Query().Get("apikey") }

		// services.CheckRateLimit is already updated to handle ctx.ProjectPool as interface{}
		result := services.CheckRateLimit(r.Context(), ctx.ProjectPool, ctx.Project.Slug, logicalResource, r.Method, string(ctx.UserRole), ip, apiKey)

		if result.Blocked {
			w.Header().Set("Retry-After", fmt.Sprintf("%d", result.RetryAfter))
			w.WriteHeader(http.StatusTooManyRequests)
			json.NewEncoder(w).Encode(result)
			return
		}

		if result.Limit > 0 {
			w.Header().Set("X-RateLimit-Limit", fmt.Sprintf("%d", result.Limit))
			w.Header().Set("X-RateLimit-Remaining", fmt.Sprintf("%d", result.Remaining))
		}

		next.ServeHTTP(w, r)
	})
}

// Helpers
func isIP(h string) bool { return strings.Contains(h, ".") }
func isLocal(h string) bool { return h == "localhost" || h == "127.0.0.1" }

// isInternalDockerHost verifica se o hostname é um serviço interno do Docker Compose
// permitindo comunicação entre containers (backend_control, backend_data, nginx_controller, etc.)
func isInternalDockerHost(h string) bool {
	internalHosts := []string{
		"backend_control",
		"backend_data",
		"backend_data_1",
		"backend_data_2",
		"backend_data_3",
		"backend_data_4",
		"nginx_controller",
		"nginx-controller",
		"cert_controller",
		"cert-controller",
		"dragonfly",
		"db",
		"postgres",
		"redis",
	}
	for _, internal := range internalHosts {
		if h == internal {
			return true
		}
	}
	// Permitir também hostnames que terminam com _control, _data (padrão Docker Compose)
	if strings.HasSuffix(h, "_control") || strings.HasSuffix(h, "_data") ||
	   strings.HasSuffix(h, "-control") || strings.HasSuffix(h, "-data") ||
	   strings.HasSuffix(h, "_controller") || strings.HasSuffix(h, "-controller") {
		return true
	}
	return false
}
func parseBytes(s string) int64 {
	// Simple KB, MB, GB parser
	s = strings.ToUpper(s)
	if strings.HasSuffix(s, "MB") {
		val, _ := strconv.ParseInt(strings.TrimSuffix(s, "MB"), 10, 64)
		return val * 1024 * 1024
	}
	if strings.HasSuffix(s, "KB") {
		val, _ := strconv.ParseInt(strings.TrimSuffix(s, "KB"), 10, 64)
		return val * 1024
	}
	val, _ := strconv.ParseInt(s, 10, 64)
	return val
}

func getClientIP(r *http.Request) string {
	if ip := r.Header.Get("X-Real-IP"); ip != "" { return ip }
	if ip := r.Header.Get("X-Forwarded-For"); ip != "" { return strings.Split(ip, ",")[0] }
	return r.RemoteAddr
}

// matchWildcardOrigin checks if an origin matches a wildcard pattern
// e.g., "https://*.example.com" matches "https://app.example.com"
func matchWildcardOrigin(pattern, origin string) bool {
	if !strings.Contains(pattern, "*") {
		return pattern == origin
	}
	
	// Convert wildcard pattern to regex
	// Escape special regex characters except *
	parts := strings.Split(pattern, "*")
	for i, part := range parts {
		parts[i] = regexp.QuoteMeta(part)
	}
	regexPattern := "^" + strings.Join(parts, ".*") + "$"
	
	matched, _ := regexp.MatchString(regexPattern, origin)
	return matched
}

// PanicMode enforces project lockdown status from Dragonfly (Hard Security)
// This is edge defense - blocks requests BEFORE they reach PostgreSQL
// Whitelists the admin who activated the panic mode (IP or UserID)
func PanicMode(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		val := r.Context().Value(types.CascataCtxKey)
		if val == nil {
			next.ServeHTTP(w, r)
			return
		}
		ctx := val.(*types.CascataRequest)

		// Skip for health checks
		if r.URL.Path == "/" || r.URL.Path == "/health" {
			next.ServeHTTP(w, r)
			return
		}

		// Only check if we have a project context
		if ctx.Project == nil {
			next.ServeHTTP(w, r)
			return
		}

		// Check Dragonfly for panic mode (in-memory, no PostgreSQL hit)
		if services.CheckPanic(ctx.Project.Slug) {
			// Verifica se é o admin que ativou o panic (whitelisting)
			// Prioriza UserID do JWT se disponível, senão usa IP
			identifier := getClientIP(r)
			if ctx.User != nil {
				if sub, ok := ctx.User["sub"].(string); ok && sub != "" {
					identifier = sub
				}
			}
			
			// Se for o admin que ativou, permite passar (bypass panic)
			if services.IsAdminWhitelisted(ctx.Project.Slug, identifier) {
				next.ServeHTTP(w, r)
				return
			}
			
			// Se for system request (dashboard), também permite
			if ctx.IsSystemRequest {
				next.ServeHTTP(w, r)
				return
			}

			// Bloqueia request - sistema em lockdown
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error": "System is currently in Panic Mode (Locked Down).",
				"code":  "PANIC_MODE",
			})
			return
		}

		next.ServeHTTP(w, r)
	})
}

func normalizePath(path, slug string) string {
	p := strings.TrimPrefix(path, "/api/data/"+slug)
	if strings.Contains(p, "/tables/") {
		parts := strings.Split(p, "/tables/")
		if len(parts) > 1 { return "table:" + strings.Split(parts[1], "/")[0] }
	}
	if strings.Contains(p, "/auth/v1/") { return "auth:*" }
	return p
}
