package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

type AIIntent string

const (
	IntentGeneral    AIIntent = "general"
	IntentSchema     AIIntent = "schema"
	IntentQuery      AIIntent = "query"
	IntentCreate     AIIntent = "create"
	IntentAlter      AIIntent = "alter"
	IntentFix        AIIntent = "fix"
	IntentDocs       AIIntent = "docs"
	IntentRoutes     AIIntent = "routes"
	maxAgentTurns             = 4
)

type ToolFunction struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
}

type ToolDef struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type OpenAIMessageWithTools struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Name       string     `json:"name,omitempty"`
}

type OpenAIResponseWithTools struct {
	Choices []struct {
		Message      OpenAIMessageWithTools `json:"message"`
		FinishReason string                 `json:"finish_reason"`
	} `json:"choices"`
	Error *OpenAIError `json:"error,omitempty"`
}

type OpenAIRequestWithTools struct {
	Model       string                   `json:"model"`
	Messages    []OpenAIMessageWithTools `json:"messages"`
	MaxTokens   int                      `json:"max_tokens,omitempty"`
	Temperature float64                  `json:"temperature,omitempty"`
	Tools       []ToolDef                `json:"tools,omitempty"`
}

func detectIntent(message string) AIIntent {
	lower := strings.ToLower(message)
	switch {
	case containsAny(lower, "tabela", "coluna", "schema", "estrutura", "describe table", "list tables"):
		return IntentSchema
	case containsAny(lower, "select", "consulta", "buscar", "filtrar", "where", "join"):
		return IntentQuery
	case containsAny(lower, "create table", "criar tabela", "nova tabela", "modelar"):
		return IntentCreate
	case containsAny(lower, "alter table", "adicionar coluna", "modificar tabela", "migrar"):
		return IntentAlter
	case containsAny(lower, "erro", "corrigir", "fix", "bug", "falha"):
		return IntentFix
	case containsAny(lower, "documentação", "docs", "guia"):
		return IntentDocs
	case containsAny(lower, "rota", "endpoint", "api route"):
		return IntentRoutes
	default:
		return IntentGeneral
	}
}

func containsAny(input string, terms ...string) bool {
	for _, t := range terms {
		if strings.Contains(input, t) {
			return true
		}
	}
	return false
}

func normalizeText(s string) string {
	s = strings.ToLower(s)
	re := regexp.MustCompile(`[^a-z0-9_ ]+`)
	return strings.TrimSpace(re.ReplaceAllString(s, " "))
}

