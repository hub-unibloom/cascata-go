package nexus

import (
	"context"
	"errors"
	"fmt"
	"log"
	"reflect"
	"regexp"
	"strconv"
	"strings"
)

// ErrFilterNotMatched é retornado pelo TriggerComponent quando o filtro
// condicional não bate com o payload. O HookResolver usa este sentinela
// para tratar como "não interceptado" sem contabilizar como falha.
var ErrFilterNotMatched = errors.New("nexus:filter_not_matched")

// ============================================================================
// STANDARD LIBRARY — Componentes Padrão do Nexus
// ============================================================================
// Implementações concretas dos componentes fundamentais:
//   - TriggerComponent:      Ponto de entrada do grafo
//   - ResponseComponent:     Ponto de saída / resposta HTTP
//   - ConditionComponent:    Avaliação condicional com branching true/false
//   - TransformComponent:    Manipulação e transformação de dados
//   - SwitchComponent:       Roteamento multi-caminho baseado em valor
//   - MergeComponent:        Combinação de múltiplos inputs
//   - SplitComponent:        Divisão de array em itens individuais
//   - ErrorHandlerComponent: Captura de erros com fallback
// ============================================================================

// ──────────────────────────────────────────────────────────────────────────────
// TRIGGER COMPONENT
// ──────────────────────────────────────────────────────────────────────────────

type TriggerComponent struct {
	*BaseComponent
}

func NewTriggerComponent(id string) *TriggerComponent {
	return &TriggerComponent{
		BaseComponent: NewBaseComponent(id, TypeTrigger,
			[]PortDefinition{},
			[]PortDefinition{{Name: "out", DataType: "any", Required: true}},
		),
	}
}

func (t *TriggerComponent) Process(ctx context.Context, ip *InformationPacket, state *NexusState) (map[string][]*InformationPacket, error) {
	t.SetStatus(StatusProcessing)

	// Injeta payload do trigger
	outIP := ip.Clone()
	outIP.SourceNode = t.ID()
	outIP.SourcePort = "out"

	if state.Trigger() != nil && state.Trigger().Payload != nil {
		outIP.Data = state.Trigger().Payload
	}

	// ── Avaliação de Filtro Condicional ──
	// Se o trigger tem filterRows configurados, avalia cada condição
	// contra o payload. Se o filtro não bater, retorna ErrFilterNotMatched
	// para que o HookResolver trate como "não interceptado".
	settings := t.Config().Settings
	if matched := t.evaluateFilter(outIP, state, settings); !matched {
		t.SetStatus(StatusSuccess)
		return nil, ErrFilterNotMatched
	}

	t.SetStatus(StatusSuccess)
	return EmitSingle("out", outIP), nil
}

// evaluateFilter verifica as condições de filtro do trigger.
// Retorna true se o payload passa no filtro (ou se não há filtro configurado).
func (t *TriggerComponent) evaluateFilter(ip *InformationPacket, state *NexusState, settings map[string]interface{}) bool {
	// Prioridade 1: filterRows (formato no-code)
	if rows, ok := settings["filterRows"].([]interface{}); ok && len(rows) > 0 {
		for _, row := range rows {
			r, ok := row.(map[string]interface{})
			if !ok {
				continue
			}
			col, _ := r["column"].(string)
			op, _ := r["operator"].(string)
			val, _ := r["value"].(string)
			if col == "" || op == "" {
				continue
			}
			// Interpola valor (pode conter {{variáveis}})
			resolvedVal := state.Interpolate(val)
			// Avalia a condição contra o payload
			if !evaluateSingleCondition(ip, state, col, op, resolvedVal) {
				return false // Qualquer condição falsa = filtro não bateu (AND lógico)
			}
		}
		return true // Todas as condições passaram
	}

	// Prioridade 2: filter string (formato code)
	if filter, ok := settings["filter"].(string); ok && filter != "" {
		// Para expressões code como "status == 'active' && price > 100"
		// Por agora, avalia como condição simples se tiver formato "field op value"
		parts := strings.Fields(filter)
		if len(parts) == 3 {
			return evaluateSingleCondition(ip, state, parts[0], parts[1], parts[2])
		}
	}

	// Sem filtro configurado = aceita tudo
	return true
}

