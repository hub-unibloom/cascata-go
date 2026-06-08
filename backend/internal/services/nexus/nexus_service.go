package nexus

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// ============================================================================
// NEXUS SERVICE — Fachada de Integração com o Sistema Cascata
// ============================================================================
// NexusService é o ponto de entrada único para todo o subsistema Nexus.
// Ele inicializa e conecta NexusEngine, WorkerLane, DLQManager e HookResolver,
// expondo uma API limpa para uso pelos controllers e pelo main.go.
// ============================================================================

// NexusService é a fachada principal do Nexus Engine.
type NexusService struct {
	Engine       *NexusEngine
	WorkerLane   *WorkerLane
	DLQManager   *DLQManager
	HookResolver *NexusHookResolver
	rdb          *redis.Client
	systemPool   *pgxpool.Pool
	logger       *StructuredLogger
}

// NexusServiceConfig contém a configuração necessária para inicializar o NexusService.
type NexusServiceConfig struct {
	RedisClient    *redis.Client
	SystemPool          *pgxpool.Pool
	MaxWorkers          int
	MaxConcurrency      int
	ProjectPoolResolver func(ctx context.Context, tenantID string) (*pgxpool.Pool, error)
	VaultSvc            SecretResolver
	EnumSvc             EnumResolver
	UserSvc             UserResolver
}

// NewNexusService cria e inicializa todo o subsistema Nexus.
func NewNexusService(cfg NexusServiceConfig) *NexusService {
	logger := NewStructuredLogger("NexusService")

	// 1. Cria o motor core com as dependências pesadas da Fase 3
	engine := NewNexusEngine(cfg.SystemPool, cfg.RedisClient, cfg.ProjectPoolResolver, cfg.VaultSvc, cfg.EnumSvc, cfg.UserSvc)

	// 2. Cria a Worker Lane
	workerLane := NewWorkerLane(WorkerLaneConfig{
		RedisClient:    cfg.RedisClient,
		Engine:         engine,
		SystemPool:     cfg.SystemPool,
		MaxWorkers:     cfg.MaxWorkers,
		MaxConcurrency: cfg.MaxConcurrency,
	}, cfg.VaultSvc, cfg.EnumSvc, cfg.UserSvc)

	// 3. Cria o DLQ Manager
	dlqManager := NewDLQManager(cfg.RedisClient, "")

	// 4. Cria o Hook Resolver
	hookResolver := NewNexusHookResolver(engine, workerLane, cfg.RedisClient, cfg.SystemPool, cfg.VaultSvc, cfg.EnumSvc, cfg.UserSvc)

	svc := &NexusService{
		Engine:       engine,
		WorkerLane:   workerLane,
		DLQManager:   dlqManager,
		HookResolver: hookResolver,
		rdb:          cfg.RedisClient,
		systemPool:   cfg.SystemPool,
		logger:       logger,
	}

	logger.Info("nexus.initialized", map[string]interface{}{
		"engine":     "NexusEngine v0",
		"components": len(engine.Registry().RegisteredTypes()),
		"workers":    cfg.MaxWorkers,
	})

	return svc
}

// Start inicia os workers da Worker Lane (para execução assíncrona POST_PERSIST).
func (s *NexusService) Start() error {
	s.logger.Info("nexus.starting", nil)
	return s.WorkerLane.Start()
}

// Stop para os workers gracefully.
func (s *NexusService) Stop() {
	s.logger.Info("nexus.stopping", nil)
	s.WorkerLane.Stop()
}

// ============================================================================
// HOOKS PARA DATA CONTROLLER — API simplificada para uso nos controllers
// ============================================================================

