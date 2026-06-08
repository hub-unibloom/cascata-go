package nexus

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// ============================================================================
// NEXUS ENGINE — Core Runtime do Motor FBP
// ============================================================================
// Orquestrador principal. Recebe um ExecutionPlan compilado, instancia
// componentes, conecta-os na topologia definida e executa o grafo
// respeitando a ordem topológica com suporte a paralelismo controlado.
// ============================================================================

// NexusEngine é o orquestrador principal do motor FBP.
type NexusEngine struct {
	registry            *ComponentRegistry
	compiler            *NexusCompiler
	logger              *StructuredLogger
	mu                  sync.RWMutex
	ProjectPoolResolver func(ctx context.Context, tenantID string) (*pgxpool.Pool, error)
	
	// Resolvers (Sinergia de Variáveis)
	VaultSvc SecretResolver
	EnumSvc  EnumResolver
	UserSvc  UserResolver
}

// NewNexusEngine cria uma nova instância do motor.
func NewNexusEngine(systemPool *pgxpool.Pool, rdb *redis.Client, projectPoolResolver func(ctx context.Context, tenantID string) (*pgxpool.Pool, error), vaultSvc SecretResolver, enumSvc EnumResolver, userSvc UserResolver) *NexusEngine {
	engine := &NexusEngine{
		registry:            NewComponentRegistry(),
		compiler:            NewNexusCompiler(),
		logger:              NewStructuredLogger("NexusEngine"),
		ProjectPoolResolver: projectPoolResolver,
		VaultSvc:            vaultSvc,
		EnumSvc:             enumSvc,
		UserSvc:             userSvc,
	}

	// Registra componentes padrão e pesados da biblioteca
	engine.registerStandardComponents(systemPool, rdb)

	return engine
}

// Compile compila JSON do frontend em ExecutionPlan.
func (e *NexusEngine) Compile(rawJSON []byte) (*ExecutionPlan, error) {
	return e.compiler.Compile(rawJSON)
}

// Registry retorna o registro de componentes.
func (e *NexusEngine) Registry() *ComponentRegistry {
	return e.registry
}

// ============================================================================
// EXECUÇÃO DE GRAFOS
// ============================================================================

// ExecutionResult contém o resultado completo de uma execução de grafo.
type ExecutionResult struct {
	TraceID       string                 `json:"trace_id"`
	GraphID       string                 `json:"graph_id"`
	Status        string                 `json:"status"` // "success", "error", "timeout"
	StartedAt     time.Time              `json:"started_at"`
	CompletedAt   time.Time              `json:"completed_at"`
	DurationMs    int64                  `json:"duration_ms"`
	ResponseData  map[string]interface{} `json:"response_data,omitempty"`
	ResponseCode  int                    `json:"response_code"`
	NodesExecuted int                    `json:"nodes_executed"`
	Error         string                 `json:"error,omitempty"`
	NodeResults   map[string]*NodeOutput `json:"node_results,omitempty"`
	mu            sync.RWMutex           // Mutex para proteção de acesso concorrente aos resultados
}

