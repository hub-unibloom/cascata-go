package services

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// TokenType represents the type of a token in the expression
type TokenType int

const (
	TokenNumber TokenType = iota
	TokenOperator
	TokenFunction
	TokenLeftParen
	TokenRightParen
	TokenVariable
	TokenComma
)

// Token represents a lexical token in the expression
type Token struct {
	Type  TokenType
	Value string
}

// MathParser implements a secure mathematical expression parser using shunting-yard algorithm
type MathParser struct {
	operators map[string]int // precedence
	functions map[string]bool // allowed functions
}

// NewMathParser creates a new math parser with allowed operations
func NewMathParser() *MathParser {
	return &MathParser{
		operators: map[string]int{
			"+": 1,
			"-": 1,
			"*": 2,
			"/": 2,
			"%": 2,
			"^": 3,
		},
		functions: map[string]bool{
			"sqrt":    true,
			"abs":     true,
			"sin":     true,
			"cos":     true,
			"tan":     true,
			"log":     true,
			"ln":      true,
			"round":   true,
			"floor":   true,
			"ceil":    true,
			"min":     true,
			"max":     true,
			"pow":     true,
			"mod":     true,
			"sign":    true,
			"trunc":   true,
		},
	}
}

// Tokenize converts an expression string into tokens
func (p *MathParser) Tokenize(expression string) ([]Token, error) {
	var tokens []Token
	i := 0
	
	// Regex patterns
	numberPattern := regexp.MustCompile(`^-?\d+\.?\d*`)
	funcPattern := regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*`)
	varPattern := regexp.MustCompile(`^\{\{[^}]+\}\}`)
	
	for i < len(expression) {
		ch := expression[i]
		
		// Skip whitespace
		if ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r' {
			i++
			continue
		}
		
		// Check for variables {{...}}
		if match := varPattern.FindString(expression[i:]); match != "" {
			tokens = append(tokens, Token{Type: TokenVariable, Value: match})
			i += len(match)
			continue
		}
		
		// Check for numbers
		if match := numberPattern.FindString(expression[i:]); match != "" {
			tokens = append(tokens, Token{Type: TokenNumber, Value: match})
			i += len(match)
			continue
		}
		
		// Check for functions/identifiers
		if match := funcPattern.FindString(expression[i:]); match != "" {
			if p.functions[match] {
				tokens = append(tokens, Token{Type: TokenFunction, Value: match})
			} else {
				return nil, fmt.Errorf("unknown function or variable: %s", match)
			}
			i += len(match)
			continue
		}
		
		// Operators and parentheses
		switch ch {
		case '+', '-', '*', '/', '%', '^':
			tokens = append(tokens, Token{Type: TokenOperator, Value: string(ch)})
			i++
		case '(':
			tokens = append(tokens, Token{Type: TokenLeftParen, Value: "("})
			i++
		case ')':
			tokens = append(tokens, Token{Type: TokenRightParen, Value: ")"})
			i++
		case ',':
			tokens = append(tokens, Token{Type: TokenComma, Value: ","})
			i++
		default:
			return nil, fmt.Errorf("invalid character: %c", ch)
		}
	}
	
	return tokens, nil
}

// ToRPN converts tokens to Reverse Polish Notation using shunting-yard algorithm
func (p *MathParser) ToRPN(tokens []Token) ([]Token, error) {
	var output []Token
	var stack []Token
	
	for _, token := range tokens {
		switch token.Type {
		case TokenNumber, TokenVariable:
			output = append(output, token)
			
		case TokenFunction:
			stack = append(stack, token)
			
		case TokenComma:
			// Pop until left parenthesis
			for len(stack) > 0 {
				top := stack[len(stack)-1]
				if top.Type == TokenLeftParen {
					break
				}
				output = append(output, top)
				stack = stack[:len(stack)-1]
			}
			
		case TokenOperator:
			// Pop operators with higher or equal precedence
			for len(stack) > 0 {
				top := stack[len(stack)-1]
				if top.Type != TokenOperator {
					break
				}
				if p.operators[top.Value] < p.operators[token.Value] {
					break
				}
				output = append(output, top)
				stack = stack[:len(stack)-1]
			}
			stack = append(stack, token)
			
		case TokenLeftParen:
			stack = append(stack, token)
			
		case TokenRightParen:
			// Pop until left parenthesis
			foundLeft := false
			for len(stack) > 0 {
				top := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				if top.Type == TokenLeftParen {
					foundLeft = true
					break
				}
				output = append(output, top)
			}
			if !foundLeft {
				return nil, fmt.Errorf("mismatched parentheses")
			}
			// Check for function on stack
			if len(stack) > 0 && stack[len(stack)-1].Type == TokenFunction {
				output = append(output, stack[len(stack)-1])
				stack = stack[:len(stack)-1]
			}
		}
	}
	
	// Pop remaining operators
	for len(stack) > 0 {
		top := stack[len(stack)-1]
		if top.Type == TokenLeftParen {
			return nil, fmt.Errorf("mismatched parentheses")
		}
		output = append(output, top)
		stack = stack[:len(stack)-1]
	}
	
	return output, nil
}

// EvaluateRPN evaluates Reverse Polish Notation with variable resolution
func (p *MathParser) EvaluateRPN(rpn []Token, variables map[string]float64) (float64, error) {
	var stack []float64
	
	for _, token := range rpn {
		switch token.Type {
		case TokenNumber:
			val, err := strconv.ParseFloat(token.Value, 64)
			if err != nil {
				return 0, fmt.Errorf("invalid number: %s", token.Value)
			}
			stack = append(stack, val)
			
		case TokenVariable:
			// Extract variable name from {{...}}
			varName := strings.Trim(token.Value, "{}")
			val, ok := variables[varName]
			if !ok {
				return 0, fmt.Errorf("undefined variable: %s", varName)
			}
			stack = append(stack, val)
			
		case TokenOperator:
			if len(stack) < 2 {
				return 0, fmt.Errorf("insufficient operands for operator %s", token.Value)
			}
			b := stack[len(stack)-1]
			a := stack[len(stack)-2]
			stack = stack[:len(stack)-2]
			
			var result float64
			switch token.Value {
			case "+":
				result = a + b
			case "-":
				result = a - b
			case "*":
				result = a * b
			case "/":
				if b == 0 {
					return 0, fmt.Errorf("division by zero")
				}
				result = a / b
			case "%":
				result = math.Mod(a, b)
			case "^":
				result = math.Pow(a, b)
			}
			stack = append(stack, result)
			
		case TokenFunction:
			args, err := p.getFunctionArgs(token.Value, &stack)
			if err != nil {
				return 0, err
			}
			
			result, err := p.executeFunction(token.Value, args)
			if err != nil {
				return 0, err
			}
			stack = append(stack, result)
		}
	}
	
	if len(stack) != 1 {
		return 0, fmt.Errorf("invalid expression")
	}
	
	return stack[0], nil
}

// getFunctionArgs extracts arguments for a function from the stack
func (p *MathParser) getFunctionArgs(funcName string, stack *[]float64) ([]float64, error) {
	// Define arity for each function
	arity := map[string]int{
		"sqrt":  1,
		"abs":   1,
		"sin":   1,
		"cos":   1,
		"tan":   1,
		"log":   1,
		"ln":    1,
		"round": 1,
		"floor": 1,
		"ceil":  1,
		"sign":  1,
		"trunc": 1,
		"min":   2,
		"max":   2,
		"pow":   2,
		"mod":   2,
	}
	
	n := arity[funcName]
	if n == 0 {
		return nil, fmt.Errorf("unknown function: %s", funcName)
	}
	
	if len(*stack) < n {
		return nil, fmt.Errorf("insufficient arguments for %s", funcName)
	}
	
	args := make([]float64, n)
	for i := 0; i < n; i++ {
		args[n-1-i] = (*stack)[len(*stack)-1-i]
	}
	*stack = (*stack)[:len(*stack)-n]
	
	return args, nil
}

// executeFunction executes a mathematical function
func (p *MathParser) executeFunction(name string, args []float64) (float64, error) {
	switch name {
	case "sqrt":
		if args[0] < 0 {
			return 0, fmt.Errorf("sqrt of negative number")
		}
		return math.Sqrt(args[0]), nil
	case "abs":
		return math.Abs(args[0]), nil
	case "sin":
		return math.Sin(args[0]), nil
	case "cos":
		return math.Cos(args[0]), nil
	case "tan":
		return math.Tan(args[0]), nil
	case "log":
		return math.Log10(args[0]), nil
	case "ln":
		return math.Log(args[0]), nil
	case "round":
		return math.Round(args[0]), nil
	case "floor":
		return math.Floor(args[0]), nil
	case "ceil":
		return math.Ceil(args[0]), nil
	case "sign":
		if args[0] > 0 {
			return 1, nil
		} else if args[0] < 0 {
			return -1, nil
		}
		return 0, nil
	case "trunc":
		return math.Trunc(args[0]), nil
	case "min":
		return math.Min(args[0], args[1]), nil
	case "max":
		return math.Max(args[0], args[1]), nil
	case "pow":
		return math.Pow(args[0], args[1]), nil
	case "mod":
		return math.Mod(args[0], args[1]), nil
	default:
		return 0, fmt.Errorf("unknown function: %s", name)
	}
}

// Evaluate evaluates a mathematical expression with variable substitution
func (p *MathParser) Evaluate(expression string, variables map[string]float64) (float64, error) {
	tokens, err := p.Tokenize(expression)
	if err != nil {
		return 0, fmt.Errorf("tokenization error: %w", err)
	}
	
	rpn, err := p.ToRPN(tokens)
	if err != nil {
		return 0, fmt.Errorf("parsing error: %w", err)
	}
	
	result, err := p.EvaluateRPN(rpn, variables)
	if err != nil {
		return 0, fmt.Errorf("evaluation error: %w", err)
	}
	
	return result, nil
}

// ValidateExpression checks if an expression is syntactically valid
func (p *MathParser) ValidateExpression(expression string) error {
	_, err := p.Tokenize(expression)
	if err != nil {
		return err
	}
	return nil
}

// ExtractVariables finds all variable references in an expression
func (p *MathParser) ExtractVariables(expression string) []string {
	var vars []string
	pattern := regexp.MustCompile(`\{\{([^}]+)\}\}`)
	matches := pattern.FindAllStringSubmatch(expression, -1)
	seen := make(map[string]bool)
	for _, match := range matches {
		if len(match) > 1 && !seen[match[1]] {
			vars = append(vars, match[1])
			seen[match[1]] = true
		}
	}
	return vars
}
