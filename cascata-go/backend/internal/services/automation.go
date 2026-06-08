package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"cascata-backend/internal/utils"
)

/**
 * CASCATA AUTOMATIONS ENGINE (Go Port)
 * High-performance node-based logic orchestrator.
 */

type AutomationNodeType string

const (
	NodeTrigger   AutomationNodeType = "trigger"
	NodeAction    AutomationNodeType = "action"
	NodeLogic     AutomationNodeType = "logic"
	NodeCondition AutomationNodeType = "condition"
	NodeResponse  AutomationNodeType = "response"
	NodeQuery     AutomationNodeType = "query"
	NodeHTTP      AutomationNodeType = "http"
	NodeTransform AutomationNodeType = "transform"
	NodeData      AutomationNodeType = "data"
	NodeRPC       AutomationNodeType = "rpc"
	NodeConvert   AutomationNodeType = "convert"
	NodeMath      AutomationNodeType = "math"
)

type AutomationNode struct {
	ID        string                 `json:"id"`
	Type      AutomationNodeType     `json:"type"`
	Config    map[string]interface{} `json:"config"`
	Next      interface{}            `json:"next"` // Can be []string or map[string]string
	TimeoutMs int                    `json:"timeout_ms,omitempty"` // Timeout individual do nó (padrão: 3000ms)
}

// Automation representa uma automação completa do sistema
type Automation struct {
	ID              string           `json:"id"`
	ProjectSlug     string           `json:"project_slug"`
	Name            string           `json:"name"`
	Description     string           `json:"description"`
	IsActive        bool             `json:"is_active"`
	TriggerType     string           `json:"trigger_type"`
	TriggerConfig   json.RawMessage  `json:"trigger_config"`
	Nodes           []AutomationNode `json:"nodes"`
	GlobalTimeoutMs int              `json:"global_timeout_ms,omitempty"` // Timeout global do fluxo (padrão: 3000ms)
	// Configurações de Queue (por automação - Worner decide!)
	QueueRetries    int              `json:"queue_retries,omitempty"`     // Máximo de tentativas (padrão: 3)
	QueueRetryDelay int              `json:"queue_retry_delay,omitempty"`   // Delay base entre retries em ms (padrão: 1000ms)
	QueuePriority   int              `json:"queue_priority,omitempty"`      // Prioridade: 1=high, 5=normal, 10=low (padrão: 5)
	CreatedAt       time.Time        `json:"created_at"`
	UpdatedAt       time.Time        `json:"updated_at"`
}

// AutomationContext representa o contexto de execução de uma automação
type AutomationContext struct {
	Vars        map[string]interface{}
	Payload     interface{}
	ProjectSlug string
	JWTSecret   string
	ProjectPool *pgxpool.Pool
	UserRole    string
	JWTClaims   map[string]interface{}
	DryRun      bool
	CryptoSvc   *CryptoService
}

var forbiddenSqlPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i);\s*-{2,}`),
	regexp.MustCompile(`(?i)COPY\s+`),
	regexp.MustCompile(`(?i)pg_read_file\s*\(`),
	regexp.MustCompile(`(?i)pg_write_file\s*\(`),
	regexp.MustCompile(`(?i)pg_ls_dir\s*\(`),
	regexp.MustCompile(`(?i)pg_terminate_backend\s*\(`),
	regexp.MustCompile(`(?i)pg_cancel_backend\s*\(`),
	regexp.MustCompile(`(?i)pg_reload_conf\s*\(`),
	regexp.MustCompile(`(?i)ALTER\s+SYSTEM\s+`),
	regexp.MustCompile(`(?i)CREATE\s+OR\s+REPLACE\s+FUNCTION`),
	regexp.MustCompile(`(?i)\bDO\s+\$\$`),
	regexp.MustCompile(`(?i)PERFORM\s+dblink`),
}

const (
	AutomationSqlTimeout = 8 * time.Second
	CacheTTL             = 5 * time.Minute
)

type cachedAutomations struct {
	automations []map[string]interface{}
	loadedAt    time.Time
}

var (
	interceptorCache = make(map[string]cachedAutomations)
	cacheMu          sync.RWMutex
)

// ============================================================================
// AUTOMATION DETAILED LOGGING (n8n-style execution tracking)
// ============================================================================

type AutomationLogLevel string

const (
	LogLevelDebug AutomationLogLevel = "debug"
	LogLevelInfo  AutomationLogLevel = "info"
	LogLevelWarn  AutomationLogLevel = "warn"
	LogLevelError AutomationLogLevel = "error"
)

type AutomationStepLog struct {
	StepID        string                 `json:"step_id"`
	ExecutionID   string                 `json:"execution_id"`
	AutomationID  string                 `json:"automation_id"`
	ProjectSlug   string                 `json:"project_slug"`
	NodeID        string                 `json:"node_id"`
	NodeType      string                 `json:"node_type"`
	NodeName      string                 `json:"node_name"`
	Level         AutomationLogLevel     `json:"level"`
	Message       string                 `json:"message"`
	InputData     interface{}            `json:"input_data,omitempty"`
	OutputData    interface{}            `json:"output_data,omitempty"`
	ErrorDetails  string                 `json:"error_details,omitempty"`
	DurationMs    int64                  `json:"duration_ms"`
	Timestamp     time.Time              `json:"timestamp"`
	Metadata      map[string]interface{} `json:"metadata,omitempty"`
}

type AutomationExecutionContext struct {
	ExecutionID    string
	AutomationID   string
	ProjectSlug    string
	StartTime      time.Time
	StepCounter    int
	LogChannel     chan AutomationStepLog
	done           chan struct{}
}

var (
	automationLogPool = sync.Pool{
		New: func() interface{} {
			return make(chan AutomationStepLog, 1000)
		},
	}
	executionContexts = make(map[string]*AutomationExecutionContext)
	executionMu       sync.RWMutex
)

type AutomationService struct {
	CryptoSvc *CryptoService
}

func (s *AutomationService) InvalidateCache(projectSlug string) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	delete(interceptorCache, projectSlug)
}

func (s *AutomationService) GetActiveInterceptors(ctx context.Context, projectSlug string) []map[string]interface{} {
	cacheMu.RLock()
	cached, exists := interceptorCache[projectSlug]
	cacheMu.RUnlock()

	if exists && time.Since(cached.loadedAt) < CacheTTL {
		return cached.automations
	}

	rows, err := SystemPool.Query(ctx,
		`SELECT id, nodes, trigger_config
		 FROM system.automations
		 WHERE project_slug = $1
		 AND is_active = true
		 AND trigger_type = 'API_INTERCEPT'`,
		projectSlug)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var automations []map[string]interface{}
	for rows.Next() {
		var id string
		var nodes, triggerConfig json.RawMessage
		if err := rows.Scan(&id, &nodes, &triggerConfig); err == nil {
			var n []interface{}
			var tc map[string]interface{}
			json.Unmarshal(nodes, &n)
			json.Unmarshal(triggerConfig, &tc)
			automations = append(automations, map[string]interface{}{
				"id":             id,
				"nodes":          n,
				"trigger_config": tc,
			})
		}
	}

	cacheMu.Lock()
	interceptorCache[projectSlug] = cachedAutomations{automations: automations, loadedAt: time.Now()}
	cacheMu.Unlock()

	return automations
}

func (s *AutomationService) InterceptResponse(ctx context.Context, projectSlug string, tableName string, eventType string, initialPayload interface{}, automationCtx *AutomationContext) interface{} {
	interceptors := s.GetActiveInterceptors(ctx, projectSlug)
	if len(interceptors) == 0 {
		return initialPayload
	}

	currentPayload := initialPayload
	for _, automation := range interceptors {
		tc := automation["trigger_config"].(map[string]interface{})
		tbl := tc["table"]
		evt := tc["event"]

		if (tbl == tableName || tbl == "*") && (evt == eventType || evt == "*") {
			// Run workflow
			nodesRaw, _ := json.Marshal(automation["nodes"])
			var nodes []AutomationNode
			json.Unmarshal(nodesRaw, &nodes)

			automationCtx.Vars = map[string]interface{}{
				"trigger": map[string]interface{}{"data": currentPayload},
				"$input":  currentPayload,
			}

			// Evaluate trigger filters if present
			var triggerNode *AutomationNode
			for i := range nodes {
				if nodes[i].Type == NodeTrigger {
					triggerNode = &nodes[i]
					break
				}
			}

			if triggerNode != nil && triggerNode.Config["conditions"] != nil {
				if !s.evaluateLogic(triggerNode, automationCtx) {
					continue
				}
			}

			res, err := s.RunAutomationLogged(ctx, automation["id"].(string), projectSlug, nodes, currentPayload, automationCtx, 0)
			if err == nil {
				currentPayload = res
			}
		}
	}

	return currentPayload
}

func (s *AutomationService) DispatchAsyncTrigger(ctx context.Context, automationId string, projectSlug string, nodes []AutomationNode, triggerPayload interface{}, automationCtx *AutomationContext) {
	go func() {
		// Isolated context for async run
		bgCtx := context.Background()
		_, err := s.RunAutomationLogged(bgCtx, automationId, projectSlug, nodes, triggerPayload, automationCtx, 0)
		if err != nil {
			log.Printf("[AutomationEngine:Async] Error in %s: %v", automationId, err)
		}
	}()
}

func (s *AutomationService) RunAutomationLogged(ctx context.Context, automationId string, projectSlug string, nodes []AutomationNode, triggerPayload interface{}, automationCtx *AutomationContext, globalTimeoutMs int) (interface{}, error) {
	startedAt := time.Now()
	var finalOutput interface{}
	var errorMessage string
	status := "success"

	// Aplicar timeout global do fluxo (padrão: 3000ms)
	if globalTimeoutMs <= 0 {
		globalTimeoutMs = 3000
	}
	
	// Criar contexto com timeout global
	ctx, cancel := context.WithTimeout(ctx, time.Duration(globalTimeoutMs)*time.Millisecond)
	defer cancel()

	output, err := s.ExecuteWorkflow(ctx, nodes, triggerPayload, automationCtx)
	if err != nil {
		// Verificar se é timeout
		if ctx.Err() == context.DeadlineExceeded {
			status = "timeout"
			errorMessage = "workflow execution timeout"
		} else {
			status = "failed"
			errorMessage = err.Error()
		}
		finalOutput = nil
	} else {
		finalOutput = output
	}

	executionTime := time.Since(startedAt).Milliseconds()

	// Fire-and-forget log write
	go func() {
		payloadJSON, _ := json.Marshal(triggerPayload)
		outputJSON, _ := json.Marshal(finalOutput)
		_, _ = SystemPool.Exec(context.Background(),
			`INSERT INTO system.automation_runs
				(automation_id, project_slug, status, execution_time_ms, trigger_payload, final_output, error_message)
			 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			automationId, projectSlug, status, executionTime, string(payloadJSON), string(outputJSON), errorMessage)
	}()

	return finalOutput, err
}

func (s *AutomationService) ExecuteWorkflow(ctx context.Context, nodes []AutomationNode, payload interface{}, automationCtx *AutomationContext) (interface{}, error) {
	nodeMap := make(map[string]AutomationNode)
	var startNode *AutomationNode
	for i := range nodes {
		nodeMap[nodes[i].ID] = nodes[i]
		if nodes[i].Type == NodeTrigger {
			startNode = &nodes[i]
		}
	}

	if startNode == nil {
		return payload, nil
	}

	if automationCtx.Vars == nil {
		automationCtx.Vars = make(map[string]interface{})
	}
	automationCtx.Vars["$input"] = payload
	automationCtx.Vars["trigger"] = map[string]interface{}{"data": payload}

	// Iniciar contexto de execução com logging
	automationID := ""
	projectSlug := automationCtx.ProjectSlug
	if id, ok := automationCtx.Vars["__automation_id"].(string); ok {
		automationID = id
	}
	execCtx := StartExecution(automationID, projectSlug)
	defer func() {
		status := "success"
		if ctx.Err() != nil {
			status = "timeout"
		}
		execCtx.FinishExecution(status, automationCtx.Vars["$output"])
	}()

	// Log do trigger
	execCtx.LogStep(AutomationStepLog{
		StepID:     "trigger",
		NodeType:   "trigger",
		Level:      LogLevelInfo,
		Message:    "Trigger received payload",
		InputData:  payload,
		Timestamp:  time.Now(),
	})

	// Obter deadline do contexto global para respeitar timeout
	globalDeadline, hasDeadline := ctx.Deadline()

	currentNode := startNode
	steps := 0
	for currentNode != nil && steps < 100 {
		steps++

		// Preparar input para logging
		nodeInput := automationCtx.Vars["$input"]
		if currentNode.Type != NodeTrigger {
			nodeInput = automationCtx.Vars[currentNode.ID]
		}
		nodeName, _ := currentNode.Config["name"].(string)
		if nodeName == "" {
			nodeName = currentNode.ID
		}

		// Log início do nó
		stepID := execCtx.LogNodeStart(currentNode.ID, string(currentNode.Type), nodeName, nodeInput)
		nodeStartTime := time.Now()

		// Criar contexto com timeout específico do nó (se configurado)
		nodeCtx := ctx
		if currentNode.TimeoutMs > 0 {
			nodeTimeout := time.Duration(currentNode.TimeoutMs) * time.Millisecond
			
			// Se tem deadline global e o timeout do nó é maior que o tempo restante,
			// usa o tempo restante do global
			if hasDeadline {
				remaining := time.Until(globalDeadline)
				if nodeTimeout > remaining && remaining > 0 {
					nodeTimeout = remaining
				}
			}
			
			var cancel context.CancelFunc
			nodeCtx, cancel = context.WithTimeout(ctx, nodeTimeout)
			
			result, err := s.ProcessNode(nodeCtx, currentNode, automationCtx)
			cancel() // Cancelar contexto do nó imediatamente após execução
			
			nodeDuration := time.Since(nodeStartTime).Milliseconds()

			if err != nil {
				if nodeCtx.Err() == context.DeadlineExceeded {
					log.Printf("[AutomationEngine] Node %s timeout after %v", currentNode.ID, nodeTimeout)
					automationCtx.Vars[currentNode.ID] = map[string]interface{}{"error": "node execution timeout"}
					execCtx.LogNodeError(stepID, currentNode.ID, string(currentNode.Type), fmt.Errorf("timeout after %v", nodeTimeout), nodeInput)
				} else {
					log.Printf("[AutomationEngine] Node %s failed: %v", currentNode.ID, err)
					automationCtx.Vars[currentNode.ID] = map[string]interface{}{"error": err.Error()}
					execCtx.LogNodeError(stepID, currentNode.ID, string(currentNode.Type), err, nodeInput)
				}
				
				// Check for error path
				if next, ok := currentNode.Next.(map[string]interface{}); ok {
					if errPath, ok := next["error"].(string); ok {
						nextMatch := nodeMap[errPath]
						currentNode = &nextMatch
						continue
					}
				}
				return nil, err
			}
			
			automationCtx.Vars[currentNode.ID] = map[string]interface{}{"data": result}
			
			// Log operação de dados se aplicável
			if currentNode.Type == NodeData {
				if op, ok := currentNode.Config["operation"].(string); ok {
					if tbl, ok := currentNode.Config["table"].(string); ok {
						filters, _ := currentNode.Config["filters"].([]interface{})
						execCtx.LogDataOperation(currentNode.ID, op, tbl, map[string]interface{}{"filters": filters}, 1)
					}
				}
			}
			
			// Log conclusão do nó
			nextNodes := []string{}
			if currentNode.Type == NodeLogic || currentNode.Type == NodeCondition {
				if next, ok := currentNode.Next.(map[string]interface{}); ok {
					if val, hasData := automationCtx.Vars[currentNode.ID].(map[string]interface{})["data"]; hasData {
						if boolVal, isBool := val.(bool); isBool {
							if boolVal {
								if n, ok := next["true"].(string); ok {
									nextNodes = append(nextNodes, n)
								}
							} else {
								if n, ok := next["false"].(string); ok {
									nextNodes = append(nextNodes, n)
								}
							}
						}
					}
				}
			} else {
				if next, ok := currentNode.Next.(map[string]interface{}); ok {
					if n, ok := next["out"].(string); ok {
						nextNodes = append(nextNodes, n)
					}
					if n, ok := next["next"].(string); ok {
						nextNodes = append(nextNodes, n)
					}
				} else if nextArr, ok := currentNode.Next.([]interface{}); ok && len(nextArr) > 0 {
					for _, n := range nextArr {
						if str, ok := n.(string); ok {
							nextNodes = append(nextNodes, str)
						}
					}
				}
			}
			execCtx.LogNodeComplete(stepID, currentNode.ID, string(currentNode.Type), result, nodeDuration, nextNodes)
			
		} else {
			// Sem timeout específico do nó, usa o contexto global diretamente
			result, err := s.ProcessNode(nodeCtx, currentNode, automationCtx)
			
			nodeDuration := time.Since(nodeStartTime).Milliseconds()

			if err != nil {
				log.Printf("[AutomationEngine] Node %s failed: %v", currentNode.ID, err)
				automationCtx.Vars[currentNode.ID] = map[string]interface{}{"error": err.Error()}
				execCtx.LogNodeError(stepID, currentNode.ID, string(currentNode.Type), err, nodeInput)
				
				// Check for error path
				if next, ok := currentNode.Next.(map[string]interface{}); ok {
					if errPath, ok := next["error"].(string); ok {
						nextMatch := nodeMap[errPath]
						currentNode = &nextMatch
						continue
					}
				}
				return nil, err
			}
			
			automationCtx.Vars[currentNode.ID] = map[string]interface{}{"data": result}
			
			// Log operação de dados se aplicável
			if currentNode.Type == NodeData {
				if op, ok := currentNode.Config["operation"].(string); ok {
					if tbl, ok := currentNode.Config["table"].(string); ok {
						filters, _ := currentNode.Config["filters"].([]interface{})
						execCtx.LogDataOperation(currentNode.ID, op, tbl, map[string]interface{}{"filters": filters}, 1)
					}
				}
			}
			
			// Log conclusão do nó
			nextNodes := []string{}
			if currentNode.Type == NodeLogic || currentNode.Type == NodeCondition {
				if next, ok := currentNode.Next.(map[string]interface{}); ok {
					if val, hasData := automationCtx.Vars[currentNode.ID].(map[string]interface{})["data"]; hasData {
						if boolVal, isBool := val.(bool); isBool {
							if boolVal {
								if n, ok := next["true"].(string); ok {
									nextNodes = append(nextNodes, n)
								}
							} else {
								if n, ok := next["false"].(string); ok {
									nextNodes = append(nextNodes, n)
								}
							}
						}
					}
				}
			} else {
				if next, ok := currentNode.Next.(map[string]interface{}); ok {
					if n, ok := next["out"].(string); ok {
						nextNodes = append(nextNodes, n)
					}
					if n, ok := next["next"].(string); ok {
						nextNodes = append(nextNodes, n)
					}
				} else if nextArr, ok := currentNode.Next.([]interface{}); ok && len(nextArr) > 0 {
					for _, n := range nextArr {
						if str, ok := n.(string); ok {
							nextNodes = append(nextNodes, str)
						}
					}
				}
			}
			execCtx.LogNodeComplete(stepID, currentNode.ID, string(currentNode.Type), result, nodeDuration, nextNodes)
		}

		if currentNode.Type == NodeTrigger && currentNode.Config["conditions"] != nil {
			if dataVal, ok := automationCtx.Vars[currentNode.ID].(map[string]interface{})["data"]; ok {
				if boolVal, isBool := dataVal.(bool); isBool && !boolVal {
					return payload, nil
				}
			}
		}

		if currentNode.Type == NodeResponse {
			return automationCtx.Vars[currentNode.ID].(map[string]interface{})["data"], nil
		}

		var nextId string
		if currentNode.Type == NodeLogic || currentNode.Type == NodeCondition {
			if next, ok := currentNode.Next.(map[string]interface{}); ok {
				if dataVal, hasData := automationCtx.Vars[currentNode.ID].(map[string]interface{})["data"]; hasData {
					if boolVal, isBool := dataVal.(bool); isBool && boolVal {
						nextId, _ = next["true"].(string)
					} else {
						nextId, _ = next["false"].(string)
					}
				}
			}
		} else {
			if next, ok := currentNode.Next.(map[string]interface{}); ok {
				if currentNode.Type == NodeHTTP {
					if dataVal, hasData := automationCtx.Vars[currentNode.ID].(map[string]interface{})["data"]; hasData {
						if resMap, ok := dataVal.(map[string]interface{}); ok && resMap["__error"] != nil {
							nextId, _ = next["error"].(string)
						}
					}
				}
				if nextId == "" {
					nextId, _ = next["out"].(string)
					if nextId == "" {
						nextId, _ = next["next"].(string)
					}
				}
			} else if nextArr, ok := currentNode.Next.([]interface{}); ok && len(nextArr) > 0 {
				nextId, _ = nextArr[0].(string)
			}
		}

		if nextId != "" {
			match := nodeMap[nextId]
			currentNode = &match
		} else {
			currentNode = nil
		}
	}

	if out, exists := automationCtx.Vars["$output"]; exists {
		return out, nil
	}
	return payload, nil
}

func (s *AutomationService) ProcessNode(ctx context.Context, node *AutomationNode, automationCtx *AutomationContext) (interface{}, error) {
	config := node.Config
	if config == nil {
		return nil, nil
	}

	switch node.Type {
	case NodeTrigger:
		if config["conditions"] != nil {
			return s.evaluateLogic(node, automationCtx), nil
		}
		return automationCtx.Vars["$input"], nil

	case NodeTransform:
		body := config["body"]
		if body == nil {
			body = config["template"]
		}
		transformed := s.resolveObject(body, automationCtx.Vars)
		automationCtx.Vars["$output"] = transformed
		return transformed, nil

	case NodeQuery:
		return s.executeSecureSqlNode(ctx, node, automationCtx)

	case NodeHTTP:
		return s.executeHttpNode(ctx, node, automationCtx)

	case NodeConvert:
		valRaw := config["value"]
		toType, _ := config["toType"].(string)
		source := s.resolveVariables(fmt.Sprintf("%v", valRaw), automationCtx.Vars)

		switch toType {
		case "int":
			var out int
			fmt.Sscanf(source, "%d", &out)
			return out, nil
		case "float":
			var out float64
			fmt.Sscanf(source, "%f", &out)
			return out, nil
		case "string":
			return source, nil
		case "boolean":
			return source == "true" || source == "1", nil
		case "json":
			var out interface{}
			json.Unmarshal([]byte(source), &out)
			return out, nil
		}
		return source, nil

	case NodeLogic, NodeCondition:
		return s.evaluateLogic(node, automationCtx), nil

	case NodeResponse:
		return s.resolveObject(config["body"], automationCtx.Vars), nil

	case NodeData:
		return s.executeDataNode(ctx, node, automationCtx)

	case NodeRPC:
		return s.executeRpcNode(ctx, node, automationCtx)

	default:
		return nil, nil
	}
}

func (s *AutomationService) executeHttpNode(ctx context.Context, node *AutomationNode, automationCtx *AutomationContext) (interface{}, error) {
	urlRaw, _ := node.Config["url"].(string)
	targetUrl := s.resolveVariables(urlRaw, automationCtx.Vars)
	if targetUrl == "" {
		return nil, fmt.Errorf("HTTP node requires a URL")
	}

	method, _ := node.Config["method"].(string)
	if method == "" { method = "POST" }

	// Params
	if params, ok := node.Config["query_params"].(map[string]interface{}); ok {
		resolvedParams := s.resolveObject(params, automationCtx.Vars).(map[string]interface{})
		u, _ := url.Parse(targetUrl)
		q := u.Query()
		for k, v := range resolvedParams {
			q.Set(k, fmt.Sprintf("%v", v))
		}
		u.RawQuery = q.Encode()
		targetUrl = u.String()
	}

	// Body
	var bodyReader io.Reader
	if method != "GET" && node.Config["body"] != nil {
		resolvedBody := s.resolveObject(node.Config["body"], automationCtx.Vars)
		bodyBytes, _ := json.Marshal(resolvedBody)
		bodyReader = bytes.NewReader(bodyBytes)
	}

	req, _ := http.NewRequestWithContext(ctx, method, targetUrl, bodyReader)
	req.Header.Set("Content-Type", "application/json")

	// Auth
	authMode, _ := node.Config["auth"].(string)
	if authMode == "bearer" {
		tokenRaw, _ := node.Config["auth_token"].(string)
		var token string
		if strings.HasPrefix(tokenRaw, "vault://") {
			v, err := s.resolveVaultSecret(ctx, automationCtx.ProjectSlug, strings.TrimPrefix(tokenRaw, "vault://"))
			if err != nil {
				return map[string]interface{}{"__error": true, "message": err.Error()}, nil
			}
			token = v
		} else {
			token = s.resolveVariables(tokenRaw, automationCtx.Vars)
		}
		req.Header.Set("Authorization", "Bearer "+token)
	}

	timeout := 15 * time.Second
	if t, ok := node.Config["timeout"].(float64); ok {
		timeout = time.Duration(t) * time.Millisecond
	}

	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return map[string]interface{}{"__error": true, "message": err.Error()}, nil
	}
	defer resp.Body.Close()

	var result interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	return result, nil
}

func (s *AutomationService) executeSecureSqlNode(ctx context.Context, node *AutomationNode, automationCtx *AutomationContext) (interface{}, error) {
	sql, _ := node.Config["sql"].(string)
	if sql == "" { return nil, nil }

	for _, p := range forbiddenSqlPatterns {
		if p.MatchString(sql) {
			return nil, fmt.Errorf("Security Violation: SQL pattern blocked")
		}
	}

	paramsRaw, _ := node.Config["params"].([]interface{})
	resolvedParams := make([]interface{}, len(paramsRaw))
	for i, p := range paramsRaw {
		resolvedParams[i] = s.getVarSync(fmt.Sprintf("%v", p), automationCtx.Vars)
	}

	conn, err := automationCtx.ProjectPool.Acquire(ctx)
	if err != nil { return nil, err }
	defer conn.Release()

	// Security Setup
	role := automationCtx.UserRole
	if role == "" { role = "authenticated" }
	claims := automationCtx.JWTClaims

	tx, err := conn.Begin(ctx)
	if err != nil { return nil, err }
	defer tx.Rollback(ctx)

	setupSql := fmt.Sprintf(`
		SET LOCAL ROLE %s;
		SET LOCAL statement_timeout = '%dms';
		SET LOCAL "request.jwt.claim.sub" = '%v';
	`, role, AutomationSqlTimeout.Milliseconds(), claims["sub"])
	
	_, err = tx.Exec(ctx, setupSql)
	if err != nil { return nil, err }

	rows, err := tx.Query(ctx, sql, resolvedParams...)
	if err != nil { return nil, err }
	defer rows.Close()

	var results []map[string]interface{}
	// Iterate rows... (simplified)
	for rows.Next() {
		values, _ := rows.Values()
		row := make(map[string]interface{})
		for i, col := range rows.FieldDescriptions() {
			row[string(col.Name)] = utils.PurifyPgxValue(values[i])
		}
		results = append(results, row)
	}

	if !automationCtx.DryRun {
		tx.Commit(ctx)
	}

	return results, nil
}

func (s *AutomationService) evaluateLogic(node *AutomationNode, context *AutomationContext) bool {
	conditionsRaw, _ := node.Config["conditions"].([]interface{})
	match, _ := node.Config["match"].(string)
	if match == "" { match = "all" }

	results := []bool{}
	for _, cRaw := range conditionsRaw {
		c := cRaw.(map[string]interface{})
		left := s.getVarSync(fmt.Sprintf("%v", c["left"]), context.Vars)
		right := c["right"]
		op, _ := c["op"].(string)

		res := false
		lStr := fmt.Sprintf("%v", left)
		rStr := fmt.Sprintf("%v", right)

		switch op {
		case "eq": res = lStr == rStr
		case "neq": res = lStr != rStr
		case "contains": res = strings.Contains(lStr, rStr)
		case "starts_with": res = strings.HasPrefix(lStr, rStr)
		case "is_empty": res = left == nil || lStr == ""
		}
		results = append(results, res)
	}

	if match == "all" {
		for _, r := range results { if !r { return false } }
		return true
	} else {
		for _, r := range results { if r { return true } }
		return false
	}
}

func (s *AutomationService) resolveVariables(template string, vars map[string]interface{}) string {
	re := regexp.MustCompile(`\{\{([^}]+)\}\}`)
	return re.ReplaceAllStringFunc(template, func(match string) string {
		path := strings.Trim(match, "{} ")
		val := s.getVarSync(path, vars)
		if val == nil { return "" }
		return fmt.Sprintf("%v", val)
	})
}

func (s *AutomationService) resolveObject(source interface{}, vars map[string]interface{}) interface{} {
	switch v := source.(type) {
	case string:
		return s.resolveVariables(v, vars)
	case []interface{}:
		res := make([]interface{}, len(v))
		for i, val := range v {
			res[i] = s.resolveObject(val, vars)
		}
		return res
	case map[string]interface{}:
		res := make(map[string]interface{})
		for k, val := range v {
			res[k] = s.resolveObject(val, vars)
		}
		return res
	}
	return source
}

func (s *AutomationService) getVarSync(path string, vars map[string]interface{}) interface{} {
	parts := strings.Split(path, ".")
	var current interface{} = vars
	for _, part := range parts {
		if m, ok := current.(map[string]interface{}); ok {
			current = m[part]
		} else {
			return nil
		}
	}
	return current
}

func (s *AutomationService) resolveVaultSecret(ctx context.Context, projectSlug, identifier string) (string, error) {
	vaultSvc := NewVaultService(s.CryptoSvc)
	returned, _, err := vaultSvc.Resolve(ctx, projectSlug, identifier, VaultPurposeAutomation)
	return returned, err
}

func (s *AutomationService) executeDataNode(ctx context.Context, node *AutomationNode, automationCtx *AutomationContext) (interface{}, error) {
	config := node.Config
	if config == nil {
		return nil, fmt.Errorf("data node missing config")
	}

	operation, _ := config["operation"].(string)
	table, _ := config["table"].(string)
	if table == "" || operation == "" {
		return nil, fmt.Errorf("data node requires table and operation")
	}

	// Get connection pool
	pool := automationCtx.ProjectPool
	if pool == nil {
		return nil, fmt.Errorf("no database pool available")
	}

	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to acquire connection: %w", err)
	}
	defer conn.Release()

	tx, err := conn.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	// Setup RLS
	role := automationCtx.UserRole
	if role == "" {
		role = "authenticated"
	}
	claims := automationCtx.JWTClaims
	if claims == nil {
		claims = make(map[string]interface{})
	}

	quoteLocal := func(s interface{}) string {
		if s == nil {
			return "''"
		}
		str := fmt.Sprintf("%v", s)
		return "'" + strings.ReplaceAll(str, "'", "''") + "'"
	}

	setupSQL := fmt.Sprintf(`
		SET LOCAL ROLE %s;
		SET LOCAL statement_timeout = '8000ms';
		SET LOCAL "request.jwt.claim.sub" = %s;
		SET LOCAL "request.jwt.claim.role" = %s;
	`, role, quoteLocal(claims["sub"]), quoteLocal(claims["role"]))

	if _, err := tx.Exec(ctx, setupSQL); err != nil {
		return nil, fmt.Errorf("failed to setup RLS: %w", err)
	}

	// Execute operation
	var result interface{}
	switch strings.ToLower(operation) {
	case "select":
		result, err = s.executeDataSelect(ctx, tx, table, config, automationCtx)
	case "insert":
		result, err = s.executeDataInsert(ctx, tx, table, config, automationCtx)
	case "update":
		result, err = s.executeDataUpdate(ctx, tx, table, config, automationCtx)
	case "delete":
		result, err = s.executeDataDelete(ctx, tx, table, config, automationCtx)
	default:
		return nil, fmt.Errorf("unknown operation: %s", operation)
	}

	if err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit failed: %w", err)
	}

	return result, nil
}

