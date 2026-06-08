package controllers

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"cascata-backend/internal/domain/branching/deployer"
	"cascata-backend/internal/domain/branching/diff"
	"cascata-backend/internal/domain/branching/environment"
	"cascata-backend/internal/services"
	"cascata-backend/internal/types"
)

type BranchController struct {
	BranchSvc    *environment.BranchService
	Deployer     *deployer.Deployer
}

func writeBranchError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}

func (c *BranchController) GetStatus(w http.ResponseWriter, r *http.Request) {
	val := r.Context().Value(types.CascataCtxKey)
	if val == nil {
		writeBranchError(w, http.StatusInternalServerError, "Context Lost")
		return
	}
	ctx := val.(*types.CascataRequest)
	if ctx.Project == nil {
		writeBranchError(w, http.StatusNotFound, "Project Context Required")
		return
	}

	// GAP #7 FIX: system.branches e system.branch_deploys estão no SystemPool, não no ProjectPool!
	var branchingReady bool
	if err := services.SystemPool.QueryRow(r.Context(),
		`SELECT to_regclass('system.branches') IS NOT NULL AND to_regclass('system.branch_deploys') IS NOT NULL`,
	).Scan(&branchingReady); err != nil || !branchingReady {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"slug":              ctx.Project.Slug,
			"status":            ctx.Project.Status,
			"branching_enabled": false,
			"branch_count":      0,
			"deploy_count":      0,
			"active_branches":   []string{},
			"last_sync":         nil,
		})
		return
	}

	var lastSync *time.Time
	var deployCount int
	var branchCount int
	var activeBranches []string
	statusQuery := `
		SELECT MAX(completed_at), COUNT(*)
		FROM system.branch_deploys bd
		JOIN system.branches b ON b.id = bd.branch_id
		WHERE b.project_slug = $1 AND bd.status = 'success'
	`
	_ = services.SystemPool.QueryRow(r.Context(), statusQuery, ctx.Project.Slug).Scan(&lastSync, &deployCount)
	_ = services.SystemPool.QueryRow(r.Context(),
		`SELECT COUNT(*) FROM system.branches WHERE project_slug = $1 AND branch_type = 'environment'`,
		ctx.Project.Slug,
	).Scan(&branchCount)
	rows, err := services.SystemPool.Query(r.Context(),
		`SELECT name FROM system.branches
		 WHERE project_slug = $1 AND branch_type = 'environment' AND status = 'active'
		 ORDER BY CASE WHEN is_main THEN 0 ELSE 1 END, created_at ASC`,
		ctx.Project.Slug,
	)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var branchName string
			if scanErr := rows.Scan(&branchName); scanErr == nil {
				activeBranches = append(activeBranches, branchName)
			}
		}
	}

	res := map[string]interface{}{
		"slug":             ctx.Project.Slug,
		"status":           ctx.Project.Status,
		"branching_enabled": branchCount > 0,
		"last_sync":        lastSync,
		"diverged_schemas": []string{},
		"deploy_count":     deployCount,
		"branch_count":     branchCount,
		"active_branches":  activeBranches,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)
}

func (c *BranchController) GetDiff(w http.ResponseWriter, r *http.Request) {
	val := r.Context().Value(types.CascataCtxKey)
	if val == nil {
		writeBranchError(w, http.StatusInternalServerError, "Context Lost")
		return
	}
	ctx := val.(*types.CascataRequest)

	if c.Deployer == nil {
		writeBranchError(w, http.StatusInternalServerError, "Deployer not initialized")
		return
	}

	sourceBranch := r.URL.Query().Get("source")
	if sourceBranch == "" {
		writeBranchError(w, http.StatusBadRequest, "Source branch required")
		return
	}
	targetBranch := r.URL.Query().Get("target")
	if targetBranch == "" {
		targetBranch = "main"
	}

	opts := deployer.DefaultDeployOptions()
	opts.DryRun = true
	opts.SafetySnapshot = false

	result, err := c.Deployer.DeployMerge(r.Context(), ctx.Project.Slug, sourceBranch, targetBranch, opts)
	if err != nil {
		writeBranchError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to generate diff: %s", err.Error()))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  result.Success,
		"message":  result.Message,
		"error":    result.Error,
		"dry_run":  result.DryRunResult,
		"diff":     result.DiffResult,
		"source":   sourceBranch,
		"target":   targetBranch,
	})
}

