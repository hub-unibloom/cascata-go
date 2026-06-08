package services

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"time"
)

// ============================================================================
// COMPILED AUTOMATION SERVICE
// High-performance workflow engine with Dragonfly cache
// ============================================================================

// CompiledAutomationService gerencia automações compiladas
type CompiledAutomationService struct {
	CryptoSvc     *CryptoService
	registry      *NodeRegistry
	compiler      *Compiler
	cache         *CompiledFlowCache
}

// CompiledFlowCache cache de flows compilados
type CompiledFlowCache struct {
	flows map[string]*cachedCompiledFlow
	mu    sync.RWMutex
}

type cachedCompiledFlow struct {
	flow      *CompiledFlow
	loadedAt  time.Time
	projectSlug string
	automationID string
}

const compiledFlowCacheTTL = 5 * time.Minute

// NewCompiledAutomationService cria um novo serviço
func NewCompiledAutomationService(cryptoSvc *CryptoService) *CompiledAutomationService {
	registry := &NodeRegistry{
		builders: make(map[AutomationNodeType]NodeBuilder),
	}
	
	// Registrar todos os builders
	registry.Register(NodeTrigger, BuildTriggerNode)
	registry.Register(NodeAction, BuildTriggerNode) // Action usa mesmo que trigger por enquanto
	registry.Register(NodeLogic, BuildLogicNode)
	registry.Register(NodeCondition, BuildConditionNode)
	registry.Register(NodeResponse, BuildResponseNode)
	registry.Register(NodeQuery, BuildSQLNode)
	registry.Register(NodeHTTP, BuildHTTPNode)
	registry.Register(AutomationNodeType("http_request"), BuildHTTPNode)
	registry.Register(NodeTransform, BuildTransformNode)
	registry.Register(NodeData, BuildDataNode)
	registry.Register(NodeRPC, BuildRPCNode)
	registry.Register(NodeConvert, BuildConvertNode)
	registry.Register(NodeMath, BuildMathNode)
	
	return &CompiledAutomationService{
		CryptoSvc: cryptoSvc,
		registry:  registry,
		compiler:  NewCompiler(registry),
		cache: &CompiledFlowCache{
			flows: make(map[string]*cachedCompiledFlow),
		},
	}
}

// GetOrCompileFlow obtém ou compila um flow
func (s *CompiledAutomationService) GetOrCompileFlow(automationID, projectSlug string, nodes []AutomationNode) (*CompiledFlow, error) {
	cacheKey := fmt.Sprintf("%s:%s", projectSlug, automationID)
	
	// Tentar cache em memória
	s.cache.mu.RLock()
	cached, exists := s.cache.flows[cacheKey]
	s.cache.mu.RUnlock()
	
	if exists && time.Since(cached.loadedAt) < compiledFlowCacheTTL {
		return cached.flow, nil
	}
	
	// Compilar
	flow, err := s.compiler.Compile(automationID, nodes)
	if err != nil {
		return nil, fmt.Errorf("failed to compile flow: %w", err)
	}
	
	// Guardar em cache
	s.cache.mu.Lock()
	s.cache.flows[cacheKey] = &cachedCompiledFlow{
		flow:         flow,
		loadedAt:     time.Now(),
		projectSlug:  projectSlug,
		automationID: automationID,
	}
	s.cache.mu.Unlock()
	
	return flow, nil
}

// InvalidateCache invalida o cache de um projeto
func (s *CompiledAutomationService) InvalidateCache(projectSlug string) {
	s.cache.mu.Lock()
	defer s.cache.mu.Unlock()
	
	prefix := projectSlug + ":"
	for key := range s.cache.flows {
		if strings.HasPrefix(key, prefix) {
			delete(s.cache.flows, key)
		}
	}
}

// GetActiveInterceptors carrega interceptors ativos para um projeto (API_INTERCEPT)
// Agora retorna apenas workflows com status='active' (não is_active)
func (s *CompiledAutomationService) GetActiveInterceptors(ctx context.Context, projectSlug string) ([]map[string]interface{}, error) {
	rows, err := SystemPool.Query(ctx,
		`SELECT id, nodes, trigger_config
		 FROM system.automations
		 WHERE project_slug = $1
		 AND status = 'active'
		 AND trigger_type = 'API_INTERCEPT'`,
		projectSlug)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var automations []map[string]interface{}
	for rows.Next() {
		var id string
		var nodes, triggerConfig json.RawMessage
		if err := rows.Scan(&id, &nodes, &triggerConfig); err == nil {
			log.Printf("[GetActiveInterceptors] Loaded automation id=%s, trigger_config=%s, nodes_len=%d", id, string(triggerConfig), len(nodes))
			automations = append(automations, map[string]interface{}{
				"id":             id,
				"nodes":          nodes,
				"trigger_config": triggerConfig,
			})
		} else {
			log.Printf("[GetActiveInterceptors] Error scanning row: %v", err)
		}
	}
	log.Printf("[GetActiveInterceptors] Total automations loaded: %d", len(automations))
	return automations, nil
}

