package nexus

import (
	"encoding/json"
	"fmt"
	"time"

	"cascata-backend/internal/services/nexus/graph"
)

// ============================================================================
// NEXUS COMPILER — Compilador JSON → ExecutionPlan
// ============================================================================
// Transforma o JSON do frontend (Nexus Architect) em um ExecutionPlan
// otimizado e validado, pronto para execução pelo NexusEngine.
//
// Fases:
//   1. PARSE     → Desserializa JSON em estrutura de grafo bruto
//   2. VALIDATE  → Valida topologia (ciclos, órfãos, portas obrigatórias)
//   3. RESOLVE   → Resolve referências de variáveis
//   4. OPTIMIZE  → Identifica caminhos paralelos
//   5. PLAN      → Gera ExecutionPlan com ordem topológica
// ============================================================================

// ExecutionMode define o modo de execução do grafo.
type ExecutionMode string

const (
	ModeFastLane   ExecutionMode = "fast_lane"   // Síncrono — PRE_PERSIST
	ModeWorkerLane ExecutionMode = "worker_lane" // Assíncrono — POST_PERSIST
)

// OrchestrationStrategy define como os nós do grafo são orquestrados.
type OrchestrationStrategy string

const (
	StrategyTopological    OrchestrationStrategy = "topological"     // Um nó por vez (sequencial)
	StrategyAtomicParallel OrchestrationStrategy = "atomic_parallel" // Todos os nós simultâneos (espera todos para responder)
)

// ExecutionPlan é a representação compilada e otimizada de um grafo de automação.
type ExecutionPlan struct {
	GraphID          string         `json:"graph_id"`
	Version          int            `json:"version"`
	Mode             ExecutionMode  `json:"mode"`
	Nodes            []CompiledNode `json:"nodes"`
	Edges            []CompiledEdge `json:"edges"`
	TopologicalOrder []string              `json:"topological_order"`
	Timeout          time.Duration         `json:"timeout"`
	Strategy         OrchestrationStrategy `json:"strategy"`
	MaxParallelism   int                   `json:"max_parallelism"`
	Metadata         PlanMetadata          `json:"metadata"`
}

// CompiledNode é a representação compilada de um nó.
type CompiledNode struct {
	ID             string          `json:"id"`
	Type           string          `json:"type"`
	Config         ComponentConfig `json:"config"`
	Inports        []PortDefinition `json:"inports"`
	Outports       []PortDefinition `json:"outports"`
	RetryPolicy    *RetryPolicy     `json:"retry_policy,omitempty"`
	TimeoutPerNode time.Duration    `json:"timeout_per_node"`
	ErrorStrategy  ErrorStrategy    `json:"error_strategy"`
}

// CompiledEdge é a representação compilada de uma aresta.
type CompiledEdge struct {
	FromNode string `json:"from_node"`
	FromPort string `json:"from_port"`
	ToNode   string `json:"to_node"`
	ToPort   string `json:"to_port"`
	TypeHint string `json:"type_hint"`
}

// PlanMetadata contém metadados do plano de execução.
type PlanMetadata struct {
	CompiledAt    time.Time `json:"compiled_at"`
	NodeCount     int       `json:"node_count"`
	EdgeCount     int       `json:"edge_count"`
	HasAINodes    bool      `json:"has_ai_nodes"`
	HasDataNodes  bool      `json:"has_data_nodes"`
	HasQdrantNodes bool     `json:"has_qdrant_nodes"`
	EstimatedLatency string `json:"estimated_latency"` // "low", "medium", "high"
}

// ============================================================================
// RAW GRAPH JSON — Estrutura do JSON vindo do frontend
// ============================================================================

// RawGraphJSON é a estrutura do JSON que o frontend envia.
type RawGraphJSON struct {
	ID       string        `json:"id"`
	Version  int           `json:"version"`
	Mode     string        `json:"mode"`     // "fast_lane" ou "worker_lane"
	Strategy string        `json:"strategy"` // "topological" ou "atomic_parallel"
	Timeout  int           `json:"timeout_seconds"`
	Nodes    []RawNodeJSON `json:"nodes"`
	Edges    []RawEdgeJSON `json:"edges"`
}