func summarizeOlderMessages(messages []OpenAIMessage, keepLast int) string {
	if len(messages) <= keepLast {
		return ""
	}
	var b strings.Builder
	b.WriteString("Resumo da conversa anterior:\n")
	for _, m := range messages[:len(messages)-keepLast] {
		content := m.Content
		if len(content) > 240 {
			content = content[:240] + "..."
		}
		b.WriteString("- ")
		b.WriteString(m.Role)
		b.WriteString(": ")
		b.WriteString(strings.ReplaceAll(content, "\n", " "))
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

func buildProjectContextSnapshot(ctx context.Context, projectPool *pgxpool.Pool, projectSlug string) string {
	var b strings.Builder

	// Schema snapshot
	rows, err := projectPool.Query(ctx, `
		SELECT table_name, column_name, data_type
		FROM information_schema.columns
		WHERE table_schema = 'public'
		ORDER BY table_name, ordinal_position
	`)
	if err == nil {
		defer rows.Close()
		current := ""
		var t, c, d string
		for rows.Next() {
			if rows.Scan(&t, &c, &d) != nil {
				continue
			}
			if t != current {
				if current != "" {
					b.WriteString("\n")
				}
				current = t
				b.WriteString("- ")
				b.WriteString(t)
				b.WriteString(": ")
			} else {
				b.WriteString(", ")
			}
			b.WriteString(c)
			b.WriteString(" ")
			b.WriteString(d)
		}
	}

	// Docs snapshot
	docRows, docErr := SystemPool.Query(ctx, `
		SELECT title FROM system.doc_pages
		WHERE project_slug = $1
		ORDER BY updated_at DESC
		LIMIT 5
	`, projectSlug)
	if docErr == nil {
		defer docRows.Close()
		b.WriteString("\n\nDocs recentes:\n")
		var title string
		for docRows.Next() {
			if docRows.Scan(&title) == nil {
				b.WriteString("- ")
				b.WriteString(title)
				b.WriteString("\n")
			}
		}
	}
	return strings.TrimSpace(b.String())
}

func resolveEntities(ctx context.Context, projectPool *pgxpool.Pool, projectSlug, userMessage string) string {
	normalized := normalizeText(userMessage)
	if normalized == "" {
		return ""
	}

	// Best-effort: use aliases table if exists
	rows, err := SystemPool.Query(ctx, `
		SELECT alias, entity_type, table_name, COALESCE(column_name, '')
		FROM system.schema_aliases
		WHERE project_slug = $1
	`, projectSlug)
	if err != nil {
		return ""
	}
	defer rows.Close()

	type hit struct {
		alias string
		typ   string
		table string
		col   string
		score int
	}
	var hits []hit
	var alias, typ, table, col string
	for rows.Next() {
		if rows.Scan(&alias, &typ, &table, &col) != nil {
			continue
		}
		a := normalizeText(alias)
		if a == "" {
			continue
		}
		score := 0
		if strings.Contains(normalized, a) {
			score = 100
		} else if strings.Contains(a, normalized) || strings.Contains(normalized, strings.ReplaceAll(a, "_", " ")) {
			score = 65
		}
		if score > 0 {
			hits = append(hits, hit{alias: alias, typ: typ, table: table, col: col, score: score})
		}
	}
	if len(hits) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("Entity linking (aliases):\n")
	for i, h := range hits {
		if i >= 10 {
			break
		}
		b.WriteString("- ")
		b.WriteString(h.alias)
		b.WriteString(" => ")
		b.WriteString(h.typ)
		b.WriteString(" ")
		b.WriteString(h.table)
		if h.col != "" {
			b.WriteString(".")
			b.WriteString(h.col)
		}
		b.WriteString(" (score=")
		b.WriteString(strconv.Itoa(h.score))
		b.WriteString(")\n")
	}
	return strings.TrimSpace(b.String())
}

func agentTools() []ToolDef {
	return []ToolDef{
		{
			Type: "function",
			Function: ToolFunction{
				Name:        "list_tables",
				Description: "List all tables from public schema",
				Parameters: map[string]interface{}{
					"type":       "object",
					"properties": map[string]interface{}{},
				},
			},
		},
		{
			Type: "function",
			Function: ToolFunction{
				Name:        "describe_table",
				Description: "Describe columns for one table",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"table_name": map[string]interface{}{"type": "string"},
					},
					"required": []string{"table_name"},
				},
			},
		},
		{
			Type: "function",
			Function: ToolFunction{
				Name:        "run_readonly_sql",
				Description: "Execute read-only SQL SELECT query",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"sql": map[string]interface{}{"type": "string"},
					},
					"required": []string{"sql"},
				},
			},
		},
	}
}

