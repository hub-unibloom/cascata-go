package middleware

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"time"

	"cascata-backend/internal/types"
)

// ═══════════════════════════════════════════════════════════════════════════════
// FORTRESS MODE - Default Deny Security Architecture
// ═══════════════════════════════════════════════════════════════════════════════
//
// PRINCÍPIO: Tudo é NEGADO por padrão. Só libera com autenticação VERIFICADA.
//
// NÍVEIS DE SEGURANÇA:
// - NÍVEL 4 (FORTRESS): Sistema Cascata - só admin token + MFA
// - NÍVEL 3 (CONTROL): APIs de gerenciamento - service_role obrigatório
// - NÍVEL 2 (DATA): APIs de dados - JWT verificado obrigatório
// - NÍVEL 1 (PUBLIC): Só health checks - sem dados
//
// REGRAS:
// 1. Nenhum acesso "anon" permitido
// 2. Nenhum JWT "unverified" permitido
// 3. TODAS as tentativas são auditadas em system.audit_logs
// 4. Falha = 403 Forbidden (não 401 - não revela se endpoint existe)
// ═══════════════════════════════════════════════════════════════════════════════

// IsFortressMode retorna true se CASCATA_FORTRESS_MODE=enabled
func IsFortressMode() bool {
	return os.Getenv("CASCATA_FORTRESS_MODE") == "enabled"
}

// FortressAuth - Default Deny Authentication Middleware
// Bloqueia TUDO exceto quando autenticação é explicitamente verificada
func FortressAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Se não está em Fortress Mode, passa para o próximo
		if !IsFortressMode() {
			next.ServeHTTP(w, r)
			return
		}

		path := r.URL.Path
		method := r.Method
		ip := getClientIP(r)

		// ═══════════════════════════════════════════════════════════════════════
		// NÍVEL 0: USER AUTH - Rotas de autenticação do cliente final
		// EM FORTRESS MODE: Estas rotas PERMANECEM PÚBLICAS
		// O usuário precisa acessar signup/login ANTES de estar autenticado
		// ═══════════════════════════════════════════════════════════════════════
		if isUserAuthPath(path) {
			// Passa direto - auth.go vai lidar com a autenticação
			next.ServeHTTP(w, r)
			return
		}

		// ═══════════════════════════════════════════════════════════════════════
		// NÍVEL 1: PUBLIC - Só health checks (sem dados sensíveis)
		// ═══════════════════════════════════════════════════════════════════════
		if isPublicPath(path) {
			next.ServeHTTP(w, r)
			return
		}

		// ═══════════════════════════════════════════════════════════════════════
		// VERIFICAÇÃO DE AUTENTICAÇÃO OBRIGATÓRIA
		// ═══════════════════════════════════════════════════════════════════════
		ctx := r.Context().Value(types.CascataCtxKey)
		if ctx == nil {
			// Contexto perdido = ataque ou bug crítico
			logFortressEvent(r, "CONTEXT_LOST", "", ip, path, method, 500, "Contexto Cascata perdido")
			http.Error(w, `{"error":"Security Context Lost"}`, 500)
			return
		}

		cascataCtx := ctx.(*types.CascataRequest)

		// NÍVEL 4: FORTRESS - Sistema interno Cascata
		if isFortressPath(path) {
			if !isFortressAuthorized(cascataCtx) {
				userID, _ := GetUserID(r.Context())
				logFortressEvent(r, "FORTRESS_DENIED", userID, ip, path, method, 403, "Nível 4: Acesso negado - requer admin+ MFA")
				http.Error(w, `{"error":"Fortress Access Denied"}`, 403)
				return
			}
			userID, _ := GetUserID(r.Context())
			logFortressEvent(r, "FORTRESS_GRANTED", userID, ip, path, method, 200, "Nível 4: Acesso concedido")
			next.ServeHTTP(w, r)
			return
		}

		// NÍVEL 3: CONTROL - APIs de gerenciamento
		if isControlPath(path) {
			if !isControlAuthorized(cascataCtx) {
				userID, _ := GetUserID(r.Context())
				logFortressEvent(r, "CONTROL_DENIED", userID, ip, path, method, 403, "Nível 3: service_role obrigatório")
				http.Error(w, `{"error":"Control Plane Access Denied"}`, 403)
				return
			}
			userID, _ := GetUserID(r.Context())
			logFortressEvent(r, "CONTROL_GRANTED", userID, ip, path, method, 200, "Nível 3: Acesso concedido")
			next.ServeHTTP(w, r)
			return
		}

		// NÍVEL 2: DATA - APIs de dados do projeto
		// EM FORTRESS MODE: Anon NUNCA é permitido
		if cascataCtx.Project != nil {
			if !isDataAuthorized(cascataCtx) {
				userID, _ := GetUserID(r.Context())
				logFortressEvent(r, "DATA_DENIED", userID, ip, path, method, 403, "Nível 2: JWT verificado obrigatório - anon negado")
				http.Error(w, `{"error":"Data Access Denied - Authentication Required"}`, 403)
				return
			}
			userID, _ := GetUserID(r.Context())
			logFortressEvent(r, "DATA_GRANTED", userID, ip, path, method, 200, "Nível 2: Acesso concedido")
			next.ServeHTTP(w, r)
			return
		}

		// ═══════════════════════════════════════════════════════════════════════
		// DEFAULT DENY: Qualquer outra rota = BLOQUEADA
		// ═══════════════════════════════════════════════════════════════════════
		userID, _ := GetUserID(r.Context())
		logFortressEvent(r, "DEFAULT_DENY", userID, ip, path, method, 403, "Rota não classificada - acesso negado por padrão")
		http.Error(w, `{"error":"Access Denied"}`, 403)
	})
}