// RawNodeJSON é um nó no JSON do frontend.
type RawNodeJSON struct {
	ID       string                 `json:"id"`
	NodeID   string                 `json:"node_id"` // ID canônico do componente (ex: pre_event_trigger)
	Type     string                 `json:"type"`    // "trigger", "action", etc.
	Label    string                 `json:"label"`
	Position struct {
		X float64 `json:"x"`
		Y float64 `json:"y"`
	} `json:"position"`
	X        float64                `json:"x"`    // Posição direta (formato do NexusArchitect)
	Y        float64                `json:"y"`    // Posição direta (formato do NexusArchitect)
	Config   map[string]interface{} `json:"config"`
	Next     []string               `json:"next"` // Conexões de saída (IDs dos nós destino)
	Retry    *RetryPolicy           `json:"retry,omitempty"`
	Timeout  int                    `json:"timeout_ms,omitempty"`
	OnError  string                 `json:"on_error,omitempty"` // "fail", "bypass", "fallback"
}

// GetX retorna a coordenada X do nó, priorizando root-level (frontend) sobre position.
func (n *RawNodeJSON) GetX() float64 {
	if n.X != 0 { return n.X }
	return n.Position.X
}

// GetY retorna a coordenada Y do nó, priorizando root-level (frontend) sobre position.
func (n *RawNodeJSON) GetY() float64 {
	if n.Y != 0 { return n.Y }
	return n.Position.Y
}

// RawEdgeJSON é uma aresta no JSON do frontend.
type RawEdgeJSON struct {
	ID       string `json:"id"`
	Source   string `json:"source"`     // Node ID de origem
	SourceHandle string `json:"sourceHandle"` // Port name de origem
	Target   string `json:"target"`     // Node ID de destino
	TargetHandle string `json:"targetHandle"` // Port name de destino
	TypeHint string `json:"type_hint,omitempty"`
}

// ============================================================================
// COMPILER
// ============================================================================

// NexusCompiler compila JSON do frontend em ExecutionPlan.
type NexusCompiler struct{}

// NewNexusCompiler cria um novo compilador.
func NewNexusCompiler() *NexusCompiler {
	return &NexusCompiler{}
}

// Compile executa todas as fases de compilação.
func (c *NexusCompiler) Compile(rawJSON []byte) (*ExecutionPlan, error) {
	// === FASE 1: PARSE ===
	rawGraph, err := c.parse(rawJSON)
	if err != nil {
		return nil, fmt.Errorf("nexus compiler: parse failed: %w", err)
	}

	// === FASE 2: BUILD DAG ===
	dag, err := c.buildDAG(rawGraph)
	if err != nil {
		return nil, fmt.Errorf("nexus compiler: DAG construction failed: %w", err)
	}

	// === FASE 3: VALIDATE ===
	mode := rawGraph.Mode
	if mode == "" {
		mode = "fast_lane"
	}
	order, validationResult := graph.ValidateAndSort(dag, mode)
	if !validationResult.Valid {
		errMsgs := make([]string, 0, len(validationResult.Errors))
		for _, e := range validationResult.Errors {
			errMsgs = append(errMsgs, e.Error())
		}
		return nil, fmt.Errorf("nexus compiler: validation failed:\n  - %s",
			joinErrors(errMsgs))
	}

	// === FASE 4: OPTIMIZE (identifica nós paralelos) ===
	maxParallel := c.calculateMaxParallelism(dag, order)

	// === FASE 5: PLAN ===
	plan := c.buildPlan(rawGraph, dag, order, maxParallel)

	return plan, nil
}

// CompileFromMap compila a partir de um mapa Go (útil para testes).
func (c *NexusCompiler) CompileFromMap(data map[string]interface{}) (*ExecutionPlan, error) {
	rawJSON, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("nexus compiler: failed to marshal input: %w", err)
	}
	return c.Compile(rawJSON)
}

// parse desserializa o JSON em RawGraphJSON.
func (c *NexusCompiler) parse(rawJSON []byte) (*RawGraphJSON, error) {
	var rawGraph RawGraphJSON
	if err := json.Unmarshal(rawJSON, &rawGraph); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}

	if len(rawGraph.Nodes) == 0 {
		return nil, fmt.Errorf("graph has no nodes")
	}

	return &rawGraph, nil
}

