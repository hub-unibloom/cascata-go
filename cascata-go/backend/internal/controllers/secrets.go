package controllers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"cascata-backend/internal/services"
	"cascata-backend/internal/types"
	"cascata-backend/internal/utils"
	"github.com/go-chi/chi/v5"
)

type SecretsController struct {
	CryptoSvc services.CryptoService
	VaultSvc  *services.VaultService
}

func NewSecretsController(crypto services.CryptoService) *SecretsController {
	return &SecretsController{CryptoSvc: crypto, VaultSvc: services.NewVaultService(&crypto)}
}

func (c *SecretsController) ListSecrets(w http.ResponseWriter, r *http.Request) {
	val := r.Context().Value(types.CascataCtxKey)
	if val == nil {
		http.Error(w, `{"error":"Context Lost"}`, 500)
		return
	}
	ctx := val.(*types.CascataRequest)

	parentId := r.URL.Query().Get("parentId")
	if parentId == "root" {
		parentId = ""
	}

	// 1. Fetch from system.project_secrets (True Synergy)
	query := `
		SELECT id, name, type, description, metadata, created_at, updated_at,
		(SELECT COUNT(*) FROM system.project_secrets c WHERE c.parent_id = s.id) as children_count
		FROM system.project_secrets s
		WHERE project_slug = $1 
		AND ((NULLIF($2, '')::uuid IS NULL AND parent_id IS NULL) OR (parent_id = NULLIF($2, '')::uuid))
		ORDER BY type DESC, name ASC
	`
	rows, err := services.SystemPool.Query(r.Context(), query, ctx.Project.Slug, parentId)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()

	res := []map[string]interface{}{}
	fDesc := rows.FieldDescriptions()
	for rows.Next() {
		vals, _ := rows.Values()
		row := make(map[string]interface{})
		for i, fd := range fDesc {
			row[fd.Name] = utils.PurifyPgxValue(vals[i])
		}
		res = append(res, row)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)
}

func (c *SecretsController) SetSecret(w http.ResponseWriter, r *http.Request) {
	val := r.Context().Value(types.CascataCtxKey)
	if val == nil {
		http.Error(w, `{"error":"Context Lost"}`, 500)
		return
	}
	ctx := val.(*types.CascataRequest)

	var body struct {
		Name          string                 `json:"name"`
		Type          string                 `json:"type"`
		ParentID      string                 `json:"parent_id"`
		Value         string                 `json:"value"`
		Description   string                 `json:"description"`
		Metadata      map[string]interface{} `json:"metadata"`
		ReleasePolicy string                 `json:"release_policy"`
	}
	json.NewDecoder(r.Body).Decode(&body)

	// SYNERGY FIX: Frontend sends 'name', but we used 'key'. Aligned now.
	if body.Name == "" || (body.Type != "folder" && body.Value == "") {
		http.Error(w, `{"error":"Name and Value required"}`, 400)
		return
	}

	parentId := body.ParentID
	if parentId == "root" || parentId == "" {
		parentId = ""
	}

	var query string
	var params []interface{}

	if body.Type == "folder" {
		query = `
			INSERT INTO system.project_secrets (project_slug, parent_id, name, type, description)
			VALUES ($1, NULLIF($2, '')::uuid, $3, 'folder', $4)
			RETURNING id, name, type
		`
		params = []interface{}{ctx.Project.Slug, parentId, body.Name, body.Description}
	} else {
		if body.Metadata == nil {
			body.Metadata = map[string]interface{}{}
		}
		if body.ReleasePolicy != "" {
			body.Metadata["release_policy"] = string(services.NormalizeVaultPolicy(body.ReleasePolicy))
		}
		if _, ok := body.Metadata["release_policy"]; !ok {
			body.Metadata["release_policy"] = string(services.VaultPolicyRuntime)
		}

		// 1. Encrypt via Sovereign Engine
		cipher, err := c.CryptoSvc.Encrypt(ctx.Project.Slug, body.Value)
		if err != nil {
			http.Error(w, `{"error":"`+err.Error()+`"}`, 500)
			return
		}

		query = `
			INSERT INTO system.project_secrets (project_slug, parent_id, name, type, description, secret_value, metadata)
			VALUES ($1, NULLIF($2, '')::uuid, $3, $4, $5, $6, $7)
			RETURNING id, name, type
		`
		meta, _ := json.Marshal(body.Metadata)
		params = []interface{}{ctx.Project.Slug, parentId, body.Name, body.Type, body.Description, cipher, meta}
	}

	var res struct {
		ID   string
		Name string
		Type string
	}
	err := services.SystemPool.QueryRow(r.Context(), query, params...).Scan(&res.ID, &res.Name, &res.Type)

	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, 500)
		return
	}

	// Invalidate the runtime cache for this project
	services.InvalidateVaultRuntimeCache(r.Context(), ctx.Project.Slug)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)
}

func (c *SecretsController) RevealSecret(w http.ResponseWriter, r *http.Request) {
	val := r.Context().Value(types.CascataCtxKey)
	if val == nil {
		http.Error(w, `{"error":"Context Lost"}`, 500)
		return
	}
	ctx := val.(*types.CascataRequest)

	id := chi.URLParam(r, "id")
	if id == "" {
		http.Error(w, "ID required", 400)
		return
	}

	vaultSvc := c.VaultSvc
	if vaultSvc == nil {
		vaultSvc = services.NewVaultService(&c.CryptoSvc)
	}
	plain, rec, err := vaultSvc.Resolve(r.Context(), ctx.Project.Slug, id, services.VaultPurposeUIReveal)
	if err != nil {
		if errors.Is(err, services.ErrVaultSecretNotFound) {
			http.Error(w, `{"error":"Secret not found"}`, 404)
			return
		}
		if errors.Is(err, services.ErrVaultPolicyDenied) {
			http.Error(w, `{"error":"Secret release denied by policy"}`, 403)
			return
		}
		http.Error(w, `{"error":"Decryption failed: `+err.Error()+`"}`, 401)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"value": plain, "meta": rec.Metadata, "release_policy": rec.Policy})
}

func (c *SecretsController) DeleteSecret(w http.ResponseWriter, r *http.Request) {
	val := r.Context().Value(types.CascataCtxKey)
	if val == nil {
		http.Error(w, `{"error":"Context Lost"}`, 500)
		return
	}
	ctx := val.(*types.CascataRequest)
	id := chi.URLParam(r, "id")

	_, err := services.SystemPool.Exec(r.Context(), "DELETE FROM system.project_secrets WHERE id = $1 AND project_slug = $2", id, ctx.Project.Slug)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	// Invalidate the runtime cache for this project
	services.InvalidateVaultRuntimeCache(r.Context(), ctx.Project.Slug)

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"success":true}`))
}

func (c *SecretsController) GetVaultStats(w http.ResponseWriter, r *http.Request) {
	val := r.Context().Value(types.CascataCtxKey)
	if val == nil {
		http.Error(w, `{"error":"Context Lost"}`, 500)
		return
	}
	ctx := val.(*types.CascataRequest)

	var count int
	services.SystemPool.QueryRow(r.Context(), "SELECT count(*) FROM system.project_secrets WHERE project_slug = $1", ctx.Project.Slug).Scan(&count)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"total_secrets":  count,
		"engine":         "cse-v1-sovereign",
		"unsealed_since": fmt.Sprintf("%dm ago", 120),
	})
}