func (s *AutomationService) executeDataSelect(ctx context.Context, tx pgx.Tx, table string, config map[string]interface{}, automationCtx *AutomationContext) (interface{}, error) {
	query := fmt.Sprintf("SELECT * FROM %s", sanitizeTableName(table))
	
	// WHERE from filters
	if filters, ok := config["filters"].([]interface{}); ok && len(filters) > 0 {
		whereClauses := []string{}
		params := []interface{}{}
		for _, f := range filters {
			if fMap, ok := f.(map[string]interface{}); ok {
				col, _ := fMap["column"].(string)
				op, _ := fMap["op"].(string)
				valRef, _ := fMap["value"].(string)
				if col != "" && op != "" {
					if op == "eq" {
						op = "="
					}
					whereClauses = append(whereClauses, fmt.Sprintf("%s %s $%d", sanitizeTableName(col), op, len(params)+1))
					params = append(params, s.resolveVar(valRef, automationCtx.Vars))
				}
			}
		}
		if len(whereClauses) > 0 {
			query += " WHERE " + strings.Join(whereClauses, " AND ")
		}
	}

	// ORDER BY
	if orderBy, ok := config["order_by"].(string); ok && orderBy != "" {
		query += " ORDER BY " + sanitizeTableName(orderBy)
	}

	// LIMIT
	if limit, ok := config["limit"].(float64); ok && limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", int(limit))
	}

	rows, err := tx.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("select failed: %w", err)
	}
	defer rows.Close()

	return s.scanDataRows(rows)
}