// buildDAG constrói o DAG a partir do raw graph.
func (c *NexusCompiler) buildDAG(raw *RawGraphJSON) (*graph.DAG, error) {
	dag := graph.NewDAG()

	// Adiciona nós
	for _, rawNode := range raw.Nodes {
		node := &graph.Node{
			ID:     rawNode.ID,
			NodeID: rawNode.NodeID,
			Type:   normalizeComponentType(rawNode.NodeID, rawNode.Type),
			Config: rawNode.Config,
		}
		node.Position.X = rawNode.Position.X
		node.Position.Y = rawNode.Position.Y

		if err := dag.AddNode(node); err != nil {
			return nil, err
		}
	}

	// Adiciona arestas explicitas
	for _, rawEdge := range raw.Edges {
		sourceHandle := normalizeSourceHandle(rawEdge.SourceHandle, rawEdge.Source)
		targetHandle := normalizeTargetHandle(rawEdge.TargetHandle)

		edge := &graph.Edge{
			ID:       rawEdge.ID,
			FromNode: rawEdge.Source,
			FromPort: sourceHandle,
			ToNode:   rawEdge.Target,
			ToPort:   targetHandle,
			TypeHint: rawEdge.TypeHint,
		}

		if err := dag.AddEdge(edge); err != nil {
			return nil, err
		}
	}

	// Inferir arestas a partir do campo 'Next' dos nós (formato v0)
	// IMPORTANTE: Sempre processar o campo 'Next', não apenas quando Edges está vazio.
	// O frontend pode enviar ambos (Edges e Next), e ambos devem ser considerados.
	// Isso permite fluxos complexos com branching, roteamento lógico (If/Else), e múltiplos caminhos.
	for _, rawNode := range raw.Nodes {
		if len(rawNode.Next) > 0 {
			for i, targetID := range rawNode.Next {
				// Segurança: Impede conexões de volta para o Trigger (evita ciclos e erro de validação)
				targetNode := findRawNode(raw, targetID)
				if targetNode != nil && normalizeComponentType(targetNode.NodeID, targetNode.Type) == "trigger" {
					continue
				}

				edgeID := fmt.Sprintf("edge_%s_%s_%d", rawNode.ID, targetID, i)

				// No formato v0, inferimos a porta de saída baseada na posição no array Next
				// e no tipo de nó, permitindo branching (If/Else, Switch) sem Arestas explícitas.
				fromPort := inferPortNameFromIndex(&rawNode, i)

				edge := &graph.Edge{
					ID:       edgeID,
					FromNode: rawNode.ID,
					FromPort: fromPort,
					ToNode:   targetID,
					ToPort:   "in", // V0 sempre assume entrada 'in'
				}

				// Se a aresta já existe (adicionada explicitamente), ignora
				if err := dag.AddEdge(edge); err != nil {
					// Ignora erro de aresta duplicada se vier de ambas as fontes
					continue
				}
			}
		}
	}

	return dag, nil
}

// calculateMaxParallelism calcula o nível máximo de paralelismo do grafo.
func (c *NexusCompiler) calculateMaxParallelism(dag *graph.DAG, order []string) int {
	maxParallel := 1
	for _, nodeID := range order {
		outDegree := dag.OutDegree(nodeID)
		if outDegree > maxParallel {
			maxParallel = outDegree
		}
	}
	return maxParallel
}

