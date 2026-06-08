package nexus

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/redis/go-redis/v9"
)

// QdrantComponent interage nativamente com o banco vetorial Qdrant.
// Inclui cache semântico via Dragonfly e implementa ToolCapable para Agentes Autônomos.
type QdrantComponent struct {
	*BaseComponent
	qdrantURL string
	qdrantKey string
	rdb       *redis.Client
	client    *http.Client
}

// NewQdrantComponent cria um novo Qdrant Node.
func NewQdrantComponent(id string, url, key string, rdb *redis.Client) *QdrantComponent {
	return &QdrantComponent{
		BaseComponent: NewBaseComponent(id, TypeQdrant,
			[]PortDefinition{{Name: "in", DataType: "object", Required: true}},
			[]PortDefinition{
				{Name: "out", DataType: "object", Required: true},
				{Name: "error", DataType: "error", Required: false},
			},
		),
		qdrantURL: url,
		qdrantKey: key,
		rdb:       rdb,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// Process executa as operações do Qdrant (search, upsert, delete).
func (c *QdrantComponent) Process(ctx context.Context, ip *InformationPacket, state *NexusState) (map[string][]*InformationPacket, error) {
	c.SetStatus(StatusProcessing)

	operation, ok := c.config.Settings["operation"].(string)
	if !ok {
		return c.handleError(ip, fmt.Errorf("nexus[qdrant]: 'operation' is required (search, upsert, delete)"))
	}

	collection, ok := c.config.Settings["collection"].(string)
	if !ok {
		// Fallback para nome do tenant se não especificado (isolamento padrão)
		collection = "tenant_" + state.GetSecurityContext().TenantID
	} else {
		collection, _ = state.InterpolateString(collection, ip.Data)
	}

	var result interface{}
	var err error

	switch operation {
	case "search":
		result, err = c.doSearch(ctx, collection, ip, state)
	case "upsert":
		result, err = c.doUpsert(ctx, collection, ip, state)
	case "delete":
		result, err = c.doDelete(ctx, collection, ip, state)
	default:
		return c.handleError(ip, fmt.Errorf("nexus[qdrant]: unknown operation '%s'", operation))
	}

	if err != nil {
		return c.handleError(ip, err)
	}

	c.SetStatus(StatusSuccess)
	outIp := ip.Clone()
	outIp.Data["qdrant_result"] = result

	return EmitSingle("out", outIp), nil
}

func (c *QdrantComponent) doSearch(ctx context.Context, collection string, ip *InformationPacket, state *NexusState) (interface{}, error) {
	// Pega o vetor de busca ou texto
	vectorRaw, _ := c.config.Settings["vector"]
	vectorData, _ := json.Marshal(vectorRaw)
	vectorStr, _ := state.InterpolateString(string(vectorData), ip.Data)

	limitRaw, ok := c.config.Settings["limit"].(float64)
	limit := 10
	if ok {
		limit = int(limitRaw)
	}

	// 1. Verificar Cache (Dragonfly)
	// Hasheia o vetor e parâmetros para criar a chave de cache
	cacheKey := c.generateCacheKey(collection, "search", vectorStr, limit)
	if c.rdb != nil {
		cached, err := c.rdb.Get(ctx, cacheKey).Result()
		if err == nil && cached != "" {
			var cachedRes interface{}
			json.Unmarshal([]byte(cached), &cachedRes)
			return map[string]interface{}{"cached": true, "data": cachedRes}, nil
		}
	}

	// 2. Monta Request para Qdrant
	endpoint := fmt.Sprintf("%s/collections/%s/points/search", c.qdrantURL, collection)
	reqBody := fmt.Sprintf(`{"vector": %s, "limit": %d, "with_payload": true}`, vectorStr, limit)

	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewBufferString(reqBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.qdrantKey != "" {
		req.Header.Set("api-key", c.qdrantKey)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("qdrant error (%d): %s", resp.StatusCode, string(body))
	}

	var jsonResp map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&jsonResp)
	resultData := jsonResp["result"]

	// 3. Salvar no Cache (Dragonfly)
	if c.rdb != nil {
		resBytes, _ := json.Marshal(resultData)
		c.rdb.Set(ctx, cacheKey, string(resBytes), 5*time.Minute)
	}

	return map[string]interface{}{"cached": false, "data": resultData}, nil
}

func (c *QdrantComponent) doUpsert(ctx context.Context, collection string, ip *InformationPacket, state *NexusState) (interface{}, error) {
	pointsRaw, _ := c.config.Settings["points"]
	pointsData, _ := json.Marshal(pointsRaw)
	pointsStr, _ := state.InterpolateString(string(pointsData), ip.Data)

	endpoint := fmt.Sprintf("%s/collections/%s/points?wait=true", c.qdrantURL, collection)
	reqBody := fmt.Sprintf(`{"points": %s}`, pointsStr)

	req, err := http.NewRequestWithContext(ctx, "PUT", endpoint, bytes.NewBufferString(reqBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.qdrantKey != "" {
		req.Header.Set("api-key", c.qdrantKey)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("qdrant error (%d): %s", resp.StatusCode, string(body))
	}

	var jsonResp map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&jsonResp)
	return jsonResp, nil
}

func (c *QdrantComponent) doDelete(ctx context.Context, collection string, ip *InformationPacket, state *NexusState) (interface{}, error) {
	filterRaw, _ := c.config.Settings["filter"]
	filterData, _ := json.Marshal(filterRaw)
	filterStr, _ := state.InterpolateString(string(filterData), ip.Data)

	endpoint := fmt.Sprintf("%s/collections/%s/points/delete?wait=true", c.qdrantURL, collection)
	reqBody := fmt.Sprintf(`{"filter": %s}`, filterStr)

	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewBufferString(reqBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.qdrantKey != "" {
		req.Header.Set("api-key", c.qdrantKey)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("qdrant error (%d): %s", resp.StatusCode, string(body))
	}

	var jsonResp map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&jsonResp)
	return jsonResp, nil
}

func (c *QdrantComponent) generateCacheKey(collection, op, payload string, limit int) string {
	hash := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%d", payload, op, limit)))
	return fmt.Sprintf("nexus:qdrant:%s:%s", collection, hex.EncodeToString(hash[:]))
}

// ToolSchema implementa ToolCapable, permitindo que este nó seja usado
// como ferramenta por Agentes de IA Autônomos.
func (c *QdrantComponent) ToolSchema() ToolSchema {
	// A descrição pode ser injetada via config, caso o owner tenha personalizado
	desc := "Busca semântica avançada e gestão de memória em banco de dados vetorial."
	if customDesc, ok := c.config.Settings["tool_description"].(string); ok && customDesc != "" {
		desc = customDesc
	}

	return ToolSchema{
		Name:        "qdrant_vector_db",
		Description: desc,
		Category:    "database",
		Tags:        []string{"vector", "semantic", "search", "memory"},
		Parameters: map[string]ParamSchema{
			"operation": {
				Type:        "string",
				Description: "Operação a ser executada: 'search', 'upsert' ou 'delete'",
				Required:    true,
				Enum:        []string{"search", "upsert", "delete"},
			},
			"vector": {
				Type:        "array",
				Description: "Array de floats (embeddings) para a busca. Obrigatório para 'search'.",
				Required:    false,
			},
			"limit": {
				Type:        "integer",
				Description: "Número máximo de resultados para a busca.",
				Required:    false,
				Default:     5,
			},
		},
	}
}

func (c *QdrantComponent) handleError(ip *InformationPacket, err error) (map[string][]*InformationPacket, error) {
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
