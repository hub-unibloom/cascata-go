package nexus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"cascata-backend/internal/types"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// ============================================================================
// NEXUS HOOK RESOLVER — Resolução de Automações e Sequestro de Requisições
// ============================================================================
// Componente que intercepta requisições ANTES do handler padrão do PostgreSQL.
// Consulta cache (Dragonfly) para verificar se existe uma automação ativa
// para a combinação tenant+route+method, e executa o sequestro se aplicável.
//
// Integra-se com o data.go e controllers existentes via hooks no Layer 4.
// ============================================================================

// Constantes de cache e configuração do hook resolver.
const (
	AutomationCachePrefix    = "nexus:automations:"
	AutomationCacheTTL       = 5 * time.Minute
	PlanCachePrefix          = "nexus:plan:"
	PlanCacheTTL             = 24 * time.Hour
	MaxConsecutiveFailures   = 3
	FailureWindowDuration    = 5 * time.Minute
	FailureCounterPrefix     = "nexus:failures:"
	FailureCounterTTL        = 10 * time.Minute
	AlertTableName           = "system.nexus_automation_alerts"
)

// MatchPriority define o nível de prioridade do matching (menor = mais específico).
type MatchPriority int

const (
	PriorityExactRouteAndMethod     MatchPriority = 1 // POST /tables/orders
	PriorityWildcardRouteAndMethod  MatchPriority = 2 // POST /tables/*
	PriorityExactRouteAnyMethod     MatchPriority = 3 // ANY /tables/orders
	PriorityWildcardAll             MatchPriority = 4 // ANY /*
)

// HookType define o tipo de hook (PRE ou POST persist).
type HookType string

const (
	HookPrePersist  HookType = "PRE_PERSIST"
	HookPostPersist HookType = "POST_PERSIST"
	HookWebhook     HookType = "WEBHOOK"
	HookCron        HookType = "CRON"
)

// AutomationRecord representa uma automação armazenada no banco.
type AutomationRecord struct {
	ID            string          `json:"id"`
	TenantID      string          `json:"tenant_id"`
	Name          string          `json:"name"`
	HookType      HookType        `json:"hook_type"`
	RoutePattern  string          `json:"route_pattern"`  // "POST /tables/orders", "ANY /tables/*"
	Method        string          `json:"method"`         // "POST", "GET", "ANY"
	IsActive      bool            `json:"is_active"`
	Version       int             `json:"version"`
	GraphJSON     json.RawMessage `json:"graph_json"`
	Timeout       int             `json:"timeout_seconds"`
	ExecutionMode string          `json:"execution_mode"` // "fast_lane" ou "worker_lane"
	UpdatedAt     time.Time       `json:"updated_at"`
}

// HookResult contém o resultado de um sequestro.
type HookResult struct {
	Intercepted  bool                   `json:"intercepted"`
	ResponseData map[string]interface{} `json:"response_data,omitempty"`
	ResponseCode int                    `json:"response_code"`
	TraceID      string                 `json:"trace_id"`
	DurationMs   int64                  `json:"duration_ms"`
	AutomationID string                 `json:"automation_id"`
	Error        string                 `json:"error,omitempty"`
}

// NexusHookResolver resolve e executa automações de sequestro.
type NexusHookResolver struct {
	Engine       *NexusEngine
	WorkerLane   *WorkerLane
	RDB          *redis.Client
	SystemPool   *pgxpool.Pool
	VaultSvc     SecretResolver
	EnumSvc      EnumResolver
	UserSvc      UserResolver
	Logger       *StructuredLogger
	mu           sync.RWMutex
	planCache    sync.Map // L1 cache em memória para planos compilados
	traceCounter uint64   // Contador atômico para traceIDs
}

// NewNexusHookResolver cria um novo resolver de hooks.
func NewNexusHookResolver(engine *NexusEngine, workerLane *WorkerLane, rdb *redis.Client, systemPool *pgxpool.Pool, vaultSvc SecretResolver, enumSvc EnumResolver, userSvc UserResolver) *NexusHookResolver {
	return &NexusHookResolver{
		Engine:     engine,
		WorkerLane: workerLane,
		RDB:        rdb,
		SystemPool: systemPool,
		VaultSvc:   vaultSvc,
		EnumSvc:    enumSvc,
		UserSvc:    userSvc,
		Logger:     NewStructuredLogger("NexusHookResolver"),
	}
}

// ============================================================================
// PRE_PERSIST — Sequestro Síncrono
// ============================================================================