// buildPlan gera o ExecutionPlan final.
func (c *NexusCompiler) buildPlan(raw *RawGraphJSON, dag *graph.DAG, order []string, maxParallel int) *ExecutionPlan {
	mode := ModeFastLane
	if raw.Mode == "worker_lane" {
		mode = ModeWorkerLane
	}

	strategy := StrategyTopological
	if raw.Strategy == string(StrategyAtomicParallel) {
		strategy = StrategyAtomicParallel
	}

	// Timeout padrão baseado no modo
	timeout := time.Duration(graph.DefaultFastLaneTimeout) * time.Second
	if mode == ModeWorkerLane {
		timeout = time.Duration(graph.DefaultWorkerLaneTimeout) * time.Second
	}
	if raw.Timeout > 0 {
		timeout = time.Duration(raw.Timeout) * time.Second
	}

	// Compila nós
	compiledNodes := make([]CompiledNode, 0, len(raw.Nodes))
	hasAI := false
	hasData := false
	hasQdrant := false

	for _, rawNode := range raw.Nodes {
		componentType := normalizeComponentType(rawNode.NodeID, rawNode.Type)
		errorStrategy := ErrorFail
		if rawNode.OnError != "" {
			errorStrategy = ErrorStrategy(rawNode.OnError)
		}

		nodeTimeout := time.Duration(0)
		if rawNode.Timeout > 0 {
			nodeTimeout = time.Duration(rawNode.Timeout) * time.Millisecond
		}

		// Extração segura do Security Level (Gatilho de Segurança)
		securityLevel := LevelL1Standard
		if sl, ok := rawNode.Config["securityLevel"].(string); ok && sl != "" {
			securityLevel = SecurityLevel(sl)
		}

		compiled := CompiledNode{
			ID:   rawNode.ID,
			Type: componentType,
			Config: ComponentConfig{
				NodeID:         rawNode.ID,
				NodeType:       ComponentType(componentType),
				Settings:       rawNode.Config,
				ErrorStrategy:  errorStrategy,
				TimeoutPerNode: nodeTimeout,
				SecurityLevel:  securityLevel,
			},
			Inports:        getDefaultInports(componentType),
			Outports:       getDefaultOutports(componentType),
			RetryPolicy:    rawNode.Retry,
			TimeoutPerNode: nodeTimeout,
			ErrorStrategy:  errorStrategy,
		}
		
		// Fallback para L1 se não vier do frontend ou for inválido
		if compiled.Config.SecurityLevel == "" {
			compiled.Config.SecurityLevel = LevelL1Standard
		}

		compiledNodes = append(compiledNodes, compiled)

		// Track capabilities
		switch componentType {
		case "ai_agent", "chain", "prompt_template":
			hasAI = true
		case "data":
			hasData = true
		case "qdrant":
			hasQdrant = true
		}
	}

	// Compila arestas a partir do DAG final, incluindo conexões inferidas por next.
	dagEdges := dag.Edges()
	compiledEdges := make([]CompiledEdge, 0, len(dagEdges))
	for _, rawEdge := range dagEdges {
		compiledEdges = append(compiledEdges, CompiledEdge{
			FromNode: rawEdge.FromNode,
			FromPort: rawEdge.FromPort,
			ToNode:   rawEdge.ToNode,
			ToPort:   rawEdge.ToPort,
			TypeHint: rawEdge.TypeHint,
		})
	}

	// Estima latência
	estimatedLatency := "low"
	if hasAI {
		estimatedLatency = "high"
	} else if hasData || hasQdrant {
		estimatedLatency = "medium"
	}

	return &ExecutionPlan{
		GraphID:          raw.ID,
		Version:          raw.Version,
		Mode:             mode,
		Nodes:            compiledNodes,
		Edges:            compiledEdges,
		TopologicalOrder: order,
		Timeout:          timeout,
		Strategy:         strategy,
		MaxParallelism:   maxParallel,
		Metadata: PlanMetadata{
			CompiledAt:       time.Now().UTC(),
			NodeCount:        len(compiledNodes),
			EdgeCount:        len(compiledEdges),
			HasAINodes:       hasAI,
			HasDataNodes:     hasData,
			HasQdrantNodes:   hasQdrant,
			EstimatedLatency: estimatedLatency,
		},
	}
}

func normalizeComponentType(nodeID, rawType string) string {
	switch nodeID {
	case "response_node":
		return "response"
	case "http_request":
		return "http"
	case "database_action", "db_query":
		return "data"
	case "condition_if":
		return "condition"
	case "qdrant_search":
		return "qdrant"
	case "webhook_trigger", "pre_event_trigger", "post_event_trigger", "cron_trigger", "api_intercept_trigger":
		return "trigger"
	}
	switch rawType {
	case "action":
		return "transform"
	case "tool":
		return "transform"
	case "ai":
		return "transform"
	default:
		return rawType
	}
}

func normalizeSourceHandle(handle, _ string) string {
	if handle == "" {
		return "out"
	}
	switch handle {
	case "out-0", "output-0":
		return "out"
	case "out-1", "output-1":
		return "error"
	default:
		return handle
	}
}

