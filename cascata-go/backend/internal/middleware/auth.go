package middleware

import (
	"context"
	"log"
	"net/http"
	"os"
	"strings"

	"cascata-backend/internal/services"
	"cascata-backend/internal/types"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var authSvc = services.AuthService{} // Local instance for middleware usage

// Helper functions to extract values from context
func GetCascataRequest(ctx context.Context) *types.CascataRequest {
	val := ctx.Value(types.CascataCtxKey)
	if val == nil {
		return nil
	}
	return val.(*types.CascataRequest)
}

func GetUserID(ctx context.Context) (string, bool) {
	req := GetCascataRequest(ctx)
	if req == nil || req.User == nil {
		return "", false
	}
	if sub, ok := req.User["sub"].(string); ok {
		return sub, true
	}
	return "", false
}

func GetUserRole(ctx context.Context) (string, bool) {
	req := GetCascataRequest(ctx)
	if req == nil {
		return "", false
	}
	return string(req.UserRole), true
}

func GetProjectSlug(ctx context.Context) string {
	req := GetCascataRequest(ctx)
	if req == nil || req.Project == nil {
		return ""
	}
	return req.Project.Slug
}

func GetProjectPool(r *http.Request) *pgxpool.Pool {
	req := GetCascataRequest(r.Context())
	if req == nil {
		return nil
	}
	return req.ProjectPool
}

// CascataAuth performs role assignment based on token hierarchy
func CascataAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Mandatory context retrieval (Ensured by ProjectResolver)
		val := r.Context().Value(types.CascataCtxKey)
		if val == nil {
			// This case should be rare but we fail-safe
			next.ServeHTTP(w, r)
			return
		}
		ctx := val.(*types.CascataRequest)

		// 1. SYSTEM ADMIN / DASHBOARD (MANAGEMENT PLANE)
		// If path is Control Plane or System Request, we check the Master Secret Layer.
		if ctx.IsSystemRequest {
			ctx.UserRole = types.RoleService
			next.ServeHTTP(w, r)
			return
		}

		// Authorization Discovery (Bearer, Query, or Cookies/Dashboard)
		bearerToken := ""
		authHeader := r.Header.Get("Authorization")
		apiKey := r.Header.Get("apikey")
		if strings.HasPrefix(authHeader, "Bearer ") {
			bearerToken = strings.TrimPrefix(authHeader, "Bearer ")
		}

		// 1.1 WEBHOOK DIPLOMATIC CORRIDOR (Refined)
		// We allow webhooks to pass if no auth is provided (public), but we DON'T 
		// return early if there IS a token, so that registered users can be identified.
		isWebhook := strings.HasPrefix(r.URL.Path, "/webhook/") || strings.HasPrefix(r.URL.Path, "/api/webhooks/in/")
		
		if isWebhook && bearerToken == "" && apiKey == "" {
			if ctx.Project != nil {
				ctx.UserRole = types.RoleAnon
				log.Printf("[CascataAuth] Webhook Diplomatic Corridor OPEN (Public Access)")
				next.ServeHTTP(w, r)
				return
			}
		}

		if bearerToken == "" {
			bearerToken = r.URL.Query().Get("access_token")
			if bearerToken == "" {
				bearerToken = r.URL.Query().Get("token")
			}
		}

		// Dashboard Cookies Parity
		if bearerToken == "" {
			if cookie, err := r.Cookie("cascata_session"); err == nil {
				bearerToken = cookie.Value
			} else if cookie, err := r.Cookie("admin_token"); err == nil {
				bearerToken = cookie.Value
			}
		}


		// Case A: Administrative Control Plane Protection
		if strings.HasPrefix(r.URL.Path, "/api/control") {
			// EXCEPTION: Rotas de bootstrap público ou autenticadas via Internal Secret — sem JWT necessário.
			// Portando whitelist do TypeScript original (core.ts PUBLIC_PATHS).
			// - handshake/login: pré-autenticação
			// - sovereign/unseal: bootstrap de segurança pós-reboot (Elite mode)
			// - sovereign/status: read-only, não expõe segredos
			// - system/rebuild-nginx: autenticado via X-Cascata-Internal-Key (IsSystemRequest)
			if strings.HasSuffix(r.URL.Path, "/auth/handshake") ||
				strings.HasSuffix(r.URL.Path, "/auth/login") ||
				strings.HasSuffix(r.URL.Path, "/auth/sovereign/unseal") ||
				strings.HasSuffix(r.URL.Path, "/auth/sovereign/status") ||
				r.URL.Path == "/api/control/system/rebuild-nginx" {
				next.ServeHTTP(w, r)
				return
			}

			// Authentication required for other control plane routes
			if bearerToken == "" {
				log.Printf("[CascataAuth] Control Plane DENIED - No bearer token for %s", r.URL.Path)
				http.Error(w, `{"error":"Admin Authentication Required"}`, 401)
				return
			}
			
			// DEBUG: Log token presence (truncated for security)
			tokenPreview := ""
			if len(bearerToken) > 20 {
				tokenPreview = bearerToken[:20] + "..."
			} else {
				tokenPreview = bearerToken
			}
			log.Printf("[CascataAuth] Validating token for %s - Token preview: %s", r.URL.Path, tokenPreview)
			
			// Validate using SYSTEM_JWT_SECRET (Master Level)
			claims := jwt.MapClaims{}
			token, err := jwt.ParseWithClaims(bearerToken, claims, func(t *jwt.Token) (interface{}, error) {
				return []byte(os.Getenv("SYSTEM_JWT_SECRET")), nil
			})

			if err == nil && token.Valid {
				ctx.User = claims
				ctx.UserRole = types.RoleService // Admin status
				log.Printf("[CascataAuth] Control Plane GRANTED - Valid admin token for %s, role: %s", r.URL.Path, ctx.UserRole)
				next.ServeHTTP(w, r)
				return
			}
			
			log.Printf("[CascataAuth] Control Plane DENIED - Invalid token for %s: %v", r.URL.Path, err)
			http.Error(w, `{"error":"Unauthorized: Invalid Admin Token"}`, 401)
			return
		}

		// Case B: Multi-Tenancy Project Data Access
		if ctx.Project != nil {
			// ═══════════════════════════════════════════════════════════════════
			// FORTRESS MODE CHECK: Bloqueia acesso anônimo mesmo em /auth/v1/*
			// EM FORTRESS MODE: Nenhuma rota aceita anon - tudo requer auth
			// ═══════════════════════════════════════════════════════════════════
			isFortress := os.Getenv("CASCATA_FORTRESS_MODE") == "enabled"

			// Public Auth Paths (v1 internal Supabase parity)
			// NOTA: /auth/v1/user requer autenticação - não é público
			if strings.Contains(r.URL.Path, "/auth/v1/") && !strings.HasSuffix(r.URL.Path, "/user") && !strings.Contains(r.URL.Path, "/mfa/") {
				// EM FORTRESS MODE: Bloqueia acesso anônimo total
				if isFortress {
					// Só permite passar com App Client key válido (nunca anon puro)
					apiKey := r.Header.Get("apikey")
					if apiKey == "" { apiKey = r.URL.Query().Get("apikey") }
					
					if apiKey != "" && strings.HasPrefix(apiKey, services.AppAnonKeyPrefix) {
						var appClient *types.AppClient
						if ctx.Project.AppClientIndex != nil {
							appClient = ctx.Project.AppClientIndex[apiKey]
						} else if len(ctx.Project.Metadata.AppClients) > 0 {
							appClient = services.ValidateAppAnonKey(apiKey, ctx.Project.Metadata.AppClients, ctx.Project.JWTSecret)
						}
						
						if appClient != nil {
							ctx.AppClient = appClient
							ctx.UserRole = types.RoleAnon
							log.Printf("[CascataAuth] [FORTRESS] App Client authenticated: %s (project: %s)", appClient.ID, ctx.Project.Slug)
							next.ServeHTTP(w, r)
							return
						}
					}
					
					// SEM App Client key = BLOQUEADO em Fortress Mode
					log.Printf("[CascataAuth] [FORTRESS] DENIED: Anonymous access blocked at %s", r.URL.Path)
					http.Error(w, `{"error":"Authentication Required - Fortress Mode Active"}`, 403)
					return
				}

				// Modo Normal (não Fortress): Mantém compatibilidade Supabase
				// First, check for App Client anon_key to enable Identity-Aware Key Bridging
				// This allows /authorize to know which App Client is making the request
				apiKey := r.Header.Get("apikey")
				if apiKey == "" { apiKey = r.URL.Query().Get("apikey") }
				
				// Check if this is an App Client key
				if apiKey != "" && strings.HasPrefix(apiKey, services.AppAnonKeyPrefix) {
					var appClient *types.AppClient
					if ctx.Project.AppClientIndex != nil {
						appClient = ctx.Project.AppClientIndex[apiKey]
					} else if len(ctx.Project.Metadata.AppClients) > 0 {
						appClient = services.ValidateAppAnonKey(apiKey, ctx.Project.Metadata.AppClients, ctx.Project.JWTSecret)
					}
					
					if appClient != nil {
						ctx.AppClient = appClient
						ctx.UserRole = types.RoleAnon
						log.Printf("[CascataAuth] App Client authenticated on public auth path: %s (project: %s)", appClient.ID, ctx.Project.Slug)
						next.ServeHTTP(w, r)
						return
					}
				}
				
				// No App Client key, proceed as anonymous
				ctx.UserRole = types.RoleAnon
				next.ServeHTTP(w, r)
				return
			}

			apiKey := r.Header.Get("apikey")
			if apiKey == "" { apiKey = r.URL.Query().Get("apikey") }

			matched := false

			// 0. GOD MODE: Master Admin Impersonation (Dashboard/Internal)
			// Check if the token is actually a Master Admin Token signed by SYSTEM_JWT_SECRET.
			// This allows the Dashboard to access any project data route with its master token.
			if bearerToken != "" {
				claims := jwt.MapClaims{}
				token, err := jwt.ParseWithClaims(bearerToken, claims, func(t *jwt.Token) (interface{}, error) {
					return []byte(os.Getenv("SYSTEM_JWT_SECRET")), nil
				})
				if err == nil && token.Valid {
					ctx.User = claims
					ctx.UserRole = types.RoleService
					matched = true
				}
			}

			// 1. JWT Identity (Project Specific)
			if !matched && bearerToken != "" && strings.Count(bearerToken, ".") == 2 {
				claims := jwt.MapClaims{}
				token, err := jwt.ParseWithClaims(bearerToken, claims, func(t *jwt.Token) (interface{}, error) {
					return []byte(ctx.Project.JWTSecret), nil
				})

				if err == nil && token.Valid {
					// SOVEREIGN PANIC GATE: Neuralization Signal Check
					if sub, ok := claims["sub"].(string); ok {
						if authSvc.CheckUserNeutralized(r.Context(), ctx.Project.Slug, sub) {
							http.Error(w, `{"error":"Identity Neutralized (Sovereign Panic Signal)"}`, 401)
							return
						}
					}
					ctx.User = claims
					if role, ok := claims["role"].(string); ok {
						ctx.UserRole = types.CascataUserRole(role)
					}
					matched = true
				}
			}
			
			// Try API Key comparisons... (previous matched = true paths continue below)


			// 2. Service/Internal Key Logic (Privileged Data Access)
			if !matched && (apiKey == ctx.Project.ServiceKey || bearerToken == ctx.Project.ServiceKey) {
				ctx.UserRole = types.RoleService
				matched = true
			}

			// 3. Anon Key Logic (Public Data Access)
			if !matched && (apiKey == ctx.Project.AnonKey || bearerToken == ctx.Project.AnonKey) {
				ctx.UserRole = types.RoleAnon
				matched = true
			}

			// 4. App Client Key Logic (Multi-App Architecture)
			// Checks for keys with "anon_" prefix that are app-specific
			// Uses O(1) cache index when available, falls back to O(n) validation
			if !matched {
				keyToCheck := apiKey
				if keyToCheck == "" {
					keyToCheck = bearerToken
				}
				
				if keyToCheck != "" && strings.HasPrefix(keyToCheck, services.AppAnonKeyPrefix) {
					var appClient *types.AppClient
					
					// O(1) lookup using cached index (preferred)
					if ctx.Project.AppClientIndex != nil {
						appClient = ctx.Project.AppClientIndex[keyToCheck]
					} else if len(ctx.Project.Metadata.AppClients) > 0 {
						// Fallback O(n) validation (when cache not built)
						appClient = services.ValidateAppAnonKey(keyToCheck, ctx.Project.Metadata.AppClients, ctx.Project.JWTSecret)
					}
					
					if appClient != nil {
						ctx.AppClient = appClient
						ctx.UserRole = types.RoleAnon
						matched = true
						log.Printf("[CascataAuth] App Client authenticated: %s (project: %s)", appClient.ID, ctx.Project.Slug)
					}
				}
			}

			if matched {
				// ═══════════════════════════════════════════════════════════════════
				// STEP-UP AUTHENTICATION INJECTION (UNIVERSAL PADLOCK)
				// ═══════════════════════════════════════════════════════════════════
				stepUpProvider := r.Header.Get("X-Cascata-Stepup-Provider")
				stepUpCode := r.Header.Get("X-Cascata-Stepup-Code")
				
				if stepUpProvider != "" && stepUpCode != "" && ctx.User != nil {
					provSlug := strings.ToLower(stepUpProvider)
					if provSlug == "passkey" || provSlug == "biometria" {
						provSlug = "biometria"
					}
					if provSlug == "totp/mfa" || provSlug == "mfa" {
						provSlug = "totp"
					}
					
					if sub, ok := ctx.User["sub"].(string); ok {
						if provSlug == "totp" {
							var secret string
							err := ctx.ProjectPool.QueryRow(r.Context(), "SELECT identifier FROM auth.identities WHERE user_id = $1 AND provider = 'totp' AND verified_at IS NOT NULL LIMIT 1", sub).Scan(&secret)
							if err == nil && authSvc.VerifyTOTP(secret, stepUpCode) {
								ctx.StepUpProviders = "totp"
								log.Printf("[CascataAuth] Step-Up MFA verified for user %s (Provider: totp)", sub)
							}
						} else {
							// AGNOSTIC OTP VALIDATION (Multi-Channel & Multi-Identity)
							// Instead of checking the login method (JWT claims), we check if the user 
							// has an identity registered for the requested stepUpProvider.
							var identifier string
							err := ctx.ProjectPool.QueryRow(r.Context(), "SELECT identifier FROM auth.identities WHERE user_id = $1 AND provider = $2 LIMIT 1", sub, provSlug).Scan(&identifier)
							
							if err == nil && identifier != "" {
								var storedCode string
								err := ctx.ProjectPool.QueryRow(r.Context(), "SELECT code FROM auth.otp_codes WHERE provider = $1 AND identifier = $2 AND expires_at > NOW()", provSlug, identifier).Scan(&storedCode)
								if err == nil && storedCode == stepUpCode {
									// Validated successfully! Burn the OTP code.
									ctx.ProjectPool.Exec(r.Context(), "DELETE FROM auth.otp_codes WHERE provider = $1 AND identifier = $2", provSlug, identifier)
									ctx.StepUpProviders = provSlug
									log.Printf("[CascataAuth] Step-Up MFA verified for user %s (Agnostic Provider: %s)", sub, provSlug)
								} else {
									log.Printf("[CascataAuth] Step-Up MFA failed for user %s (Agnostic Provider: %s)", sub, provSlug)
								}
							} else {
								log.Printf("[CascataAuth] Step-Up MFA rejected: User %s does not have a registered identity for provider '%s'", sub, provSlug)
							}
						}
					}
				}

				newR := r.WithContext(context.WithValue(r.Context(), types.CascataCtxKey, ctx))
				next.ServeHTTP(w, newR)
				return
			}
		}

		// Final block: Unauthorized
		http.Error(w, `{"error":"Authentication Failed: Scope or Token Mismatch"}`, 401)
	})
}

