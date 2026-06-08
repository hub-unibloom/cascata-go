package services

import (
	"context"
	"fmt"
	"log"
	"time"
)

// ============================================================================
// CASCATA COMPILED AUTOMATION ENGINE
// High-performance workflow execution with zero-allocation hot path
// ============================================================================

// NodeExecutor é a interface que todos os nós compilados implementam
type NodeExecutor interface {
	Execute(ctx *FlowContext) error
	GetOutputSlot() int
	GetErrorSlot() int
}

// CompiledNode representa um nó pré-compilado em memória
type CompiledNode struct {
	ID         string
	Type       AutomationNodeType
	Executor   NodeExecutor
	Next       map[bool][]*CompiledNode // true/false/error paths
	InputSlots map[string]int           // mapeia "url", "body" → VarPool index
	Timeout    time.Duration
	RetryCount int
}

// CompiledFlow é a AST otimizada de um workflow
type CompiledFlow struct {
	ID          string
	StartNode   *CompiledNode
	VarPoolSize int
	MaxSteps    int
	Timeout     time.Duration
}

// FlowContext é o contexto de execução zero-allocation
type FlowContext struct {
	context.Context
	Vars      []interface{}  // VarPool pré-alocado
	NodeOutputs map[string]int // mapeia node_id → slot index
	ProjectSlug string
	ProjectPool interface{} // *pgxpool.Pool
	JWTSecret   string
	UserRole    string
	JWTClaims   map[string]interface{}
	CryptoSvc   *CryptoService
	StartTime   time.Time
	StepCount   int
}

// NodeRegistry mantém os construtores de cada tipo de nó
type NodeRegistry struct {
	builders map[AutomationNodeType]NodeBuilder
}

// NodeBuilder cria um NodeExecutor a partir da configuração JSON
type NodeBuilder func(config map[string]interface{}, compiler *Compiler) (NodeExecutor, error)

// Compiler transforma JSON em CompiledFlow
type Compiler struct {
	registry *NodeRegistry
	slotGen  int
}

// NewNodeRegistry cria um registro vazio
func NewNodeRegistry() *NodeRegistry {
	return &NodeRegistry{
		builders: make(map[AutomationNodeType]NodeBuilder),
	}
}

// Register adiciona um builder para um tipo de nó
func (r *NodeRegistry) Register(nodeType AutomationNodeType, builder NodeBuilder) {
	r.builders[nodeType] = builder
}

// NewCompiler cria um novo compilador
func NewCompiler(registry *NodeRegistry) *Compiler {
	return &Compiler{
		registry: registry,
		slotGen:  0,
	}
}

// allocSlot aloca um novo slot no VarPool
func (c *Compiler) allocSlot() int {
	slot := c.slotGen
	c.slotGen++
	return slot
}

// Compile transforma nós JSON em CompiledFlow
func (c *Compiler) Compile(automationID string, nodes []AutomationNode) (*CompiledFlow, error) {
	if len(nodes) == 0 {
		return nil, fmt.Errorf("no nodes to compile")
	}

	// Primeira passagem: criar todos os nós compilados
	compiledNodes := make(map[string]*CompiledNode)
	
	for _, node := range nodes {
		var executor NodeExecutor
		var err error
		switch node.Type {
		case "http", "http_request":
			executor, err = BuildHTTPNodeV2(node.Config, c, nil)
			if err != nil {
				return nil, fmt.Errorf("failed to build HTTP node %s: %w", node.ID, err)
			}
		default:
			builder, exists := c.registry.builders[node.Type]
			if !exists {
				return nil, fmt.Errorf("unknown node type: %s", node.Type)
			}
			executor, err = builder(node.Config, c)
			if err != nil {
				return nil, fmt.Errorf("failed to compile node %s: %w", node.ID, err)
			}
		}
		
		compiledNodes[node.ID] = &CompiledNode{
			ID:         node.ID,
			Type:       node.Type,
			Executor:   executor,
			InputSlots: make(map[string]int),
		}
	}

	// Segunda passagem: resolver conexões (next)
	for _, node := range nodes {
		cNode := compiledNodes[node.ID]
		cNode.Next = c.resolveNext(node.Next, compiledNodes)
		
		// Configurar timeout e retry
		if node.Config != nil {
			if t, ok := node.Config["timeout"].(float64); ok {
				cNode.Timeout = time.Duration(t) * time.Millisecond
			}
			if r, ok := node.Config["retries"].(float64); ok {
				cNode.RetryCount = int(r)
			}
		}
	}

	// Encontrar nó inicial (trigger)
	var startNode *CompiledNode
	for _, node := range nodes {
		if node.Type == NodeTrigger {
			startNode = compiledNodes[node.ID]
			break
		}
	}

	if startNode == nil && len(nodes) > 0 {
		// Se não tiver trigger explícito, usa o primeiro
		startNode = compiledNodes[nodes[0].ID]
	}

	return &CompiledFlow{
		ID:          automationID,
		StartNode:   startNode,
		VarPoolSize: c.slotGen,
		MaxSteps:    100,
		Timeout:     30 * time.Second,
	}, nil
}

