package services

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// UploadWorker processes async upload jobs
type UploadWorker struct {
	storageSvc *StorageService
	ctx        context.Context
	cancel     context.CancelFunc
	isRunning  bool
}

// NewUploadWorker creates a new upload worker
func NewUploadWorker() *UploadWorker {
	return &UploadWorker{
		storageSvc: NewStorageService(),
	}
}

// Start begins the upload worker loop
func (w *UploadWorker) Start() {
	if w.isRunning {
		return
	}

	w.ctx, w.cancel = context.WithCancel(context.Background())
	w.isRunning = true

	fmt.Println("[UploadWorker] Starting async upload processor...")

	// Start the main processing loop
	go w.processLoop()

	// Start cleanup job (runs every hour)
	go w.cleanupLoop()
}

// Stop halts the upload worker
func (w *UploadWorker) Stop() {
	if !w.isRunning {
		return
	}

	fmt.Println("[UploadWorker] Stopping...")
	w.cancel()
	w.isRunning = false
}

func (w *UploadWorker) processLoop() {
	ticker := time.NewTicker(1 * time.Second) // Check for jobs every second
	defer ticker.Stop()

	// CORREÇÃO: Verificar jobs stale a cada 30 segundos
	staleCheckTicker := time.NewTicker(30 * time.Second)
	defer staleCheckTicker.Stop()

	for {
		select {
		case <-w.ctx.Done():
			return
		case <-ticker.C:
			w.processNextJob()
		case <-staleCheckTicker.C:
			// CORREÇÃO: Requeue jobs presos em processing por mais de 5 minutos
			if err := RequeueStaleJobs(w.ctx, 5*time.Minute); err != nil {
				fmt.Printf("[UploadWorker] Error requeueing stale jobs: %v\n", err)
			}
		}
	}
}

func (w *UploadWorker) cleanupLoop() {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-w.ctx.Done():
			return
		case <-ticker.C:
			// Clean up jobs older than 7 days
			CleanupOldUploadJobs(w.ctx, 7*24*time.Hour)
			// CORREÇÃO: Limpar temp files órfãos (mais de 24h)
			w.cleanupOrphanTempFiles()
		}
	}
}

// cleanupOrphanTempFiles removes temp files not associated with pending/processing jobs
// CORREÇÃO: Garantir que temp files de jobs crashados sejam limpos
func (w *UploadWorker) cleanupOrphanTempFiles() {
	tempDir := os.Getenv("TEMP_UPLOAD_ROOT")
	if tempDir == "" {
		storageRoot := os.Getenv("STORAGE_ROOT")
		if storageRoot == "" {
			storageRoot = "./storage"
		}
		tempDir = filepath.Join(storageRoot, ".temp")
	}

	// Listar todos os temp files
	entries, err := os.ReadDir(tempDir)
	if err != nil {
		fmt.Printf("[UploadWorker] Failed to read temp dir: %v\n", err)
		return
	}

	// CORREÇÃO: Limpar temp files com mais de 24h (órfãos de jobs crashados)
	cutoff := time.Now().Add(-24 * time.Hour)
	cleaned := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		
		info, err := entry.Info()
		if err != nil {
			continue
		}
		
		// Remover temp files com mais de 24h
		if info.ModTime().Before(cutoff) {
			path := filepath.Join(tempDir, entry.Name())
			if err := os.Remove(path); err == nil {
				cleaned++
				fmt.Printf("[UploadWorker] Cleaned orphan temp file: %s\n", entry.Name())
			}
		}
	}
	
	if cleaned > 0 {
		fmt.Printf("[UploadWorker] Cleaned %d orphan temp files\n", cleaned)
	}
}

func (w *UploadWorker) processNextJob() {
	// Try to get a job from the queue
	job, err := DequeueUpload(w.ctx)
	if err != nil {
		fmt.Printf("[UploadWorker] Error dequeuing job: %v\n", err)
		return
	}
	if job == nil {
		return // No jobs available
	}

	fmt.Printf("[UploadWorker] Processing job %s: %s/%s (%d bytes)\n",
		job.ID, job.ProjectSlug, job.FileName, job.FileSize)

	// CORREÇÃO: Cleanup garantido mesmo em crash/panic
	tempCleaned := false
	cleanup := func() {
		if !tempCleaned && job.TempPath != "" {
			os.Remove(job.TempPath)
			tempCleaned = true
		}
	}
	defer cleanup()

	// CORREÇÃO: Verificar se temp file ainda existe (pode ter sido limpo por crash anterior)
	if _, err := os.Stat(job.TempPath); os.IsNotExist(err) {
		FailUpload(w.ctx, job.ID, "temp file missing (already processed or cleaned)")
		return
	}

	// Process the upload based on storage configuration
	if err := w.processJob(job); err != nil {
		FailUpload(w.ctx, job.ID, err.Error())
		cleanup()
	} else {
		CompleteUpload(w.ctx, job.ID)
		cleanup()
	}
}

func (w *UploadWorker) processJob(job *UploadJob) error {
	// Get project storage configuration
	// For now, default to local filesystem
	// TODO: Load project-specific storage config from DB

	if job.TempPath == "" {
		return fmt.Errorf("no temp file path provided")
	}

	// Open temp file
	tempFile, err := os.Open(job.TempPath)
	if err != nil {
		return fmt.Errorf("failed to open temp file: %w", err)
	}
	defer tempFile.Close()

	// Determine final destination
	storageRoot := os.Getenv("STORAGE_ROOT")
	if storageRoot == "" {
		storageRoot = "/cascata/storage"
	}

	safePath, err := w.storageSvc.GetSafePath(storageRoot, job.ProjectSlug, job.Bucket, job.TargetPath)
	if err != nil {
		return fmt.Errorf("invalid path: %w", err)
	}

	// Ensure directory exists
	if err := os.MkdirAll(safePath, 0750); err != nil {  // CORREÇÃO: world não-readable
		return fmt.Errorf("failed to create directory: %w", err)
	}

	// Create final file
	destPath := safePath + "/" + job.FileName
	destFile, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("failed to create destination file: %w", err)
	}
	defer destFile.Close()

	// Copy with progress tracking (streaming, no memory buffer)
	written, err := io.Copy(destFile, tempFile)
	if err != nil {
		os.Remove(destPath) // Cleanup on error
		return fmt.Errorf("failed to copy file: %w", err)
	}

	// Index in database
	_, err = SystemPool.Exec(w.ctx, `
		INSERT INTO system.storage_objects (project_slug, bucket, name, parent_path, full_path, is_folder, size, mime_type)
		VALUES ($1, $2, $3, $4, $5, false, $6, $7)
		ON CONFLICT (project_slug, bucket, full_path) DO UPDATE SET 
			size = $6,
			updated_at = NOW()
	`, job.ProjectSlug, job.Bucket, job.FileName, job.TargetPath, 
		job.TargetPath+"/"+job.FileName, written, job.ContentType)

	if err != nil {
		fmt.Printf("[UploadWorker] Warning: failed to index file: %v\n", err)
		// Don't fail the upload, just log the warning
	}

	// Invalidate quota cache
	InvalidateProjectStorageUsage(job.ProjectSlug)

	fmt.Printf("[UploadWorker] Successfully uploaded %s (%d bytes)\n", job.FileName, written)
	return nil
}

// IsRunning returns whether the worker is active
func (w *UploadWorker) IsRunning() bool {
	return w.isRunning
}
