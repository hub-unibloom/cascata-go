package services

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// InlineFieldLogicRule representa uma regra simples da pipeline de lógica por campo.
type InlineFieldLogicRule struct {
	Left  string
	Op    string
	Right interface{}
}

// InlineFieldLogicPipeline representa a pipeline de lógica salva no config do nó.
type InlineFieldLogicPipeline struct {
	Match     string
	Rules     []InlineFieldLogicRule
	WhenTrue  interface{}
	WhenFalse interface{}
}

func parseFieldExpressions(config map[string]interface{}) map[string]string {
	result := map[string]string{}
	raw, ok := config["_fieldExpressions"].(map[string]interface{})
	if !ok {
		return result
	}
	for field, v := range raw {
		if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
			result[field] = s
		}
	}
	return result
}

func parseFieldLogicPipelines(config map[string]interface{}) map[string]InlineFieldLogicPipeline {
	result := map[string]InlineFieldLogicPipeline{}
	raw, ok := config["_fieldLogicPipelines"].(map[string]interface{})
	if !ok {
		return result
	}

	for field, v := range raw {
		pMap, ok := v.(map[string]interface{})
		if !ok {
			continue
		}

		pipeline := InlineFieldLogicPipeline{
			Match: "all",
			Rules: []InlineFieldLogicRule{},
		}

		if match, ok := pMap["match"].(string); ok && strings.ToLower(match) == "any" {
			pipeline.Match = "any"
		}
		if wt, ok := pMap["whenTrue"]; ok {
			pipeline.WhenTrue = wt
		}
		if wf, ok := pMap["whenFalse"]; ok {
			pipeline.WhenFalse = wf
		}

		if rules, ok := pMap["rules"].([]interface{}); ok {
			for _, rv := range rules {
				rMap, ok := rv.(map[string]interface{})
				if !ok {
					continue
				}
				r := InlineFieldLogicRule{
					Op: "eq",
				}
				if left, ok := rMap["left"].(string); ok {
					r.Left = left
				}
				if op, ok := rMap["op"].(string); ok && op != "" {
					r.Op = op
				}
				if right, ok := rMap["right"]; ok {
					r.Right = right
				}
				pipeline.Rules = append(pipeline.Rules, r)
			}
		}

		result[field] = pipeline
	}

	return result
}

func buildIndexedFieldPathMap(config map[string]interface{}, listKey, keyField string, withLegacyNumeric bool) map[string]string {
	result := map[string]string{}
	raw, ok := config[listKey].([]interface{})
	if !ok {
		return result
	}

	for i, item := range raw {
		m, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		key, ok := m[keyField].(string)
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}

		fieldPath := fmt.Sprintf("%s.%d.value", listKey, i)
		result[fieldPath] = key
		if withLegacyNumeric {
			result[strconv.Itoa(i)] = key
		}
	}

	return result
}

func mergeFieldPathMaps(parts ...map[string]string) map[string]string {
	result := map[string]string{}
	for _, p := range parts {
		for k, v := range p {
			result[k] = v
		}
	}
	return result
}

func applyFieldPipelinesToBodyMap(
	bodyMap map[string]interface{},
	fieldPathToBodyKey map[string]string,
	fieldExpressions map[string]string,
	fieldLogicPipelines map[string]InlineFieldLogicPipeline,
	ctx *FlowContext,
) map[string]interface{} {
	if bodyMap == nil || len(bodyMap) == 0 {
		return bodyMap
	}

	for fieldPath, bodyKey := range fieldPathToBodyKey {
		current, exists := bodyMap[bodyKey]
		if !exists {
			continue
		}
		bodyMap[bodyKey] = applyFieldPipelineValue(fieldPath, current, fieldExpressions, fieldLogicPipelines, ctx)
	}

	return bodyMap
}

func applyFieldPipelineValue(
	fieldPath string,
	current interface{},
	fieldExpressions map[string]string,
	fieldLogicPipelines map[string]InlineFieldLogicPipeline,
	ctx *FlowContext,
) interface{} {
	result := current

	if expr, ok := fieldExpressions[fieldPath]; ok && strings.TrimSpace(expr) != "" {
		result = evaluateInlineExpression(expr, result, ctx)
	}

	if pipeline, ok := fieldLogicPipelines[fieldPath]; ok {
		result = evaluateInlineLogicPipeline(pipeline, result, ctx)
	}

	return result
}

func evaluateInlineExpression(expression string, current interface{}, ctx *FlowContext) interface{} {
	expr := strings.ReplaceAll(expression, "{{current}}", fmt.Sprintf("%v", current))
	resolved := resolveStringCtx(expr, ctx)
	resolved = strings.TrimSpace(resolved)
	if resolved == "" {
		return ""
	}

	parser := NewMathParser()
	if number, err := parser.Evaluate(resolved, map[string]float64{}); err == nil {
		if number == float64(int64(number)) {
			return int64(number)
		}
		return number
	}

	if b, err := strconv.ParseBool(strings.ToLower(resolved)); err == nil {
		return b
	}
	if i, err := strconv.ParseInt(resolved, 10, 64); err == nil {
		return i
	}
	if f, err := strconv.ParseFloat(resolved, 64); err == nil {
		return f
	}

	return resolved
}