// ResolvePrePersist verifica se existe automação PRE_PERSIST para a requisição
// e executa o sequestro se aplicável. Retorna HookResult com intercepted=true
// se o sequestro foi ativado, ou intercepted=false para seguir fluxo normal.
func (h *NexusHookResolver) ResolvePrePersist(
	ctx context.Context,
	tenantID string,
	userUUID string,
	userRole string,
	authSource string,
	route string,
	method string,
	headers map[string]string,
	payload map[string]interface{},
) (*HookResult, error) {
	startedAt := time.Now()

	// 1. Busca automação correspondente (cache-first)
	automation, found, err := h.findMatchingAutomation(ctx, tenantID, route, method, HookPrePersist)
	if err != nil {
		h.Logger.Error("resolve.lookup_error", map[string]interface{}{
			"tenant_id": tenantID,
			"route":     route,
			"method":    method,
			"error":     err.Error(),
		})
		// Em caso de erro de lookup, NÃO faz fallback — retorna não interceptado
		return &HookResult{Intercepted: false}, nil
	}

	if !found {
		return &HookResult{Intercepted: false}, nil
	}

	// 2. Verifica se automação está desabilitada por falhas consecutivas
	if h.isAutoDisabled(ctx, automation.ID) {
		h.Logger.Warn("resolve.auto_disabled", map[string]interface{}{
			"automation_id": automation.ID,
			"tenant_id":     tenantID,
		})
		return &HookResult{Intercepted: false}, nil
	}

	traceID := h.generateTraceID()

	// 3. Obtém ou Compila o plano de execução (Cache-First)
	plan, err := h.getOrCompilePlan(ctx, automation, traceID)
	if err != nil {
		h.recordFailure(ctx, automation.ID, tenantID, err)
		return &HookResult{
			Intercepted:  true,
			ResponseCode: 502,
			Error:        fmt.Sprintf("Nexus compilation error: %v", err),
			TraceID:      traceID,
			AutomationID: automation.ID,
			DurationMs:   time.Since(startedAt).Milliseconds(),
		}, err
	}

	// 4. Monta o NexusState
	state := NewNexusState(
		&TriggerContext{
			Type:    string(HookPrePersist),
			Payload: payload,
			Headers: headers,
			Method:  method,
			Route:   route,
		},
		&SecurityContext{
			TenantID:   tenantID,
			UserUUID:   userUUID,
			UserRole:   userRole,
			AuthSource: authSource,
			TraceID:    traceID,
			Timestamp:  time.Now().UTC().Format(time.RFC3339),
		},
		&SystemContext{
			AutomationID:      automation.ID,
			AutomationVersion: automation.Version,
			ExecutionMode:     string(ModeFastLane),
		},
	)
	state.SetContext(ctx)
	state.SetSecretResolver(h.VaultSvc)
	state.SetEnumResolver(h.EnumSvc)
	state.SetUserResolver(h.UserSvc)

	// 5. Executa na Fast Lane (síncrono com timeout)
	result, err := h.Engine.ExecGraph(ctx, plan, state)
	duration := time.Since(startedAt).Milliseconds()
	if result != nil {
		// Audit log assíncrono para não bloquear o caminho crítico
		go h.recordExecutionAsync(ctx, result, automation.ID, tenantID, payload)
	}

	if err != nil {
		// FILTRO NÃO BATEU: se o TriggerComponent retornou ErrFilterNotMatched,
		// significa que o payload não atende ao filtro condicional. Neste caso,
		// NÃO interceptamos — deixamos o fluxo normal prosseguir.
		// Não contabiliza como falha e não aciona auto-disable.
		if errors.Is(err, ErrFilterNotMatched) {
			log.Printf("[NexusHookResolver] Filter not matched — trace=%s, automation=%s, falling through to normal handler", traceID, automation.ID)
			return &HookResult{
				Intercepted:  false,
				TraceID:      traceID,
				AutomationID: automation.ID,
				DurationMs:   duration,
			}, nil
		}

		h.recordFailure(ctx, automation.ID, tenantID, err)

		statusCode := 500
		if ctx.Err() == context.DeadlineExceeded {
			statusCode = 504
		}

		return &HookResult{
			Intercepted:  true,
			ResponseCode: statusCode,
			Error:        err.Error(),
			TraceID:      traceID,
			AutomationID: automation.ID,
			DurationMs:   duration,
		}, err
	}

	// Sequestro bem-sucedido — reseta contagem de falhas
	h.resetFailures(ctx, automation.ID)

	// Log estruturado de sequestro
	h.logSequestro(traceID, tenantID, userUUID, automation, route, method, duration, "success", result.NodesExecuted)

	return &HookResult{
		Intercepted:  true,
		ResponseData: result.ResponseData,
		ResponseCode: result.ResponseCode,
		TraceID:      traceID,
		AutomationID: automation.ID,
		DurationMs:   duration,
	}, nil
}

// ============================================================================
// POST_PERSIST — Sequestro Síncrono e Disparo Assíncrono
// ============================================================================

