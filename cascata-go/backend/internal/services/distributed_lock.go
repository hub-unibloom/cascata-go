package services

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// DistributedLock provides distributed locking using Dragonfly (Redis)
// This ensures atomic operations across multiple workers
// CRITICAL: Prevents race conditions in storage operations

type DistributedLock struct {
	key    string
	value  string
	ttl    time.Duration
	client *redis.Client
}

// lockValue generates a unique value for this lock instance
func generateLockValue() string {
	return fmt.Sprintf("lock:%d:%d", time.Now().UnixNano(), time.Now().UnixMicro())
}

// AcquireLock attempts to acquire a distributed lock
// Returns the lock if successful, nil if lock is already held
func AcquireLock(ctx context.Context, lockKey string, ttl time.Duration) *DistributedLock {
	rdb := GetDragonfly()
	if rdb == nil {
		// Dragonfly not available - operation proceeds without lock (degraded mode)
		return &DistributedLock{
			key:    lockKey,
			value:  "no-dragonfly",
			ttl:    ttl,
			client: nil,
		}
	}

	value := generateLockValue()
	
	// Try to acquire lock using SET NX (Not Exists)
	acquired, err := rdb.SetNX(ctx, lockKey, value, ttl).Result()
	if err != nil || !acquired {
		return nil // Lock already held by another worker
	}

	return &DistributedLock{
		key:    lockKey,
		value:  value,
		ttl:    ttl,
		client: rdb,
	}
}

// AcquireLockWithRetry attempts to acquire lock with retries
// Useful for operations that must succeed
func AcquireLockWithRetry(ctx context.Context, lockKey string, ttl time.Duration, maxRetries int, retryDelay time.Duration) *DistributedLock {
	for i := 0; i < maxRetries; i++ {
		lock := AcquireLock(ctx, lockKey, ttl)
		if lock != nil {
			return lock
		}
		time.Sleep(retryDelay)
	}
	return nil
}

// Release releases the distributed lock
// Uses check-and-delete to ensure we only delete our own lock
func (l *DistributedLock) Release(ctx context.Context) error {
	if l.client == nil || l.value == "no-dragonfly" {
		return nil // No lock to release
	}

	// Use Lua script for atomic check-and-delete
	script := `
		if redis.call("get", KEYS[1]) == ARGV[1] then
			return redis.call("del", KEYS[1])
		else
			return 0
		end
	`

	result, err := l.client.Eval(ctx, script, []string{l.key}, l.value).Result()
	if err != nil {
		return fmt.Errorf("failed to release lock: %w", err)
	}

	if result.(int64) == 0 {
		return fmt.Errorf("lock was not released (owned by another process or expired)")
	}

	return nil
}

// Extend extends the lock TTL
func (l *DistributedLock) Extend(ctx context.Context, additionalTTL time.Duration) error {
	if l.client == nil || l.value == "no-dragonfly" {
		return nil
	}

	// Use Lua script to extend only if we still own the lock
	script := `
		if redis.call("get", KEYS[1]) == ARGV[1] then
			return redis.call("expire", KEYS[1], ARGV[2])
		else
			return 0
		end
	`

	result, err := l.client.Eval(ctx, script, []string{l.key}, l.value, int(additionalTTL.Seconds())).Result()
	if err != nil {
		return fmt.Errorf("failed to extend lock: %w", err)
	}

	if result.(int64) == 0 {
		return fmt.Errorf("cannot extend lock (owned by another process or expired)")
	}

	l.ttl += additionalTTL
	return nil
}

// IsHeld checks if this lock is still held by us
func (l *DistributedLock) IsHeld(ctx context.Context) bool {
	if l.client == nil || l.value == "no-dragonfly" {
		return true // Always consider held in degraded mode
	}

	val, err := l.client.Get(ctx, l.key).Result()
	if err != nil {
		return false
	}

	return val == l.value
}

// --- Storage-specific lock helpers ---

// LockBucketAcquire acquires a lock for bucket operations
func LockBucketAcquire(ctx context.Context, projectSlug, bucketName string) *DistributedLock {
	lockKey := fmt.Sprintf("lock:bucket:%s:%s", projectSlug, bucketName)
	// 30 seconds should be enough for bucket operations
	return AcquireLock(ctx, lockKey, 30*time.Second)
}

// LockBucketAcquireWithRetry acquires bucket lock with retries
func LockBucketAcquireWithRetry(ctx context.Context, projectSlug, bucketName string, maxRetries int) *DistributedLock {
	lockKey := fmt.Sprintf("lock:bucket:%s:%s", projectSlug, bucketName)
	return AcquireLockWithRetry(ctx, lockKey, 30*time.Second, maxRetries, 100*time.Millisecond)
}

// LockFolderAcquire acquires a lock for folder operations within a bucket
func LockFolderAcquire(ctx context.Context, projectSlug, bucketName, folderPath string) *DistributedLock {
	lockKey := fmt.Sprintf("lock:folder:%s:%s:%s", projectSlug, bucketName, folderPath)
	// 15 seconds for folder operations
	return AcquireLock(ctx, lockKey, 15*time.Second)
}

// LockFolderAcquireWithRetry acquires folder lock with retries
func LockFolderAcquireWithRetry(ctx context.Context, projectSlug, bucketName, folderPath string, maxRetries int) *DistributedLock {
	lockKey := fmt.Sprintf("lock:folder:%s:%s:%s", projectSlug, bucketName, folderPath)
	return AcquireLockWithRetry(ctx, lockKey, 15*time.Second, maxRetries, 100*time.Millisecond)
}

// LockStorageOperation acquires a general lock for storage operations on a project
func LockStorageOperation(ctx context.Context, projectSlug, operation string) *DistributedLock {
	lockKey := fmt.Sprintf("lock:storage:%s:%s", projectSlug, operation)
	return AcquireLock(ctx, lockKey, 60*time.Second)
}