// RequireManagementRole restricts access to schemas/infra to privileged identities
func RequireManagementRole(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		val := r.Context().Value(types.CascataCtxKey)
		if val == nil {
			log.Printf("[RequireManagementRole] ERROR: Context Lost for path %s", r.URL.Path)
			http.Error(w, `{"error":"Context Lost: Management Layer Unreachable"}`, 500)
			return
		}
		ctx := val.(*types.CascataRequest)
		
		// DEBUG: Log para rastrear falhas de autorização em rotas de management
		log.Printf("[RequireManagementRole] Path: %s, UserRole: %s, IsSystemRequest: %v, IsDashboardAuth: %v", 
			r.URL.Path, ctx.UserRole, ctx.IsSystemRequest, ctx.IsDashboardAuth)
		
		// Role check (Service Role is required for infrastructure management)
		// Permitir também se é Dashboard Auth (usuário logado no painel admin)
		if ctx.IsSystemRequest || ctx.UserRole == types.RoleService || ctx.IsDashboardAuth {
			log.Printf("[RequireManagementRole] ACCESS GRANTED for %s", r.URL.Path)
			next.ServeHTTP(w, r)
			return
		}
		
		log.Printf("[RequireManagementRole] ACCESS DENIED for %s - UserRole: %s", r.URL.Path, ctx.UserRole)
		http.Error(w, `{"error":"Management Access Forbidden","role": "`+string(ctx.UserRole)+`"}`, 403)
	})
}