// ResolvePostPersistSync verifica se existe automação POST_PERSIST síncrona para a requisição
// e executa o sequestro do resultado do banco.
func (h *NexusHookResolver) ResolvePostPersistSync(
	ctx context.Context,
	tenantID string,
	userUUID string,
	authSource string,
	route string,
	method string,
	dbResult interface{}, // Dados vindos do PostgreSQL
	originalPayload map[string]interface{},
	headers map[string]string,
) (*HookResult, error) {
	startedAt := time.Now()

	// 1. Busca automação correspondente
	automation, found, err := h.findMatchingAutomation(ctx, tenantID, route, method, HookPostPersist)
	if err != nil || !found {
		return &HookResult{Intercepted: false}, nil
	}

	if h.isAutoDisabled(ctx, automation.ID) {
		return &HookResult{Intercepted: false}, nil
	}

	traceID := h.generateTraceID()

	// 2. Compila e Prepara (Cache-First)
	plan, err := h.getOrCompilePlan(ctx, automation, traceID)
	if err != nil {
		h.recordFailure(ctx, automation.ID, tenantID, err)
		return &HookResult{Intercepted: true, ResponseCode: 502, TraceID: traceID}, err
	}

	// 3. O Payload do Pós-Banco inclui o resultado do DB
	triggerPayload := make(map[string]interface{})

	// PADRONIZAÇÃO DE SEQUESTRO (Sinergia):
	// No Pós-Evento, o payload que o fluxo deve interpretar como "verdadeiro" é a resposta do banco.
	// As colunas da tabela tornam-se os campos de primeira linha do trigger.
	if m, ok := dbResult.(map[string]interface{}); ok {
		for k, v := range m {
			triggerPayload[k] = v
		}
	} else if rows, ok := dbResult.([]map[string]interface{}); ok && len(rows) > 0 {
		// InsertRows/UpdateRows retornam []map[string]interface{}
		for k, v := range rows[0] {
			triggerPayload[k] = v
		}
	} else if rows, ok := dbResult.([]interface{}); ok && len(rows) > 0 {
		if m, ok := rows[0].(map[string]interface{}); ok {
			for k, v := range m {
				triggerPayload[k] = v
			}
		}
	}

	// Mantemos as referências explícitas para acesso avançado
	triggerPayload["db_result"] = dbResult
	triggerPayload["original_request"] = originalPayload

	state := NewNexusState(
		&TriggerContext{
			Type:    string(HookPostPersist),
			Payload: triggerPayload,
			Headers: headers,
			Method:  method,
			Route:   route,
		},
		&SecurityContext{
			TenantID:   tenantID,
			UserUUID:   userUUID,
			AuthSource: authSource,
			TraceID:    traceID,
			Timestamp:  time.Now().UTC().Format(time.RFC3339),
		},
		&SystemContext{
			AutomationID:      automation.ID,
			AutomationVersion: automation.Version,
			ExecutionMode:     string(ModeFastLane),
		},
	)
	state.SetContext(ctx)
	state.SetSecretResolver(h.VaultSvc)
	state.SetEnumResolver(h.EnumSvc)
	state.SetUserResolver(h.UserSvc)

	// 4. Executa síncronamente
	result, err := h.Engine.ExecGraph(ctx, plan, state)
	duration := time.Since(startedAt).Milliseconds()
	if result != nil {
		// Audit log assíncrono para não bloquear o caminho crítico
		go h.recordExecutionAsync(ctx, result, automation.ID, tenantID, triggerPayload)
	}

	if err != nil {
		if errors.Is(err, ErrFilterNotMatched) {
			return &HookResult{Intercepted: false, TraceID: traceID, AutomationID: automation.ID, DurationMs: duration}, nil
		}
		h.recordFailure(ctx, automation.ID, tenantID, err)
		return &HookResult{Intercepted: true, ResponseCode: 500, TraceID: traceID, AutomationID: automation.ID, DurationMs: duration}, err
	}

	h.resetFailures(ctx, automation.ID)
	h.logSequestro(traceID, tenantID, userUUID, automation, route, method, duration, "success_post", result.NodesExecuted)

	return &HookResult{
		Intercepted:  true,
		ResponseData: result.ResponseData,
		ResponseCode: result.ResponseCode,
		TraceID:      traceID,
		AutomationID: automation.ID,
		DurationMs:   duration,
	}, nil
}

// ResolvePostPersistAsync verifica se existe automação POST_PERSIST e dispara na Worker Lane (Assíncrono).
func (h *NexusHookResolver) ResolvePostPersistAsync(
	ctx context.Context,
	tenantID string,
	userUUID string,
	userRole string,
	authSource string,
	route string,
	method string,
	responseData map[string]interface{},
	originalBody map[string]interface{},
) error {
	// (Mantemos a lógica de enfileiramento assíncrono aqui)
	automation, found, err := h.findMatchingAutomation(ctx, tenantID, route, method, HookPostPersist)
	if err != nil || !found {
		return nil
	}

	traceID := h.generateTraceID()

	h.Logger.Info("post_persist.dispatched", map[string]interface{}{
		"trace_id":      traceID,
		"automation_id": automation.ID,
		"tenant_id":     tenantID,
		"route":         fmt.Sprintf("%s %s", method, route),
	})

	// PADRONIZAÇÃO DE SEQUESTRO (Sinergia):
	// Em Pós-Evento Assíncrono, injetamos os dados do banco no topo do payload.
	triggerData := make(map[string]interface{})
	for k, v := range responseData {
		triggerData[k] = v
	}
	triggerData["db_result"] = responseData
	triggerData["original_request"] = originalBody

	branchName := types.GetBranchName(ctx)
	// Enfileira na Worker Lane
	task := &WorkerTask{
		AutomationID:  automation.ID,
		TenantID:      tenantID,
		UserUUID:      userUUID,
		UserRole:      userRole,
		AuthSource:    authSource,
		TraceID:       traceID,
		GraphJSON:     automation.GraphJSON,
		TriggerData:   triggerData,
		TriggerType:   string(HookPostPersist),
		Route:         route,
		Method:        method,
		Priority:      5, // Normal
		ExecutionMode: ModeWorkerLane,
		BranchName:    branchName,
	}

	return h.WorkerLane.Enqueue(ctx, task)
}

