package services

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

const (
	StreamWebhooks    = "cascata-webhooks"
	StreamPush        = "cascata-push"
	StreamRestore     = "cascata-restore"
	QueuePrefix       = "{cascata}stream"
)

// AddWebhookJob adds a webhook delivery job to the stream
func AddWebhookJob(ctx context.Context, data map[string]interface{}) error {
	return AddStreamJob(ctx, StreamWebhooks, data)
}

// AddStreamJob is the base method to push data to a Redis Stream
func AddStreamJob(ctx context.Context, streamName string, data map[string]interface{}) error {
	if dragonfly == nil {
		return fmt.Errorf("Dragonfly not initialized")
	}

	payload, err := json.Marshal(data)
	if err != nil { return err }

	_, err = dragonfly.XAdd(ctx, &redis.XAddArgs{
		Stream: streamName,
		Values: map[string]interface{}{"payload": string(payload), "ts": time.Now().UnixMilli()},
	}).Result()

	return err
}

// InitQueues starts background workers for different task streams
func InitQueues() {
	if dragonfly == nil { return }
	
	ctx := context.Background()
	
	// Create Consumer Groups (Idempotent)
	_ = dragonfly.XGroupCreateMkStream(ctx, StreamWebhooks, "worker-group", "0").Err()
	_ = dragonfly.XGroupCreateMkStream(ctx, StreamPush, "worker-group", "0").Err()
	_ = dragonfly.XGroupCreateMkStream(ctx, StreamRestore, "worker-group", "0").Err()

	// Start Workers
	go RunWorkerLoop(StreamWebhooks, "worker-group", "webhook-worker-1", ProcessWebhookJob)
	go RunWorkerLoop(StreamPush, "worker-group", "push-worker-1", ProcessPushJob)
	
	// Iniciar workers de automação
	InitAutomationQueues()
}

// RunWorkerLoop handles generic stream reading
func RunWorkerLoop(stream, group, consumer string, handler func(map[string]interface{}) error) {
	ctx := context.Background()
	for {
		entries, err := dragonfly.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group:    group,
			Consumer: consumer,
			Streams:  []string{stream, ">"},
			Count:    5,
			Block:    0,
		}).Result()

		if err != nil {
			if !strings.Contains(err.Error(), "NOGROUP") {
				log.Printf("[QueueWorker] Error reading stream %s: %v", stream, err)
			}
			time.Sleep(5 * time.Second)
			continue
		}

		for _, e := range entries {
			for _, msg := range e.Messages {
				var data map[string]interface{}
				if err := json.Unmarshal([]byte(msg.Values["payload"].(string)), &data); err == nil {
					if err := handler(data); err == nil {
						// ACK message
						dragonfly.XAck(ctx, stream, group, msg.ID)
					} else {
						log.Printf("[QueueWorker] Job execution failed in %s: %v", stream, err)
					}
				}
			}
		}
	}
}

// ProcessWebhookJob executes the actual HTTP call
func ProcessWebhookJob(data map[string]interface{}) error {
	targetURL, _ := data["targetUrl"].(string)
	secret, _ := data["secret"].(string)
	payload := data["payload"]

	if targetURL == "" { return nil }

	// TODO: SSRF Check

	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", targetURL, strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Cascata-Signature", secret)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil { return err }
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("target returned status %d", resp.StatusCode)
	}
	return nil
}

// ProcessPushJob handles push notification delivery via FCM
func ProcessPushJob(data map[string]interface{}) error {
	ctx := context.Background()

	// Parse job data
	jobType, _ := data["type"].(string)
	projectSlug, _ := data["project_slug"].(string)
	fcmConfigStr, _ := data["fcm_config"].(string)

	if projectSlug == "" || fcmConfigStr == "" {
		return fmt.Errorf("missing required fields: project_slug or fcm_config")
	}

	// Parse FCM config
	var fcmConfig FCMConfig
	if err := json.Unmarshal([]byte(fcmConfigStr), &fcmConfig); err != nil {
		return fmt.Errorf("invalid FCM config: %w", err)
	}

	// Get system pool and initialize push service
	systemPool := GetSystemPool()
	if systemPool == nil {
		return fmt.Errorf("system pool not initialized")
	}

	pushService := NewPushService(systemPool)

	// Get project from slug
	project := GetProjectBySlug(ctx, projectSlug)
	if project == nil {
		return fmt.Errorf("project not found: %s", projectSlug)
	}

	// Get project pool
	projectPool, err := GetProjectPool(project, "live")
	if err != nil {
		return fmt.Errorf("project pool not found: %s", projectSlug)
	}

	switch jobType {
	case "single":
		userID, _ := data["user_id"].(string)
		notification, _ := data["notification"].(map[string]interface{})
		
		if userID == "" {
			return fmt.Errorf("missing user_id for single push")
		}

		// Build FCM message
		msg := &FCMMessage{
			Title:    getMapString(notification, "title"),
			Body:     getMapString(notification, "body"),
			Priority: "high",
		}

		// Extract data payload
		if dataPayload, ok := notification["data"].(map[string]interface{}); ok {
			msg.Data = make(map[string]string)
			for k, v := range dataPayload {
				msg.Data[k] = fmt.Sprintf("%v", v)
			}
		}

		// Send notification
		_, err := pushService.SendPushToUser(ctx, projectPool, projectSlug, userID, msg, &fcmConfig)
		return err

	case "bulk":
		userIDs, _ := data["user_ids"].([]interface{})
		notification, _ := data["notification"].(map[string]interface{})

		// Build FCM message
		msg := &FCMMessage{
			Title:    getMapString(notification, "title"),
			Body:     getMapString(notification, "body"),
			Priority: "high",
		}

		// Extract data payload
		if dataPayload, ok := notification["data"].(map[string]interface{}); ok {
			msg.Data = make(map[string]string)
			for k, v := range dataPayload {
				msg.Data[k] = fmt.Sprintf("%v", v)
			}
		}

		// Send to each user
		for _, uid := range userIDs {
			if userID, ok := uid.(string); ok {
				_, _ = pushService.SendPushToUser(ctx, projectPool, projectSlug, userID, msg, &fcmConfig)
			}
		}
		return nil

	case "campaign":
		campaignID, _ := data["campaign_id"].(string)
		if campaignID == "" {
			return fmt.Errorf("missing campaign_id")
		}

		// Initialize bulk service
		bulkService := NewPushBulkService(systemPool)
		return bulkService.ProcessCampaign(ctx, projectPool, campaignID, pushService)

	default:
		return fmt.Errorf("unknown push job type: %s", jobType)
	}
}

// getMapString safely extracts a string from a map
func getMapString(m map[string]interface{}, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// GetSystemPool returns the system database pool
func GetSystemPool() *pgxpool.Pool {
	// This should return the system pool from the main application
	// For now, return the global pool if available
	return systemDB
}

// Global pool variables (should be initialized by main)
var systemDB *pgxpool.Pool

// SetSystemPool sets the system database pool
func SetSystemPool(pool *pgxpool.Pool) {
	systemDB = pool
}
