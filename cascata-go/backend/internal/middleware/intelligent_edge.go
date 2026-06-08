package middleware

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"cascata-backend/internal/services"
	"github.com/golang-jwt/jwt/v5"
)

// ═══════════════════════════════════════════════════════════════════════════════
// INTELLIGENT EDGE LIMITER - Arquitetura do Relatório (Sem Hard Limits)
// ═══════════════════════════════════════════════════════════════════════════════
//
// LAYER 1 (Opcional): IP Hard Cap - Configurável via ENV (não hardcoded)
// LAYER 2:           JWT Parse Local - Extrai UUID sem DB lookup
// LAYER 3:           Rate Limit por IP+UUID+Tenant+Regra - Usa system.rate_limits
// LAYER 4:           PostgreSQL - Só requests legítimos chegam aqui
//
// NENHUM limite é hardcoded. Todos vêm de:
// - Layer 1: EDGE_IP_HARD_CAP env variable
// - Layer 3: system.rate_limits (configurado via frontend)
// ═══════════════════════════════════════════════════════════════════════════════

func IntelligentEdgeLimiter(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := getClientIPIntelligent(r)
		ctx := r.Context()

		// ╔════════════════════════════════════════════════════════════════╗
		// ║  LAYER 3.5: Ban Progressivo (Strikes Acumulados)                ║
		// ║  Verifica se IP já está banido por strikes excessivos          ║
		// ║  TTL progressivo: 1min → 10min → 1h → 6h → 24h                 ║
		// ╚════════════════════════════════════════════════════════════════╝
		if err := applyProgressiveBan(ctx, ip, w); err != nil {
			return // Bloqueado no Layer 3.5 (ban progressivo)
		}

		// ╔════════════════════════════════════════════════════════════════╗
		// ║  LAYER 1: IP Hard Cap (OPCIONAL - via ENV)                      ║
		// ║  Só ativa se EDGE_IP_HARD_CAP estiver setada                    ║
		// ║  NÃO hardcoded - configurável por deploy                        ║
		// ╚════════════════════════════════════════════════════════════════╝
		if err := applyIPHardCap(ctx, ip, w); err != nil {
			// Registra strike para ban progressivo
			RegisterProgressiveStrike(ctx, ip, "anon", "layer1_hard_cap")
			return // Bloqueado no Layer 1
		}

		// ╔════════════════════════════════════════════════════════════════╗
		// ║  LAYER 2: JWT Parse Local (sem DB lookup)                       ║
		// ║  Extrai UUID do usuário do token para granularidade             ║
		// ║  COM verificação de assinatura usando chave cacheada            ║
		// ╚════════════════════════════════════════════════════════════════╝
		userUUID, authSource, tenantSlug := extractUserIdentity(r)

		// ╔════════════════════════════════════════════════════════════════╗
		// ║  LAYER 3: Rate Limit por Regras do Banco                        ║
		// ║  Usa system.rate_limits (configurado via frontend)              ║
		// ║  Cacheado no Dragonfly para não tocar PostgreSQL                ║
		// ╚════════════════════════════════════════════════════════════════╝
		if tenantSlug != "" {
			if err := applyRateLimitFromDatabase(ctx, ip, userUUID, tenantSlug, r, authSource, w); err != nil {
				return // Bloqueado no Layer 3
			}
		}

		// Passou todas as camadas - request legítimo
		next.ServeHTTP(w, r)
	})
}

// applyIPHardCap aplica limite por IP apenas se configurado via ENV
// Retorna erro se bloqueado (caller deve retornar)
func applyIPHardCap(ctx context.Context, ip string, w http.ResponseWriter) error {
	hardCapStr := os.Getenv("EDGE_IP_HARD_CAP")
	if hardCapStr == "" {
		// Layer 1 desabilitado - não hardcoded!
		return nil
	}

	hardCap, err := strconv.Atoi(hardCapStr)
	if err != nil || hardCap <= 0 {
		// Valor inválido - desabilita Layer 1
		return nil
	}

	ipGlobalKey := fmt.Sprintf("edge:layer1:ip:%s", ip)
	
	ipCount, _ := services.GetDragonfly().Incr(ctx, ipGlobalKey).Result()
	if ipCount == 1 {
		services.GetDragonfly().Expire(ctx, ipGlobalKey, time.Minute)
	}
	
	if int(ipCount) > hardCap {
		services.RegisterStrike(ctx, ip, fmt.Sprintf("layer1: IP hard cap exceeded (%d)", hardCap))
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(fmt.Sprintf(`{"error":"IP rate limit exceeded","layer":1,"limit":%d,"retry_after":60}`, hardCap)))
		return fmt.Errorf("blocked")
	}
	
	return nil
}