func (s *AutomationService) executeDataInsert(ctx context.Context, tx pgx.Tx, table string, config map[string]interface{}, automationCtx *AutomationContext) (interface{}, error) {
	bodyRaw := config["body"]
	if bodyRaw == nil {
		return nil, fmt.Errorf("insert requires body")
	}

	// Resolve template variables
	body := s.resolveObject(bodyRaw, automationCtx.Vars)
	bodyMap, ok := body.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("insert body must be an object")
	}

	keys := make([]string, 0, len(bodyMap))
	values := make([]interface{}, 0, len(bodyMap))
	for k, v := range bodyMap {
		keys = append(keys, sanitizeTableName(k))
		values = append(values, v)
	}

	placeholders := make([]string, len(values))
	for i := range values {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
	}

	query := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s) RETURNING *",
		sanitizeTableName(table),
		strings.Join(keys, ", "),
		strings.Join(placeholders, ", "))

	rows, err := tx.Query(ctx, query, values...)
	if err != nil {
		return nil, fmt.Errorf("insert failed: %w", err)
	}
	defer rows.Close()

	return s.scanDataRows(rows)
}

func (s *AutomationService) executeDataUpdate(ctx context.Context, tx pgx.Tx, table string, config map[string]interface{}, automationCtx *AutomationContext) (interface{}, error) {
	bodyRaw := config["body"]
	if bodyRaw == nil {
		return nil, fmt.Errorf("update requires body")
	}

	body := s.resolveObject(bodyRaw, automationCtx.Vars)
	bodyMap, ok := body.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("update body must be an object")
	}

	setClauses := []string{}
	params := []interface{}{}
	for k, v := range bodyMap {
		setClauses = append(setClauses, fmt.Sprintf("%s = $%d", sanitizeTableName(k), len(params)+1))
		params = append(params, v)
	}

	query := fmt.Sprintf("UPDATE %s SET %s", sanitizeTableName(table), strings.Join(setClauses, ", "))

	// WHERE from filters
	if filters, ok := config["filters"].([]interface{}); ok && len(filters) > 0 {
		whereClauses := []string{}
		for _, f := range filters {
			if fMap, ok := f.(map[string]interface{}); ok {
				col, _ := fMap["column"].(string)
				op, _ := fMap["op"].(string)
				valRef, _ := fMap["value"].(string)
				if col != "" && op != "" {
					if op == "eq" {
						op = "="
					}
					whereClauses = append(whereClauses, fmt.Sprintf("%s %s $%d", sanitizeTableName(col), op, len(params)+1))
					params = append(params, s.resolveVar(valRef, automationCtx.Vars))
				}
			}
		}
		if len(whereClauses) > 0 {
			query += " WHERE " + strings.Join(whereClauses, " AND ")
		}
	}

	query += " RETURNING *"

	rows, err := tx.Query(ctx, query, params...)
	if err != nil {
		return nil, fmt.Errorf("update failed: %w", err)
	}
	defer rows.Close()

	return s.scanDataRows(rows)
}

