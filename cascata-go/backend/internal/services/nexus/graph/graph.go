package graph

import (
	"fmt"
	"sync"
)

// ============================================================================
// DAG — Directed Acyclic Graph para Topologia de Automações
// ============================================================================
// Estrutura de dados que representa o grafo de nós e arestas do Nexus.
// Suporta inserção de nós, conexão de arestas, ordem topológica
// e detecção de ciclos via Kahn's Algorithm.
// ============================================================================

// Node representa um nó no grafo DAG.
type Node struct {
	ID       string `json:"id"`      // ID único da instância (ex: node_123)
	NodeID   string `json:"node_id"` // ID do componente (ex: response_node, database_action)
	Type     string `json:"type"`    // Categoria do nó (trigger, action, logic)
	Position struct {
		X float64 `json:"x"`
		Y float64 `json:"y"`
	} `json:"position,omitempty"`
	Config map[string]interface{} `json:"config"`
}

// Edge representa uma aresta direcionada entre dois nós (porta a porta).
type Edge struct {
	ID       string `json:"id"`
	FromNode string `json:"from_node"`
	FromPort string `json:"from_port"`
	ToNode   string `json:"to_node"`
	ToPort   string `json:"to_port"`
	TypeHint string `json:"type_hint,omitempty"` // "string", "number", "object", "array", "boolean", "any"
}

// DAG é a estrutura de dados principal para o grafo de automação.
type DAG struct {
	mu    sync.RWMutex
	nodes map[string]*Node
	edges []*Edge

	// Listas de adjacência para travessia eficiente
	outgoing map[string][]*Edge // nodeID → arestas de saída
	incoming map[string][]*Edge // nodeID → arestas de entrada
}

// NewDAG cria um novo DAG vazio.
func NewDAG() *DAG {
	return &DAG{
		nodes:    make(map[string]*Node),
		edges:    make([]*Edge, 0),
		outgoing: make(map[string][]*Edge),
		incoming: make(map[string][]*Edge),
	}
}

// AddNode adiciona um nó ao DAG. Retorna erro se o ID já existir.
func (g *DAG) AddNode(node *Node) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if node.ID == "" {
		return fmt.Errorf("graph: node ID is empty")
	}
	if _, exists := g.nodes[node.ID]; exists {
		return fmt.Errorf("graph: duplicate node ID %q", node.ID)
	}

	g.nodes[node.ID] = node
	return nil
}

// AddEdge adiciona uma aresta ao DAG. Retorna erro se nós referenciados não existirem.
func (g *DAG) AddEdge(edge *Edge) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if _, exists := g.nodes[edge.FromNode]; !exists {
		return fmt.Errorf("graph: source node %q does not exist", edge.FromNode)
	}
	if _, exists := g.nodes[edge.ToNode]; !exists {
		return fmt.Errorf("graph: target node %q does not exist", edge.ToNode)
	}
	if edge.FromNode == edge.ToNode {
		return fmt.Errorf("graph: self-loop detected on node %q", edge.FromNode)
	}

	g.edges = append(g.edges, edge)
	g.outgoing[edge.FromNode] = append(g.outgoing[edge.FromNode], edge)
	g.incoming[edge.ToNode] = append(g.incoming[edge.ToNode], edge)
	return nil
}

// GetNode retorna um nó pelo ID.
func (g *DAG) GetNode(id string) (*Node, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	n, ok := g.nodes[id]
	return n, ok
}

// Nodes retorna todos os nós.
func (g *DAG) Nodes() map[string]*Node {
	g.mu.RLock()
	defer g.mu.RUnlock()
	// Retorna cópia para segurança
	result := make(map[string]*Node, len(g.nodes))
	for k, v := range g.nodes {
		result[k] = v
	}
	return result
}

// Edges retorna todas as arestas.
func (g *DAG) Edges() []*Edge {
	g.mu.RLock()
	defer g.mu.RUnlock()
	result := make([]*Edge, len(g.edges))
	copy(result, g.edges)
	return result
}

// NodeCount retorna o número de nós.
func (g *DAG) NodeCount() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return len(g.nodes)
}

// EdgeCount retorna o número de arestas.
func (g *DAG) EdgeCount() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return len(g.edges)
}

// OutgoingEdges retorna todas as arestas de saída de um nó.
func (g *DAG) OutgoingEdges(nodeID string) []*Edge {
	g.mu.RLock()
	defer g.mu.RUnlock()
	result := make([]*Edge, len(g.outgoing[nodeID]))
	copy(result, g.outgoing[nodeID])
	return result
}