// HasActiveAutomationForTable verifica se existe automação ativa para uma tabela/evento específico
// Retorna a automação e true se encontrada, nil e false caso contrário
func (s *CompiledAutomationService) HasActiveAutomationForTable(ctx context.Context, projectSlug, tableName, eventType string) (map[string]interface{}, bool) {
	automations, err := s.GetActiveInterceptors(ctx, projectSlug)
	if err != nil || len(automations) == 0 {
		return nil, false
	}
	
	for _, automation := range automations {
		automationID, _ := automation["id"].(string)
		
		// Parse trigger_config
		triggerConfigRaw := automation["trigger_config"]
		var triggerConfig map[string]interface{}
		
		if triggerConfigBytes, ok := triggerConfigRaw.(json.RawMessage); ok {
			if err := json.Unmarshal(triggerConfigBytes, &triggerConfig); err != nil {
				log.Printf("[HasActiveAutomationForTable] Automation %s - Failed to unmarshal trigger_config: %v", automationID, err)
				continue
			}
		} else if tc, ok := triggerConfigRaw.(map[string]interface{}); ok {
			triggerConfig = tc
		} else {
			continue
		}
		
		tbl, _ := triggerConfig["table"].(string)
		evt, _ := triggerConfig["event"].(string)
		
		// Match table and event (support wildcards)
		if (tbl == tableName || tbl == "*" || tbl == "") && 
		   (evt == eventType || evt == "*" || evt == "") {
			log.Printf("[HasActiveAutomationForTable] Found matching automation %s for table=%s, event=%s", automationID, tableName, eventType)
			return automation, true
		}
	}
	
	return nil, false
}

// ExecuteAutomationPrePersist executa uma automação no modo pre-persist
// A automação deve conter nós Data para salvar no banco e Response para responder ao cliente
func (s *CompiledAutomationService) ExecuteAutomationPrePersist(ctx context.Context, automation map[string]interface{}, projectSlug string, payload interface{}, automationCtx *AutomationContext) (interface{}, error) {
	automationID, _ := automation["id"].(string)
	
	log.Printf("[ExecuteAutomationPrePersist] Executing automation %s for project %s", automationID, projectSlug)
	
	// Parse nodes
	nodesRaw, _ := automation["nodes"].(json.RawMessage)
	if nodesRaw == nil {
		return nil, fmt.Errorf("automation %s missing nodes", automationID)
	}
	
	var nodes []AutomationNode
	if err := json.Unmarshal(nodesRaw, &nodes); err != nil {
		return nil, fmt.Errorf("failed to unmarshal nodes for automation %s: %w", automationID, err)
	}
	
	// Compilar e executar
	flow, err := s.GetOrCompileFlow(automationID, projectSlug, nodes)
	if err != nil {
		return nil, fmt.Errorf("failed to compile flow for automation %s: %w", automationID, err)
	}
	
	// Usar versão com logging estruturado
	result, err := ExecuteCompiledFlowWithLogging(ctx, flow, payload, automationCtx, automationID, projectSlug)
	if err != nil {
		s.logRun(automationID, projectSlug, "failed", 0, payload, nil, err.Error())
		return nil, err
	}
	
	// Log success
	start := time.Now()
	elapsed := time.Since(start).Milliseconds()
	s.logRun(automationID, projectSlug, "success", elapsed, payload, result, "")
	
	log.Printf("[ExecuteAutomationPrePersist] Automation %s completed successfully", automationID)
	return result, nil
}

// GetRequestInterceptors carrega REQUEST_INTERCEPT automations ativas para um projeto
// Usado no Security Gateway para interceptar requests antes da persistência
func (s *CompiledAutomationService) GetRequestInterceptors(ctx context.Context, projectSlug string) ([]map[string]interface{}, error) {
	rows, err := SystemPool.Query(ctx,
		`SELECT id, nodes, trigger_config
		 FROM system.automations
		 WHERE project_slug = $1
		 AND status = 'active'
		 AND trigger_type = 'REQUEST_INTERCEPT'`,
		projectSlug)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var automations []map[string]interface{}
	for rows.Next() {
		var id string
		var nodes, triggerConfig json.RawMessage
		if err := rows.Scan(&id, &nodes, &triggerConfig); err == nil {
			automations = append(automations, map[string]interface{}{
				"id":             id,
				"nodes":          nodes,
				"trigger_config": triggerConfig,
			})
		}
	}
	return automations, nil
}

