package services

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"
)

type OpenAIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type OpenAIRequest struct {
	Model       string          `json:"model"`
	Messages    []OpenAIMessage `json:"messages"`
	MaxTokens   int             `json:"max_tokens,omitempty"`
	Temperature float64         `json:"temperature,omitempty"`
	Stream      bool            `json:"stream,omitempty"`
}

type OpenAIChoice struct {
	Message OpenAIMessage `json:"message"`
}

type OpenAIResponse struct {
	Choices []OpenAIChoice `json:"choices"`
	Error   *OpenAIError   `json:"error,omitempty"`
}

type OpenAIError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
}

var (
	httpClient = &http.Client{
		Timeout: 60 * time.Second,
	}
)

func estimateTokens(messages []OpenAIMessage) int {
	totalChars := 0
	for _, m := range messages {
		totalChars += len(m.Content)
	}
	return totalChars / 4
}

func trimMessagesToBudget(messages []OpenAIMessage, maxTokens int) []OpenAIMessage {
	if len(messages) == 0 {
		return messages
	}
	if maxTokens <= 0 {
		maxTokens = 4000
	}
	if estimateTokens(messages) <= maxTokens {
		return messages
	}

	result := make([]OpenAIMessage, 0, len(messages))
	start := 0
	if messages[0].Role == "system" {
		result = append(result, messages[0])
		start = 1
	}

	recent := make([]OpenAIMessage, 0, len(messages)-start)
	for i := len(messages) - 1; i >= start; i-- {
		candidateRecent := append([]OpenAIMessage{messages[i]}, recent...)
		candidate := append(append([]OpenAIMessage{}, result...), candidateRecent...)
		if estimateTokens(candidate) > maxTokens {
			break
		}
		recent = candidateRecent
	}
	result = append(result, recent...)

	if len(result) == 0 {
		return messages[len(messages)-1:]
	}
	return result
}