// ============================================================================
// WEBHOOK — Disparo Assíncrono (Public Gateway)
// ============================================================================

// ResolveWebhook dispara uma automação WEBHOOK.
// Se a automação estiver em modo 'fast_lane', executa de forma síncrona e retorna o HookResult.
// Se estiver em modo 'worker_lane', enfileira para execução assíncrona.
func (h *NexusHookResolver) ResolveWebhook(
	ctx context.Context,
	tenantID string,
	userRole string,
	automationID string,
	route string,
	payload map[string]interface{},
	headers map[string]string,
) (*HookResult, error) {
	// 1. Busca automação WEBHOOK específica por ID
	automation, found, err := h.findAutomationByID(ctx, tenantID, automationID)
	if err != nil || !found {
		return nil, fmt.Errorf("nexus: webhook automation not found for id %s", automationID)
	}

	traceID := h.generateTraceID()
	
	// 2. Execução Síncrona (Fast-Lane)
	if automation.ExecutionMode == "fast_lane" {
		startTime := time.Now()
		h.Logger.Info("webhook.sync_execution", map[string]interface{}{
			"trace_id":      traceID,
			"automation_id": automation.ID,
			"tenant_id":     tenantID,
		})

		result, err := h.Engine.Execute(ctx, tenantID, userRole, automation.GraphJSON, payload, headers, ModeFastLane)
		duration := time.Since(startTime).Milliseconds()

		// Registra execução (Sinergia de Telemetria) - Assíncrono
		go h.recordExecutionAsync(ctx, result, automation.ID, tenantID, payload)

		if err != nil {
			return &HookResult{
				Intercepted:  true,
				ResponseCode: 500,
				Error:        err.Error(),
				TraceID:      traceID,
				DurationMs:   duration,
			}, err
		}

		return &HookResult{
			Intercepted:  true,
			ResponseData: result.ResponseData,
			ResponseCode: result.ResponseCode,
			TraceID:      traceID,
			DurationMs:   duration,
			AutomationID: automation.ID,
		}, nil
	}

	// 3. Execução Assíncrona (Worker-Lane)
	h.Logger.Info("webhook.dispatched", map[string]interface{}{
		"trace_id":      traceID,
		"automation_id": automation.ID,
		"tenant_id":     tenantID,
		"route":         route,
	})

	branchName := types.GetBranchName(ctx)
	task := &WorkerTask{
		AutomationID:  automation.ID,
		TenantID:      tenantID,
		UserUUID:      "anon",
		AuthSource:    "webhook",
		TraceID:       traceID,
		GraphJSON:     automation.GraphJSON,
		TriggerData:   payload,
		Headers:       headers,
		TriggerType:   string(HookWebhook),
		Route:         route,
		Method:        "ANY",
		Priority:      1, // Webhooks geralmente têm alta prioridade
		ExecutionMode: ModeWorkerLane,
		BranchName:    branchName,
	}

	err = h.WorkerLane.Enqueue(ctx, task)
	if err != nil {
		return nil, err
	}

	return &HookResult{
		Intercepted: false, // Dispatched, not finished
		TraceID:     traceID,
	}, nil
}

// ============================================================================
// RESOLUÇÃO DE AUTOMAÇÕES (Cache-First)
// ============================================================================

