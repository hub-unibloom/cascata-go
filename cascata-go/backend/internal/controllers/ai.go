package controllers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"cascata-backend/internal/services"
	"cascata-backend/internal/types"
	"github.com/go-chi/chi/v5"
)

type AiController struct {
	// Removemos os services daqui pois as queries atuais rodam direto no SystemPool
	// e injetar dependências não utilizadas é anti-padrão.
}

func sendJSONError(w http.ResponseWriter, message string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}

func (c *AiController) GetHistory(w http.ResponseWriter, r *http.Request) {
	val := r.Context().Value(types.CascataCtxKey)
	if val == nil {
		sendJSONError(w, "Context Lost", http.StatusInternalServerError)
		return
	}
	ctx := val.(*types.CascataRequest)
	if ctx.Project == nil {
		sendJSONError(w, "Project Context Required", http.StatusNotFound)
		return
	}

	sessionID := chi.URLParam(r, "session_id")

	rows, err := services.SystemPool.Query(r.Context(), "SELECT id, role, content, created_at FROM system.ai_history WHERE project_slug = $1 AND session_id = $2 ORDER BY created_at ASC", ctx.Project.Slug, sessionID)
	if err != nil {
		sendJSONError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var history []map[string]interface{}
	for rows.Next() {
		var id, role, content string
		var created time.Time
		if err := rows.Scan(&id, &role, &content, &created); err == nil {
			history = append(history, map[string]interface{}{
				"id":         id,
				"role":       role,
				"content":    content,
				"created_at": created,
			})
		}
	}

	if err := rows.Err(); err != nil {
		sendJSONError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if history == nil {
		history = make([]map[string]interface{}, 0)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(history)
}

func (c *AiController) ListSessions(w http.ResponseWriter, r *http.Request) {
	val := r.Context().Value(types.CascataCtxKey)
	if val == nil {
		sendJSONError(w, "Context Lost", http.StatusInternalServerError)
		return
	}
	ctx := val.(*types.CascataRequest)
	if ctx.Project == nil {
		sendJSONError(w, "Project Context Required", http.StatusNotFound)
		return
	}

	rows, err := services.SystemPool.Query(r.Context(), "SELECT id, project_slug, title, created_at, updated_at FROM system.ai_sessions WHERE project_slug = $1 ORDER BY updated_at DESC", ctx.Project.Slug)
	if err != nil {
		sendJSONError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var sessions []map[string]interface{}
	for rows.Next() {
		var id, projSlug, title string
		var created, updated time.Time
		if err := rows.Scan(&id, &projSlug, &title, &created, &updated); err == nil {
			sessions = append(sessions, map[string]interface{}{
				"id":           id,
				"project_slug": projSlug,
				"title":        title,
				"created_at":   created,
				"updated_at":   updated,
			})
		}
	}

	if err := rows.Err(); err != nil {
		sendJSONError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if sessions == nil {
		sessions = make([]map[string]interface{}, 0)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(sessions)
}


func (c *AiController) SearchSessions(w http.ResponseWriter, r *http.Request) {
	val := r.Context().Value(types.CascataCtxKey)
	if val == nil {
		sendJSONError(w, "Context Lost", http.StatusInternalServerError)
		return
	}
	ctx := val.(*types.CascataRequest)
	if ctx.Project == nil {
		sendJSONError(w, "Project Context Required", http.StatusNotFound)
		return
	}

	var body struct {
		Query string `json:"query"`
	}
	json.NewDecoder(r.Body).Decode(&body)

	if body.Query == "" {
		json.NewEncoder(w).Encode([]interface{}{})
		return
	}

	rows, err := services.SystemPool.Query(r.Context(), 
		`SELECT DISTINCT s.id, s.title, s.updated_at 
		 FROM system.ai_history h
		 JOIN system.ai_sessions s ON s.id::text = h.session_id
		 WHERE h.project_slug = $1 AND h.content ILIKE $2
		 ORDER BY s.updated_at DESC LIMIT 10`,
		ctx.Project.Slug, "%"+body.Query+"%")
	
	if err != nil {
		sendJSONError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var sessions []map[string]interface{}
	for rows.Next() {
		var id, title string
		var updated time.Time
		if err := rows.Scan(&id, &title, &updated); err == nil {
			sessions = append(sessions, map[string]interface{}{
				"id": id, "title": title, "updated_at": updated,
			})
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(sessions)
}

func (c *AiController) UpdateSession(w http.ResponseWriter, r *http.Request) {
	val := r.Context().Value(types.CascataCtxKey)
	if val == nil {
		sendJSONError(w, "Context Lost", http.StatusInternalServerError)
		return
	}
	ctx := val.(*types.CascataRequest)
	
	id := chi.URLParam(r, "id")
	var body struct {
		Title string `json:"title"`
	}
	json.NewDecoder(r.Body).Decode(&body)

	_, err := services.SystemPool.Exec(r.Context(), 
		"UPDATE system.ai_sessions SET title = $1, updated_at = NOW() WHERE id = $2 AND project_slug = $3",
		body.Title, id, ctx.Project.Slug)
	
	if err != nil {
		sendJSONError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Write([]byte(`{"success":true}`))
}

func (c *AiController) DeleteSession(w http.ResponseWriter, r *http.Request) {
	val := r.Context().Value(types.CascataCtxKey)
	if val == nil {
		sendJSONError(w, "Context Lost", http.StatusInternalServerError)
		return
	}
	ctx := val.(*types.CascataRequest)

	id := chi.URLParam(r, "id")

	// Deleta mensagens primeiro (foreign key constraint)
	_, err := services.SystemPool.Exec(r.Context(),
		"DELETE FROM system.ai_history WHERE session_id = $1 AND project_slug = $2",
		id, ctx.Project.Slug)
	if err != nil {
		sendJSONError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Deleta a sessão
	_, err = services.SystemPool.Exec(r.Context(),
		"DELETE FROM system.ai_sessions WHERE id = $1 AND project_slug = $2",
		id, ctx.Project.Slug)
	if err != nil {
		sendJSONError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Write([]byte(`{"success":true}`))
}

func (c *AiController) UpdateMessage(w http.ResponseWriter, r *http.Request) {
	val := r.Context().Value(types.CascataCtxKey)
	if val == nil {
		sendJSONError(w, "Context Lost", http.StatusInternalServerError)
		return
	}
	ctx := val.(*types.CascataRequest)

	id := chi.URLParam(r, "id")
	var body struct {
		Content string `json:"content"`
	}
	json.NewDecoder(r.Body).Decode(&body)

	if body.Content == "" {
		sendJSONError(w, "Content required", http.StatusBadRequest)
		return
	}

	_, err := services.SystemPool.Exec(r.Context(),
		"UPDATE system.ai_history SET content = $1, updated_at = NOW() WHERE id = $2 AND project_slug = $3",
		body.Content, id, ctx.Project.Slug)

	if err != nil {
		sendJSONError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Write([]byte(`{"success":true}`))
}

func (c *AiController) DeleteMessage(w http.ResponseWriter, r *http.Request) {
	val := r.Context().Value(types.CascataCtxKey)
	if val == nil {
		sendJSONError(w, "Context Lost", http.StatusInternalServerError)
		return
	}
	ctx := val.(*types.CascataRequest)

	id := chi.URLParam(r, "id")

	_, err := services.SystemPool.Exec(r.Context(),
		"DELETE FROM system.ai_history WHERE id = $1 AND project_slug = $2",
		id, ctx.Project.Slug)

	if err != nil {
		sendJSONError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Write([]byte(`{"success":true}`))
}

func (c *AiController) Chat(w http.ResponseWriter, r *http.Request) {
	val := r.Context().Value(types.CascataCtxKey)
	if val == nil {
		sendJSONError(w, "Context Lost", http.StatusInternalServerError)
		return
	}
	ctx := val.(*types.CascataRequest)

	var payload struct {
		SessionID string `json:"session_id"`
		Messages  []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
		Source  string `json:"source"`
		Stream  bool   `json:"stream"`
		APIMode string `json:"api_mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		sendJSONError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	// Validate session
	if payload.SessionID == "" {
		sendJSONError(w, "Session ID required", http.StatusBadRequest)
		return
	}

	// Update or create session
	_, err := services.SystemPool.Exec(r.Context(), 
		`INSERT INTO system.ai_sessions (id, project_slug, title) 
		 VALUES ($1, $2, COALESCE((SELECT title FROM system.ai_sessions WHERE id = $1), 'Nova Conversa')) 
		 ON CONFLICT (id) DO UPDATE SET updated_at = NOW()`,
		payload.SessionID, ctx.Project.Slug)
	if err != nil {
		sendJSONError(w, "Failed to update session", http.StatusInternalServerError)
		return
	}

	// Convert messages to OpenAI format
	var openAIMessages []services.OpenAIMessage
	for _, msg := range payload.Messages {
		openAIMessages = append(openAIMessages, services.OpenAIMessage{
			Role:    msg.Role,
			Content: msg.Content,
		})
	}

	// --- CASCATA INTELLIGENCE: BACKEND NAVIGATION FALLBACK ---
	// If the frontend missed a navigation command, the backend detects it here
	// and returns a special JSON response forcing the frontend to navigate.
	// This is a second line of defense — the frontend should catch most commands,
	// but voice transcription errors can cause misses.
	if len(payload.Messages) > 0 {
		lastUserMsg := strings.ToLower(strings.TrimSpace(payload.Messages[len(payload.Messages)-1].Content))
		if navFallback := services.DetectNavigationCommand(lastUserMsg); navFallback != nil {
			fmt.Printf("[Cascata Intelligence] Backend Navigation Fallback Triggered: %s -> %s\n", lastUserMsg, navFallback["label"])
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{
				"navigation_fallback": navFallback,
			})
			return
		}
	}

	// Get AI response
	projectContext := fmt.Sprintf("%s (slug: %s, db: %s)", ctx.Project.Name, ctx.Project.Slug, ctx.Project.DbName)
	var aiResponse string
	
	if services.IsAIServiceAvailable(r.Context()) {
		if payload.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Connection", "keep-alive")

			aiResponse, err = services.StreamAIResponseWithMode(r.Context(), w, openAIMessages, projectContext, payload.APIMode)
		} else {
			if ctx.ProjectPool != nil {
				aiResponse, err = services.GetAIResponseIntelligent(r.Context(), ctx.ProjectPool, ctx.Project.Slug, openAIMessages, projectContext, payload.APIMode)
			} else {
				aiResponse, err = services.GetAIResponseWithMode(r.Context(), openAIMessages, projectContext, payload.APIMode)
			}
		}
		if err != nil {
			// Log error but still provide fallback response
			fmt.Printf("AI Service Error: %v\n", err)
			aiResponse = services.GetFallbackResponse(ctx.Project.Name)
			if payload.Stream {
				fmt.Fprintf(w, "data: {\"delta\": %q}\n\n", aiResponse)
				fmt.Fprintf(w, "data: {\"done\": true}\n\n")
			}
		}
	} else {
		aiResponse = services.GetFallbackResponse(ctx.Project.Name)
		if payload.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Connection", "keep-alive")
			fmt.Fprintf(w, "data: {\"delta\": %q}\n\n", aiResponse)
			fmt.Fprintf(w, "data: {\"done\": true}\n\n")
		}
	}

	// Save to history if there are messages
	if len(payload.Messages) > 0 {
		lastUserMsg := payload.Messages[len(payload.Messages)-1].Content
		_, err = services.SystemPool.Exec(r.Context(), 
			"INSERT INTO system.ai_history (project_slug, session_id, role, content) VALUES ($1, $2, 'user', $3), ($1, $2, 'assistant', $4)",
			ctx.Project.Slug, payload.SessionID, lastUserMsg, aiResponse)
		if err != nil {
			fmt.Printf("Failed to save history: %v\n", err)
		}
	}

	if payload.Stream {
		return
	}

	resp := map[string]interface{}{
		"choices": []interface{}{
			map[string]interface{}{
				"message": map[string]string{
					"role":    "assistant",
					"content": aiResponse,
				},
			},
		},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (c *AiController) GetOpenApiSpec(w http.ResponseWriter, r *http.Request) {
	val := r.Context().Value(types.CascataCtxKey)
	if val == nil {
		sendJSONError(w, "Context Lost", http.StatusInternalServerError)
		return
	}
	ctx := val.(*types.CascataRequest)
	if ctx.Project == nil {
		sendJSONError(w, "Project Context Required", http.StatusNotFound)
		return
	}

	// Check if schema exposure is enabled (required for FlutterFlow/Supabase SDK compatibility)
	if !ctx.Project.Metadata.SchemaExposure {
		sendJSONError(w, "Schema Discovery Disabled", http.StatusForbidden)
		return
	}

	// Use OpenApiService to generate spec (OpenAPI 3.0 - Modern Sovereign Mode)
	spec, err := services.GenerateOpenApiSpec(ctx.Project.Slug, ctx.Project.DbName, r.Host, ctx.ProjectPool)
	if err != nil {
		sendJSONError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(spec)
}

// GetSwaggerSpec returns Swagger 2.0 spec (PostgREST/Supabase/FlutterFlow compatibility mode)
func (c *AiController) GetSwaggerSpec(w http.ResponseWriter, r *http.Request) {
	val := r.Context().Value(types.CascataCtxKey)
	if val == nil {
		sendJSONError(w, "Context Lost", http.StatusInternalServerError)
		return
	}
	ctx := val.(*types.CascataRequest)
	if ctx.Project == nil {
		sendJSONError(w, "Project Context Required", http.StatusNotFound)
		return
	}

	// Check if schema exposure is enabled (required for FlutterFlow/Supabase SDK compatibility)
	if !ctx.Project.Metadata.SchemaExposure {
		sendJSONError(w, "Schema Discovery Disabled", http.StatusForbidden)
		return
	}

	// Determine basePath based on whether using custom domain or slug
	var basePath string
	if ctx.Project.CustomDomain != "" && r.Host == ctx.Project.CustomDomain {
		// Custom domain mode: /rest/v1
		basePath = "/rest/v1"
	} else {
		// Slug mode: /api/data/{slug}/rest/v1
		basePath = fmt.Sprintf("/api/data/%s/rest/v1", ctx.Project.Slug)
	}

	// Generate Swagger 2.0 spec (PostgREST compatibility)
	spec, err := services.GenerateSwagger2Spec(ctx.Project.Slug, ctx.Project.DbName, r.Host, basePath, ctx.ProjectPool)
	if err != nil {
		sendJSONError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(spec)
}

func (c *AiController) ListDocPages(w http.ResponseWriter, r *http.Request) {
	val := r.Context().Value(types.CascataCtxKey)
	if val == nil {
		sendJSONError(w, "Context Lost", http.StatusInternalServerError)
		return
	}
	ctx := val.(*types.CascataRequest)
	if ctx.Project == nil {
		sendJSONError(w, "Project Context Required", http.StatusNotFound)
		return
	}

	rows, err := services.SystemPool.Query(r.Context(),
		"SELECT id, project_slug, slug, title, content_markdown, created_at, updated_at FROM system.doc_pages WHERE project_slug = $1 ORDER BY title ASC",
		ctx.Project.Slug)
	if err != nil {
		sendJSONError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var docs []map[string]interface{}
	for rows.Next() {
		var id, projSlug, slug, title string
		var contentMarkdown *string
		var created, updated time.Time
		if err := rows.Scan(&id, &projSlug, &slug, &title, &contentMarkdown, &created, &updated); err == nil {
			doc := map[string]interface{}{
				"id":             id,
				"project_slug":   projSlug,
				"slug":           slug,
				"title":          title,
				"created_at":     created,
				"updated_at":     updated,
			}
			if contentMarkdown != nil {
				doc["content_markdown"] = *contentMarkdown
			}
			docs = append(docs, doc)
		}
	}

	if docs == nil {
		docs = make([]map[string]interface{}, 0)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(docs)
}