// ──────────────────────────────────────────────────────────────────────────────
// RESPONSE COMPONENT
// ──────────────────────────────────────────────────────────────────────────────

type ResponseComponent struct {
	*BaseComponent
}

func NewResponseComponent(id string) *ResponseComponent {
	return &ResponseComponent{
		BaseComponent: NewBaseComponent(id, TypeResponse,
			[]PortDefinition{{Name: "in", DataType: "any", Required: true}},
			[]PortDefinition{{Name: "out", DataType: "any", Required: false}},
		),
	}
}

func (r *ResponseComponent) Process(ctx context.Context, ip *InformationPacket, state *NexusState) (map[string][]*InformationPacket, error) {
	r.SetStatus(StatusProcessing)
	defer r.SetStatus(StatusSuccess)

	config := r.Config()
	settings := config.Settings

	responseData := make(map[string]interface{})

	// Status code
	statusCode := 200
	if code, ok := settings["status_code"]; ok {
		switch v := code.(type) {
		case float64:
			statusCode = int(v)
		case int:
			statusCode = v
		case string:
			if c, err := strconv.Atoi(v); err == nil {
				statusCode = c
			}
		}
	}
	responseData["status_code"] = float64(statusCode)

	// ── Resolução do body da resposta ──
	// Suporta 3 formatos:
	//   1. mappingRows (no-code): [{column: "key", value: "val"}, ...]
	//   2. mapping (no-code compacto): {key: "val", ...}
	//   3. body (code): string template ou objeto com interpolação
	//   4. Fallback: dados de entrada do IP

	bodyResolved := false

	// Formato 1: mappingRows (formato no-code do NexusArchitect)
	if rows, ok := settings["mappingRows"].([]interface{}); ok && len(rows) > 0 {
		for _, row := range rows {
			if r, ok := row.(map[string]interface{}); ok {
				col, _ := r["column"].(string)
				val := r["value"]
				if col != "" {
					responseData[col] = state.ResolveAny(val)
				}
			}
		}
		bodyResolved = true
	}

	// Formato 2: mapping (objeto compacto)
	if !bodyResolved {
		if mapping, ok := settings["mapping"].(map[string]interface{}); ok && len(mapping) > 0 {
			interpolated := state.InterpolateMap(mapping)
			for k, v := range interpolated {
				responseData[k] = v
			}
			bodyResolved = true
		}
	}

	// Formato 3: body (string template ou objeto — modo code)
	if !bodyResolved {
		if body, ok := settings["body"]; ok {
			log.Printf("[ResponseComponent] Processing body field, type: %T, value: %v", body, body)
			switch v := body.(type) {
			case string:
				resolved := state.ResolveAny(v)
				log.Printf("[ResponseComponent] Resolved string template: %s -> %v", v, resolved)
				responseData["body"] = resolved
			case map[string]interface{}:
				responseData = state.InterpolateMap(v)
				responseData["status_code"] = float64(statusCode)
			default:
				responseData["body"] = v
			}
			bodyResolved = true
		} else {
			log.Printf("[ResponseComponent] No 'body' field found in settings")
		}
	}

	// Formato 4: Fallback — usa dados de entrada
	if !bodyResolved {
		for k, v := range ip.Data {
			responseData[k] = v
		}
	}

	responseData["status_code"] = float64(statusCode)

	// Headers customizados
	if headers, ok := settings["headers"].(map[string]interface{}); ok {
		responseData["headers"] = state.InterpolateMap(headers)
	}

	// ── Tipo de Resposta (json, text, html, xml) ──
	responseType := "json"
	if rt, ok := settings["responseType"].(string); ok {
		responseType = rt
	}
	responseData["response_type"] = responseType

	outIP := ip.CloneWithData(responseData)
	outIP.SourceNode = r.ID()
	outIP.SourcePort = "out"

	return EmitSingle("out", outIP), nil
}