// extractUserIdentity extrai UUID do JWT localmente com verificação de assinatura
// Usa extractTenantRobust para identificação multi-fonte do tenant
func extractUserIdentity(r *http.Request) (string, string, string) {
	var userUUID string
	var authSource string

	token := extractToken(r)

	// ═════════════════════════════════════════════════════════════════
	// EXTRAI TENANT de múltiplas fontes com log estruturado se falhar
	// ═════════════════════════════════════════════════════════════════
	tenantSlug, _, warn := extractTenantRobust(r)

	// Se não conseguiu extrair tenant, LOG ESTRUTURADO REAL
	if tenantSlug == "" && warn != "" {
		// Só loga se for uma rota que deveria ter tenant
		// (não loga para rotas do sistema, health checks, etc)
		if shouldLogTenantFailure(r.URL.Path) {
			// Usa log do pacote services se disponível, ou fmt
			log.Printf("[SECURITY_WARNING] %s", warn)
		}
	} else if tenantSlug != "" {
		// Log de debugging (opcional, descomentar se necessário)
		// log.Printf("[IntelligentEdge] Tenant identificado: %s (fonte: %s)", tenantSlug, source)
	}

	if token != "" {
		// JWT token (começa com eyJ)
		if strings.HasPrefix(token, "eyJ") {
			// ═══════════════════════════════════════════════════════════════════
			// SINERGIA: Verificação em cascata para eliminar dissonância entre layers
			// Ordem: SYSTEM_JWT_SECRET (admin) → jwt_secret do projeto (user) → unverified
			// ═══════════════════════════════════════════════════════════════════
			
			claimsVerified := false
			
			// PASSO 1: Tentar verificar com SYSTEM_JWT_SECRET (GOD MODE detection)
			// Tokens de admin/dashboard são assinados com SYSTEM_JWT_SECRET
			systemSecret := os.Getenv("SYSTEM_JWT_SECRET")
			if systemSecret != "" {
				claims := jwt.MapClaims{}
				parsedToken, err := jwt.ParseWithClaims(token, claims, func(t *jwt.Token) (interface{}, error) {
					return []byte(systemSecret), nil
				})
				if err == nil && parsedToken.Valid {
					if sub, ok := claims["sub"].(string); ok && sub != "" {
						userUUID = sub
						authSource = "jwt-admin" // Admin token verificado
						claimsVerified = true
						// Debug log opcional para tracing de admin
						// log.Printf("[IntelligentEdge] Admin token detected for %s", sub)
					}
				}
			}
			
			// PASSO 2: Se não verificou como admin E temos tenant, tentar com jwt_secret do projeto
			if !claimsVerified && tenantSlug != "" {
				claims, err := parseJWTLocalWithRefresh(r.Context(), token, tenantSlug)
				if err == nil && claims != nil {
					if sub, ok := claims["sub"].(string); ok && sub != "" {
						userUUID = sub
						// Verifica se foi verificado ou não
						if _, unverified := claims["_unverified"]; unverified {
							authSource = "jwt-unverified"
						} else {
							authSource = "jwt"
						}
						claimsVerified = true
					}
				} else {
					// JWT verification falhou com jwt_secret do projeto
					// Só loga se NÃO for um admin token (que já tentamos no passo 1)
					// Isso elimina os warnings falsos para tokens admin acessando rotas de projeto
					if systemSecret != "" {
						// Verifica se é um token admin válido antes de logar erro
						adminClaims := jwt.MapClaims{}
						adminToken, adminErr := jwt.ParseWithClaims(token, adminClaims, func(t *jwt.Token) (interface{}, error) {
							return []byte(systemSecret), nil
						})
						if adminErr != nil || !adminToken.Valid {
							// Não é token admin válido - loga o erro real de verificação
							log.Printf("[SECURITY_WARNING] JWT verification failed for tenant=%s: %v", tenantSlug, err)
						}
						// Se é token admin válido, não loga erro (sinergia perfeita)
					} else {
						log.Printf("[SECURITY_WARNING] JWT verification failed for tenant=%s: %v", tenantSlug, err)
					}
				}
			}
			
			// PASSO 3: Sem tenant ou falha total - parse sem verificação (fallback)
			if !claimsVerified && tenantSlug == "" {
				claims, err := parseJWTLocal(r.Context(), token, "")
				if err == nil && claims != nil {
					if sub, ok := claims["sub"].(string); ok && sub != "" {
						userUUID = sub
						authSource = "jwt-unverified"
					}
				}
			}
		} else if strings.HasPrefix(token, "sk_") {
			// API Key - usa SHA-256 hash como identificador único
			userUUID = "key:" + hashToken(token)
			authSource = "apikey"
		}
	}

	if userUUID == "" {
		userUUID = "anon"
		authSource = "anon"
	}

	return userUUID, authSource, tenantSlug
}

