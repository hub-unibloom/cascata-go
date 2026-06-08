package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"cascata-backend/internal/services"
	"cascata-backend/internal/types"

	"github.com/go-chi/chi/v5"
)

// StorageController handles all storage operations matching TypeScript implementation
type StorageController struct {
	storageSvc *services.StorageService
}

// NewStorageController creates a new storage controller
func NewStorageController() *StorageController {
	return &StorageController{
		storageSvc: services.NewStorageService(),
	}
}

// listBuckets returns all buckets for a project
func (c *StorageController) ListBuckets(w http.ResponseWriter, r *http.Request) {
	val := r.Context().Value(types.CascataCtxKey)
	if val == nil {
		http.Error(w, `{"error":"Context Lost"}`, 500)
		return
	}
	ctx := val.(*types.CascataRequest)

	storageRoot := os.Getenv("STORAGE_ROOT")
	if storageRoot == "" {
		storageRoot = "./storage"
	}

	projectPath := c.getProjectStoragePath(storageRoot, ctx)
	os.MkdirAll(projectPath, 0750)  // CORREÇÃO: world não-readable

	entries, err := os.ReadDir(projectPath)
	if err != nil {
		http.Error(w, `{"error":"Failed to read storage directories"}`, 500)
		return
	}

	// Busca RLS status dos buckets do banco de dados (usando SystemPool - tabela está no banco do sistema)
	branchName := types.GetBranchName(r.Context())
	rlsRows, err := services.SystemPool.Query(r.Context(), `
		SELECT bucket, rls_enabled FROM system.storage_objects 
		WHERE project_slug = $1 AND branch_name = $2 AND is_folder = true AND parent_path = ''
	`, ctx.Project.Slug, branchName)
	if err != nil {
		rlsRows = nil
	}
	rlsMap := make(map[string]bool)
	if rlsRows != nil {
		defer rlsRows.Close()
		for rlsRows.Next() {
			var bucketName string
			var rlsEnabled bool
			rlsRows.Scan(&bucketName, &rlsEnabled)
			rlsMap[bucketName] = rlsEnabled
		}
	}

	buckets := []map[string]interface{}{}

	for _, entry := range entries {
		if entry.IsDir() && entry.Name() != "branches" { // Ignora pasta reservada para branches
			name := entry.Name()
			// Por padrão, assume RLS ativo se não estiver no mapa
			rlsEnabled, hasRls := rlsMap[name]
			if !hasRls {
				rlsEnabled = true
			}
			buckets = append(buckets, map[string]interface{}{
				"name":        name,
				"rls_enabled": rlsEnabled,
			})
		}
	}

	// NOTE: The 'default' bucket is created only when:
	// 1. A new project is created (in admin.go CreateProject)
	// 2. User explicitly creates a bucket named "default"
	// This allows users to rename or delete the default bucket if desired.

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(buckets)
}

// createBucket creates a new bucket
func (c *StorageController) CreateBucket(w http.ResponseWriter, r *http.Request) {
	val := r.Context().Value(types.CascataCtxKey)
	if val == nil {
		http.Error(w, `{"error":"Context Lost"}`, 500)
		return
	}
	ctx := val.(*types.CascataRequest)

	var body struct {
		Name       string `json:"name"`
		RLSEnabled *bool  `json:"rls_enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" {
		http.Error(w, `{"error":"Bucket name required"}`, 400)
		return
	}

	branchName := types.GetBranchName(r.Context())

	// CRITICAL: Acquire distributed lock to prevent race conditions between workers
	lockKey := ctx.Project.Slug
	if branchName != "main" {
		lockKey = ctx.Project.Slug + ":" + branchName
	}
	lock := services.LockBucketAcquireWithRetry(r.Context(), lockKey, body.Name, 10)
	if lock == nil {
		http.Error(w, `{"error":"Bucket operation in progress by another worker. Please retry."}`, 423)
		return
	}
	defer lock.Release(r.Context())

	// Por padrão, RLS é ativado (true) se não especificado
	rlsEnabled := true
	if body.RLSEnabled != nil {
		rlsEnabled = *body.RLSEnabled
	}

	storageRoot := os.Getenv("STORAGE_ROOT")
	if storageRoot == "" {
		storageRoot = "./storage"
	}

	// Use safe path
	segments := append(c.getProjectStorageSegments(ctx), body.Name)
	safePath, err := c.storageSvc.GetSafePath(storageRoot, segments...)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"Invalid bucket name: %s"}`, err.Error()), 403)
		return
	}

	// Check if bucket already exists to prevent overwrites
	if _, err := os.Stat(safePath); err == nil {
		http.Error(w, `{"error":"Bucket already exists"}`, 409)
		return
	}

	if err := os.MkdirAll(safePath, 0750); err != nil {  // CORREÇÃO: world não-readable
		http.Error(w, `{"error":"Failed to create bucket"}`, 500)
		return
	}

	// Insere o bucket na tabela storage_objects com RLS ativo por padrão (usando SystemPool)
	_, err = services.SystemPool.Exec(r.Context(), `
		INSERT INTO system.storage_objects (project_slug, branch_name, bucket, name, parent_path, full_path, is_folder, size, rls_enabled)
		VALUES ($1, $2, $3, $3, '', $3, true, 0, $4)
		ON CONFLICT (project_slug, branch_name, bucket, full_path) DO UPDATE SET rls_enabled = $4
	`, ctx.Project.Slug, branchName, body.Name, rlsEnabled)
	if err != nil {
		// Log erro mas não falha a criação do bucket físico
		fmt.Printf("Warning: failed to insert bucket metadata: %v\n", err)
	}

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "name": body.Name, "rls_enabled": rlsEnabled})
}