// GetConflicts analisa conflitos detalhados entre branches com foco em tenancy, auth e data loss
func (c *BranchController) GetConflicts(w http.ResponseWriter, r *http.Request) {
	val := r.Context().Value(types.CascataCtxKey)
	if val == nil {
		writeBranchError(w, http.StatusInternalServerError, "Context Lost")
		return
	}
	ctx := val.(*types.CascataRequest)

	if ctx.Project == nil {
		writeBranchError(w, http.StatusNotFound, "Project Context Required")
		return
	}

	if c.Deployer == nil {
		writeBranchError(w, http.StatusInternalServerError, "Deployer not initialized")
		return
	}

	sourceBranch := r.URL.Query().Get("source")
	if sourceBranch == "" {
		writeBranchError(w, http.StatusBadRequest, "Source branch required")
		return
	}
	targetBranch := r.URL.Query().Get("target")
	if targetBranch == "" {
		targetBranch = "main"
	}

	// Cria o contexto de diff para o ConflictAnalyzer
	diffCtx := diff.DiffContext{
		PoolProvider: deployer.NewPoolAdapter(),
		ProjectSlug:  ctx.Project.Slug,
		SourceBranch: sourceBranch,
		TargetBranch: targetBranch,
		Mode:         diff.ModeBranchToMain,
	}

	// Executa a análise completa de conflitos
	analyzer := diff.NewConflictAnalyzer(diffCtx)
	analysis, err := analyzer.Analyze(r.Context())
	if err != nil {
		writeBranchError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to analyze conflicts: %s", err.Error()))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(analysis)
}

// ============================================================================
// SISTEMA 1 — Branching (Privacy-First por Design)
// ============================================================================

// ListBranches lista todas as branches de um projeto
func (c *BranchController) ListBranches(w http.ResponseWriter, r *http.Request) {
	val := r.Context().Value(types.CascataCtxKey)
	if val == nil {
		writeBranchError(w, http.StatusInternalServerError, "Context Lost")
		return
	}
	ctx := val.(*types.CascataRequest)
	if ctx.Project == nil {
		writeBranchError(w, http.StatusNotFound, "Project Context Required")
		return
	}

	if c.BranchSvc == nil {
		writeBranchError(w, http.StatusInternalServerError, "Branch service not initialized")
		return
	}

	response, err := c.BranchSvc.ListBranches(r.Context(), ctx.Project.Slug)
	if err != nil {
		writeBranchError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to list branches: %s", err.Error()))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// ListDeploys lista o histórico de deploys (merges) de um projeto
func (c *BranchController) ListDeploys(w http.ResponseWriter, r *http.Request) {
	val := r.Context().Value(types.CascataCtxKey)
	if val == nil {
		writeBranchError(w, http.StatusInternalServerError, "Context Lost")
		return
	}
	ctx := val.(*types.CascataRequest)
	if ctx.Project == nil {
		writeBranchError(w, http.StatusNotFound, "Project Context Required")
		return
	}

	query := `
		SELECT bd.id, bd.branch_id, b.name as branch_name, bd.source_branch, bd.target_branch, 
		       bd.status, bd.started_at, bd.completed_at, bd.duration_ms, 
		       bd.error_message, bd.snapshot_name
		FROM system.branch_deploys bd
		JOIN system.branches b ON b.id = bd.branch_id
		WHERE b.project_slug = $1
		ORDER BY bd.started_at DESC
		LIMIT 50
	`

	rows, err := services.SystemPool.Query(r.Context(), query, ctx.Project.Slug)
	if err != nil {
		writeBranchError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to query deploys: %s", err.Error()))
		return
	}
	defer rows.Close()

	type DeployItem struct {
		ID           string     `json:"id"`
		BranchID     string     `json:"branch_id"`
		BranchName   string     `json:"branch_name"`
		SourceBranch string     `json:"source_branch"`
		TargetBranch string     `json:"target_branch"`
		Status       string     `json:"status"`
		StartedAt    time.Time  `json:"started_at"`
		CompletedAt  *time.Time `json:"completed_at"`
		DurationMs   *int       `json:"duration_ms"`
		ErrorMessage *string    `json:"error_message"`
		SnapshotName *string    `json:"snapshot_name"`
	}

	list := []DeployItem{}
	for rows.Next() {
		var item DeployItem
		err := rows.Scan(
			&item.ID,
			&item.BranchID,
			&item.BranchName,
			&item.SourceBranch,
			&item.TargetBranch,
			&item.Status,
			&item.StartedAt,
			&item.CompletedAt,
			&item.DurationMs,
			&item.ErrorMessage,
			&item.SnapshotName,
		)
		if err == nil {
			list = append(list, item)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(list)
}

// GetBranch busca uma branch específica por nome
func (c *BranchController) GetBranch(w http.ResponseWriter, r *http.Request) {
	val := r.Context().Value(types.CascataCtxKey)
	if val == nil {
		writeBranchError(w, http.StatusInternalServerError, "Context Lost")
		return
	}
	ctx := val.(*types.CascataRequest)
	if ctx.Project == nil {
		writeBranchError(w, http.StatusNotFound, "Project Context Required")
		return
	}

	branchName := r.URL.Query().Get("name")
	if branchName == "" {
		writeBranchError(w, http.StatusBadRequest, "Branch name required")
		return
	}

	if c.BranchSvc == nil {
		writeBranchError(w, http.StatusInternalServerError, "Branch service not initialized")
		return
	}

	branch, err := c.BranchSvc.GetBranch(r.Context(), ctx.Project.Slug, branchName)
	if err != nil {
		if err.Error() == "branch not found" {
			writeBranchError(w, http.StatusNotFound, "Branch not found")
			return
		}
		writeBranchError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to get branch: %s", err.Error()))
		return
	}

	response := environment.GetBranchResponse{
		Branch: *branch,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// CreateBranch cria uma nova branch
func (c *BranchController) CreateBranch(w http.ResponseWriter, r *http.Request) {
	val := r.Context().Value(types.CascataCtxKey)
	if val == nil {
		writeBranchError(w, http.StatusInternalServerError, "Context Lost")
		return
	}
	ctx := val.(*types.CascataRequest)
	if ctx.Project == nil {
		writeBranchError(w, http.StatusNotFound, "Project Context Required")
		return
	}

	if c.BranchSvc == nil {
		writeBranchError(w, http.StatusInternalServerError, "Branch service not initialized")
		return
	}

	var req environment.CreateBranchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeBranchError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	// Extrai user ID do contexto
	var createdBy *string
	if userID, ok := types.GetUserID(r.Context()); ok {
		createdBy = &userID
	}

	branch, err := c.BranchSvc.CreateBranch(r.Context(), ctx.Project.Slug, req, createdBy)
	if err != nil {
		writeBranchError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to create branch: %s", err.Error()))
		return
	}

	response := environment.CreateBranchResponse{
		Branch: *branch,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(response)
}

// UpdateBranch atualiza uma branch existente
func (c *BranchController) UpdateBranch(w http.ResponseWriter, r *http.Request) {
	val := r.Context().Value(types.CascataCtxKey)
	if val == nil {
		writeBranchError(w, http.StatusInternalServerError, "Context Lost")
		return
	}
	ctx := val.(*types.CascataRequest)
	if ctx.Project == nil {
		writeBranchError(w, http.StatusNotFound, "Project Context Required")
		return
	}

	branchName := r.URL.Query().Get("name")
	if branchName == "" {
		writeBranchError(w, http.StatusBadRequest, "Branch name required")
		return
	}

	if c.BranchSvc == nil {
		writeBranchError(w, http.StatusInternalServerError, "Branch service not initialized")
		return
	}

	var req environment.UpdateBranchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeBranchError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	branch, err := c.BranchSvc.UpdateBranch(r.Context(), ctx.Project.Slug, branchName, req)
	if err != nil {
		if err.Error() == "branch not found" {
			writeBranchError(w, http.StatusNotFound, "Branch not found")
			return
		}
		writeBranchError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to update branch: %s", err.Error()))
		return
	}

	response := environment.UpdateBranchResponse{
		Branch: *branch,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// DeleteBranch deleta uma branch
func (c *BranchController) DeleteBranch(w http.ResponseWriter, r *http.Request) {
	val := r.Context().Value(types.CascataCtxKey)
	if val == nil {
		writeBranchError(w, http.StatusInternalServerError, "Context Lost")
		return
	}
	ctx := val.(*types.CascataRequest)
	if ctx.Project == nil {
		writeBranchError(w, http.StatusNotFound, "Project Context Required")
		return
	}

	branchName := r.URL.Query().Get("name")
	if branchName == "" {
		writeBranchError(w, http.StatusBadRequest, "Branch name required")
		return
	}

	if c.BranchSvc == nil {
		writeBranchError(w, http.StatusInternalServerError, "Branch service not initialized")
		return
	}

	err := c.BranchSvc.DeleteBranch(r.Context(), ctx.Project.Slug, branchName)
	if err != nil {
		if err.Error() == "branch not found" {
			writeBranchError(w, http.StatusNotFound, "Branch not found")
			return
		}
		writeBranchError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to delete branch: %s", err.Error()))
		return
	}

	response := environment.DeleteBranchResponse{
		Success: true,
		Message: fmt.Sprintf("Branch '%s' deleted successfully", branchName),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// DeployBranch faz deploy de uma branch (merge)
func (c *BranchController) DeployBranch(w http.ResponseWriter, r *http.Request) {
	val := r.Context().Value(types.CascataCtxKey)
	if val == nil {
		writeBranchError(w, http.StatusInternalServerError, "Context Lost")
		return
	}
	ctx := val.(*types.CascataRequest)
	if ctx.Project == nil {
		writeBranchError(w, http.StatusNotFound, "Project Context Required")
		return
	}

	if c.Deployer == nil {
		writeBranchError(w, http.StatusInternalServerError, "Deployer not initialized")
		return
	}

	var req environment.DeployBranchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeBranchError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	// Configura opções de deploy
	opts := deployer.DefaultDeployOptions()
	opts.DryRun = req.DryRun
	opts.SafetySnapshot = req.SafetySnapshot

	// Executa o deploy merge
	result, err := c.Deployer.DeployMerge(
		r.Context(),
		ctx.Project.Slug,
		req.SourceBranch,
		req.TargetBranch,
		opts,
	)
	if err != nil {
		writeBranchError(w, http.StatusInternalServerError, fmt.Sprintf("Deploy failed: %s", err.Error()))
		return
	}

	// Deploy/Merge nexus automations (orchestrations) if this is not a dry-run
	if !opts.DryRun {
		// 1. Prune automations in target branch that were deleted in source branch
		deleteQuery := `
			DELETE FROM system.nexus_automations
			WHERE tenant_id = $1 AND branch_name = $2
			  AND id NOT IN (
				  SELECT id FROM system.nexus_automations 
				  WHERE tenant_id = $1 AND branch_name = $3
			  )
		`
		if _, err := services.SystemPool.Exec(r.Context(), deleteQuery, ctx.Project.Slug, req.TargetBranch, req.SourceBranch); err != nil {
			log.Printf("[BranchController] Warning: failed to prune deleted nexus automations during deploy: %v", err)
		}

		// 2. Copy/Update automations from source branch to target branch
		mergeAutomationsQuery := `
			INSERT INTO system.nexus_automations (
				id, tenant_id, branch_name, name, description, hook_type, 
				table_name, event_type, graph_json, is_active, status, 
				execution_mode, route_pattern, method
			)
			SELECT 
				id, tenant_id, $1, name, description, hook_type, 
				table_name, event_type, graph_json, is_active, status, 
				execution_mode, route_pattern, method
			FROM system.nexus_automations
			WHERE tenant_id = $2 AND branch_name = $3
			ON CONFLICT (id, tenant_id, branch_name) 
			DO UPDATE SET 
				name = EXCLUDED.name,
				description = EXCLUDED.description,
				hook_type = EXCLUDED.hook_type,
				table_name = EXCLUDED.table_name,
				event_type = EXCLUDED.event_type,
				graph_json = EXCLUDED.graph_json,
				is_active = EXCLUDED.is_active,
				status = EXCLUDED.status,
				execution_mode = EXCLUDED.execution_mode,
				route_pattern = EXCLUDED.route_pattern,
				method = EXCLUDED.method
		`
		if _, err := services.SystemPool.Exec(r.Context(), mergeAutomationsQuery, req.TargetBranch, ctx.Project.Slug, req.SourceBranch); err != nil {
			log.Printf("[BranchController] Warning: failed to merge nexus automations from %s to %s for project %s: %v", req.SourceBranch, req.TargetBranch, ctx.Project.Slug, err)
		} else {
			log.Printf("[BranchController] Successfully merged/deployed nexus automations from %s to %s for project %s", req.SourceBranch, req.TargetBranch, ctx.Project.Slug)
		}

		// 3. Prune storage objects in target branch that no longer exist in source branch
		deleteStorageQuery := `
			DELETE FROM system.storage_objects
			WHERE project_slug = $1 AND branch_name = $2
			  AND bucket NOT IN (
				  SELECT DISTINCT bucket FROM system.storage_objects 
				  WHERE project_slug = $1 AND branch_name = $3
			  )
		`
		if _, err := services.SystemPool.Exec(r.Context(), deleteStorageQuery, ctx.Project.Slug, req.TargetBranch, req.SourceBranch); err != nil {
			log.Printf("[BranchController] Warning: failed to prune deleted storage buckets during deploy: %v", err)
		}

		// 4. Copy/Update storage objects metadata from source branch to target branch
		mergeStorageQuery := `
			INSERT INTO system.storage_objects (
				project_slug, branch_name, bucket, name, parent_path, 
				full_path, size, mime_type, is_folder, provider, 
				updated_at, rls_enabled
			)
			SELECT 
				project_slug, $1, bucket, name, parent_path, 
				full_path, size, mime_type, is_folder, provider, 
				NOW(), rls_enabled
			FROM system.storage_objects
			WHERE project_slug = $2 AND branch_name = $3
			ON CONFLICT (project_slug, branch_name, bucket, full_path) 
			DO UPDATE SET 
				name = EXCLUDED.name,
				parent_path = EXCLUDED.parent_path,
				size = EXCLUDED.size,
				mime_type = EXCLUDED.mime_type,
				is_folder = EXCLUDED.is_folder,
				provider = EXCLUDED.provider,
				updated_at = NOW(),
				rls_enabled = EXCLUDED.rls_enabled
		`
		if _, err := services.SystemPool.Exec(r.Context(), mergeStorageQuery, req.TargetBranch, ctx.Project.Slug, req.SourceBranch); err != nil {
			log.Printf("[BranchController] Warning: failed to merge storage objects metadata from %s to %s for project %s: %v", req.SourceBranch, req.TargetBranch, ctx.Project.Slug, err)
		} else {
			log.Printf("[BranchController] Successfully merged storage objects metadata from %s to %s for project %s", req.SourceBranch, req.TargetBranch, ctx.Project.Slug)
		}

		// 5. Replicate physical directories for storage buckets on the filesystem
		storageRoot := os.Getenv("STORAGE_ROOT")
		if storageRoot == "" {
			storageRoot = "./storage"
		}
		var srcPath string
		if req.SourceBranch == "main" {
			srcPath = filepath.Join(storageRoot, ctx.Project.Slug)
		} else {
			srcPath = filepath.Join(storageRoot, ctx.Project.Slug, "branches", req.SourceBranch)
		}
		var dstPath string
		if req.TargetBranch == "main" {
			dstPath = filepath.Join(storageRoot, ctx.Project.Slug)
		} else {
			dstPath = filepath.Join(storageRoot, ctx.Project.Slug, "branches", req.TargetBranch)
		}

		// Recreate directories found in the source branch's filesystem
		if entries, err := os.ReadDir(srcPath); err == nil {
			for _, entry := range entries {
				if entry.IsDir() && entry.Name() != "branches" && entry.Name() != ".temp" {
					bucketDir := filepath.Join(dstPath, entry.Name())
					if err := os.MkdirAll(bucketDir, 0750); err == nil {
						log.Printf("[BranchController] Replicated physical directory for bucket %s on deploy to branch %s", entry.Name(), req.TargetBranch)
					}
				}
			}
		}

		// Double-safety: check database rows for unique buckets to ensure they are created
		if dbRows, err := services.SystemPool.Query(r.Context(), `
			SELECT DISTINCT bucket FROM system.storage_objects 
			WHERE project_slug = $1 AND branch_name = $2
		`, ctx.Project.Slug, req.SourceBranch); err == nil {
			defer dbRows.Close()
			for dbRows.Next() {
				var b string
				if scanErr := dbRows.Scan(&b); scanErr == nil && b != "" {
					bucketDir := filepath.Join(dstPath, b)
					if err := os.MkdirAll(bucketDir, 0750); err != nil {
						log.Printf("[BranchController] Warning: failed to create physical directory for bucket %s: %v", b, err)
					}
				}
			}
		}

		// 6. Merge auth config/strategies from source branch to target branch
		var sourceAuthConfigJSON *string
		// Retrieve auth_config_json from the source branch
		if req.SourceBranch == "main" {
			// If source is main, we construct auth_config_json from project.metadata.extra
			var metadataRaw json.RawMessage
			if err := services.SystemPool.QueryRow(r.Context(),
				"SELECT metadata FROM system.projects WHERE slug = $1",
				ctx.Project.Slug).Scan(&metadataRaw); err == nil && len(metadataRaw) > 0 {
				var metadata map[string]interface{}
				if json.Unmarshal(metadataRaw, &metadata) == nil {
					if extra, ok := metadata["extra"].(map[string]interface{}); ok {
						branchAuth := map[string]interface{}{
							"auth_config":     extra["auth_config"],
							"auth_strategies": extra["auth_strategies"],
							"linked_tables":   extra["linked_tables"],
						}
						if authBytes, err := json.Marshal(branchAuth); err == nil {
							authStr := string(authBytes)
							sourceAuthConfigJSON = &authStr
						}
					}
				}
			}
		} else {
			// Source is a sandbox branch
			if err := services.SystemPool.QueryRow(r.Context(),
				"SELECT auth_config_json FROM system.branches WHERE project_slug = $1 AND name = $2",
				ctx.Project.Slug, req.SourceBranch).Scan(&sourceAuthConfigJSON); err != nil {
				log.Printf("[BranchController] Warning: failed to retrieve source auth config: %v", err)
			}
		}

		if sourceAuthConfigJSON != nil && *sourceAuthConfigJSON != "" {
			if req.TargetBranch == "main" {
				// Target is main: merge back into system.projects.metadata
				var currentMetadataRaw json.RawMessage
				err := services.SystemPool.QueryRow(r.Context(),
					"SELECT metadata FROM system.projects WHERE slug = $1",
					ctx.Project.Slug).Scan(&currentMetadataRaw)

				currentMetadata := make(map[string]interface{})
				if err == nil && len(currentMetadataRaw) > 0 {
					json.Unmarshal(currentMetadataRaw, &currentMetadata)
				}

				extra, ok := currentMetadata["extra"].(map[string]interface{})
				if !ok {
					extra = make(map[string]interface{})
					currentMetadata["extra"] = extra
				}

				var branchAuth map[string]interface{}
				if json.Unmarshal([]byte(*sourceAuthConfigJSON), &branchAuth) == nil {
					if ac, ok := branchAuth["auth_config"].(map[string]interface{}); ok {
						currentAuthConfig, _ := extra["auth_config"].(map[string]interface{})
						extra["auth_config"] = deepMerge(currentAuthConfig, ac)
					}
					if as, ok := branchAuth["auth_strategies"]; ok {
						extra["auth_strategies"] = as
					}
					if lt, ok := branchAuth["linked_tables"]; ok {
						extra["linked_tables"] = lt
					}

					_, err = services.SystemPool.Exec(r.Context(),
						"UPDATE system.projects SET metadata = $1 WHERE slug = $2",
						currentMetadata, ctx.Project.Slug)
					if err != nil {
						log.Printf("[BranchController] Warning: failed to merge auth config to main project metadata: %v", err)
					} else {
						log.Printf("[BranchController] Successfully merged branch auth config to main project metadata")
					}
				}
			} else {
				// Target is a sandbox branch: update system.branches.auth_config_json
				_, err := services.SystemPool.Exec(r.Context(),
					"UPDATE system.branches SET auth_config_json = $1 WHERE project_slug = $2 AND name = $3",
					sourceAuthConfigJSON, ctx.Project.Slug, req.TargetBranch)
				if err != nil {
					log.Printf("[BranchController] Warning: failed to merge auth config to target branch %s: %v", req.TargetBranch, err)
				} else {
					log.Printf("[BranchController] Successfully merged auth config to target branch %s", req.TargetBranch)
				}
			}
		}
	}

	// Serializa diff result se houver
	var diffResultJSON *string
	if result.DiffResult != nil {
		jsonBytes, err := json.Marshal(result.DiffResult)
		if err == nil {
			jsonStr := string(jsonBytes)
			diffResultJSON = &jsonStr
		}
	}

	response := environment.DeployBranchResponse{
		DeployID:   fmt.Sprintf("deploy_%d", time.Now().UnixNano()),
		Status:     environment.DeployStatusSuccess,
		Message:    result.Message,
		DiffResult: diffResultJSON,
	}

	if c.BranchSvc != nil {
		if branch, berr := c.BranchSvc.GetBranch(r.Context(), ctx.Project.Slug, req.SourceBranch); berr == nil {
			var triggeredBy *string
			if userID, ok := types.GetUserID(r.Context()); ok {
				triggeredBy = &userID
			}
			var sqlStatements []string
			if result.DiffResult != nil {
				sqlStatements = result.DiffResult.SQL
			}
			// GAP #8 FIX: system.branch_deploys está no SystemPool, não no ProjectPool!
			insertErr := services.SystemPool.QueryRow(
				r.Context(),
				`INSERT INTO system.branch_deploys
				(branch_id, source_branch, target_branch, status, diff_result, sql_statements, snapshot_name, completed_at, triggered_by)
				VALUES ($1, $2, $3, $4, $5::jsonb, $6, $7, NOW(), $8::uuid)
				RETURNING id`,
				branch.ID, req.SourceBranch, req.TargetBranch, "success", diffResultJSON, sqlStatements, result.SnapshotName, triggeredBy,
			).Scan(&response.DeployID)
			
			if insertErr != nil {
				// Log error but don't fail the deploy - historical record is secondary
				fmt.Printf("[BranchController] Failed to record deploy history: %v\n", insertErr)
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// EnsureMainBranch garante que a branch main existe
func (c *BranchController) EnsureMainBranch(w http.ResponseWriter, r *http.Request) {
	val := r.Context().Value(types.CascataCtxKey)
	if val == nil {
		writeBranchError(w, http.StatusInternalServerError, "Context Lost")
		return
	}
	ctx := val.(*types.CascataRequest)
	if ctx.Project == nil {
		writeBranchError(w, http.StatusNotFound, "Project Context Required")
		return
	}

	if c.BranchSvc == nil {
		writeBranchError(w, http.StatusInternalServerError, "Branch service not initialized")
		return
	}

	branch, err := c.BranchSvc.EnsureMainBranch(r.Context(), ctx.Project.Slug)
	if err != nil {
		writeBranchError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to ensure main branch: %s", err.Error()))
		return
	}

	response := environment.GetBranchResponse{
		Branch: *branch,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// AccessBranch materializa a branch on-demand e retorna informações de conexão.
// O frontend deve usar o env_identifier retornado no header X-Cascata-Env
// para todas as requests subsequentes enquanto estiver na branch.
func (c *BranchController) AccessBranch(w http.ResponseWriter, r *http.Request) {
	val := r.Context().Value(types.CascataCtxKey)
	if val == nil {
		writeBranchError(w, http.StatusInternalServerError, "Context Lost")
		return
	}
	ctx := val.(*types.CascataRequest)
	if ctx.Project == nil {
		writeBranchError(w, http.StatusNotFound, "Project Context Required")
		return
	}

	if c.BranchSvc == nil {
		writeBranchError(w, http.StatusInternalServerError, "Branch service not initialized")
		return
	}

	var req environment.AccessBranchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeBranchError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if req.BranchName == "" {
		writeBranchError(w, http.StatusBadRequest, "Branch name required")
		return
	}

	response, err := c.BranchSvc.AccessBranch(r.Context(), ctx.Project.Slug, req.BranchName)
	if err != nil {
		if err.Error() == "branch not found" {
			writeBranchError(w, http.StatusNotFound, "Branch not found")
			return
		}
		writeBranchError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to access branch: %s", err.Error()))
		return
	}

	// Set cookie for persistent branch context across requests (Dashboard-only)
	// Cookie is HttpOnly and Secure in production, valid for 24 hours
	http.SetCookie(w, &http.Cookie{
		Name:     "cascata_active_env",
		Value:    response.EnvIdentifier,
		Path:     "/",
		MaxAge:   86400, // 24 hours
		HttpOnly: false, // Allow frontend JS to read for debugging
		Secure:   true,  // Only send over HTTPS
		SameSite: http.SameSiteLaxMode,
	})
	log.Printf("[AccessBranch] Cookie set for branch: %s", response.EnvIdentifier)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// RestoreDeploy restaura o banco de dados principal do tenant a partir de um checkpoint físico (snapshot)
func (c *BranchController) RestoreDeploy(w http.ResponseWriter, r *http.Request) {
	val := r.Context().Value(types.CascataCtxKey)
	if val == nil {
		writeBranchError(w, http.StatusInternalServerError, "Context Lost")
		return
	}
	ctx := val.(*types.CascataRequest)
	if ctx.Project == nil {
		writeBranchError(w, http.StatusNotFound, "Project Context Required")
		return
	}

	if c.Deployer == nil {
		writeBranchError(w, http.StatusInternalServerError, "Deployer not initialized")
		return
	}

	deployID := r.URL.Query().Get("id")
	if deployID == "" {
		writeBranchError(w, http.StatusBadRequest, "Deploy ID required")
		return
	}

	// 1. Busca o snapshot_name do deploy na tabela system.branch_deploys
	var snapshotDBName string
	var targetBranch string
	err := services.SystemPool.QueryRow(r.Context(),
		`SELECT snapshot_name, target_branch FROM system.branch_deploys WHERE id = $1`,
		deployID,
	).Scan(&snapshotDBName, &targetBranch)
	if err != nil {
		writeBranchError(w, http.StatusNotFound, fmt.Sprintf("Deploy record not found: %s", err.Error()))
		return
	}

	if snapshotDBName == "" {
		writeBranchError(w, http.StatusBadRequest, "This checkpoint does not have a physical safety snapshot available for restore")
		return
	}

	// 1.5 Cria um snapshot do estado ATUAL do banco de dados (antes do rollback)
	// para que o usuário possa reverter/desfazer se quiser!
	undoSnapshotName := fmt.Sprintf("%s_%s_undo_restore_%d", ctx.Project.Slug, targetBranch, time.Now().Unix())
	undoSnapshot, err := c.Deployer.CreateSnapshot(r.Context(), ctx.Project.Slug, targetBranch, undoSnapshotName)
	if err != nil {
		fmt.Printf("[RestoreDeploy] Warning: failed to create undo checkpoint: %v\n", err)
	} else {
		// Insere o snapshot de UNDO no histórico de deploys
		var branchID string
		_ = services.SystemPool.QueryRow(r.Context(),
			`SELECT id FROM system.branches WHERE project_slug = $1 AND name = $2`,
			ctx.Project.Slug, targetBranch,
		).Scan(&branchID)

		if branchID != "" {
			var triggeredBy *string
			if userID, ok := types.GetUserID(r.Context()); ok {
				triggeredBy = &userID
			}
			
			_, _ = services.SystemPool.Exec(r.Context(),
				`INSERT INTO system.branch_deploys
				(branch_id, source_branch, target_branch, status, diff_result, sql_statements, snapshot_name, completed_at, triggered_by)
				VALUES ($1, $2, $3, $4, $5::jsonb, $6, $7, NOW(), $8::uuid)`,
				branchID, "restore-undo", targetBranch, "success", 
				`{"message": "Auto-checkpoint created before restoring database state"}`, 
				[]string{}, undoSnapshot.DBName, triggeredBy,
			)
		}
	}

	// 2. Chama rollbackToSnapshot do Deployer
	snapshotInfo := &deployer.SnapshotInfo{
		Name:    snapshotDBName,
		Project: ctx.Project.Slug,
		Env:     targetBranch,
		DBName:  snapshotDBName,
	}

	err = c.Deployer.RollbackToSnapshot(r.Context(), snapshotInfo)
	if err != nil {
		writeBranchError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to restore checkpoint: %s", err.Error()))
		return
	}

	// 3. Atualiza o status do histórico para rolled_back
	_, _ = services.SystemPool.Exec(r.Context(),
		`UPDATE system.branch_deploys SET status = 'rolled_back' WHERE id = $1`,
		deployID,
	)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": fmt.Sprintf("Successfully restored database state to physical checkpoint '%s'. Current state was preserved as an undo checkpoint.", snapshotDBName),
	})
}					