// ──────────────────────────────────────────────────────────────────────────────
// CONDITION COMPONENT
// ──────────────────────────────────────────────────────────────────────────────

type ConditionComponent struct {
	*BaseComponent
}

func NewConditionComponent(id string) *ConditionComponent {
	// Portas de saída dinâmicas serão definidas em tempo de execução via config.routes
	// Por ora, definimos portas base que serão expandidas dinamicamente
	return &ConditionComponent{
		BaseComponent: NewBaseComponent(id, TypeCondition,
			[]PortDefinition{{Name: "in", DataType: "any", Required: true}},
			// Portas mínimas para compatibilidade legada (true/false) + else para fallback
			// Portas route_X são criadas dinamicamente conforme necessário
			[]PortDefinition{
				{Name: "true", DataType: "any", Required: false},
				{Name: "false", DataType: "any", Required: false},
				{Name: "else", DataType: "any", Required: false},
			},
		),
	}
}

func (c *ConditionComponent) Process(ctx context.Context, ip *InformationPacket, state *NexusState) (map[string][]*InformationPacket, error) {
	c.SetStatus(StatusProcessing)
	defer c.SetStatus(StatusSuccess)

	config := c.Config()
	settings := config.Settings

	// ── Roteamento Multi-Canal (Versão Enterprise) ──
	// Se houver "routes" configurados, avalia cada canal em ordem.
	if routes, ok := settings["routes"].([]interface{}); ok && len(routes) > 0 {
		for i, route := range routes {
			r, ok := route.(map[string]interface{})
			if !ok {
				continue
			}

			// Cada route pode ter um array de conditions (AND lógico)
			if matched := c.evaluateRoute(ip, state, r); matched {
				portName := fmt.Sprintf("route_%d", i)
				outIP := ip.Clone()
				outIP.SourceNode = c.ID()
				outIP.SourcePort = portName
				return EmitSingle(portName, outIP), nil
			}
		}

		// Se nenhum canal bateu, vai para o "else"
		outIP := ip.Clone()
		outIP.SourceNode = c.ID()
		outIP.SourcePort = "else"
		return EmitSingle("else", outIP), nil
	}

	// ── Lógica Binária Legada (If/Else Simples) ──
	result := c.evaluateCondition(ip, state, settings)

	outIP := ip.Clone()
	outIP.SourceNode = c.ID()

	if result {
		outIP.SourcePort = "true"
		return EmitSingle("true", outIP), nil
	}

	outIP.SourcePort = "false"
	return EmitSingle("false", outIP), nil
}

// evaluateRoute avalia um canal específico de roteamento.
func (c *ConditionComponent) evaluateRoute(ip *InformationPacket, state *NexusState, route map[string]interface{}) bool {
	conditions, ok := route["conditions"].([]interface{})
	if !ok {
		return false
	}

	for _, cond := range conditions {
		cMap, ok := cond.(map[string]interface{})
		if !ok {
			continue
		}
		field, _ := cMap["column"].(string)
		if field == "" {
			field, _ = cMap["field"].(string)
		}
		op, _ := cMap["operator"].(string)
		val := cMap["value"]

		if !evaluateSingleCondition(ip, state, field, op, val) {
			return false // AND lógico por padrão dentro da rota
		}
	}

	return len(conditions) > 0
}

// evaluateCondition avalia uma expressão condicional.
func (c *ConditionComponent) evaluateCondition(ip *InformationPacket, state *NexusState, settings map[string]interface{}) bool {
	conditions, ok := settings["conditions"].([]interface{})
	if !ok {
		// Fallback: avalia um único campo
		field, _ := settings["field"].(string)
		operator, _ := settings["operator"].(string)
		value := settings["value"]

		return evaluateSingleCondition(ip, state, field, operator, value)
	}

	// Operador lógico entre condições: "and" (padrão) ou "or"
	logicOp, _ := settings["logic"].(string)
	if logicOp == "" {
		logicOp = "and"
	}

	for _, cond := range conditions {
		condMap, ok := cond.(map[string]interface{})
		if !ok {
			continue
		}

		field, _ := condMap["field"].(string)
		operator, _ := condMap["operator"].(string)
		value := condMap["value"]

		result := evaluateSingleCondition(ip, state, field, operator, value)

		if logicOp == "or" && result {
			return true
		}
		if logicOp == "and" && !result {
			return false
		}
	}

	return logicOp == "and"
}

