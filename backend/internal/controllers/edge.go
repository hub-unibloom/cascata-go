package controllers

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"cascata-backend/internal/services"
	"cascata-backend/internal/types"
	"time"
)

type EdgeController struct {
	cryptoSvc services.CryptoService
	edgeSvc   services.EdgeService
	vaultSvc  *services.VaultService
}

func NewEdgeController(cryptoSvc services.CryptoService, edgeSvc services.EdgeService, vaultSvc *services.VaultService) *EdgeController {
	return &EdgeController{
		cryptoSvc: cryptoSvc,
		edgeSvc:   edgeSvc,
		vaultSvc:  vaultSvc,
	}
}

func (c *EdgeController) ListFunctions(w http.ResponseWriter, r *http.Request) {
	val := r.Context().Value(types.CascataCtxKey)
	if val == nil {
		http.Error(w, `{"error":"Context Lost"}`, 500)
		return
	}
	ctx := val.(*types.CascataRequest)
	if ctx.Project == nil {
		http.Error(w, `{"error":"Project Context Required"}`, 404)
		return
	}

	// Fetch from system pool for security isolation
	rows, err := services.SystemPool.Query(r.Context(),
		"SELECT id, name, runtime, status, created_at, timeout_ms, env_vars FROM system.edge_functions WHERE project_slug = $1",
		ctx.Project.Slug)

	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, 500)
		return
	}
	defer rows.Close()

	funcs := []map[string]interface{}{}
	for rows.Next() {
		var id, name, runtime, status string
		var created time.Time
		var timeout int
		var envVars map[string]string
		
		err := rows.Scan(&id, &name, &runtime, &status, &created, &timeout, &envVars)
		if err != nil {
			log.Printf("Error scanning edge function list row: %v", err)
			continue
		}
		
		if envVars == nil {
			envVars = make(map[string]string)
		}
		funcs = append(funcs, map[string]interface{}{
			"id": id, "name": name, "runtime": runtime, "status": status, "created_at": created.Format(time.RFC3339),
			"timeout_ms": timeout, "env_vars": envVars,
		})
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(funcs)
}