// InterceptResponse intercepta e processa responses
func (s *CompiledAutomationService) InterceptResponse(ctx context.Context, projectSlug, tableName, eventType string, initialPayload interface{}, automationCtx *AutomationContext) interface{} {
	interceptors, err := s.GetActiveInterceptors(ctx, projectSlug)
	if err != nil {
		log.Printf("[InterceptResponse] Error getting interceptors: %v", err)
		return initialPayload
	}
	if len(interceptors) == 0 {
		log.Printf("[InterceptResponse] No active interceptors found for project %s", projectSlug)
		return initialPayload
	}
	
	log.Printf("[InterceptResponse] Found %d interceptors for project %s, table=%s, event=%s", len(interceptors), projectSlug, tableName, eventType)

	currentPayload := initialPayload
	for _, automation := range interceptors {
		automationID, _ := automation["id"].(string)
		
		// Debug: mostrar o tipo real do trigger_config
		triggerConfigRaw := automation["trigger_config"]
		log.Printf("[InterceptResponse] Automation %s - trigger_config type: %T, value: %s", automationID, triggerConfigRaw, string(triggerConfigRaw.(json.RawMessage)))
		
		// Verificar se aplica a esta tabela/evento
		// O trigger_config vem como json.RawMessage, precisamos fazer unmarshal
		var triggerConfig map[string]interface{}
		if triggerConfigBytes, ok := triggerConfigRaw.(json.RawMessage); ok {
			if err := json.Unmarshal(triggerConfigBytes, &triggerConfig); err != nil {
				log.Printf("[InterceptResponse] Automation %s - Failed to unmarshal trigger_config: %v, raw: %s", automationID, err, string(triggerConfigBytes))
				continue
			}
		} else if tc, ok := triggerConfigRaw.(map[string]interface{}); ok {
			triggerConfig = tc
		} else {
			log.Printf("[InterceptResponse] Automation %s - trigger_config is nil or wrong type: %T", automationID, triggerConfigRaw)
			continue
		}
		
		tbl, _ := triggerConfig["table"].(string)
		evt, _ := triggerConfig["event"].(string)
		
		log.Printf("[InterceptResponse] Checking automation %s: triggerConfig=%+v, table=%s, event=%s against target: table=%s, event=%s", 
			automationID, triggerConfig, tbl, evt, tableName, eventType)
		
		if (tbl != tableName && tbl != "*") || (evt != eventType && evt != "*") {
			log.Printf("[InterceptResponse] Automation does not match, skipping")
			continue
		}

		// Parse nodes
		nodesRaw, _ := automation["nodes"].(json.RawMessage)
		if nodesRaw == nil {
			log.Printf("[InterceptResponse] Automation missing nodes, skipping")
			continue
		}
		
		var nodes []AutomationNode
		if err := json.Unmarshal(nodesRaw, &nodes); err != nil {
			log.Printf("[InterceptResponse] Failed to unmarshal nodes: %v", err)
			continue
		}
		
		log.Printf("[InterceptResponse] Parsed %d nodes", len(nodes))

		// Obter flow compilado (automationID já declarado no início do loop)
		automationID, _ = automation["id"].(string)
		flow, err := s.GetOrCompileFlow(automationID, projectSlug, nodes)
		if err != nil {
			log.Printf("[InterceptResponse] Failed to compile flow for automation %s: %v", automationID, err)
			continue
		}
		
		log.Printf("[InterceptResponse] Flow compiled successfully, starting execution with %d max steps", flow.MaxSteps)

		// Executar com logging estruturado
		result, err := ExecuteCompiledFlowWithLogging(ctx, flow, currentPayload, automationCtx, automationID, projectSlug)
		if err == nil {
			log.Printf("[InterceptResponse] Flow executed successfully")
			currentPayload = result
		} else {
			log.Printf("[InterceptResponse] Flow execution error for automation %s: %v", automationID, err)
		}
	}

	return currentPayload
}

