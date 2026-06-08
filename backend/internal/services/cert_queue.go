package services

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/redis/go-redis/v9"
)

// CertTaskType represents the type of certificate operation
type CertTaskType string

const (
	TaskIssue     CertTaskType = "issue"
	TaskRenew     CertTaskType = "renew"
	TaskDelete    CertTaskType = "delete"
	TaskReload    CertTaskType = "reload"
)

// CertTask represents a certificate operation task
type CertTask struct {
	ID        string       `json:"id"`
	Type      CertTaskType `json:"type"`
	Domain    string       `json:"domain"`
	Email     string       `json:"email,omitempty"`
	Provider  string       `json:"provider,omitempty"`
	Cert      string       `json:"cert,omitempty"`
	Key       string       `json:"key,omitempty"`
	Status    string       `json:"status"`
	Message   string       `json:"message,omitempty"`
	CreatedAt time.Time    `json:"created_at"`
	UpdatedAt time.Time    `json:"updated_at"`
}

const (
	CertQueueKey      = "cert:queue:pending"
	CertProcessingKey = "cert:queue:processing"
	CertCompletedKey  = "cert:queue:completed"
	CertFailedKey     = "cert:queue:failed"
	TaskPrefix        = "cert:task:"
)

// CertQueueService manages certificate task queue using Redis/Dragonfly
type CertQueueService struct {
	redis *redis.Client
}

// NewCertQueueService creates a new certificate queue service
func NewCertQueueService(redisAddr string) *CertQueueService {
	rdb := redis.NewClient(&redis.Options{
		Addr:     redisAddr,
		Password: "", // No password by default
		DB:       0,
	})

	return &CertQueueService{redis: rdb}
}

// GetDragonflyClient returns the shared dragonfly client
func GetDragonflyClient() *redis.Client {
	return GetDragonfly()
}

// EnqueueTask adds a new certificate task to the queue
func (s *CertQueueService) EnqueueTask(ctx context.Context, task *CertTask) error {
	task.ID = fmt.Sprintf("task_%d", time.Now().UnixNano())
	task.Status = "pending"
	task.CreatedAt = time.Now()
	task.UpdatedAt = time.Now()

	taskJSON, err := json.Marshal(task)
	if err != nil {
		return fmt.Errorf("failed to marshal task: %w", err)
	}

	// Store task details
	taskKey := TaskPrefix + task.ID
	if err := s.redis.Set(ctx, taskKey, taskJSON, 24*time.Hour).Err(); err != nil {
		return fmt.Errorf("failed to store task: %w", err)
	}

	// Add to pending queue
	if err := s.redis.LPush(ctx, CertQueueKey, task.ID).Err(); err != nil {
		return fmt.Errorf("failed to enqueue task: %w", err)
	}

	log.Printf("[CertQueue] Task enqueued: %s (type: %s, domain: %s)", task.ID, task.Type, task.Domain)
	return nil
}

// DequeueTask retrieves and marks a task as processing
func (s *CertQueueService) DequeueTask(ctx context.Context) (*CertTask, error) {
	// Block and wait for task (with timeout)
	result, err := s.redis.BRPopLPush(ctx, CertQueueKey, CertProcessingKey, 5*time.Second).Result()
	if err == redis.Nil {
		return nil, nil // No task available
	}
	if err != nil {
		return nil, fmt.Errorf("failed to dequeue task: %w", err)
	}

	taskID := result
	taskKey := TaskPrefix + taskID

	// Get task details
	taskJSON, err := s.redis.Get(ctx, taskKey).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to get task details: %w", err)
	}

	var task CertTask
	if err := json.Unmarshal([]byte(taskJSON), &task); err != nil {
		return nil, fmt.Errorf("failed to unmarshal task: %w", err)
	}

	task.Status = "processing"
	task.UpdatedAt = time.Now()

	// Update task status
	updatedJSON, _ := json.Marshal(task)
	s.redis.Set(ctx, taskKey, updatedJSON, 24*time.Hour)

	log.Printf("[CertQueue] Task dequeued: %s (type: %s, domain: %s)", task.ID, task.Type, task.Domain)
	return &task, nil
}