// renameBucket renames a bucket with DB rollback support
func (c *StorageController) RenameBucket(w http.ResponseWriter, r *http.Request) {
	val := r.Context().Value(types.CascataCtxKey)
	if val == nil {
		http.Error(w, `{"error":"Context Lost"}`, 500)
		return
	}
	ctx := val.(*types.CascataRequest)

	oldName := chi.URLParam(r, "name")
	var body struct {
		NewName string `json:"new_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.NewName == "" {
		http.Error(w, `{"error":"New bucket name required"}`, 400)
		return
	}

	storageRoot := os.Getenv("STORAGE_ROOT")
	if storageRoot == "" {
		storageRoot = "./storage"
	}

	// Get safe paths
	projectRoot, _ := c.storageSvc.GetSafePath(storageRoot, c.getProjectStorageSegments(ctx)...)
	oldPath, _ := c.storageSvc.GetSafePath(projectRoot, oldName)
	newPath, _ := c.storageSvc.GetSafePath(projectRoot, body.NewName)

	// Check old path exists
	if _, err := os.Stat(oldPath); os.IsNotExist(err) {
		http.Error(w, `{"error":"Bucket not found"}`, 404)
		return
	}

	// Check new path doesn't exist
	if _, err := os.Stat(newPath); err == nil {
		http.Error(w, `{"error":"Name already exists"}`, 400)
		return
	}

	// Rename filesystem
	if err := os.Rename(oldPath, newPath); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"Failed to rename bucket: %s"}`, err.Error()), 500)
		return
	}

	branchName := types.GetBranchName(r.Context())

	// Update database (with rollback on failure)
	_, err := services.SystemPool.Exec(r.Context(),
		"UPDATE system.storage_objects SET bucket = $1 WHERE project_slug = $2 AND branch_name = $3 AND bucket = $4",
		body.NewName, ctx.Project.Slug, branchName, oldName)

	if err != nil {
		// Rollback filesystem
		os.Rename(newPath, oldPath)
		http.Error(w, `{"error":"Database update failed, filesystem change reverted"}`, 500)
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  true,
		"old_name": oldName,
		"new_name": body.NewName,
	})
}

// deleteBucket removes a bucket with external provider cleanup
func (c *StorageController) DeleteBucket(w http.ResponseWriter, r *http.Request) {
	val := r.Context().Value(types.CascataCtxKey)
	if val == nil {
		http.Error(w, `{"error":"Context Lost"}`, 500)
		return
	}
	ctx := val.(*types.CascataRequest)

	// Security check
	name := chi.URLParam(r, "name")
	if name == "" || name == "." || name == ".." {
		http.Error(w, `{"error":"Invalid bucket name structure"}`, 400)
		return
	}

	storageRoot := os.Getenv("STORAGE_ROOT")
	if storageRoot == "" {
		storageRoot = "./storage"
	}

	// Get storage config
	storageConfig := services.ResolveStorageConfig(metadataToMap(ctx.Project.Metadata), "")

	branchName := types.GetBranchName(r.Context())

	// External provider cleanup
	if storageConfig.Provider != services.ProviderLocal {
		objects, err := services.SystemPool.Query(r.Context(),
			"SELECT full_path FROM system.storage_objects WHERE project_slug=$1 AND branch_name=$2 AND bucket=$3",
			ctx.Project.Slug, branchName, name)
		if err == nil {
			defer objects.Close()
			for objects.Next() {
				var fullPath string
				objects.Scan(&fullPath)
				// Fire and forget deletion
				go func(path string) {
					c.storageSvc.Delete(context.Background(), path, storageConfig)
				}(fullPath)
			}
		}
	}

	// Local cleanup
	projectRoot, _ := c.storageSvc.GetSafePath(storageRoot, c.getProjectStorageSegments(ctx)...)
	bucketPath, _ := c.storageSvc.GetSafePath(projectRoot, name)

	os.RemoveAll(bucketPath)

	// Metadata cleanup
	services.SystemPool.Exec(r.Context(),
		"DELETE FROM system.storage_objects WHERE project_slug=$1 AND branch_name=$2 AND bucket=$3",
		ctx.Project.Slug, branchName, name)

	// Invalidate quota cache
	services.InvalidateProjectStorageUsage(ctx.Project.Slug)

	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// createFolder creates a new folder in a bucket
func (c *StorageController) CreateFolder(w http.ResponseWriter, r *http.Request) {
	val := r.Context().Value(types.CascataCtxKey)
	if val == nil {
		http.Error(w, `{"error":"Context Lost"}`, 500)
		return
	}
	ctx := val.(*types.CascataRequest)

	bucket := chi.URLParam(r, "bucket")
	if bucket == "" {
		bucket = "default"
	}

	var body struct {
		Name string `json:"name"`
		Path string `json:"path"`
	}
	json.NewDecoder(r.Body).Decode(&body)

	folderName := body.Name
	if folderName == "" {
		folderName = "new-folder"
	}

	branchName := types.GetBranchName(r.Context())

	// CRITICAL: Acquire distributed lock to prevent race conditions between workers
	fullFolderPath := filepath.Join(body.Path, folderName)
	fullFolderPath = strings.ReplaceAll(fullFolderPath, "\\", "/")
	lockKey := ctx.Project.Slug
	if branchName != "main" {
		lockKey = ctx.Project.Slug + ":" + branchName
	}
	lock := services.LockFolderAcquireWithRetry(r.Context(), lockKey, bucket, fullFolderPath, 10)
	if lock == nil {
		http.Error(w, `{"error":"Folder operation in progress by another worker. Please retry."}`, 423)
		return
	}
	defer lock.Release(r.Context())

	storageRoot := os.Getenv("STORAGE_ROOT")
	if storageRoot == "" {
		storageRoot = "./storage"
	}

	// Get safe paths
	bucketSegments := append(c.getProjectStorageSegments(ctx), bucket)
	bucketPath, _ := c.storageSvc.GetSafePath(storageRoot, bucketSegments...)
	targetDir, err := c.storageSvc.GetSafePath(bucketPath, body.Path, folderName)
	if err != nil {
		http.Error(w, `{"error":"Access Denied: Path Traversal"}`, 403)
		return
	}

	// Check if exists (double-check after acquiring lock)
	if _, err := os.Stat(targetDir); err == nil {
		http.Error(w, `{"error":"Folder already exists"}`, 400)
		return
	}

	if err := os.MkdirAll(targetDir, 0750); err != nil {  // CORREÇÃO: world não-readable
		http.Error(w, `{"error":"Failed to create folder"}`, 500)
		return
	}

	// Index the folder
	fullRelPath := filepath.Join(body.Path, folderName)
	fullRelPath = strings.ReplaceAll(fullRelPath, "\\", "/")
	if err := services.IndexObject(r.Context(), services.SystemPool, ctx.Project.Slug, bucket, fullRelPath, map[string]interface{}{
		"size":     0,
		"mimeType": "application/directory",
		"isFolder": true,
		"provider": "local",
	}); err != nil {
		// Remove pasta do filesystem se indexação falhar
		os.RemoveAll(targetDir)
		http.Error(w, fmt.Sprintf(`{"error":"Failed to index folder: %s"}`, err.Error()), 500)
		return
	}

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"name":    folderName,
		"path":    fullRelPath,
	})
}