func extractSchemaContext(ctx context.Context) string {
	rows, err := SystemPool.Query(ctx, `
		SELECT table_name, column_name, data_type, is_nullable
		FROM information_schema.columns
		WHERE table_schema = 'public'
		ORDER BY table_name, ordinal_position
	`)
	if err != nil {
		return "Schema unavailable."
	}
	defer rows.Close()

	type col struct {
		name     string
		dataType string
		nullable string
	}
	tableMap := map[string][]col{}
	var tableName, columnName, dataType, nullable string
	for rows.Next() {
		if scanErr := rows.Scan(&tableName, &columnName, &dataType, &nullable); scanErr != nil {
			continue
		}
		tableMap[tableName] = append(tableMap[tableName], col{name: columnName, dataType: dataType, nullable: nullable})
	}
	if len(tableMap) == 0 {
		return "No user tables found in public schema."
	}

	tableNames := make([]string, 0, len(tableMap))
	for t := range tableMap {
		tableNames = append(tableNames, t)
	}
	sort.Strings(tableNames)

	var b strings.Builder
	for _, t := range tableNames {
		b.WriteString("- ")
		b.WriteString(t)
		b.WriteString(": ")
		cols := tableMap[t]
		for idx, c := range cols {
			if idx > 0 {
				b.WriteString(", ")
			}
			b.WriteString(c.name)
			b.WriteString(" ")
			b.WriteString(c.dataType)
			if strings.EqualFold(c.nullable, "NO") {
				b.WriteString(" NOT NULL")
			}
		}
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

func doOpenAIRequestWithRetry(ctx context.Context, req *http.Request, maxRetries int) (*http.Response, error) {
	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		resp, err := httpClient.Do(req.Clone(ctx))
		if err == nil && resp.StatusCode < 500 && resp.StatusCode != http.StatusTooManyRequests {
			return resp, nil
		}
		if err == nil {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			lastErr = fmt.Errorf("status %d: %s", resp.StatusCode, string(body))
		} else {
			lastErr = err
		}
		if attempt == maxRetries {
			break
		}
		backoff := time.Duration(math.Pow(2, float64(attempt))) * 500 * time.Millisecond
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	return nil, fmt.Errorf("failed after retries: %w", lastErr)
}

// AIConfig holds the AI configuration from database
type AIConfig struct {
	APIKey          string  `json:"api_key"`
	Model           string  `json:"model"`
	BaseURL         string  `json:"base_url"`
	MaxTokens       int     `json:"max_tokens"`
	Temperature     float64 `json:"temperature"`
	ResponseMode    string  `json:"response_mode"`
	EnableStreaming bool    `json:"enable_streaming"`
}

// DefaultAIConfig returns empty configuration (no env fallback - config only via panel)
func DefaultAIConfig() *AIConfig {
	return &AIConfig{
		APIKey:      "",
		Model:       "gpt-4o-mini",
		BaseURL:     "",
		MaxTokens:   2000,
		Temperature: 0.7,
		ResponseMode: "chat_completions",
		EnableStreaming: false,
	}
}

// GetAIConfigFromDB retrieves AI configuration from system settings (from ui_settings table)
func GetAIConfigFromDB(ctx context.Context) (*AIConfig, error) {
	config := DefaultAIConfig()
	
	// Query system settings from ui_settings table (same as admin controller)
	var settingsStr string
	err := SystemPool.QueryRow(ctx, 
		"SELECT settings FROM system.ui_settings WHERE project_slug = '_system_root_' AND table_name = 'system_config'").Scan(&settingsStr)
	
	if err != nil {
		// No settings found, use env defaults
		return config, nil
	}
	
	// Parse settings JSON
	var settings map[string]interface{}
	if err := json.Unmarshal([]byte(settingsStr), &settings); err != nil {
		return config, nil
	}
	
	// Extract ai_config
	aiConfigRaw, ok := settings["ai_config"]
	if !ok {
		return config, nil
	}
	
	aiConfigMap, ok := aiConfigRaw.(map[string]interface{})
	if !ok {
		return config, nil
	}
	
	// Override with DB values if present
	if v, ok := aiConfigMap["api_key"].(string); ok && v != "" {
		config.APIKey = v
	}
	if v, ok := aiConfigMap["model"].(string); ok && v != "" {
		config.Model = v
	}
	if v, ok := aiConfigMap["base_url"].(string); ok && v != "" {
		config.BaseURL = v
	}
	if v, ok := aiConfigMap["max_tokens"].(float64); ok && v > 0 {
		config.MaxTokens = int(v)
	}
	if v, ok := aiConfigMap["temperature"].(float64); ok && v > 0 {
		config.Temperature = v
	}
	if v, ok := aiConfigMap["response_mode"].(string); ok && v != "" {
		config.ResponseMode = strings.ToLower(v)
	}
	if v, ok := aiConfigMap["enable_streaming"].(bool); ok {
		config.EnableStreaming = v
	}
	
	return config, nil
}

func buildSystemPrompt(projectContext, schemaContext, responseMode string) string {
	modeNotes := "Use standard Chat Completions behavior."
	if responseMode == "realtime" {
		modeNotes = "Prioritize low-latency concise responses suitable for real-time streaming conversations."
	}
	return `You are Cascata Architect, an intelligent AI agent specialized in database design and application architecture.
You are not just a chatbot - you are an autonomous agent that can execute, correct, and iterate.

Current Project: ` + projectContext + `

Current Database Schema:
` + schemaContext + `

Runtime Mode: ` + responseMode + `
Mode Guidance: ` + modeNotes + `

## AGENT BEHAVIOR RULES

1. **SEPARATE EXPLANATION FROM CODE**: Always structure your response in two distinct parts:
   - First: Brief explanation (2-3 sentences max)
   - Second: The actual code/SQL in a code block
   
   Example format:
   "Here's the table you requested. It includes id, name, and created_at columns.
   
   ` + "```" + `sql
   CREATE TABLE users (id SERIAL PRIMARY KEY, name TEXT NOT NULL, created_at TIMESTAMP DEFAULT NOW());
   ` + "```" + `"

2. **SQL EXECUTION**: When providing SQL:
   - Use ` + "```" + `sql code blocks ONLY for the actual SQL code
   - Never include explanatory text inside the code block
   - If multiple statements are needed, separate them clearly

3. **ERROR CORRECTION**: When receiving error feedback:
   - Analyze the specific SQL error message
   - Identify the root cause (syntax, missing table, constraint violation, etc.)
   - Return ONLY the corrected SQL in a code block
   - No re-explanation needed - just the fix

4. **JSON ACTIONS**: For table creation via API:
   - Return JSON with "action": "create_table" format
   - Include proper column definitions with types
   - Specify primary keys and constraints

5. **BE CONCISE**: Users want results, not lectures. Keep explanations brief and actionable.

6. **LANGUAGE**: Always respond in the same language as the user.

## OUTPUT FORMATS

- For SQL: ` + "```" + `sql ... ` + "```" + `
- For JSON: {"action": "create_table", "name": "...", "columns": [...]}
- For explanations: Plain text (no code block)`
}

func buildOpenAIRequest(ctx context.Context, messages []OpenAIMessage, projectContext string, stream bool, responseMode string) (*http.Request, *AIConfig, error) {
	config, err := GetAIConfigFromDB(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get AI config: %w", err)
	}
	if config.APIKey == "" {
		return nil, nil, fmt.Errorf("API key not configured")
	}
	if config.Model == "" {
		config.Model = "gpt-4o-mini"
	}

	baseURL := config.BaseURL
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	baseURL = strings.TrimSuffix(baseURL, "/")
	baseURL = strings.TrimSuffix(baseURL, "/chat/completions")
	endpoint := baseURL + "/chat/completions"

	mode := strings.ToLower(responseMode)
	if mode == "" {
		mode = config.ResponseMode
	}
	if mode != "realtime" {
		mode = "chat_completions"
	}

	systemPrompt := buildSystemPrompt(projectContext, extractSchemaContext(ctx), mode)
	fullMessages := []OpenAIMessage{{Role: "system", Content: systemPrompt}}
	fullMessages = append(fullMessages, messages...)

	contextBudget := config.MaxTokens * 2
	if contextBudget < 1200 {
		contextBudget = 1200
	}
	fullMessages = trimMessagesToBudget(fullMessages, contextBudget)

	reqBody := OpenAIRequest{
		Model:       config.Model,
		Messages:    fullMessages,
		MaxTokens:   config.MaxTokens,
		Temperature: config.Temperature,
		Stream:      stream,
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+config.APIKey)
	req.Header.Set("Content-Type", "application/json")
	return req, config, nil
}

// IsAIServiceAvailable returns true if AI service is configured
func IsAIServiceAvailable(ctx context.Context) bool {
	config, _ := GetAIConfigFromDB(ctx)
	return config.APIKey != ""
}

// GetAIResponse sends messages to OpenAI-compatible API and returns the response
func GetAIResponse(ctx context.Context, messages []OpenAIMessage, projectContext string) (string, error) {
	return GetAIResponseWithMode(ctx, messages, projectContext, "")
}

func GetAIResponseWithMode(ctx context.Context, messages []OpenAIMessage, projectContext, responseMode string) (string, error) {
	req, _, err := buildOpenAIRequest(ctx, messages, projectContext, false, responseMode)
	if err != nil {
		return "", err
	}
	resp, err := doOpenAIRequestWithRetry(ctx, req, 3)
	if err != nil { return "", fmt.Errorf("failed to call AI API: %w", err) }
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("AI API error (status %d): %s", resp.StatusCode, string(body))
	}

	var openAIResp OpenAIResponse
	if err := json.Unmarshal(body, &openAIResp); err != nil {
		return "", fmt.Errorf("failed to parse response: %w", err)
	}

	if openAIResp.Error != nil {
		return "", fmt.Errorf("AI error: %s", openAIResp.Error.Message)
	}

	if len(openAIResp.Choices) == 0 {
		return "", fmt.Errorf("no response from AI")
	}

	return openAIResp.Choices[0].Message.Content, nil
}

func StreamAIResponseWithMode(ctx context.Context, w http.ResponseWriter, messages []OpenAIMessage, projectContext, responseMode string) (string, error) {
	req, _, err := buildOpenAIRequest(ctx, messages, projectContext, true, responseMode)
	if err != nil {
		return "", err
	}
	resp, err := doOpenAIRequestWithRetry(ctx, req, 3)
	if err != nil {
		return "", fmt.Errorf("failed to call AI API: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("AI API error (status %d): %s", resp.StatusCode, string(body))
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		return "", fmt.Errorf("streaming not supported by response writer")
	}

	sendEvent := func(payload map[string]interface{}) {
		raw, _ := json.Marshal(payload)
		fmt.Fprintf(w, "data: %s\n\n", string(raw))
		flusher.Flush()
	}

	reader := bufio.NewReader(resp.Body)
	var full strings.Builder
	for {
		line, readErr := reader.ReadString('\n')
		if readErr != nil && readErr != io.EOF {
			return full.String(), readErr
		}

		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")
			if data == "[DONE]" {
				sendEvent(map[string]interface{}{"done": true})
				break
			}

			var chunk map[string]interface{}
			if err := json.Unmarshal([]byte(data), &chunk); err == nil {
				choices, _ := chunk["choices"].([]interface{})
				if len(choices) > 0 {
					if choice, ok := choices[0].(map[string]interface{}); ok {
						if delta, ok := choice["delta"].(map[string]interface{}); ok {
							if piece, ok := delta["content"].(string); ok && piece != "" {
								full.WriteString(piece)
								sendEvent(map[string]interface{}{"delta": piece})
							}
						}
					}
				}
			}
		}

		if readErr == io.EOF {
			sendEvent(map[string]interface{}{"done": true})
			break
		}
	}
	return full.String(), nil
}

// GetFallbackResponse returns a fallback response when AI service is unavailable
func GetFallbackResponse(projectName string) string {
	return fmt.Sprintf(`Olá! Sou o Cascata Architect. Estou aqui para ajudar com seu projeto **%s**.

Para habilitar respostas inteligentes, configure:

1. **Via System Settings:** Acesse Configurações > AI Configuration
2. **Via Variáveis de Ambiente:**
   - OPENAI_API_KEY: sua chave de API
   - OPENAI_MODEL: modelo (ex: gpt-4o-mini, gemini-2-flash)
   - OPENAI_BASE_URL: URL base (opcional, para providers compatíveis)

**Providers suportados (OpenAI API compatibility):**
• OpenAI (api.openai.com)
• Google Gemini (generativelanguage.googleapis.com/v1beta/openai)
• Anthropic Claude (via proxy)
• Outros compatíveis com OpenAI API

Posso ajudar você a:
• Criar tabelas e definir schemas
• Gerar SQL para consultas
• Projetar APIs REST
• E muito mais!`, projectName)
}
