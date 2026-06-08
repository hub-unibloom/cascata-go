package nexus

import (
	"context"
	"fmt"
	"sync"
)

// ForeachComponent executa a separação de um array em múltiplos pacotes downstream
// implementando também o controle de paralelismo (se necessário, na layer do worker).
type ForeachComponent struct {
	*BaseComponent
}

func NewForeachComponent(id string) *ForeachComponent {
	return &ForeachComponent{
		BaseComponent: NewBaseComponent(id, TypeForeach,
			[]PortDefinition{{Name: "in", DataType: "array", Required: true}},
			[]PortDefinition{
				{Name: "item", DataType: "any", Required: true},
				{Name: "error", DataType: "error", Required: false},
			},
		),
	}
}

func (c *ForeachComponent) Process(ctx context.Context, ip *InformationPacket, state *NexusState) (map[string][]*InformationPacket, error) {
	c.SetStatus(StatusProcessing)

	config := c.Config()
	settings := config.Settings

	fieldName, _ := settings["field"].(string)
	if fieldName == "" {
		fieldName = "items"
	}

	arrayData, ok := ip.Data[fieldName]
	if !ok {
		// Tenta no payload inteiro se for um array
		if payloadArray, isArray := ip.Data[fieldName].([]interface{}); isArray {
			arrayData = payloadArray
		} else {
			return EmitEmpty(), nil
		}
	}

	items, ok := arrayData.([]interface{})
	if !ok {
		// Se não for array, passa o item como único
		return EmitSingle("item", ip.CloneForForeach(0)), nil
	}

	maxItems := MaxForeachItems
	if max, ok := settings["max_items"].(float64); ok {
		maxItems = int(max)
	}

	if len(items) > maxItems {
		return c.handleError(ip, fmt.Errorf("nexus[foreach]: array length %d exceeds max limit %d", len(items), maxItems))
	}

	packets := make([]*InformationPacket, 0, len(items))

	for i, item := range items {
		itemIP := ip.CloneForForeach(i)
		itemIP.SourceNode = c.ID()
		itemIP.SourcePort = "item"

		// Prepara payload individual
		switch v := item.(type) {
		case map[string]interface{}:
			itemIP.Data = v
		default:
			itemIP.Data = map[string]interface{}{"value": v, "index": i}
		}

		packets = append(packets, itemIP)
	}

	c.SetStatus(StatusSuccess)
	return EmitMultiple("item", packets), nil
}

func (c *ForeachComponent) handleError(ip *InformationPacket, err error) (map[string][]*InformationPacket, error) {
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

// AggregatorComponent atua como barreira, aguardando pacotes irmãos
// gerados por Branching ou Foreach para combiná-los.
type AggregatorComponent struct {
	*BaseComponent
	mu      sync.Mutex
	buffer  []*InformationPacket
}

func NewAggregatorComponent(id string) *AggregatorComponent {
	return &AggregatorComponent{
		BaseComponent: NewBaseComponent(id, TypeAggregator,
			[]PortDefinition{{Name: "in", DataType: "any", Required: true}},
			[]PortDefinition{
				{Name: "out", DataType: "array", Required: true},
			},
		),
		buffer: make([]*InformationPacket, 0),
	}
}

func (c *AggregatorComponent) Process(ctx context.Context, ip *InformationPacket, state *NexusState) (map[string][]*InformationPacket, error) {
	c.SetStatus(StatusProcessing)

	// O comportamento real do Aggregator precisaria de suporte especial no 
	// Executor do Grafo (nexus_engine.go), já que ele deve bloquear até que 
	// todas as branches atinjam a porta.
	// Por agora, implementamos a acumulação simples baseada num contador se conhecido.
	
	c.mu.Lock()
	defer c.mu.Unlock()

	c.buffer = append(c.buffer, ip)

	// Em um sistema real, precisamos saber o total_expected do Foreach pai.
	// Vamos assumir que o NexusEngine resolve isso injetando "total_expected" no state ou ip
	
	c.SetStatus(StatusSuccess)
	
	// Simplificação: agrupa e emite
	outData := make([]interface{}, 0, len(c.buffer))
	for _, p := range c.buffer {
		outData = append(outData, p.Data)
	}

	outIP := ip.CloneWithData(map[string]interface{}{"aggregated": outData})
	return EmitSingle("out", outIP), nil
}