func evaluateInlineLogicPipeline(pipeline InlineFieldLogicPipeline, current interface{}, ctx *FlowContext) interface{} {
	if len(pipeline.Rules) == 0 {
		return current
	}

	matchAll := strings.ToLower(pipeline.Match) != "any"
	result := matchAll

	for _, rule := range pipeline.Rules {
		left := resolvePipelineOperand(rule.Left, current, ctx)
		right := resolvePipelineOperand(rule.Right, current, ctx)
		ok := comparePipelineValues(left, rule.Op, right)

		if matchAll && !ok {
			result = false
			break
		}
		if !matchAll && ok {
			result = true
			break
		}
		if !matchAll {
			result = false
		}
	}

	if result {
		if pipeline.WhenTrue == nil {
			return current
		}
		return resolvePipelineOperand(pipeline.WhenTrue, current, ctx)
	}

	if pipeline.WhenFalse == nil {
		return current
	}
	return resolvePipelineOperand(pipeline.WhenFalse, current, ctx)
}

func resolvePipelineOperand(raw interface{}, current interface{}, ctx *FlowContext) interface{} {
	switch v := raw.(type) {
	case nil:
		return nil
	case string:
		if v == "$current" || v == "current" {
			return current
		}

		s := strings.ReplaceAll(v, "{{current}}", fmt.Sprintf("%v", current))
		sTrim := strings.TrimSpace(s)
		if sTrim == "" {
			return ""
		}

		if strings.Contains(s, "{{") && strings.Contains(s, "}}") {
			s = resolveStringCtx(s, ctx)
			sTrim = strings.TrimSpace(s)
		}

		if looksLikeVarPath(sTrim) {
			if val := resolveVarCtx(sTrim, ctx); val != nil {
				return val
			}
		}

		if b, err := strconv.ParseBool(strings.ToLower(sTrim)); err == nil {
			return b
		}
		if i, err := strconv.ParseInt(sTrim, 10, 64); err == nil {
			return i
		}
		if f, err := strconv.ParseFloat(sTrim, 64); err == nil {
			return f
		}

		return s
	default:
		return resolveTemplateCtx(v, ctx)
	}
}

func looksLikeVarPath(s string) bool {
	if s == "$input" || s == "$output" || s == "input" || s == "output" {
		return true
	}
	if strings.HasPrefix(s, "slot:") {
		return true
	}
	if strings.HasPrefix(s, "trigger.") || strings.HasPrefix(s, "node_") {
		return true
	}
	// Formatos do tipo node_1.data.total
	pathLike := regexp.MustCompile(`^[a-zA-Z0-9_]+(\.[a-zA-Z0-9_]+)+$`)
	return pathLike.MatchString(s)
}

func comparePipelineValues(left interface{}, op string, right interface{}) bool {
	leftStr := fmt.Sprintf("%v", left)
	rightStr := fmt.Sprintf("%v", right)

	switch strings.ToLower(strings.TrimSpace(op)) {
	case "eq", "equals", "=", "==":
		return leftStr == rightStr
	case "neq", "not_equals", "!=", "<>":
		return leftStr != rightStr
	case "gt", "greater_than", ">":
		leftNum, lErr := strconv.ParseFloat(leftStr, 64)
		rightNum, rErr := strconv.ParseFloat(rightStr, 64)
		return lErr == nil && rErr == nil && leftNum > rightNum
	case "lt", "less_than", "<":
		leftNum, lErr := strconv.ParseFloat(leftStr, 64)
		rightNum, rErr := strconv.ParseFloat(rightStr, 64)
		return lErr == nil && rErr == nil && leftNum < rightNum
	case "gte", "greater_than_or_equal", ">=", "=>":
		leftNum, lErr := strconv.ParseFloat(leftStr, 64)
		rightNum, rErr := strconv.ParseFloat(rightStr, 64)
		return lErr == nil && rErr == nil && leftNum >= rightNum
	case "lte", "less_than_or_equal", "<=", "=<":
		leftNum, lErr := strconv.ParseFloat(leftStr, 64)
		rightNum, rErr := strconv.ParseFloat(rightStr, 64)
		return lErr == nil && rErr == nil && leftNum <= rightNum
	case "contains":
		return strings.Contains(strings.ToLower(leftStr), strings.ToLower(rightStr))
	case "starts_with", "startswith", "starts":
		return strings.HasPrefix(leftStr, rightStr)
	case "ends_with", "endswith", "ends":
		return strings.HasSuffix(leftStr, rightStr)
	case "regex":
		re, err := regexp.Compile(rightStr)
		return err == nil && re.MatchString(leftStr)
	case "is_empty", "isempty", "empty":
		return isPipelineEmpty(left)
	default:
		return leftStr == rightStr
	}
}

func isPipelineEmpty(v interface{}) bool {
	if v == nil {
		return true
	}

	switch typed := v.(type) {
	case string:
		return strings.TrimSpace(typed) == ""
	case []interface{}:
		return len(typed) == 0
	case map[string]interface{}:
		return len(typed) == 0
	default:
		return fmt.Sprintf("%v", typed) == ""
	}
}

func resolveFilterBaseValue(raw string, ctx *FlowContext) interface{} {
	if strings.Contains(raw, "{{") && strings.Contains(raw, "}}") {
		return resolveStringCtx(raw, ctx)
	}
	if val := resolveVarCtx(raw, ctx); val != nil {
		return val
	}
	return raw
}
