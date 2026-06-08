package services

import (
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"
)

// ============================================================================
// Transform Node Executor - Template substitution
// ============================================================================

// TransformNodeExecutor aplica templates de transformação
type TransformNodeExecutor struct {
	Template   interface{} // Pode ser string, map, ou array
	FieldExpressions    map[string]string
	FieldLogicPipelines map[string]InlineFieldLogicPipeline
	FieldPathToBodyKey  map[string]string
	OutputSlot int
	ErrorSlot  int
}

func (n *TransformNodeExecutor) GetOutputSlot() int { return n.OutputSlot }
func (n *TransformNodeExecutor) GetErrorSlot() int  { return n.ErrorSlot }

func (n *TransformNodeExecutor) Execute(ctx *FlowContext) error {
	result := resolveTemplateCtx(n.Template, ctx)
	if bodyMap, ok := result.(map[string]interface{}); ok {
		result = applyFieldPipelinesToBodyMap(bodyMap, n.FieldPathToBodyKey, n.FieldExpressions, n.FieldLogicPipelines, ctx)
	}
	ctx.Vars[n.OutputSlot] = result
	// Também coloca em $output para acesso fácil
	if n.OutputSlot < len(ctx.Vars) {
		ctx.Vars[1] = result // slot 1 = $output
	}
	return nil
}

// BuildTransformNode cria um TransformNodeExecutor
func BuildTransformNode(config map[string]interface{}, compiler *Compiler) (NodeExecutor, error) {
	node := &TransformNodeExecutor{
		OutputSlot: compiler.allocSlot(),
		ErrorSlot:  -1,
		FieldExpressions:    map[string]string{},
		FieldLogicPipelines: map[string]InlineFieldLogicPipeline{},
		FieldPathToBodyKey:  map[string]string{},
	}

	// Pode ser "body" ou "template"
	if v, ok := config["body"]; ok {
		node.Template = v
	} else if v, ok := config["template"]; ok {
		node.Template = v
	}

	node.FieldExpressions = parseFieldExpressions(config)
	node.FieldLogicPipelines = parseFieldLogicPipelines(config)
	node.FieldPathToBodyKey = mergeFieldPathMaps(
		buildIndexedFieldPathMap(config, "_customFields", "key", true),
		buildIndexedFieldPathMap(config, "_payload", "column", false),
	)

	return node, nil
}

// ============================================================================
// Convert Node Executor - Type conversion
// ============================================================================

// ConvertNodeExecutor converte valores entre tipos
type ConvertNodeExecutor struct {
	ValueRef   string // referência para VarPool
	ToType     string // int, float, string, boolean, json
	OutputSlot int
	ErrorSlot  int
}

func (n *ConvertNodeExecutor) GetOutputSlot() int { return n.OutputSlot }
func (n *ConvertNodeExecutor) GetErrorSlot() int  { return n.ErrorSlot }

func (n *ConvertNodeExecutor) Execute(ctx *FlowContext) error {
	rawValue := resolveVarCtx(n.ValueRef, ctx)
	strValue := fmt.Sprintf("%v", rawValue)

	var result interface{}
	switch n.ToType {
	case "int", "integer":
		v, _ := strconv.ParseInt(strValue, 10, 64)
		result = int(v)
	case "float", "number":
		v, _ := strconv.ParseFloat(strValue, 64)
		result = v
	case "string":
		result = strValue
	case "boolean", "bool":
		result = strValue == "true" || strValue == "1" || strValue == "yes"
	case "json":
		var jsonVal interface{}
		if err := json.Unmarshal([]byte(strValue), &jsonVal); err == nil {
			result = jsonVal
		} else {
			result = rawValue
		}
	default:
		result = rawValue
	}

	ctx.Vars[n.OutputSlot] = result
	return nil
}

// BuildConvertNode cria um ConvertNodeExecutor
func BuildConvertNode(config map[string]interface{}, compiler *Compiler) (NodeExecutor, error) {
	node := &ConvertNodeExecutor{
		OutputSlot: compiler.allocSlot(),
		ErrorSlot:  -1,
	}

	if v, ok := config["value"].(string); ok {
		node.ValueRef = v
	}
	if v, ok := config["toType"].(string); ok {
		node.ToType = strings.ToLower(v)
	}

	return node, nil
}

