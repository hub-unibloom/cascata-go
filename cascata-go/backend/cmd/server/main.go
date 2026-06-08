package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"cascata-backend/internal/config"
	"cascata-backend/internal/controllers"
	"cascata-backend/internal/domain/branching/deployer"
	"cascata-backend/internal/domain/branching/environment"
	"cascata-backend/internal/middleware"
	"cascata-backend/internal/services"
	"cascata-backend/internal/services/nexus"
	"cascata-backend/internal/utils"
	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/go-co-op/gocron"
	"github.com/jackc/pgx/v5/pgxpool"
)

// vaultWrapper adapta o VaultService do sistema para a interface SecretResolver do Nexus,
// garantindo o desacoplamento entre os pacotes e resolvendo a incompatibilidade de assinaturas.
type vaultWrapper struct {
	svc *services.VaultService
}

func (w *vaultWrapper) Resolve(ctx context.Context, tenantID, identifier string) (string, error) {
	// O Nexus sempre acessa o Vault com o propósito de automação
	val, _, err := w.svc.Resolve(ctx, tenantID, identifier, services.VaultPurposeAutomation)
	return val, err
}

// enumWrapper adapta o acesso ao PostgreSQL para a interface EnumResolver do Nexus,
// permitindo a busca dinâmica de tipos ENUM definidos nos projetos.
type enumWrapper struct {
	poolResolver func(ctx context.Context, tenantID string) (*pgxpool.Pool, error)
}

func (w *enumWrapper) GetEnumValues(ctx context.Context, tenantID, enumName string) ([]string, error) {
	pool, err := w.poolResolver(ctx, tenantID)
	if err != nil {
		return nil, err
	}

	// Busca os valores do ENUM nativo do PostgreSQL
	query := `
		SELECT array_agg(e.enumlabel ORDER BY e.enumsortorder)
		FROM pg_type t
		JOIN pg_namespace n ON n.oid = t.typnamespace
		JOIN pg_enum e ON e.enumtypid = t.oid
		WHERE t.typname = $1
		AND n.nspname NOT IN ('information_schema', 'pg_catalog')
		GROUP BY t.typname`

	var values []string
	err = pool.QueryRow(ctx, query, enumName).Scan(&values)
	if err != nil {
		return nil, err
	}
	return values, nil
}

// userWrapper adapta o acesso ao PostgreSQL para a interface UserResolver do Nexus,
// permitindo busca dinâmica de qualquer tabela vinculada ao usuário autenticado.
// Suporta tabelas do schema auth (via user_id/id) e tabelas concatenadas do public (via user_id FK).
type userWrapper struct {
	poolResolver func(ctx context.Context, tenantID string) (*pgxpool.Pool, error)
}

func (w *userWrapper) GetUserTableData(ctx context.Context, tenantID, userUUID, tableName, schema string) ([]map[string]interface{}, error) {
	if userUUID == "" {
		return nil, fmt.Errorf("no authenticated user")
	}

	pool, err := w.poolResolver(ctx, tenantID)
	if err != nil {
		return nil, err
	}

	// auth.users usa "id" como referência direta; todas as outras tabelas usam "user_id"
	linkColumn := "user_id"
	if schema == "auth" && tableName == "users" {
		linkColumn = "id"
	}

	// Query dinâmica: SELECT * filtrado pelo campo de ligação ao usuário
	query := fmt.Sprintf("SELECT * FROM %s.%s WHERE %s = $1",
		utils.QuoteId(schema),
		utils.QuoteId(tableName),
		utils.QuoteId(linkColumn),
	)

	rows, err := pool.Query(ctx, query, userUUID)
	if err != nil {
		return nil, fmt.Errorf("user table query failed (%s.%s): %w", schema, tableName, err)
	}
	defer rows.Close()

	fieldDescs := rows.FieldDescriptions()
	var results []map[string]interface{}

	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			continue
		}
		row := make(map[string]interface{}, len(fieldDescs))
		for i, fd := range fieldDescs {
			val := values[i]
			// Converte tipos complexos do pgx para interface{} amigáveis ao JSON
			switch v := val.(type) {
			case []byte:
				// Tenta parsear JSONB
				var parsed interface{}
				if len(v) > 0 && json.Unmarshal(v, &parsed) == nil {
					row[fd.Name] = parsed
				} else {
					row[fd.Name] = string(v)
				}
			case [16]byte:
				// pgx retorna UUID como [16]byte em rows.Values()
				row[fd.Name] = fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", v[0:4], v[4:6], v[6:8], v[8:10], v[10:16])
			default:
				// Garante que pgtype.* e outros tipos complexos virem primitivos do Go
				row[fd.Name] = utils.PurifyPgxValue(v)
			}
		}
		results = append(results, row)
	}

	return results, nil
}