// ResolvePrePersistForTable verifica e executa automações PRE_PERSIST para uma tabela/evento.
// Esta é a API principal chamada pelo data.go nos métodos InsertRows, UpdateRows, DeleteRows, HandlePostgrest.
//
// Parâmetros:
//   - ctx: Context da requisição HTTP
//   - tenantID: Slug do projeto/tenant (já resolvido pelo middleware)
//   - userUUID: UUID do usuário autenticado (já resolvido pelo Layer 2)
//   - authSource: "jwt", "apikey" ou "anon" (já resolvido pelo Layer 2)
//   - tableName: Nome da tabela alvo
//   - eventType: "INSERT", "UPDATE" ou "DELETE"
//   - payload: Dados da requisição (body parseado)
//   - headers: Headers HTTP sanitizados
//
// Retorna:
//   - *HookResult: resultado com intercepted=true se houve sequestro, false caso contrário
//   - error: erro se a execução falhar
func (s *NexusService) ResolvePrePersistForTable(
	ctx context.Context,
	tenantID string,
	userUUID string,
	userRole string,
	authSource string,
	tableName string,
	eventType string,
	payload map[string]interface{},
	headers map[string]string,
) (*HookResult, error) {
	// Constrói a rota no formato que o HookResolver espera
	route := fmt.Sprintf("/tables/%s", tableName)
	method := eventTypeToHTTPMethod(eventType)

	return s.HookResolver.ResolvePrePersist(
		ctx, tenantID, userUUID, userRole, authSource, route, method, headers, payload,
	)
}

// ResolvePrePersistForRoute verifica e executa automações PRE_PERSIST para uma rota genérica.
// Usada por HandlePostgrest e rotas /rest/v1/.
func (s *NexusService) ResolvePrePersistForRoute(
	ctx context.Context,
	tenantID string,
	userUUID string,
	userRole string,
	authSource string,
	route string,
	method string,
	payload map[string]interface{},
	headers map[string]string,
) (*HookResult, error) {
	return s.HookResolver.ResolvePrePersist(
		ctx, tenantID, userUUID, userRole, authSource, route, method, headers, payload,
	)
}

// ResolvePostPersistForTableSync verifica e executa automações POST_PERSIST de forma síncrona.
// Usada quando a automação precisa interceptar o resultado do banco antes de responder ao usuário.
func (s *NexusService) ResolvePostPersistForTableSync(
	ctx context.Context,
	tenantID string,
	userUUID string,
	authSource string,
	tableName string,
	eventType string,
	dbResult interface{},
	originalBody map[string]interface{},
	headers map[string]string,
) (*HookResult, error) {
	route := fmt.Sprintf("/tables/%s", tableName)
	method := eventTypeToHTTPMethod(eventType)

	return s.HookResolver.ResolvePostPersistSync(
		ctx, tenantID, userUUID, authSource, route, method, dbResult, originalBody, headers,
	)
}

// DispatchPostPersistAsync dispara automações POST_PERSIST de forma assíncrona via Worker Lane.
// Não bloqueia a resposta ao cliente. Erros são silenciosos (logged, mas não propagados).
func (s *NexusService) DispatchPostPersistAsync(
	ctx context.Context,
	tenantID string,
	userUUID string,
	userRole string,
	authSource string,
	tableName string,
	eventType string,
	responseData map[string]interface{},
	originalBody map[string]interface{},
) {
	route := fmt.Sprintf("/tables/%s", tableName)
	method := eventTypeToHTTPMethod(eventType)

	err := s.HookResolver.ResolvePostPersistAsync(
		ctx, tenantID, userUUID, userRole, authSource, route, method, responseData, originalBody,
	)
	if err != nil {
		s.logger.Error("post_persist.dispatch_error", map[string]interface{}{
			"tenant_id":  tenantID,
			"table":      tableName,
			"event":      eventType,
			"error":      err.Error(),
		})
	}
}

// ResolveWebhook dispara uma automação do tipo WEBHOOK de forma assíncrona.
func (s *NexusService) ResolveWebhook(
	ctx context.Context,
	tenantID string,
	userRole string,
	automationID string,
	route string,
	payload map[string]interface{},
	headers map[string]string,
) (*HookResult, error) {
	return s.HookResolver.ResolveWebhook(ctx, tenantID, userRole, automationID, route, payload, headers)
}

// InvalidateCache invalida o cache de automações para um tenant.
// Chamado quando automações são criadas, atualizadas ou deletadas.
func (s *NexusService) InvalidateCache(ctx context.Context, tenantID string) {
	s.HookResolver.invalidateAutomationCache(ctx, tenantID)
	s.logger.Info("cache.invalidated", map[string]interface{}{
		"tenant_id": tenantID,
	})
}