// ============================================================================
// Logic Node Executor - Conditions and boolean logic
// ============================================================================

// LogicNodeExecutor avalia condições lógicas
type LogicNodeExecutor struct {
	Conditions []LogicCondition
	Match      string // "all" (AND) ou "any" (OR)
	OutputSlot int
	ErrorSlot  int
}

type LogicCondition struct {
	Left  string      // referência VarPool ou valor literal
	Op    string      // eq, neq, gt, lt, gte, lte, contains, regex, starts_with, ends_with, is_empty
	Right interface{} // valor para comparar
}

func (n *LogicNodeExecutor) GetOutputSlot() int { return n.OutputSlot }
func (n *LogicNodeExecutor) GetErrorSlot() int  { return n.ErrorSlot }

func (n *LogicNodeExecutor) Execute(ctx *FlowContext) error {
	if len(n.Conditions) == 0 {
		ctx.Vars[n.OutputSlot] = true
		return nil
	}

	matchAll := n.Match == "all" || n.Match == ""

	for _, cond := range n.Conditions {
		result := n.evaluateCondition(cond, ctx)
		if matchAll && !result {
			ctx.Vars[n.OutputSlot] = false
			return nil
		}
		if !matchAll && result {
			ctx.Vars[n.OutputSlot] = true
			return nil
		}
	}

	if matchAll {
		ctx.Vars[n.OutputSlot] = true
	} else {
		ctx.Vars[n.OutputSlot] = false
	}
	return nil
}

func (n *LogicNodeExecutor) evaluateCondition(cond LogicCondition, ctx *FlowContext) bool {
	leftVal := resolveVarCtx(cond.Left, ctx)
	leftStr := fmt.Sprintf("%v", leftVal)
	
	// Resolver variáveis no campo right também (permite comparar com dados de outros nós)
	var rightStr string
	if rightStrVal, ok := cond.Right.(string); ok {
		// Se for string, pode conter variáveis {{...}} - resolver
		rightStr = resolveStringCtx(rightStrVal, ctx)
	} else {
		// Se não for string, converter diretamente
		rightStr = fmt.Sprintf("%v", cond.Right)
	}

	switch cond.Op {
	case "eq", "equals", "=", "==":
		return leftStr == rightStr
	case "neq", "not_equals", "!=", "<>":
		return leftStr != rightStr
	case "gt", "greater_than", ">":
		leftNum, _ := strconv.ParseFloat(leftStr, 64)
		rightNum, _ := strconv.ParseFloat(rightStr, 64)
		return leftNum > rightNum
	case "lt", "less_than", "<":
		leftNum, _ := strconv.ParseFloat(leftStr, 64)
		rightNum, _ := strconv.ParseFloat(rightStr, 64)
		return leftNum < rightNum
	case "gte", "greater_than_or_equal", ">=", "=>":
		leftNum, _ := strconv.ParseFloat(leftStr, 64)
		rightNum, _ := strconv.ParseFloat(rightStr, 64)
		return leftNum >= rightNum
	case "lte", "less_than_or_equal", "<=", "=<":
		leftNum, _ := strconv.ParseFloat(leftStr, 64)
		rightNum, _ := strconv.ParseFloat(rightStr, 64)
		return leftNum <= rightNum
	case "contains":
		return strings.Contains(strings.ToLower(leftStr), strings.ToLower(rightStr))
	case "regex":
		re, err := regexp.Compile(rightStr)
		if err != nil {
			return false
		}
		return re.MatchString(leftStr)
	case "starts_with", "startsWith", "starts":
		return strings.HasPrefix(leftStr, rightStr)
	case "ends_with", "endsWith", "ends":
		return strings.HasSuffix(leftStr, rightStr)
	case "is_empty", "isEmpty", "empty":
		return leftVal == nil || leftStr == "" || (isEmptySlice(leftVal))
	default:
		return leftStr == rightStr
	}
}

func isEmptySlice(v interface{}) bool {
	if arr, ok := v.([]interface{}); ok {
		return len(arr) == 0
	}
	return false
}

