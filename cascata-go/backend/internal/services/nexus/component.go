package nexus

import (
	"context"
	"fmt"
	"time"
)

// ============================================================================
// COMPONENT — Interface Base Universal de Nós do Nexus
// ============================================================================
// Cada nó no Nexus é um Component Go. Componentes são processos isolados
// que se comunicam exclusivamente via canais (Information Packets).
// ============================================================================

// ComponentType identifica o tipo de componente.
type ComponentType string

const (
	TypeTrigger      ComponentType = "trigger"
	TypeHTTP         ComponentType = "http"
	TypeData         ComponentType = "data"
	TypeQdrant       ComponentType = "qdrant"
	TypeResponse     ComponentType = "response"
	TypeCondition    ComponentType = "condition"
	TypeForeach      ComponentType = "foreach"
	TypeAggregator   ComponentType = "aggregator"
	TypeSubdag       ComponentType = "subdag"
	TypeTransform    ComponentType = "transform"
	TypeDelay        ComponentType = "delay"
	TypeSwitch       ComponentType = "switch"
	TypeMerge        ComponentType = "merge"
	TypeSplit        ComponentType = "split"
	TypeErrorHandler ComponentType = "error_handler"
	TypePrivacyGuard ComponentType = "privacy_guard"
	TypeWebhook      ComponentType = "webhook"
	TypeScript       ComponentType = "script"
	TypeAIAgent      ComponentType = "ai_agent"
	TypeChain        ComponentType = "chain"
	TypeMemory       ComponentType = "memory"
	TypePrompt       ComponentType = "prompt_template"
)

// ComponentStatus indica o estado de execução de um componente.
type ComponentStatus int

const (
	StatusIdle       ComponentStatus = iota // Aguardando
	StatusWaiting                           // Esperando dados de entrada
	StatusProcessing                        // Executando
	StatusSuccess                           // Executado com sucesso
	StatusError                             // Falhou
	StatusTimeout                           // Excedeu timeout
	StatusSkipped                           // Não executado (caminho não tomado)
)

func (s ComponentStatus) String() string {
	switch s {
	case StatusIdle:
		return "idle"
	case StatusWaiting:
		return "waiting"
	case StatusProcessing:
		return "processing"
	case StatusSuccess:
		return "success"
	case StatusError:
		return "error"
	case StatusTimeout:
		return "timeout"
	case StatusSkipped:
		return "skipped"
	default:
		return "unknown"
	}
}

// ErrorStrategy define como um nó lida com erros.
type ErrorStrategy string

const (
	ErrorFail     ErrorStrategy = "fail"     // Propaga o erro, aborta fluxo
	ErrorBypass   ErrorStrategy = "bypass"   // Ignora o erro, pula o nó
	ErrorFallback ErrorStrategy = "fallback" // Executa lógica de fallback
)

// RetryPolicy define política de re-tentativa de um nó.
type RetryPolicy struct {
	MaxRetries     int           `json:"max_retries"`      // Máximo de tentativas (0 = sem retry)
	BackoffBase    time.Duration `json:"backoff_base_ms"`  // Base do backoff exponencial
	BackoffMax     time.Duration `json:"backoff_max_ms"`   // Máximo do backoff
	RetryableErrors []string    `json:"retryable_errors"` // Apenas esses erros são retriáveis
}

// SecurityLevel define o nível de proteção exigido para executar um nó (Security Gateway).
type SecurityLevel string

const (
	LevelL1Standard SecurityLevel = "L1" // Standard — Acesso livre (inclui anônimos)
	LevelL2Strict   SecurityLevel = "L2" // Strict — Exige autenticação (Usuários reais)
	LevelL3Admin    SecurityLevel = "L3" // Admin — Exige privilégios administrativos ou Service Keys
)

// ComponentConfig armazena a configuração de um componente.
type ComponentConfig struct {
	NodeID         string                 `json:"node_id"`
	NodeType       ComponentType          `json:"node_type"`
	Settings       map[string]interface{} `json:"settings"` // Configurações específicas do tipo
	Retry          *RetryPolicy           `json:"retry,omitempty"`
	ErrorStrategy  ErrorStrategy          `json:"error_strategy"`
	TimeoutPerNode time.Duration          `json:"timeout_per_node"`
	SecurityLevel  SecurityLevel          `json:"security_level"` // L1, L2, L3
}

// PortDefinition define uma porta de entrada ou saída de um componente.
type PortDefinition struct {
	Name     string `json:"name"`      // Nome da porta (ex: "in", "out", "error", "true", "false")
	DataType string `json:"data_type"` // Tipo esperado: "string", "number", "boolean", "object", "array", "any", "error"
	Required bool   `json:"required"`  // Se obrigatória
}