// buildNode cria um NodeExecutor para um nó específico
func (c *Compiler) buildNode(node AutomationNode) (NodeExecutor, error) {
	builder, exists := c.registry.builders[node.Type]
	if !exists {
		return nil, fmt.Errorf("unknown node type: %s", node.Type)
	}
	return builder(node.Config, c)
}

// resolveNext converte next JSON em mapa de ponteiros
func (c *Compiler) resolveNext(next interface{}, nodes map[string]*CompiledNode) map[bool][]*CompiledNode {
	result := make(map[bool][]*CompiledNode)
	
	switch n := next.(type) {
	case []interface{}:
		// Array de IDs: ["node2", "node3"] → true path
		arr := make([]*CompiledNode, 0, len(n))
		for _, id := range n {
			if strID, ok := id.(string); ok {
				if node, exists := nodes[strID]; exists {
					arr = append(arr, node)
				}
			}
		}
		result[true] = arr
		
	case map[string]interface{}:
		// Objeto com true/false/error: {true: "node2", false: "node3"}
		for key, val := range n {
			if strID, ok := val.(string); ok {
				if node, exists := nodes[strID]; exists {
					var pathKey bool
					switch key {
					case "true":
						pathKey = true
					case "false", "error":
						pathKey = false
					}
					result[pathKey] = append(result[pathKey], node)
				}
			}
		}
	}
	
	return result
}

// ExecuteCompiledFlow executa um flow compilado
func ExecuteCompiledFlow(ctx context.Context, flow *CompiledFlow, initialPayload interface{}, projectCtx *AutomationContext) (interface{}, error) {
	return ExecuteCompiledFlowWithLogging(ctx, flow, initialPayload, projectCtx, "", "")
}