func (s *AutomationService) executeDataDelete(ctx context.Context, tx pgx.Tx, table string, config map[string]interface{}, automationCtx *AutomationContext) (interface{}, error) {
	query := fmt.Sprintf("DELETE FROM %s", sanitizeTableName(table))

	// WHERE from filters
	params := []interface{}{}
	if filters, ok := config["filters"].([]interface{}); ok && len(filters) > 0 {
		whereClauses := []string{}
		for _, f := range filters {
			if fMap, ok := f.(map[string]interface{}); ok {
				col, _ := fMap["column"].(string)
				op, _ := fMap["op"].(string)
				valRef, _ := fMap["value"].(string)
				if col != "" {
					if op == "eq" || op == "" {
						op = "="
					}
					whereClauses = append(whereClauses, fmt.Sprintf("%s %s $%d", sanitizeTableName(col), op, len(params)+1))
					params = append(params, s.resolveVar(valRef, automationCtx.Vars))
				}
			}
		}
		if len(whereClauses) > 0 {
			query += " WHERE " + strings.Join(whereClauses, " AND ")
		}
	}

	query += " RETURNING *"

	rows, err := tx.Query(ctx, query, params...)
	if err != nil {
		return nil, fmt.Errorf("delete failed: %w", err)
	}
	defer rows.Close()

	return s.scanDataRows(rows)
}

