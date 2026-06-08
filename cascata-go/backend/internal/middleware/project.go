package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"cascata-backend/internal/services"
	"cascata-backend/internal/types"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ProjectResolver identifies the tenant and environment using multimodal logic (Slug, Domain, and API Key)
// EAGER version: conecta no banco do tenant imediatamente
func ProjectResolver(next http.Handler) http.Handler {
	return projectResolverInternal(next, false)
}

// ProjectResolverLazy identifies tenant WITHOUT connecting to tenant database
// LAZY version: ideal for rate limiting - schemas initialized on first actual query
func ProjectResolverLazy(next http.Handler) http.Handler {
	return projectResolverInternal(next, true)
}

// projectResolverInternal contém a lógica compartilhada entre eager e lazy
func projectResolverInternal(next http.Handler, lazy bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 1. Initialize Baseline Context (Mandatory for Middleware Chain)
		ctx := types.NewCascataRequest(r)

		path := r.URL.Path
		host := r.Host
		if strings.Contains(host, ":") {
			host = strings.Split(host, ":")[0]
		}
		// Normalizar host para lowercase (domínios são case-insensitive)
		host = strings.ToLower(host)
		method := r.Method

		// DEBUG: Log detalhado de todas as requisições para investigar query params
		queryStr := ""
		if r.URL.RawQuery != "" {
			queryStr = "?" + r.URL.RawQuery
		}
		mode := "EAGER"
		if lazy {
			mode = "LAZY"
		}
		log.Printf("[ProjectResolver-%s] %s %s%s | Host: %s", mode, method, path, queryStr, host)

		// --- 2. MULTIMODAL TENANCY RESOLUTION ---
		targetEnv := "live"
		slugFromUrl := ""
		resolutionMethod := types.ResolutionMethod("")

		// Priority: Detect API Key (Sticky Tenancy for SDK/Discovery)
		apiKey := r.Header.Get("apikey")
		if apiKey == "" {
			apiKey = r.URL.Query().Get("apikey")
		}

		pathParts := strings.Split(path, "/")

		// Strategy A: URL Path Mapping (/api/data/{slug}/...)
		if len(pathParts) > 3 && pathParts[1] == "api" && pathParts[2] == "data" {
			slugFromUrl = pathParts[3]
		} else if len(pathParts) > 4 && pathParts[1] == "api" && pathParts[2] == "control" && pathParts[3] == "projects" {
			// Strategy B: Tenancy via Control Plane (/api/control/projects/{slug}/...)
			slugFromUrl = pathParts[4]
		}

		ctx.TargetEnv = targetEnv

		// --- 3. SYSTEM AUTHENTICATION (God Mode Boundary) ---
		internalSecret := r.Header.Get("X-Cascata-Internal-Key")
		if internalSecret != "" && internalSecret == os.Getenv("INTERNAL_CTRL_SECRET") {
			ctx.IsSystemRequest = true
			ctx.UserRole = types.RoleService
		}

		// Detect Dashboard Context via header or referrer
		if r.Header.Get("X-Cascata-Context") == "dashboard" ||
			(r.Header.Get("Referer") != "" && strings.Contains(r.Header.Get("Referer"), "dash.")) {
			ctx.IsDashboardAuth = true
		}

		// --- 4. PROJECT SEARCH (Resilience Hierarchy: Domain > Slug > API Key) ---
		var project *types.Project

		// Priority 1: Domain Mapping (Custom Domain Proxy)
		if !isLocalhost(host) && !ctx.IsSystemRequest {
			project = services.GetProjectByDomain(r.Context(), host)
			if project != nil {
				resolutionMethod = types.ResolutionByDomain
			}
		}

		// Priority 2: Slug Mapping (URL Context)
		if project == nil && slugFromUrl != "" {
			project = services.GetProjectBySlug(r.Context(), slugFromUrl)
			if project != nil {
				resolutionMethod = types.ResolutionBySlug
			}
		}

		// Priority 3: API Key Mapping (SDK/Supabase Client Bridge)
		// This is the "Sticky Tenancy" that allows clients to access resources without slug in URL
		if project == nil && apiKey != "" && !ctx.IsSystemRequest {
			project = services.GetProjectByApiKey(r.Context(), apiKey)
			if project != nil {
				resolutionMethod = types.ResolutionByAPIKey
			}
		}

		// Handle Resolution Failure (Fail-Safe)
		if project == nil {
			// Bloqueio Seletivo: Apenas o Data Plane EXIGE o contexto do projeto.
			// O Dashboard/Control Plane pode ser acessado como Admin Global.
			isDataReq := strings.Contains(path, "/api/data/") ||
				strings.Contains(path, "/rest/v1/") ||
				strings.Contains(path, "/auth/v1/") ||
				strings.Contains(path, "/webhook/")

			if isDataReq && !ctx.IsSystemRequest {
				http.Error(w, `{"error":"Project Context Not Found (404)","hint":"Check your API Key or project slug in URL."}`, 404)
				return
			}

			// Procced with empty context for admin routes
			newR := r.WithContext(context.WithValue(r.Context(), types.CascataCtxKey, ctx))
			next.ServeHTTP(w, newR)
			return
		}

		// Store resolution method in context
		ctx.ResolutionMethod = resolutionMethod

		// --- 5. ACCESS PLANE CLASSIFICATION ---
		accessPlane := classifyAccessPlane(path)
		ctx.AccessPlane = accessPlane

		// --- 5.5 SYSTEM DOMAIN DETECTION (Dashboard Access) ---
		// CRITICAL: Fetch system domain to ensure the Dashboard can still access the API via slug
		sysDomain := ""
		if !isLocalhost(host) && resolutionMethod == types.ResolutionBySlug {
			// Only fetch system domain when accessing via slug (not custom domain)
			// This is used to detect if request is coming from dashboard
			var domainNull interface{}
			err := services.SystemPool.QueryRow(r.Context(),
				"SELECT settings->>'domain' FROM system.ui_settings WHERE project_slug = '_system_root_' AND table_name = 'system_config'").Scan(&domainNull)
			if err == nil && domainNull != nil {
				sysDomain = fmt.Sprintf("%v", domainNull)
			}
			// Silently ignore errors - system domain is optional for basic operation
		}

		// Detectar se é requisição do Dashboard via múltiplos métodos:
		// 1. Host é o system domain
		// 2. Header X-Cascata-Context: dashboard
		isSystemDomain := sysDomain != "" && strings.EqualFold(host, sysDomain)
		isDashboardContext := r.Header.Get("X-Cascata-Context") == "dashboard"
		isDashboardReferer := r.Header.Get("Referer") != "" &&
			(strings.Contains(r.Header.Get("Referer"), "dash.") ||
				strings.Contains(r.Header.Get("Referer"), "/dashboard") ||
				strings.Contains(r.Header.Get("Referer"), ":3001")) // Porta do dashboard dev

		if isSystemDomain || isDashboardContext || isDashboardReferer {
			ctx.IsDashboardAuth = true
			log.Printf("[ProjectResolver] Dashboard access detected for project %s (host: %s)", project.Slug, host)
		}

		// --- 6. DOMAIN LOCKING POLICY (Cascata Guard 2.0) ---
		// Path-Aware Domain Locking: Só bloqueia quando faz sentido de segurança
		// Se for system domain (dashboard), sempre permite (gestão de infra)
		// Se for localhost, sempre permite (desenvolvimento)
		// Se for requisição com Authorization header (possível admin), loga mas não bloqueia ainda - deixa CascataAuth decidir

		// DEBUG: Log para investigar Domain Locking
		log.Printf("[DomainLocking] Check - Host: %s, Path: %s, Method: %s, CustomDomain: %s, IsDashboardAuth: %v, IsSystemRequest: %v, HasAuthHeader: %v",
			host, path, method, project.CustomDomain, ctx.IsDashboardAuth, ctx.IsSystemRequest, r.Header.Get("Authorization") != "")

		// Bypass Domain Locking para rotas de administração/query (usadas pelo dashboard)
		isAdminRoute := strings.Contains(path, "/query") ||
			strings.Contains(path, "/tables") ||
			strings.Contains(path, "/schemas") ||
			strings.Contains(path, "/extensions") ||
			strings.Contains(path, "/automations") ||
			strings.Contains(path, "/push/") ||
			strings.Contains(path, "/storage/") ||
			strings.Contains(path, "/snapshots") ||
			strings.Contains(path, "/auth/") ||
			strings.Contains(path, "/sessions") ||
			strings.Contains(path, "/rate-limits") ||
			strings.Contains(path, "/policies") ||
			strings.Contains(path, "/security/") ||
			strings.Contains(path, "/api-keys") ||
			strings.Contains(path, "/recycle-bin") ||
			strings.Contains(path, "/assets") ||
			strings.HasPrefix(path, "/api/control/")

		if !ctx.IsDashboardAuth && !isLocalhost(host) && !ctx.IsSystemRequest && !isAdminRoute {
			if shouldApplyDomainLocking(path, method, project, host, ctx, resolutionMethod) {
				log.Printf("[DomainLocking] BLOCKED - Host: %s, Path: %s, Method: %s, CustomDomain: %s",
					host, path, method, project.CustomDomain)
				http.Error(w, `{"error":"Domain Locking Policy Active","hint":"Use https://`+project.CustomDomain+`","code":"DOMAIN_LOCK"}`, 403)
				return
			}
		} else {
			log.Printf("[DomainLocking] BYPASSED - Host: %s, Path: %s, IsDashboardAuth: %v, isLocalhost: %v, isAdminRoute: %v",
				host, path, ctx.IsDashboardAuth, isLocalhost(host), isAdminRoute)
		}

		// --- 7. CRYPTO ENGINE UNSEALING (Decryption Transfer) ---
		// Descriptografa as chaves do projeto para que CascataAuth possa comparar com
		// o apikey enviado. Se o crypto-engine estiver selado, as chaves ficam no formato
		// cse:v1:... e qualquer comparação downstream falhará silenciosamente com 401.
		// Para evitar isso, detectamos ENGINE_SEALED aqui e retornamos 503 de imediato
		// em requisições de dados — exceto para o plano de controle admin (GOD MODE via SYSTEM_JWT_SECRET).
		cryptoSvc := &services.CryptoService{}
		decrypted, err := cryptoSvc.DecryptBatch([]string{project.JWTSecret, project.AnonKey, project.ServiceKey})
		if err == nil && len(decrypted) == 3 {
			project.JWTSecret = decrypted[0]
			project.AnonKey = decrypted[1]
			project.ServiceKey = decrypted[2]
		} else if err != nil {
			log.Printf("[ProjectResolver] ⚠ Crypto Error para projeto '%s': %v", project.Slug, err)

			// Se o engine está SEALED, requisições de data plane não podem ser atendidas.
			// Requisições de control plane (admin dashboard via SYSTEM_JWT_SECRET) ainda passam.
			path := r.URL.Path
			isDataRequest := strings.Contains(path, "/api/data/") ||
				strings.Contains(path, "/rest/v1/") ||
				strings.Contains(path, "/auth/v1/") ||
				strings.Contains(path, "/storage/v1/") ||
				strings.Contains(path, "/realtime/v1/")

			if errors.Is(err, services.ErrEngineSealed) && isDataRequest {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusServiceUnavailable)
				w.Write([]byte(`{"error":"Crypto Engine selado","hint":"O administrador precisa desbloquear via POST /api/control/auth/sovereign/unseal","code":"ENGINE_SEALED"}`))
				return
			}
			// Para outras situações (erro transitório, control plane): log e continua.
			// Auth vai falhar 401 se as chaves ainda estiverem cifradas — comportamento esperado.
		}

		// --- 8. BRANCH ENVIRONMENT RESOLUTION (Path-Based + Header/Query Param) ---
		// Estratégia híbrida inteligente:
		// 1. Path-Based Routing (URL como fonte de verdade): /project/{slug}/branch/{branch_name}/...
		// 2. Fallback para header X-Cascata-Env ou query param cascata_env
		// Isso garante que reload, copiar link, abrir em outra aba tudo funciona corretamente.

		var cascataEnv string

		// Prioridade 1: Path-Based Routing (extrair da URL)
		// Padrão: /api/data/{slug}/branch/{branch_name}/... ou /project/{slug}/branch/{branch_name}/...
		pathSegments := strings.Split(strings.Trim(path, "/"), "/")
		for i, seg := range pathSegments {
			if seg == "branch" && i+1 < len(pathSegments) {
				candidateBranch := pathSegments[i+1]
				// Valida que não é uma sub-rota fixa como "list", "create", etc.
				if candidateBranch != "list" && candidateBranch != "get" && candidateBranch != "create" &&
					candidateBranch != "update" && candidateBranch != "delete" && candidateBranch != "deploy" &&
					candidateBranch != "access" && candidateBranch != "diff" && candidateBranch != "status" &&
					candidateBranch != "ensure-main" && candidateBranch != "deploys" {
					cascataEnv = candidateBranch
					log.Printf("[ProjectResolver] Path-based branch detected: %s", cascataEnv)
				}
			}
		}

		// Prioridade 2: Header X-Cascata-Env (para chamadas de API programáticas)
		if cascataEnv == "" {
			cascataEnv = r.Header.Get("X-Cascata-Env")
		}

		// Prioridade 3: Query param cascata_env (fallback para WebSocket e casos especiais)
		if cascataEnv == "" {
			cascataEnv = r.URL.Query().Get("cascata_env")
		}

		if cascataEnv != "" && cascataEnv != "live" {
			// Resolve o env para um branch_id real via SystemPool
			// SEGURANÇA: Nunca confiamos no nome do banco diretamente.
			// O env value é o branch.Name, que resolvemos para materialized_db ou data_branch_db_name.
			var branchDBName *string
			var branchType string
			var authConfigJSON *string
			branchValidation := services.SystemPool.QueryRow(r.Context(),
				`SELECT COALESCE(materialized_db, data_branch_db_name), branch_type, auth_config_json
				 FROM system.branches
				 WHERE project_slug = $1 AND name = $2 AND status = 'active'`,
				project.Slug, cascataEnv,
			)
			if err := branchValidation.Scan(&branchDBName, &branchType, &authConfigJSON); err != nil {
				log.Printf("[ProjectResolver] Branch validation failed for %s/%s: %v", project.Slug, cascataEnv, err)
				// Não bloqueia — cai para o banco principal (live)
			} else if branchDBName != nil && *branchDBName != "" {
				// SEGURANÇA: Fazemos um shallow clone do projeto para evitar envenenar o cache global
				// Isso permite que o DbName seja alterado apenas para ESTA requisição
				projectCopy := *project
				projectCopy.DbName = *branchDBName

				// Apply branch-specific auth override if auth_config_json is set
				if authConfigJSON != nil && *authConfigJSON != "" {
					var branchAuth map[string]interface{}
					if err := json.Unmarshal([]byte(*authConfigJSON), &branchAuth); err == nil {
						// Clone Metadata to prevent mutating the global cached metadata
						metadataCopy := projectCopy.Metadata
						// Make a copy of Extra map
						extraCopy := make(map[string]interface{})
						for k, v := range metadataCopy.Extra {
							extraCopy[k] = v
						}

						if ac, ok := branchAuth["auth_config"]; ok {
							extraCopy["auth_config"] = ac
						}
						if as, ok := branchAuth["auth_strategies"]; ok {
							extraCopy["auth_strategies"] = as
						}
						if lt, ok := branchAuth["linked_tables"]; ok {
							extraCopy["linked_tables"] = lt
						}

						metadataCopy.Extra = extraCopy
						projectCopy.Metadata = metadataCopy
						log.Printf("[ProjectResolver] Applied branch-specific auth overrides for branch %s", cascataEnv)
					} else {
						log.Printf("[ProjectResolver] Warning: failed to parse auth_config_json for branch %s: %v", cascataEnv, err)
					}
				}

				project = &projectCopy
				targetEnv = cascataEnv
				ctx.TargetEnv = targetEnv
				log.Printf("[ProjectResolver] Branch routing active: %s/%s → %s (Isolation: Shallow Clone)", project.Slug, cascataEnv, project.DbName)
			} else {
				// ISOLATION: Branch exists but has no active database.
				// The owner MUST call /branch/access first to materialize it.
				log.Printf("[ProjectResolver] Branch %s/%s exists but is not materialized — use /branch/access first", project.Slug, cascataEnv)
				http.Error(w, fmt.Sprintf(`{"error":"Branch '%s' is not materialized","hint":"Call POST /branch/access with {\"branch_name\":\"%s\"} first to activate it","code":"BRANCH_NOT_MATERIALIZED"}`, cascataEnv, cascataEnv), http.StatusPreconditionRequired)
				return
			}
		}

		// --- 9. DATABASE CONNECTION BINDING ---
		// LAZY mode: não conecta no banco do tenant, schemas serão inicializados depois
		// ISOLATION FIX: Quando já fizemos shallow clone acima (branchDBName → projectCopy.DbName),
		// passamos "live" ao GetProjectPool para evitar double-query ao system.branches.
		// O isolamento já está garantido pelo DbName correto no shallow clone.
		poolEnv := targetEnv
		if targetEnv != "live" && project.DbName != "" {
			// Se o DbName já foi substituído pelo branchDBName, o pool deve acessar esse banco
			// diretamente — sem re-consultar system.branches. Forçamos "live" para isso.
			poolEnv = "live"
		}

		var pool *pgxpool.Pool
		if lazy {
			pool, err = services.GetProjectPoolLazy(project, poolEnv)
		} else {
			pool, err = services.GetProjectPool(project, poolEnv)
		}
		if err != nil {
			log.Printf("[ProjectResolver] DB Connect Error for %s: %v", project.Slug, err)
			http.Error(w, `{"error":"Database Connection Failed (Tenant Offline)"}`, 502)
			return
		}

		// Build App Client index for O(1) authentication lookup (Multi-App Architecture)
		if len(project.Metadata.AppClients) > 0 && project.JWTSecret != "" {
			project.AppClientIndex = services.BuildAppClientIndex(project.Metadata.AppClients, project.JWTSecret)
			log.Printf("[ProjectResolver] Built AppClientIndex with %d clients for project %s", len(project.AppClientIndex), project.Slug)
		}

		ctx.Project = project
		ctx.ProjectPool = pool

		// Final Context Propagation
		newR := r.WithContext(context.WithValue(r.Context(), types.CascataCtxKey, ctx))
		next.ServeHTTP(w, newR)
	})
}