// ExecGraph executa um grafo compilado.
// mode pode ser FAST_LANE (síncrono) ou WORKER_LANE (assíncrono).
func (e *NexusEngine) ExecGraph(ctx context.Context, plan *ExecutionPlan, state *NexusState) (*ExecutionResult, error) {
	startedAt := time.Now()
	traceID := state.Security().TraceID
	if traceID == "" {
		traceID = uuid.New().String()
	}

	result := &ExecutionResult{
		TraceID:     traceID,
		GraphID:     plan.GraphID,
		StartedAt:   startedAt,
		NodeResults: make(map[string]*NodeOutput),
	}

	// Aplica timeout global do plano
	execCtx, cancel := context.WithTimeout(ctx, plan.Timeout)
	defer cancel()

	e.logger.Info("graph.started", map[string]interface{}{
		"trace_id":  traceID,
		"graph_id":  plan.GraphID,
		"mode":      plan.Mode,
		"timeout":   plan.Timeout.String(),
		"nodes":     plan.Metadata.NodeCount,
		"tenant_id": state.Security().TenantID,
	})

	// 1. Instancia todos os componentes do plano
	components, err := e.instantiateComponents(plan)
	if err != nil {
		result.Status = "error"
		result.Error = fmt.Sprintf("component instantiation failed: %v", err)
		result.CompletedAt = time.Now()
		result.DurationMs = time.Since(startedAt).Milliseconds()
		return result, err
	}

	// 2. Constrói mapa de adjacência para roteamento de pacotes
	edgeMap := buildEdgeMap(plan)

	// 3. Executa conforme a estratégia definida
	if plan.Strategy == StrategyAtomicParallel {
		err = e.execParallel(execCtx, plan, state, components, edgeMap, result)
	} else {
		err = e.execTopological(execCtx, plan, state, components, edgeMap, result)
	}

	if err != nil {
		result.Status = "error"
		result.Error = err.Error()
		result.CompletedAt = time.Now()
		result.DurationMs = time.Since(startedAt).Milliseconds()
		return result, err
	}

	// 4. Garante que o resultado final reflete o nó de resposta capturado
	responseData, responseCode := e.collectResponse(plan, state)
	result.ResponseData = responseData
	result.ResponseCode = responseCode

	result.Status = "success"
	result.CompletedAt = time.Now()
	result.DurationMs = time.Since(startedAt).Milliseconds()

	e.logger.Info("graph.completed", map[string]interface{}{
		"trace_id":       traceID,
		"graph_id":       plan.GraphID,
		"duration_ms":    result.DurationMs,
		"nodes_executed": result.NodesExecuted,
		"status":         result.Status,
		"strategy":       plan.Strategy,
	})

	return result, nil
}

// execTopological executa os nós em ordem sequencial estrita.
func (e *NexusEngine) execTopological(ctx context.Context, plan *ExecutionPlan, state *NexusState, components map[string]Component, edgeMap map[string][]CompiledEdge, result *ExecutionResult) error {
	var nodesExecuted int32

	for _, nodeID := range plan.TopologicalOrder {
		select {
		case <-ctx.Done():
			return fmt.Errorf("nexus: execution timeout at node %s", nodeID)
		default:
		}

		comp, exists := components[nodeID]
		if !exists { continue }

		compiledNode := findCompiledNode(plan, nodeID)
		if compiledNode == nil { continue }

		// Se não for trigger e não tiver input, pula (exceto se for um fluxo que não depende de dados, o que é raro no Nexus)
		if compiledNode.Type != "trigger" && !e.hasInputForNode(nodeID, state) {
			nodeResult := &NodeOutput{Status: "skipped", DurationMs: 0}
			state.SetNodeOutput(nodeID, nodeResult)
			result.NodeResults[nodeID] = nodeResult
			continue
		}

		// Executa
		nodeStart := time.Now()
		inputIP := e.prepareInputIP(nodeID, plan, state)
		nodeOutput, err := e.executeNode(ctx, comp, compiledNode, inputIP, state)
		nodeDuration := time.Since(nodeStart).Milliseconds()

		atomic.AddInt32(&nodesExecuted, 1)

		nodeResult := &NodeOutput{
			Status:     "success",
			DurationMs: nodeDuration,
			InputData:  inputIP.Data,
		}

		if err != nil {
			// Tratamento de erro (bypass, fallback, fail)
			if stop := e.handleNodeError(nodeID, compiledNode, err, nodeResult, state, result); stop {
				result.NodesExecuted = int(nodesExecuted)
				return err
			}
			continue
		}

		// Processa saídas e roteia
		if nodeOutput != nil {
			for portName, packets := range nodeOutput {
				for _, packet := range packets {
					packet.Propagate(nodeID, portName, state.NextSequence())
					if packet.Data != nil {
						nodeResult.Data = packet.Data
					}
					e.routePacket(nodeID, portName, packet, edgeMap, state)
				}
			}
		}

		state.SetNodeOutput(nodeID, nodeResult)
		result.NodeResults[nodeID] = nodeResult
	}

	result.NodesExecuted = int(nodesExecuted)
	return nil
}