func (s *AutomationService) scanDataRows(rows pgx.Rows) ([]map[string]interface{}, error) {
	var results []map[string]interface{}
	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			continue
		}
		row := make(map[string]interface{})
		for i, fd := range rows.FieldDescriptions() {
			row[string(fd.Name)] = utils.PurifyPgxValue(values[i])
		}
		results = append(results, row)
	}
	return results, nil
}

func (s *AutomationService) resolveVar(path string, vars map[string]interface{}) interface{} {
	if path == "" {
		return nil
	}
	// Handle $input, $output, or direct access
	if val, ok := vars[path]; ok {
		return val
	}
	// Handle nested paths like "trigger.data.xxx"
	parts := strings.Split(path, ".")
	var current interface{} = vars
	for _, part := range parts {
		if m, ok := current.(map[string]interface{}); ok {
			current = m[part]
		} else {
			return nil
		}
	}
	return current
}

func sanitizeTableName(name string) string {
	var result strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			result.WriteRune(r)
		}
	}
	return result.String()
}

func (s *AutomationService) executeRpcNode(ctx context.Context, node *AutomationNode, automationCtx *AutomationContext) (interface{}, error) {
	config := node.Config
	if config == nil {
		return nil, fmt.Errorf("rpc node missing config")
	}

	function, _ := config["function"].(string)
	if function == "" {
		return nil, fmt.Errorf("rpc node requires function name")
	}

	// Get connection pool
	pool := automationCtx.ProjectPool
	if pool == nil {
		return nil, fmt.Errorf("no database pool available")
	}

	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to acquire connection: %w", err)
	}
	defer conn.Release()

	tx, err := conn.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	// Setup RLS
	role := automationCtx.UserRole
	if role == "" {
		role = "authenticated"
	}
	claims := automationCtx.JWTClaims
	if claims == nil {
		claims = make(map[string]interface{})
	}

	quoteLocal := func(s interface{}) string {
		if s == nil {
			return "''"
		}
		str := fmt.Sprintf("%v", s)
		return "'" + strings.ReplaceAll(str, "'", "''") + "'"
	}

	timeoutMs := 8000
	if t, ok := config["timeout_ms"].(float64); ok && t > 0 {
		timeoutMs = int(t)
	}

	setupSQL := fmt.Sprintf(`
		SET LOCAL ROLE %s;
		SET LOCAL statement_timeout = '%dms';
		SET LOCAL "request.jwt.claim.sub" = %s;
		SET LOCAL "request.jwt.claim.role" = %s;
	`, role, timeoutMs, quoteLocal(claims["sub"]), quoteLocal(claims["role"]))

	if _, err := tx.Exec(ctx, setupSQL); err != nil {
		return nil, fmt.Errorf("failed to setup RLS: %w", err)
	}

	// Prepare args
	var args []interface{}
	if argsConfig, ok := config["args"].([]interface{}); ok {
		for _, argRef := range argsConfig {
			if ref, ok := argRef.(string); ok {
				args = append(args, s.resolveVar(ref, automationCtx.Vars))
			} else {
				args = append(args, argRef)
			}
		}
	}

	placeholders := make([]string, len(args))
	for i := range args {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
	}

	query := fmt.Sprintf("SELECT * FROM %s(%s)", sanitizeTableName(function), strings.Join(placeholders, ", "))

	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("rpc failed: %w", err)
	}
	defer rows.Close()

	result, err := s.scanDataRows(rows)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit failed: %w", err)
	}

	return result, nil
}