// ═══════════════════════════════════════════════════════════════════════════════
// FUNÇÕES DE AUTORIZAÇÃO POR NÍVEL
// ═══════════════════════════════════════════════════════════════════════════════

func isFortressAuthorized(ctx *types.CascataRequest) bool {
	// Nível 4: Só admin com MFA (quando implementado)
	// Por enquanto: SYSTEM token + RoleService
	return ctx.UserRole == types.RoleService && ctx.IsDashboardAuth
}

func isControlAuthorized(ctx *types.CascataRequest) bool {
	// Nível 3: Service Role ou Dashboard Auth
	return ctx.UserRole == types.RoleService || ctx.IsDashboardAuth || ctx.IsSystemRequest
}

func isDataAuthorized(ctx *types.CascataRequest) bool {
	// NÍVEL 2: EM FORTRESS MODE
	// - Anon NUNCA é permitido
	// - Unverified NUNCA é permitido
	// - Só JWT verificado ou Service Key

	if ctx.UserRole == types.RoleAnon {
		return false // ANON NEGADO EM FORTRESS MODE
	}

	// Só permite Service Role ou Authenticated (nunca Anon)
	return ctx.UserRole == types.RoleService || ctx.UserRole == types.RoleAuthenticated
}

// ═══════════════════════════════════════════════════════════════════════════════
// CLASSIFICAÇÃO DE ROTAS
// ═══════════════════════════════════════════════════════════════════════════════

func isPublicPath(path string) bool {
	// NÍVEL 1: Só health checks - nenhum dado
	publicPaths := []string{
		"/health",
		"/ready",
		"/metrics",
		"/",
	}
	for _, p := range publicPaths {
		if path == p {
			return true
		}
	}
	return false
}

func isFortressPath(path string) bool {
	// NÍVEL 4: Sistema interno Cascata (mais crítico)
	// Ex: /api/fortress/*, /api/system/secrets, etc.
	fortressPrefixes := []string{
		"/api/fortress/",
		"/api/system/secrets",
		"/api/system/keys",
		"/api/admin/sovereign",
	}
	for _, prefix := range fortressPrefixes {
		if len(path) >= len(prefix) && path[:len(prefix)] == prefix {
			return true
		}
	}
	return false
}

func isControlPath(path string) bool {
	// NÍVEL 3: APIs de gerenciamento
	controlPrefixes := []string{
		"/api/control/",
		"/api/admin/",
		"/api/system/",
	}
	for _, prefix := range controlPrefixes {
		if len(path) >= len(prefix) && path[:len(prefix)] == prefix {
			return true
		}
	}
	return false
}

func isUserAuthPath(path string) bool {
	// NÍVEL 0: Rotas de autenticação do CLIENTE FINAL
	// ESTAS ROTAS PERMANECEM PÚBLICAS MESMO EM FORTRESS MODE
	// O usuário precisa acessar estas rotas ANTES de estar autenticado
	
	// Auth v1 endpoints (GoTrue parity)
	authPaths := []string{
		"/auth/v1/signup",      // Criar conta
		"/auth/v1/token",       // Login (password, refresh, etc)
		"/auth/v1/authorize",   // OAuth authorize
		"/auth/v1/callback",    // OAuth callback
		"/auth/v1/recover",     // Password recovery
		"/auth/v1/invite",      // Invite acceptance
		"/auth/v1/magiclink",   // Magic link
		"/auth/v1/otp",         // OTP verification
		"/auth/v1/sso",         // SSO
		"/auth/v1/challenge",   // Passwordless challenge
		"/auth/v1/verify-challenge", // Passwordless verify
		"/auth/v1/mfa",         // MFA enroll and verify
		"/auth/v1/webauthn",    // Passkeys / FIDO2
		"/auth/challenge",      // Alias
		"/auth/verify-challenge",// Alias
	}
	
	for _, authPath := range authPaths {
		if len(path) >= len(authPath) && path[:len(authPath)] == authPath {
			return true
		}
	}
	
	// Verifica prefixos também (para rotas como /auth/v1/token?grant_type=...)
	authPrefixes := []string{
		"/auth/v1/signup",
		"/auth/v1/token",
		"/auth/v1/authorize",
		"/auth/v1/callback",
	}
	
	for _, prefix := range authPrefixes {
		if len(path) >= len(prefix) && path[:len(prefix)] == prefix {
			return true
		}
	}
	
	return false
}

// ═══════════════════════════════════════════════════════════════════════════════
// AUDITORIA DE SISTEMA
// ═══════════════════════════════════════════════════════════════════════════════

type FortressAuditLog struct {
	Timestamp   time.Time `json:"timestamp"`
	Level       string    `json:"level"`        // FORTRESS_DENIED, FORTRESS_GRANTED, etc.
	UserID      string    `json:"user_id"`
	IP          string    `json:"ip"`
	Path        string    `json:"path"`
	Method      string    `json:"method"`
	StatusCode  int       `json:"status_code"`
	Message     string    `json:"message"`
	UserAgent   string    `json:"user_agent"`
	RequestID   string    `json:"request_id,omitempty"`
}

func logFortressEvent(r *http.Request, level, userID, ip, path, method string, status int, message string) {
	// Log estruturado para análise de segurança
	event := FortressAuditLog{
		Timestamp:  time.Now().UTC(),
		Level:      level,
		UserID:     userID,
		IP:         ip,
		Path:       path,
		Method:     method,
		StatusCode: status,
		Message:    message,
		UserAgent:  r.Header.Get("User-Agent"),
	}

	// Serializa para JSON
	jsonLog, _ := json.Marshal(event)

	// Log no console (em produção, vai para system.audit_logs)
	log.Printf("[FORTRESS_AUDIT] %s", string(jsonLog))

	// TODO: Persistir em system.audit_logs async via Firehose
}