// execParallel executa todos os nós em paralelo, respeitando dependências de dados.
func (e *NexusEngine) execParallel(ctx context.Context, plan *ExecutionPlan, state *NexusState, components map[string]Component, edgeMap map[string][]CompiledEdge, result *ExecutionResult) error {
	var wg sync.WaitGroup
	var nodesExecuted int32
	var execError atomic.Value // Armazena o primeiro erro fatal

	// No modo paralelo "atômico", disparamos todos os nós que são independentes.
	// Para simplificar e garantir segurança, usamos o fato de que é um DAG.
	// Vamos disparar goroutines que esperam por seus inputs.
	
	nodeDone := make(map[string]chan struct{})
	for _, nodeID := range plan.TopologicalOrder {
		nodeDone[nodeID] = make(chan struct{})
	}

	for _, nodeID := range plan.TopologicalOrder {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			defer close(nodeDone[id])

			comp := components[id]
			compiledNode := findCompiledNode(plan, id)

			// Espera pelos predecessores (se houver arestas para este nó)
			if compiledNode.Type != "trigger" {
				for _, edge := range plan.Edges {
					if edge.ToNode == id {
						select {
						case <-nodeDone[edge.FromNode]:
						case <-ctx.Done():
							return
						}
					}
				}
			}

			// Verifica se houve erro fatal em outro nó
			if err := execError.Load(); err != nil {
				return
			}

			// Se não tiver input e não for trigger, pula
			if compiledNode.Type != "trigger" && !e.hasInputForNode(id, state) {
				nodeResult := &NodeOutput{Status: "skipped", DurationMs: 0}
				state.SetNodeOutput(id, nodeResult)
				result.mu.Lock()
				result.NodeResults[id] = nodeResult
				result.mu.Unlock()
				return
			}

			// Executa
			nodeStart := time.Now()
			inputIP := e.prepareInputIP(id, plan, state)
			nodeOutput, err := e.executeNode(ctx, comp, compiledNode, inputIP, state)
			nodeDuration := time.Since(nodeStart).Milliseconds()

			atomic.AddInt32(&nodesExecuted, 1)

			nodeResult := &NodeOutput{
				Status:     "success",
				DurationMs: nodeDuration,
				InputData:  inputIP.Data,
			}

			if err != nil {
				if stop := e.handleNodeError(id, compiledNode, err, nodeResult, state, result); stop {
					execError.Store(err)
				}
				return
			}

			// Processa saídas
			if nodeOutput != nil {
				for portName, packets := range nodeOutput {
					for _, packet := range packets {
						packet.Propagate(id, portName, state.NextSequence())
						if packet.Data != nil {
							nodeResult.Data = packet.Data
						}
						e.routePacket(id, portName, packet, edgeMap, state)
					}
				}
			}

			state.SetNodeOutput(id, nodeResult)
			result.mu.Lock()
			result.NodeResults[id] = nodeResult
			result.mu.Unlock()

		}(nodeID)
	}

	wg.Wait()

	if err := execError.Load(); err != nil {
		return err.(error)
	}

	result.NodesExecuted = int(nodesExecuted)
	return nil
}

// handleNodeError centraliza a lógica de decisão sobre erros de nós.
func (e *NexusEngine) handleNodeError(nodeID string, node *CompiledNode, err error, nodeResult *NodeOutput, state *NexusState, result *ExecutionResult) bool {
	nodeResult.Status = "error"
	nodeResult.Error = err.Error()

	e.logger.Error("node.error", map[string]interface{}{
		"node_id":   nodeID,
		"node_type": node.Type,
		"error":     err.Error(),
	})

	result.mu.Lock()
	result.NodeResults[nodeID] = nodeResult
	result.mu.Unlock()

	switch node.ErrorStrategy {
	case ErrorBypass:
		nodeResult.Status = "skipped"
		state.SetNodeOutput(nodeID, nodeResult)
		return false
	case ErrorFallback:
		nodeResult.Status = "fallback"
		state.SetNodeOutput(nodeID, nodeResult)
		return false
	default: // ErrorFail
		return true
	}
}

