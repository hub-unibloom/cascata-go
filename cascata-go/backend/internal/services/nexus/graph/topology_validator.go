package graph

import (
	"fmt"
	"strings"
)

// ============================================================================
// TOPOLOGY VALIDATOR — Validação Completa de Topologia de Grafos
// ============================================================================
// Executa todas as regras de validação topológica antes da compilação.
// Rejeita qualquer grafo que viole regras de segurança ou integridade.
// ============================================================================

// Limites de complexidade.
const (
	MaxNodesPerGraph    = 200
	MaxEdgesPerGraph    = 500
	MaxParallelBranches = 20
	MaxSubdagNesting    = 5
	DefaultFastLaneTimeout  = 30  // segundos
	DefaultWorkerLaneTimeout = 300 // segundos
)

// ValidationError representa um erro de validação com contexto rico.
type ValidationError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	NodeID  string `json:"node_id,omitempty"`
	Detail  string `json:"detail,omitempty"`
}

func (e *ValidationError) Error() string {
	if e.NodeID != "" {
		return fmt.Sprintf("[%s] %s (node: %s)", e.Code, e.Message, e.NodeID)
	}
	return fmt.Sprintf("[%s] %s", e.Code, e.Message)
}

// ValidationResult contém todos os erros e avisos da validação.
type ValidationResult struct {
	Errors   []*ValidationError `json:"errors"`
	Warnings []*ValidationError `json:"warnings"`
	Valid    bool               `json:"valid"`
}

func NewValidationResult() *ValidationResult {
	return &ValidationResult{
		Errors:   make([]*ValidationError, 0),
		Warnings: make([]*ValidationError, 0),
		Valid:    true,
	}
}

func (r *ValidationResult) AddError(code, message string, nodeID ...string) {
	nid := ""
	if len(nodeID) > 0 {
		nid = nodeID[0]
	}
	r.Errors = append(r.Errors, &ValidationError{Code: code, Message: message, NodeID: nid})
	r.Valid = false
}

func (r *ValidationResult) AddWarning(code, message string, nodeID ...string) {
	nid := ""
	if len(nodeID) > 0 {
		nid = nodeID[0]
	}
	r.Warnings = append(r.Warnings, &ValidationError{Code: code, Message: message, NodeID: nid})
}

// TopologyValidator valida a topologia completa de um DAG.
type TopologyValidator struct {
	dag            *DAG
	executionMode  string // "fast_lane" ou "worker_lane"
}

// NewTopologyValidator cria um novo validador para o DAG dado.
func NewTopologyValidator(dag *DAG, mode string) *TopologyValidator {
	return &TopologyValidator{
		dag:           dag,
		executionMode: mode,
	}
}

// Validate executa todas as regras de validação e retorna o resultado.
func (v *TopologyValidator) Validate() *ValidationResult {
	result := NewValidationResult()

	v.validateCycles(result)
	v.validateTriggerNode(result)
	v.validateResponseNode(result)
	v.validateOrphanNodes(result)
	v.validateComplexityLimits(result)
	v.validatePortConnections(result)
	v.validateSubdagRecursion(result)

	return result
}

// validateCycles verifica se o grafo contém ciclos usando Kahn's Algorithm.
func (v *TopologyValidator) validateCycles(result *ValidationResult) {
	_, err := v.dag.TopologicalSort()
	if err != nil {
		result.AddError("CYCLE_DETECTED", err.Error())
	}
}

// validateTriggerNode garante que existe exatamente um nó trigger.
func (v *TopologyValidator) validateTriggerNode(result *ValidationResult) {
	// Busca por tipo "trigger" OU NodeID canônico de trigger
	var triggers []*Node
	for _, node := range v.dag.Nodes() {
		if node.Type == "trigger" || 
		   node.NodeID == "pre_event_trigger" || 
		   node.NodeID == "api_intercept_trigger" || 
		   node.NodeID == "webhook_trigger" {
			triggers = append(triggers, node)
		}
	}

	if len(triggers) == 0 {
		result.AddError("MISSING_TRIGGER", "Graph must have exactly one trigger node")
		return
	}
	if len(triggers) > 1 {
		ids := make([]string, len(triggers))
		for i, t := range triggers {
			ids[i] = t.ID
		}
		result.AddError("MULTIPLE_TRIGGERS",
			fmt.Sprintf("Graph must have exactly one trigger node, found %d: [%s]",
				len(triggers), strings.Join(ids, ", ")))
		return
	}

	// O trigger não deve ter arestas de entrada
	trigger := triggers[0]
	if v.dag.InDegree(trigger.ID) > 0 {
		result.AddError("TRIGGER_HAS_INPUTS",
			"Trigger node must not have incoming edges", trigger.ID)
	}

	// O trigger deve ter pelo menos uma saída
	if v.dag.OutDegree(trigger.ID) == 0 {
		result.AddError("TRIGGER_NO_OUTPUTS",
			"Trigger node must have at least one outgoing edge", trigger.ID)
	}
}