// shouldLogTenantFailure determina se falha de extração de tenant deve ser logada
// Ignora rotas do sistema, health checks, assets estáticos, etc
func shouldLogTenantFailure(path string) bool {
	// Rotas que NÃO precisam de tenant (não logar)
	noTenantPaths := []string{
		"/", "/health", "/ready", "/metrics",
		"/assets/", "/static/", "/favicon.ico",
		"/api/auth/", "/api/system/", "/api/admin/login",
		"/api/control/admin/login", "/api/control/system/",
		"/ws", "/socket.io", "/realtime",
	}

	for _, prefix := range noTenantPaths {
		if strings.HasPrefix(path, prefix) || path == prefix {
			return false
		}
	}

	return true
}

// applyRateLimitFromDatabase aplica rate limit baseado em system.rate_limits
// Busca do cache Dragonfly primeiro, depois SystemPool se necessário
func applyRateLimitFromDatabase(ctx context.Context, ip, userUUID, tenantSlug string, r *http.Request, authSource string, w http.ResponseWriter) error {
	ruleType := determineRuleType(r.URL.Path, r.Method)
	
	// Busca limite do banco (cacheado no Dragonfly)
	limit := getRateLimitFromCacheOrDatabase(ctx, tenantSlug, ruleType, authSource)
	
	// Se não há regra configurada, permite passar (sem hardcoded default!)
	if limit <= 0 {
		// Sem regra = sem limite (ou use DEFAULT_RATE_LIMIT env)
		defaultLimit := os.Getenv("DEFAULT_RATE_LIMIT")
		if defaultLimit == "" {
			// Sem default configurado - permite passar
			return nil
		}
		limit, _ = strconv.Atoi(defaultLimit)
		if limit <= 0 {
			return nil
		}
	}
	
	// Chave única: ip:uuid:tenant:rule_type
	rateKey := fmt.Sprintf("edge:layer3:%s:%s:%s:%s", ip, userUUID, tenantSlug, ruleType)
	
	// Verifica rate limit
	current, _ := services.GetDragonfly().Incr(ctx, rateKey).Result()
	if current == 1 {
		// TTL baseado na regra do banco (padrão: 1s)
		windowSeconds := getWindowSecondsFromCache(ctx, tenantSlug, ruleType)
		if windowSeconds <= 0 {
			windowSeconds = 1
		}
		services.GetDragonfly().Expire(ctx, rateKey, time.Duration(windowSeconds)*time.Second)
	}
	
	if int(current) > limit {
		ttl, _ := services.GetDragonfly().TTL(ctx, rateKey).Result()
		
		// Registra strike progressivo (Layer 3.5)
		banned, banDuration := RegisterProgressiveStrike(ctx, ip, userUUID, fmt.Sprintf("layer3_%s", ruleType))
		
		// Se atingiu ban progressivo, retorna informação do ban
		if banned {
			w.Header().Set("Retry-After", fmt.Sprintf("%d", int(banDuration.Seconds())))
			w.Header().Set("X-Ban-Status", "progressive")
			w.Header().Set("X-Ban-Reason", ruleType)
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(fmt.Sprintf(`{"error":"Access suspended - too many violations","layer":3.5,"rule_type":"%s","retry_after":%d}`, ruleType, int(banDuration.Seconds()))))
			return fmt.Errorf("blocked")
		}
		
		w.Header().Set("Retry-After", fmt.Sprintf("%d", int(ttl.Seconds())+1))
		w.Header().Set("X-RateLimit-Rule", ruleType)
		w.Header().Set("X-RateLimit-Limit", fmt.Sprintf("%d", limit))
		w.Header().Set("X-RateLimit-Auth", authSource)
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(fmt.Sprintf(`{"error":"Rate limit exceeded","layer":3,"rule_type":"%s","limit":%d,"retry_after":%d}`, ruleType, limit, int(ttl.Seconds())+1)))
		return fmt.Errorf("blocked")
	}
	
	// Headers informativos
	w.Header().Set("X-RateLimit-Rule", ruleType)
	w.Header().Set("X-RateLimit-Limit", fmt.Sprintf("%d", limit))
	w.Header().Set("X-RateLimit-Auth", authSource)
	w.Header().Set("X-RateLimit-Remaining", fmt.Sprintf("%d", limit-int(current)))
	
	return nil
}

// extractToken extrai token de múltiplas fontes
func extractToken(r *http.Request) string {
	// Header Authorization: Bearer <token>
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	
	// Header direto
	if token := r.Header.Get("X-JWT-Token"); token != "" {
		return token
	}
	
	// Query param
	if token := r.URL.Query().Get("token"); token != "" {
		return token
	}
	
	// API Key header
	if key := r.Header.Get("apikey"); key != "" {
		return key
	}
	if key := r.URL.Query().Get("apikey"); key != "" {
		return key
	}
	
	return ""
}

