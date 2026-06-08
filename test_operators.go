package main

import (
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"
)

// InformationPacket representa o pacote de dados (simplificado para teste)
type InformationPacket struct {
	Data map[string]interface{}
}

func (ip *InformationPacket) GetDataField(field string) (interface{}, bool) {
	val, ok := ip.Data[field]
	return val, ok
}

// NexusState representa o estado (simplificado para teste)
type NexusState struct {
	variables map[string]interface{}
}

func (ns *NexusState) Interpolate(expr string) string {
	// Implementação simplificada para teste
	if strings.Contains(expr, "nome") {
		return "Exemplo Nome"
	}
	return expr
}

// Função copiada do código corrigido
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

// Função corrigida para teste
func evaluateSingleCondition(ip *InformationPacket, state *NexusState, field, operator string, expected interface{}) bool {
	// Resolve o valor do campo
	var actual interface{}

	if strings.HasPrefix(field, "{{") {
		// É uma expressão de interpolação
		resolved := state.Interpolate(field)
		actual = resolved
	} else if strings.HasPrefix(field, "$") {
		// É uma referência de estado
		resolved := state.Interpolate("{" + field + "}")
		actual = resolved
	} else {
		// É um campo direto do IP
		actual, _ = ip.GetDataField(field)
	}

	actualStr := fmt.Sprintf("%v", actual)
	expectedStr := fmt.Sprintf("%v", expected)

	// --- Detecção Inteligente de Tipos ---
	// Verifica se ambos os valores são numericamente válidos para comparação numérica
	isNumeric := false
	var aVal, eVal float64
	
	// Função auxiliar para verificar se uma string representa um número válido
	isValidNumber := func(s string) bool {
		if s == "" {
			return false
		}
		// Remove espaços em branco
		s = strings.TrimSpace(s)
		// Verifica se é uma representação de struct do tipo Decimal
		if strings.HasPrefix(s, "{") && strings.HasSuffix(s, "}") && strings.Contains(s, "finite") {
			parts := strings.Fields(strings.Trim(s, "{}"))
			if len(parts) > 0 {
				_, err := strconv.ParseFloat(parts[0], 64)
				return err == nil
			}
			return false
		}
		// Verificação direta para números
		_, err := strconv.ParseFloat(s, 64)
		return err == nil
	}
	
	// Se ambos os valores forem números válidos, usa comparação numérica
	if isValidNumber(actualStr) && isValidNumber(expectedStr) {
		aVal = toFloat(actual)
		eVal = toFloat(expected)
		isNumeric = true
	}

	switch operator {
	case "equals", "eq", "==":
		if isNumeric {
			return aVal == eVal
		}
		// Fallback para comparação de string se não for obviamente numérico
		return actualStr == expectedStr
	case "not_equals", "neq", "!=":
		if isNumeric {
			return aVal != eVal
		}
		return actualStr != expectedStr
	case "contains":
		return strings.Contains(actualStr, expectedStr)
	case "not_contains":
		return !strings.Contains(actualStr, expectedStr)
	case "starts_with":
		return strings.HasPrefix(actualStr, expectedStr)
	case "ends_with":
		return strings.HasSuffix(actualStr, expectedStr)
	case "gt", ">":
		a, e := toFloat(actual), toFloat(expected)
		return a > e
	case "gte", ">=":
		a, e := toFloat(actual), toFloat(expected)
		return a >= e
	case "lt", "<":
		a, e := toFloat(actual), toFloat(expected)
		return a < e
	case "lte", "<=":
		a, e := toFloat(actual), toFloat(expected)
		return a <= e
	case "exists":
		return actual != nil
	case "not_exists":
		return actual == nil
	case "is_empty":
		return actualStr == "" || actualStr == "<nil>"
	case "is_not_empty":
		return actualStr != "" && actualStr != "<nil>"
	case "is_true":
		return actualStr == "true" || actualStr == "1"
	case "is_false":
		return actualStr == "false" || actualStr == "0"
	case "between":
		// Espera "min,max"
		parts := strings.Split(expectedStr, ",")
		if len(parts) == 2 {
			a, min, max := toFloat(actual), toFloat(parts[0]), toFloat(parts[1])
			return a >= min && a <= max
		}
		return false
	case "matches", "regex":
		matched, _ := regexp.MatchString(expectedStr, actualStr)
		return matched
	default:
		return false
	}
}

func main() {
	fmt.Println("=== Testando Operadores de Comparação Corrigidos ===\n")

	// Teste 1: Strings com ==
	fmt.Println("Teste 1: Comparação de strings com ==")
	ip1 := &InformationPacket{Data: map[string]interface{}{"nome": "Exemplo Nome"}}
	state := &NexusState{}
	
	result1 := evaluateSingleCondition(ip1, state, "nome", "==", "Exemplo Nome")
	fmt.Printf("nome == 'Exemplo Nome': %v (esperado: true)\n", result1)
	
	result2 := evaluateSingleCondition(ip1, state, "nome", "==", "Exemplo else")
	fmt.Printf("nome == 'Exemplo else': %v (esperado: false)\n", result2)
	
	// Teste 2: Strings com !=
	fmt.Println("\nTeste 2: Comparação de strings com !=")
	result3 := evaluateSingleCondition(ip1, state, "nome", "!=", "Exemplo Nome")
	fmt.Printf("nome != 'Exemplo Nome': %v (esperado: false)\n", result3)
	
	result4 := evaluateSingleCondition(ip1, state, "nome", "!=", "Exemplo else")
	fmt.Printf("nome != 'Exemplo else': %v (esperado: true)\n", result4)
	
	// Teste 3: Números com ==
	fmt.Println("\nTeste 3: Comparação de números com ==")
	ip2 := &InformationPacket{Data: map[string]interface{}{"numero": 1}}
	
	result5 := evaluateSingleCondition(ip2, state, "numero", "==", 1)
	fmt.Printf("numero == 1: %v (esperado: true)\n", result5)
	
	result6 := evaluateSingleCondition(ip2, state, "numero", "==", 2)
	fmt.Printf("numero == 2: %v (esperado: false)\n", result6)
	
	// Teste 4: Números com !=
	fmt.Println("\nTeste 4: Comparação de números com !=")
	result7 := evaluateSingleCondition(ip2, state, "numero", "!=", 1)
	fmt.Printf("numero != 1: %v (esperado: false)\n", result7)
	
	result8 := evaluateSingleCondition(ip2, state, "numero", "!=", 2)
	fmt.Printf("numero != 2: %v (esperado: true)\n", result8)
	
	// Teste 5: String vs Número (deve ser string comparison)
	fmt.Println("\nTeste 5: String vs Número (comparação mista)")
	result9 := evaluateSingleCondition(ip1, state, "nome", "==", 123)
	fmt.Printf("nome == 123: %v (esperado: false)\n", result9)
	
	// Teste 6: Números como strings (deve detectar como numérico)
	fmt.Println("\nTeste 6: Números como strings (detecção automática)")
	ip3 := &InformationPacket{Data: map[string]interface{}{"valor": "100"}}
	
	result10 := evaluateSingleCondition(ip3, state, "valor", "==", "100")
	fmt.Printf("valor == '100': %v (esperado: true)\n", result10)
	
	result11 := evaluateSingleCondition(ip3, state, "valor", ">", "50")
	fmt.Printf("valor > '50': %v (esperado: true)\n", result11)
	
	fmt.Println("\n=== Testes Concluídos ===")
}
