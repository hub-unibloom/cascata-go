package nexus

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"context"
	"reflect"
	"encoding/json"
)

// SecretResolver define a interface para busca de segredos sem dependência circular.
type SecretResolver interface {
	Resolve(ctx context.Context, tenantID, identifier string) (string, error)
}

// UserResolver define a interface para buscar dados do usuário autenticado.
// Busca registros de uma tabela específica filtrados pelo user_id (ou id para auth.users).
// tableName: nome da tabela (ex: "users", "identities", "medicos")
// schema: "auth" ou "public" (concatenated tables)
type UserResolver interface {
	GetUserTableData(ctx context.Context, tenantID, userUUID, tableName, schema string) ([]map[string]interface{}, error)
}

// EnumResolver define a interface para busca de enums nativos sem dependência circular.
type EnumResolver interface {
	GetEnumValues(ctx context.Context, tenantID, enumName string) ([]string, error)
}

// ============================================================================
// NEXUS STATE — Sistema de Variáveis e Context Scoping
// ============================================================================
// O NexusState é o "cérebro de curto prazo" de uma execução de grafo.
// Ele mantém todas as variáveis de escopo e permite que qualquer nó
// acesse o estado histórico da execução via interpolação.
// ============================================================================

// NexusState armazena todo o estado de uma execução de grafo.
// Thread-safe para uso concorrente entre goroutines de componentes.
type NexusState struct {
	mu sync.RWMutex

	// === Contexto de Execução ===
	ctx context.Context // Contexto da execução (para resolvers externos)

	// === Contextos Imutáveis (definidos na criação, nunca alterados) ===
	trigger *TriggerContext  // Dados do trigger (payload original)
	context *SecurityContext // Contexto de segurança e rastreamento
	system  *SystemContext   // Informações do sistema

	// === Estado Dinâmico (atualizado durante a execução) ===
	nodes     map[string]*NodeOutput // Saídas de nós que já executaram
	iteration *IterationContext      // Contexto de iteração (dentro de Foreach)
	vars      map[string]interface{} // Variáveis customizadas

	// === Sequência Global ===
	sequence int64 // Contador atômico de pacotes

	// === Resolvers Externos ===
	secretResolver SecretResolver
	enumResolver   EnumResolver
	userResolver   UserResolver
	userCache      map[string]interface{} // Cache lazy do perfil do usuário por execução
}

// TriggerContext contém dados do trigger original.
type TriggerContext struct {
	Type    string                 `json:"type"`    // "PRE_PERSIST" ou "POST_PERSIST"
	Payload map[string]interface{} `json:"payload"` // Body da requisição
	Headers map[string]string      `json:"headers"` // Headers HTTP sanitizados
	Method  string                 `json:"method"`  // Método HTTP
	Route   string                 `json:"route"`   // Rota da requisição
}

// SecurityContext contém dados de segurança e rastreamento.
// Esses dados são IMUTÁVEIS — nenhum nó pode alterá-los.
type SecurityContext struct {
	TenantID   string `json:"tenant_id"`   // Slug do tenant
	UserUUID   string `json:"user_uuid"`   // UUID do usuário
	UserRole   string `json:"user_role"`   // Papel do usuário (anon, user, admin, service)
	AuthSource string `json:"auth_source"` // Fonte de autenticação
	TraceID    string `json:"trace_id"`    // ID de rastreamento
	Timestamp  string `json:"timestamp"`   // Timestamp da requisição
}

// SystemContext contém informações do sistema.
type SystemContext struct {
	AutomationID      string `json:"automation_id"`
	AutomationVersion int    `json:"automation_version"`
	ExecutionMode     string `json:"execution_mode"` // "fast_lane" ou "worker_lane"
	GraphID           string `json:"graph_id"`
}

// NodeOutput armazena a saída de um nó que já executou.
type NodeOutput struct {
	InputData  map[string]interface{} `json:"input_data,omitempty"`
	Data       map[string]interface{} `json:"data"`
	Status     string                 `json:"status"`      // "success", "error", "timeout", "skipped"
	DurationMs int64                  `json:"duration_ms"`
	Error      string                 `json:"error,omitempty"`
}