// BuildLogicNode cria um LogicNodeExecutor
func BuildLogicNode(config map[string]interface{}, compiler *Compiler) (NodeExecutor, error) {
	node := &LogicNodeExecutor{
		OutputSlot: compiler.allocSlot(),
		ErrorSlot:  -1,
		Conditions: []LogicCondition{},
		Match:      "all",
	}

	if v, ok := config["match"].(string); ok {
		node.Match = v
	}

	// Parse conditions array
	if conditions, ok := config["conditions"].([]interface{}); ok {
		for _, c := range conditions {
			if cMap, ok := c.(map[string]interface{}); ok {
				cond := LogicCondition{}
				if l, ok := cMap["left"].(string); ok {
					cond.Left = l
				}
				if o, ok := cMap["op"].(string); ok {
					cond.Op = o
				}
				if r, ok := cMap["right"]; ok {
					cond.Right = r
				}
				node.Conditions = append(node.Conditions, cond)
			}
		}
	}

	// Support legacy single condition format
	if len(node.Conditions) == 0 && (config["left"] != nil || config["op"] != nil) {
		cond := LogicCondition{}
		if l, ok := config["left"].(string); ok {
			cond.Left = l
		}
		if o, ok := config["op"].(string); ok {
			cond.Op = o
		}
		if r, ok := config["right"]; ok {
			cond.Right = r
		}
		node.Conditions = append(node.Conditions, cond)
	}

	return node, nil
}

// BuildConditionNode é um alias para BuildLogicNode
func BuildConditionNode(config map[string]interface{}, compiler *Compiler) (NodeExecutor, error) {
	return BuildLogicNode(config, compiler)
}

// ============================================================================
// Response Node Executor - Workflow response
// ============================================================================

// ResponseNodeExecutor define a resposta do workflow
type ResponseNodeExecutor struct {
	Body       interface{} // template
	FieldExpressions    map[string]string
	FieldLogicPipelines map[string]InlineFieldLogicPipeline
	FieldPathToBodyKey  map[string]string
	OutputSlot int
	ErrorSlot  int
}

func (n *ResponseNodeExecutor) GetOutputSlot() int { return n.OutputSlot }
func (n *ResponseNodeExecutor) GetErrorSlot() int  { return n.ErrorSlot }

func (n *ResponseNodeExecutor) Execute(ctx *FlowContext) error {
	result := resolveTemplateCtx(n.Body, ctx)
	if bodyMap, ok := result.(map[string]interface{}); ok {
		result = applyFieldPipelinesToBodyMap(bodyMap, n.FieldPathToBodyKey, n.FieldExpressions, n.FieldLogicPipelines, ctx)
	}
	ctx.Vars[n.OutputSlot] = result
	return nil
}

// BuildResponseNode cria um ResponseNodeExecutor
func BuildResponseNode(config map[string]interface{}, compiler *Compiler) (NodeExecutor, error) {
	node := &ResponseNodeExecutor{
		OutputSlot: compiler.allocSlot(),
		ErrorSlot:  -1,
		FieldExpressions:    map[string]string{},
		FieldLogicPipelines: map[string]InlineFieldLogicPipeline{},
		FieldPathToBodyKey:  map[string]string{},
	}

	if v, ok := config["body"]; ok {
		node.Body = v
	}

	node.FieldExpressions = parseFieldExpressions(config)
	node.FieldLogicPipelines = parseFieldLogicPipelines(config)
	node.FieldPathToBodyKey = mergeFieldPathMaps(
		buildIndexedFieldPathMap(config, "_customFields", "key", true),
		buildIndexedFieldPathMap(config, "_payload", "column", false),
	)

	return node, nil
}

// ============================================================================
// Trigger Node Executor - Workflow start
// ============================================================================

// TriggerNodeExecutor inicializa o workflow e pode ter condições
type TriggerNodeExecutor struct {
	Conditions []LogicCondition // condições para permitir execução
	Match      string         // "all" ou "any"
	OutputSlot int
	ErrorSlot  int
}

func (n *TriggerNodeExecutor) GetOutputSlot() int { return n.OutputSlot }
func (n *TriggerNodeExecutor) GetErrorSlot() int  { return n.ErrorSlot }