func (c *EdgeController) InvokeFunction(w http.ResponseWriter, r *http.Request) {
	val := r.Context().Value(types.CascataCtxKey)
	if val == nil {
		http.Error(w, `{"error":"Context Lost"}`, 500)
		return
	}
	ctx := val.(*types.CascataRequest)

	// Try to get function name from URL path first (data plane: /edge/{name})
	// Fall back to query parameter (control plane: ?name=)
	functionName := chi.URLParam(r, "name")
	if functionName == "" {
		functionName = r.URL.Query().Get("name")
	}
	if functionName == "" {
		http.Error(w, `{"error":"Function name required"}`, 400)
		return
	}

	var body map[string]interface{}
	json.NewDecoder(r.Body).Decode(&body)

	// Fetch code and configuration from System Pool
	var code string
	var timeout int = 5000 // default
	var envVars map[string]string
	var status string
	err := services.SystemPool.QueryRow(r.Context(),
		"SELECT content, timeout_ms, env_vars, status FROM system.edge_functions WHERE project_slug = $1 AND name = $2",
		ctx.Project.Slug, functionName).Scan(&code, &timeout, &envVars, &status)
	if err != nil {
		http.Error(w, `{"error":"Function not found"}`, 404)
		return
	}

	// Block invocation if function is inactive
	if status == "inactive" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(403)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":  "Function is inactive",
			"status": "inactive",
			"hint":   "Activate this function before invoking it",
		})
		return
	}
	if envVars == nil {
		envVars = make(map[string]string)
	}

	// Vault Integration: Merge project-level secrets into env
	if c.vaultSvc != nil {
		vaultSecrets, err := c.vaultSvc.ResolveAllRuntimeSecrets(r.Context(), ctx.Project.Slug)
		if err != nil {
			// Log warning but don't fail - vault secrets are supplementary
			log.Printf("[EdgeFunction] Warning: Vault secrets unavailable for %s: %v", ctx.Project.Slug, err)
		}
		if len(vaultSecrets) > 0 {
			// Merge: vault (base) + function env_vars (override = precedência local)
			mergedEnv := make(map[string]string, len(vaultSecrets)+len(envVars))
			for k, v := range vaultSecrets {
				mergedEnv[k] = v
			}
			for k, v := range envVars {
				mergedEnv[k] = v // Local override wins
			}
			envVars = mergedEnv
		}
	}

	// Decrypt if necessary
	plainCode, _ := c.cryptoSvc.Decrypt(code)

	// 1. Prepare JS Context Hierarchy
	reqCtx := map[string]interface{}{
		"query": r.URL.Query(),
		"body":  body,
		"user":  ctx.User,
		"role":  string(ctx.UserRole),
	}

	// Invoke via EdgeService (Method: Execute)
	res, err := c.edgeSvc.Execute(r.Context(), plainCode, reqCtx, envVars, ctx.ProjectPool, timeout, ctx.Project.Slug)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"Execution failed: %s"}`, err.Error()), 502)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)
}

func (c *EdgeController) DeployFunction(w http.ResponseWriter, r *http.Request) {
	val := r.Context().Value(types.CascataCtxKey)
	if val == nil {
		http.Error(w, `{"error":"Context Lost"}`, 500)
		return
	}
	ctx := val.(*types.CascataRequest)

	var payload struct {
		Name      string            `json:"name"`
		Content   string            `json:"content"`
		EnvVars   map[string]string `json:"env_vars"`
		Imports   []string          `json:"imports"`
		TimeoutMs int               `json:"timeout_ms"`
		Timeout   int               `json:"timeout"`
		Status    string            `json:"status"`
	}
	json.NewDecoder(r.Body).Decode(&payload)

	// Encrypt code content for persistent safety
	cipher, _ := c.cryptoSvc.Encrypt(ctx.Project.Slug, payload.Content)

	// Set defaults for optional fields
	envVars := payload.EnvVars
	if envVars == nil {
		envVars = make(map[string]string)
	}
	imports := payload.Imports
	if imports == nil {
		imports = []string{}
	}
	// Accept timeout_ms (preferred) or timeout (legacy fallback)
	timeout := payload.TimeoutMs
	if timeout == 0 {
		timeout = payload.Timeout
	}
	if timeout == 0 {
		timeout = 5000 // 5 seconds default
	}
	status := payload.Status
	if status == "" {
		status = "active"
	}

	// Archive existing version to history before overwriting (if function already exists)
	var existingID, existingContent string
	var existingVersion int
	archiveErr := services.SystemPool.QueryRow(r.Context(),
		"SELECT id, content, version FROM system.edge_functions WHERE project_slug = $1 AND name = $2",
		ctx.Project.Slug, payload.Name).Scan(&existingID, &existingContent, &existingVersion)

	if archiveErr == nil {
		// Function exists — archive current version before overwriting
		userUUID := ""
		if ctx.User != nil {
			if sub, ok := ctx.User["sub"].(string); ok {
				userUUID = sub
			}
		}
		services.SystemPool.Exec(r.Context(),
			"INSERT INTO system.edge_functions_history (function_id, project_slug, name, content, version, created_by, change_reason) VALUES ($1, $2, $3, $4, $5, $6, $7)",
			existingID, ctx.Project.Slug, payload.Name, existingContent, existingVersion, userUUID, "Deploy Update")
	}

	_, err := services.SystemPool.Exec(r.Context(),
		`INSERT INTO system.edge_functions (project_slug, name, content, runtime, status, env_vars, imports, timeout_ms)
		 VALUES ($1, $2, $3, 'javascript', $7, $4, $5, $6)
		 ON CONFLICT (project_slug, name) DO UPDATE SET
		   content = EXCLUDED.content,
		   env_vars = EXCLUDED.env_vars,
		   imports = EXCLUDED.imports,
		   timeout_ms = EXCLUDED.timeout_ms,
		   status = EXCLUDED.status,
		   version = system.edge_functions.version + 1,
		   updated_at = NOW()`,
		ctx.Project.Slug, payload.Name, cipher, envVars, imports, timeout, status)

	if err != nil {
		http.Error(w, `{"error":"Deployment failed: `+err.Error()+`"}`, 500)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":    true,
		"deployed":   true,
		"function":   payload.Name,
		"timeout_ms": timeout,
		"status":     status,
	})
}

func (c *EdgeController) GetStats(w http.ResponseWriter, r *http.Request) {
	val := r.Context().Value(types.CascataCtxKey)
	if val == nil {
		http.Error(w, `{"error":"Context Lost"}`, 500)
		return
	}
	ctx := val.(*types.CascataRequest)

	// Get real stats from edge_functions table
	var totalInvocations, totalErrors, activeFunctions int

	// Get project-wide stats
	err := services.SystemPool.QueryRow(r.Context(), 
		"SELECT COALESCE(SUM(invocation_count), 0), COALESCE(SUM(error_count), 0), COUNT(*) FROM system.edge_functions WHERE project_slug = $1", 
		ctx.Project.Slug).Scan(&totalInvocations, &totalErrors, &activeFunctions)
	
	if err != nil {
		totalInvocations = 0
		totalErrors = 0
		activeFunctions = 0
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"total_invocations": totalInvocations,
		"total_errors":      totalErrors,
		"active_functions":  activeFunctions,
		"runtime":           "v8-goja-0.10.x",
	})
}

func (c *EdgeController) DeleteFunction(w http.ResponseWriter, r *http.Request) {
	val := r.Context().Value(types.CascataCtxKey)
	if val == nil {
		http.Error(w, `{"error":"Context Lost"}`, 500)
		return
	}
	ctx := val.(*types.CascataRequest)

	functionName := chi.URLParam(r, "name")
	if functionName == "" {
		http.Error(w, `{"error":"Function name required"}`, 400)
		return
	}

	// Archive to history before deleting
	var functionID, content string
	var version int
	err := services.SystemPool.QueryRow(r.Context(), 
		"SELECT id, content, version FROM system.edge_functions WHERE project_slug = $1 AND name = $2", 
		ctx.Project.Slug, functionName).Scan(&functionID, &content, &version)
	
	if err != nil {
		http.Error(w, `{"error":"Function not found"}`, 404)
		return
	}

	// Insert into history
	userUUID := ""
	if ctx.User != nil {
		if sub, ok := ctx.User["sub"].(string); ok { userUUID = sub }
	}
	_, err = services.SystemPool.Exec(r.Context(),
		"INSERT INTO system.edge_functions_history (function_id, project_slug, name, content, version, created_by, change_reason) VALUES ($1, $2, $3, $4, $5, $6, $7)",
		functionID, ctx.Project.Slug, functionName, content, version, userUUID, "Deleted")
	
	if err != nil {
		// Log error but continue with deletion
		fmt.Printf("Warning: Failed to archive function history: %v", err)
	}

	// Delete the function
	result, err := services.SystemPool.Exec(r.Context(), 
		"DELETE FROM system.edge_functions WHERE project_slug = $1 AND name = $2", 
		ctx.Project.Slug, functionName)
	
	if err != nil {
		http.Error(w, `{"error":"Deletion failed: `+err.Error()+`"}`, 500)
		return
	}

	rowsAffected := result.RowsAffected()
	if rowsAffected == 0 {
		http.Error(w, `{"error":"Function not found"}`, 404)
		return
	}

	w.Write([]byte(`{"success":true,"deleted":true,"function":"` + functionName + `"}`))
}

func (c *EdgeController) UpdateFunction(w http.ResponseWriter, r *http.Request) {
	val := r.Context().Value(types.CascataCtxKey)
	if val == nil {
		http.Error(w, `{"error":"Context Lost"}`, 500)
		return
	}
	ctx := val.(*types.CascataRequest)

	var payload struct {
		Name          string            `json:"name"`
		Content       string            `json:"content"`
		TimeoutMs     *int              `json:"timeout_ms"`
		MemoryLimitMb *int              `json:"memory_limit_mb"`
		EnvVars       map[string]string `json:"env_vars"`
		Status        *string           `json:"status"`
	}
	json.NewDecoder(r.Body).Decode(&payload)

	functionName := chi.URLParam(r, "name")
	if functionName == "" {
		functionName = payload.Name
	}
	if functionName == "" {
		http.Error(w, `{"error":"Function name required"}`, 400)
		return
	}

	// Get current version
	var currentVersion int
	var functionID string
	err := services.SystemPool.QueryRow(r.Context(), 
		"SELECT id, version FROM system.edge_functions WHERE project_slug = $1 AND name = $2", 
		ctx.Project.Slug, functionName).Scan(&functionID, &currentVersion)
	
	if err != nil {
		http.Error(w, `{"error":"Function not found"}`, 404)
		return
	}

	// Archive current version to history
	var currentContent string
	userUUID := ""
	if ctx.User != nil {
		if sub, ok := ctx.User["sub"].(string); ok { userUUID = sub }
	}
	err = services.SystemPool.QueryRow(r.Context(),
		"SELECT content FROM system.edge_functions WHERE project_slug = $1 AND name = $2",
		ctx.Project.Slug, functionName).Scan(&currentContent)

	if err == nil {
		services.SystemPool.Exec(r.Context(), 
			"INSERT INTO system.edge_functions_history (function_id, project_slug, name, content, version, created_by, change_reason) VALUES ($1, $2, $3, $4, $5, $6, $7)",
			functionID, ctx.Project.Slug, functionName, currentContent, currentVersion, userUUID, "Updated")
	}

	// Build dynamic update query
	updateFields := []string{"version = version + 1", "updated_at = NOW()"}
	args := []interface{}{ctx.Project.Slug, functionName}
	argIndex := 3

	if payload.Content != "" {
		cipher, _ := c.cryptoSvc.Encrypt(ctx.Project.Slug, payload.Content)
		updateFields = append(updateFields, fmt.Sprintf("content = $%d", argIndex))
		args = append(args, cipher)
		argIndex++
	}

	if payload.TimeoutMs != nil {
		updateFields = append(updateFields, fmt.Sprintf("timeout_ms = $%d", argIndex))
		args = append(args, *payload.TimeoutMs)
		argIndex++
	}

	if payload.MemoryLimitMb != nil {
		updateFields = append(updateFields, fmt.Sprintf("memory_limit_mb = $%d", argIndex))
		args = append(args, *payload.MemoryLimitMb)
		argIndex++
	}

	if payload.EnvVars != nil {
		updateFields = append(updateFields, fmt.Sprintf("env_vars = $%d", argIndex))
		args = append(args, payload.EnvVars)
		argIndex++
	}

	if payload.Status != nil {
		updateFields = append(updateFields, fmt.Sprintf("status = $%d", argIndex))
		args = append(args, *payload.Status)
		argIndex++
	}

	query := fmt.Sprintf("UPDATE system.edge_functions SET %s WHERE project_slug = $1 AND name = $2", strings.Join(updateFields, ", "))
	
	_, err = services.SystemPool.Exec(r.Context(), query, args...)
	if err != nil {
		http.Error(w, `{"error":"Update failed: `+err.Error()+`"}`, 500)
		return
	}

	w.Write([]byte(`{"success":true,"updated":true,"function":"` + functionName + `","version":` + fmt.Sprintf("%d", currentVersion+1) + `}`))
}

func (c *EdgeController) GetFunctionHistory(w http.ResponseWriter, r *http.Request) {
	val := r.Context().Value(types.CascataCtxKey)
	if val == nil {
		http.Error(w, `{"error":"Context Lost"}`, 500)
		return
	}
	ctx := val.(*types.CascataRequest)

	functionName := chi.URLParam(r, "name")
	if functionName == "" {
		http.Error(w, `{"error":"Function name required"}`, 400)
		return
	}

	rows, err := services.SystemPool.Query(r.Context(), 
		"SELECT id, version, created_at, created_by, change_reason FROM system.edge_functions_history WHERE project_slug = $1 AND name = $2 ORDER BY version DESC LIMIT 10", 
		ctx.Project.Slug, functionName)
	
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, 500)
		return
	}
	defer rows.Close()

	history := []map[string]interface{}{}
	for rows.Next() {
		var id, createdBy, changeReason string
		var version int
		var createdAt string
		rows.Scan(&id, &version, &createdAt, &createdBy, &changeReason)
		history = append(history, map[string]interface{}{
			"id": id, "version": version, "created_at": createdAt, "created_by": createdBy, "change_reason": changeReason,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(history)
}

func (c *EdgeController) GetFunctionDetails(w http.ResponseWriter, r *http.Request) {
	val := r.Context().Value(types.CascataCtxKey)
	if val == nil {
		http.Error(w, `{"error":"Context Lost"}`, 500)
		return
	}
	ctx := val.(*types.CascataRequest)

	functionName := chi.URLParam(r, "name")
	if functionName == "" {
		http.Error(w, `{"error":"Function name required"}`, 400)
		return
	}

	var id, name, runtime, status, content string
	var timeoutMs, memoryLimitMb, version, invocationCount, errorCount int
	var envVars map[string]string
	var createdAt, updatedAt, lastInvokedAt, lastErrorAt sql.NullTime
	var lastError sql.NullString

	err := services.SystemPool.QueryRow(r.Context(), 
		"SELECT id, name, content, runtime, status, timeout_ms, memory_limit_mb, env_vars, version, created_at, updated_at, last_invoked_at, invocation_count, error_count, last_error, last_error_at FROM system.edge_functions WHERE project_slug = $1 AND name = $2", 
		ctx.Project.Slug, functionName).Scan(&id, &name, &content, &runtime, &status, &timeoutMs, &memoryLimitMb, &envVars, &version, &createdAt, &updatedAt, &lastInvokedAt, &invocationCount, &errorCount, &lastError, &lastErrorAt)
	
	if err != nil {
		http.Error(w, `{"error":"Function not found"}`, 404)
		return
	}

	// Decrypt content
	plainContent, _ := c.cryptoSvc.Decrypt(content)

	details := map[string]interface{}{
		"id": id, "name": name, "content": plainContent, "runtime": runtime, "status": status,
		"timeout_ms": timeoutMs, "memory_limit_mb": memoryLimitMb, "env_vars": envVars,
		"version": version, "created_at": createdAt, "updated_at": updatedAt,
		"invocation_count": invocationCount, "error_count": errorCount,
	}

	if lastInvokedAt.Valid {
		details["last_invoked_at"] = lastInvokedAt.Time
	}
	if lastError.Valid {
		details["last_error"] = lastError.String
	}
	if lastErrorAt.Valid {
		details["last_error_at"] = lastErrorAt.Time
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(details)
}

func (c *EdgeController) GetHistoryVersionContent(w http.ResponseWriter, r *http.Request) {
	val := r.Context().Value(types.CascataCtxKey)
	if val == nil {
		http.Error(w, `{"error":"Context Lost"}`, 500)
		return
	}
	ctx := val.(*types.CascataRequest)

	functionName := chi.URLParam(r, "name")
	historyID := chi.URLParam(r, "historyId")
	if functionName == "" || historyID == "" {
		http.Error(w, `{"error":"Function name and history ID required"}`, 400)
		return
	}

	var id, name, runtime, content, createdBy string
	var changeReason sql.NullString
	var version, timeoutMs, memoryLimitMb int
	var envVars map[string]string
	var createdAt time.Time

	err := services.SystemPool.QueryRow(r.Context(), 
		"SELECT id, name, content, runtime, version, timeout_ms, memory_limit_mb, env_vars, created_at, created_by, change_reason FROM system.edge_functions_history WHERE project_slug = $1 AND name = $2 AND id = $3", 
		ctx.Project.Slug, functionName, historyID).Scan(&id, &name, &content, &runtime, &version, &timeoutMs, &memoryLimitMb, &envVars, &createdAt, &createdBy, &changeReason)
	
	if err != nil {
		http.Error(w, `{"error":"History version not found"}`, 404)
		return
	}

	// Decrypt content
	plainContent, _ := c.cryptoSvc.Decrypt(content)

	reason := ""
	if changeReason.Valid {
		reason = changeReason.String
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"id": id,
		"name": name,
		"content": plainContent,
		"runtime": runtime,
		"version": version,
		"timeout_ms": timeoutMs,
		"memory_limit_mb": memoryLimitMb,
		"env_vars": envVars,
		"created_at": createdAt,
		"created_by": createdBy,
		"change_reason": reason,
	})
}

func (c *EdgeController) RollbackToVersion(w http.ResponseWriter, r *http.Request) {
	val := r.Context().Value(types.CascataCtxKey)
	if val == nil {
		http.Error(w, `{"error":"Context Lost"}`, 500)
		return
	}
	ctx := val.(*types.CascataRequest)

	functionName := chi.URLParam(r, "name")
	historyID := chi.URLParam(r, "historyId")
	if functionName == "" || historyID == "" {
		http.Error(w, `{"error":"Function name and history ID required"}`, 400)
		return
	}

	// 1. Get history version details to restore
	var targetContent, targetRuntime string
	var targetVersion, targetTimeout, targetMemory int
	var targetEnv map[string]string
	
	err := services.SystemPool.QueryRow(r.Context(),
		"SELECT content, runtime, version, timeout_ms, memory_limit_mb, env_vars FROM system.edge_functions_history WHERE project_slug = $1 AND name = $2 AND id = $3",
		ctx.Project.Slug, functionName, historyID).Scan(&targetContent, &targetRuntime, &targetVersion, &targetTimeout, &targetMemory, &targetEnv)
	
	if err != nil {
		http.Error(w, `{"error":"History version not found"}`, 404)
		return
	}

	// 2. Retrieve current version details of active function to archive
	var currentID, currentContent string
	var currentVersion int
	err = services.SystemPool.QueryRow(r.Context(),
		"SELECT id, content, version FROM system.edge_functions WHERE project_slug = $1 AND name = $2",
		ctx.Project.Slug, functionName).Scan(&currentID, &currentContent, &currentVersion)
	
	if err != nil {
		http.Error(w, `{"error":"Active function not found"}`, 404)
		return
	}

	// 3. Archive current version before rollback
	userUUID := ""
	if ctx.User != nil {
		if sub, ok := ctx.User["sub"].(string); ok {
			userUUID = sub
		}
	}
	changeReason := fmt.Sprintf("Before Rollback to version %d", targetVersion)
	_, err = services.SystemPool.Exec(r.Context(),
		"INSERT INTO system.edge_functions_history (function_id, project_slug, name, content, version, created_by, change_reason) VALUES ($1, $2, $3, $4, $5, $6, $7)",
		currentID, ctx.Project.Slug, functionName, currentContent, currentVersion, userUUID, changeReason)
	
	if err != nil {
		log.Printf("Warning: failed to archive pre-rollback function version: %v", err)
	}

	// 4. Update function with history content
	_, err = services.SystemPool.Exec(r.Context(),
		`UPDATE system.edge_functions 
		 SET content = $1, runtime = $2, timeout_ms = $3, memory_limit_mb = $4, env_vars = $5, version = version + 1, updated_at = NOW() 
		 WHERE project_slug = $6 AND name = $7`,
		targetContent, targetRuntime, targetTimeout, targetMemory, targetEnv, ctx.Project.Slug, functionName)
	
	if err != nil {
		http.Error(w, `{"error":"Rollback update failed: `+err.Error()+`"}`, 500)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"rolled_back_to_version": targetVersion,
		"new_version": currentVersion + 1,
	})
}