// Execute é um helper de "um clique" que compila e executa um grafo a partir do JSON bruto.
func (e *NexusEngine) Execute(ctx context.Context, tenantID string, userRole string, graphJSON []byte, payload map[string]interface{}, headers map[string]string, mode ExecutionMode) (*ExecutionResult, error) {
	// 1. Compila
	plan, err := e.Compile(graphJSON)
	if err != nil {
		return nil, fmt.Errorf("compile failed: %w", err)
	}

	// 2. Prepara Estado
	traceID := fmt.Sprintf("nxs-exec-%d", time.Now().UnixNano())
	state := NewNexusState(
		&TriggerContext{
			Type:    "EXECUTION",
			Payload: payload,
			Headers: headers,
		},
		&SecurityContext{
			TenantID:   tenantID,
			UserUUID:   "", // Em execuções manuais sem token, fica vazio
			UserRole:   userRole,
			TraceID:    traceID,
			Timestamp:  time.Now().UTC().Format(time.RFC3339),
		},
		&SystemContext{
			AutomationID:  plan.GraphID,
			ExecutionMode: string(mode),
		},
	)

	// Injeta Resolvers para sinergia total de variáveis
	state.SetSecretResolver(e.VaultSvc)
	state.SetEnumResolver(e.EnumSvc)
	state.SetUserResolver(e.UserSvc)

	// 3. Executa
	return e.ExecGraph(ctx, plan, state)
}

// ============================================================================
// MÉTODOS INTERNOS
// ============================================================================

// instantiateComponents cria instâncias de todos os componentes do plano.
func (e *NexusEngine) instantiateComponents(plan *ExecutionPlan) (map[string]Component, error) {
	components := make(map[string]Component, len(plan.Nodes))

	for _, compiledNode := range plan.Nodes {
		comp, err := e.registry.Create(ComponentType(compiledNode.Type), compiledNode.ID)
		if err != nil {
			return nil, fmt.Errorf("failed to create component %q (type: %s): %w",
				compiledNode.ID, compiledNode.Type, err)
		}

		if err := comp.Init(compiledNode.Config); err != nil {
			return nil, fmt.Errorf("failed to initialize component %q: %w", compiledNode.ID, err)
		}

		if err := comp.ValidateConfig(); err != nil {
			return nil, fmt.Errorf("invalid config for component %q: %w", compiledNode.ID, err)
		}

		components[compiledNode.ID] = comp
	}

	return components, nil
}
// executeNode executa um único componente com controle de timeout e segurança.
func (e *NexusEngine) executeNode(ctx context.Context, comp Component, compiledNode *CompiledNode, ip *InformationPacket, state *NexusState) (map[string][]*InformationPacket, error) {
	// === SECURITY GATEWAY (L1, L2, L3) ===
	if err := e.enforceSecurityGateway(state, compiledNode.Config.SecurityLevel); err != nil {
		return nil, err
	}

	// Aplica timeout individual do nó
	nodeCtx := ctx
	if compiledNode.TimeoutPerNode > 0 {
		var cancel context.CancelFunc
		nodeCtx, cancel = context.WithTimeout(ctx, compiledNode.TimeoutPerNode)
		defer cancel()
	}

	// Retry com backoff exponencial
	if compiledNode.RetryPolicy != nil && compiledNode.RetryPolicy.MaxRetries > 0 {
		return e.executeWithRetry(nodeCtx, comp, ip, state, compiledNode.RetryPolicy)
	}

	return comp.Process(nodeCtx, ip, state)
}

// enforceSecurityGateway valida se o usuário atual tem permissão para executar o nó baseado no nível (L1, L2, L3).
func (e *NexusEngine) enforceSecurityGateway(state *NexusState, level SecurityLevel) error {
	role := state.Security().UserRole
	
	// Default para L1 se não especificado
	if level == "" {
		level = LevelL1Standard
	}

	switch level {
	case LevelL1Standard:
		return nil // L1: Livre (inclui Anon)

	case LevelL2Strict:
		// L2: Exige um usuário autenticado (não pode ser Anon)
		if role == "anon" || role == "" {
			return fmt.Errorf("security gateway: node requires Level L2 (Strict Authentication), but current identity is Anonymous")
		}
		return nil

	case LevelL3Admin:
		// L3: Exige privilégios administrativos
		if role != "admin" && role != "service" {
			return fmt.Errorf("security gateway: node requires Level L3 (Admin Privileges), but current role is %q", role)
		}
		return nil

	default:
		return nil // Nível desconhecido, falha segura permitindo execução
	}
}