// parseJWTLocal faz parse do JWT e verifica assinatura com chave do projeto
// Busca jwt_secret do cache Dragonfly (populado quando projeto é criado)
func parseJWTLocal(ctx context.Context, tokenString string, tenantSlug string) (jwt.MapClaims, error) {
	// Busca jwt_secret do cache
	jwtSecret, err := getJWTSecretFromCache(ctx, tenantSlug)
	if err != nil {
		// Se não tem chave no cache, tenta parse sem verificação (fallback)
		// Mas retorna erro de verificação para não confiar no token
		token, _, err := new(jwt.Parser).ParseUnverified(tokenString, jwt.MapClaims{})
		if err != nil {
			return nil, err
		}
		if claims, ok := token.Claims.(jwt.MapClaims); ok {
			// Marca como não verificado
			claims["_unverified"] = true
			return claims, nil
		}
		return nil, fmt.Errorf("invalid claims")
	}

	// Parse COM verificação de assinatura
	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		// Verifica algoritmo
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return []byte(jwtSecret), nil
	})

	if err != nil {
		return nil, fmt.Errorf("jwt verification failed: %w", err)
	}

	if claims, ok := token.Claims.(jwt.MapClaims); ok && token.Valid {
		return claims, nil
	}

	return nil, fmt.Errorf("invalid or expired token")
}

// getJWTSecretFromCache busca jwt_secret do Dragonfly
// Cache key: project:{slug}:jwt_secret
func getJWTSecretFromCache(ctx context.Context, tenantSlug string) (string, error) {
	cacheKey := fmt.Sprintf("project:%s:jwt_secret", tenantSlug)
	cachedValue, err := services.GetDragonfly().Get(ctx, cacheKey).Result()
	if err != nil || cachedValue == "" {
		return "", fmt.Errorf("jwt_secret not in cache")
	}
	return cachedValue, nil
}

// CacheJWTSecret popula o cache Dragonfly com jwt_secret do projeto
// Deve ser chamado quando projeto é criado ou chave é rotacionada
func CacheJWTSecret(ctx context.Context, tenantSlug, jwtSecret string) error {
	cacheKey := fmt.Sprintf("project:%s:jwt_secret", tenantSlug)
	// Cache por 24 horas (ou até ser explicitamente invalidado)
	return services.GetDragonfly().Set(ctx, cacheKey, jwtSecret, 24*time.Hour).Err()
}

// InvalidateJWTSecret invalida o cache do jwt_secret no Dragonfly
// Deve ser chamado quando:
// - Projeto é deletado
// - JWT secret é rotacionado
// - Projeto é desativado
func InvalidateJWTSecret(ctx context.Context, tenantSlug string) error {
	cacheKey := fmt.Sprintf("project:%s:jwt_secret", tenantSlug)
	dragonfly := services.GetDragonfly()
	if dragonfly == nil {
		return fmt.Errorf("dragonfly not connected")
	}

	// Deleta a chave imediatamente (não espera TTL)
	err := dragonfly.Del(ctx, cacheKey).Err()
	if err != nil {
		log.Printf("[InvalidateJWTSecret] Warning: Failed to invalidate cache for %s: %v", tenantSlug, err)
		return err
	}

	log.Printf("[InvalidateJWTSecret] JWT secret cache invalidated for project: %s", tenantSlug)
	return nil
}

// InvalidateRateLimitCache invalida o cache de rate limits de um projeto
// Deve ser chamado quando regras de rate limit são deletadas ou projeto é removido
func InvalidateRateLimitCache(ctx context.Context, tenantSlug string) error {
	dragonfly := services.GetDragonfly()
	if dragonfly == nil {
		return fmt.Errorf("dragonfly not connected")
	}

	// Deleta todas as chaves de configuração de rate limit do projeto
	ruleTypes := []string{"global", "auth", "table", "rpc", "edge"}
	for _, ruleType := range ruleTypes {
		cacheKey := fmt.Sprintf("ratelimit:config:%s:%s", tenantSlug, ruleType)
		dragonfly.Del(ctx, cacheKey) // Ignora erro individual
	}

	log.Printf("[InvalidateRateLimitCache] Rate limit cache invalidated for project: %s", tenantSlug)
	return nil
}

// InvalidateProjectCache invalida TODOS os caches de um projeto
// Chamado quando projeto é deletado ou desativado
func InvalidateProjectCache(ctx context.Context, tenantSlug string) error {
	// Invalida JWT secret
	if err := InvalidateJWTSecret(ctx, tenantSlug); err != nil {
		log.Printf("[InvalidateProjectCache] Warning: JWT invalidation failed: %v", err)
	}

	// Invalida rate limits
	if err := InvalidateRateLimitCache(ctx, tenantSlug); err != nil {
		log.Printf("[InvalidateProjectCache] Warning: Rate limit invalidation failed: %v", err)
	}

	// Limpa contadores de strikes para o projeto (opcional, mas limpa estado)
	// Nota: Isso requer scan ou padrão de chave, pode ser custoso em produção
	// Por simplicidade, deixamos o TTL expirar naturalmente (24h)

	log.Printf("[InvalidateProjectCache] All caches invalidated for project: %s", tenantSlug)
	return nil
}