// CompleteTask marks a task as completed
func (s *CertQueueService) CompleteTask(ctx context.Context, taskID string, message string) error {
	taskKey := TaskPrefix + taskID

	taskJSON, err := s.redis.Get(ctx, taskKey).Result()
	if err != nil {
		return fmt.Errorf("failed to get task: %w", err)
	}

	var task CertTask
	if err := json.Unmarshal([]byte(taskJSON), &task); err != nil {
		return fmt.Errorf("failed to unmarshal task: %w", err)
	}

	task.Status = "completed"
	task.Message = message
	task.UpdatedAt = time.Now()

	updatedJSON, _ := json.Marshal(task)
	if err := s.redis.Set(ctx, taskKey, updatedJSON, 24*time.Hour).Err(); err != nil {
		return err
	}

	// Remove from processing
	s.redis.LRem(ctx, CertProcessingKey, 0, taskID)

	// Add to completed
	s.redis.LPush(ctx, CertCompletedKey, taskID)
	s.redis.LTrim(ctx, CertCompletedKey, 0, 99) // Keep only last 100

	log.Printf("[CertQueue] Task completed: %s - %s", taskID, message)
	return nil
}

// FailTask marks a task as failed
func (s *CertQueueService) FailTask(ctx context.Context, taskID string, errMsg string) error {
	taskKey := TaskPrefix + taskID

	taskJSON, err := s.redis.Get(ctx, taskKey).Result()
	if err != nil {
		return fmt.Errorf("failed to get task: %w", err)
	}

	var task CertTask
	if err := json.Unmarshal([]byte(taskJSON), &task); err != nil {
		return fmt.Errorf("failed to unmarshal task: %w", err)
	}

	task.Status = "failed"
	task.Message = errMsg
	task.UpdatedAt = time.Now()

	updatedJSON, _ := json.Marshal(task)
	if err := s.redis.Set(ctx, taskKey, updatedJSON, 24*time.Hour).Err(); err != nil {
		return err
	}

	// Remove from processing
	s.redis.LRem(ctx, CertProcessingKey, 0, taskID)

	// Add to failed
	s.redis.LPush(ctx, CertFailedKey, taskID)
	s.redis.LTrim(ctx, CertFailedKey, 0, 99) // Keep only last 100

	log.Printf("[CertQueue] Task failed: %s - %s", taskID, errMsg)
	return nil
}

// GetTaskStatus retrieves the status of a specific task
func (s *CertQueueService) GetTaskStatus(ctx context.Context, taskID string) (*CertTask, error) {
	taskKey := TaskPrefix + taskID

	taskJSON, err := s.redis.Get(ctx, taskKey).Result()
	if err == redis.Nil {
		return nil, fmt.Errorf("task not found")
	}
	if err != nil {
		return nil, err
	}

	var task CertTask
	if err := json.Unmarshal([]byte(taskJSON), &task); err != nil {
		return nil, err
	}

	return &task, nil
}

// ListPendingTasks returns all pending tasks
func (s *CertQueueService) ListPendingTasks(ctx context.Context) ([]string, error) {
	return s.redis.LRange(ctx, CertQueueKey, 0, -1).Result()
}

// RequeueStaleTasks moves stale processing tasks back to pending
func (s *CertQueueService) RequeueStaleTasks(ctx context.Context, maxAge time.Duration) error {
	taskIDs, err := s.redis.LRange(ctx, CertProcessingKey, 0, -1).Result()
	if err != nil {
		return err
	}

	for _, taskID := range taskIDs {
		taskKey := TaskPrefix + taskID
		taskJSON, err := s.redis.Get(ctx, taskKey).Result()
		if err != nil {
			continue
		}

		var task CertTask
		if err := json.Unmarshal([]byte(taskJSON), &task); err != nil {
			continue
		}

		// If task is processing for too long, requeue it
		if time.Since(task.UpdatedAt) > maxAge {
			s.redis.LRem(ctx, CertProcessingKey, 0, taskID)
			s.redis.LPush(ctx, CertQueueKey, taskID)
			task.Status = "pending"
			task.UpdatedAt = time.Now()
			updatedJSON, _ := json.Marshal(task)
			s.redis.Set(ctx, taskKey, updatedJSON, 24*time.Hour)
			log.Printf("[CertQueue] Requeued stale task: %s", taskID)
		}
	}

	return nil
}