func isLocalhost(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || strings.HasPrefix(host, "172.") || strings.HasPrefix(host, "10.") || strings.HasPrefix(host, "192.168.")
}

// classifyAccessPlane determina qual plano de acesso a requisição pertence
func classifyAccessPlane(path string) string {
	if isControlPlaneRoute(path) {
		return "control"
	}
	if isAuthPlaneRoute(path) {
		return "auth"
	}
	if isDataPlaneRoute(path) {
		return "data"
	}
	return "system"
}

// isControlPlaneRoute verifica se é rota de gestão/administração
func isControlPlaneRoute(path string) bool {
	return strings.HasPrefix(path, "/api/control/") ||
		strings.HasPrefix(path, "/tables/") ||
		strings.HasPrefix(path, "/schemas/") ||
		path == "/schemas"
}

// isDataPlaneRoute verifica se é rota de consumo de dados
func isDataPlaneRoute(path string) bool {
	return strings.HasPrefix(path, "/api/data/") ||
		strings.HasPrefix(path, "/rest/v1/") ||
		strings.HasPrefix(path, "/realtime/v1/") ||
		strings.HasPrefix(path, "/storage/v1/") ||
		strings.HasPrefix(path, "/webhook/")
}

// isAuthPlaneRoute verifica se é rota de autenticação
func isAuthPlaneRoute(path string) bool {
	return strings.HasPrefix(path, "/auth/v1/") ||
		strings.HasPrefix(path, "/auth/")
}