// findMatchingAutomation busca a automação mais específica para a combinação.
// getOrCompilePlan tenta recuperar o plano compilado do cache (L1 memória → L2 Dragonfly) ou o compila se necessário.
func (h *NexusHookResolver) getOrCompilePlan(ctx context.Context, automation *AutomationRecord, traceID string) (*ExecutionPlan, error) {
	planKey := fmt.Sprintf("%s%s:%d", PlanCachePrefix, automation.ID, automation.UpdatedAt.UnixNano())

	// 1. Tenta L1 Cache (sync.Map em memória) - ~100x mais rápido que Dragonfly
	if cachedPlan, ok := h.planCache.Load(planKey); ok {
		if plan, ok := cachedPlan.(*ExecutionPlan); ok {
			h.Logger.Info("nexus.plan_cache_l1_hit", map[string]interface{}{
				"trace_id":      traceID,
				"automation_id": automation.ID,
			})
			return plan, nil
		}
	}

	// 2. Tenta L2 Cache (Dragonfly)
	cachedPlan, err := h.RDB.Get(ctx, planKey).Result()
	if err == nil {
		var plan ExecutionPlan
		if err := json.Unmarshal([]byte(cachedPlan), &plan); err == nil {
			h.Logger.Info("nexus.plan_cache_l2_hit", map[string]interface{}{
				"trace_id":      traceID,
				"automation_id": automation.ID,
			})
			// Promove para L1 cache
			h.planCache.Store(planKey, &plan)
			return &plan, nil
		}
	}

	// 3. Cache Miss: Compila
	h.Logger.Info("nexus.compiling_plan", map[string]interface{}{
		"trace_id":      traceID,
		"automation_id": automation.ID,
	})

	plan, err := h.Engine.Compile(automation.GraphJSON)
	if err != nil {
		return nil, err
	}

	// 4. Salva em ambos os caches
	// L1: sync.Map (sem TTL, evictado por invalidação)
	h.planCache.Store(planKey, plan)
	// L2: Dragonfly (com TTL de 24h)
	if planData, err := json.Marshal(plan); err == nil {
		h.RDB.Set(ctx, planKey, planData, PlanCacheTTL)
	}

	return plan, nil
}

func (h *NexusHookResolver) findMatchingAutomation(
	ctx context.Context,
	tenantID string,
	route string,
	method string,
	hookType HookType,
) (*AutomationRecord, bool, error) {
	branchName := types.GetBranchName(ctx)
	tenantBranchKey := tenantID
	if branchName != "main" {
		tenantBranchKey = tenantID + ":" + branchName
	}

	// 1. Tenta cache no Dragonfly
	cacheKey := fmt.Sprintf("%s%s:%s:%s:%s", AutomationCachePrefix, tenantBranchKey, hookType, method, route)
	cached, err := h.RDB.Get(ctx, cacheKey).Result()
	if err == nil && cached != "" {
		if cached == "__none__" {
			return nil, false, nil // Cache negativo
		}
		var record AutomationRecord
		if err := json.Unmarshal([]byte(cached), &record); err == nil {
			return &record, true, nil
		}
	}

	// 2. Fallback para PostgreSQL
	record, found, err := h.queryAutomationFromDB(ctx, tenantID, route, method, hookType)
	if err != nil {
		return nil, false, err
	}

	// 3. Cacheia o resultado (positivo ou negativo)
	if found {
		recordJSON, _ := json.Marshal(record)
		h.RDB.Set(ctx, cacheKey, string(recordJSON), AutomationCacheTTL)
	} else {
		h.RDB.Set(ctx, cacheKey, "__none__", AutomationCacheTTL)
	}

	return record, found, nil
}

// findAutomationByID busca uma automação específica pelo ID.
func (h *NexusHookResolver) findAutomationByID(
	ctx context.Context,
	tenantID string,
	automationID string,
 ) (*AutomationRecord, bool, error) {
	// 1. Tenta cache no Dragonfly
	cacheKey := fmt.Sprintf("%sid:%s", AutomationCachePrefix, automationID)
	cached, err := h.RDB.Get(ctx, cacheKey).Result()
	if err == nil && cached != "" {
		var record AutomationRecord
		if err := json.Unmarshal([]byte(cached), &record); err == nil {
			return &record, true, nil
		}
	}

	// 2. Fallback para PostgreSQL
	var row struct {
		ID             string
		Name           string
		GraphJSON      []byte
		HookType       string
		RoutePattern   string
		Method         string
		IsActive       bool
		Version        int
		TimeoutSeconds int
		ExecutionMode  string
		UpdatedAt      time.Time
	}

	branchName := types.GetBranchName(ctx)

	err = h.SystemPool.QueryRow(ctx, `
		SELECT id, name, graph_json, hook_type, route_pattern, method, is_active, version, timeout_seconds, execution_mode, updated_at
		FROM system.nexus_automations
		WHERE id = $1 AND tenant_id = $2 AND branch_name = $3 AND is_active = true
	`, automationID, tenantID, branchName).Scan(
		&row.ID, &row.Name, &row.GraphJSON, &row.HookType, &row.RoutePattern,
		&row.Method, &row.IsActive, &row.Version, &row.TimeoutSeconds, &row.ExecutionMode, &row.UpdatedAt,
	)

	if err != nil {
		return nil, false, nil
	}

	record := &AutomationRecord{
		ID:            row.ID,
		TenantID:      tenantID,
		Name:          row.Name,
		HookType:      HookType(row.HookType),
		RoutePattern:  row.RoutePattern,
		Method:        row.Method,
		IsActive:      row.IsActive,
		Version:       row.Version,
		GraphJSON:     json.RawMessage(row.GraphJSON),
		Timeout:       row.TimeoutSeconds,
		ExecutionMode: row.ExecutionMode,
		UpdatedAt:     row.UpdatedAt,
	}

	// 3. Cacheia o resultado
	recordJSON, _ := json.Marshal(record)
	h.RDB.Set(ctx, cacheKey, string(recordJSON), AutomationCacheTTL)

	return record, true, nil
}