// evaluateSingleCondition avalia uma única condição.
func evaluateSingleCondition(ip *InformationPacket, state *NexusState, field, operator string, expected interface{}) bool {
	// Resolve o valor do campo
	var actual interface{}

	if strings.HasPrefix(field, "{{") {
		// É uma expressão de interpolação ou referência pura
		actual = state.Resolve(field)
	} else if strings.HasPrefix(field, "$") {
		// É uma referência de estado direta
		actual = state.Resolve("{{" + field + "}}")
	} else {
		// É um campo direto do IP
		actual, _ = ip.GetDataField(field)
	}

	// ── Suporte a Matemática Banal ──
	// Se a string contiver operadores matemáticos, tenta avaliar
	if s, ok := actual.(string); ok && (strings.Contains(s, "+") || strings.Contains(s, "-") || strings.Contains(s, "*") || strings.Contains(s, "/")) {
		actual = evaluateMath(s)
	}

	// Resolve o valor esperado se for uma expressão
	if expectedStr, ok := expected.(string); ok && strings.HasPrefix(expectedStr, "{{") {
		expected = state.Resolve(expectedStr)
	}
	if s, ok := expected.(string); ok && (strings.Contains(s, "+") || strings.Contains(s, "-") || strings.Contains(s, "*") || strings.Contains(s, "/")) {
		expected = evaluateMath(s)
	}

	return compareValues(actual, expected, operator)
}

func compareValues(actual, expected interface{}, operator string) bool {
	// Normalização básica de nulos
	if actual == nil {
		actual = ""
	}
	if expected == nil {
		expected = ""
	}

	actualStr := fmt.Sprintf("%v", actual)
	expectedStr := fmt.Sprintf("%v", expected)

	// --- Detecção Inteligente de Tipos ---
	isValidNumber := func(s string) bool {
		if s == "" || s == "<nil>" {
			return false
		}
		s = strings.TrimSpace(s)
		if strings.HasPrefix(s, "{") && strings.HasSuffix(s, "}") && strings.Contains(s, "finite") {
			return true
		}
		_, err := strconv.ParseFloat(s, 64)
		return err == nil
	}

	// Helper para verificar se um valor existe dentro de uma lista (Slice/Array)
	containsValue := func(haystack interface{}, needle interface{}) bool {
		v := reflect.ValueOf(haystack)
		if v.Kind() == reflect.Slice || v.Kind() == reflect.Array {
			needleStr := fmt.Sprintf("%v", needle)
			for i := 0; i < v.Len(); i++ {
				item := v.Index(i).Interface()
				if fmt.Sprintf("%v", item) == needleStr {
					return true
				}
			}
			return false
		}
		// Fallback para string contains se não for slice
		hStr := fmt.Sprintf("%v", haystack)
		nStr := fmt.Sprintf("%v", needle)
		return strings.Contains(hStr, nStr)
	}

	isNumeric := isValidNumber(actualStr) && isValidNumber(expectedStr)
	var aVal, eVal float64
	if isNumeric {
		aVal = toFloat(actual)
		eVal = toFloat(expected)
	}

	switch strings.ToLower(operator) {
	case "equals", "eq", "==":
		if isNumeric {
			return aVal == eVal
		}
		return actualStr == expectedStr

	case "not_equals", "neq", "!=":
		if isNumeric {
			return aVal != eVal
		}
		return actualStr != expectedStr

	case "gt", ">":
		if isNumeric {
			return aVal > eVal
		}
		return actualStr > expectedStr

	case "gte", ">=":
		if isNumeric {
			return aVal >= eVal
		}
		return actualStr >= expectedStr

	case "lt", "<":
		if isNumeric {
			return aVal < eVal
		}
		return actualStr < expectedStr

	case "lte", "<=":
		if isNumeric {
			return aVal <= eVal
		}
		return actualStr <= expectedStr

	case "contains", "contem":
		return containsValue(actual, expected)

	case "not_contains", "nao_contem":
		return !containsValue(actual, expected)

	case "in", "esta_em", "any_of":
		// 'in' é o inverso: verifica se o valor atual está contido na lista esperada
		return containsValue(expected, actual)

	case "not_in", "nao_esta_em", "none_of":
		return !containsValue(expected, actual)

	case "starts_with", "comeca_com":
		return strings.HasPrefix(actualStr, expectedStr)

	case "ends_with", "termina_com":
		return strings.HasSuffix(actualStr, expectedStr)

	case "exists":
		return actual != nil && actualStr != "" && actualStr != "<nil>"

	case "not_exists":
		return actual == nil || actualStr == "" || actualStr == "<nil>"

	case "is_empty":
		return actualStr == "" || actualStr == "<nil>"

	case "is_not_empty":
		return actualStr != "" && actualStr != "<nil>"

	case "is_true":
		return actualStr == "true" || actualStr == "1"

	case "is_false":
		return actualStr == "false" || actualStr == "0"

	case "between":
		var min, max float64
		v := reflect.ValueOf(expected)
		if (v.Kind() == reflect.Slice || v.Kind() == reflect.Array) && v.Len() == 2 {
			min = toFloat(v.Index(0).Interface())
			max = toFloat(v.Index(1).Interface())
		} else {
			parts := strings.Split(expectedStr, ",")
			if len(parts) == 2 {
				min = toFloat(parts[0])
				max = toFloat(parts[1])
			} else {
				return false
			}
		}
		a := toFloat(actual)
		return a >= min && a <= max

	case "matches", "regex":
		matched, _ := regexp.MatchString(expectedStr, actualStr)
		return matched

	default:
		return false
	}
}