func executeAgentTool(ctx context.Context, projectPool *pgxpool.Pool, tc ToolCall) string {
	switch tc.Function.Name {
	case "list_tables":
		rows, err := projectPool.Query(ctx, `
			SELECT table_name
			FROM information_schema.tables
			WHERE table_schema='public'
			ORDER BY table_name
		`)
		if err != nil {
			return `{"error":"failed to list tables"}`
		}
		defer rows.Close()
		var tables []string
		var t string
		for rows.Next() {
			if rows.Scan(&t) == nil {
				tables = append(tables, t)
			}
		}
		raw, _ := json.Marshal(map[string]interface{}{"tables": tables})
		return string(raw)
	case "describe_table":
		var args map[string]interface{}
		_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)
		tableName, _ := args["table_name"].(string)
		if tableName == "" {
			return `{"error":"table_name required"}`
		}
		rows, err := projectPool.Query(ctx, `
			SELECT column_name, data_type, is_nullable
			FROM information_schema.columns
			WHERE table_schema='public' AND table_name=$1
			ORDER BY ordinal_position
		`, tableName)
		if err != nil {
			return `{"error":"failed to describe table"}`
		}
		defer rows.Close()
		cols := []map[string]string{}
		var c, d, n string
		for rows.Next() {
			if rows.Scan(&c, &d, &n) == nil {
				cols = append(cols, map[string]string{"column": c, "type": d, "nullable": n})
			}
		}
		raw, _ := json.Marshal(map[string]interface{}{"table": tableName, "columns": cols})
		return string(raw)
	case "run_readonly_sql":
		var args map[string]interface{}
		_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)
		sqlRaw, _ := args["sql"].(string)
		sqlNormalized := strings.TrimSpace(strings.ToLower(sqlRaw))
		if !strings.HasPrefix(sqlNormalized, "select") {
			return `{"error":"only SELECT is allowed"}`
		}
		if !strings.Contains(sqlNormalized, "limit") {
			sqlRaw += " LIMIT 50"
		}
		rows, err := projectPool.Query(ctx, sqlRaw)
		if err != nil {
			return fmt.Sprintf(`{"error":%q}`, err.Error())
		}
		defer rows.Close()
		values, err := rows.Values()
		if err != nil {
			return `{"rows":[]}`
		}
		raw, _ := json.Marshal(map[string]interface{}{"sample_row": values})
		return string(raw)
	default:
		return `{"error":"unknown tool"}`
	}
}

func GetAIResponseIntelligent(ctx context.Context, projectPool *pgxpool.Pool, projectSlug string, messages []OpenAIMessage, projectContext, responseMode string) (string, error) {
	config, err := GetAIConfigFromDB(ctx)
	if err != nil {
		return "", err
	}
	if config.APIKey == "" {
		return "", fmt.Errorf("API key not configured")
	}
	if config.Model == "" {
		config.Model = "gpt-4o-mini"
	}
	baseURL := strings.TrimSuffix(strings.TrimSuffix(strings.TrimSpace(config.BaseURL), "/"), "/chat/completions")
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	endpoint := baseURL + "/chat/completions"

	lastUser := ""
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			lastUser = messages[i].Content
			break
		}
	}
	intent := detectIntent(lastUser)
	projectSnapshot := buildProjectContextSnapshot(ctx, projectPool, projectSlug)
	entityLink := resolveEntities(ctx, projectPool, projectSlug, lastUser)
	summary := summarizeOlderMessages(messages, 12)

	system := buildSystemPrompt(projectContext, projectSnapshot, responseMode) + `

INTENT DETECTED: ` + string(intent) + `
` + summary + `
` + entityLink + `

Use available tools when schema precision is required before answering.
`

	conv := []OpenAIMessageWithTools{{Role: "system", Content: system}}
	for _, m := range messages {
		conv = append(conv, OpenAIMessageWithTools{Role: m.Role, Content: m.Content})
	}

	requestWithTools := func(msgs []OpenAIMessageWithTools, tools []ToolDef) (OpenAIResponseWithTools, error) {
		reqBody := OpenAIRequestWithTools{
			Model:       config.Model,
			Messages:    msgs,
			MaxTokens:   config.MaxTokens,
			Temperature: config.Temperature,
			Tools:       tools,
		}
		body, _ := json.Marshal(reqBody)
		req, _ := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewBuffer(body))
		req.Header.Set("Authorization", "Bearer "+config.APIKey)
		req.Header.Set("Content-Type", "application/json")
		resp, err := doOpenAIRequestWithRetry(ctx, req, 3)
		if err != nil {
			return OpenAIResponseWithTools{}, err
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			return OpenAIResponseWithTools{}, fmt.Errorf("AI API error (status %d): %s", resp.StatusCode, string(raw))
		}
		var parsed OpenAIResponseWithTools
		if err := json.Unmarshal(raw, &parsed); err != nil {
			return OpenAIResponseWithTools{}, err
		}
		return parsed, nil
	}

	tools := agentTools()
	for turn := 0; turn < maxAgentTurns; turn++ {
		resp, err := requestWithTools(conv, tools)
		if err != nil {
			return "", err
		}
		if resp.Error != nil {
			return "", fmt.Errorf("AI error: %s", resp.Error.Message)
		}
		if len(resp.Choices) == 0 {
			return "", fmt.Errorf("no response from AI")
		}

		choice := resp.Choices[0]
		conv = append(conv, choice.Message)
		if choice.FinishReason != "tool_calls" || len(choice.Message.ToolCalls) == 0 {
			return choice.Message.Content, nil
		}

		for _, tc := range choice.Message.ToolCalls {
			result := executeAgentTool(ctx, projectPool, tc)
			conv = append(conv, OpenAIMessageWithTools{
				Role:       "tool",
				ToolCallID: tc.ID,
				Name:       tc.Function.Name,
				Content:    result,
			})
		}
	}
	return "", fmt.Errorf("agent loop exceeded max turns")
}