// queryAutomationFromDB busca automações no PostgreSQL com regras de prioridade.
// Lê da tabela system.automations (schema Nexus v0 canônico).
func (h *NexusHookResolver) queryAutomationFromDB(
	ctx context.Context,
	tenantID string,
	route string,
	method string,
	hookType HookType,
) (*AutomationRecord, bool, error) {

	// Extrai o nome da tabela da rota: "/tables/medico" → "medico"
	// Ou da rota direta: "/medico" → "medico"
	routeTable := ""
	routeParts := strings.Split(strings.Trim(route, "/"), "/")
	if len(routeParts) > 0 {
		routeTable = routeParts[len(routeParts)-1]
		// Se a rota for /tables/nome, pega o último
		for i, p := range routeParts {
			if p == "tables" && i+1 < len(routeParts) {
				routeTable = routeParts[i+1]
				break
			}
		}
	}

	// Converte método HTTP para tipo de evento
	eventType := ""
	switch strings.ToUpper(method) {
	case "GET":
		eventType = "SELECT"
	case "POST":
		eventType = "INSERT"
	case "PATCH", "PUT":
		eventType = "UPDATE"
	case "DELETE":
		eventType = "DELETE"
	}

	branchName := types.GetBranchName(ctx)
	// Busca automações ativas para este tenant usando a tabela Nexus v0 canônica
	// Filtragem eficiente por tenant, hook_type e ativação no banco
	query := `
		SELECT id, name, graph_json, hook_type, route_pattern, method, 
		       table_name, event_type, is_active, status, timeout_seconds, version, updated_at
		FROM system.nexus_automations
		WHERE tenant_id = $1
		  AND branch_name = $2
		  AND is_active = true
		  AND status = 'active'
		  AND hook_type = $3
		ORDER BY created_at DESC
	`

	rows, err := h.SystemPool.Query(ctx, query, tenantID, branchName, string(hookType))
	if err != nil {
		return nil, false, fmt.Errorf("nexus: DB query failed: %w", err)
	}
	defer rows.Close()

	type automationRow struct {
		ID             string
		Name           string
		GraphJSON      []byte
		HookType       string
		RoutePattern   string
		Method         string
		TableName      *string
		EventType      *string
		IsActive       bool
		Status         string
		TimeoutSeconds int
		Version        int
		UpdatedAt      time.Time
	}

	var candidates []automationRow
	for rows.Next() {
		var row automationRow
		if err := rows.Scan(
			&row.ID, &row.Name, &row.GraphJSON, &row.HookType, &row.RoutePattern,
			&row.Method, &row.TableName, &row.EventType, &row.IsActive, &row.Status,
			&row.TimeoutSeconds, &row.Version, &row.UpdatedAt,
		); err != nil {
			h.Logger.Error("query.scan_error", map[string]interface{}{"error": err.Error()})
			continue
		}
		candidates = append(candidates, row)
	}

	// Avalia cada automação para ver se combina com esta requisição
	for _, row := range candidates {
		// 1. Verifica Tabela (se especificado)
		if row.TableName != nil && *row.TableName != "*" && *row.TableName != "" {
			if *row.TableName != routeTable {
				continue
			}
		}

		// 2. Verifica Evento (se especificado)
		if row.EventType != nil && *row.EventType != "*" && *row.EventType != "" && *row.EventType != "ANY" {
			if *row.EventType != eventType {
				continue
			}
		}

		// 3. Verifica Método (se especificado e não for ANY)
		if row.Method != "ANY" && row.Method != "" {
			if strings.ToUpper(row.Method) != strings.ToUpper(method) {
				continue
			}
		}

		// 4. Verifica Rota (Pattern matching opcional)
		if row.RoutePattern != "*" && row.RoutePattern != "" {
			if !matchRoute(row.RoutePattern, route) {
				continue
			}
		}

		// Match! 
		h.Logger.Info("automation.matched", map[string]interface{}{
			"automation_id": row.ID,
			"tenant_id":     tenantID,
			"route":         route,
			"table":         routeTable,
			"event":         eventType,
			"hook_type":     string(hookType),
		})

		return &AutomationRecord{
			ID:           row.ID,
			TenantID:     tenantID,
			Name:         row.Name,
			HookType:     hookType,
			RoutePattern: row.RoutePattern,
			Method:       row.Method,
			IsActive:     row.IsActive,
			Version:      row.Version,
			GraphJSON:    json.RawMessage(row.GraphJSON),
			Timeout:      row.TimeoutSeconds,
			UpdatedAt:    row.UpdatedAt,
		}, true, nil
	}

	return nil, false, nil
}


// matchRoute verifica se a rota da requisição corresponde ao padrão da automação.
func matchRoute(pattern, actual string) bool {
	if pattern == "*" || pattern == "/*" {
		return true
	}
	if pattern == actual {
		return true
	}

	// Wildcard no final: "/tables/*" corresponde a "/tables/orders"
	if strings.HasSuffix(pattern, "/*") {
		prefix := strings.TrimSuffix(pattern, "/*")
		return strings.HasPrefix(actual, prefix+"/")
	}
	if strings.HasSuffix(pattern, "*") {
		prefix := strings.TrimSuffix(pattern, "*")
		return strings.HasPrefix(actual, prefix)
	}

	return false
}