// IterationContext contém dados de iteração para Foreach.
type IterationContext struct {
	Index int         `json:"index"`
	Total int         `json:"total"`
	Item  interface{} `json:"item"`
}

// NewNexusState cria um novo NexusState com os contextos imutáveis definidos.
func NewNexusState(trigger *TriggerContext, security *SecurityContext, system *SystemContext) *NexusState {
	return &NexusState{
		trigger:  trigger,
		context:  security,
		system:   system,
		nodes:    make(map[string]*NodeOutput),
		vars:     make(map[string]interface{}),
		sequence: 0,
	}
}

// NextSequence retorna o próximo número de sequência (thread-safe).
func (s *NexusState) NextSequence() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sequence++
	return s.sequence
}

// SetNodeOutput registra a saída de um nó executado.
func (s *NexusState) SetNodeOutput(nodeID string, output *NodeOutput) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nodes[nodeID] = output
}

// GetNodeOutput recupera a saída de um nó.
func (s *NexusState) GetNodeOutput(nodeID string) (*NodeOutput, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out, ok := s.nodes[nodeID]
	return out, ok
}

// SetIteration define o contexto de iteração (usado por Foreach).
func (s *NexusState) SetIteration(index, total int, item interface{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.iteration = &IterationContext{
		Index: index,
		Total: total,
		Item:  item,
	}
}

// ClearIteration limpa o contexto de iteração.
func (s *NexusState) ClearIteration() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.iteration = nil
}

// SetVar define uma variável customizada.
func (s *NexusState) SetVar(key string, value interface{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.vars[key] = value
}

// GetVar recupera uma variável customizada.
func (s *NexusState) GetVar(key string) (interface{}, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.vars[key]
	return v, ok
}

// Trigger retorna o contexto de trigger (somente leitura).
func (s *NexusState) Trigger() *TriggerContext { return s.trigger }

// Security retorna o contexto de segurança (somente leitura, IMUTÁVEL).
func (s *NexusState) Security() *SecurityContext { return s.context }

// GetSecurityContext é um alias para Security() (compatibilidade com nós).
func (s *NexusState) GetSecurityContext() *SecurityContext { return s.context }

// System retorna o contexto do sistema (somente leitura).
func (s *NexusState) System() *SystemContext { return s.system }

// SetContext define o contexto de execução para resolvers externos.
// Isso permite que Vault, Enum e User resolvers respeitem timeouts e cancelamento.
func (s *NexusState) SetContext(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ctx = ctx
}

// SetSecretResolver define o buscador de segredos do Vault.
func (s *NexusState) SetSecretResolver(r SecretResolver) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.secretResolver = r
}

// SetEnumResolver define o buscador de enums do PostgreSQL.
func (s *NexusState) SetEnumResolver(r EnumResolver) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.enumResolver = r
}

// SetUserResolver define o buscador de dados do usuário autenticado.
func (s *NexusState) SetUserResolver(r UserResolver) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.userResolver = r
}

// ============================================================================
// INTERPOLAÇÃO DE VARIÁVEIS
// ============================================================================

// Regex para detectar expressões de interpolação: {{$path.to.value}}
var interpolationRegex = regexp.MustCompile(`\{\{([^}]+)\}\}`)

// Interpolate resolve todas as expressões {{...}} em uma string.
func (s *NexusState) Interpolate(template string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return interpolationRegex.ReplaceAllStringFunc(template, func(match string) string {
		// Remove {{ e }}
		expr := strings.TrimSpace(match[2 : len(match)-2])
		val, ok := s.resolveExpression(expr)
		if !ok {
			return match // Retorna a expressão original se não encontrada no estado
		}
		if val == nil {
			return "null" // Substitui por literal null se o valor for nulo
		}
		// Se o valor for um map ou slice, serializamos como JSON para strings
		if reflect.TypeOf(val).Kind() == reflect.Map || reflect.TypeOf(val).Kind() == reflect.Slice {
			bytes, _ := json.Marshal(val)
			return string(bytes)
		}
		// --- Sinergia: Formatação de Tipos Especiais ---
		if t, ok := val.(time.Time); ok {
			return t.Format(time.RFC3339)
		}
		
		sVal := fmt.Sprintf("%v", val)
		// --- Sinergia: Limpeza de representação de structs (ex: Decimal do DB) ---
		if strings.HasPrefix(sVal, "{") && strings.HasSuffix(sVal, "}") && strings.Contains(sVal, "finite") {
			return fmt.Sprintf("%g", toFloat(val))
		}
		return sVal
	})
}

