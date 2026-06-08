package services

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// ComputedService handles formula evaluation for computed columns
type ComputedService struct{}

// NewComputedService creates a new computed service instance
func NewComputedService() *ComputedService {
	return &ComputedService{}
}

// EvaluateFormula evaluates a formula string with given row data
// Formula syntax: {{field_name}} for variable substitution
// Supports: +, -, *, /, SUM, UPPER, LOWER, ROUND, ABS, CONCAT, IF
func (s *ComputedService) EvaluateFormula(formula string, rowData map[string]interface{}) (interface{}, error) {
	if formula == "" {
		return nil, nil
	}

	// Replace {{field}} placeholders with actual values
	expr := s.resolvePlaceholders(formula, rowData)

	// Check if it's a function call
	if fnResult, ok := s.evaluateFunction(expr, rowData); ok {
		return fnResult, nil
	}

	// Evaluate as mathematical expression
	result, err := s.evaluateMath(expr)
	if err != nil {
		// If math evaluation fails, return the resolved string
		return expr, nil
	}

	return result, nil
}

// resolvePlaceholders replaces {{field}} with actual values from rowData
func (s *ComputedService) resolvePlaceholders(formula string, rowData map[string]interface{}) string {
	re := regexp.MustCompile(`\{\{(\w+)\}\}`)
	return re.ReplaceAllStringFunc(formula, func(match string) string {
		field := strings.Trim(match, "{}")
		if val, exists := rowData[field]; exists {
			return fmt.Sprintf("%v", val)
		}
		return "0"
	})
}

// evaluateFunction checks and executes supported functions
func (s *ComputedService) evaluateFunction(expr string, rowData map[string]interface{}) (interface{}, bool) {
	expr = strings.TrimSpace(expr)
	upperExpr := strings.ToUpper(expr)

	// SUM(field1, field2, ...)
	if strings.HasPrefix(upperExpr, "SUM(") && strings.HasSuffix(expr, ")") {
		args := s.extractArgs(expr[4 : len(expr)-1])
		total := 0.0
		for _, arg := range args {
			val := s.toFloat(arg)
			total += val
		}
		return total, true
	}

	// UPPER(field)
	if strings.HasPrefix(upperExpr, "UPPER(") && strings.HasSuffix(expr, ")") {
		arg := expr[6 : len(expr)-1]
		return strings.ToUpper(arg), true
	}

	// LOWER(field)
	if strings.HasPrefix(upperExpr, "LOWER(") && strings.HasSuffix(expr, ")") {
		arg := expr[6 : len(expr)-1]
		return strings.ToLower(arg), true
	}

	// ROUND(field, decimals)
	if strings.HasPrefix(upperExpr, "ROUND(") && strings.HasSuffix(expr, ")") {
		args := s.extractArgs(expr[6 : len(expr)-1])
		if len(args) >= 1 {
			val := s.toFloat(args[0])
			decimals := 2
			if len(args) >= 2 {
				decimals = int(s.toFloat(args[1]))
			}
			factor := 1.0
			for i := 0; i < decimals; i++ {
				factor *= 10
			}
			return float64(int(val*factor+0.5)) / factor, true
		}
	}

	// ABS(field)
	if strings.HasPrefix(upperExpr, "ABS(") && strings.HasSuffix(expr, ")") {
		arg := expr[4 : len(expr)-1]
		val := s.toFloat(arg)
		if val < 0 {
			return -val, true
		}
		return val, true
	}

	// CONCAT(field1, field2, ...)
	if strings.HasPrefix(upperExpr, "CONCAT(") && strings.HasSuffix(expr, ")") {
		args := s.extractArgs(expr[7 : len(expr)-1])
		result := ""
		for _, arg := range args {
			result += arg
		}
		return result, true
	}

	// PARSE_NUMBER(value) - Extracts number from currency/text
	if strings.HasPrefix(upperExpr, "PARSE_NUMBER(") && strings.HasSuffix(expr, ")") {
		arg := expr[13 : len(expr)-1]
		return s.toFloat(arg), true
	}

	// CURRENCY(value, symbol, locale) - Format number as currency
	// Examples: CURRENCY(49.90, "R$", "BR") -> "R$ 49,90"
	//           CURRENCY(100.50, "$", "US") -> "$100.50"
	if strings.HasPrefix(upperExpr, "CURRENCY(") && strings.HasSuffix(expr, ")") {
		args := s.extractArgs(expr[9 : len(expr)-1])
		if len(args) >= 2 {
			val := s.toFloat(args[0])
			symbol := strings.Trim(args[1], `"'`)
			locale := "US"
			if len(args) >= 3 {
				locale = strings.Trim(args[2], `"'`)
			}
			return s.formatCurrency(val, symbol, locale), true
		}
	}

	// PARSE_MONEY(value, currency) - Parse money string to number
	// Example: PARSE_MONEY("R$ 1.234,56", "BRL") -> 1234.56
	if strings.HasPrefix(upperExpr, "PARSE_MONEY(") && strings.HasSuffix(expr, ")") {
		args := s.extractArgs(expr[12 : len(expr)-1])
		if len(args) >= 1 {
			return s.toFloat(args[0]), true
		}
	}

	// IF(condition, trueVal, falseVal)
	if strings.HasPrefix(upperExpr, "IF(") && strings.HasSuffix(expr, ")") {
		args := s.extractArgs(expr[3 : len(expr)-1])
		if len(args) >= 3 {
			condition := s.evaluateCondition(args[0])
			if condition {
				// Try to return as number if possible
				if val := s.toFloat(args[1]); val != 0 || args[1] == "0" || args[1] == "0.0" {
					return val, true
				}
				return args[1], true
			}
			if val := s.toFloat(args[2]); val != 0 || args[2] == "0" || args[2] == "0.0" {
				return val, true
			}
			return args[2], true
		}
	}

	return nil, false
}