// evaluateMath avalia uma expressão matemática simples (+, -, *, /)
func evaluateMath(expr string) float64 {
	// Limpeza básica
	expr = strings.ReplaceAll(expr, " ", "")
	if expr == "" {
		return 0
	}

	// Esta é uma implementação simplificada para "operações banais"
	// Para um motor real, usaríamos um parser completo.
	
	// Tenta converter direto primeiro
	if val, err := strconv.ParseFloat(expr, 64); err == nil {
		return val
	}

	// Ordem inversa para garantir precedência correta (+/- por último)
	if idx := strings.LastIndex(expr, "+"); idx >= 0 {
		return evaluateMath(expr[:idx]) + evaluateMath(expr[idx+1:])
	}
	if idx := strings.LastIndex(expr, "-"); idx >= 0 {
		// Proteção contra números negativos
		if idx > 0 && (expr[idx-1] == '*' || expr[idx-1] == '/' || expr[idx-1] == '+' || expr[idx-1] == '-') {
			// É um sinal de negativo, não operador
		} else {
			return evaluateMath(expr[:idx]) - evaluateMath(expr[idx+1:])
		}
	}
	if idx := strings.LastIndex(expr, "*"); idx >= 0 {
		return evaluateMath(expr[:idx]) * evaluateMath(expr[idx+1:])
	}
	if idx := strings.LastIndex(expr, "/"); idx >= 0 {
		divisor := evaluateMath(expr[idx+1:])
		if divisor == 0 {
			return 0
		}
		return evaluateMath(expr[:idx]) / divisor
	}

	val, _ := strconv.ParseFloat(expr, 64)
	return val
}

// ──────────────────────────────────────────────────────────────────────────────
// TRANSFORM COMPONENT
// ──────────────────────────────────────────────────────────────────────────────

type TransformComponent struct {
	*BaseComponent
}