func normalizeTargetHandle(handle string) string {
	switch handle {
	case "", "in-0", "input-0":
		return "in"
	default:
		return handle
	}
}

func defaultOutPort(nodeID, rawType string) string {
	nodeType := normalizeComponentType(nodeID, rawType)
	if nodeType == "condition" {
		return "true"
	}
	return "out"
}

// findRawNode busca um nó no grafo bruto pelo ID.
func findRawNode(raw *RawGraphJSON, id string) *RawNodeJSON {
	for i := range raw.Nodes {
		if raw.Nodes[i].ID == id {
			return &raw.Nodes[i]
		}
	}
	return nil
}

// inferPortNameFromIndex deduz o nome da porta de saída baseado no índice do array 'Next'.
// Essencial para manter a estrutura de grafos complexos vindo do Nexus Architect.
func inferPortNameFromIndex(node *RawNodeJSON, index int) string {
	nodeType := normalizeComponentType(node.NodeID, node.Type)
	
	switch nodeType {
	case "condition":
		// Verifica se tem routes configurados (Enterprise mode com múltiplas rotas)
		if routes, ok := node.Config["routes"].([]interface{}); ok && len(routes) > 0 {
			routeCount := len(routes)
			nextCount := len(node.Next)

			// Sinergia: Roteamento Inteligente Enterprise
			// O último item do array Next é SEMPRE tratado como o 'else' (fallback).
			// Os itens anteriores são mapeados para route_0, route_1, etc.
			if index == nextCount-1 {
				return "else"
			}
			
			// Se o índice estiver dentro do range de rotas, mapeia 1:1
			if index < routeCount {
				return fmt.Sprintf("route_%d", index)
			}

			// Fallback para índices excedentes
			return fmt.Sprintf("route_%d", index)
		}
		// Modo legado (if/else simples): 0->true, 1->false
		if index == 0 {
			return "true"
		}
		if index == 1 {
			return "false"
		}
		return fmt.Sprintf("route_%d", index)
		
	case "switch":
		if index == len(node.Next)-1 {
			return "default"
		}
		return fmt.Sprintf("case_%d", index)
		
	case "data", "http":
		if index == 1 {
			return "error"
		}
		return "out"
		
	default:
		return "out"
	}
}

// getDefaultInports retorna as portas de entrada padrão para um tipo de nó.
func getDefaultInports(nodeType string) []PortDefinition {
	switch nodeType {
	case "trigger":
		return []PortDefinition{} // Trigger não tem entradas
	case "condition":
		return []PortDefinition{{Name: "in", DataType: "any", Required: true}}
	case "aggregator":
		return []PortDefinition{
			{Name: "in_0", DataType: "any", Required: true},
			{Name: "in_1", DataType: "any", Required: false},
			{Name: "in_2", DataType: "any", Required: false},
		}
	case "merge":
		return []PortDefinition{
			{Name: "in_0", DataType: "any", Required: true},
			{Name: "in_1", DataType: "any", Required: true},
		}
	default:
		return []PortDefinition{{Name: "in", DataType: "any", Required: true}}
	}
}

// getDefaultOutports retorna as portas de saída padrão para um tipo de nó.
func getDefaultOutports(nodeType string) []PortDefinition {
	switch nodeType {
	case "response":
		return []PortDefinition{{Name: "out", DataType: "any", Required: false}}
	case "condition":
		return []PortDefinition{
			{Name: "true", DataType: "any", Required: false},
			{Name: "false", DataType: "any", Required: false},
		}
	case "switch":
		return []PortDefinition{
			{Name: "case_0", DataType: "any", Required: false},
			{Name: "case_1", DataType: "any", Required: false},
			{Name: "default", DataType: "any", Required: false},
		}
	case "error_handler":
		return []PortDefinition{
			{Name: "handled", DataType: "any", Required: false},
		}
	case "split":
		return []PortDefinition{
			{Name: "item", DataType: "any", Required: true},
		}
	default:
		return []PortDefinition{
			{Name: "out", DataType: "any", Required: true},
			{Name: "error", DataType: "error", Required: false},
		}
	}
}

func joinErrors(msgs []string) string {
	result := ""
	for i, msg := range msgs {
		if i > 0 {
			result += "\n  - "
		}
		result += msg
	}
	return result
}