// CleanupAutomationResidues limpa todos os resíduos de uma automação específica quando ela é deletada.
// Este método limpa: cache de planos compilados, contadores de falha, chaves de auto-disable.
// Chamado pelo DeleteAutomation no controller para garantir cleanup completo.
func (s *NexusService) CleanupAutomationResidues(ctx context.Context, automationID string) {
	s.HookResolver.CleanupAutomationResidues(ctx, automationID)
	s.logger.Info("automation.residues_cleaned", map[string]interface{}{
		"automation_id": automationID,
	})
}

// ============================================================================
// CRUD DE AUTOMAÇÕES — Para uso pelos controllers de automação
// ============================================================================

// FindAutomationsForTable busca automações ativas por tenant, tabela e tipo de evento.
// Retorna a automação mais específica, conforme regras de prioridade.
func (s *NexusService) FindAutomationsForTable(
	ctx context.Context,
	tenantID string,
	tableName string,
	eventType string,
	hookType HookType,
) (*AutomationRecord, bool, error) {
	route := fmt.Sprintf("/tables/%s", tableName)
	method := eventTypeToHTTPMethod(eventType)

	return s.HookResolver.findMatchingAutomation(ctx, tenantID, route, method, hookType)
}

// LogExecution registra uma execução no banco de dados.
// DEPRECATED: Use RecordExecution do pacote nexus diretamente.
// Mantido para compatibilidade, mas delega para RecordExecution.
func (s *NexusService) LogExecution(result *ExecutionResult, tenantID, automationID string, triggerData map[string]interface{}) {
	// Delega para RecordExecution do pacote nexus (única fonte de verdade)
	// Já é assíncrono via goroutine no caller
	RecordExecution(context.Background(), s.systemPool, s.logger, result, automationID, tenantID, triggerData)
}

// ============================================================================
// HELPERS
// ============================================================================

// eventTypeToHTTPMethod converte evento de banco para método HTTP equivalente.
func eventTypeToHTTPMethod(eventType string) string {
	switch eventType {
	case "SELECT":
		return "GET"
	case "INSERT":
		return "POST"
	case "UPDATE":
		return "PATCH"
	case "DELETE":
		return "DELETE"
	default:
		return "ANY"
	}
}

// ExtractClientIP tenta obter o IP real do cliente, considerando headers de proxy.
func ExtractClientIP(r *http.Request) string {
	if ip := r.Header.Get("X-Forwarded-For"); ip != "" {
		parts := strings.Split(ip, ",")
		return strings.TrimSpace(parts[0])
	}
	if ip := r.Header.Get("X-Real-IP"); ip != "" {
		return strings.TrimSpace(ip)
	}
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	return host
}

// ExtractSafeHeaders extrai headers seguros de um http.Header para uso em automações.
// Remove headers sensíveis (Authorization, Cookie, etc.) e retorna mapa limpo.
func ExtractSafeHeaders(headers map[string][]string) map[string]string {
	safe := make(map[string]string)
	sensitiveHeaders := map[string]bool{
		"Authorization":    true,
		"Cookie":           true,
		"Set-Cookie":       true,
		"X-Forwarded-For":  true,
		"X-Real-Ip":        true,
	}

	for key, values := range headers {
		if sensitiveHeaders[key] {
			continue
		}
		if len(values) > 0 {
			safe[key] = values[0]
		}
	}

	return safe
}

// GetAuthContext extrai de forma segura o userUUID e o authSource do contexto da requisição.
func GetAuthContext(ctx interface{}) (string, string) {
	// Usamos reflexão ou type assertion segura para evitar dependência circular pesada
	// mas aqui como estamos no pacote nexus, vamos assumir que o controller passa o que precisamos.
	// Por simplicidade e performance, vamos extrair diretamente no controller ou usar um helper de tipos.
	return "", "" // Placeholder - será implementado com tipos concretos no controller se necessário
}