// executeWithRetry executa com política de retry.
func (e *NexusEngine) executeWithRetry(ctx context.Context, comp Component, ip *InformationPacket, state *NexusState, policy *RetryPolicy) (map[string][]*InformationPacket, error) {
	var lastErr error

	for attempt := 0; attempt <= policy.MaxRetries; attempt++ {
		if attempt > 0 {
			// Backoff exponencial
			backoff := policy.BackoffBase * time.Duration(1<<uint(attempt-1))
			if backoff > policy.BackoffMax {
				backoff = policy.BackoffMax
			}

			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
			}

			e.logger.Warn("node.retry", map[string]interface{}{
				"node_id":   comp.ID(),
				"attempt":   attempt,
				"max":       policy.MaxRetries,
				"backoff":   backoff.String(),
			})
		}

		result, err := comp.Process(ctx, ip, state)
		if err == nil {
			return result, nil
		}

		lastErr = err

		// Verifica se o erro é retriável
		if len(policy.RetryableErrors) > 0 {
			retriable := false
			for _, pattern := range policy.RetryableErrors {
				if containsString(err.Error(), pattern) {
					retriable = true
					break
				}
			}
			if !retriable {
				return nil, err
			}
		}
	}

	return nil, fmt.Errorf("nexus: max retries (%d) exhausted: %w", policy.MaxRetries, lastErr)
}

// prepareInputIP prepara o Information Packet de entrada para um nó.
func (e *NexusEngine) prepareInputIP(nodeID string, plan *ExecutionPlan, state *NexusState) *InformationPacket {
	// Para o trigger, cria IP a partir do contexto
	compiledNode := findCompiledNode(plan, nodeID)
	if compiledNode != nil && compiledNode.Type == "trigger" {
		return NewFromTrigger(
			state.Security().TenantID,
			state.Security().UserUUID,
			state.Security().AuthSource,
			state.Security().TraceID,
			state.Trigger().Payload,
		)
	}

	// Para outros nós, coleta dados dos predecessores
	ip := NewInformationPacket(
		state.Security().TenantID,
		state.Security().UserUUID,
		state.Security().AuthSource,
		state.Security().TraceID,
	)
	ip.SourceNode = nodeID
	ip.SourcePort = "in"

	// Busca dados do nó predecessor (último nó que roteiou para este)
	stateKey := fmt.Sprintf("__routed_to_%s", nodeID)
	if routedData, ok := state.GetVar(stateKey); ok {
		if data, ok := routedData.(map[string]interface{}); ok {
			ip.Data = data
		} else if packets, ok := routedData.([]map[string]interface{}); ok {
			if len(packets) == 1 {
				ip.Data = packets[0]
			} else if len(packets) > 1 {
				ip.Data = map[string]interface{}{"inputs": packets}
			}
		}
	}

	return ip
}

func (e *NexusEngine) hasInputForNode(nodeID string, state *NexusState) bool {
	_, ok := state.GetVar(fmt.Sprintf("__routed_to_%s", nodeID))
	return ok
}

// routePacket roteia um pacote para os nós downstream via o mapa de arestas.
func (e *NexusEngine) routePacket(fromNode, fromPort string, ip *InformationPacket, edgeMap map[string][]CompiledEdge, state *NexusState) {
	key := fmt.Sprintf("%s:%s", fromNode, fromPort)
	edges, ok := edgeMap[key]
	if !ok {
		return
	}

	for _, edge := range edges {
		// Armazena os dados roteados no state para o nó destino
		stateKey := fmt.Sprintf("__routed_to_%s", edge.ToNode)
		existing, exists := state.GetVar(stateKey)
		if !exists {
			state.SetVar(stateKey, []map[string]interface{}{ip.Data})
			continue
		}
		if packets, ok := existing.([]map[string]interface{}); ok {
			state.SetVar(stateKey, append(packets, ip.Data))
			continue
		}
		if data, ok := existing.(map[string]interface{}); ok {
			state.SetVar(stateKey, []map[string]interface{}{data, ip.Data})
			continue
		}
		state.SetVar(stateKey, []map[string]interface{}{ip.Data})
	}
}

