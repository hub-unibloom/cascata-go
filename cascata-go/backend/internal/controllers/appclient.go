package controllers

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"cascata-backend/internal/services"
	"cascata-backend/internal/types"

	"github.com/go-chi/chi/v5"
)

type AppClientController struct{}

func NewAppClientController() *AppClientController {
	return &AppClientController{}
}

// ListAppClients returns all app clients for the project
func (c *AppClientController) ListAppClients(w http.ResponseWriter, r *http.Request) {
	val := r.Context().Value(types.CascataCtxKey)
	if val == nil {
		http.Error(w, `{"error":"Context Lost"}`, 500)
		return
	}
	ctx := val.(*types.CascataRequest)

	appClients := ctx.Project.Metadata.AppClients
	if appClients == nil {
		appClients = []types.AppClient{}
	}

	// Return sanitized version (without nonce)
	sanitized := make([]map[string]interface{}, len(appClients))
	for i, ac := range appClients {
		sanitized[i] = map[string]interface{}{
			"id":              ac.ID,
			"name":            ac.Name,
			"site_url":        ac.SiteURL,
			"allowed_origins": ac.AllowedOrigins,
			"allowed_tables":  ac.AllowedTables,
			"blocked_tables":  ac.BlockedTables,
			"active":          ac.Active,
			"created_at":      time.Now().Add(-time.Hour * 24 * time.Duration(i)).Format(time.RFC3339), // Placeholder
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(sanitized)
}

// CreateAppClient creates a new app client
func (c *AppClientController) CreateAppClient(w http.ResponseWriter, r *http.Request) {
	val := r.Context().Value(types.CascataCtxKey)
	if val == nil {
		http.Error(w, `{"error":"Context Lost"}`, 500)
		return
	}
	ctx := val.(*types.CascataRequest)

	var body struct {
		Name           string   `json:"name"`
		SiteURL        string   `json:"site_url"`
		AllowedOrigins []string `json:"allowed_origins"`
		AllowedTables  []string `json:"allowed_tables"`
		BlockedTables  []string `json:"blocked_tables"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"Invalid JSON"}`, 400)
		return
	}

	if body.Name == "" {
		http.Error(w, `{"error":"Name is required"}`, 400)
		return
	}

	// Create new app client
	appClient, anonKey, err := services.CreateAppClient(body.Name, body.SiteURL, body.AllowedOrigins, body.AllowedTables, body.BlockedTables, ctx.Project.JWTSecret)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, 500)
		return
	}

	// Add to existing app clients
	appClients := ctx.Project.Metadata.AppClients
	appClients = append(appClients, *appClient)

	// Update project metadata
	if err := c.updateAppClientsMetadata(r.Context(), ctx.Project.Slug, appClients); err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, 500)
		return
	}

	// Update in-memory project
	ctx.Project.Metadata.AppClients = appClients
	ctx.Project.AppClientIndex = services.BuildAppClientIndex(appClients, ctx.Project.JWTSecret)

	// Return response (only show anon_key once)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"id":              appClient.ID,
		"name":            appClient.Name,
		"site_url":        appClient.SiteURL,
		"allowed_origins": appClient.AllowedOrigins,
		"allowed_tables":  appClient.AllowedTables,
		"blocked_tables":  appClient.BlockedTables,
		"active":          appClient.Active,
		"anon_key":        anonKey, // Only shown once at creation
		"warning":         "Store this key securely - it won't be shown again",
	})
}

// GetAppClient returns a single app client
func (c *AppClientController) GetAppClient(w http.ResponseWriter, r *http.Request) {
	val := r.Context().Value(types.CascataCtxKey)
	if val == nil {
		http.Error(w, `{"error":"Context Lost"}`, 500)
		return
	}
	ctx := val.(*types.CascataRequest)

	id := chi.URLParam(r, "id")
	if id == "" {
		http.Error(w, `{"error":"ID required"}`, 400)
		return
	}

	// Find app client
	var appClient *types.AppClient
	for i := range ctx.Project.Metadata.AppClients {
		if ctx.Project.Metadata.AppClients[i].ID == id {
			appClient = &ctx.Project.Metadata.AppClients[i]
			break
		}
	}

	if appClient == nil {
		http.Error(w, `{"error":"App Client not found"}`, 404)
		return
	}

	// Return sanitized version (without nonce)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"id":              appClient.ID,
		"name":            appClient.Name,
		"site_url":        appClient.SiteURL,
		"allowed_origins": appClient.AllowedOrigins,
		"allowed_tables":  appClient.AllowedTables,
		"blocked_tables":  appClient.BlockedTables,
		"active":          appClient.Active,
	})
}