func (n *TriggerNodeExecutor) Execute(ctx *FlowContext) error {
	// Se não tiver condições, sempre permite
	if len(n.Conditions) == 0 {
		// Passa o input adiante
		if len(ctx.Vars) > 0 {
			ctx.Vars[n.OutputSlot] = ctx.Vars[0] // slot 0 = input
		}
		return nil
	}

	// Avaliar condições
	logicNode := &LogicNodeExecutor{
		Conditions: n.Conditions,
		Match:      n.Match,
		OutputSlot: n.OutputSlot,
	}
	
	if err := logicNode.Execute(ctx); err != nil {
		return err
	}

	// Se condições não passaram, retorna false para abortar
	if result, ok := ctx.Vars[n.OutputSlot].(bool); ok && !result {
		return fmt.Errorf("trigger conditions not met")
	}

	// Passa input adiante
	if len(ctx.Vars) > 0 {
		ctx.Vars[n.OutputSlot] = ctx.Vars[0]
	}
	return nil
}

// BuildTriggerNode cria um TriggerNodeExecutor
func BuildTriggerNode(config map[string]interface{}, compiler *Compiler) (NodeExecutor, error) {
	node := &TriggerNodeExecutor{
		OutputSlot: compiler.allocSlot(),
		ErrorSlot:  -1,
		Conditions: []LogicCondition{},
		Match:      "all",
	}

	if v, ok := config["match"].(string); ok {
		node.Match = v
	}

	// Parse conditions
	if conditions, ok := config["conditions"].([]interface{}); ok {
		for _, c := range conditions {
			if cMap, ok := c.(map[string]interface{}); ok {
				cond := LogicCondition{}
				if l, ok := cMap["left"].(string); ok {
					cond.Left = l
				}
				if o, ok := cMap["op"].(string); ok {
					cond.Op = o
				}
				if r, ok := cMap["right"]; ok {
					cond.Right = r
				}
				node.Conditions = append(node.Conditions, cond)
			}
		}
	}

	return node, nil
}

// ============================================================================
// Helper functions para resolução de templates
// ============================================================================

// resolveTemplate resolve variáveis {{...}} em templates (versão compatível com []interface{})
func resolveTemplate(template interface{}, vars []interface{}) interface{} {
	switch v := template.(type) {
	case string:
		return resolveString(v, vars)
	case map[string]interface{}:
		result := make(map[string]interface{})
		for key, val := range v {
			result[key] = resolveTemplate(val, vars)
		}
		return result
	case []interface{}:
		result := make([]interface{}, len(v))
		for i, val := range v {
			result[i] = resolveTemplate(val, vars)
		}
		return result
	default:
		return template
	}
}

// resolveTemplateCtx resolve variáveis {{...}} em templates (versão com FlowContext - suporta NodeOutputs)
func resolveTemplateCtx(template interface{}, ctx *FlowContext) interface{} {
	switch v := template.(type) {
	case string:
		return resolveStringCtx(v, ctx)
	case map[string]interface{}:
		result := make(map[string]interface{})
		for key, val := range v {
			result[key] = resolveTemplateCtx(val, ctx)
		}
		return result
	case []interface{}:
		result := make([]interface{}, len(v))
		for i, val := range v {
			result[i] = resolveTemplateCtx(val, ctx)
		}
		return result
	default:
		return template
	}
}

// resolveString resolve variáveis {{...}} em uma string (versão compatível com []interface{})
func resolveString(template string, vars []interface{}) string {
	if template == "" {
		return ""
	}

	// Regex para {{var.path}}
	re := regexp.MustCompile(`\{\{([^}]+)\}\}`)

	return re.ReplaceAllStringFunc(template, func(match string) string {
		path := strings.Trim(match, "{}")
		path = strings.TrimSpace(path)

		val := resolveVar(path, vars)
		if val == nil {
			return ""
		}
		return fmt.Sprintf("%v", val)
	})
}

// resolveStringCtx resolve variáveis {{...}} em uma string (versão com FlowContext - suporta NodeOutputs)
func resolveStringCtx(template string, ctx *FlowContext) string {
	if template == "" {
		return ""
	}

	// Regex para {{var.path}}
	re := regexp.MustCompile(`\{\{([^}]+)\}\}`)

	return re.ReplaceAllStringFunc(template, func(match string) string {
		path := strings.Trim(match, "{}")
		path = strings.TrimSpace(path)

		log.Printf("[resolveStringCtx] Resolving variable: %s from template: %s", path, template)
		val := resolveVarCtx(path, ctx)
		if val == nil {
			log.Printf("[resolveStringCtx] Variable %s resolved to NIL", path)
			return ""
		}
		log.Printf("[resolveStringCtx] Variable %s resolved to: %v (type: %T)", path, val, val)
		return fmt.Sprintf("%v", val)
	})
}