// collectResponse coleta o resultado do(s) nó(s) Response.
func (e *NexusEngine) collectResponse(plan *ExecutionPlan, state *NexusState) (map[string]interface{}, int) {
	// Procuramos o nó de resposta que foi efetivamente executado com sucesso.
	// Em fluxos ramificados, múltiplos nós de resposta podem existir, mas apenas um deve ser atingido por execução.
	for _, node := range plan.Nodes {
		if node.Type == "response" {
			output, ok := state.GetNodeOutput(node.ID)
			// Verifica se o nó foi executado com sucesso e contém dados
			if ok && output.Status == "success" && output.Data != nil {
				statusCode := 200
				if code, ok := output.Data["status_code"]; ok {
					switch v := code.(type) {
					case float64:
						statusCode = int(v)
					case int:
						statusCode = v
					}
				}

				// Verifica o tipo de resposta
				responseType := "json"
				if rt, ok := output.Data["response_type"].(string); ok {
					responseType = rt
				}
				log.Printf("[collectResponse] responseType: %s, output.Data keys: %v", responseType, getMapKeys(output.Data))

				// Para tipos não-JSON (text, html, xml), retorna apenas o valor do body
				if responseType == "text" || responseType == "html" || responseType == "xml" {
					var bodyValue interface{}
					if body, ok := output.Data["body"]; ok {
						bodyValue = body
						log.Printf("[collectResponse] Found body field: %v", bodyValue)
					} else {
						// Se não tem body específico, usa o primeiro valor disponível (excluindo metadados)
						log.Printf("[collectResponse] No body field, using first available value")
						for k, v := range output.Data {
							if k != "status_code" && k != "response_type" && k != "headers" {
								bodyValue = v
								log.Printf("[collectResponse] Using value from key %s: %v", k, v)
								break
							}
						}
					}

					responseData := map[string]interface{}{
						"body":          bodyValue,
						"response_type": responseType,
						"status_code":   float64(statusCode),
					}
					log.Printf("[collectResponse] Returning responseData for non-JSON: %+v", responseData)
					return responseData, statusCode
				}

				// Para JSON, clona os dados removendo metadados internos da engine
				responseData := make(map[string]interface{})
				for k, v := range output.Data {
					if k != "status_code" && k != "headers" {
						responseData[k] = v
					}
				}

				return responseData, statusCode
			}
		}
	}

	// Fallback caso nenhum nó de resposta tenha sido atingido
	return map[string]interface{}{
		"message": "no response node reached",
		"status":  "flow_completed_without_response",
	}, 200
}

// registerStandardComponents registra a biblioteca padrão de componentes.
func (e *NexusEngine) registerStandardComponents(systemPool *pgxpool.Pool, rdb *redis.Client) {
	e.registry.RegisterFactory(TypeTrigger, func(id string) Component {
		return NewTriggerComponent(id)
	})
	e.registry.RegisterFactory(TypeResponse, func(id string) Component {
		return NewResponseComponent(id)
	})
	e.registry.RegisterFactory(TypeCondition, func(id string) Component {
		return NewConditionComponent(id)
	})
	e.registry.RegisterFactory(TypeTransform, func(id string) Component {
		return NewTransformComponent(id)
	})
	e.registry.RegisterFactory(TypeSwitch, func(id string) Component {
		return NewSwitchComponent(id)
	})
	e.registry.RegisterFactory(TypeMerge, func(id string) Component {
		return NewMergeComponent(id)
	})
	e.registry.RegisterFactory(TypeSplit, func(id string) Component {
		return NewSplitComponent(id)
	})
	e.registry.RegisterFactory(TypeErrorHandler, func(id string) Component {
		return NewErrorHandlerComponent(id)
	})
	
	// Fase 3: Novos Nós de Processamento Pesado
	e.registry.RegisterFactory(TypeForeach, func(id string) Component {
		return NewForeachComponent(id)
	})
	e.registry.RegisterFactory(TypeAggregator, func(id string) Component {
		return NewAggregatorComponent(id)
	})
	e.registry.RegisterFactory(TypeSubdag, func(id string) Component {
		return NewSubdagComponent(id, e)
	})
	e.registry.RegisterFactory(TypeData, func(id string) Component {
		return NewDataComponent(id, systemPool, e.ProjectPoolResolver)
	})
	e.registry.RegisterFactory(TypeHTTP, func(id string) Component {
		return NewHTTPComponent(id, []string{}) // allowlist global configurável depois
	})
	e.registry.RegisterFactory(TypeQdrant, func(id string) Component {
		// URLs e Keys estáticas por enquanto; num cenário dinâmico seria via ENV
		return NewQdrantComponent(id, "http://localhost:6333", "", rdb)
	})
}