// hashToken gera SHA-256 hash do token para fingerprint único
func hashToken(token string) string {
	if len(token) < 8 {
		return "short:" + token
	}
	hash := sha256.Sum256([]byte(token))
	// Retorna primeiros 16 caracteres do hex (suficiente para evitar colisões)
	return hex.EncodeToString(hash[:])[:16]
}

// extractTenantRobust extrai tenant de múltiplas fontes com prioridade:
// 1. X-Tenant-Slug header (mais confiável - pode ser setado por Nginx baseado em server_name)
// 2. Host header (subdomínio: tenant.cascata.io)
// 3. URL path (/api/data/{tenant}/... ou /api/control/projects/{tenant}/...)
// Retorna "", source e um log estruturado se falhar
func extractTenantRobust(r *http.Request) (tenantSlug, source string, warn string) {
	path := r.URL.Path

	// ═════════════════════════════════════════════════════════════════
	// FONTE 1: Header X-Tenant-Slug (injetado por Nginx ou upstream)
	// ═════════════════════════════════════════════════════════════════
	if headerSlug := r.Header.Get("X-Tenant-Slug"); headerSlug != "" {
		// Valida formato básico (alphanumeric + hyphen/underscore)
		if isValidSlug(headerSlug) {
			return headerSlug, "header-x-tenant-slug", ""
		}
	}

	// ═════════════════════════════════════════════════════════════════
	// FONTE 2: Host header (subdomínio)
	// ═════════════════════════════════════════════════════════════════
	host := r.Host
	if host == "" {
		host = r.Header.Get("Host")
	}
	if host != "" && !strings.Contains(host, "localhost") && !isIPAddress(host) {
		// Remove porta se existir
		if idx := strings.LastIndex(host, ":"); idx != -1 {
			host = host[:idx]
		}
		// Extrai subdomínio (tenant.cascata.io ou tenant.custom.com)
		parts := strings.Split(host, ".")
		if len(parts) >= 2 && parts[0] != "www" && parts[0] != "" {
			// Valida se não é TLD ou domínio principal
			if len(parts) > 2 || (len(parts) == 2 && !isKnownTLD(parts[1])) {
				subdomain := parts[0]
				if isValidSlug(subdomain) {
					return subdomain, "host-subdomain", ""
				}
			}
		}
	}

	// ═════════════════════════════════════════════════════════════════
	// FONTE 3: URL path
	// ═════════════════════════════════════════════════════════════════
	urlParts := strings.Split(path, "/")

	// Padrão 3a: /api/data/{tenant}/...
	for i, part := range urlParts {
		if part == "data" && i+1 < len(urlParts) {
			tenant := urlParts[i+1]
			if tenant != "" && isValidSlug(tenant) {
				return tenant, "url-data-path", ""
			}
		}
	}

	// Padrão 3b: /api/control/projects/{tenant}/...
	if len(urlParts) >= 5 && urlParts[1] == "api" && urlParts[2] == "control" {
		if urlParts[3] == "projects" {
			tenant := urlParts[4]
			if tenant != "" && isValidSlug(tenant) {
				return tenant, "url-control-projects", ""
			}
		}
	}

	// Padrão 3c: /{tenant}/... (sem /api/ prefixo)
	if len(urlParts) > 1 && urlParts[1] != "" && urlParts[1] != "api" {
		tenant := urlParts[1]
		if isValidSlug(tenant) {
			return tenant, "url-root-path", ""
		}
	}

	// ═════════════════════════════════════════════════════════════════
	// FALHA: Nenhuma fonte conseguiu extrair tenant
	// ═════════════════════════════════════════════════════════════════
	warn = fmt.Sprintf("TENANT_EXTRACTION_FAILED: path=%s, host=%s, x-tenant-slug=%s, "+
		"method=%s, remote_addr=%s, user_agent=%.50s",
		path, r.Host, r.Header.Get("X-Tenant-Slug"),
		r.Method, r.RemoteAddr, r.UserAgent())

	return "", "none", warn
}