func NewTransformComponent(id string) *TransformComponent {
	return &TransformComponent{
		BaseComponent: NewBaseComponent(id, TypeTransform,
			[]PortDefinition{{Name: "in", DataType: "any", Required: true}},
			[]PortDefinition{
				{Name: "out", DataType: "any", Required: true},
				{Name: "error", DataType: "error", Required: false},
			},
		),
	}
}

func (t *TransformComponent) Process(ctx context.Context, ip *InformationPacket, state *NexusState) (map[string][]*InformationPacket, error) {
	t.SetStatus(StatusProcessing)
	defer t.SetStatus(StatusSuccess)

	config := t.Config()
	settings := config.Settings

	operation, _ := settings["operation"].(string)

	var resultData map[string]interface{}

	switch operation {
	case "map", "template":
		// Mapeia campos com template
		template, ok := settings["template"].(map[string]interface{})
		if !ok {
			template, ok = settings["body"].(map[string]interface{})
		}
		if ok {
			resultData = state.InterpolateMap(template)
		} else {
			resultData = ip.Data
		}

	case "pick":
		// Seleciona apenas os campos especificados
		fields, ok := settings["fields"].([]interface{})
		if ok {
			resultData = make(map[string]interface{})
			for _, f := range fields {
				field, ok := f.(string)
				if ok {
					if val, exists := ip.Data[field]; exists {
						resultData[field] = val
					}
				}
			}
		}

	case "omit":
		// Remove campos especificados
		fields, ok := settings["fields"].([]interface{})
		resultData = make(map[string]interface{})
		for k, v := range ip.Data {
			resultData[k] = v
		}
		if ok {
			for _, f := range fields {
				field, ok := f.(string)
				if ok {
					delete(resultData, field)
				}
			}
		}

	case "merge":
		// Merge com dados adicionais
		resultData = make(map[string]interface{})
		for k, v := range ip.Data {
			resultData[k] = v
		}
		if extra, ok := settings["merge_data"].(map[string]interface{}); ok {
			interpolated := state.InterpolateMap(extra)
			for k, v := range interpolated {
				resultData[k] = v
			}
		}

	case "set":
		// Define/sobrescreve campos
		resultData = make(map[string]interface{})
		for k, v := range ip.Data {
			resultData[k] = v
		}
		if setFields, ok := settings["set"].(map[string]interface{}); ok {
			interpolated := state.InterpolateMap(setFields)
			for k, v := range interpolated {
				resultData[k] = v
			}
		}

	default:
		// Sem operação especificada, aplica template se disponível
		if template, ok := settings["template"].(map[string]interface{}); ok {
			resultData = state.InterpolateMap(template)
		} else if body, ok := settings["body"].(map[string]interface{}); ok {
			resultData = state.InterpolateMap(body)
		} else {
			resultData = ip.Data
		}
	}

	outIP := ip.CloneWithData(resultData)
	outIP.SourceNode = t.ID()
	outIP.SourcePort = "out"

	return EmitSingle("out", outIP), nil
}

// ──────────────────────────────────────────────────────────────────────────────
// SWITCH COMPONENT
// ──────────────────────────────────────────────────────────────────────────────

type SwitchComponent struct {
	*BaseComponent
}

func NewSwitchComponent(id string) *SwitchComponent {
	return &SwitchComponent{
		BaseComponent: NewBaseComponent(id, TypeSwitch,
			[]PortDefinition{{Name: "in", DataType: "any", Required: true}},
			[]PortDefinition{
				{Name: "case_0", DataType: "any", Required: false},
				{Name: "case_1", DataType: "any", Required: false},
				{Name: "case_2", DataType: "any", Required: false},
				{Name: "case_3", DataType: "any", Required: false},
				{Name: "default", DataType: "any", Required: false},
			},
		),
	}
}