// --- CASCATA INTELLIGENCE: NAVIGATION COMMAND DETECTION (BACKEND FALLBACK) ---
// This is the backend's second line of defense. If the frontend's regex didn't catch
// a navigation command (e.g. due to voice transcription errors, different phrasing),
// the backend detects it here and returns a special JSON that forces the frontend to navigate.
// This prevents wasting API tokens on commands that should be handled locally.

type navRoute struct {
	Route    string
	Label    string
	IsGlobal bool
}

var navigationKeywords = map[string]navRoute{
	"banco de dados":  {Route: "database", Label: "Banco de Dados"},
	"tabelas":         {Route: "database", Label: "Banco de Dados"},
	"dados":           {Route: "database", Label: "Banco de Dados"},
	"database":        {Route: "database", Label: "Banco de Dados"},
	"data browser":    {Route: "database", Label: "Banco de Dados"},
	"autenticação":    {Route: "auth", Label: "Autenticação"},
	"autenticacao":    {Route: "auth", Label: "Autenticação"},
	"usuários":        {Route: "auth", Label: "Usuários"},
	"usuarios":        {Route: "auth", Label: "Usuários"},
	"auth":            {Route: "auth", Label: "Autenticação"},
	"regras":          {Route: "rls", Label: "Regras de Segurança"},
	"segurança":       {Route: "rls", Label: "Regras de Segurança"},
	"seguranca":       {Route: "rls", Label: "Regras de Segurança"},
	"políticas":       {Route: "rls", Label: "Políticas de Segurança"},
	"politicas":       {Route: "rls", Label: "Políticas de Segurança"},
	"rls":             {Route: "rls", Label: "Regras de Segurança"},
	"rpc":             {Route: "rpc", Label: "Funções RPC"},
	"funções":         {Route: "rpc", Label: "Funções RPC"},
	"funcoes":         {Route: "rpc", Label: "Funções RPC"},
	"lógica":          {Route: "rpc", Label: "Lógica"},
	"logica":          {Route: "rpc", Label: "Lógica"},
	"storage":         {Route: "storage", Label: "Storage"},
	"arquivos":        {Route: "storage", Label: "Arquivos"},
	"armazenamento":   {Route: "storage", Label: "Armazenamento"},
	"eventos":         {Route: "events", Label: "Eventos"},
	"gatilhos":        {Route: "events", Label: "Eventos"},
	"webhook":         {Route: "events", Label: "Webhooks"},
	"webhooks":        {Route: "events", Label: "Webhooks"},
	"push":            {Route: "push", Label: "Notificações Push"},
	"notificações":    {Route: "push", Label: "Notificações Push"},
	"notificacoes":    {Route: "push", Label: "Notificações Push"},
	"backups":         {Route: "backups", Label: "Backups"},
	"backup":          {Route: "backups", Label: "Backups"},
	"documentação":    {Route: "docs", Label: "Documentação API"},
	"documentacao":    {Route: "docs", Label: "Documentação API"},
	"api docs":        {Route: "docs", Label: "Documentação API"},
	"resumo":          {Route: "overview", Label: "Resumo do Projeto"},
	"dashboard":       {Route: "overview", Label: "Dashboard"},
	"painel":          {Route: "overview", Label: "Painel"},
	"overview":        {Route: "overview", Label: "Overview"},
	"configurações":   {Route: "settings", Label: "Configurações de Sistema", IsGlobal: true},
	"configuracoes":   {Route: "settings", Label: "Configurações de Sistema", IsGlobal: true},
	"sistema":         {Route: "settings", Label: "Sistema", IsGlobal: true},
	"settings":        {Route: "settings", Label: "Configurações", IsGlobal: true},
}