// isValidSlug valida formato básico de slug (alphanumeric + hyphen/underscore)
func isValidSlug(s string) bool {
	if s == "" || len(s) > 63 {
		return false
	}
	for _, r := range s {
		if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

// isIPAddress verifica se string é IP (v4 ou v6)
func isIPAddress(s string) bool {
	// Remove porta
	if idx := strings.LastIndex(s, ":"); idx != -1 {
		s = s[:idx]
	}
	// IPv4 simples
	parts := strings.Split(s, ".")
	if len(parts) == 4 {
		for _, p := range parts {
			if _, err := strconv.Atoi(p); err != nil {
				return false
			}
		}
		return true
	}
	// IPv6 (simplificado)
	if strings.Contains(s, ":") {
		return true
	}
	return false
}

// isKnownTLD verifica se é TLD conhecido (simplificado)
func isKnownTLD(s string) bool {
	// Lista simplificada - em produção usar biblioteca adequada
	tlds := []string{"com", "org", "net", "io", "dev", "app", "co", "ai", "cloud", "tech"}
	for _, tld := range tlds {
		if s == tld || strings.HasSuffix(s, "."+tld) {
			return true
		}
	}
	return false
}

// extractTenantFromURL mantém compatibilidade com código existente
// DEPRECATED: Use extractTenantRobust
func extractTenantFromURL(path string) string {
	dummyReq, _ := http.NewRequest("GET", "http://dummy"+path, nil)
	slug, _, _ := extractTenantRobust(dummyReq)
	return slug
}

// determineRuleType determina qual das 4 regras se aplica
func determineRuleType(path, method string) string {
	lowerPath := strings.ToLower(path)
	
	// 1. Auth Routes
	if strings.Contains(lowerPath, "/auth/") ||
	   strings.Contains(lowerPath, "/login") ||
	   strings.Contains(lowerPath, "/signup") ||
	   strings.Contains(lowerPath, "/verify") ||
	   strings.Contains(lowerPath, "/recovery") {
		return "auth"
	}
	
	// 2. RPC Function
	if strings.Contains(lowerPath, "/rpc/") {
		return "rpc"
	}
	
	// 3. Specific Table
	if strings.Contains(lowerPath, "/tables/") {
		return "table"
	}

	// 4. Edge Functions
	if strings.Contains(lowerPath, "/edge/") {
		return "edge"
	}
	
	// 5. Global API (default)
	return "global"
}

// getRateLimitFromCacheOrDatabase busca limite do cache Dragonfly ou do banco
// Cache key: ratelimit:{tenant}:{rule_type}
func getRateLimitFromCacheOrDatabase(ctx context.Context, tenantSlug, ruleType, authSource string) int {
	// Tenta buscar do cache do Dragonfly primeiro
	cacheKey := fmt.Sprintf("ratelimit:config:%s:%s", tenantSlug, ruleType)
	cachedValue, err := services.GetDragonfly().Get(ctx, cacheKey).Result()
	if err == nil && cachedValue != "" {
		// Parse do valor cacheado: "rate_limit:burst_limit:window_seconds:rate_limit_anon"
		parts := strings.Split(cachedValue, ":")
		if len(parts) >= 4 {
			rateLimit, _ := strconv.Atoi(parts[0])
			rateLimitAnon, _ := strconv.Atoi(parts[3])
			
			// Retorna limite baseado na fonte de auth
			switch authSource {
			case "jwt":
				return rateLimit // Usuários autenticados usam rate_limit
			case "apikey":
				// API keys - verifica se há config específica no grupo
				return rateLimit // Simplificado por agora
			default: // anon
				return rateLimitAnon
			}
		}
	}
	
	// Se não está no cache, retorna 0 (sem limite configurado)
	// O cache é populado pelo backend quando as regras são criadas/atualizadas
	return 0
}

// getWindowSecondsFromCache busca window seconds do cache
func getWindowSecondsFromCache(ctx context.Context, tenantSlug, ruleType string) int {
	cacheKey := fmt.Sprintf("ratelimit:config:%s:%s", tenantSlug, ruleType)
	cachedValue, err := services.GetDragonfly().Get(ctx, cacheKey).Result()
	if err == nil && cachedValue != "" {
		parts := strings.Split(cachedValue, ":")
		if len(parts) >= 3 {
			windowSeconds, _ := strconv.Atoi(parts[2])
			if windowSeconds > 0 {
				return windowSeconds
			}
		}
	}
	return 1 // Default: 1 segundo
}

// RefreshRateLimitCache popula o cache Dragonfly com regras do banco
// Deve ser chamado quando regras são criadas/atualizadas
func RefreshRateLimitCache(ctx context.Context, tenantSlug string) error {
	// Busca regras do banco
	rows, err := services.SystemPool.Query(ctx, 
		"SELECT route_pattern, rate_limit, burst_limit, window_seconds, rate_limit_anon FROM system.rate_limits WHERE project_slug = $1", 
		tenantSlug)
	if err != nil {
		return err
	}
	defer rows.Close()
	
	for rows.Next() {
		var routePattern string
		var rateLimit, burstLimit, windowSeconds, rateLimitAnon int
		if err := rows.Scan(&routePattern, &rateLimit, &burstLimit, &windowSeconds, &rateLimitAnon); err != nil {
			continue
		}
		
		// Determina o tipo de regra baseado no route_pattern
		ruleType := determineRuleTypeFromPattern(routePattern)
		
		// Cacheia no Dragonfly (TTL: 1 hora)
		cacheKey := fmt.Sprintf("ratelimit:config:%s:%s", tenantSlug, ruleType)
		cacheValue := fmt.Sprintf("%d:%d:%d:%d", rateLimit, burstLimit, windowSeconds, rateLimitAnon)
		services.GetDragonfly().Set(ctx, cacheKey, cacheValue, time.Hour)
	}
	
	return nil
}

// determineRuleTypeFromPattern converte route_pattern para rule_type
func determineRuleTypeFromPattern(pattern string) string {
	lowerPattern := strings.ToLower(pattern)
	if strings.Contains(lowerPattern, "/auth/") ||
	   strings.Contains(lowerPattern, "login") ||
	   strings.Contains(lowerPattern, "signup") ||
	   strings.HasPrefix(lowerPattern, "auth:") {
		return "auth"
	}
	
	if strings.Contains(lowerPattern, "/rpc/") ||
	   strings.HasPrefix(lowerPattern, "rpc:") {
		return "rpc"
	}
	
	if strings.Contains(lowerPattern, "/tables/") ||
	   strings.HasPrefix(lowerPattern, "table:") {
		return "table"
	}

	if strings.Contains(lowerPattern, "/edge/") ||
	   strings.HasPrefix(lowerPattern, "edge:") {
		return "edge"
	}
	
	return "global"
}

// getClientIPIntelligent extrai IP considerando proxies
func getClientIPIntelligent(r *http.Request) string {
	// X-Forwarded-For (mais comum)
	xff := r.Header.Get("X-Forwarded-For")
	if xff != "" {
		parts := strings.Split(xff, ",")
		if len(parts) > 0 {
			return strings.TrimSpace(parts[0])
		}
	}

	// X-Real-IP
	xri := r.Header.Get("X-Real-Ip")
	if xri != "" {
		return xri
	}

	// CF-Connecting-IP (Cloudflare)
	cfip := r.Header.Get("CF-Connecting-IP")
	if cfip != "" {
		return cfip
	}

	// RemoteAddr fallback
	ip := r.RemoteAddr
	if idx := strings.LastIndex(ip, ":"); idx != -1 {
		ip = ip[:idx]
	}
	return ip
}

// ═══════════════════════════════════════════════════════════════════════════════
// BAN PROGRESSIVO - Layer 3.5
// ═══════════════════════════════════════════════════════════════════════════════
// Sistema de strikes com TTL progressivo:
// 1-2 strikes: 1 minuto | 3-4 strikes: 10 minutos | 5-6 strikes: 1 hora | 7+: 24 horas

// RegisterProgressiveStrike registra strike e retorna se IP está banido
func RegisterProgressiveStrike(ctx context.Context, ip, userUUID, reason string) (banned bool, banDuration time.Duration) {
	dragonfly := services.GetDragonfly()
	if dragonfly == nil {
		return false, 0
	}

	// Chave única por IP+UUID (evita que atacante use múltiplos UUIDs)
	strikeKey := fmt.Sprintf("ban:strikes:%s:%s", ip, userUUID)

	strikes, _ := dragonfly.Incr(ctx, strikeKey).Result()
	if strikes == 1 {
		// Primeiro strike - inicia contador com TTL de 24h
		dragonfly.Expire(ctx, strikeKey, 24*time.Hour)
	}

	// Determina duração do ban baseado no número de strikes
	var banMinutes int
	switch {
	case strikes >= 10:
		banMinutes = 1440 // 24 horas - ban permanente no blocklist
	case strikes >= 7:
		banMinutes = 360 // 6 horas
	case strikes >= 5:
		banMinutes = 60 // 1 hora
	case strikes >= 3:
		banMinutes = 10 // 10 minutos
	default:
		banMinutes = 1 // 1 minuto (cooldown)
	}

	banDuration = time.Duration(banMinutes) * time.Minute

	// Se atingiu threshold, aplica ban
	if strikes >= 3 {
		banKey := fmt.Sprintf("ban:active:%s", ip)
		dragonfly.Set(ctx, banKey, fmt.Sprintf("strikes:%d:reason:%s", strikes, reason), banDuration)

		// Adiciona ao blocklist global se strikes >= 10
		if strikes >= 10 {
			services.RegisterStrike(ctx, ip, reason)
		}

		return true, banDuration
	}

	return false, 0
}

// IsIPBanned verifica se IP está banido (Layer 3.5 check)
func IsIPBanned(ctx context.Context, ip string) (bool, time.Duration) {
	dragonfly := services.GetDragonfly()
	if dragonfly == nil {
		return false, 0
	}

	banKey := fmt.Sprintf("ban:active:%s", ip)
	ttl, err := dragonfly.TTL(ctx, banKey).Result()
	if err != nil || ttl <= 0 {
		return false, 0
	}

	return true, ttl
}

// applyProgressiveBan middleware check para ban progressivo
// Deve ser chamado no início do IntelligentEdgeLimiter (antes de Layer 1)
func applyProgressiveBan(ctx context.Context, ip string, w http.ResponseWriter) error {
	banned, ttl := IsIPBanned(ctx, ip)
	if !banned {
		return nil
	}

	w.Header().Set("Retry-After", fmt.Sprintf("%d", int(ttl.Seconds())))
	w.Header().Set("X-Ban-Status", "progressive")
	w.WriteHeader(http.StatusTooManyRequests)
	w.Write([]byte(fmt.Sprintf(`{"error":"Access temporarily suspended","layer":3.5,"retry_after":%d}`, int(ttl.Seconds()))))
	return fmt.Errorf("banned")
}

// ═══════════════════════════════════════════════════════════════════════════════
// JWT CACHE WARMUP & REFRESH - Consistência entre workers
// ═══════════════════════════════════════════════════════════════════════════════

// WarmupJWTCache popula o cache Dragonfly com JWT secrets de todos os projetos ativos
// Deve ser chamado no startup de cada worker para garantir consistência
func WarmupJWTCache(ctx context.Context) error {
	dragonfly := services.GetDragonfly()
	if dragonfly == nil {
		return fmt.Errorf("dragonfly not connected")
	}

	// Busca todos os projetos ativos com seus JWT secrets
	rows, err := services.SystemPool.Query(ctx,
		"SELECT slug, jwt_secret FROM system.projects WHERE status = 'active'")
	if err != nil {
		return fmt.Errorf("failed to fetch projects for JWT warmup: %w", err)
	}
	defer rows.Close()

	count := 0
	errors := 0
	for rows.Next() {
		var slug, jwtSecret string
		if err := rows.Scan(&slug, &jwtSecret); err != nil {
			errors++
			continue
		}

		if jwtSecret == "" {
			continue
		}

		// Popula cache com TTL de 24 horas
		cacheKey := fmt.Sprintf("project:%s:jwt_secret", slug)
		if err := dragonfly.Set(ctx, cacheKey, jwtSecret, 24*time.Hour).Err(); err != nil {
			log.Printf("[WarmupJWTCache] Warning: Failed to cache JWT for %s: %v", slug, err)
			errors++
		} else {
			count++
		}
	}

	log.Printf("[WarmupJWTCache] Warmup complete: %d projects cached, %d errors", count, errors)
	return nil
}

// refreshJWTSecretFromDatabase busca JWT secret do banco e popula cache (cache miss)
// Chamado automaticamente quando getJWTSecretFromCache falha
func refreshJWTSecretFromDatabase(ctx context.Context, tenantSlug string) (string, error) {
	// Busca do banco
	var jwtSecret string
	err := services.SystemPool.QueryRow(ctx,
		"SELECT jwt_secret FROM system.projects WHERE slug = $1 AND status = 'active'",
		tenantSlug).Scan(&jwtSecret)
	if err != nil {
		return "", fmt.Errorf("failed to fetch JWT secret from database: %w", err)
	}

	if jwtSecret == "" {
		return "", fmt.Errorf("JWT secret is empty for project: %s", tenantSlug)
	}

	// Popula cache
	if err := CacheJWTSecret(ctx, tenantSlug, jwtSecret); err != nil {
		log.Printf("[refreshJWTSecret] Warning: Failed to populate cache for %s: %v", tenantSlug, err)
		// Não falha - retorna o secret mesmo se cache falhar
	}

	log.Printf("[refreshJWTSecret] Cache miss resolved for project: %s", tenantSlug)
	return jwtSecret, nil
}

// GetJWTSecretWithRefresh busca JWT secret do cache com refresh automático em cache miss
// Esta função garante consistência entre workers - se um worker reiniciar,
// o cache será transparentemente repopulado do banco
func GetJWTSecretWithRefresh(ctx context.Context, tenantSlug string) (string, error) {
	// Tenta buscar do cache primeiro
	secret, err := getJWTSecretFromCache(ctx, tenantSlug)
	if err == nil {
		return secret, nil
	}

	// Cache miss - busca do banco e popula cache (refresh)
	return refreshJWTSecretFromDatabase(ctx, tenantSlug)
}

// parseJWTLocalWithRefresh faz parse do JWT com verificação de assinatura
// e refresh automático do cache em caso de cache miss
func parseJWTLocalWithRefresh(ctx context.Context, tokenString string, tenantSlug string) (jwt.MapClaims, error) {
	// Busca JWT secret com refresh automático
	jwtSecret, err := GetJWTSecretWithRefresh(ctx, tenantSlug)
	if err != nil {
		// Se não conseguiu buscar do banco, tenta parse sem verificação (fallback)
		token, _, err := new(jwt.Parser).ParseUnverified(tokenString, jwt.MapClaims{})
		if err != nil {
			return nil, err
		}
		if claims, ok := token.Claims.(jwt.MapClaims); ok {
			claims["_unverified"] = true
			return claims, nil
		}
		return nil, fmt.Errorf("invalid claims")
	}

	// Parse COM verificação de assinatura
	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return []byte(jwtSecret), nil
	})

	if err != nil {
		return nil, fmt.Errorf("jwt verification failed: %w", err)
	}

	if claims, ok := token.Claims.(jwt.MapClaims); ok && token.Valid {
		return claims, nil
	}

	return nil, fmt.Errorf("invalid or expired token")
}