func (s *SwitchComponent) Process(ctx context.Context, ip *InformationPacket, state *NexusState) (map[string][]*InformationPacket, error) {
	s.SetStatus(StatusProcessing)
	defer s.SetStatus(StatusSuccess)

	config := s.Config()
	settings := config.Settings

	// Campo a avaliar
	fieldName, _ := settings["field"].(string)
	var fieldValue interface{}

	if strings.HasPrefix(fieldName, "{{") {
		fieldValue = state.Interpolate(fieldName)
	} else {
		fieldValue, _ = ip.GetDataField(fieldName)
	}

	fieldStr := fmt.Sprintf("%v", fieldValue)

	// Cases
	cases, ok := settings["cases"].([]interface{})
	if !ok {
		outIP := ip.Clone()
		outIP.SourceNode = s.ID()
		outIP.SourcePort = "default"
		return EmitSingle("default", outIP), nil
	}

	for i, c := range cases {
		caseMap, ok := c.(map[string]interface{})
		if !ok {
			continue
		}

		caseValue := fmt.Sprintf("%v", caseMap["value"])
		if fieldStr == caseValue {
			portName := fmt.Sprintf("case_%d", i)
			outIP := ip.Clone()
			outIP.SourceNode = s.ID()
			outIP.SourcePort = portName
			return EmitSingle(portName, outIP), nil
		}
	}

	// Default
	outIP := ip.Clone()
	outIP.SourceNode = s.ID()
	outIP.SourcePort = "default"
	return EmitSingle("default", outIP), nil
}

// ──────────────────────────────────────────────────────────────────────────────
// MERGE COMPONENT
// ──────────────────────────────────────────────────────────────────────────────

type MergeComponent struct {
	*BaseComponent
}

func NewMergeComponent(id string) *MergeComponent {
	return &MergeComponent{
		BaseComponent: NewBaseComponent(id, TypeMerge,
			[]PortDefinition{
				{Name: "in_0", DataType: "any", Required: true},
				{Name: "in_1", DataType: "any", Required: true},
			},
			[]PortDefinition{
				{Name: "out", DataType: "any", Required: true},
			},
		),
	}
}

func (m *MergeComponent) Process(ctx context.Context, ip *InformationPacket, state *NexusState) (map[string][]*InformationPacket, error) {
	m.SetStatus(StatusProcessing)
	defer m.SetStatus(StatusSuccess)

	// Merge combina dados de entrada em um único pacote
	mergedData := make(map[string]interface{})
	for k, v := range ip.Data {
		mergedData[k] = v
	}

	outIP := ip.CloneWithData(mergedData)
	outIP.SourceNode = m.ID()
	outIP.SourcePort = "out"

	return EmitSingle("out", outIP), nil
}

// ──────────────────────────────────────────────────────────────────────────────
// SPLIT COMPONENT
// ──────────────────────────────────────────────────────────────────────────────

type SplitComponent struct {
	*BaseComponent
}

func NewSplitComponent(id string) *SplitComponent {
	return &SplitComponent{
		BaseComponent: NewBaseComponent(id, TypeSplit,
			[]PortDefinition{{Name: "in", DataType: "array", Required: true}},
			[]PortDefinition{{Name: "item", DataType: "any", Required: true}},
		),
	}
}

func (s *SplitComponent) Process(ctx context.Context, ip *InformationPacket, state *NexusState) (map[string][]*InformationPacket, error) {
	s.SetStatus(StatusProcessing)
	defer s.SetStatus(StatusSuccess)

	config := s.Config()
	settings := config.Settings

	// Campo que contém o array
	fieldName, _ := settings["field"].(string)
	if fieldName == "" {
		fieldName = "items"
	}

	arrayData, ok := ip.Data[fieldName]
	if !ok {
		return EmitEmpty(), nil
	}

	items, ok := arrayData.([]interface{})
	if !ok {
		// Tenta como array de maps
		return EmitSingle("item", ip), nil
	}

	// Limite de itens
	maxItems := MaxForeachItems
	if max, ok := settings["max_items"].(float64); ok {
		maxItems = int(max)
	}
	if len(items) > maxItems {
		return nil, fmt.Errorf("nexus: split exceeds max items (%d/%d)", len(items), maxItems)
	}

	// Emite um pacote por item
	packets := make([]*InformationPacket, 0, len(items))
	for i, item := range items {
		itemIP := ip.CloneForForeach(i)
		itemIP.SourceNode = s.ID()
		itemIP.SourcePort = "item"

		// Converte o item para map se possível
		switch v := item.(type) {
		case map[string]interface{}:
			itemIP.Data = v
		default:
			itemIP.Data = map[string]interface{}{"value": v, "index": i}
		}

		packets = append(packets, itemIP)
	}

	return EmitMultiple("item", packets), nil
}