// getRawMetadata busca o metadata cru do banco de dados (preserva campos não mapeados na struct)
func (c *StorageController) getRawMetadata(ctx context.Context, projectSlug string) map[string]interface{} {
	var rawMetaJSON string
	err := services.SystemPool.QueryRow(ctx,
		"SELECT COALESCE(metadata::text, '{}') FROM system.projects WHERE slug = $1",
		projectSlug).Scan(&rawMetaJSON)
	
	if err != nil {
		fmt.Printf("[getRawMetadata] ERROR: %v\n", err)
		return make(map[string]interface{})
	}
	
	var rawMeta map[string]interface{}
	if err := json.Unmarshal([]byte(rawMetaJSON), &rawMeta); err != nil {
		fmt.Printf("[getRawMetadata] Unmarshal ERROR: %v\n", err)
		return make(map[string]interface{})
	}
	
	fmt.Printf("[getRawMetadata] Loaded metadata keys: %v\n", getKeys(rawMeta))
	return rawMeta
}

// getKeys helper para debug
func getKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// signUpload generates signed URL for hybrid upload (direct vs proxy)
func (c *StorageController) signUpload(w http.ResponseWriter, r *http.Request) {
	val := r.Context().Value(types.CascataCtxKey)
	if val == nil {
		http.Error(w, `{"error":"Context Lost"}`, 500)
		return
	}
	ctx := val.(*types.CascataRequest)

	bucket := chi.URLParam(r, "bucket")
	if bucket == "" {
		bucket = "default"
	}

	var body struct {
		Name string `json:"name"`
		Type string `json:"type"`
		Size int64  `json:"size"`
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"Invalid request body"}`, 400)
		return
	}

	ext := filepath.Ext(body.Name)
	
	// Buscar metadata fresh do banco (preserva storage_governance e outros campos não mapeados)
	rawMetadata := c.getRawMetadata(r.Context(), ctx.Project.Slug)
	
	storageConfig := services.ResolveStorageConfig(rawMetadata, ext)

	// Quota check
	limit := "1GB"
	if limitData, ok := rawMetadata["storage_limit"].(string); ok {
		limit = limitData
	}

	quotaCheck := c.checkQuota(ctx.Project.Slug, body.Size, limit, string(storageConfig.Provider))
	if !quotaCheck.Allowed {
		http.Error(w, `{"error":"Storage Quota Exceeded. Upgrade plan or delete files."}`, 402)
		return
	}
	reservationID := quotaCheck.ReservationID

	// Cleanup function to release reservation on error
	cleanup := func() {
		if reservationID != "" {
			services.ReleaseStorage(r.Context(), ctx.Project.Slug, reservationID)
		}
	}

	// Governance check (usa metadata fresh do banco)
	governance := c.getGovernance(rawMetadata)
	sector := services.GetSectorForExt(ext)
	rule := governance[sector]
	globalRule := governance["global"]
	
	// CORREÇÃO: Fallback para global se setor nao existe ou nao tem MaxSize
	if rule.MaxSize == "" {
		rule = globalRule
	}
	
	// CORREÇÃO: Fallback de AllowedExts para global se setor especifico esta vazio
	if len(rule.AllowedExts) == 0 {
		rule.AllowedExts = globalRule.AllowedExts
	}

	// CORREÇÃO: Bloquear por padrão - só permitir extensões explicitamente aprovadas
	extClean := strings.ToLower(strings.TrimLeft(ext, "."))
	found := false
	for _, allowed := range rule.AllowedExts {
		if strings.ToLower(allowed) == extClean {
			found = true
			break
		}
	}
	if !found {
		cleanup() // Release reservation on policy violation
		fmt.Printf("[Security] SignUpload rejected: extension '.%s' not in allowed list for sector '%s'. Allowed: %v\n", 
			extClean, sector, rule.AllowedExts)
		http.Error(w, fmt.Sprintf(`{"error":"Policy Violation: Extension .%s not allowed in sector '%s'"}`, extClean, sector), 403)
		return
	}

	maxSizeBytes := services.ParseBytes(rule.MaxSize)
	fmt.Printf("[SignUpload Debug] File: %s, body.Size: %d, rule.MaxSize: %s, parsed: %d\n", 
		body.Name, body.Size, rule.MaxSize, maxSizeBytes)
	
	if body.Size > maxSizeBytes {
		cleanup() // Release reservation on policy violation
		fmt.Printf("[SignUpload Debug] REJECTED: %d > %d (max: %s)\n", body.Size, maxSizeBytes, rule.MaxSize)
		http.Error(w, fmt.Sprintf(`{"error":"Policy Violation: File size %d exceeds limit %s"}`, body.Size, rule.MaxSize), 403)
		return
	}
	fmt.Printf("[SignUpload Debug] ACCEPTED: %d <= %d\n", body.Size, maxSizeBytes)

	// Generate upload URL
	relativePath := body.Path
	relativePath = strings.TrimLeft(relativePath, "/")
	fullKey := filepath.Join(relativePath, body.Name)
	fullKey = strings.ReplaceAll(fullKey, "\\", "/")

	branchName := types.GetBranchName(r.Context())
	if branchName != "main" {
		fullKey = filepath.Join("branches", branchName, fullKey)
		fullKey = strings.ReplaceAll(fullKey, "\\", "/")
	}

	result, err := c.storageSvc.CreateUploadUrl(fullKey, body.Type, storageConfig)
	if err != nil {
		cleanup()
		http.Error(w, fmt.Sprintf(`{"error":"Failed to create upload URL: %s"}`, err.Error()), 500)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"strategy": result.Strategy,
		"url":      result.URL,
		"method":   result.Method,
		"fields":   result.Headers,
		"proxyUrl": fmt.Sprintf("/api/data/%s/storage/%s/upload", ctx.Project.Slug, bucket),
	})
}

// UploadFile handles uploads with streaming and async support
// For files < 50MB: Direct streaming upload (no temp files)
// For files >= 50MB: Async queue upload (background processing)
func (c *StorageController) UploadFile(w http.ResponseWriter, r *http.Request) {
	val := r.Context().Value(types.CascataCtxKey)
	if val == nil {
		http.Error(w, `{"error":"Context Lost"}`, 500)
		return
	}
	ctx := val.(*types.CascataRequest)

	bucket := chi.URLParam(r, "bucket")
	if bucket == "" {
		bucket = "default"
	}

	// Parse multipart with generous limit for async uploads
	if err := r.ParseMultipartForm(5 << 30); err != nil { // 5GB max for parsing
		fmt.Printf("[Upload Debug] ParseMultipartForm error: %v\n", err)
		http.Error(w, fmt.Sprintf(`{"error":"Upload too large: %v"}`, err), 400)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, `{"error":"Missing file in form-data"}`, 400)
		return
	}
	defer file.Close()

	// Get target path
	targetPath := r.FormValue("path")
	ext := filepath.Ext(header.Filename)
	
	// Buscar metadata fresh do banco (preserva storage_governance e outros campos não mapeados)
	rawMetadata := c.getRawMetadata(r.Context(), ctx.Project.Slug)
	
	storageConfig := services.ResolveStorageConfig(rawMetadata, ext)

	// Quota check
	limit := "1GB"
	if limitData, ok := rawMetadata["storage_limit"].(string); ok {
		limit = limitData
	}

	quotaCheck := c.checkQuota(ctx.Project.Slug, header.Size, limit, string(storageConfig.Provider))
	if !quotaCheck.Allowed {
		http.Error(w, `{"error":"Storage Quota Exceeded"}`, 402)
		return
	}
	reservationID := quotaCheck.ReservationID

	// Cleanup function
	cleanup := func() {
		if reservationID != "" {
			services.ReleaseStorage(r.Context(), ctx.Project.Slug, reservationID)
		}
	}

	// Governance check (usa metadata fresh do banco)
	governance := c.getGovernance(rawMetadata)
	sector := services.GetSectorForExt(ext)
	rule := governance[sector]
	globalRule := governance["global"]
	
	// CORREÇÃO: Fallback para global se setor nao existe ou nao tem MaxSize
	if rule.MaxSize == "" {
		rule = globalRule
	}
	
	// CORREÇÃO: Fallback de AllowedExts para global se setor especifico esta vazio
	if len(rule.AllowedExts) == 0 {
		rule.AllowedExts = globalRule.AllowedExts
	}

	// CORREÇÃO: Bloquear por padrão - só permitir extensões explicitamente aprovadas
	extClean := strings.ToLower(strings.TrimLeft(ext, "."))
	found := false
	for _, allowed := range rule.AllowedExts {
		if strings.ToLower(allowed) == extClean {
			found = true
			break
		}
	}
	if !found {
		cleanup()
		fmt.Printf("[Security] Upload rejected: extension '.%s' not in allowed list for sector '%s'. Allowed: %v\n", 
			extClean, sector, rule.AllowedExts)
		http.Error(w, fmt.Sprintf(`{"error":"Policy Violation: Extension .%s not allowed in sector '%s'"}`, extClean, sector), 403)
		return
	}

	maxSizeBytes := services.ParseBytes(rule.MaxSize)
	fmt.Printf("[Upload Debug] File: %s, header.Size: %d, rule.MaxSize: %s, parsed: %d\n", 
		header.Filename, header.Size, rule.MaxSize, maxSizeBytes)
	
	if header.Size > maxSizeBytes {
		cleanup()
		fmt.Printf("[Upload Debug] REJECTED: %d > %d (max: %s)\n", header.Size, maxSizeBytes, rule.MaxSize)
		http.Error(w, fmt.Sprintf(`{"error":"Policy Violation: File size %d exceeds limit %s"}`, header.Size, rule.MaxSize), 403)
		return
	}

	const AsyncThreshold = 50 << 20 // 50MB - files larger than this go to async queue

	// ASYNC UPLOAD for large files
	if header.Size >= AsyncThreshold && storageConfig.Provider == services.ProviderLocal {
		// CORREÇÃO: Usar diretório seguro com permissões restritas
		tempDir := os.Getenv("TEMP_UPLOAD_ROOT")
		if tempDir == "" {
			storageRoot := os.Getenv("STORAGE_ROOT")
			if storageRoot == "" {
				storageRoot = "./storage"
			}
			tempDir = filepath.Join(storageRoot, ".temp")
		}
		
		// Garantir que diretório temp existe com permissões seguras (0700)
		if err := os.MkdirAll(tempDir, 0700); err != nil {
			cleanup()
			http.Error(w, `{"error":"Failed to create temp directory"}`, 500)
			return
		}
		
		// CORREÇÃO: Criar temp file no diretório seguro com permissões 0600
		tempFile, err := os.CreateTemp(tempDir, "upload-async-*")
		if err != nil {
			cleanup()
			http.Error(w, `{"error":"Failed to create temp file"}`, 500)
			return
		}
		
		// CORREÇÃO: Setar permissões restritas imediatamente (0600 = só owner)
		tempFile.Chmod(0600)
		tempFilePath := tempFile.Name()

		// CORREÇÃO: Cleanup garantido mesmo em crash/panic durante streaming
		uploadFailed := false
		defer func() {
			if uploadFailed && tempFilePath != "" {
				os.Remove(tempFilePath)
			}
		}()

		// Stream directly to temp file (no memory buffer)
		written, err := io.Copy(tempFile, file)
		tempFile.Close()
		if err != nil {
			uploadFailed = true
			cleanup()
			http.Error(w, `{"error":"Failed to stream file"}`, 500)
			return
		}

		// Validate magic bytes
		if !services.ValidateMagicBytes(tempFilePath, ext) {
			uploadFailed = true
			cleanup()
			http.Error(w, `{"error":"Security Alert: File signature mismatch"}`, 400)
			return
		}

		// Enqueue for async processing
		job := &services.UploadJob{
			ProjectSlug: ctx.Project.Slug,
			Bucket:      bucket,
			FileName:    header.Filename,
			FileSize:    written,
			ContentType: header.Header.Get("Content-Type"),
			TempPath:    tempFilePath,
			TargetPath:  targetPath,
		}

		if err := services.EnqueueUpload(r.Context(), job); err != nil {
			uploadFailed = true
			cleanup()
			http.Error(w, fmt.Sprintf(`{"error":"Failed to queue upload: %s"}`, err.Error()), 500)
			return
		}
		
		// Upload enfileirado com sucesso - não marcar como failed para não limpar temp file

		// Release reservation immediately (will be re-checked by worker)
		if reservationID != "" {
			services.ReleaseStorage(r.Context(), ctx.Project.Slug, reservationID)
		}

		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":   true,
			"async":     true,
			"job_id":    job.ID,
			"message":   "Upload queued for background processing",
			"provider":  "local",
			"file_size": written,
		})
		return
	}

	// SYNC UPLOAD for small files - PERFECT STREAMING (no temp files!)
	storageRoot := os.Getenv("STORAGE_ROOT")
	if storageRoot == "" {
		storageRoot = "/cascata/storage"
	}

	if storageConfig.Provider == services.ProviderLocal {
		// DIRECT STREAMING: file → destination (no intermediate temp file!)
		bucketSegments := append(c.getProjectStorageSegments(ctx), bucket)
		bucketPath, _ := c.storageSvc.GetSafePath(storageRoot, bucketSegments...)
		destPath := filepath.Join(bucketPath, targetPath, header.Filename)

		// Ensure directory exists
		if err := os.MkdirAll(filepath.Dir(destPath), 0750); err != nil {  // CORREÇÃO: world não-readable
			cleanup()
			http.Error(w, `{"error":"Failed to create directory"}`, 500)
			return
		}

		// For small files, do magic bytes validation on first 8KB
		if header.Size > 0 {
			headerBuffer := make([]byte, min(8192, int(header.Size)))
			if _, err := file.Read(headerBuffer); err != nil {
				cleanup()
				http.Error(w, `{"error":"Failed to read file header"}`, 500)
				return
			}
			file.Seek(0, 0) // Reset to beginning

			if !services.ValidateMagicBytesBuffer(headerBuffer, ext) {
				cleanup()
				http.Error(w, `{"error":"Security Alert: File signature mismatch"}`, 400)
				return
			}
		}

		// Create destination file
		destFile, err := os.Create(destPath)
		if err != nil {
			cleanup()
			http.Error(w, `{"error":"Failed to create destination file"}`, 500)
			return
		}

		// PERFECT STREAMING: io.Copy streams from request to disk without loading into memory
		written, err := io.Copy(destFile, file)
		destFile.Close()

		if err != nil {
			os.Remove(destPath)
			cleanup()
			http.Error(w, `{"error":"Failed to write file"}`, 500)
			return
		}

		resultPath := strings.Replace(destPath, storageRoot, "", 1)

		// Index the file
		fullKey := filepath.Join(targetPath, header.Filename)
		fullKey = strings.ReplaceAll(fullKey, "\\", "/")
		if err := services.IndexObject(r.Context(), services.SystemPool, ctx.Project.Slug, bucket, fullKey, map[string]interface{}{
			"size":     written,
			"mimeType": header.Header.Get("Content-Type"),
			"isFolder": false,
			"provider": "local",
		}); err != nil {
			fmt.Printf("Warning: failed to index uploaded file: %v\n", err)
		}

		// Cleanup
		services.InvalidateProjectStorageUsage(ctx.Project.Slug)
		if reservationID != "" {
			services.ReleaseStorage(r.Context(), ctx.Project.Slug, reservationID)
		}

		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":   true,
			"async":     false,
			"path":      resultPath,
			"provider":  "local",
			"file_size": written,
		})
	} else {
		// External provider upload (S3, etc.)
		file.Seek(0, 0)
		targetPathWithBranch := targetPath
		branchName := types.GetBranchName(r.Context())
		if branchName != "main" {
			targetPathWithBranch = filepath.Join("branches", branchName, targetPath)
			targetPathWithBranch = strings.ReplaceAll(targetPathWithBranch, "\\", "/")
		}
		url, err := c.storageSvc.Upload(r.Context(), file, header, ctx.Project.Slug, bucket, targetPathWithBranch, storageConfig)
		if err != nil {
			cleanup()
			http.Error(w, fmt.Sprintf(`{"error":"Upload failed: %s"}`, err.Error()), 500)
			return
		}

		// Index the file
		fullKey := filepath.Join(targetPath, header.Filename)
		fullKey = strings.ReplaceAll(fullKey, "\\", "/")
		if err := services.IndexObject(r.Context(), services.SystemPool, ctx.Project.Slug, bucket, fullKey, map[string]interface{}{
			"size":     header.Size,
			"mimeType": header.Header.Get("Content-Type"),
			"isFolder": false,
			"provider": string(storageConfig.Provider),
		}); err != nil {
			fmt.Printf("Warning: failed to index uploaded file: %v\n", err)
		}

		services.InvalidateProjectStorageUsage(ctx.Project.Slug)
		if reservationID != "" {
			services.ReleaseStorage(r.Context(), ctx.Project.Slug, reservationID)
		}

		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":  true,
			"path":     url,
			"provider": storageConfig.Provider,
			"url":      url,
		})
	}
}

// listFiles lists files in a bucket path using the indexer
func (c *StorageController) ListFiles(w http.ResponseWriter, r *http.Request) {
	val := r.Context().Value(types.CascataCtxKey)
	if val == nil {
		http.Error(w, `{"error":"Context Lost"}`, 500)
		return
	}
	ctx := val.(*types.CascataRequest)

	bucket := chi.URLParam(r, "bucket")
	if bucket == "" {
		bucket = "default"
	}
	path := r.URL.Query().Get("path")

	items, err := services.ListStorageObjects(r.Context(), services.SystemPool, ctx.Project.Slug, bucket, path)
	if err != nil {
		// Fallback to filesystem if DB error
		items = c.listFromFilesystem(ctx, ctx.Project.Slug, bucket, path)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"items": items})
}

// listFromFilesystem reads files directly from filesystem as fallback
func (c *StorageController) listFromFilesystem(ctx *types.CascataRequest, projectSlug, bucket, path string) []map[string]interface{} {
	storageRoot := os.Getenv("STORAGE_ROOT")
	if storageRoot == "" {
		storageRoot = "./storage"
	}

	// CORREÇÃO: Usar GetSafePath para prevenir path traversal
	bucketSegments := append(c.getProjectStorageSegments(ctx), bucket)
	bucketPath, err := c.storageSvc.GetSafePath(storageRoot, bucketSegments...)
	if err != nil {
		fmt.Printf("[listFromFilesystem] Security error: invalid bucket path: %v\n", err)
		return []map[string]interface{}{}
	}

	fullPath, err := c.storageSvc.GetSafePath(bucketPath, path)
	if err != nil {
		fmt.Printf("[listFromFilesystem] Security error: path traversal detected in path '%s': %v\n", path, err)
		return []map[string]interface{}{}
	}

	entries, err := os.ReadDir(fullPath)
	if err != nil {
		return []map[string]interface{}{}
	}

	items := []map[string]interface{}{}
	for _, entry := range entries {
		info, _ := entry.Info()
		itemType := "file"
		if entry.IsDir() {
			itemType = "folder"
		}
		item := map[string]interface{}{
			"name": entry.Name(),
			"type": itemType,
			"path": filepath.Join(path, entry.Name()),
		}
		if info != nil {
			item["size"] = info.Size()
			item["updated_at"] = info.ModTime()
		}
		items = append(items, item)
	}
	return items
}

// searchFiles searches for files by name
func (c *StorageController) SearchFiles(w http.ResponseWriter, r *http.Request) {
	val := r.Context().Value(types.CascataCtxKey)
	if val == nil {
		http.Error(w, `{"error":"Context Lost"}`, 500)
		return
	}
	ctx := val.(*types.CascataRequest)

	query := r.URL.Query().Get("q")
	bucket := r.URL.Query().Get("bucket")

	items, err := services.SearchStorageObjects(r.Context(), services.SystemPool, ctx.Project.Slug, query, bucket)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"items": []interface{}{}})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"items": items})
}

// syncBucket triggers a sync of local bucket to database
func (c *StorageController) SyncBucket(w http.ResponseWriter, r *http.Request) {
	val := r.Context().Value(types.CascataCtxKey)
	if val == nil {
		http.Error(w, `{"error":"Context Lost"}`, 500)
		return
	}
	ctx := val.(*types.CascataRequest)

	bucket := chi.URLParam(r, "bucket")

	storageRoot := os.Getenv("STORAGE_ROOT")
	if storageRoot == "" {
		storageRoot = "./storage"
	}

	// Run sync in background
	go func() {
		services.SyncLocalBucket(context.Background(), services.SystemPool, ctx.Project.Slug, bucket, storageRoot)
		services.InvalidateProjectStorageUsage(ctx.Project.Slug)
	}()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Synchronization started in background",
	})
}

// serveFile serves a file from local storage
func (c *StorageController) ServeFile(w http.ResponseWriter, r *http.Request) {
	val := r.Context().Value(types.CascataCtxKey)
	if val == nil {
		http.Error(w, `{"error":"Context Lost"}`, 500)
		return
	}
	ctx := val.(*types.CascataRequest)

	bucket := chi.URLParam(r, "bucket")
	if bucket == "" {
		bucket = "default"
	}

	// Get path from URL wildcard
	relativePath := chi.URLParam(r, "*")

	// Get storage config
	storageConfig := services.ResolveStorageConfig(metadataToMap(ctx.Project.Metadata), "")

	if storageConfig.Provider != services.ProviderLocal {
		http.Error(w, `{"error":"File is hosted externally. Use direct links."}`, 404)
		return
	}

	storageRoot := os.Getenv("STORAGE_ROOT")
	if storageRoot == "" {
		storageRoot = "./storage"
	}

	bucketSegments := append(c.getProjectStorageSegments(ctx), bucket)
	bucketPath, _ := c.storageSvc.GetSafePath(storageRoot, bucketSegments...)
	filePath, err := c.storageSvc.GetSafePath(bucketPath, relativePath)
	if err != nil {
		http.Error(w, `{"error":"Access Denied"}`, 403)
		return
	}

	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		http.Error(w, `{"error":"File Not Found"}`, 404)
		return
	}

	http.ServeFile(w, r, filePath)
}

// moveFiles moves files between buckets/paths
func (c *StorageController) MoveFiles(w http.ResponseWriter, r *http.Request) {
	val := r.Context().Value(types.CascataCtxKey)
	if val == nil {
		http.Error(w, `{"error":"Context Lost"}`, 500)
		return
	}
	ctx := val.(*types.CascataRequest)

	storageConfig := services.ResolveStorageConfig(metadataToMap(ctx.Project.Metadata), "")
	if storageConfig.Provider != services.ProviderLocal {
		http.Error(w, `{"error":"Move operation not supported for external providers"}`, 501)
		return
	}

	var body struct {
		Bucket      string   `json:"bucket"`
		Paths       []string `json:"paths"`
		Destination struct {
			Bucket string `json:"bucket"`
			Path   string `json:"path"`
		} `json:"destination"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"Invalid request body"}`, 400)
		return
	}

	storageRoot := os.Getenv("STORAGE_ROOT")
	if storageRoot == "" {
		storageRoot = "./storage"
	}

	projectRoot, _ := c.storageSvc.GetSafePath(storageRoot, c.getProjectStorageSegments(ctx)...)
	destPath, _ := c.storageSvc.GetSafePath(projectRoot, body.Destination.Bucket, body.Destination.Path)
	os.MkdirAll(destPath, 0750)  // CORREÇÃO: world não-readable

	// Pre-calcular bucketRoot uma vez só (segurança + performance)
	bucketRoot, err := c.storageSvc.GetSafePath(projectRoot, body.Bucket)
	if err != nil {
		http.Error(w, `{"error":"Invalid bucket"}`, 400)
		return
	}

	movedCount := 0
	for _, itemPath := range body.Paths {
		// CORREÇÃO: Usar GetSafePath para sanitizar itemPath e prevenir path traversal
		source, err := c.storageSvc.GetSafePath(bucketRoot, itemPath)
		if err != nil {
			fmt.Printf("[MoveFiles] Security error: path traversal detected in source: %s, error: %v\n", itemPath, err)
			continue
		}
		
		target, err := c.storageSvc.GetSafePath(destPath, filepath.Base(itemPath))
		if err != nil {
			fmt.Printf("[MoveFiles] Security error: path traversal detected in target: %s, error: %v\n", itemPath, err)
			continue
		}

		// Verificação adicional: garantir que source existe e está dentro do projeto
		if _, err := os.Stat(source); os.IsNotExist(err) {
			fmt.Printf("[MoveFiles] Source not found: %s\n", source)
			continue
		}

		if err := os.Rename(source, target); err == nil {
			// Update index
			newRelPath := filepath.Join(body.Destination.Path, filepath.Base(itemPath))
			newRelPath = strings.ReplaceAll(newRelPath, "\\", "/")

			if err := services.UnindexObject(r.Context(), services.SystemPool, ctx.Project.Slug, body.Bucket, itemPath); err != nil {
				fmt.Printf("Warning: failed to unindex moved object: %v\n", err)
			}

			// Get new file info
			if info, err := os.Stat(target); err == nil {
				if err := services.IndexObject(r.Context(), services.SystemPool, ctx.Project.Slug, body.Destination.Bucket, newRelPath, map[string]interface{}{
					"size":     info.Size(),
					"mimeType": "application/octet-stream",
					"isFolder": info.IsDir(),
					"provider": "local",
				}); err != nil {
					fmt.Printf("Warning: failed to index moved object: %v\n", err)
				}
			}

			movedCount++
		}
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"moved":   movedCount,
	})
}

// deleteObject deletes a file or folder
func (c *StorageController) DeleteObject(w http.ResponseWriter, r *http.Request) {
	val := r.Context().Value(types.CascataCtxKey)
	if val == nil {
		http.Error(w, `{"error":"Context Lost"}`, 500)
		return
	}
	ctx := val.(*types.CascataRequest)

	bucket := chi.URLParam(r, "bucket")
	if bucket == "" {
		bucket = "default"
	}

	objectPath := r.URL.Query().Get("path")
	if objectPath == "" || objectPath == "." || objectPath == ".." {
		http.Error(w, `{"error":"Invalid object path structure"}`, 400)
		return
	}

	storageConfig := services.ResolveStorageConfig(metadataToMap(ctx.Project.Metadata), "")

	branchName := types.GetBranchName(r.Context())

	if storageConfig.Provider == services.ProviderLocal {
		storageRoot := os.Getenv("STORAGE_ROOT")
		if storageRoot == "" {
			storageRoot = "./storage"
		}

		bucketSegments := append(c.getProjectStorageSegments(ctx), bucket)
		bucketRoot, _ := c.storageSvc.GetSafePath(storageRoot, bucketSegments...)
		filePath, err := c.storageSvc.GetSafePath(bucketRoot, objectPath)
		if err != nil {
			http.Error(w, `{"error":"Access Denied"}`, 403)
			return
		}

		os.RemoveAll(filePath)
	} else {
		// External provider delete
		key := filepath.Join(bucket, objectPath)
		if branchName != "main" {
			key = filepath.Join("branches", branchName, key)
		}
		key = strings.ReplaceAll(key, "\\", "/")
		go c.storageSvc.Delete(context.Background(), key, storageConfig)
	}

	// Unindex
	services.UnindexObject(r.Context(), services.SystemPool, ctx.Project.Slug, bucket, objectPath)

	// Invalidate quota cache
	services.InvalidateProjectStorageUsage(ctx.Project.Slug)

	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// QuotaCheckResult holds quota check result
type QuotaCheckResult struct {
	Allowed       bool
	ReservationID string
}

// checkQuota checks storage quota with Dragonfly cache
func (c *StorageController) checkQuota(projectSlug string, incomingSize int64, limitStr, provider string) QuotaCheckResult {
	// FAIL OPEN (Safety Net): If quota check fails, allow small uploads (50MB)
	defer func() {
		if r := recover(); r != nil {
			// Panic occurred - this will be handled by returning safe result
		}
	}()

	// Get limit
	limit := services.ParseBytes(limitStr)
	if limit == 0 {
		limit = 1 << 30 // 1GB default
	}

	var currentUsage int64

	// Check Dragonfly cache
	cachedUsage := services.GetProjectStorageUsage(projectSlug)

	if cachedUsage != -1 {
		currentUsage = cachedUsage
	} else {
		// Cache Miss: Use Logical Sum (DB) as Source of Truth
		var logicalSize int64
		err := services.SystemPool.QueryRow(context.Background(),
			"SELECT COALESCE(SUM(size), 0) FROM system.storage_objects WHERE project_slug = $1",
			projectSlug).Scan(&logicalSize)
		if err != nil {
			// FAIL OPEN: On DB error, allow small uploads
			return QuotaCheckResult{Allowed: incomingSize < 50*1024*1024}
		}

		// Physical Check (Optional Sanity Check for Local Provider)
		if provider == "local" {
			storageRoot := os.Getenv("STORAGE_ROOT")
			if storageRoot == "" {
				storageRoot = "./storage"
			}
			physicalSize, _ := c.storageSvc.GetPhysicalDiskUsage(projectSlug, storageRoot)
			if physicalSize > logicalSize {
				logicalSize = physicalSize
			}
		}

		currentUsage = logicalSize
		services.SetProjectStorageUsage(projectSlug, currentUsage, time.Hour)
	}

	// Check reserved space
	reserved := services.GetReservedStorage(context.Background(), projectSlug)
	totalProjected := currentUsage + reserved + incomingSize

	if totalProjected > limit {
		return QuotaCheckResult{Allowed: false}
	}

	// Reserve space
	resID := services.ReserveStorage(context.Background(), projectSlug, incomingSize, 10*time.Minute)

	return QuotaCheckResult{Allowed: true, ReservationID: resID}
}

// CheckQuotaSafe checks quota with FAIL OPEN safety (returns true for small uploads if check fails)
func CheckQuotaSafe(projectSlug string, incomingSize int64) bool {
	// FAIL OPEN: If anything fails, allow uploads under 50MB as safety net
	return incomingSize < 50*1024*1024
}

// getGovernance extracts governance config from project metadata
func (c *StorageController) getGovernance(metadata map[string]interface{}) map[string]services.GovernanceRule {
	result := make(map[string]services.GovernanceRule)

	// Default global rule com extensoes seguras comuns
	// CORREÇÃO: Adicionar defaults seguros para nao bloquear tudo quando nao ha config
	result["global"] = services.GovernanceRule{
		MaxSize:       "100MB",
		MaxSizeDirect: "5GB",
		AllowedExts: []string{
			"jpg", "jpeg", "png", "gif", "webp", "svg", "bmp", "ico", // Visual
			"mp4", "mov", "webm", "avi", "mkv", // Motion
			"mp3", "wav", "ogg", "m4a", "flac", // Audio
			"pdf", "doc", "docx", "odt", "rtf", "txt", // Docs
			"csv", "json", "xml", "yaml", "xls", "xlsx", // Structured
			"zip", "rar", "7z", "tar", "gz", // Archives
		},
	}
	fmt.Printf("[Governance Debug] Default global rule: MaxSize=%s, AllowedExts=%v\n", 
		result["global"].MaxSize, result["global"].AllowedExts)

	if govData, ok := metadata["storage_governance"].(map[string]interface{}); ok {
		fmt.Printf("[Governance Debug] Found storage_governance with %d sectors\n", len(govData))
		for sector, data := range govData {
			if ruleMap, ok := data.(map[string]interface{}); ok {
				rule := services.GovernanceRule{}
				
				// max_size pode vir como string ou número
				if maxSize, ok := ruleMap["max_size"].(string); ok {
					rule.MaxSize = maxSize
				} else if maxSizeNum, ok := ruleMap["max_size"].(float64); ok {
					rule.MaxSize = fmt.Sprintf("%.0fMB", maxSizeNum)
					fmt.Printf("[Governance Debug] Sector '%s': max_size era número %.0f, convertido para %s\n", 
						sector, maxSizeNum, rule.MaxSize)
				} else if maxSize != "" {
					fmt.Printf("[Governance Debug] Sector '%s': max_size tipo inesperado: %T, valor: %v\n", 
						sector, ruleMap["max_size"], ruleMap["max_size"])
				}
				
				// max_size_direct pode vir como string ou número
				if maxSizeDirect, ok := ruleMap["max_size_direct"].(string); ok {
					rule.MaxSizeDirect = maxSizeDirect
				} else if maxSizeDirectNum, ok := ruleMap["max_size_direct"].(float64); ok {
					rule.MaxSizeDirect = fmt.Sprintf("%.0fGB", maxSizeDirectNum)
					fmt.Printf("[Governance Debug] Sector '%s': max_size_direct era número %.0f, convertido para %s\n", 
						sector, maxSizeDirectNum, rule.MaxSizeDirect)
				}
				if exts, ok := ruleMap["allowed_exts"].([]interface{}); ok {
					for _, ext := range exts {
						if extStr, ok := ext.(string); ok {
							rule.AllowedExts = append(rule.AllowedExts, extStr)
						}
					}
				}
				result[sector] = rule
				fmt.Printf("[Governance Debug] Sector '%s': MaxSize=%s, AllowedExts=%v\n", 
					sector, rule.MaxSize, rule.AllowedExts)
			}
		}
	} else {
		fmt.Printf("[Governance Debug] No storage_governance found in metadata, using defaults\n")
	}

	return result
}

// metadataToMap converte ProjectMetadata para map[string]interface{}
func metadataToMap(metadata types.ProjectMetadata) map[string]interface{} {
	result := make(map[string]interface{})
	
	// Converter campos conhecidos
	result["timezone"] = metadata.Timezone
	result["allowed_origins"] = metadata.AllowedOrigins
	result["draft_sync_active"] = metadata.DraftSyncActive
	result["masked_columns"] = metadata.MaskedColumns
	result["locked_columns"] = metadata.LockedColumns
	result["computed_columns"] = metadata.ComputedColumns
	result["secrets"] = metadata.Secrets
	result["storage_config"] = metadata.Extra["storage_config"]
	result["storage_governance"] = metadata.Extra["storage_governance"]
	result["storage_limit"] = metadata.Extra["storage_limit"]
	
	// Copiar todos os campos de Extra
	for k, v := range metadata.Extra {
		if _, exists := result[k]; !exists {
			result[k] = v
		}
	}
	
	return result
}

// Alias methods for backward compatibility
func (c *StorageController) ListBucketContents(w http.ResponseWriter, r *http.Request) {
	c.ListFiles(w, r)
}

func (c *StorageController) SignUpload(w http.ResponseWriter, r *http.Request) {
	c.signUpload(w, r)
}

func (c *StorageController) GetFile(w http.ResponseWriter, r *http.Request) {
	c.ServeFile(w, r)
}

func (c *StorageController) DeleteFile(w http.ResponseWriter, r *http.Request) {
	c.DeleteObject(w, r)
}

func (c *StorageController) getProjectStorageSegments(ctx *types.CascataRequest) []string {
	branch := "main"
	if ctx.TargetEnv != "live" && ctx.TargetEnv != "" {
		branch = ctx.TargetEnv
	}
	if branch == "main" {
		return []string{ctx.Project.Slug}
	}
	return []string{ctx.Project.Slug, "branches", branch}
}

func (c *StorageController) getProjectStoragePath(storageRoot string, ctx *types.CascataRequest) string {
	segments := c.getProjectStorageSegments(ctx)
	return filepath.Join(append([]string{storageRoot}, segments...)...)
}