// ============================================================================
// DETAILED LOGGING FUNCTIONS (n8n-style)
// ============================================================================

// StartExecution inicia um novo contexto de execução com logging
func StartExecution(automationID, projectSlug string) *AutomationExecutionContext {
	execCtx := &AutomationExecutionContext{
		ExecutionID:   generateExecutionID(),
		AutomationID: automationID,
		ProjectSlug:   projectSlug,
		StartTime:     time.Now(),
		StepCounter:   0,
		LogChannel:    automationLogPool.Get().(chan AutomationStepLog),
		done:          make(chan struct{}),
	}

	executionMu.Lock()
	executionContexts[execCtx.ExecutionID] = execCtx
	executionMu.Unlock()

	// Iniciar worker de persistência assíncrona
	go execCtx.logWorker()

	// Log de início
	execCtx.LogStep(AutomationStepLog{
		StepID:       "start",
		NodeType:     "execution",
		Level:        LogLevelInfo,
		Message:      "Automation execution started",
		Timestamp:    time.Now(),
	})

	return execCtx
}

// FinishExecution finaliza a execução e persiste os logs restantes
func (execCtx *AutomationExecutionContext) FinishExecution(finalStatus string, finalOutput interface{}) {
	duration := time.Since(execCtx.StartTime).Milliseconds()

	execCtx.LogStep(AutomationStepLog{
		StepID:       "end",
		NodeType:     "execution",
		Level:        LogLevelInfo,
		Message:      fmt.Sprintf("Automation execution finished with status: %s", finalStatus),
		OutputData:   finalOutput,
		DurationMs:   duration,
		Timestamp:    time.Now(),
	})

	// Fechar canal e aguardar worker
	close(execCtx.LogChannel)
	<-execCtx.done

	// Retornar canal ao pool
	automationLogPool.Put(execCtx.LogChannel)

	// Remover do registry
	executionMu.Lock()
	delete(executionContexts, execCtx.ExecutionID)
	executionMu.Unlock()
}

// LogStep envia um log para o canal (não bloqueante)
func (execCtx *AutomationExecutionContext) LogStep(stepLog AutomationStepLog) {
	stepLog.ExecutionID = execCtx.ExecutionID
	stepLog.AutomationID = execCtx.AutomationID
	stepLog.ProjectSlug = execCtx.ProjectSlug
	
	if stepLog.StepID == "" {
		execCtx.StepCounter++
		stepLog.StepID = fmt.Sprintf("step_%d", execCtx.StepCounter)
	}
	if stepLog.Timestamp.IsZero() {
		stepLog.Timestamp = time.Now()
	}

	// Enviar de forma não bloqueante
	select {
	case execCtx.LogChannel <- stepLog:
	default:
		// Canal cheio, logar no stdout como fallback
		log.Printf("[AutomationLog-Drop] %s: %s", stepLog.StepID, stepLog.Message)
	}
}