// UpdateAppClient updates an existing app client
func (c *AppClientController) UpdateAppClient(w http.ResponseWriter, r *http.Request) {
	val := r.Context().Value(types.CascataCtxKey)
	if val == nil {
		http.Error(w, `{"error":"Context Lost"}`, 500)
		return
	}
	ctx := val.(*types.CascataRequest)

	id := chi.URLParam(r, "id")
	if id == "" {
		http.Error(w, `{"error":"ID required"}`, 400)
		return
	}

	var body struct {
		Name           string   `json:"name,omitempty"`
		SiteURL        string   `json:"site_url,omitempty"`
		AllowedOrigins []string `json:"allowed_origins,omitempty"`
		AllowedTables  []string `json:"allowed_tables,omitempty"`
		BlockedTables  []string `json:"blocked_tables,omitempty"`
		Active         *bool    `json:"active,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"Invalid JSON"}`, 400)
		return
	}

	// Find and update app client
	found := false
	appClients := ctx.Project.Metadata.AppClients
	for i := range appClients {
		if appClients[i].ID == id {
			found = true
			if body.Name != "" {
				appClients[i].Name = body.Name
			}
			if body.SiteURL != "" {
				appClients[i].SiteURL = body.SiteURL
			}
			if body.AllowedOrigins != nil {
				appClients[i].AllowedOrigins = body.AllowedOrigins
			}
			if body.AllowedTables != nil {
				appClients[i].AllowedTables = body.AllowedTables
			}
			if body.BlockedTables != nil {
				appClients[i].BlockedTables = body.BlockedTables
			}
			if body.Active != nil {
				appClients[i].Active = *body.Active
			}
			break
		}
	}

	if !found {
		http.Error(w, `{"error":"App Client not found"}`, 404)
		return
	}

	// Update project metadata
	if err := c.updateAppClientsMetadata(r.Context(), ctx.Project.Slug, appClients); err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, 500)
		return
	}

	// Update in-memory project
	ctx.Project.Metadata.AppClients = appClients
	ctx.Project.AppClientIndex = services.BuildAppClientIndex(appClients, ctx.Project.JWTSecret)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
}

// DeleteAppClient removes an app client
func (c *AppClientController) DeleteAppClient(w http.ResponseWriter, r *http.Request) {
	val := r.Context().Value(types.CascataCtxKey)
	if val == nil {
		http.Error(w, `{"error":"Context Lost"}`, 500)
		return
	}
	ctx := val.(*types.CascataRequest)

	id := chi.URLParam(r, "id")
	if id == "" {
		http.Error(w, `{"error":"ID required"}`, 400)
		return
	}

	// Remove app client from slice
	appClients := ctx.Project.Metadata.AppClients
	newAppClients := make([]types.AppClient, 0, len(appClients))
	for _, ac := range appClients {
		if ac.ID != id {
			newAppClients = append(newAppClients, ac)
		}
	}

	if len(newAppClients) == len(appClients) {
		http.Error(w, `{"error":"App Client not found"}`, 404)
		return
	}

	// Update project metadata
	if err := c.updateAppClientsMetadata(r.Context(), ctx.Project.Slug, newAppClients); err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, 500)
		return
	}

	// Update in-memory project
	ctx.Project.Metadata.AppClients = newAppClients
	ctx.Project.AppClientIndex = services.BuildAppClientIndex(newAppClients, ctx.Project.JWTSecret)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
}

// RotateAppClientKey rotates the anon key for an app client
func (c *AppClientController) RotateAppClientKey(w http.ResponseWriter, r *http.Request) {
	val := r.Context().Value(types.CascataCtxKey)
	if val == nil {
		http.Error(w, `{"error":"Context Lost"}`, 500)
		return
	}
	ctx := val.(*types.CascataRequest)

	id := chi.URLParam(r, "id")
	if id == "" {
		http.Error(w, `{"error":"ID required"}`, 400)
		return
	}

	// Find app client
	var appClient *types.AppClient
	for i := range ctx.Project.Metadata.AppClients {
		if ctx.Project.Metadata.AppClients[i].ID == id {
			appClient = &ctx.Project.Metadata.AppClients[i]
			break
		}
	}

	if appClient == nil {
		http.Error(w, `{"error":"App Client not found"}`, 404)
		return
	}

	// Rotate nonce and generate new key
	newAnonKey, err := services.RotateAppClientNonce(appClient, ctx.Project.JWTSecret)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, 500)
		return
	}

	// Update project metadata
	if err := c.updateAppClientsMetadata(r.Context(), ctx.Project.Slug, ctx.Project.Metadata.AppClients); err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, 500)
		return
	}

	// Update in-memory project
	ctx.Project.AppClientIndex = services.BuildAppClientIndex(ctx.Project.Metadata.AppClients, ctx.Project.JWTSecret)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"anon_key": newAnonKey,
		"warning":  "Store this key securely - the old key is now invalid",
	})
}

// updateAppClientsMetadata updates the app_clients in project metadata
func (c *AppClientController) updateAppClientsMetadata(ctx context.Context, projectSlug string, appClients []types.AppClient) error {
	appClientsJSON, err := json.Marshal(appClients)
	if err != nil {
		return err
	}

	_, err = services.SystemPool.Exec(ctx,
		"UPDATE system.projects SET metadata = jsonb_set(COALESCE(metadata, '{}'::jsonb), '{app_clients}', $1::jsonb) WHERE slug = $2",
		appClientsJSON, projectSlug)

	return err
}