var navigationVerbs = regexp.MustCompile(`(?i)^(?:eu\s+quero\s+|gostaria\s+de\s+)?(?:ver|abrir|abre|abra|mostrar|mostre|mostra|ir\s+para|vá\s+para|vai\s+para|navegar\s+para|leve\s*-?\s*me\s+para|me\s+leve\s+para|open|go\s+to|show)\s+`)

var tableNavRegex = regexp.MustCompile(`(?i)(?:a\s+)?tabela\s+([a-zA-Z0-9_]+)`)

func DetectNavigationCommand(message string) map[string]interface{} {
	msg := strings.TrimSpace(message)
	if msg == "" {
		return nil
	}

	// Remove prepositions/articles noise
	cleaned := navigationVerbs.ReplaceAllString(msg, "")
	cleaned = strings.TrimSpace(cleaned)
	// Remove more articles: "a tela de", "o menu de", etc.
	articleRegex := regexp.MustCompile(`(?i)^(?:a\s+|o\s+|as\s+|os\s+)?(?:tela\s+de\s+|página\s+de\s+|pagina\s+de\s+|menu\s+de\s+|seção\s+de\s+|secao\s+de\s+|aba\s+de\s+)?`)
	cleaned = articleRegex.ReplaceAllString(cleaned, "")
	cleaned = strings.TrimSpace(cleaned)

	if cleaned == "" {
		return nil
	}

	// Only intercept if the original message contains a navigation verb
	if !navigationVerbs.MatchString(msg) {
		return nil
	}

	// 1. Check for table navigation (highest priority)
	if tableMatch := tableNavRegex.FindStringSubmatch(msg); len(tableMatch) > 1 {
		tableName := strings.ToLower(tableMatch[1])
		return map[string]interface{}{
			"action":          "navigation_fallback",
			"target_type":     "table",
			"table_name":      tableName,
			"target_route":    "database",
			"label":           "Tabela " + tableName,
			"spoken_feedback": "Abrindo a tabela " + tableName,
		}
	}

	// 2. Check screen/page navigation
	cleanedLower := strings.ToLower(cleaned)
	if route, ok := navigationKeywords[cleanedLower]; ok {
		return map[string]interface{}{
			"action":          "navigation_fallback",
			"target_type":     "screen",
			"target_route":    route.Route,
			"label":           route.Label,
			"is_global":       route.IsGlobal,
			"spoken_feedback": "Abrindo " + route.Label,
		}
	}

	// 3. Fuzzy match: check if any keyword is contained in the cleaned text
	for keyword, route := range navigationKeywords {
		if strings.Contains(cleanedLower, keyword) {
			return map[string]interface{}{
				"action":          "navigation_fallback",
				"target_type":     "screen",
				"target_route":    route.Route,
				"label":           route.Label,
				"is_global":       route.IsGlobal,
				"spoken_feedback": "Abrindo " + route.Label,
			}
		}
	}

	return nil
}