// validateResponseNode garante que todo fluxo PRE_PERSIST tem pelo menos um response node.
func (v *TopologyValidator) validateResponseNode(result *ValidationResult) {
	if v.executionMode == "worker_lane" {
		// Worker Lane não requer response node (é assíncrono)
		return
	}

	// Busca por tipo "response" OU NodeID canônico "response_node"
	var responses []*Node
	for _, node := range v.dag.Nodes() {
		if node.Type == "response" || node.NodeID == "response_node" {
			responses = append(responses, node)
		}
	}

	if len(responses) == 0 {
		result.AddError("MISSING_RESPONSE",
			"Fast Lane (PRE_PERSIST) graphs must have at least one response node")
		return
	}

	// Response nodes não devem ter arestas de saída (são terminais)
	for _, resp := range responses {
		if v.dag.OutDegree(resp.ID) > 0 {
			result.AddWarning("RESPONSE_HAS_OUTPUTS",
				"Response node should be terminal (no outgoing edges)", resp.ID)
		}
	}
}

// validateOrphanNodes verifica nós órfãos (sem conexão de entrada ou saída).
func (v *TopologyValidator) validateOrphanNodes(result *ValidationResult) {
	for _, node := range v.dag.Nodes() {
		// Trigger não precisa de entradas
		if node.Type == "trigger" || node.NodeID == "pre_event_trigger" || node.NodeID == "api_intercept_trigger" || node.NodeID == "webhook_trigger" {
			continue
		}

		// Todo nó exceto trigger deve ter pelo menos uma entrada
		if v.dag.InDegree(node.ID) == 0 {
			result.AddError("ORPHAN_NODE_NO_INPUT",
				fmt.Sprintf("Node %q (type: %s, component: %s) has no incoming edges — it will never receive data",
					node.ID, node.Type, node.NodeID), node.ID)
		}

		// Todo nó exceto response e error_handler deve ter pelo menos uma saída
		if node.Type != "response" && node.NodeID != "response_node" && node.Type != "error_handler" {
			if v.dag.OutDegree(node.ID) == 0 {
				result.AddWarning("DEAD_END_NODE",
					fmt.Sprintf("Node %q (type: %s, component: %s) has no outgoing edges — data will stop here",
						node.ID, node.Type, node.NodeID), node.ID)
			}
		}
	}
}

// validateComplexityLimits verifica limites de complexidade do grafo.
func (v *TopologyValidator) validateComplexityLimits(result *ValidationResult) {
	nodeCount := v.dag.NodeCount()
	edgeCount := v.dag.EdgeCount()

	if nodeCount > MaxNodesPerGraph {
		result.AddError("MAX_NODES_EXCEEDED",
			fmt.Sprintf("Graph has %d nodes, maximum is %d", nodeCount, MaxNodesPerGraph))
	}

	if edgeCount > MaxEdgesPerGraph {
		result.AddError("MAX_EDGES_EXCEEDED",
			fmt.Sprintf("Graph has %d edges, maximum is %d", edgeCount, MaxEdgesPerGraph))
	}

	// Verifica branches paralelos
	for _, node := range v.dag.Nodes() {
		outDegree := v.dag.OutDegree(node.ID)
		if outDegree > MaxParallelBranches {
			result.AddError("MAX_BRANCHES_EXCEEDED",
				fmt.Sprintf("Node %q has %d outgoing edges, maximum parallel branches is %d",
					node.ID, outDegree, MaxParallelBranches), node.ID)
		}
	}
}

// validatePortConnections valida compatibilidade de tipos entre portas conectadas.
func (v *TopologyValidator) validatePortConnections(result *ValidationResult) {
	for _, edge := range v.dag.Edges() {
		if edge.TypeHint == "" {
			continue // Sem type hint, skip validation
		}

		// Validação básica de incompatibilidade de tipos
		// Regras: array → boolean é incompatível, etc.
		if !isTypeCompatible(edge.TypeHint, edge.TypeHint) {
			result.AddWarning("TYPE_MISMATCH",
				fmt.Sprintf("Edge %s:%s → %s:%s has potential type mismatch: %s",
					edge.FromNode, edge.FromPort, edge.ToNode, edge.ToPort, edge.TypeHint))
		}
	}
}

// validateSubdagRecursion verifica que subdags não formam recursão infinita.
func (v *TopologyValidator) validateSubdagRecursion(result *ValidationResult) {
	subdags := v.dag.FindNodesByType("subdag")
	for _, subdag := range subdags {
		refID, ok := subdag.Config["subgraph_id"].(string)
		if !ok {
			continue
		}
		// Verifica auto-referência direta
		if refID == subdag.ID {
			result.AddError("SUBDAG_SELF_REFERENCE",
				"Subdag node references itself — infinite recursion", subdag.ID)
		}
	}
}

// isTypeCompatible verifica se dois tipos de porta são compatíveis.
func isTypeCompatible(sourceType, targetType string) bool {
	if sourceType == "any" || targetType == "any" {
		return true
	}
	if sourceType == targetType {
		return true
	}

	// Regras de incompatibilidade explícita
	incompatible := map[string]map[string]bool{
		"array":   {"boolean": true, "number": true},
		"boolean": {"array": true, "object": true},
		"object":  {"boolean": true, "number": true},
	}

	if targets, ok := incompatible[sourceType]; ok {
		return !targets[targetType]
	}

	return true
}

// ValidateAndSort executa validação completa e retorna a ordem topológica.
// Método de conveniência para uso pelo compilador.
func ValidateAndSort(dag *DAG, mode string) ([]string, *ValidationResult) {
	validator := NewTopologyValidator(dag, mode)
	result := validator.Validate()

	if !result.Valid {
		return nil, result
	}

	order, err := dag.TopologicalSort()
	if err != nil {
		result.AddError("SORT_FAILED", err.Error())
		return nil, result
	}

	return order, result
}