// LogNodeStart inicia o log de um nó
func (execCtx *AutomationExecutionContext) LogNodeStart(nodeID, nodeType, nodeName string, inputData interface{}) string {
	stepID := fmt.Sprintf("%s_%d", nodeID, execCtx.StepCounter)
	execCtx.LogStep(AutomationStepLog{
		StepID:      stepID,
		NodeID:      nodeID,
		NodeType:    nodeType,
		NodeName:    nodeName,
		Level:       LogLevelDebug,
		Message:     fmt.Sprintf("Node %s (%s) started execution", nodeName, nodeType),
		InputData:   inputData,
		Timestamp:   time.Now(),
		Metadata: map[string]interface{}{
			"phase": "start",
		},
	})
	return stepID
}

// LogNodeComplete loga a conclusão de um nó
func (execCtx *AutomationExecutionContext) LogNodeComplete(stepID, nodeID, nodeType string, outputData interface{}, durationMs int64, nextNodes []string) {
	execCtx.LogStep(AutomationStepLog{
		StepID:     stepID + "_complete",
		NodeID:     nodeID,
		NodeType:   nodeType,
		Level:      LogLevelDebug,
		Message:    fmt.Sprintf("Node execution completed in %dms, proceeding to %v", durationMs, nextNodes),
		OutputData: outputData,
		DurationMs: durationMs,
		Timestamp:  time.Now(),
		Metadata: map[string]interface{}{
			"phase":      "complete",
			"next_nodes": nextNodes,
		},
	})
}

// LogNodeError loga erro em um nó
func (execCtx *AutomationExecutionContext) LogNodeError(stepID, nodeID, nodeType string, err error, inputData interface{}) {
	execCtx.LogStep(AutomationStepLog{
		StepID:       stepID + "_error",
		NodeID:       nodeID,
		NodeType:     nodeType,
		Level:        LogLevelError,
		Message:      fmt.Sprintf("Node execution failed: %v", err),
		InputData:    inputData,
		ErrorDetails: err.Error(),
		Timestamp:    time.Now(),
		Metadata: map[string]interface{}{
			"phase": "error",
		},
	})
}

// LogDataOperation loga operação de banco de dados
func (execCtx *AutomationExecutionContext) LogDataOperation(nodeID string, operation, table string, filters map[string]interface{}, rowCount int) {
	execCtx.LogStep(AutomationStepLog{
		StepID:    nodeID + "_data_op",
		NodeID:    nodeID,
		NodeType:  "data",
		Level:     LogLevelDebug,
		Message:   fmt.Sprintf("Data operation: %s on %s, %d rows affected", operation, table, rowCount),
		Timestamp: time.Now(),
		Metadata: map[string]interface{}{
			"operation":  operation,
			"table":      table,
			"filters":    filters,
			"row_count":  rowCount,
		},
	})
}

// LogHTTPRequest loga requisição HTTP
func (execCtx *AutomationExecutionContext) LogHTTPRequest(nodeID string, method, url string, statusCode int, durationMs int64) {
	execCtx.LogStep(AutomationStepLog{
		StepID:     nodeID + "_http",
		NodeID:     nodeID,
		NodeType:   "http",
		Level:      LogLevelDebug,
		Message:    fmt.Sprintf("HTTP %s %s - Status %d (%dms)", method, url, statusCode, durationMs),
		DurationMs: durationMs,
		Timestamp:  time.Now(),
		Metadata: map[string]interface{}{
			"method":      method,
			"url":         url,
			"status_code": statusCode,
		},
	})
}

// logWorker processa logs de forma assíncrona e persiste no banco
func (execCtx *AutomationExecutionContext) logWorker() {
	defer close(execCtx.done)

	var logs []AutomationStepLog
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	batchInsert := func() {
		if len(logs) == 0 {
			return
		}
		
		// Inserir em batch (fire-and-forget)
		go func(batch []AutomationStepLog) {
			for _, log := range batch {
				inputJSON, _ := json.Marshal(log.InputData)
				outputJSON, _ := json.Marshal(log.OutputData)
				metaJSON, _ := json.Marshal(log.Metadata)
				
				_, _ = SystemPool.Exec(context.Background(),
					`INSERT INTO system.automation_step_logs 
					 (id, execution_id, automation_id, project_slug, step_id, node_id, node_type, 
					  node_name, level, message, input_data, output_data, error_details, 
					  duration_ms, metadata, created_at)
					 VALUES (gen_random_uuid(), $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
					 ON CONFLICT DO NOTHING`,
					log.ExecutionID, log.AutomationID, log.ProjectSlug, log.StepID, 
					log.NodeID, log.NodeType, log.NodeName, string(log.Level),
					log.Message, string(inputJSON), string(outputJSON), 
					log.ErrorDetails, log.DurationMs, string(metaJSON), log.Timestamp)
			}
		}(logs)
		
		logs = nil
	}

	for {
		select {
		case log, ok := <-execCtx.LogChannel:
			if !ok {
				// Canal fechado, flush final
				batchInsert()
				return
			}
			logs = append(logs, log)
			if len(logs) >= 10 {
				batchInsert()
			}
		case <-ticker.C:
			batchInsert()
		}
	}
}

// generateExecutionID gera ID único para execução
func generateExecutionID() string {
	return fmt.Sprintf("exec_%d_%d", time.Now().UnixNano(), time.Now().Nanosecond())
}

// GetExecutionLogs recupera logs de uma execução (para API)
func GetExecutionLogs(executionID string) []AutomationStepLog {
	// Query do banco
	rows, err := SystemPool.Query(context.Background(),
		`SELECT step_id, node_id, node_type, level, message, 
		 input_data, output_data, error_details, duration_ms, metadata, created_at
		 FROM system.automation_step_logs 
		 WHERE execution_id = $1 
		 ORDER BY created_at ASC`,
		executionID)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var logs []AutomationStepLog
	for rows.Next() {
		var log AutomationStepLog
		var inputJSON, outputJSON, metaJSON []byte
		rows.Scan(&log.StepID, &log.NodeID, &log.NodeType, &log.Level, &log.Message,
			&inputJSON, &outputJSON, &log.ErrorDetails, &log.DurationMs, &metaJSON, &log.Timestamp)
		json.Unmarshal(inputJSON, &log.InputData)
		json.Unmarshal(outputJSON, &log.OutputData)
		json.Unmarshal(metaJSON, &log.Metadata)
		logs = append(logs, log)
	}
	return logs
}
