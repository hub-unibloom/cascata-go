package services

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// UploadJob represents an async upload job
type UploadJob struct {
	ID          string    `json:"id"`
	ProjectSlug string    `json:"project_slug"`
	Bucket      string    `json:"bucket"`
	FileName    string    `json:"file_name"`
	FileSize    int64     `json:"file_size"`
	ContentType string    `json:"content_type"`
	TempPath    string    `json:"temp_path"`
	TargetPath  string    `json:"target_path"`
	Status      string    `json:"status"` // pending, processing, completed, failed
	Error       string    `json:"error,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

const (
	UploadQueueKey     = "queue:uploads:pending"
	UploadProcessingKey = "queue:uploads:processing"
	UploadCompletedKey = "queue:uploads:completed"
	UploadFailedKey    = "queue:uploads:failed"
	UploadJobPrefix    = "upload:job:"
	MaxUploadRetries   = 3
)

// EnqueueUpload adds an upload job to the queue
func EnqueueUpload(ctx context.Context, job *UploadJob) error {
	rdb := GetDragonfly()
	if rdb == nil {
		return fmt.Errorf("Dragonfly not available for queue")
	}

	job.ID = fmt.Sprintf("%s-%d", job.ProjectSlug, time.Now().UnixNano())
	job.Status = "pending"
	job.CreatedAt = time.Now()
	job.UpdatedAt = time.Now()

	// Store job details
	jobData, _ := json.Marshal(job)
	if err := rdb.Set(ctx, UploadJobPrefix+job.ID, jobData, 24*time.Hour).Err(); err != nil {
		return fmt.Errorf("failed to store upload job: %w", err)
	}

	// Add to pending queue (sorted by timestamp for FIFO)
	score := float64(time.Now().Unix())
	if err := rdb.ZAdd(ctx, UploadQueueKey, redis.Z{Score: score, Member: job.ID}).Err(); err != nil {
		return fmt.Errorf("failed to enqueue upload: %w", err)
	}

	fmt.Printf("[UploadQueue] Enqueued job %s for %s/%s (%d bytes)\n", 
		job.ID, job.ProjectSlug, job.FileName, job.FileSize)
	return nil
}

// DequeueUpload gets the next pending upload job
func DequeueUpload(ctx context.Context) (*UploadJob, error) {
	rdb := GetDragonfly()
	if rdb == nil {
		return nil, fmt.Errorf("Dragonfly not available")
	}

	// Get oldest pending job
	result, err := rdb.ZPopMin(ctx, UploadQueueKey, 1).Result()
	if err != nil || len(result) == 0 {
		return nil, nil // No jobs available
	}

	jobID := result[0].Member.(string)

	// Get job details
	jobData, err := rdb.Get(ctx, UploadJobPrefix+jobID).Bytes()
	if err != nil {
		return nil, fmt.Errorf("failed to get job details: %w", err)
	}

	var job UploadJob
	if err := json.Unmarshal(jobData, &job); err != nil {
		return nil, fmt.Errorf("failed to unmarshal job: %w", err)
	}

	// Move to processing queue
	job.Status = "processing"
	job.UpdatedAt = time.Now()
	jobData, _ = json.Marshal(job)
	rdb.Set(ctx, UploadJobPrefix+jobID, jobData, 24*time.Hour)
	rdb.ZAdd(ctx, UploadProcessingKey, redis.Z{Score: float64(time.Now().Unix()), Member: jobID})

	return &job, nil
}

// CompleteUpload marks an upload job as completed
func CompleteUpload(ctx context.Context, jobID string) error {
	rdb := GetDragonfly()
	if rdb == nil {
		return fmt.Errorf("Dragonfly not available")
	}

	// Remove from processing
	rdb.ZRem(ctx, UploadProcessingKey, jobID)

	// Update job status
	jobData, err := rdb.Get(ctx, UploadJobPrefix+jobID).Bytes()
	if err != nil {
		return err
	}

	var job UploadJob
	if err := json.Unmarshal(jobData, &job); err != nil {
		return err
	}

	job.Status = "completed"
	job.UpdatedAt = time.Now()
	jobData, _ = json.Marshal(job)

	// Store in completed queue (expire after 7 days)
	rdb.Set(ctx, UploadJobPrefix+jobID, jobData, 7*24*time.Hour)
	rdb.ZAdd(ctx, UploadCompletedKey, redis.Z{Score: float64(time.Now().Unix()), Member: jobID})

	fmt.Printf("[UploadQueue] Completed job %s\n", jobID)
	return nil
}

// FailUpload marks an upload job as failed
func FailUpload(ctx context.Context, jobID string, errMsg string) error {
	rdb := GetDragonfly()
	if rdb == nil {
		return fmt.Errorf("Dragonfly not available")
	}

	// Remove from processing
	rdb.ZRem(ctx, UploadProcessingKey, jobID)

	// Update job status
	jobData, err := rdb.Get(ctx, UploadJobPrefix+jobID).Bytes()
	if err != nil {
		return err
	}

	var job UploadJob
	if err := json.Unmarshal(jobData, &job); err != nil {
		return err
	}

	job.Status = "failed"
	job.Error = errMsg
	job.UpdatedAt = time.Now()
	jobData, _ = json.Marshal(job)

	// Store in failed queue
	rdb.Set(ctx, UploadJobPrefix+jobID, jobData, 7*24*time.Hour)
	rdb.ZAdd(ctx, UploadFailedKey, redis.Z{Score: float64(time.Now().Unix()), Member: jobID})

	fmt.Printf("[UploadQueue] Failed job %s: %s\n", jobID, errMsg)
	return nil
}

// GetUploadJob retrieves a specific upload job by ID
func GetUploadJob(ctx context.Context, jobID string) (*UploadJob, error) {
	rdb := GetDragonfly()
	if rdb == nil {
		return nil, fmt.Errorf("Dragonfly not available")
	}

	jobData, err := rdb.Get(ctx, UploadJobPrefix+jobID).Bytes()
	if err != nil {
		return nil, err
	}

	var job UploadJob
	if err := json.Unmarshal(jobData, &job); err != nil {
		return nil, err
	}

	return &job, nil
}

// GetQueueStats returns statistics about upload queues
func GetQueueStats(ctx context.Context) map[string]int64 {
	rdb := GetDragonfly()
	if rdb == nil {
		return map[string]int64{
			"pending":    0,
			"processing": 0,
			"completed":  0,
			"failed":     0,
		}
	}

	pending, _ := rdb.ZCard(ctx, UploadQueueKey).Result()
	processing, _ := rdb.ZCard(ctx, UploadProcessingKey).Result()
	completed, _ := rdb.ZCard(ctx, UploadCompletedKey).Result()
	failed, _ := rdb.ZCard(ctx, UploadFailedKey).Result()

	return map[string]int64{
		"pending":    pending,
		"processing": processing,
		"completed":  completed,
		"failed":     failed,
	}
}

// RequeueStaleJobs moves jobs stuck in processing back to pending queue
// CORREÇÃO: Garantir que jobs nao fiquem presos se worker crashar
func RequeueStaleJobs(ctx context.Context, maxProcessingTime time.Duration) error {
	rdb := GetDragonfly()
	if rdb == nil {
		return nil
	}

	cutoff := float64(time.Now().Add(-maxProcessingTime).Unix())

	// Find jobs stuck in processing for too long
	staleJobs, err := rdb.ZRangeByScore(ctx, UploadProcessingKey, &redis.ZRangeBy{
		Min: "0",
		Max: fmt.Sprintf("%f", cutoff),
	}).Result()
	if err != nil {
		return fmt.Errorf("failed to get stale jobs: %w", err)
	}

	requeued := 0
	for _, jobID := range staleJobs {
		// Get job details
		jobData, err := rdb.Get(ctx, UploadJobPrefix+jobID).Bytes()
		if err != nil {
			// Job data missing, just remove from processing
			rdb.ZRem(ctx, UploadProcessingKey, jobID)
			continue
		}

		var job UploadJob
		if err := json.Unmarshal(jobData, &job); err != nil {
			rdb.ZRem(ctx, UploadProcessingKey, jobID)
			continue
		}

		// Check retry count
		retryCount, _ := rdb.Get(ctx, UploadJobPrefix+job.ID+":retries").Int()
		if retryCount >= MaxUploadRetries {
			// Max retries reached, mark as failed
			job.Status = "failed"
			job.Error = "max retries exceeded (worker crash)"
			job.UpdatedAt = time.Now()
			jobData, _ = json.Marshal(job)
			rdb.Set(ctx, UploadJobPrefix+jobID, jobData, 24*time.Hour)
			rdb.ZRem(ctx, UploadProcessingKey, jobID)
			rdb.ZAdd(ctx, UploadFailedKey, redis.Z{Score: float64(time.Now().Unix()), Member: jobID})
			fmt.Printf("[UploadQueue] Job %s marked as failed (max retries)\n", jobID)
			continue
		}

		// Increment retry count
		rdb.Incr(ctx, UploadJobPrefix+job.ID+":retries")
		rdb.Expire(ctx, UploadJobPrefix+job.ID+":retries", 24*time.Hour)

		// Requeue job
		job.Status = "pending"
		job.UpdatedAt = time.Now()
		jobData, _ = json.Marshal(job)
		rdb.Set(ctx, UploadJobPrefix+jobID, jobData, 24*time.Hour)

		// Remove from processing and add back to pending
		rdb.ZRem(ctx, UploadProcessingKey, jobID)
		score := float64(time.Now().Unix())
		rdb.ZAdd(ctx, UploadQueueKey, redis.Z{Score: score, Member: jobID})

		requeued++
		fmt.Printf("[UploadQueue] Requeued stale job %s (retry %d/%d)\n", 
			jobID, retryCount+1, MaxUploadRetries)
	}

	if requeued > 0 {
		fmt.Printf("[UploadQueue] Requeued %d stale jobs\n", requeued)
	}
	return nil
}

// CleanupOldUploadJobs removes completed/failed jobs older than retention period
func CleanupOldUploadJobs(ctx context.Context, maxAge time.Duration) error {
	rdb := GetDragonfly()
	if rdb == nil {
		return nil
	}

	cutoff := float64(time.Now().Add(-maxAge).Unix())

	// Remove old completed jobs
	completedJobs, _ := rdb.ZRangeByScore(ctx, UploadCompletedKey, &redis.ZRangeBy{
		Min: "0",
		Max: fmt.Sprintf("%f", cutoff),
	}).Result()

	for _, jobID := range completedJobs {
		rdb.Del(ctx, UploadJobPrefix+jobID)
		rdb.ZRem(ctx, UploadCompletedKey, jobID)
	}

	// Remove old failed jobs
	failedJobs, _ := rdb.ZRangeByScore(ctx, UploadFailedKey, &redis.ZRangeBy{
		Min: "0",
		Max: fmt.Sprintf("%f", cutoff),
	}).Result()

	for _, jobID := range failedJobs {
		rdb.Del(ctx, UploadJobPrefix+jobID)
		rdb.ZRem(ctx, UploadFailedKey, jobID)
	}

	fmt.Printf("[UploadQueue] Cleaned up %d completed and %d failed jobs\n", 
		len(completedJobs), len(failedJobs))
	return nil
}
