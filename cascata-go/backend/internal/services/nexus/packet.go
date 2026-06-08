package nexus

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// ============================================================================
// INFORMATION PACKET — Unidade Atômica de Dados do Nexus
// ============================================================================
// Cada IP carrega metadados indestrutíveis que garantem rastreabilidade,
// isolamento multi-tenant e auditoria completa em todo o ciclo de vida.
// ============================================================================

// InformationPacket é a unidade de dados que flui entre componentes.
type InformationPacket struct {
	// === Metadados Indestrutíveis (nunca podem ser removidos ou alterados) ===
	ID         string    `json:"id"`          // UUID v4 único do pacote
	TraceID    string    `json:"trace_id"`    // ID de rastreamento da requisição original
	TenantID   string    `json:"tenant_id"`   // Slug do tenant (do Nginx Layer 0)
	UserUUID   string    `json:"user_uuid"`   // UUID do usuário autenticado (Layer 2)
	AuthSource string    `json:"auth_source"` // "jwt", "apikey", "anon"
	CreatedAt  time.Time `json:"created_at"`  // Timestamp de criação
	SourceNode string    `json:"source_node"` // ID do nó que gerou este pacote
	SourcePort string    `json:"source_port"` // Porta de saída que gerou este pacote

	// === Payload (dados transportados) ===
	Data map[string]interface{} `json:"data"` // Dados do pacote

	// === Metadados Operacionais (podem ser atualizados pelo motor) ===
	Sequence   int64   `json:"sequence"`               // Sequência global de pacotes no grafo
	Depth      int     `json:"depth"`                  // Profundidade de nesting (para Subdags)
	ForeachIdx *int    `json:"foreach_idx,omitempty"`   // Índice da iteração (se dentro de Foreach)
	ParentID   *string `json:"parent_id,omitempty"`     // ID do pacote pai (para Subdags)
}

// Limites de validação de pacotes.
const (
	MaxPayloadSize    = 10 * 1024 * 1024 // 10 MB
	MaxSubdagDepth    = 5
	MaxForeachItems   = 1000
	MaxInFlightPackets = 10000
)

// NewInformationPacket cria um novo IP com metadados indestrutíveis preenchidos.
func NewInformationPacket(tenantID, userUUID, authSource, traceID string) *InformationPacket {
	return &InformationPacket{
		ID:         uuid.New().String(),
		TraceID:    traceID,
		TenantID:   tenantID,
		UserUUID:   userUUID,
		AuthSource: authSource,
		CreatedAt:  time.Now().UTC(),
		Data:       make(map[string]interface{}),
	}
}

// NewFromTrigger cria um IP a partir de dados de trigger (payload HTTP).
func NewFromTrigger(tenantID, userUUID, authSource, traceID string, payload map[string]interface{}) *InformationPacket {
	ip := NewInformationPacket(tenantID, userUUID, authSource, traceID)
	ip.SourceNode = "trigger"
	ip.SourcePort = "out"
	if payload != nil {
		ip.Data = payload
	}
	return ip
}

// Clone cria uma cópia profunda do pacote com novo ID, preservando TraceID.
// Usado em branching (Condition/Switch).
func (ip *InformationPacket) Clone() *InformationPacket {
	clone := &InformationPacket{
		ID:         uuid.New().String(),
		TraceID:    ip.TraceID,
		TenantID:   ip.TenantID,
		UserUUID:   ip.UserUUID,
		AuthSource: ip.AuthSource,
		CreatedAt:  time.Now().UTC(),
		SourceNode: ip.SourceNode,
		SourcePort: ip.SourcePort,
		Sequence:   ip.Sequence,
		Depth:      ip.Depth,
		ForeachIdx: ip.ForeachIdx,
		ParentID:   ip.ParentID,
	}

	// Deep clone Data via JSON roundtrip
	if ip.Data != nil {
		rawData, _ := json.Marshal(ip.Data)
		clone.Data = make(map[string]interface{})
		json.Unmarshal(rawData, &clone.Data)
	}

	return clone
}

// CloneWithData cria uma cópia preservando metadados, mas com dados novos.
func (ip *InformationPacket) CloneWithData(data map[string]interface{}) *InformationPacket {
	clone := ip.Clone()
	clone.Data = data
	return clone
}

// CloneForForeach cria cópia preparada para iteração Foreach.
func (ip *InformationPacket) CloneForForeach(index int) *InformationPacket {
	clone := ip.Clone()
	clone.ForeachIdx = &index
	return clone
}

// CloneForSubdag cria cópia preparada para execução em Subdag.
func (ip *InformationPacket) CloneForSubdag() (*InformationPacket, error) {
	if ip.Depth+1 > MaxSubdagDepth {
		return nil, fmt.Errorf("nexus: max subdag depth exceeded (%d/%d)", ip.Depth+1, MaxSubdagDepth)
	}
	clone := ip.Clone()
	parentID := ip.ID
	clone.ParentID = &parentID
	clone.Depth = ip.Depth + 1
	return clone, nil
}

// Propagate atualiza o SourceNode/SourcePort para indicar a passagem por um nó.
// Retorna o mesmo ponteiro para fluent chaining.
func (ip *InformationPacket) Propagate(nodeID, portName string, seq int64) *InformationPacket {
	ip.SourceNode = nodeID
	ip.SourcePort = portName
	ip.Sequence = seq
	return ip
}

// Validate garante que o IP está dentro dos limites aceitáveis.
func (ip *InformationPacket) Validate() error {
	if ip.ID == "" {
		return fmt.Errorf("nexus: packet ID is empty")
	}
	if ip.TraceID == "" {
		return fmt.Errorf("nexus: packet TraceID is empty")
	}
	if ip.TenantID == "" {
		return fmt.Errorf("nexus: packet TenantID is empty — tenant isolation VIOLATED")
	}

	// Validate payload size
	if ip.Data != nil {
		raw, err := json.Marshal(ip.Data)
		if err != nil {
			return fmt.Errorf("nexus: packet data cannot be serialized: %w", err)
		}
		if len(raw) > MaxPayloadSize {
			return fmt.Errorf("nexus: packet payload exceeds maximum size (%d/%d bytes)", len(raw), MaxPayloadSize)
		}
	}

	if ip.Depth > MaxSubdagDepth {
		return fmt.Errorf("nexus: packet depth exceeds maximum (%d/%d)", ip.Depth, MaxSubdagDepth)
	}

	return nil
}

// GetDataField extrai um campo do Data usando dot-notation simplificada.
func (ip *InformationPacket) GetDataField(field string) (interface{}, bool) {
	if ip.Data == nil {
		return nil, false
	}
	val, ok := ip.Data[field]
	return val, ok
}

// SetDataField define um campo no Data.
func (ip *InformationPacket) SetDataField(field string, value interface{}) {
	if ip.Data == nil {
		ip.Data = make(map[string]interface{})
	}
	ip.Data[field] = value
}