// formatCurrency formats a number as currency string
func (s *ComputedService) formatCurrency(val float64, symbol string, locale string) string {
	switch locale {
	case "BR", "PT", "DE", "FR", "IT", "ES": // European/Brazilian format
		// Format: symbol 1.234,56 or symbol 1,00
		whole := int64(val)
		fraction := int64((val - float64(whole)) * 100)
		if fraction < 0 {
			fraction = -fraction
		}
		
		// Format with thousand separators
		wholeStr := fmt.Sprintf("%d", whole)
		if whole < 0 {
			wholeStr = wholeStr[1:] // Remove negative sign for formatting
		}
		
		// Add dots as thousand separators
		parts := []string{}
		for i := len(wholeStr); i > 0; i -= 3 {
			start := i - 3
			if start < 0 {
				start = 0
			}
			parts = append([]string{wholeStr[start:i]}, parts...)
		}
		formatted := strings.Join(parts, ".")
		
		if whole < 0 {
			formatted = "-" + formatted
		}
		
		return fmt.Sprintf("%s %s,%02d", symbol, formatted, fraction)
		
	default: // US/UK format
		return fmt.Sprintf("%s%.2f", symbol, val)
	}
}

// evaluateCondition evaluates a simple condition like "field > 10" or "a = b"
func (s *ComputedService) evaluateCondition(condition string) bool {
	condition = strings.TrimSpace(condition)
	
	// Extract operator and operands
	operators := []string{">=", "<=", "!=", "=", ">", "<"}
	for _, op := range operators {
		if idx := strings.Index(condition, op); idx >= 0 {
			left := strings.TrimSpace(condition[:idx])
			right := strings.TrimSpace(condition[idx+len(op):])
			
			// Try numeric comparison
			leftVal := s.toFloat(left)
			rightVal := s.toFloat(right)
			
			// If both are numeric, do numeric comparison
			if leftVal != 0 || rightVal != 0 || left == "0" || right == "0" || left == "0.0" || right == "0.0" {
				switch op {
				case ">=":
					return leftVal >= rightVal
				case "<=":
					return leftVal <= rightVal
				case "!=":
					return leftVal != rightVal
				case "=":
					return leftVal == rightVal
				case ">":
					return leftVal > rightVal
				case "<":
					return leftVal < rightVal
				}
			}
			
			// String comparison
			switch op {
			case "=":
				return left == right
			case "!=":
				return left != right
			default:
				return false
			}
		}
	}
	
	// Check for boolean-like values
	upper := strings.ToUpper(condition)
	if upper == "TRUE" || upper == "YES" || upper == "1" {
		return true
	}
	if upper == "FALSE" || upper == "NO" || upper == "0" || upper == "" {
		return false
	}
	
	// Non-empty string is truthy
	return condition != ""
}