// isOAuthCallbackRoute verifica se é callback OAuth
func isOAuthCallbackRoute(path string) bool {
	return strings.HasPrefix(path, "/auth/v1/callback/") ||
		strings.HasPrefix(path, "/auth/v1/oauth/") ||
		strings.HasPrefix(path, "/auth/v1/authorize/")
}

// shouldApplyDomainLocking - Lógica inteligente de Domain Locking (Cascata Guard 2.0)
func shouldApplyDomainLocking(path string, method string, project *types.Project, host string, ctx *types.CascataRequest, resolutionMethod types.ResolutionMethod) bool {
	// 1. Se não tem custom_domain, não aplica locking
	if project.CustomDomain == "" {
		return false
	}

	// 2. Se já está acessando via domínio customizado, permite
	if host == project.CustomDomain {
		return false
	}

	// 3. Se é system request (God Mode), permite
	if ctx.IsSystemRequest {
		return false
	}

	// 4. Se é localhost, permite (desenvolvimento)
	if isLocalhost(host) {
		return false
	}

	// 5. Se foi resolvido por domínio (não por slug/apikey), permite
	if resolutionMethod == types.ResolutionByDomain {
		return false
	}

	// 6. CONTROL PLANE: Só permite via dashboard (slug + system domain)
	//    Gestão de infra NUNCA via domínio compartilhado/IP
	if isControlPlaneRoute(path) {
		return true // Bloqueia - deve usar dashboard
	}

	// 7. AUTH PLANE: Permite OAuth callbacks (precisam do domínio correto)
	if isOAuthCallbackRoute(path) {
		return false // Permite - OAuth providers exigem callback no domínio correto
	}

	// 8. AUTH PLANE (não-OAuth): Bloqueia se não é custom domain
	//    Login direto deve usar o domínio correto
	if isAuthPlaneRoute(path) && !isOAuthCallbackRoute(path) {
		return true
	}

	// 9. DATA PLANE: GET/OPTIONS permite via slug (API exploratória)
	//    Mas POST/PUT/PATCH/DELETE exige custom domain
	if isDataPlaneRoute(path) {
		if method == "GET" || method == "OPTIONS" || method == "HEAD" {
			return false // Permite leitura via slug
		}
		// Métodos de escrita exigem domínio customizado
		return true
	}

	// Default: bloqueia para segurança
	return true
}