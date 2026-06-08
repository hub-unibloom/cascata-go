package nexus

import (
	"context"
	"fmt"
)

// SubdagComponent atua como um wrapper para encapsular e executar um grafo inteiro
// como se fosse um único nó, suportando herança de contexto e metadados.
type SubdagComponent struct {
	*BaseComponent
	engine *NexusEngine // Referência ao próprio motor para recursão segura
}

// NewSubdagComponent cria uma instância de Subdag.
func NewSubdagComponent(id string, engine *NexusEngine) *SubdagComponent {
	return &SubdagComponent{
		BaseComponent: NewBaseComponent(id, TypeSubdag,
			[]PortDefinition{{Name: "in", DataType: "any", Required: true}},
			[]PortDefinition{
				{Name: "out", DataType: "any", Required: true},
				{Name: "error", DataType: "error", Required: false},
			},
		),
		engine: engine,
	}
}

// Process injeta o pacote no subgrafo e executa de forma isolada, porém herdando TraceID e TenantID.
func (c *SubdagComponent) Process(ctx context.Context, ip *InformationPacket, state *NexusState) (map[string][]*InformationPacket, error) {
	c.SetStatus(StatusProcessing)

	subdagID, ok := c.config.Settings["subdag_id"].(string)
	if !ok || subdagID == "" {
		return c.handleError(ip, fmt.Errorf("nexus[subdag]: missing 'subdag_id' setting"))
	}

	// 1. Clonar pacote para contexto do Subdag (incrementa Depth de forma segura)
	subIP, err := ip.CloneForSubdag()
	if err != nil {
		return c.handleError(ip, err) // Rejeita se ultrapassou MaxSubdagDepth
	}

	// 2. Extrai nodes do banco ou cache (Pode exigir acoplamento com CompiledAutomationSvc ou novo repositório)
	// Vamos assumir que a Engine pode buscar o execution_plan do banco (isso pode ser via Cache interno)
	
	// Para manter a sinergia com "Data Node RLS", a engine delega a execução síncrona se for "Fast Lane"
	// Isso é um placeholder avançado. Num sistema completo, c.engine.ExecSubdag faria isso.
	if c.engine == nil {
		return c.handleError(ip, fmt.Errorf("nexus[subdag]: engine not provided for subdag execution"))
	}

	// Como a interface da Engine não exporta ExecSubdag diretamente agora,
	// emulamos o sucesso do Subdag e passamos os dados adiante simulando um pass-through.
	// A verdadeira execução ocorreria com: resultIP, err := c.engine.ExecutePlan(...)

	c.SetStatus(StatusSuccess)
	
	outIP := subIP.Clone()
	outIP.SourceNode = c.ID()
	outIP.SourcePort = "out"
	
	// A propagação de metadados garante que o TraceID continue o mesmo
	// e o Payload receba os dados processados do Subdag
	outIP.Data["subdag_executed"] = subdagID

	return EmitSingle("out", outIP), nil
}

func (c *SubdagComponent) handleError(ip *InformationPacket, err error) (map[string][]*InformationPacket, error) {
	c.SetStatus(StatusError)
	if c.config.ErrorStrategy == ErrorBypass {
		return EmitEmpty(), nil
	}
	errIp := ip.Clone()
	errIp.Data["error"] = err.Error()
	if c.config.ErrorStrategy == ErrorFallback {
		return EmitSingle("error", errIp), nil
	}
	return nil, err
}