// resolveVar resolve uma referência de variável (ex: "trigger.data", "$input") - versão compatível
func resolveVar(path string, vars []interface{}) interface{} {
	if path == "" {
		return nil
	}

	// Slots especiais
	if path == "$input" || path == "input" {
		if len(vars) > 0 {
			return vars[0]
		}
		return nil
	}
	if path == "$output" || path == "output" {
		if len(vars) > 1 {
			return vars[1]
		}
		return nil
	}

	// Referência a slot específico (formato: slot:N)
	if strings.HasPrefix(path, "slot:") {
		numStr := strings.TrimPrefix(path, "slot:")
		if num, err := strconv.Atoi(numStr); err == nil && num >= 0 && num < len(vars) {
			return vars[num]
		}
		return nil
	}

	// Referência a output de nó (formato: nodeId.data.field ou nodeId.field)
	parts := strings.Split(path, ".")
	if len(parts) == 0 {
		return nil
	}

	// Fallback: tentar achar nos vars[0] (input) que é um map
	if len(vars) > 0 && vars[0] != nil {
		if rootMap, ok := vars[0].(map[string]interface{}); ok {
			return getNestedValue(rootMap, parts)
		}
	}

	return nil
}

// resolveVarCtx resolve uma referência de variável (ex: "node_1.data", "$input", "trigger.data") - versão com FlowContext
func resolveVarCtx(path string, ctx *FlowContext) interface{} {
	if path == "" {
		return nil
	}

	// Normalização de handles do Frontend (Sinergia)
	// O frontend trata o trigger como um nó com porta 'out.data', mas o backend
	// armazena o payload diretamente. Removemos esses prefixos para compatibilidade.
	if strings.HasPrefix(path, "out.data.") {
		path = strings.TrimPrefix(path, "out.data.")
	} else if strings.HasPrefix(path, "payload.") {
		path = strings.TrimPrefix(path, "payload.")
	}

	vars := ctx.Vars
	
	log.Printf("[resolveVarCtx] START - path=%s (normalized), NodeOutputs=%v, Vars slots=%d", path, ctx.NodeOutputs, len(vars))

	// Slots especiais
	if path == "$input" || path == "input" {
		if len(vars) > 0 {
			return vars[0]
		}
		return nil
	}
	if path == "$output" || path == "output" {
		if len(vars) > 1 {
			return vars[1]
		}
		return nil
	}

	// Referência a vault secret (formato: $vault.secret_name, $vault.secret_name.value ou vault.secret_name)
	if strings.HasPrefix(path, "$vault.") || strings.HasPrefix(path, "vault.") {
		secretName := strings.TrimPrefix(path, "$vault.")
		secretName = strings.TrimPrefix(secretName, "vault.")
		// Remove .value suffix if present (new frontend pattern)
		if strings.HasSuffix(secretName, ".value") {
			secretName = strings.TrimSuffix(secretName, ".value")
		}
		if ctx.CryptoSvc != nil && ctx.ProjectSlug != "" {
			secret, _, err := NewVaultService(ctx.CryptoSvc).Resolve(ctx, ctx.ProjectSlug, secretName, VaultPurposeAutomation)
			if err == nil {
				log.Printf("[resolveVarCtx] Vault secret %s resolved successfully", secretName)
				return secret
			}
			log.Printf("[resolveVarCtx] Vault secret %s resolution failed: %v", secretName, err)
		}
		return nil
	}

	// Referência a slot específico (formato: slot:N)
	if strings.HasPrefix(path, "slot:") {
		numStr := strings.TrimPrefix(path, "slot:")
		if num, err := strconv.Atoi(numStr); err == nil && num >= 0 && num < len(vars) {
			return vars[num]
		}
		return nil
	}

	// Referência a output de nó (formato: nodeId.data.field ou nodeId.field)
	parts := strings.Split(path, ".")
	if len(parts) == 0 {
		return nil
	}

	// Verificar se a primeira parte é "trigger" - aponta para o input do fluxo (vars[0])
	nodeID := parts[0]
	log.Printf("[resolveVarCtx] Checking nodeID=%s in NodeOutputs", nodeID)
	
	// Suporte especial para "trigger" que é o input inicial do fluxo
	if nodeID == "trigger" && len(vars) > 0 {
		log.Printf("[resolveVarCtx] 'trigger' keyword found, using vars[0] as trigger output")
		triggerOutput := vars[0]
		if len(parts) > 1 {
			// Skip "data" if present (trigger.data.nome -> trigger.nome)
			remainingParts := parts[1:]
			if remainingParts[0] == "data" && len(remainingParts) > 1 {
				remainingParts = remainingParts[1:]
			}
			result := getNestedValue(triggerOutput, remainingParts)
			log.Printf("[resolveVarCtx] Trigger resolved with %v: result=%v", remainingParts, result)
			return result
		}
		return triggerOutput
	}
	
	if slot, exists := ctx.NodeOutputs[nodeID]; exists && slot >= 0 && slot < len(vars) {
		// Nó encontrado! Pegar o output do nó
		nodeOutput := vars[slot]
		log.Printf("[resolveVarCtx] Node %s found at slot %d, output type=%T, value=%v", nodeID, slot, nodeOutput, nodeOutput)
		
		// Navegar no resto do path (ex: data.nome)
		if len(parts) > 1 {
			remainingParts := parts[1:]
			
			// Se o próximo path for "data", interpretar como "primeiro elemento do slice"
			// Isso é necessário porque DataNode retorna []map[string]interface{}
			// e o frontend usa node_X.data.field para acessar o primeiro registro
			// Para triggers (map direto), "data" é ignorado e acessamos diretamente o campo
			log.Printf("[resolveVarCtx] remainingParts[0]=%s, checking if 'data'", remainingParts[0])
			if remainingParts[0] == "data" {
				log.Printf("[resolveVarCtx] Found 'data' keyword")
				// Tentar extrair o primeiro elemento se for um slice
				switch v := nodeOutput.(type) {
				case []map[string]interface{}:
					log.Printf("[resolveVarCtx] Output is []map[string]interface{} with %d elements", len(v))
					if len(v) > 0 {
						nodeOutput = v[0]
						log.Printf("[resolveVarCtx] Extracted first element: %v", nodeOutput)
						// Continuar com o resto do path (ex: nome)
						if len(remainingParts) > 1 {
							result := getNestedValue(nodeOutput, remainingParts[1:])
							log.Printf("[resolveVarCtx] After getNestedValue with %v: result=%v", remainingParts[1:], result)
							return result
						}
						return nodeOutput
					}
					log.Printf("[resolveVarCtx] Slice is empty, returning nil")
					return nil
				case []interface{}:
					log.Printf("[resolveVarCtx] Output is []interface{} with %d elements", len(v))
					if len(v) > 0 {
						nodeOutput = v[0]
						log.Printf("[resolveVarCtx] Extracted first element: %v", nodeOutput)
						if len(remainingParts) > 1 {
							result := getNestedValue(nodeOutput, remainingParts[1:])
							log.Printf("[resolveVarCtx] After getNestedValue with %v: result=%v", remainingParts[1:], result)
							return result
						}
						return nodeOutput
					}
					log.Printf("[resolveVarCtx] Slice is empty, returning nil")
					return nil
				case map[string]interface{}:
					// Check if map has "data" key - if yes, use it; otherwise ignore "data" (trigger behavior)
					if _, hasDataKey := v["data"]; hasDataKey {
						// Map has "data" key (e.g., HTTP node) - use it
						log.Printf("[resolveVarCtx] Output is map[string]interface{} with 'data' key, using it")
						if len(remainingParts) > 1 {
							// Special case for HTTP node: if accessing body.field, try direct access first
							if remainingParts[0] == "body" && len(remainingParts) > 1 {
								// Try to access the field directly in the data map (for mapped fields)
								if directVal, exists := v["data"].(map[string]interface{})[remainingParts[1]]; exists {
									log.Printf("[resolveVarCtx] Found direct field in body: %s = %v", remainingParts[1], directVal)
									return directVal
								}
							}
							result := getNestedValue(v["data"], remainingParts[1:])
							log.Printf("[resolveVarCtx] After getNestedValue with %v: result=%v", remainingParts[1:], result)
							return result
						}
						return v["data"]
					} else {
						// Trigger node: output é map direto, não tem chave "data"
						// Ignorar "data" e acessar diretamente o campo no map
						log.Printf("[resolveVarCtx] Output is map[string]interface{} (trigger, no 'data' key), ignoring 'data' and accessing directly")
						if len(remainingParts) > 1 {
							result := getNestedValue(nodeOutput, remainingParts[1:])
							log.Printf("[resolveVarCtx] After getNestedValue with %v: result=%v", remainingParts[1:], result)
							return result
						}
						return nodeOutput
					}
				default:
					log.Printf("[resolveVarCtx] Output is unexpected type=%T", nodeOutput)
				}
			}
			
			return getNestedValue(nodeOutput, remainingParts)
		}
		return nodeOutput
	}

	log.Printf("[resolveVarCtx] Node %s not found in NodeOutputs, trying fallback", nodeID)
	
	// Fallback: tentar achar nos vars[0] (input) que é um map
	if len(vars) > 0 && vars[0] != nil {
		if rootMap, ok := vars[0].(map[string]interface{}); ok {
			result := getNestedValue(rootMap, parts)
			log.Printf("[resolveVarCtx] Fallback result: %v", result)
			return result
		}
	}

	log.Printf("[resolveVarCtx] END - returning nil for path=%s", path)
	return nil
}