// IncomingEdges retorna todas as arestas de entrada de um nó.
func (g *DAG) IncomingEdges(nodeID string) []*Edge {
	g.mu.RLock()
	defer g.mu.RUnlock()
	result := make([]*Edge, len(g.incoming[nodeID]))
	copy(result, g.incoming[nodeID])
	return result
}

// Predecessors retorna os IDs dos nós predecessores diretos.
func (g *DAG) Predecessors(nodeID string) []string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	seen := make(map[string]bool)
	result := make([]string, 0)
	for _, edge := range g.incoming[nodeID] {
		if !seen[edge.FromNode] {
			seen[edge.FromNode] = true
			result = append(result, edge.FromNode)
		}
	}
	return result
}

// Successors retorna os IDs dos nós sucessores diretos.
func (g *DAG) Successors(nodeID string) []string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	seen := make(map[string]bool)
	result := make([]string, 0)
	for _, edge := range g.outgoing[nodeID] {
		if !seen[edge.ToNode] {
			seen[edge.ToNode] = true
			result = append(result, edge.ToNode)
		}
	}
	return result
}

// FindNodesByType retorna todos os nós de um tipo específico.
func (g *DAG) FindNodesByType(nodeType string) []*Node {
	g.mu.RLock()
	defer g.mu.RUnlock()
	result := make([]*Node, 0)
	for _, node := range g.nodes {
		if node.Type == nodeType {
			result = append(result, node)
		}
	}
	return result
}

// TopologicalSort retorna a ordem topológica dos nós usando Kahn's Algorithm.
// Retorna erro se o grafo contiver ciclos, incluindo o caminho do ciclo.
func (g *DAG) TopologicalSort() ([]string, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	// Calcula in-degree de cada nó
	inDegree := make(map[string]int, len(g.nodes))
	for id := range g.nodes {
		inDegree[id] = 0
	}
	for _, edge := range g.edges {
		inDegree[edge.ToNode]++
	}

	// Enfileira todos os nós com in-degree 0
	queue := make([]string, 0)
	for id, deg := range inDegree {
		if deg == 0 {
			queue = append(queue, id)
		}
	}

	// Processa BFS
	order := make([]string, 0, len(g.nodes))
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		order = append(order, current)

		for _, edge := range g.outgoing[current] {
			inDegree[edge.ToNode]--
			if inDegree[edge.ToNode] == 0 {
				queue = append(queue, edge.ToNode)
			}
		}
	}

	// Se nem todos os nós foram visitados, existe um ciclo
	if len(order) != len(g.nodes) {
		cyclePath := g.findCyclePath(inDegree)
		return nil, fmt.Errorf("graph: cycle detected involving nodes: %v", cyclePath)
	}

	return order, nil
}

// findCyclePath identifica os nós envolvidos no ciclo para diagnóstico.
func (g *DAG) findCyclePath(inDegree map[string]int) []string {
	// Nós com in-degree > 0 após Kahn são parte do ciclo
	cycleNodes := make([]string, 0)
	for id, deg := range inDegree {
		if deg > 0 {
			cycleNodes = append(cycleNodes, id)
		}
	}

	if len(cycleNodes) == 0 {
		return cycleNodes
	}

	// Tenta reconstruir o caminho do ciclo via DFS
	visited := make(map[string]bool)
	path := make([]string, 0)
	var dfs func(node string) bool
	dfs = func(node string) bool {
		if visited[node] {
			// Encontrou ciclo — extrai caminho
			for _, n := range path {
				if n == node {
					return true
				}
			}
			return false
		}
		visited[node] = true
		path = append(path, node)
		for _, edge := range g.outgoing[node] {
			if inDegree[edge.ToNode] > 0 {
				if dfs(edge.ToNode) {
					return true
				}
			}
		}
		path = path[:len(path)-1]
		return false
	}

	for _, start := range cycleNodes {
		visited = make(map[string]bool)
		path = make([]string, 0)
		if dfs(start) {
			return path
		}
	}

	return cycleNodes
}

// HasCycle retorna true se o DAG contiver ciclos.
func (g *DAG) HasCycle() bool {
	_, err := g.TopologicalSort()
	return err != nil
}

// InDegree retorna o grau de entrada de um nó.
func (g *DAG) InDegree(nodeID string) int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return len(g.incoming[nodeID])
}

// OutDegree retorna o grau de saída de um nó.
func (g *DAG) OutDegree(nodeID string) int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return len(g.outgoing[nodeID])
}