// DispatchAsyncTrigger dispara execução assíncrona via Queue System
func (s *CompiledAutomationService) DispatchAsyncTrigger(ctx context.Context, automationID, projectSlug string, nodes []AutomationNode, triggerPayload interface{}, automationCtx *AutomationContext) {
	// Buscar automação no banco para obter configurações de queue
	var automation Automation
	err := SystemPool.QueryRow(ctx,
		`SELECT id, queue_retries, queue_retry_delay, queue_priority 
		 FROM system.automations 
		 WHERE id = $1 AND project_slug = $2`,
		automationID, projectSlug).Scan(
		&automation.ID, &automation.QueueRetries, &automation.QueueRetryDelay, &automation.QueuePriority)
	
	// Valores padrão se não encontrar ou campos NULL
	maxAttempts := 3
	retryDelayMs := 1000
	priority := PriorityNormal
	
	if err == nil {
		// Usar configurações da automação (Worner decide!)
		if automation.QueueRetries > 0 {
			maxAttempts = automation.QueueRetries
		}
		if automation.QueueRetryDelay > 0 {
			retryDelayMs = automation.QueueRetryDelay
		}
		if automation.QueuePriority > 0 {
			priority = automation.QueuePriority
		}
	} else {
		log.Printf("[CompiledAutomation:Queue] Could not load automation config for %s: %v, using defaults", automationID, err)
	}
	
	// Criar job de automação com configurações personalizadas
	job := &AutomationJob{
		AutomationID: automationID,
		ProjectSlug:  projectSlug,
		Nodes:        nodes,
		Payload:      triggerPayload,
		Context:      automationCtx,
		Priority:     priority,      // Configurável: 1=high, 5=normal, 10=low
		MaxAttempts:  maxAttempts,   // Configurável: padrão 3, pode ser 0 (sem retry) ou 10+
		RetryDelayMs: retryDelayMs,  // Configurável: padrão 1000ms
	}
	
	// Adicionar à fila (persistente, com retries, DLQ)
	if err := AddAutomationJob(ctx, job); err != nil {
		log.Printf("[CompiledAutomation:Queue] Failed to queue job: %v", err)
		// Fallback: executar diretamente (fire-and-forget)
		go func() {
			s.ExecuteSync(context.Background(), automationID, projectSlug, nodes, triggerPayload, automationCtx)
		}()
		return
	}
	
	log.Printf("[CompiledAutomation:Queue] Job queued for automation %s (retries: %d, delay: %dms, priority: %d)", 
		automationID, maxAttempts, retryDelayMs, priority)
}

// ExecuteSync executa uma automação síncrona
func (s *CompiledAutomationService) ExecuteSync(ctx context.Context, automationID, projectSlug string, nodes []AutomationNode, payload interface{}, automationCtx *AutomationContext) (interface{}, error) {
	start := time.Now()
	
	// Compilar
	flow, err := s.GetOrCompileFlow(automationID, projectSlug, nodes)
	if err != nil {
		elapsed := time.Since(start).Milliseconds()
		s.logRun(automationID, projectSlug, "failed", elapsed, payload, nil, err.Error())
		return nil, err
	}

	// Executar com logging estruturado
	result, err := ExecuteCompiledFlowWithLogging(ctx, flow, payload, automationCtx, automationID, projectSlug)
	elapsed := time.Since(start).Milliseconds()
	
	if err != nil {
		s.logRun(automationID, projectSlug, "failed", elapsed, payload, nil, err.Error())
		return nil, err
	}
	
	s.logRun(automationID, projectSlug, "success", elapsed, payload, result, "")
	return result, nil
}