// ============================================================================
// FAIL-SAFE: Desativação Automática por Falhas
// ============================================================================

// recordFailure registra uma falha e verifica se deve desativar a automação.
func (h *NexusHookResolver) recordFailure(ctx context.Context, automationID, tenantID string, err error) {
	key := fmt.Sprintf("%s%s", FailureCounterPrefix, automationID)

	// Incrementa o contador de falhas
	count, _ := h.RDB.Incr(ctx, key).Result()
	h.RDB.Expire(ctx, key, FailureWindowDuration)

	h.Logger.Warn("sequestro.failure_recorded", map[string]interface{}{
		"automation_id":      automationID,
		"tenant_id":          tenantID,
		"consecutive_fails":  count,
		"threshold":          MaxConsecutiveFailures,
		"error":              err.Error(),
	})

	if count >= int64(MaxConsecutiveFailures) {
		// Auto-desativa a automação
		h.autoDisable(ctx, automationID, tenantID)
	}
}

// autoDisable desativa automaticamente uma automação e gera alerta.
func (h *NexusHookResolver) autoDisable(ctx context.Context, automationID, tenantID string) {
	disableKey := fmt.Sprintf("nexus:disabled:%s", automationID)
	h.RDB.Set(ctx, disableKey, "1", FailureCounterTTL)

	h.Logger.Error("sequestro.auto_disabled", map[string]interface{}{
		"automation_id": automationID,
		"tenant_id":     tenantID,
		"reason":        fmt.Sprintf("exceeded %d consecutive failures in %v", MaxConsecutiveFailures, FailureWindowDuration),
	})

	// Registra alerta no banco (fire-and-forget)
	go func() {
		_, err := h.SystemPool.Exec(context.Background(),
			`INSERT INTO system.nexus_automation_alerts 
			 (automation_id, tenant_id, alert_type, message, created_at)
			 VALUES ($1, $2, 'AUTO_DISABLED', $3, NOW())`,
			automationID, tenantID,
			fmt.Sprintf("Automation auto-disabled after %d consecutive failures", MaxConsecutiveFailures),
		)
		if err != nil {
			log.Printf("[NexusHookResolver] Failed to insert alert: %v", err)
		}
	}()

	// Invalida cache da automação
	h.invalidateAutomationCache(ctx, tenantID)
}

// isAutoDisabled verifica se uma automação foi desativada automaticamente.
func (h *NexusHookResolver) isAutoDisabled(ctx context.Context, automationID string) bool {
	disableKey := fmt.Sprintf("nexus:disabled:%s", automationID)
	val, err := h.RDB.Get(ctx, disableKey).Result()
	return err == nil && val == "1"
}

// resetFailures reseta o contador de falhas após sucesso.
func (h *NexusHookResolver) resetFailures(ctx context.Context, automationID string) {
	key := fmt.Sprintf("%s%s", FailureCounterPrefix, automationID)
	h.RDB.Del(ctx, key)
}

// CleanupAutomationResidues limpa todos os resíduos de uma automação específica quando ela é deletada.
// Este método limpa: cache de planos compilados, contadores de falha, chaves de auto-disable.
// Chamado pelo DeleteAutomation no controller para garantir cleanup completo.
func (h *NexusHookResolver) CleanupAutomationResidues(ctx context.Context, automationID string) {
	// 1. Limpa contadores de falha
	failureKey := fmt.Sprintf("%s%s", FailureCounterPrefix, automationID)
	h.RDB.Del(ctx, failureKey)

	// 2. Limpa chaves de auto-disable
	disableKey := fmt.Sprintf("nexus:disabled:%s", automationID)
	h.RDB.Del(ctx, disableKey)

	// 3. Limpa cache de automação por ID no L2 (Dragonfly)
	idKey := fmt.Sprintf("%sid:%s", AutomationCachePrefix, automationID)
	h.RDB.Del(ctx, idKey)

	// 4. Limpa cache L1 (sync.Map em memória) para os planos compilados deste ID
	prefixToMatch := fmt.Sprintf("%s%s:", PlanCachePrefix, automationID)
	h.planCache.Range(func(key, value interface{}) bool {
		if keyStr, ok := key.(string); ok {
			if strings.HasPrefix(keyStr, prefixToMatch) {
				h.planCache.Delete(key)
			}
		}
		return true
	})

	// 5. Limpa cache L2 (Dragonfly) para os planos compilados deste ID
	l2PlanPattern := fmt.Sprintf("%s%s:*", PlanCachePrefix, automationID)
	planIter := h.RDB.Scan(ctx, 0, l2PlanPattern, 100).Iterator()
	for planIter.Next(ctx) {
		h.RDB.Del(ctx, planIter.Val())
	}

	h.Logger.Info("automation.residues_cleaned", map[string]interface{}{
		"automation_id": automationID,
	})
}