// extractArgs extracts comma-separated arguments
func (s *ComputedService) extractArgs(argsStr string) []string {
	args := []string{}
	current := ""
	depth := 0

	for _, ch := range argsStr {
		if ch == '(' {
			depth++
			current += string(ch)
		} else if ch == ')' {
			depth--
			current += string(ch)
		} else if ch == ',' && depth == 0 {
			args = append(args, strings.TrimSpace(current))
			current = ""
		} else {
			current += string(ch)
		}
	}

	if current != "" {
		args = append(args, strings.TrimSpace(current))
	}
	return args
}

// toFloat converts string to float64 (intelligent parsing for currency formats)
func (s *ComputedService) toFloat(str string) float64 {
	str = strings.TrimSpace(str)
	
	// Try direct parsing first
	val, err := strconv.ParseFloat(str, 64)
	if err == nil {
		return val
	}
	
	// Intelligent currency parsing - handle formats like "R$ 49,90", "$100.50", "€1.234,56"
	// Remove currency symbols and normalize separators
	currencySymbols := []string{"R$", "$", "€", "£", "¥", "BTC", "ETH"}
	cleanStr := str
	
	for _, symbol := range currencySymbols {
		cleanStr = strings.ReplaceAll(cleanStr, symbol, "")
	}
	cleanStr = strings.TrimSpace(cleanStr)
	
	// Detect format: if contains comma as decimal separator (European/Brazilian)
	// Pattern: digits,digits (exactly 2 digits after comma) -> decimal comma
	if matched, _ := regexp.MatchString(`\d,\d{2}$`, cleanStr); matched {
		// European format: 1.234,56 or 1234,56
		cleanStr = strings.ReplaceAll(cleanStr, ".", "")  // Remove thousand separators
		cleanStr = strings.ReplaceAll(cleanStr, ",", ".") // Convert decimal comma to dot
	} else {
		// US format: 1,234.56 or 1234.56
		cleanStr = strings.ReplaceAll(cleanStr, ",", "") // Remove thousand separators
	}
	
	val, err = strconv.ParseFloat(cleanStr, 64)
	if err != nil {
		return 0
	}
	return val
}

// evaluateMath evaluates simple mathematical expressions
func (s *ComputedService) evaluateMath(expr string) (float64, error) {
	expr = strings.TrimSpace(expr)

	// Handle parentheses recursively
	for {
		start := strings.Index(expr, "(")
		if start == -1 {
			break
		}
		end := s.findMatchingParen(expr, start)
		if end == -1 {
			return 0, fmt.Errorf("mismatched parentheses")
		}
		inner := expr[start+1 : end]
		innerVal, err := s.evaluateMath(inner)
		if err != nil {
			return 0, err
		}
		expr = expr[:start] + fmt.Sprintf("%f", innerVal) + expr[end+1:]
	}

	// Evaluate expression (respecting operator precedence)
	return s.evaluateSimpleExpr(expr)
}