// logRun registra execução no banco (fire-and-forget)
func (s *CompiledAutomationService) logRun(automationID, projectSlug, status string, elapsedMs int64, triggerPayload, finalOutput interface{}, errorMsg string) {
	go func() {
		payloadJSON, _ := json.Marshal(triggerPayload)
		outputJSON, _ := json.Marshal(finalOutput)
		
		_, _ = SystemPool.Exec(context.Background(),
			`INSERT INTO system.automation_runs
				(automation_id, project_slug, status, execution_time_ms, trigger_payload, final_output, error_message)
			 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			automationID, projectSlug, status, elapsedMs, string(payloadJSON), string(outputJSON), errorMsg)
	}()
}

// ============================================================================
// Request Interceptor (Sequestro de Entrada)
// ============================================================================

// RequestInterceptor middleware para interceptar requests
func (s *CompiledAutomationService) RequestInterceptor(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Extrair project slug da URL
		projectSlug := extractProjectSlug(r.URL.Path)
		if projectSlug == "" {
			next.ServeHTTP(w, r)
			return
		}

		// Verificar se existe interceptor para este path
		interceptors, err := s.getRequestInterceptors(r.Context(), projectSlug, r.URL.Path)
		if err != nil || len(interceptors) == 0 {
			next.ServeHTTP(w, r)
			return
		}

		// Ler body
		bodyBytes, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(bodyBytes))

		var payload interface{}
		if r.Header.Get("Content-Type") == "application/json" {
			json.Unmarshal(bodyBytes, &payload)
		} else {
			payload = string(bodyBytes)
		}

		// Contexto de automação
		automationCtx := &AutomationContext{
			ProjectSlug: projectSlug,
			Payload:     payload,
			Vars:        make(map[string]interface{}),
			UserRole:    "authenticated",
		}

		// Extrair JWT claims se houver
		if auth := r.Header.Get("Authorization"); auth != "" {
			claims := s.extractJWTClaims(auth)
			automationCtx.JWTClaims = claims
			if role, ok := claims["role"].(string); ok {
				automationCtx.UserRole = role
			}
		}

		// Executar interceptors
		modifiedPayload := payload
		blockRequest := false
		var blockStatus int
		var blockBody interface{}

		for _, interceptor := range interceptors {
			nodesRaw, _ := interceptor["nodes"].(json.RawMessage)
			var nodes []AutomationNode
			if err := json.Unmarshal(nodesRaw, &nodes); err != nil {
				continue
			}

			automationID, _ := interceptor["id"].(string)
			flow, err := s.GetOrCompileFlow(automationID, projectSlug, nodes)
			if err != nil {
				continue
			}

			result, err := ExecuteCompiledFlowWithLogging(r.Context(), flow, modifiedPayload, automationCtx, automationID, projectSlug)
			if err != nil {
				// Verificar se é "abort" ou erro
				if resMap, ok := result.(map[string]interface{}); ok {
					if abort, _ := resMap["__abort"].(bool); abort {
						blockRequest = true
						if status, ok := resMap["status"].(float64); ok {
							blockStatus = int(status)
						} else {
							blockStatus = 403
						}
						blockBody = resMap["body"]
						break
					}
				}
				continue
			}

			// Resultado pode modificar payload
			if resMap, ok := result.(map[string]interface{}); ok {
				if newPayload, ok := resMap["$payload"]; ok {
					modifiedPayload = newPayload
				}
			}
		}

		if blockRequest {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(blockStatus)
			if blockBody != nil {
				json.NewEncoder(w).Encode(blockBody)
			}
			return
		}

		// Se payload foi modificado, recriar body
		if !reflect.DeepEqual(modifiedPayload, payload) {
			newBody, _ := json.Marshal(modifiedPayload)
			r.Body = io.NopCloser(bytes.NewReader(newBody))
			r.ContentLength = int64(len(newBody))
		}

		next.ServeHTTP(w, r)
	})
}

func (s *CompiledAutomationService) getRequestInterceptors(ctx context.Context, projectSlug, path string) ([]map[string]interface{}, error) {
	// Buscar apenas workflows com status='active'
	// TODO: Implementar ordenacao por especificidade (numero de filtros) corretamente
	rows, err := SystemPool.Query(ctx,
		`SELECT id, nodes, trigger_config
		 FROM system.automations
		 WHERE project_slug = $1
		 AND status = 'active'
		 AND trigger_type = 'REQUEST_INTERCEPT'
		 AND (trigger_config->>'path' = $2 OR trigger_config->>'path' = '*' OR trigger_config->>'path' IS NULL)`,
		projectSlug, path)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var interceptors []map[string]interface{}
	for rows.Next() {
		var id string
		var nodes, triggerConfig json.RawMessage
		if err := rows.Scan(&id, &nodes, &triggerConfig); err == nil {
			interceptors = append(interceptors, map[string]interface{}{
				"id":             id,
				"nodes":          nodes,
				"trigger_config": triggerConfig,
			})
		}
	}
	return interceptors, nil
}

func (s *CompiledAutomationService) extractJWTClaims(authHeader string) map[string]interface{} {
	// Simplificado - extrair claims de um JWT
	if !strings.HasPrefix(authHeader, "Bearer ") {
		return nil
	}
	token := strings.TrimPrefix(authHeader, "Bearer ")
	
	// Parse JWT (simplificado - em produção usar lib adequada)
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil
	}
	
	payload, _ := base64Decode(parts[1])
	var claims map[string]interface{}
	json.Unmarshal(payload, &claims)
	return claims
}

func base64Decode(s string) ([]byte, error) {
	// Add padding if needed for standard base64
	if len(s)%4 != 0 {
		s += strings.Repeat("=", 4-len(s)%4)
	}
	return base64.URLEncoding.DecodeString(s)
}

func extractProjectSlug(path string) string {
	// Extrai project slug de /api/data/{slug}/...
	parts := strings.Split(path, "/")
	for i, p := range parts {
		if p == "data" && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return ""
}