// InterpolateString resolve interpolação e permite injetar um contexto local (ex: $input).
func (s *NexusState) InterpolateString(template string, localContext map[string]interface{}) (string, error) {
	if localContext != nil {
		s.SetVar("$input", localContext)
	}
	return s.Interpolate(template), nil
}

// Resolve tenta resolver uma expressão {{...}} para seu tipo original (raw interface{}).
// Se não for uma expressão pura (ex: "ID: {{$id}}"), retorna via Interpolate (string).
func (s *NexusState) Resolve(expr string) interface{} {
	if !strings.HasPrefix(expr, "{{") || !strings.HasSuffix(expr, "}}") {
		return expr
	}

	// Verifica se tem apenas um par de chaves (expressão pura)
	inner := expr[2 : len(expr)-2]
	if strings.Contains(inner, "{{") || strings.Contains(inner, "}}") {
		// Template misto, forçamos string
		return s.Interpolate(expr)
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	
	val, ok := s.resolveExpression(strings.TrimSpace(inner))
	if ok {
		return val
	}
	
	return expr // Não resolveu, retorna literal original
}

// ResolveAny resolve interpolação recursivamente em qualquer tipo de valor (string, map, slice), 
// preservando tipos originais se forem expressões puras (ex: "{{$var}}").
func (s *NexusState) ResolveAny(v interface{}) interface{} {
	switch val := v.(type) {
	case string:
		// EXTRACAO DE TIPO PURO: Se a string for exclusivamente uma variável (ex: "{{$nodes.id.out}}"),
		// retornamos o objeto/tipo original em vez de converter para string.
		if strings.HasPrefix(val, "{{") && strings.HasSuffix(val, "}}") && strings.Count(val, "{{") == 1 {
			expr := strings.TrimSpace(val[2 : len(val)-2])
			resolved, ok := s.resolveExpression(expr)
			if ok {
				return resolved
			}
		}
		return s.Interpolate(val)
	case map[string]interface{}:
		result := make(map[string]interface{}, len(val))
		for k, inner := range val {
			result[k] = s.ResolveAny(inner)
		}
		return result
	case []interface{}:
		result := make([]interface{}, len(val))
		for i, inner := range val {
			result[i] = s.ResolveAny(inner)
		}
		return result
	default:
		return v
	}
}

// InterpolateMap resolve interpolação em todos os valores de um map preservando tipos.
func (s *NexusState) InterpolateMap(data map[string]interface{}) map[string]interface{} {
	if data == nil {
		return nil
	}
	result := make(map[string]interface{}, len(data))
	for k, v := range data {
		result[k] = s.ResolveAny(v)
	}
	return result
}

// resolveExpression tenta resolver uma expressão completa e retorna (valor, encontrado).
// Suporta pipes de transformação (ex: {{$trigger.data.amount | to_number}})
func (s *NexusState) resolveExpression(fullExpr string) (interface{}, bool) {
	pipeParts := strings.Split(fullExpr, "|")
	expr := strings.TrimSpace(pipeParts[0])

	val, ok := s.resolveBaseExpression(expr)
	if !ok {
		return nil, false
	}

	// Aplica pipes sequencialmente
	for i := 1; i < len(pipeParts); i++ {
		val = s.applyPipe(val, strings.TrimSpace(pipeParts[i]))
	}

	return val, true
}

// resolveBaseExpression contém a lógica original de resolução de caminhos.
func (s *NexusState) resolveBaseExpression(expr string) (interface{}, bool) {
	parts := strings.SplitN(expr, ".", 2)
	if len(parts) == 0 {
		return nil, false
	}

	root := parts[0]
	rest := ""
	if len(parts) > 1 {
		rest = parts[1]
	}

	switch root {
	case "$trigger":
		return s.resolveFromTrigger(rest)
	case "$context":
		return s.resolveFromContext(rest)
	case "$nodes":
		return s.resolveFromNodes(rest)
	case "$system":
		return s.resolveFromSystem(rest)
	case "$iteration":
		return s.resolveFromIteration(rest)
	case "$vault":
		return s.resolveFromVault(rest)
	case "$enums":
		return s.resolveFromEnums(rest)
	case "$user":
		return s.resolveFromUser(rest)
	case "$input":
		if v, ok := s.vars["$input"]; ok {
			return resolveNestedPath(v, rest)
		}
		return nil, false
	default:
		// SINERGIA: Se a expressão não começa com $, mas parece um ID de nó (node_...)
		// Resolvemos automaticamente como nó para garantir compatibilidade com o frontend
		if strings.HasPrefix(root, "node_") {
			return s.resolveFromNodes(expr)
		}

		// Tenta variável customizada
		if v, ok := s.vars[root]; ok {
			if rest == "" {
				return v, true
			}
			return resolveNestedPath(v, rest)
		}
		return nil, false
	}
}

// applyPipe aplica transformações de dados em linha.
func (s *NexusState) applyPipe(val interface{}, pipe string) interface{} {
	if val == nil {
		return nil
	}

	switch strings.ToLower(pipe) {
	case "to_number", "number", "float":
		return toFloat(val)
	case "to_int", "int":
		return int64(toFloat(val))
	case "to_string", "string", "text":
		return fmt.Sprintf("%v", val)
	case "to_bool", "boolean", "bool":
		if b, ok := val.(bool); ok {
			return b
		}
		str := strings.ToLower(fmt.Sprintf("%v", val))
		return str == "true" || str == "1" || str == "yes" || str == "on"
	case "to_timestamp", "datetime", "time":
		if t, ok := val.(time.Time); ok {
			return t
		}
		str := fmt.Sprintf("%v", val)
		// Tenta RFC3339 primeiro
		t, err := time.Parse(time.RFC3339, str)
		if err == nil {
			return t
		}
		// Tenta formato de data simples YYYY-MM-DD
		t, err = time.Parse("2006-01-02", str)
		if err == nil {
			return t
		}
		return val
	case "uppercase", "upper":
		return strings.ToUpper(fmt.Sprintf("%v", val))
	case "lowercase", "lower":
		return strings.ToLower(fmt.Sprintf("%v", val))
	case "trim":
		return strings.TrimSpace(fmt.Sprintf("%v", val))
	case "json":
		bytes, _ := json.Marshal(val)
		return string(bytes)
	default:
		return val
	}
}

func (s *NexusState) resolveFromTrigger(path string) (interface{}, bool) {
	if s.trigger == nil {
		return nil, false
	}

	// Normalização de handles do Frontend (Sinergia)
	// O frontend trata o trigger como um nó com porta 'out.data', mas o backend
	// armazena o payload diretamente. Removemos esses prefixos para compatibilidade.
	if strings.HasPrefix(path, "out.data.") {
		path = strings.TrimPrefix(path, "out.data.")
	} else if strings.HasPrefix(path, "payload.") {
		path = strings.TrimPrefix(path, "payload.")
	}

	// Case-insensitive field resolution for common fields (body vs Body, etc.)
	// This handles the WebhookEnvelope structure where fields use specific casing
	normalizedPath := normalizeCaseInsensitivePath(s.trigger.Payload, path)

	switch {
	case path == "" || path == "payload" || path == "out.data":
		return s.trigger.Payload, true
	case path == "method":
		return s.trigger.Method, true
	case path == "route":
		return s.trigger.Route, true
	case path == "type":
		return s.trigger.Type, true
	case path == "headers":
		return s.trigger.Headers, true
	case strings.HasPrefix(path, "headers."):
		key := strings.TrimPrefix(path, "headers.")
		if s.trigger.Headers != nil {
			val, ok := s.trigger.Headers[key]
			return val, ok
		}
		return nil, false
	default:
		// Try normalized path first, fall back to original
		if normalizedPath != path {
			if val, ok := resolveNestedPath(s.trigger.Payload, normalizedPath); ok {
				return val, true
			}
		}
		return resolveNestedPath(s.trigger.Payload, path)
	}
}

// normalizeCaseInsensitivePath attempts to find the correct case for path segments
// in a nested map structure. This handles cases where the user uses "body" but the
// actual field is "Body" (as in WebhookEnvelope).
func normalizeCaseInsensitivePath(data interface{}, path string) string {
	if path == "" {
		return path
	}

	// Split path into segments
	parts := strings.Split(path, ".")
	if len(parts) == 0 {
		return path
	}

	// Start with the original data
	current := data
	normalizedParts := make([]string, 0, len(parts))

	for i, part := range parts {
		if current == nil {
			// Can't normalize further, return original
			return path
		}

		// If this is the last part, try to find case-insensitive match
		if i == len(parts)-1 {
			if m, ok := current.(map[string]interface{}); ok {
				// Try to find a case-insensitive match
				for key := range m {
					if strings.EqualFold(key, part) {
						normalizedParts = append(normalizedParts, key)
						return strings.Join(normalizedParts, ".")
					}
				}
			}
			// No match found, use original
			normalizedParts = append(normalizedParts, part)
			return strings.Join(normalizedParts, ".")
		}

		// For intermediate parts, navigate to the next level
		if m, ok := current.(map[string]interface{}); ok {
			// Try to find case-insensitive match
			found := false
			for key := range m {
				if strings.EqualFold(key, part) {
					normalizedParts = append(normalizedParts, key)
					current = m[key]
					found = true
					break
				}
			}
			if !found {
				// No match, use original and continue
				normalizedParts = append(normalizedParts, part)
				current = m[part]
			}
		} else {
			// Not a map, can't navigate further
			normalizedParts = append(normalizedParts, part)
			return strings.Join(normalizedParts, ".")
		}
	}

	return strings.Join(normalizedParts, ".")
}

func (s *NexusState) resolveFromVault(path string) (interface{}, bool) {
	if s.secretResolver == nil {
		return nil, false
	}
	// O path esperado é "SECRET_NAME.value" ou apenas "SECRET_NAME"
	identifier := strings.TrimSuffix(path, ".value")
	
	// Tenta resolver o segredo via VaultService (injetado via interface)
	val, err := s.secretResolver.Resolve(s.ctx, s.context.TenantID, identifier)
	if err != nil {
		return nil, false
	}
	return val, true
}

func (s *NexusState) resolveFromEnums(path string) (interface{}, bool) {
	if s.enumResolver == nil {
		return nil, false
	}

	parts := strings.Split(path, ".")
	if len(parts) == 0 {
		return nil, false
	}

	enumName := parts[0]
	values, err := s.enumResolver.GetEnumValues(s.ctx, s.context.TenantID, enumName)
	if err != nil {
		return nil, false
	}

	// Se pediu apenas o nome do enum (ex: $enums.exemplo), retorna a lista completa
	if len(parts) == 1 {
		return values, true
	}

	// Se pediu um valor específico (ex: $enums.exemplo.teste1)
	requestedValue := parts[1]
	for _, v := range values {
		if v == requestedValue {
			return v, true
		}
	}

	return nil, false
}

func (s *NexusState) resolveFromUser(path string) (interface{}, bool) {
	if s.userResolver == nil || s.context == nil || s.context.UserUUID == "" {
		return nil, false
	}

	if path == "" {
		return nil, false
	}

	// Formato esperado: $user.tableName.columnName (ou $user.tableName para linhas completas)
	parts := strings.SplitN(path, ".", 2)
	tableName := parts[0]
	columnPath := ""
	if len(parts) > 1 {
		columnPath = parts[1]
	}

	// Lazy-load por tabela: busca apenas quando necessário e cacheia por execução
	cacheKey := tableName
	if s.userCache == nil {
		s.userCache = make(map[string]interface{})
	}

	if _, cached := s.userCache[cacheKey]; !cached {
		// Detecta schema: tabelas nativas do auth vs concatenadas do public
		schema := "auth"
		authTables := map[string]bool{
			"users": true, "identities": true, "audit_log": true,
			"otp_codes": true, "refresh_tokens": true, "user_devices": true,
		}
		if !authTables[tableName] {
			schema = "public"
		}

		rows, err := s.userResolver.GetUserTableData(
			s.ctx,
			s.context.TenantID,
			s.context.UserUUID,
			tableName,
			schema,
		)
		if err != nil {
			s.userCache[cacheKey] = nil
			return nil, false
		}

		// auth.users retorna row única -> acesso direto aos campos
		// outras tabelas podem ter múltiplas rows -> array
		if tableName == "users" && len(rows) == 1 {
			s.userCache[cacheKey] = rows[0]
		} else if len(rows) == 1 {
			// Tabela com uma única row para esse usuário -> acesso direto
			s.userCache[cacheKey] = rows[0]
		} else {
			// Múltiplas rows -> retorna como array
			var iRows []interface{}
			for _, r := range rows {
				iRows = append(iRows, r)
			}
			s.userCache[cacheKey] = iRows
		}
	}

	cachedData := s.userCache[cacheKey]
	if cachedData == nil {
		return nil, false
	}

	// Sem coluna específica: retorna a tabela inteira
	if columnPath == "" {
		return cachedData, true
	}

	// Resolve o campo dentro dos dados cacheados
	return resolveNestedPath(cachedData, columnPath)
}

func (s *NexusState) resolveFromContext(path string) (interface{}, bool) {
	if s.context == nil {
		return nil, false
	}
	switch path {
	case "tenant_id":
		return s.context.TenantID, true
	case "user_uuid":
		return s.context.UserUUID, true
	case "auth_source":
		return s.context.AuthSource, true
	case "trace_id":
		return s.context.TraceID, true
	case "timestamp":
		return s.context.Timestamp, true
	default:
		return nil, false
	}
}

func (s *NexusState) resolveFromNodes(path string) (interface{}, bool) {
	// Normalização: remove o prefixo $nodes se presente (para chamadas recursivas)
	path = strings.TrimPrefix(path, "$nodes.")

	parts := strings.SplitN(path, ".", 2)
	if len(parts) == 0 {
		return nil, false
	}

	nodeID := parts[0]
	output, ok := s.nodes[nodeID]
	if !ok {
		return nil, false
	}

	if len(parts) == 1 {
		return output, true
	}

	subPath := parts[1]

	// Normalização de handles do Frontend (Sinergia)
	// Converte 'row.field', 'payload.field' ou 'data.field' em 'out.data.field'
	if strings.HasPrefix(subPath, "row.") {
		subPath = "out.data." + strings.TrimPrefix(subPath, "row.")
	} else if strings.HasPrefix(subPath, "payload.") {
		subPath = "out.data." + strings.TrimPrefix(subPath, "payload.")
	} else if strings.HasPrefix(subPath, "data.") {
		subPath = "out.data." + strings.TrimPrefix(subPath, "data.")
	}

	switch {
	case subPath == "out" || subPath == "out.data":
		return output.Data, true
	case subPath == "body":
		// Alias para facilitar acesso a respostas HTTP
		return resolveNestedPath(output.Data, "http_response.body")
	case strings.HasPrefix(subPath, "body."):
		// Alias para facilitar acesso a campos de respostas HTTP
		field := strings.TrimPrefix(subPath, "body.")
		return resolveNestedPath(output.Data, "http_response.body."+field)
	case subPath == "status":
		// Alias para facilitar acesso ao status de respostas HTTP ou de nós
		if val, ok := resolveNestedPath(output.Data, "http_response.status_code"); ok {
			return val, true
		}
		return output.Status, true
	case strings.HasPrefix(subPath, "out.data."):
		field := strings.TrimPrefix(subPath, "out.data.")
		return resolveNestedPath(output.Data, field)
	case strings.HasPrefix(subPath, "out."):
		field := strings.TrimPrefix(subPath, "out.")
		switch field {
		case "status":
			return output.Status, true
		case "duration_ms":
			return output.DurationMs, true
		case "error":
			return output.Error, true
		default:
			return resolveNestedPath(output.Data, field)
		}
	case subPath == "config":
		return nil, false // Config é somente leitura e não é exposto por segurança
	default:
		return resolveNestedPath(output.Data, subPath)
	}
}

func (s *NexusState) resolveFromSystem(path string) (interface{}, bool) {
	if s.system == nil {
		return nil, false
	}
	switch path {
	case "automation_id":
		return s.system.AutomationID, true
	case "automation_version":
		return s.system.AutomationVersion, true
	case "execution_mode":
		return s.system.ExecutionMode, true
	case "graph_id":
		return s.system.GraphID, true
	default:
		return nil, false
	}
}

func (s *NexusState) resolveFromIteration(path string) (interface{}, bool) {
	if s.iteration == nil {
		return nil, false
	}
	switch path {
	case "index":
		return s.iteration.Index, true
	case "total":
		return s.iteration.Total, true
	case "item":
		return s.iteration.Item, true
	default:
		if strings.HasPrefix(path, "item.") {
			return resolveNestedPath(s.iteration.Item, strings.TrimPrefix(path, "item."))
		}
		return nil, false
	}
}

// resolveNestedPath navega por um caminho dot-separated em dados aninhados.
// Suporta tanto notação de pontos (a.b) quanto colchetes (a[0].b).
func resolveNestedPath(data interface{}, path string) (interface{}, bool) {
	path = normalizeBracketPath(path)
	return resolveNestedPathRec(data, path)
}

func normalizeBracketPath(path string) string {
	if !strings.Contains(path, "[") {
		return path
	}
	var sb strings.Builder
	sb.Grow(len(path))
	for i := 0; i < len(path); i++ {
		char := path[i]
		if char == '[' {
			sb.WriteByte('.')
		} else if char == ']' {
			// Ignora o fechamento do colchete
		} else {
			sb.WriteByte(char)
		}
	}
	res := sb.String()
	for strings.Contains(res, "..") {
		res = strings.ReplaceAll(res, "..", ".")
	}
	return strings.Trim(res, ".")
}

func resolveNestedPathRec(data interface{}, path string) (interface{}, bool) {
	if path == "" {
		return data, true
	}
	if data == nil {
		return nil, false // Caminho não existe em objeto nulo
	}

	parts := strings.SplitN(path, ".", 2)
	key := parts[0]
	rest := ""
	if len(parts) > 1 {
		rest = parts[1]
	}

	switch v := data.(type) {
	case map[string]interface{}:
		next, ok := v[key]
		if !ok {
			return nil, false
		}
		return resolveNestedPathRec(next, rest)

	case []interface{}:
		// Suporta acesso por índice numérico: items.0.name
		idx, err := strconv.Atoi(key)
		if err != nil || idx < 0 || idx >= len(v) {
			// Suporta "length"
			if key == "length" {
				return len(v), true
			}
			return nil, false
		}
		return resolveNestedPathRec(v[idx], rest)

	default:
		return nil, false
	}
}

// ============================================================================
// SNAPSHOT — Para debug e telemetria
// ============================================================================

// Snapshot retorna uma cópia segura do estado atual para telemetria/debug.
func (s *NexusState) Snapshot() map[string]interface{} {
	s.mu.RLock()
	defer s.mu.RUnlock()

	snapshot := map[string]interface{}{
		"trigger":   s.trigger,
		"context":   s.context,
		"system":    s.system,
		"iteration": s.iteration,
		"sequence":  s.sequence,
		"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
	}

	nodes := make(map[string]interface{}, len(s.nodes))
	for k, v := range s.nodes {
		nodes[k] = v
	}
	snapshot["nodes"] = nodes

	return snapshot
}