// ──────────────────────────────────────────────────────────────────────────────
// ERROR HANDLER COMPONENT
// ──────────────────────────────────────────────────────────────────────────────

type ErrorHandlerComponent struct {
	*BaseComponent
}

func NewErrorHandlerComponent(id string) *ErrorHandlerComponent {
	return &ErrorHandlerComponent{
		BaseComponent: NewBaseComponent(id, TypeErrorHandler,
			[]PortDefinition{{Name: "in", DataType: "error", Required: true}},
			[]PortDefinition{{Name: "handled", DataType: "any", Required: false}},
		),
	}
}

func (e *ErrorHandlerComponent) Process(ctx context.Context, ip *InformationPacket, state *NexusState) (map[string][]*InformationPacket, error) {
	e.SetStatus(StatusProcessing)
	defer e.SetStatus(StatusSuccess)

	config := e.Config()
	settings := config.Settings

	// Estratégia de tratamento
	strategy, _ := settings["strategy"].(string)

	switch strategy {
	case "log_and_continue":
		// Loga o erro e continua com dados de fallback
		fallbackData := make(map[string]interface{})
		if fb, ok := settings["fallback_data"].(map[string]interface{}); ok {
			fallbackData = state.InterpolateMap(fb)
		} else {
			fallbackData["error"] = ip.Data["error"]
			fallbackData["handled"] = true
		}

		outIP := ip.CloneWithData(fallbackData)
		outIP.SourceNode = e.ID()
		outIP.SourcePort = "handled"
		return EmitSingle("handled", outIP), nil

	case "retry":
		// Retry é tratado pela política de retry do nó que falhou
		return EmitEmpty(), nil

	case "default_response":
		// Retorna resposta padrão
		defaultData := make(map[string]interface{})
		if def, ok := settings["default_response"].(map[string]interface{}); ok {
			defaultData = state.InterpolateMap(def)
		} else {
			defaultData["message"] = "An error occurred"
			defaultData["status_code"] = float64(500)
		}

		outIP := ip.CloneWithData(defaultData)
		outIP.SourceNode = e.ID()
		outIP.SourcePort = "handled"
		return EmitSingle("handled", outIP), nil

	default:
		// Propaga o erro
		return EmitEmpty(), fmt.Errorf("error in upstream node: %v", ip.Data["error"])
	}
}

// ============================================================================
// UTILITY FUNCTIONS
// ============================================================================

func toFloat(v interface{}) float64 {
	switch val := v.(type) {
	case float64:
		return val
	case float32:
		return float64(val)
	case int:
		return float64(val)
	case int64:
		return float64(val)
	case string:
		// Limpeza de representação de structs (ex: Decimal do DB)
		if strings.HasPrefix(val, "{") && strings.HasSuffix(val, "}") && strings.Contains(val, "finite") {
			parts := strings.Fields(strings.Trim(val, "{}"))
			if len(parts) > 0 {
				val = parts[0]
			}
		}
		f, _ := strconv.ParseFloat(val, 64)
		return f
	default:
		// Tenta via string como último recurso
		s := fmt.Sprintf("%v", val)
		if strings.HasPrefix(s, "{") && strings.HasSuffix(s, "}") && strings.Contains(s, "finite") {
			parts := strings.Fields(strings.Trim(s, "{}"))
			if len(parts) > 0 {
				f, _ := strconv.ParseFloat(parts[0], 64)
				return f
			}
		}
		f, _ := strconv.ParseFloat(s, 64)
		return f
	}
}