func main() {
	// 1. Core Service Initialization (Sovereign Infrastructure)
	config.InitConfig()
	services.InitSystemPool()
	services.InitReaper()
	services.InitLogging() // Inicia sistema de audit logging
	
	// Initialize L2 Persistence (Dragonfly)
	dragonflyAddr := os.Getenv("DRAGONFLY_URL")
	if dragonflyAddr == "" { 
		// Fallback to separate host/port if URL is not provided
		host := os.Getenv("DRAGONFLY_HOST")
		if host == "" { host = "localhost" }
		port := os.Getenv("DRAGONFLY_PORT")
		if port == "" { port = "6379" }
		dragonflyAddr = host + ":" + port
	}
	services.InitDragonfly(dragonflyAddr)

	// Start Async Upload Worker for background processing of large files
	uploadWorker := services.NewUploadWorker()
	uploadWorker.Start()
	log.Println("[Cascata:Init] Async Upload Worker started")

	// EDGE-FIRST Schema Cache: GlobalSchemaCache is auto-initialized as package-level var
	// No explicit Init needed — it uses Dragonfly (L2) + sync.Map (L1) on demand

	// 2. Database Migration Runner (Tier-1 Boot Sequence)
	// Essencial para garantir que o banco de dados esteja pronto antes do tráfego.
	// NOTA: As migrações são a ÚNICA fonte de verdade para o schema.
	// InitSystemTables foi REMOVIDO para eliminar race conditions - as migrações
	// gerenciam todas as tabelas system.* de forma idempotente e versionada.
	migrationsPath := "./migrations"
	if err := services.RunMigrations(services.SystemPool, migrationsPath); err != nil {
		log.Fatalf("[Cascata:Fatal] Migration Failure: %v", err)
	}

	// 3. Initialize Purge Scheduler (DEPOIS das migrações - aguarda schema readiness)
	// O scheduler detecta automaticamente quando o schema está pronto via:
	// - Sinalização local se este worker rodou migrações
	// - Polling inteligente com detecção de colunas específicas
	// - Timeout gracefully se schema não estiver pronto
	services.GetPurgeScheduler(services.SystemPool)

	// 3.1 WARMUP JWT CACHE - Consistência entre workers
	// Popula o cache Dragonfly com JWT secrets de todos os projetos ativos
	// Isso garante que requests JWT possam ser verificados imediatamente,
	// mesmo que este worker tenha reiniciado e perdido o cache em memória
	go func() {
		// Aguarda um pouco para garantir que Dragonfly está conectado
		time.Sleep(2 * time.Second)
		if err := middleware.WarmupJWTCache(context.Background()); err != nil {
			log.Printf("[Cascata:Init] JWT Cache Warmup warning: %v", err)
		} else {
			log.Println("[Cascata:Init] JWT Cache Warmup completed successfully")
		}
	}()

	// 2.1 AUTO-UNSEAL: Desbloquear Crypto Engine automaticamente se Master Secret estiver disponível.
	// Usa retry loop porque pode existir race condition no boot Docker:
	// o healthcheck passa quando o HTTP server está up, mas o engine pode estar
	// ainda processando a chave internamente nos primeiros ciclos.
	masterSecret := os.Getenv("CASCATA_MASTER_SECRET")
	if masterSecret != "" {
		log.Println("[Cascata:Init] Detectada CASCATA_MASTER_SECRET. Iniciando sequência de auto-unseal...")
		cryptoSvc := services.CryptoService{}

		const maxAttempts = 5
		const retryInterval = 3 * time.Second
		var unsealed bool

		for attempt := 1; attempt <= maxAttempts; attempt++ {
			if err := cryptoSvc.Unseal(masterSecret); err != nil {
				log.Printf("[Cascata:Init] Auto-unseal tentativa %d/%d falhou: %v", attempt, maxAttempts, err)
				if attempt < maxAttempts {
					log.Printf("[Cascata:Init] Aguardando %s antes da próxima tentativa...", retryInterval)
					time.Sleep(retryInterval)
				}
			} else {
				log.Printf("[Cascata:Init] ✓ Crypto Engine auto-unsealed com sucesso (tentativa %d/%d)", attempt, maxAttempts)
				unsealed = true
				break
			}
		}

		if !unsealed {
			log.Printf("[Cascata:CRITICAL] ════════════════════════════════════════════════════════")
			log.Printf("[Cascata:CRITICAL] ⚠ AUTO-UNSEAL FALHOU APÓS %d TENTATIVAS", maxAttempts)
			log.Printf("[Cascata:CRITICAL] O Crypto Engine está SELADO. Criação de projetos e")
			log.Printf("[Cascata:CRITICAL] criptografia de chaves estão INDISPONÍVEIS.")
			log.Printf("[Cascata:CRITICAL] CAUSAS PROVÁVEIS:")
			log.Printf("[Cascata:CRITICAL]   1. CASCATA_MASTER_SECRET mudou (reinstalação gerou chave nova)")
			log.Printf("[Cascata:CRITICAL]      → Solução: Delete /cascata-data/crypto/keys.enc e reinicie")
			log.Printf("[Cascata:CRITICAL]   2. Crypto Engine não respondeu no tempo esperado")
			log.Printf("[Cascata:CRITICAL]      → Verifique: docker logs cascata-crypto-engine")
			log.Printf("[Cascata:CRITICAL] ════════════════════════════════════════════════════════")
		}
	} else {
		log.Println("[Cascata:Init] ⚠ CASCATA_MASTER_SECRET não definida. Modo Sovereign Elite ativo — unseal manual necessário.")
	}

	// 3. Control/Data Matrix (Controllers with Dependency Injection)
	cryptoSvc := services.CryptoService{}
	authSvc := services.AuthService{}
	dbSvc := services.DatabaseService{} // Inicializado aqui para AdminController
	certSvc := services.CertificateService{CryptoSvc: &cryptoSvc}

	// ============================================================
	// NEXUS ENGINE v0: Initialize new automation subsystem
	// ============================================================
	vaultSvc := services.NewVaultService(&cryptoSvc)
	
	projectPoolResolver := func(ctx context.Context, tenantID string) (*pgxpool.Pool, error) {
		if req := middleware.GetCascataRequest(ctx); req != nil && req.ProjectPool != nil && req.Project != nil && req.Project.Slug == tenantID {
			return req.ProjectPool, nil
		}
		project := services.GetProjectBySlug(ctx, tenantID)
		if project == nil {
			return nil, fmt.Errorf("project not found: %s", tenantID)
		}
		return services.GetProjectPool(project, "live")
	}

	nexusSvc := nexus.NewNexusService(nexus.NexusServiceConfig{
		RedisClient:    services.GetDragonflyClient(),
		SystemPool:     services.SystemPool,
		MaxWorkers:     4,
		MaxConcurrency: 10,
		ProjectPoolResolver: projectPoolResolver,
		VaultSvc: &vaultWrapper{svc: vaultSvc},
		EnumSvc:  &enumWrapper{poolResolver: projectPoolResolver},
		UserSvc:  &userWrapper{poolResolver: projectPoolResolver},
	})
	// Start Nexus Worker Lane (async POST_PERSIST processing)
	if err := nexusSvc.Start(); err != nil {
		log.Printf("[Cascata:Warn] Nexus Worker Lane failed to start: %v", err)
	} else {
		log.Println("[Cascata:Init] ✓ Nexus Engine v0 initialized — Worker Lane active")
	}
	
	// Inject Nexus into GlobalSchemaCache for centralized Pre-Persist Security Gateway
	services.GlobalSchemaCache.NexusSvc = nexusSvc

	
	backupSvc := services.BackupService{CryptoSvc: &cryptoSvc}

	adminCtrl := &controllers.AdminController{
		CryptoSvc: cryptoSvc,
		AuthSvc:   authSvc,
		DbSvc:     dbSvc,
		CertSvc:   &certSvc,
	}
	authCtrl := &controllers.AuthController{}
	dataCtrl := &controllers.DataController{
		CryptoSvc:    &cryptoSvc,
		ExtensionSvc: services.NewExtensionService(),
		ComputedSvc:  services.NewComputedService(),
		NexusSvc:     nexusSvc,
	}
	storageCtrl := controllers.NewStorageController()
	sitesCtrl := controllers.NewSitesController()
	
	// ============================================================
	// SISTEMA 1 — Branching (Privacy-First por Design)
	// ============================================================
	// Inicializa BranchService para gerenciamento de branches
	branchSvc := environment.NewBranchService(services.SystemPool)
	
	// Inicializa PoolAdapter para o deployer
	poolAdapter := deployer.NewPoolAdapter()
	
	// Inicializa Deployer com logger padrão
	deployerLogger := deployer.NewDefaultLogger()
	branchDeployer := deployer.NewDeployer(poolAdapter, deployerLogger)
	
	// Inicializa BranchController com dependências
	branchCtrl := &controllers.BranchController{
		BranchSvc: branchSvc,
		Deployer:  branchDeployer,
	}

	// [GAP #5] TTL Cleanup Job — Desmaterializa branches expiradas a cada hora
	// Usa gocron para agendar varredura horária de branches com materialized_db != NULL
	// cujo last_accessed_at + TTL < NOW(). Delega para BranchService.CleanupExpiredMaterializations
	// que por sua vez chama DematerializeBranch — mesmo caminho que DeleteBranch.
	branchCleanupScheduler := gocron.NewScheduler(time.UTC)
	branchCleanupScheduler.Every(1).Hour().Do(func() {
		cleaned, err := branchSvc.CleanupExpiredMaterializations(context.Background())
		if err != nil {
			log.Printf("[BranchCleanup] Error during materialization cleanup: %v", err)
		} else if cleaned > 0 {
			log.Printf("[BranchCleanup] Cleaned %d expired materializations", cleaned)
		}
	})
	branchCleanupScheduler.StartAsync()
	log.Println("[Cascata:Init] ✓ Branch materialization TTL cleanup job started (hourly)")

	secretsCtrl := &controllers.SecretsController{
		CryptoSvc: cryptoSvc,
		VaultSvc:  services.NewVaultService(&cryptoSvc),
	}
	mcpCtrl := &controllers.McpController{}
	edgeCtrl := controllers.NewEdgeController(cryptoSvc, services.EdgeService{}, services.NewVaultService(&cryptoSvc))
	aiCtrl := &controllers.AiController{}
	backupCtrl := &controllers.BackupController{
		BackupSvc: &backupSvc,
		CryptoSvc: &cryptoSvc,
	}
	webhookCtrl := &controllers.WebhookController{
		NexusSvc:  nexusSvc,
		CryptoSvc: &cryptoSvc,
		VaultSvc:  services.NewVaultService(&cryptoSvc),
	}
	
	// Initialize Global Vault for services (Rate Limiting, Auth)
	services.GlobalVaultSvc = services.NewVaultService(&cryptoSvc)

	securityCtrl := &controllers.SecurityController{
		VaultSvc:  services.GlobalVaultSvc,
		CryptoSvc: &cryptoSvc,
	} 
	queueCtrl := controllers.NewQueueController()      // Queue metrics and operations
	pushCtrl := controllers.NewPushController(services.SystemPool) // Push notifications
	appClientCtrl := controllers.NewAppClientController() // Multi-App Architecture

	// 3. Router Orchestration (Go-Chi Performance Tier)
	r := chi.NewRouter()

	// Phase 1: Global Resilience Middlewares
	r.Use(chimw.RequestID)
	r.Use(chimw.RealIP)
	r.Use(chimw.Logger)
	r.Use(chimw.Recoverer)
	r.Use(chimw.Timeout(60 * time.Second))

	// ╔══════════════════════════════════════════════════════════════════╗
	// ║  EDGE DEFENSE LAYER 1: Intelligent Edge (3 camadas)              ║
	// ║  Layer 1: IP Hard Cap (via ENV EDGE_IP_HARD_CAP) - bloqueia DDoS ║
	// ║  Layer 2: JWT Parse Local (sem DB) - extrai UUID                   ║
	// ║  Layer 3: Rate Limit por IP+UUID+Tenant+Regra (system.rate_limits) ║
	// ╚══════════════════════════════════════════════════════════════════╝
	r.Use(middleware.IntelligentEdgeLimiter) // NENHUM limite hardcoded - tudo via config

	// ╔══════════════════════════════════════════════════════════════════╗
	// ║  EDGE DEFENSE LAYER 2: Tenancy Resolution (LAZY - No DB Conn)    ║
	// ║  Resolve o projeto mas NÃO conecta no banco do tenant ainda    ║
	// ╚══════════════════════════════════════════════════════════════════╝
	r.Use(middleware.ProjectResolverLazy) // Resolve tenant sem conectar PostgreSQL

	// Phase 2: Cascata Sovereign Middlewares
	r.Use(middleware.DynamicCORS)        // Policy-Driven CORS
	r.Use(middleware.HostGuard)          // 404 Steering & Stealth
	r.Use(middleware.AuditLogger)         // Compliant Transparency
	r.Use(middleware.CascataAuth)        // RBAC/Identity (Sovereign Mode)
	r.Use(middleware.AppClientTableAccess) // Table-level access control for App Clients

	// ╔══════════════════════════════════════════════════════════════════╗
	// ║  EDGE DEFENSE LAYER 3: Hard Security (Dragonfly-based)           ║
	// ║  Panic Mode e Rate Limiting refinado - ainda sem PostgreSQL      ║
	// ╚══════════════════════════════════════════════════════════════════╝
	r.Use(middleware.PanicMode)          // Hard Security Lockdown (Edge Defense - Dragonfly)
	r.Use(middleware.DynamicRateLimiter) // Adaptive Throttling (SystemPool para regras, não ProjectPool)
	r.Use(middleware.DynamicBodyParser)  // Payload Sanitization
	
	// 4. API CONTRACT MAP (Sovereign Tier-1 Parity)

	// [LEGACY COMPATIBILITY] Control Plane Routing (/api/control/*)
	r.Route("/api/control", func(r chi.Router) {
		r.Get("/auth/handshake", adminCtrl.Handshake)
		r.Post("/auth/handshake", adminCtrl.Handshake)
		
		r.Post("/auth/login", adminCtrl.Login)
		r.Post("/auth/verify", adminCtrl.Verify)
		r.Get("/auth/sovereign/status", adminCtrl.SovereignStatus)
		r.Post("/auth/sovereign/unseal", adminCtrl.Unseal)

		// Management Plane (Protected)
		r.Group(func(r chi.Router) {
			r.Use(middleware.RequireManagementRole)
			
			r.Get("/system/settings", adminCtrl.GetSystemSettings)
			r.Post("/system/settings", adminCtrl.UpdateSystemSettings)
			r.Get("/system/public-ip", adminCtrl.GetServerPublicIp)
			r.Get("/me/ip", adminCtrl.GetMyIP)
			r.Get("/system/certificates/status", adminCtrl.ListCertificates)
			r.Get("/system/certificates/task/{taskId}", adminCtrl.GetCertTaskStatus)
			r.Post("/system/certificates", adminCtrl.CreateCertificate)
			r.Post("/system/ssl-check", adminCtrl.CheckSsl)
			r.Delete("/system/certificates/{domain}", adminCtrl.DeleteCertificate)
			r.Post("/system/rebuild-nginx", adminCtrl.RebuildNginx)
			
			r.Get("/projects", adminCtrl.ListProjects)
			r.Post("/projects", adminCtrl.CreateProject)
			
			r.Route("/projects/{slug}", func(r chi.Router) {
				r.Post("/reveal-key", adminCtrl.RevealKey)
				r.Patch("/", adminCtrl.UpdateProject)
				r.Delete("/", adminCtrl.DeleteProject)
				
				// Secrets Vault
				r.Get("/vault", secretsCtrl.ListSecrets)
				r.Post("/vault", secretsCtrl.SetSecret)
				r.Post("/vault/{id}/reveal", secretsCtrl.RevealSecret)
				r.Delete("/vault/{id}", secretsCtrl.DeleteSecret)
				r.Post("/vault/stats", secretsCtrl.GetVaultStats)
				
				// Sovereign Edge
				r.Get("/edge/functions", edgeCtrl.ListFunctions)
				r.Post("/edge/deploy", edgeCtrl.DeployFunction)
				r.Post("/edge/invoke", edgeCtrl.InvokeFunction)
				r.Get("/edge/stats", edgeCtrl.GetStats)

				// Static Sites Deployment
				r.Post("/sites/deploy", sitesCtrl.DeploySite)
				r.Get("/sites", sitesCtrl.ListSites)
				r.Delete("/sites/{id}", sitesCtrl.DeleteSite)
				r.Patch("/sites/{id}", sitesCtrl.UpdateSite)

				// WEBKHOOKS PARITY (Control API)
				r.Get("/webhooks/receivers", webhookCtrl.List)
				r.Post("/webhooks/receivers", webhookCtrl.Create)
				r.Delete("/webhooks/receivers/{id}", webhookCtrl.Delete)

				// BACKUP PARITY (Control API)
				r.Route("/backups", func(r chi.Router) {
					r.Get("/policies", backupCtrl.ListPolicies)
					r.Post("/policies", backupCtrl.CreatePolicy)
					r.Post("/validate", backupCtrl.ValidateConfig)
					r.Patch("/policies/{id}", backupCtrl.UpdatePolicy)
					r.Delete("/policies/{id}", backupCtrl.DeletePolicy)
					r.Post("/policies/{id}/run", backupCtrl.TriggerManual)
					r.Get("/history", backupCtrl.GetHistory)
					r.Get("/history/{historyId}/download", backupCtrl.GetDownloadLink)
					r.Post("/history/{historyId}/restore", backupCtrl.RestoreBackup)
				})
				
				// APP CLIENTS (Multi-App Architecture)
				r.Get("/app-clients", appClientCtrl.ListAppClients)
				r.Post("/app-clients", appClientCtrl.CreateAppClient)
				r.Get("/app-clients/{id}", appClientCtrl.GetAppClient)
				r.Put("/app-clients/{id}", appClientCtrl.UpdateAppClient)
				r.Delete("/app-clients/{id}", appClientCtrl.DeleteAppClient)
				r.Post("/app-clients/{id}/rotate", appClientCtrl.RotateAppClientKey)
			})
		})
	})

	// [STANDARD DATA PLANE] API Data Access (/api/data/{slug}/*)
	r.Route("/api/data/{slug}", func(r chi.Router) {
		r.Get("/metadata", dataCtrl.GetMetadata)
		
		// Observability Hub - API Logs (must be BEFORE generic table routes)
		r.Get("/logs", dataCtrl.GetLogs)
		r.Get("/logs/stats", dataCtrl.GetLogsStats)
		r.Get("/logs/export", dataCtrl.GetLogsExport)
		r.Delete("/logs", dataCtrl.ClearLogs)
		r.Patch("/logs/schedule", dataCtrl.UpdatePurgeSchedule)

		// Log Export Configuration (OpenTelemetry)
		r.Get("/logs/export-config", dataCtrl.GetLogExportConfig)
		r.Post("/logs/export-config", dataCtrl.UpdateLogExportConfig)
		r.Post("/logs/export-config/test", dataCtrl.TestLogExportConnection)
		r.Post("/logs/export-config/api-key", dataCtrl.GenerateLogExportAPIKey)
		
		// MODERN PATHS (Sovereign Mode)
		r.Get("/{tableName}", dataCtrl.HandlePostgrest)
		r.Post("/{tableName}", dataCtrl.HandlePostgrest)
		r.Patch("/{tableName}", dataCtrl.HandlePostgrest)
		r.Delete("/{tableName}", dataCtrl.HandlePostgrest)

		// COMPATIBILITY PATHS (Supabase/PostgREST/FlutterFlow Style)
		r.Route("/rest/v1", func(r chi.Router) {
			// GET /rest/v1 (raiz) retorna Swagger 2.0 spec
			r.Get("/", aiCtrl.GetSwaggerSpec)
			// Rotas de tabela
			r.Get("/{tableName}", dataCtrl.HandlePostgrest)
			r.Post("/{tableName}", dataCtrl.HandlePostgrest)
			r.Patch("/{tableName}", dataCtrl.HandlePostgrest)
			r.Delete("/{tableName}", dataCtrl.HandlePostgrest)
		})
		
		r.Get("/tables/{tableName}", dataCtrl.QueryRows)
		r.Post("/tables/{tableName}/rows", dataCtrl.InsertRows)
		r.Patch("/tables/{tableName}", dataCtrl.UpdateRows)
		r.Put("/tables/{tableName}/rows", dataCtrl.UpdateRows)
		r.Delete("/tables/{tableName}/rows", dataCtrl.DeleteRows)
		r.Delete("/tables/{tableName}", dataCtrl.DeleteTable)
		
		r.Get("/schemas", dataCtrl.GetSchemas)
		r.Get("/realtime", dataCtrl.HandleRealtime)
		r.Get("/stats", dataCtrl.GetStats)
		
		r.Route("/ai", func(r chi.Router) {
			r.Post("/chat", aiCtrl.Chat)
			r.Get("/sessions", aiCtrl.ListSessions)
			r.Post("/sessions/search", aiCtrl.SearchSessions)
			r.Patch("/sessions/{id}", aiCtrl.UpdateSession)
			r.Delete("/sessions/{id}", aiCtrl.DeleteSession)
			r.Get("/history/{session_id}", aiCtrl.GetHistory)
			r.Patch("/history/{id}", aiCtrl.UpdateMessage)
			r.Delete("/history/{id}", aiCtrl.DeleteMessage)
		})

		// Docs routes (OpenAPI spec and documentation pages)
		r.Get("/docs/openapi", aiCtrl.GetOpenApiSpec)
		r.Get("/docs/pages", aiCtrl.ListDocPages)
		
		r.Get("/tables", dataCtrl.ListTables)
		r.Get("/tables/{tableName}/columns", dataCtrl.GetColumns)
		r.Get("/tables/{tableName}/data", dataCtrl.QueryRows)
		r.Post("/tables/{tableName}/rows", dataCtrl.InsertRows)
		r.Patch("/tables/{tableName}", dataCtrl.UpdateRows)
		r.Put("/tables/{tableName}/rows", dataCtrl.UpdateRows)
		r.Delete("/tables/{tableName}/rows", dataCtrl.DeleteRows)
		r.Delete("/tables/{tableName}", dataCtrl.DeleteTable)
		r.Get("/functions", dataCtrl.ListFunctions)
		r.Post("/query", dataCtrl.RunRawQuery)
		r.Get("/deploys", branchCtrl.ListDeploys)
		r.Post("/deploys/restore", branchCtrl.RestoreDeploy)
		
		r.Route("/branch", func(r chi.Router) {
			r.Get("/status", branchCtrl.GetStatus)
			r.Get("/diff", branchCtrl.GetDiff)
			r.Get("/conflicts", branchCtrl.GetConflicts)
			
			// [SISTEMA 1] Branching Privacy-First endpoints
			r.Get("/list", branchCtrl.ListBranches)
			r.Get("/get", branchCtrl.GetBranch)
			r.Post("/create", branchCtrl.CreateBranch)
			r.Put("/update", branchCtrl.UpdateBranch)
			r.Delete("/delete", branchCtrl.DeleteBranch)
			r.Post("/deploy", branchCtrl.DeployBranch)
			r.Post("/ensure-main", branchCtrl.EnsureMainBranch)
			
			// [GAP #5] Access Branch — Materializa thin clone on-demand
			r.Post("/access", branchCtrl.AccessBranch)
		})

		r.Get("/assets", dataCtrl.GetAssets)
		r.Post("/assets", dataCtrl.UpsertAsset)
		r.Delete("/assets/{id}", dataCtrl.DeleteAsset)
		r.Get("/assets/{id}/history", dataCtrl.GetAssetHistory)
		r.Get("/ui-settings/{table}", dataCtrl.GetUiSettings)
		r.Post("/ui-settings/{table}", dataCtrl.SaveUiSettings)
		r.Get("/functions", dataCtrl.ListFunctions)
		r.Get("/triggers", dataCtrl.ListTriggers)
		r.Get("/cron-jobs", dataCtrl.ListCronJobs)
		r.Get("/edge-functions", edgeCtrl.ListFunctions)
		r.Get("/edge-functions/{name}", edgeCtrl.GetFunctionDetails)
		r.Post("/edge-functions/deploy", edgeCtrl.DeployFunction)
		r.Delete("/edge-functions/{name}", edgeCtrl.DeleteFunction)
		r.Get("/edge-functions/{name}/history", edgeCtrl.GetFunctionHistory)
		r.Get("/edge-functions/{name}/history/{historyId}", edgeCtrl.GetHistoryVersionContent)
		r.Post("/edge-functions/{name}/rollback/{historyId}", edgeCtrl.RollbackToVersion)
		r.Get("/rpc/{name}/definition", dataCtrl.GetFunctionDefinition)
		r.Get("/automations", dataCtrl.ListAutomations)
		r.Post("/automations", dataCtrl.UpsertAutomation)
		r.Put("/automations/{id}", dataCtrl.UpsertAutomation)
		r.Patch("/automations/{id}", dataCtrl.UpsertAutomation)
		r.Delete("/automations/{id}", dataCtrl.DeleteAutomation)
		r.Post("/automations/{id}/activate", dataCtrl.ActivateAutomation)
		r.Post("/automations/{id}/deactivate", dataCtrl.DeactivateAutomation)
		r.Get("/automations/stats", dataCtrl.GetAutomationStats)
		r.Get("/automations/runs/{executionId}/logs", dataCtrl.GetAutomationRunLogs)
		r.Get("/automations/runs", dataCtrl.GetAutomationRuns)
		r.Get("/automations/step-logs", dataCtrl.GetAutomationStepLogs)
		r.Get("/automations/executions", dataCtrl.GetAutomationExecutionList)
		r.Post("/automations/{id}/test", dataCtrl.TestAutomation)

		// EXTENSIONS (require management role - TypeScript parity)
		r.Group(func(r chi.Router) {
			r.Use(middleware.RequireManagementRole)
			r.Get("/extensions", dataCtrl.ListExtensions)
			r.Post("/extensions/install", dataCtrl.InstallExtension)
			r.Post("/extensions/uninstall", dataCtrl.UninstallExtension)
			
			// ENUM TYPES (PostgreSQL Native)
			r.Get("/enum-types", dataCtrl.ListEnumTypes)
			r.Post("/enum-types", dataCtrl.CreateEnumType)
			r.Patch("/enum-types/{name}", dataCtrl.UpdateEnumType)
			r.Delete("/enum-types/{name}", dataCtrl.DeleteEnumType)
		})
		
		r.Get("/policies", securityCtrl.ListPolicies)
		r.Post("/policies", securityCtrl.CreatePolicy)
		r.Delete("/policies/{table}/{name}", securityCtrl.DeletePolicy)
		r.Get("/security/status", securityCtrl.GetStatus)
		r.Post("/security/panic", securityCtrl.TogglePanic)
		r.Get("/rate-limits", securityCtrl.ListRateLimits)
		r.Post("/rate-limits", securityCtrl.CreateRateLimit)
		r.Delete("/rate-limits/{id}", securityCtrl.DeleteRateLimit)
		r.Get("/security/key-groups", securityCtrl.ListKeyGroups)
		r.Post("/security/key-groups", securityCtrl.CreateKeyGroup)
		r.Patch("/security/key-groups/{id}", securityCtrl.UpdateKeyGroup)
		r.Delete("/security/key-groups/{id}", securityCtrl.DeleteKeyGroup)
		r.Get("/api-keys", securityCtrl.ListApiKeys)
		r.Post("/api-keys", securityCtrl.CreateApiKey)
		r.Patch("/api-keys/{id}", securityCtrl.UpdateApiKey)
		r.Delete("/api-keys/{id}", securityCtrl.DeleteApiKey)
		
		r.Get("/trigger/{name}/definition", dataCtrl.GetTriggerDefinition)
		r.Get("/recycle-bin", dataCtrl.ListRecycleBin)
		r.Post("/recycle-bin/{table}/restore", dataCtrl.RestoreTable)
		
		r.Route("/auth", func(r chi.Router) {
			r.Get("/users", authCtrl.ListUsers)
			r.Post("/users", authCtrl.CreateUser)
			r.Delete("/users/{id}", authCtrl.DeleteUser)
			r.Get("/orchestration/policies", authCtrl.ListPolicies)
			r.Get("/strategies", authCtrl.GetStrategies)
			
			// User Session Management (TypeScript parity)
			r.Get("/users/{id}/sessions", authCtrl.GetUserSessions)
			r.Delete("/users/{id}/sessions", authCtrl.RevokeOtherSessions)
			r.Delete("/users/{id}/sessions/{sessionId}", authCtrl.RevokeSession)
		})

		r.Route("/storage", func(r chi.Router) {
			r.Get("/buckets", storageCtrl.ListBuckets)
			r.Post("/buckets", storageCtrl.CreateBucket)
			r.Patch("/buckets/{name}", storageCtrl.RenameBucket)
			r.Delete("/buckets/{name}", storageCtrl.DeleteBucket)
			r.Get("/search", storageCtrl.SearchFiles)
			r.Post("/move", storageCtrl.MoveFiles)
			r.Get("/{bucket}/list", storageCtrl.ListBucketContents)
			r.Post("/{bucket}/folder", storageCtrl.CreateFolder)
			r.Post("/{bucket}/upload", storageCtrl.UploadFile)
			r.Post("/{bucket}/sign", storageCtrl.SignUpload)
			r.Get("/{bucket}/sync", storageCtrl.SyncBucket)
			r.Delete("/{bucket}/object", storageCtrl.DeleteFile)
			r.Get("/{bucket}/object/*", storageCtrl.GetFile)
		})

		r.Post("/auth/v1/signup", authCtrl.Signup)
		r.Post("/auth/v1/token", authCtrl.Token)
		r.Get("/auth/v1/user", authCtrl.GetUser)

		// PUSH NOTIFICATIONS ROUTES
		r.Route("/push", func(r chi.Router) {
			// Device management
			r.Post("/devices/register", pushCtrl.RegisterDevice)
			r.Post("/devices/unregister", pushCtrl.UnregisterDevice)
			r.Get("/devices", pushCtrl.ListDevices)

			// Push sending
			r.Post("/send", pushCtrl.SendPush)
			r.Post("/send-bulk", pushCtrl.SendBulkPush)

			// Rules (Automation)
			r.Get("/rules", pushCtrl.ListRules)
			r.Post("/rules", pushCtrl.CreateRule)
			r.Delete("/rules/{id}", pushCtrl.DeleteRule)

			// Templates (I18N)
			r.Get("/templates", pushCtrl.ListTemplates)
			r.Post("/templates", pushCtrl.CreateTemplate)
			r.Put("/templates/{id}", pushCtrl.UpdateTemplate)
			r.Delete("/templates/{id}", pushCtrl.DeleteTemplate)

			// Groups (Segmentation)
			r.Get("/groups", pushCtrl.ListGroups)
			r.Post("/groups", pushCtrl.CreateGroup)
			r.Put("/groups/{id}", pushCtrl.UpdateGroup)
			r.Delete("/groups/{id}", pushCtrl.DeleteGroup)
			r.Post("/groups/{id}/sync", pushCtrl.SyncGroup)

			// Campaigns (Bulk sending)
			r.Get("/campaigns", pushCtrl.ListCampaigns)
			r.Post("/campaigns", pushCtrl.CreateCampaign)
			r.Post("/campaigns/{id}/cancel", pushCtrl.CancelCampaign)

			// History & Analytics
			r.Get("/history", pushCtrl.ListHistory)
			r.Get("/stats", pushCtrl.GetStats)

			// FCM Configuration
			r.Get("/config", pushCtrl.GetFCMConfig)
			r.Post("/config", pushCtrl.SaveFCMConfig)
		})

		r.Post("/rpc/{name}", dataCtrl.ExecuteRpc)
		
		// Edge Functions - Data Plane Invocation
		r.Post("/edge/{name}", edgeCtrl.InvokeFunction)
		
		// MCP Gateway (AI Agent Protocol) - Project Specific
		r.Get("/mcp/sse", mcpCtrl.ConnectSSE)
		r.Post("/mcp/message", mcpCtrl.HandleMessage)
		
		r.Handle("/auth/v1/*", http.HandlerFunc(authCtrl.HandleAuth))
		r.Handle("/auth/*", http.HandlerFunc(authCtrl.HandleAuth))
	})

	// [CUSTOM DOMAIN DIRECT ACCESS] Routes for domain-resolved projects (no /api/data/{slug} prefix)
	// These routes activate when ProjectResolver identifies project via custom domain
	r.Group(func(r chi.Router) {
		// REST compatibility: /rest/v1/{tableName} (SINERGIA - deve funcionar igual ao formato próprio)
		r.Route("/rest/v1", func(r chi.Router) {
			r.Get("/", aiCtrl.GetSwaggerSpec)
			r.Get("/{tableName}", dataCtrl.HandlePostgrest)
			r.Post("/{tableName}", dataCtrl.HandlePostgrest)
			r.Patch("/{tableName}", dataCtrl.HandlePostgrest)
			r.Delete("/{tableName}", dataCtrl.HandlePostgrest)
		})
		
		// Direct table access: /{tableName} (for custom domains) - SINERGIA TOTAL
		// Todas as operações devem funcionar igual ao /rest/v1/{tableName}
		r.Get("/{tableName}", dataCtrl.HandlePostgrest)
		r.Post("/{tableName}", dataCtrl.HandlePostgrest)
		r.Patch("/{tableName}", dataCtrl.HandlePostgrest)
		r.Delete("/{tableName}", dataCtrl.HandlePostgrest)
		
		// Auth endpoints
		r.Post("/auth/v1/signup", authCtrl.Signup)
		r.Post("/auth/v1/token", authCtrl.Token)
		r.Get("/auth/v1/user", authCtrl.GetUser)
		r.Handle("/auth/*", http.HandlerFunc(authCtrl.HandleAuth))
		
		// Storage endpoints
		r.Route("/storage", func(r chi.Router) {
			r.Get("/buckets", storageCtrl.ListBuckets)
			r.Post("/buckets", storageCtrl.CreateBucket)
			r.Get("/{bucket}/list", storageCtrl.ListBucketContents)
			r.Post("/{bucket}/upload", storageCtrl.UploadFile)
			r.Get("/{bucket}/object/*", storageCtrl.GetFile)
		})
		
		// RPC
		r.Post("/rpc/{name}", dataCtrl.ExecuteRpc)
		
		// Edge Functions
		r.Post("/edge/{name}", edgeCtrl.InvokeFunction)
	})

	// Global Infrastructure Services (Direct Access)
	r.Group(func(r chi.Router) {
		r.Use(middleware.RequireManagementRole)
		
		r.Post("/api/storage/{bucket}/upload", storageCtrl.UploadFile)
		r.Get("/api/storage/{bucket}/list", storageCtrl.ListFiles)
		r.Delete("/api/storage/{bucket}/object", storageCtrl.DeleteFile)

		r.Get("/api/mcp/sse", mcpCtrl.ConnectSSE)
		r.Post("/api/mcp/message", mcpCtrl.HandleMessage)
	})

	// Public Webhook Gateway (Enterprise Gateway)
	// Supports both modern clean URLs and legacy parameterized paths.
	// HandleFunc is used to delegate method validation to the controller (supporting GET/POST/ANY as configured).
	r.HandleFunc("/webhook/{pathSlug}", webhookCtrl.HandleIncoming)
	r.HandleFunc("/api/webhooks/in/{projectSlug}/{pathSlug}", webhookCtrl.HandleIncoming)



	// Platform Health Check & Queue Metrics
	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"online","engine":"cascata-go-master","version":"1.2.0-sovereign"}`))
	})
	r.Get("/health/queue", queueCtrl.HealthCheck)
	r.Get("/api/control/queue/stats", queueCtrl.GetQueueStats)
	r.Post("/api/control/queue/dlq/requeue", queueCtrl.RequeueDLQHandler)

	// 5. SERVER ORCHESTRATION & SHUTDOWN
	// Configuração de portas e sockets
	port := os.Getenv("PORT")
	if port == "" { port = "3000" }

	// CASCATA ZERO-NETWORK HOT-PATH: Unix Domain Sockets para comunicação de alta performance
	socketsDir := "/tmp/cascata_sockets"
	if err := os.MkdirAll(socketsDir, 0755); err != nil {
		log.Fatalf("[Cascata:Fatal] Failed to create sockets directory: %v", err)
	}

	// Determinar worker ID baseado no nome do serviço Docker ou índice
	workerID := determineWorkerID()
	socketPath := filepath.Join(socketsDir, fmt.Sprintf("worker_%s.sock", workerID))

	// Remover socket anterior se existir (para evitar "address already in use")
	if err := os.Remove(socketPath); err != nil && !os.IsNotExist(err) {
		log.Printf("[Cascata:Warn] Could not remove old socket: %v", err)
	}

	// Criar servidor HTTP compartilhado entre TCP e Unix Socket
	srv := &http.Server{
		Handler:      middleware.BranchRewriterInterceptor(r),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// WaitGroup para sincronizar graceful shutdown de múltiplos listeners
	var wg sync.WaitGroup
	ctx, cancel := context.WithCancel(context.Background())

	// Iniciar listener TCP (porta 3000)
	wg.Add(1)
	go func() {
		defer wg.Done()
		tcpAddr := ":" + port
		tcpListener, err := net.Listen("tcp", tcpAddr)
		if err != nil {
			log.Fatalf("[Cascata:Fatal] TCP Listener failed: %v", err)
		}
		log.Printf("[Cascata:Go] TCP Engine listening on %s (Tier-1 Parity Active)", tcpAddr)

		// Serve em loop até contexto ser cancelado
		go func() {
			<-ctx.Done()
			tcpListener.Close()
		}()

		if err := srv.Serve(tcpListener); err != nil && err != http.ErrServerClosed && !strings.Contains(err.Error(), "use of closed network connection") {
			log.Printf("[Cascata:Error] TCP Server error: %v", err)
		}
	}()

	// Iniciar listener Unix Socket (Zero-Network Hot-Path)
	wg.Add(1)
	go func() {
		defer wg.Done()
		unixListener, err := net.Listen("unix", socketPath)
		if err != nil {
			log.Fatalf("[Cascata:Fatal] Unix Socket Listener failed: %v", err)
		}

		// Configurar permissões do socket para permitir acesso do nginx
		if err := os.Chmod(socketPath, 0666); err != nil {
			log.Printf("[Cascata:Warn] Could not chmod socket: %v", err)
		}

		log.Printf("[Cascata:Go] Unix Socket Engine listening on %s (Zero-Network Hot-Path Active)", socketPath)

		// Serve em loop até contexto ser cancelado
		go func() {
			<-ctx.Done()
			unixListener.Close()
		}()

		if err := srv.Serve(unixListener); err != nil && err != http.ErrServerClosed && !strings.Contains(err.Error(), "use of closed network connection") {
			log.Printf("[Cascata:Error] Unix Socket Server error: %v", err)
		}

		// Cleanup do socket
		os.Remove(socketPath)
	}()

	// Graceful Exit Pipeline
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	log.Println("[Cascata:System] Initiating graceful shutdown sequence...")

	// Cancelar contexto para parar listeners
	cancel()

	// Shutdown do servidor HTTP com timeout
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("[Cascata:Warn] Graceful Shutdown Error: %v", err)
	}

	// Aguardar listeners terminarem
	wg.Wait()

	// Shutdown Nexus Worker Lane
	nexusSvc.Stop()

	// Shutdown do sistema de logging (flush final dos audit logs)
	services.ShutdownLogging()

	log.Println("[Cascata:System] Engine offline. All resources released.")
}

// determineWorkerID retorna o ID do worker baseado no SERVICE_TYPE ou hostname
// Mapeamento fixo para containers Docker: control=1, data=2, engine=3
// Suporta múltiplos workers: worker_1, worker_2, worker_3, worker_4
func determineWorkerID() string {
	// Verificar variável de ambiente WORKER_ID primeiro
	if workerID := os.Getenv("WORKER_ID"); workerID != "" {
		return workerID
	}

	// Verificar SERVICE_TYPE para mapeamento fixo (Docker Compose)
	serviceType := os.Getenv("SERVICE_TYPE")
	switch serviceType {
	case "control":
		return "1"
	case "data":
		return "2"
	case "engine":
		return "3"
	}

	// Fallback: extrair do hostname Docker (ex: cascata-backend-data-1 -> 1)
	hostname, err := os.Hostname()
	if err == nil && hostname != "" {
		// Padrões comuns: backend-data-1, worker-2, etc.
		parts := strings.Split(hostname, "-")
		for _, part := range parts {
			if part >= "1" && part <= "9" {
				return part
			}
		}
	}

	// Default: worker 1
	return "1"
}	