// Component é a unidade fundamental de execução no Nexus.
// Cada componente é um processo isolado que se comunica apenas via canais.
type Component interface {
	// ID retorna o identificador único do componente dentro do grafo
	ID() string

	// Type retorna o tipo do componente
	Type() ComponentType

	// Inports retorna as definições das portas de entrada
	InportDefs() []PortDefinition

	// Outports retorna as definições das portas de saída
	OutportDefs() []PortDefinition

	// Init inicializa o componente com sua configuração específica
	Init(config ComponentConfig) error

	// Process processa um InformationPacket de entrada e retorna o(s) resultado(s).
	// O map de retorno é portName → []*InformationPacket
	// Exemplo: {"out": [ip1], "error": [ip2]}
	Process(ctx context.Context, ip *InformationPacket, state *NexusState) (map[string][]*InformationPacket, error)

	// Status retorna o estado atual do componente
	Status() ComponentStatus

	// ValidateConfig valida a configuração do componente antes da execução
	ValidateConfig() error
}

// ToolCapable é implementado por componentes que podem atuar como ferramentas de IA.
type ToolCapable interface {
	// ToolSchema retorna a descrição JSON Schema para uso pelo LLM
	ToolSchema() ToolSchema
}

// ToolSchema descreve uma ferramenta para o LLM.
type ToolSchema struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]ParamSchema `json:"parameters"`
	Category    string                 `json:"category"`
	Tags        []string               `json:"tags"`
	Examples    []ToolExample          `json:"examples,omitempty"`
}

// ParamSchema descreve um parâmetro da ferramenta.
type ParamSchema struct {
	Type        string      `json:"type"`
	Description string      `json:"description"`
	Required    bool        `json:"required"`
	Default     interface{} `json:"default,omitempty"`
	Enum        []string    `json:"enum,omitempty"`
}

// ToolExample é um exemplo de uso da ferramenta para few-shot learning.
type ToolExample struct {
	Input       map[string]interface{} `json:"input"`
	Output      map[string]interface{} `json:"output"`
	Description string                 `json:"description"`
}

// ============================================================================
// BASE COMPONENT — Implementação base compartilhada por todos os componentes
// ============================================================================

// BaseComponent fornece funcionalidade padrão para todos os componentes.
// Componentes concretos devem embuti-la e sobrescrever Process().
type BaseComponent struct {
	id     string
	typ    ComponentType
	config ComponentConfig
	status ComponentStatus
	inports  []PortDefinition
	outports []PortDefinition
}

func NewBaseComponent(id string, typ ComponentType, inports, outports []PortDefinition) *BaseComponent {
	return &BaseComponent{
		id:       id,
		typ:      typ,
		status:   StatusIdle,
		inports:  inports,
		outports: outports,
	}
}

func (b *BaseComponent) ID() string           { return b.id }
func (b *BaseComponent) Type() ComponentType   { return b.typ }
func (b *BaseComponent) Status() ComponentStatus { return b.status }
func (b *BaseComponent) InportDefs() []PortDefinition  { return b.inports }
func (b *BaseComponent) OutportDefs() []PortDefinition { return b.outports }
func (b *BaseComponent) Config() ComponentConfig { return b.config }

func (b *BaseComponent) SetStatus(s ComponentStatus) { b.status = s }

func (b *BaseComponent) Init(config ComponentConfig) error {
	b.config = config
	return nil
}

func (b *BaseComponent) ValidateConfig() error {
	if b.id == "" {
		return fmt.Errorf("nexus: component ID is empty")
	}
	return nil
}

// Process padrão — deve ser sobrescrito por implementações concretas.
func (b *BaseComponent) Process(ctx context.Context, ip *InformationPacket, state *NexusState) (map[string][]*InformationPacket, error) {
	return nil, fmt.Errorf("nexus: Process() not implemented for component %s (type: %s)", b.id, b.typ)
}

// EmitSingle é helper para emitir um único pacote em uma porta.
func EmitSingle(portName string, ip *InformationPacket) map[string][]*InformationPacket {
	return map[string][]*InformationPacket{
		portName: {ip},
	}
}

// EmitMultiple é helper para emitir múltiplos pacotes em uma porta.
func EmitMultiple(portName string, ips []*InformationPacket) map[string][]*InformationPacket {
	return map[string][]*InformationPacket{
		portName: ips,
	}
}

// EmitEmpty retorna mapa vazio (sem saída — nó terminal ou skip).
func EmitEmpty() map[string][]*InformationPacket {
	return map[string][]*InformationPacket{}
}