// InvalidateAutomationCache invalida o cache de automações de um tenant (L1 e L2).
func (h *NexusHookResolver) invalidateAutomationCache(ctx context.Context, tenantID string) {
	branchName := types.GetBranchName(ctx)
	tenantBranchKey := tenantID
	if branchName != "main" {
		tenantBranchKey = tenantID + ":" + branchName
	}

	// 1. Busca todos os IDs de automação do tenant e branch para limpeza profunda
	var ids []string
	rows, err := h.SystemPool.Query(ctx, `
		SELECT id::text FROM system.nexus_automations 
		WHERE tenant_id = $1 AND branch_name = $2
	`, tenantID, branchName)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err == nil && id != "" {
				ids = append(ids, id)
			}
		}
	} else {
		h.Logger.Error("cache.invalidation_query_error", map[string]interface{}{
			"tenant_id": tenantID,
			"branch":    branchName,
			"error":     err.Error(),
		})
	}

	// 2. Limpa cache de IDs específicos e seus planos associados
	for _, id := range ids {
		// 2.1 Invalida cache de registro por ID no L2 (Dragonfly)
		idKey := fmt.Sprintf("%sid:%s", AutomationCachePrefix, id)
		h.RDB.Del(ctx, idKey)

		// 2.2 Invalida cache L1 (sync.Map em memória) para os planos compilados deste ID
		prefixToMatch := fmt.Sprintf("%s%s:", PlanCachePrefix, id)
		h.planCache.Range(func(key, value interface{}) bool {
			if keyStr, ok := key.(string); ok {
				if strings.HasPrefix(keyStr, prefixToMatch) {
					h.planCache.Delete(key)
				}
			}
			return true
		})

		// 2.3 Invalida cache L2 (Dragonfly) para os planos compilados deste ID
		l2PlanPattern := fmt.Sprintf("%s%s:*", PlanCachePrefix, id)
		planIter := h.RDB.Scan(ctx, 0, l2PlanPattern, 100).Iterator()
		for planIter.Next(ctx) {
			h.RDB.Del(ctx, planIter.Val())
		}
	}

	// 3. Limpa L2 Cache de roteamento geral do tenant/branch (Dragonfly)
	pattern := fmt.Sprintf("%s%s:*", AutomationCachePrefix, tenantBranchKey)
	iter := h.RDB.Scan(ctx, 0, pattern, 100).Iterator()
	for iter.Next(ctx) {
		h.RDB.Del(ctx, iter.Val())
	}

	// 4. Limpa L1 Cache de roteamento do tenant/branch (sync.Map em memória)
	// Adicionalmente, caso haja chaves antigas contendo o tenantBranchKey, as limpa
	h.planCache.Range(func(key, value interface{}) bool {
		if keyStr, ok := key.(string); ok {
			if strings.Contains(keyStr, tenantBranchKey) {
				h.planCache.Delete(key)
			}
		}
		return true
	})

	h.Logger.Info("cache.invalidated_deep", map[string]interface{}{
		"tenant_id":         tenantID,
		"branch":            branchName,
		"automations_clean": len(ids),
	})
}

// ============================================================================
// UTILITÁRIOS DE PERFORMANCE
// ============================================================================

// generateTraceID gera um traceID único usando contador atômico (~100x mais rápido que time.Now().UnixNano())
func (h *NexusHookResolver) generateTraceID() string {
	counter := atomic.AddUint64(&h.traceCounter, 1)
	return fmt.Sprintf("nxs-%d-%d", time.Now().UnixNano(), counter)
}

// recordExecutionAsync registra o log de execução de forma assíncrona para não bloquear o caminho crítico
func (h *NexusHookResolver) recordExecutionAsync(
	ctx context.Context,
	result *ExecutionResult,
	automationID string,
	tenantID string,
	triggerData map[string]interface{},
) {
	// Usa context.Background() para garantir que o log seja gravado mesmo se o contexto original for cancelado
	// Isso é seguro pois é fire-and-forget e não afeta a resposta ao cliente
	RecordExecution(context.Background(), h.SystemPool, h.Logger, result, automationID, tenantID, triggerData)
}

// ============================================================================
// LOGGING ESTRUTURADO DE SEQUESTRO
// ============================================================================

func (h *NexusHookResolver) logSequestro(
	traceID, tenantID, userUUID string,
	automation *AutomationRecord,
	route, method string,
	durationMs int64,
	status string,
	nodesExecuted int,
) {
	logEntry := map[string]interface{}{
		"event":              "PRE_PERSIST_SEQUESTRO",
		"trace_id":           traceID,
		"tenant_id":          tenantID,
		"user_uuid":          userUUID,
		"automation_id":      automation.ID,
		"automation_version": automation.Version,
		"route":              fmt.Sprintf("%s %s", method, route),
		"duration_ms":        durationMs,
		"status":             status,
		"nodes_executed":     nodesExecuted,
	}

	h.Logger.Info("sequestro.executed", logEntry)
}
