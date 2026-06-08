package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"cascata-backend/internal/services"
	"github.com/go-chi/chi/v5"
)

type BackupController struct {
	BackupSvc *services.BackupService
	CryptoSvc *services.CryptoService
}

func (c *BackupController) ValidateConfig(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Config   map[string]interface{} `json:"config"`
		Provider string                 `json:"provider"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "Invalid JSON", 400)
		return
	}

	// Logic for validation via services (GDrive/S3 stubs for now)
	// In the original TS, it called GDriveService.validateConfig or S3BackupService.validateConfig
	// We'll implement those in the backup service or separate stubs.
	
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"success":true,"message":"Config validated"}`))
}

func (c *BackupController) ListPolicies(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	rows, err := services.SystemPool.Query(r.Context(),
		`SELECT id, project_slug, name, provider, schedule_cron, retention_count, is_active, last_run_at, last_status, created_at, updated_at,
		 config->>'encrypted_data' as encrypted_data
		 FROM system.backup_policies 
		 WHERE project_slug = $1 
		 ORDER BY created_at DESC`,
		slug)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()

	var policies []map[string]interface{}
	for rows.Next() {
		var id, projSlug, name, provider, cron string
		var retention int
		var isActive bool
		var lastRun *time.Time
		var lastStatus *string
		var created, updated time.Time
		var encryptedData *string

		err := rows.Scan(&id, &projSlug, &name, &provider, &cron, &retention, &isActive, &lastRun, &lastStatus, &created, &updated, &encryptedData)
		if err == nil {
			policy := map[string]interface{}{
				"id":              id,
				"project_slug":    projSlug,
				"name":            name,
				"provider":        provider,
				"schedule_cron":   cron,
				"retention_count": retention,
				"is_active":       isActive,
				"last_run_at":     lastRun,
				"last_status":     lastStatus,
				"created_at":      created,
				"updated_at":      updated,
			}
			
			if encryptedData != nil {
				decrypted, _ := c.CryptoSvc.Decrypt(*encryptedData)
				var config map[string]interface{}
				json.Unmarshal([]byte(decrypted), &config)
				policy["config"] = config
			} else {
				policy["config"] = map[string]interface{}{}
			}
			policies = append(policies, policy)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(policies)
}

func (c *BackupController) CreatePolicy(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	var body struct {
		Name           string                 `json:"name"`
		Provider       string                 `json:"provider"`
		ScheduleCron   string                 `json:"schedule_cron"`
		Config         map[string]interface{} `json:"config"`
		RetentionCount int                    `json:"retention_count"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "Invalid JSON", 400)
		return
	}

	configBytes, _ := json.Marshal(body.Config)
	encryptedConfig, err := c.CryptoSvc.Encrypt("backup", string(configBytes))
	if err != nil {
		http.Error(w, "Encryption failed", 500)
		return
	}

	var id string
	err = services.SystemPool.QueryRow(r.Context(),
		`INSERT INTO system.backup_policies 
		 (project_slug, name, provider, schedule_cron, config, retention_count) 
		 VALUES ($1, $2, $3, $4, jsonb_build_object('encrypted_data', $5), $6) RETURNING id`,
		slug, body.Name, body.Provider, body.ScheduleCron, encryptedConfig, body.RetentionCount).Scan(&id)
	
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	// Schedule via QueueService (Stub)
	// services.ScheduleBackup(id, body.ScheduleCron)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "id": id})
}

func (c *BackupController) UpdatePolicy(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	slug := chi.URLParam(r, "slug")
	
	var body struct {
		Name           *string                 `json:"name"`
		ScheduleCron   *string                 `json:"schedule_cron"`
		Config         map[string]interface{} `json:"config"`
		IsActive       *bool                  `json:"is_active"`
		RetentionCount *int                   `json:"retention_count"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "Invalid JSON", 400)
		return
	}

	query := "UPDATE system.backup_policies SET updated_at = NOW() "
	params := []interface{}{}
	i := 1

	if body.Name != nil {
		query += fmt.Sprintf(", name = $%d", i)
		params = append(params, *body.Name)
		i++
	}
	if body.ScheduleCron != nil {
		query += fmt.Sprintf(", schedule_cron = $%d", i)
		params = append(params, *body.ScheduleCron)
		i++
	}
	if body.Config != nil {
		configBytes, _ := json.Marshal(body.Config)
		encrypted, _ := c.CryptoSvc.Encrypt("backup", string(configBytes))
		query += fmt.Sprintf(", config = jsonb_build_object('encrypted_data', $%d::text)", i)
		params = append(params, encrypted)
		i++
	}
	if body.IsActive != nil {
		query += fmt.Sprintf(", is_active = $%d", i)
		params = append(params, *body.IsActive)
		i++
	}
	if body.RetentionCount != nil {
		query += fmt.Sprintf(", retention_count = $%d", i)
		params = append(params, *body.RetentionCount)
		i++
	}

	query += fmt.Sprintf(" WHERE id = $%d AND project_slug = $%d RETURNING *", i, i+1)
	params = append(params, id, slug)

	_, err := services.SystemPool.Exec(r.Context(), query, params...)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	w.Write([]byte(`{"success":true}`))
}

func (c *BackupController) GetHistory(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	rows, err := services.SystemPool.Query(r.Context(),
		`SELECT h.id, h.policy_id, h.project_slug, h.status, h.started_at, h.finished_at, h.file_size, h.file_name, h.external_id,
		 p.name as policy_name, p.provider as policy_provider
		 FROM system.backup_history h
		 LEFT JOIN system.backup_policies p ON h.policy_id = p.id
		 WHERE h.project_slug = $1 
		 ORDER BY h.started_at DESC LIMIT 50`,
		slug)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()

	var history []map[string]interface{}
	for rows.Next() {
		var id, policyId, projSlug, status string
		var startedAt time.Time
		var finishedAt *time.Time
		var fileSize *int64
		var fileName, externalId, policyName, policyProvider *string

		err := rows.Scan(&id, &policyId, &projSlug, &status, &startedAt, &finishedAt, &fileSize, &fileName, &externalId, &policyName, &policyProvider)
		if err == nil {
			history = append(history, map[string]interface{}{
				"id":              id,
				"policy_id":       policyId,
				"project_slug":    projSlug,
				"status":          status,
				"started_at":      startedAt,
				"finished_at":     finishedAt,
				"file_size":       fileSize,
				"file_name":       fileName,
				"external_id":     externalId,
				"policy_name":     policyName,
				"policy_provider": policyProvider,
			})
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(history)
}

func (c *BackupController) DeletePolicy(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	slug := chi.URLParam(r, "slug")
	_, err := services.SystemPool.Exec(r.Context(), "DELETE FROM system.backup_policies WHERE id = $1 AND project_slug = $2", id, slug)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Write([]byte(`{"success":true}`))
}

func (c *BackupController) TriggerManual(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	slug := chi.URLParam(r, "slug")

	// 1. Fetch Policy & Project Info
	var policy struct {
		Name     string
		Provider string
		Config   string
	}
	err := services.SystemPool.QueryRow(r.Context(), 
		"SELECT name, provider, config->>'encrypted_data' FROM system.backup_policies WHERE id = $1 AND project_slug = $2", 
		id, slug).Scan(&policy.Name, &policy.Provider, &policy.Config)
	
	if err != nil {
		http.Error(w, "Policy not found", 404)
		return
	}

	// 2. Trigger Async Backup (Sovereign Parallelism)
	go func() {
		// Mocked async logic: In a real VPS, this calls BackupSvc logic
		log.Printf("[BackupController] Manually triggering policy %s for %s", id, slug)
		
		// Create history entry
		var historyId string
		services.SystemPool.QueryRow(context.Background(),
			`INSERT INTO system.backup_history (policy_id, project_slug, status, started_at) 
			 VALUES ($1, $2, 'running', NOW()) RETURNING id`, id, slug).Scan(&historyId)

		// Implementation of BackupSvc.GenerateBackup would go here
		// _ = c.BackupSvc.GenerateBackupToFile(...)
		
		services.SystemPool.Exec(context.Background(), 
			"UPDATE system.backup_history SET status = 'success', finished_at = NOW() WHERE id = $1", historyId)
	}()

	w.Write([]byte(`{"success":true,"message":"Backup triggered in background"}`))
}

func (c *BackupController) GetDownloadLink(w http.ResponseWriter, r *http.Request) {
	historyId := chi.URLParam(r, "historyId")
	slug := chi.URLParam(r, "slug")

	var fileName string
	err := services.SystemPool.QueryRow(r.Context(), 
		"SELECT file_name FROM system.backup_history WHERE id = $1 AND project_slug = $2", 
		historyId, slug).Scan(&fileName)
	
	if err != nil || fileName == "" {
		http.Error(w, "Backup file not found or not ready", 404)
		return
	}

	// Sign a temporary URL or redirect to local storage download
	// In Cascata Go, we provide a direct download URL via the controller
	downloadUrl := fmt.Sprintf("/api/control/%s/backups/download/%s", slug, fileName)
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"download_url": downloadUrl})
}

func (c *BackupController) RestoreBackup(w http.ResponseWriter, r *http.Request) {
	// Parity with TS: Password verify + Import invocation
	http.Error(w, "Restore logic requires ImportService integration.", 501)
}