// ExecuteCompiledFlowWithLogging executa um flow compilado com logging estruturado
func ExecuteCompiledFlowWithLogging(ctx context.Context, flow *CompiledFlow, initialPayload interface{}, projectCtx *AutomationContext, automationID, projectSlug string) (interface{}, error) {
	log.Printf("[ExecuteCompiledFlow] Creating flow context - ProjectPool=%v, ProjectSlug=%s", projectCtx.ProjectPool, projectCtx.ProjectSlug)

	flowCtx := &FlowContext{
		Context:     ctx,
		Vars:        make([]interface{}, flow.VarPoolSize),
		NodeOutputs: make(map[string]int),
		ProjectSlug: projectCtx.ProjectSlug,
		ProjectPool: projectCtx.ProjectPool,
		JWTSecret:   projectCtx.JWTSecret,
		UserRole:    projectCtx.UserRole,
		JWTClaims:   projectCtx.JWTClaims,
		CryptoSvc:   projectCtx.CryptoSvc,
		StartTime:   time.Now(),
		StepCount:   0,
	}
	
	// Iniciar contexto de logging se tivermos automationID
	var execLogCtx *AutomationExecutionContext
	if automationID != "" && projectSlug != "" {
		execLogCtx = StartExecution(automationID, projectSlug)
		execLogCtx.LogStep(AutomationStepLog{
			StepID:     "trigger",
			NodeType:   "trigger",
			Level:      LogLevelInfo,
			Message:    "Fluxo iniciado via API_INTERCEPT",
			InputData:  initialPayload,
			OutputData: initialPayload,
		})
		defer func() {
			status := "success"
			if ctx.Err() != nil {
				status = "timeout"
			}
			var finalOutput interface{}
			if len(flowCtx.Vars) > 1 {
				finalOutput = flowCtx.Vars[1]
			}
			execLogCtx.FinishExecution(status, finalOutput)
		}()
	}

	// Inicializar payload
	if len(flowCtx.Vars) > 0 {
		flowCtx.Vars[0] = initialPayload // slot 0 = input
	}

	// Criar contexto com timeout do fluxo
	execCtx, cancel := context.WithTimeout(ctx, flow.Timeout)
	defer cancel()
	flowCtx.Context = execCtx

	// Executar
	currentNode := flow.StartNode
	
	log.Printf("[ExecuteCompiledFlow] Starting execution, startNode type=%s, id=%s", currentNode.Type, currentNode.ID)
	
	for currentNode != nil && flowCtx.StepCount < flow.MaxSteps {
		flowCtx.StepCount++
		log.Printf("[ExecuteCompiledFlow] Step %d: Executing node type=%s, id=%s", flowCtx.StepCount, currentNode.Type, currentNode.ID)
		
		// LOG estruturado: início do nó
		var stepID string
		if execLogCtx != nil {
			stepID = execLogCtx.LogNodeStart(currentNode.ID, string(currentNode.Type), currentNode.ID, flowCtx.Vars)
		}
		nodeStartTime := time.Now()
		
		// Executar com retry
		var err error
		retryCount := currentNode.RetryCount
		if retryCount < 0 {
			retryCount = 0
		}
		
		for attempt := 0; attempt <= retryCount; attempt++ {
			if attempt > 0 {
				log.Printf("[ExecuteCompiledFlow] Retry attempt %d for node %s", attempt, currentNode.ID)
				time.Sleep(time.Duration(attempt) * time.Second) // backoff
			}

			err = currentNode.Executor.Execute(flowCtx)
			if err == nil {
				log.Printf("[ExecuteCompiledFlow] Node %s executed successfully", currentNode.ID)
				
				// REGISTRAR OUTPUT do nó para acesso por outros nós via {{node_id.data.field}}
				var outputData interface{}
				if outputSlot := currentNode.Executor.GetOutputSlot(); outputSlot >= 0 {
					flowCtx.NodeOutputs[currentNode.ID] = outputSlot
					log.Printf("[ExecuteCompiledFlow] Registered node output: %s -> slot %d", currentNode.ID, outputSlot)
					if outputSlot < len(flowCtx.Vars) {
						outputData = flowCtx.Vars[outputSlot]
					}
				}
				
				// LOG estruturado: sucesso do nó
				if execLogCtx != nil && stepID != "" {
					durationMs := time.Since(nodeStartTime).Milliseconds()
					nextNodeIDs := []string{}
					if len(currentNode.Next[true]) > 0 {
						nextNodeIDs = append(nextNodeIDs, currentNode.Next[true][0].ID)
					}
					execLogCtx.LogNodeComplete(stepID, currentNode.ID, string(currentNode.Type), outputData, durationMs, nextNodeIDs)
				}
				
				// Se for nó response, retornar imediatamente o output
				if currentNode.Type == NodeResponse {
					log.Printf("[ExecuteCompiledFlow] Response node executed, returning output")
					if execLogCtx != nil {
						execLogCtx.LogStep(AutomationStepLog{
							StepID:     "response",
							NodeType:   "response",
							Level:      LogLevelInfo,
							Message:    "Resposta enviada ao cliente",
							OutputData: outputData,
						})
					}
					if outputSlot := currentNode.Executor.GetOutputSlot(); outputSlot >= 0 {
						return flowCtx.Vars[outputSlot], nil
					}
					return initialPayload, nil
				}
				break
			}
			log.Printf("[ExecuteCompiledFlow] Node %s execution error: %v", currentNode.ID, err)
		}
		
		// LOG estruturado: erro no nó
		if err != nil && execLogCtx != nil && stepID != "" {
			execLogCtx.LogNodeError(stepID, currentNode.ID, string(currentNode.Type), err, flowCtx.Vars)
		}

		// Determinar próximo nó
		var nextNodes []*CompiledNode
		if err != nil {
			// Caminho de erro
			log.Printf("[ExecuteCompiledFlow] Taking error path for node %s", currentNode.ID)
			nextNodes = currentNode.Next[false]
			// Se tiver error slot, armazenar erro
			if es := currentNode.Executor.GetErrorSlot(); es >= 0 && es < len(flowCtx.Vars) {
				flowCtx.Vars[es] = err.Error()
			}
		} else {
			// Sucesso - determinar base no tipo de nó
			switch currentNode.Type {
			case NodeLogic, NodeCondition:
				// Nós de lógica retornam bool
				if outputSlot := currentNode.Executor.GetOutputSlot(); outputSlot >= 0 {
					if val, ok := flowCtx.Vars[outputSlot].(bool); ok {
						log.Printf("[ExecuteCompiledFlow] Logic node %s result: %v", currentNode.ID, val)
						nextNodes = currentNode.Next[val]
					}
				}
			case NodeHTTP:
				// HTTP pode ter caminho de erro __error
				if outputSlot := currentNode.Executor.GetOutputSlot(); outputSlot >= 0 {
					if res, ok := flowCtx.Vars[outputSlot].(map[string]interface{}); ok {
						if _, hasError := res["__error"]; hasError {
							log.Printf("[ExecuteCompiledFlow] HTTP node %s has error, taking error path", currentNode.ID)
							nextNodes = currentNode.Next[false]
							break
						}
					}
				}
				nextNodes = currentNode.Next[true]
			default:
				nextNodes = currentNode.Next[true]
			}
		}

		if len(nextNodes) == 0 {
			log.Printf("[ExecuteCompiledFlow] No next nodes, stopping execution")
			break
		}
		
		// Para simplicidade, seguimos o primeiro nó do caminho
		currentNode = nextNodes[0]
		log.Printf("[ExecuteCompiledFlow] Moving to next node: type=%s, id=%s", currentNode.Type, currentNode.ID)
	}

	// Retornar output ou input
	if len(flowCtx.Vars) > 1 && flowCtx.Vars[1] != nil {
		return flowCtx.Vars[1], nil // slot 1 = output
	}
	return initialPayload, nil
}