// parsePathWithArrayIndexing converte um path com notação de array (ex: "entry[0]") em partes separadas (ex: ["entry", "0"])
func parsePathWithArrayIndexing(path []string) []string {
	result := make([]string, 0, len(path)*2)
	
	for _, part := range path {
		// Verifica se a parte tem notação de array [n]
		if strings.Contains(part, "[") && strings.HasSuffix(part, "]") {
			// Separa o nome do campo do índice: "entry[0]" -> "entry" e "0"
			openBracket := strings.Index(part, "[")
			closeBracket := strings.Index(part, "]")
			
			if openBracket > 0 && closeBracket > openBracket {
				fieldName := part[:openBracket]
				indexStr := part[openBracket+1 : closeBracket]
				
				// Adiciona o nome do campo
				result = append(result, fieldName)
				
				// Adiciona o índice se for um número válido
				if idx, err := strconv.Atoi(indexStr); err == nil {
					result = append(result, strconv.Itoa(idx))
				}
			} else {
				// Se a notação estiver malformada, mantém como está
				result = append(result, part)
			}
		} else {
			result = append(result, part)
		}
	}
	
	return result
}

// getNestedValue navega em um map aninhado
func getNestedValue(data interface{}, path []string) interface{} {
	if len(path) == 0 {
		return data
	}

	// Parse array indexing notation (ex: "entry[0]" -> ["entry", "0"])
	parsedPath := parsePathWithArrayIndexing(path)
	
	log.Printf("[getNestedValue] original path=%v, parsed path=%v, data type=%T, keys available=%v", path, parsedPath, data, getMapKeys(data))

	switch v := data.(type) {
	case map[string]interface{}:
		if len(parsedPath) == 0 {
			return data
		}
		val, exists := v[parsedPath[0]]
		if !exists {
			log.Printf("[getNestedValue] Key %s not found in map", parsedPath[0])
			return nil
		}
		log.Printf("[getNestedValue] Found key %s = %v", parsedPath[0], val)
		if len(parsedPath) == 1 {
			return val
		}
		return getNestedValue(val, parsedPath[1:])
	case []interface{}:
		if len(parsedPath) == 0 {
			return data
		}
		// Se o path[0] for um número, tenta acessar como índice de array
		if idx, err := strconv.Atoi(parsedPath[0]); err == nil && idx >= 0 && idx < len(v) {
			if len(parsedPath) == 1 {
				return v[idx]
			}
			return getNestedValue(v[idx], parsedPath[1:])
		}
		return nil
	default:
		return nil
	}
}

// getMapKeys retorna as chaves de um map[string]interface{} para debug
func getMapKeys(data interface{}) []string {
	if m, ok := data.(map[string]interface{}); ok {
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		return keys
	}
	return nil
}