// ============================================================================
// COMPONENT REGISTRY
// ============================================================================

// ComponentFactory é uma função que cria uma instância de componente.
type ComponentFactory func(id string) Component

// ComponentRegistry gerencia o registro e criação de componentes.
type ComponentRegistry struct {
	mu        sync.RWMutex
	factories map[ComponentType]ComponentFactory
}

// NewComponentRegistry cria um novo registro de componentes.
func NewComponentRegistry() *ComponentRegistry {
	return &ComponentRegistry{
		factories: make(map[ComponentType]ComponentFactory),
	}
}

// RegisterFactory registra uma factory para um tipo de componente.
func (r *ComponentRegistry) RegisterFactory(typ ComponentType, factory ComponentFactory) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.factories[typ] = factory
}

// Create cria uma instância de componente pelo tipo.
func (r *ComponentRegistry) Create(typ ComponentType, id string) (Component, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	factory, exists := r.factories[typ]
	if !exists {
		return nil, fmt.Errorf("nexus: unknown component type %q", typ)
	}

	return factory(id), nil
}

// HasType verifica se um tipo de componente está registrado.
func (r *ComponentRegistry) HasType(typ ComponentType) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, exists := r.factories[typ]
	return exists
}

// RegisteredTypes retorna todos os tipos registrados.
func (r *ComponentRegistry) RegisteredTypes() []ComponentType {
	r.mu.RLock()
	defer r.mu.RUnlock()
	types := make([]ComponentType, 0, len(r.factories))
	for t := range r.factories {
		types = append(types, t)
	}
	return types
}

// ============================================================================
// HELPERS
// ============================================================================

// buildEdgeMap constrói mapa de roteamento: "nodeID:portName" → []CompiledEdge
func buildEdgeMap(plan *ExecutionPlan) map[string][]CompiledEdge {
	edgeMap := make(map[string][]CompiledEdge)
	for _, edge := range plan.Edges {
		key := fmt.Sprintf("%s:%s", edge.FromNode, edge.FromPort)
		edgeMap[key] = append(edgeMap[key], edge)
	}
	return edgeMap
}

// findCompiledNode encontra um nó compilado pelo ID.
func findCompiledNode(plan *ExecutionPlan, nodeID string) *CompiledNode {
	for i := range plan.Nodes {
		if plan.Nodes[i].ID == nodeID {
			return &plan.Nodes[i]
		}
	}
	return nil
}

// containsString verifica se a string contém o substring.
func containsString(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsCheck(s, substr))
}

func containsCheck(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// ============================================================================
// STRUCTURED LOGGER
// ============================================================================

// StructuredLogger fornece logging estruturado para o Nexus.
type StructuredLogger struct {
	prefix string
}

// NewStructuredLogger cria um novo logger estruturado.
func NewStructuredLogger(prefix string) *StructuredLogger {
	return &StructuredLogger{prefix: prefix}
}

func (l *StructuredLogger) Info(event string, fields map[string]interface{}) {
	fieldsJSON, _ := json.Marshal(fields)
	log.Printf("[%s] [INFO] %s %s", l.prefix, event, string(fieldsJSON))
}

func (l *StructuredLogger) Warn(event string, fields map[string]interface{}) {
	fieldsJSON, _ := json.Marshal(fields)
	log.Printf("[%s] [WARN] %s %s", l.prefix, event, string(fieldsJSON))
}

func (l *StructuredLogger) Error(event string, fields map[string]interface{}) {
	fieldsJSON, _ := json.Marshal(fields)
	log.Printf("[%s] [ERROR] %s %s", l.prefix, event, string(fieldsJSON))
}

// getMapKeys retorna as chaves de um map[string]interface{}
func getMapKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func (l *StructuredLogger) Debug(event string, fields map[string]interface{}) {
	fieldsJSON, _ := json.Marshal(fields)
	log.Printf("[%s] [DEBUG] %s %s", l.prefix, event, string(fieldsJSON))
}