// findMatchingParen finds the matching closing parenthesis
func (s *ComputedService) findMatchingParen(expr string, start int) int {
	depth := 1
	for i := start + 1; i < len(expr); i++ {
		if expr[i] == '(' {
			depth++
		} else if expr[i] == ')' {
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// evaluateSimpleExpr evaluates expression with +, -, *, /
func (s *ComputedService) evaluateSimpleExpr(expr string) (float64, error) {
	expr = strings.TrimSpace(expr)

	// Handle multiplication and division first
	reMD := regexp.MustCompile(`(-?\d+\.?\d*)\s*([*/])\s*(-?\d+\.?\d*)`)
	for {
		match := reMD.FindStringSubmatch(expr)
		if match == nil {
			break
		}
		left := s.toFloat(match[1])
		right := s.toFloat(match[3])
		var result float64
		if match[2] == "*" {
			result = left * right
		} else {
			if right == 0 {
				return 0, fmt.Errorf("division by zero")
			}
			result = left / right
		}
		expr = strings.Replace(expr, match[0], fmt.Sprintf("%f", result), 1)
	}

	// Handle addition and subtraction
	reAS := regexp.MustCompile(`(-?\d+\.?\d*)\s*([+-])\s*(-?\d+\.?\d*)`)
	for {
		match := reAS.FindStringSubmatch(expr)
		if match == nil {
			break
		}
		left := s.toFloat(match[1])
		right := s.toFloat(match[3])
		var result float64
		if match[2] == "+" {
			result = left + right
		} else {
			result = left - right
		}
		expr = strings.Replace(expr, match[0], fmt.Sprintf("%f", result), 1)
	}

	// Return final value
	return s.toFloat(expr), nil
}

// EvaluateFormulaSafe evaluates a formula and returns nil on error (safe for PG NULL)
// Error policy: errors result in NULL (nil) - this is the recommended behavior for computed columns
func (s *ComputedService) EvaluateFormulaSafe(formula string, rowData map[string]interface{}) interface{} {
	if formula == "" {
		return nil
	}

	result, err := s.EvaluateFormula(formula, rowData)
	if err != nil {
		// Error policy: return nil (NULL in PostgreSQL)
		// This handles: division by zero, invalid PARSE_NUMBER, malformed formulas
		return nil
	}
	return result
}

// ExtractFieldReferences extracts all {{field}} references from a formula
func (s *ComputedService) ExtractFieldReferences(formula string) []string {
	re := regexp.MustCompile(`\{\{(\w+)\}\}`)
	matches := re.FindAllStringSubmatch(formula, -1)
	fields := make([]string, 0, len(matches))
	seen := make(map[string]bool)
	for _, match := range matches {
		if len(match) > 1 && !seen[match[1]] {
			fields = append(fields, match[1])
			seen[match[1]] = true
		}
	}
	return fields
}

// TopologicalSortColumns sorts computed columns by dependency order (Kahn's algorithm)
// Returns columns in order such that dependencies are computed before dependents
// Example: if tax = total * 0.18 and total = price * qty, returns [total, tax]
func (s *ComputedService) TopologicalSortColumns(computedCols map[string]string) []string {
	// Build dependency graph
	// node -> columns that depend on it (reverse edges for Kahn's)
	dependents := make(map[string][]string) // col -> columns that depend on it
	inDegree := make(map[string]int)         // col -> number of dependencies it has

	// Initialize all columns
	for col := range computedCols {
		inDegree[col] = 0
		dependents[col] = []string{}
	}

	// Build edges: if A references B, then A depends on B
	for col, formula := range computedCols {
		refs := s.ExtractFieldReferences(formula)
		for _, ref := range refs {
			// Check if ref is also a computed column (intra-row dependency)
			if _, isComputed := computedCols[ref]; isComputed {
				// col depends on ref
				dependents[ref] = append(dependents[ref], col)
				inDegree[col]++
			}
		}
	}

	// Kahn's algorithm: start with nodes having no dependencies
	queue := []string{}
	for col, degree := range inDegree {
		if degree == 0 {
			queue = append(queue, col)
		}
	}

	result := []string{}
	for len(queue) > 0 {
		// Pop from queue (process in alphabetical order for determinism)
		sort.Strings(queue)
		col := queue[0]
		queue = queue[1:]
		result = append(result, col)

		// Reduce in-degree of dependents
		for _, dep := range dependents[col] {
			inDegree[dep]--
			if inDegree[dep] == 0 {
				queue = append(queue, dep)
			}
		}
	}

	// Check for cycles (should not happen in valid schemas, but handle gracefully)
	if len(result) != len(computedCols) {
		// Cycle detected - return columns in original order as fallback
		for col := range computedCols {
			found := false
			for _, r := range result {
				if r == col {
					found = true
					break
				}
			}
			if !found {
				result = append(result, col)
			}
		}
	}

	return result
}

// ComputeRow computes all computed columns for a row in correct dependency order
// Returns a map of computed column names to their values
func (s *ComputedService) ComputeRow(computedCols map[string]string, inputRow map[string]interface{}) map[string]interface{} {
	computed := make(map[string]interface{})

	// Handle empty case
	if len(computedCols) == 0 {
		return computed
	}

	// Get ordered columns (dependencies first)
	orderedCols := s.TopologicalSortColumns(computedCols)

	// Working copy of row data (includes computed values as we calculate them)
	rowData := make(map[string]interface{})
	for k, v := range inputRow {
		rowData[k] = v
	}

	// Compute each column in order
	for _, colName := range orderedCols {
		formula := computedCols[colName]
		result := s.EvaluateFormulaSafe(formula, rowData)
		computed[colName] = result
		// Make computed value available to subsequent calculations
		if result != nil {
			rowData[colName] = result
		}
	}

	return computed
}
