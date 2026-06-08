package services

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// MathNodeExecutor executes mathematical expressions with variable resolution
type MathNodeExecutor struct {
	parser     *MathParser
	config     map[string]interface{}
	OutputSlot int
	ErrorSlot  int
}

// GetOutputSlot retorna o slot de saída
func (e *MathNodeExecutor) GetOutputSlot() int { return e.OutputSlot }

// GetErrorSlot retorna o slot de erro
func (e *MathNodeExecutor) GetErrorSlot() int { return e.ErrorSlot }

// Execute runs a mathematical expression node
func (e *MathNodeExecutor) Execute(ctx *FlowContext) error {
	config := e.config
	if config == nil {
		ctx.Vars[e.OutputSlot] = map[string]interface{}{"__error": true, "message": "no config"}
		return fmt.Errorf("math node requires config")
	}
	
	// Get expression from config
	expression, ok := config["expression"].(string)
	if !ok || expression == "" {
		ctx.Vars[e.OutputSlot] = map[string]interface{}{"__error": true, "message": "no expression"}
		return fmt.Errorf("math node requires an expression")
	}

	// Extract variables from expression
	variables := e.parser.ExtractVariables(expression)

	// Build variable map by resolving each variable
	varMap := make(map[string]float64)
	for _, varName := range variables {
		val, err := e.resolveVariable(varName, ctx)
		if err != nil {
			ctx.Vars[e.OutputSlot] = map[string]interface{}{"__error": true, "message": fmt.Sprintf("failed to resolve variable %s: %v", varName, err)}
			return fmt.Errorf("failed to resolve variable %s: %w", varName, err)
		}
		varMap[varName] = val
	}

	// Evaluate expression
	result, err := e.parser.Evaluate(expression, varMap)
	if err != nil {
		ctx.Vars[e.OutputSlot] = map[string]interface{}{"__error": true, "message": fmt.Sprintf("math evaluation failed: %v", err)}
		return fmt.Errorf("math evaluation failed: %w", err)
	}

	// Apply precision if specified
	if precision, ok := config["precision"].(float64); ok {
		result = e.applyPrecision(result, int(precision))
	}

	// Handle output format
	outputFormat, _ := config["output_format"].(string)
	switch outputFormat {
	case "int", "integer":
		ctx.Vars[e.OutputSlot] = int64(math.Round(result))
	case "float":
		ctx.Vars[e.OutputSlot] = result
	case "string":
		ctx.Vars[e.OutputSlot] = fmt.Sprintf("%g", result)
	default:
		// Auto-detect: return int if no decimal, otherwise float
		if result == math.Trunc(result) {
			ctx.Vars[e.OutputSlot] = int64(result)
		} else {
			ctx.Vars[e.OutputSlot] = result
		}
	}

	return nil
}

// resolveVariable resolves a variable reference like "node_1.data.valor" or "trigger.data.price"
func (e *MathNodeExecutor) resolveVariable(varName string, ctx *FlowContext) (float64, error) {
	parts := strings.Split(varName, ".")
	if len(parts) < 2 {
		// Try direct value from context vars usando resolveVarCtx
		val := resolveVarCtx(varName, ctx)
		if val != nil {
			return e.toFloat64(val)
		}
		return 0, fmt.Errorf("invalid variable reference: %s", varName)
	}

	// Usar resolveVarCtx que entende o formato "node_1.data.xxx", "trigger.data.xxx" ou "slot:N"
	val := resolveVarCtx(varName, ctx)
	if val == nil {
		return 0, fmt.Errorf("variable %s not found", varName)
	}

	return e.toFloat64(val)
}

// getNestedValue retrieves a nested value from a map using dot notation
func (e *MathNodeExecutor) getNestedValue(data map[string]interface{}, path string) (interface{}, error) {
	parts := strings.Split(path, ".")
	current := interface{}(data)
	
	for _, part := range parts {
		switch v := current.(type) {
		case map[string]interface{}:
			val, ok := v[part]
			if !ok {
				return nil, fmt.Errorf("field %s not found", part)
			}
			current = val
		case []interface{}:
			// Try to access array index
			idx, err := strconv.Atoi(part)
			if err != nil || idx < 0 || idx >= len(v) {
				return nil, fmt.Errorf("invalid array index: %s", part)
			}
			current = v[idx]
		default:
			return nil, fmt.Errorf("cannot access %s on non-object", part)
		}
	}
	
	return current, nil
}

// toFloat64 converts various types to float64
func (e *MathNodeExecutor) toFloat64(val interface{}) (float64, error) {
	switch v := val.(type) {
	case float64:
		return v, nil
	case float32:
		return float64(v), nil
	case int:
		return float64(v), nil
	case int8:
		return float64(v), nil
	case int16:
		return float64(v), nil
	case int32:
		return float64(v), nil
	case int64:
		return float64(v), nil
	case uint:
		return float64(v), nil
	case uint8:
		return float64(v), nil
	case uint16:
		return float64(v), nil
	case uint32:
		return float64(v), nil
	case uint64:
		return float64(v), nil
	case string:
		// Try to parse as number
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return 0, fmt.Errorf("cannot convert string '%s' to number", v)
		}
		return f, nil
	case bool:
		if v {
			return 1, nil
		}
		return 0, nil
	case json.Number:
		f, err := v.Float64()
		if err != nil {
			return 0, err
		}
		return f, nil
	case nil:
		return 0, nil
	default:
		return 0, fmt.Errorf("cannot convert type %T to number", val)
	}
}

// applyPrecision rounds to specified decimal places
func (e *MathNodeExecutor) applyPrecision(val float64, precision int) float64 {
	if precision < 0 {
		return val
	}
	multiplier := math.Pow(10, float64(precision))
	return math.Round(val*multiplier) / multiplier
}

// ValidateConfig validates math node configuration
func (e *MathNodeExecutor) ValidateConfig(config map[string]interface{}) error {
	expression, ok := config["expression"].(string)
	if !ok || expression == "" {
		return fmt.Errorf("expression is required")
	}
	
	// Validate expression syntax
	if err := e.parser.ValidateExpression(expression); err != nil {
		return fmt.Errorf("invalid expression: %w", err)
	}
	
	// Validate precision if provided
	if precision, ok := config["precision"]; ok {
		switch p := precision.(type) {
		case float64:
			if p < 0 || p > 15 {
				return fmt.Errorf("precision must be between 0 and 15")
			}
		case int:
			if p < 0 || p > 15 {
				return fmt.Errorf("precision must be between 0 and 15")
			}
		default:
			return fmt.Errorf("precision must be a number")
		}
	}
	
	// Validate output format
	if format, ok := config["output_format"].(string); ok {
		validFormats := map[string]bool{
			"int":     true,
			"integer": true,
			"float":   true,
			"string":  true,
			"auto":    true,
			"":        true,
		}
		if !validFormats[format] {
			return fmt.Errorf("invalid output_format: %s", format)
		}
	}
	
	return nil
}

// BuildMathNode cria um MathNodeExecutor a partir da configuração
func BuildMathNode(config map[string]interface{}, compiler *Compiler) (NodeExecutor, error) {
	node := &MathNodeExecutor{
		parser:     NewMathParser(),
		config:     config,
		OutputSlot: compiler.allocSlot(),
		ErrorSlot:  -1,
	}

	if err := node.ValidateConfig(config); err != nil {
		return nil, err
	}

	return node, nil
}
