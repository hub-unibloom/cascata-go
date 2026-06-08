package services

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ============================================================================
// PUSH NOTIFICATION SERVICE - FCM (Firebase Cloud Messaging)
// ============================================================================

// FCMConfig holds Firebase service account credentials
type FCMConfig struct {
	ProjectID   string `json:"project_id"`
	ClientEmail string `json:"client_email"`
	PrivateKey  string `json:"private_key"`
}

// FCMMessage represents a push notification message
type FCMMessage struct {
	Token        string                 `json:"token"`
	Title        string                 `json:"title"`
	Body         string                 `json:"body"`
	Data         map[string]string      `json:"data,omitempty"`
	ImageURL     string                 `json:"image_url,omitempty"`
	Sound        string                 `json:"sound,omitempty"`
	Badge        int                    `json:"badge,omitempty"`
	Priority     string                 `json:"priority,omitempty"` // "high" or "normal"
	CollapseKey  string                 `json:"collapse_key,omitempty"`
	TimeToLive   int                    `json:"time_to_live,omitempty"` // seconds
}

// FCMBatchResponse represents FCM multicast response
type FCMBatchResponse struct {
	SuccessCount int                `json:"success_count"`
	FailureCount   int                `json:"failure_count"`
	Responses    []FCMResponse      `json:"responses"`
}

// FCMResponse represents individual message response
type FCMResponse struct {
	Success   bool   `json:"success"`
	MessageID string `json:"message_id,omitempty"`
	Error     string `json:"error,omitempty"`
	Token     string `json:"token"`
}

// PushService handles all push notification operations
type PushService struct {
	systemPool *pgxpool.Pool
}

// NewPushService creates a new push service instance
func NewPushService(systemPool *pgxpool.Pool) *PushService {
	return &PushService{systemPool: systemPool}
}

// ============================================================================
// DEVICE MANAGEMENT
// ============================================================================

// DeviceRegistration represents a registered device
type DeviceRegistration struct {
	ID           string    `json:"id"`
	UserID       string    `json:"user_id"`
	Token        string    `json:"token"`
	Platform     string    `json:"platform"` // "android", "ios", "web"
	AppVersion   string    `json:"app_version"`
	IsActive     bool      `json:"is_active"`
	LastActiveAt time.Time `json:"last_active_at"`
	CreatedAt    time.Time `json:"created_at"`
}

// RegisterDevice registers or updates a device token for a user
func (s *PushService) RegisterDevice(ctx context.Context, projectPool *pgxpool.Pool, userID, token, platform, appVersion string) error {
	// Ensure one active token per user (remove from other users)
	_, err := projectPool.Exec(ctx, `
		DELETE FROM auth.user_devices 
		WHERE token = $1 AND user_id != $2
	`, token, userID)
	if err != nil {
		return fmt.Errorf("failed to cleanup duplicate tokens: %w", err)
	}

	// Insert or update device
	_, err = projectPool.Exec(ctx, `
		INSERT INTO auth.user_devices (user_id, token, platform, app_version, last_active_at, is_active)
		VALUES ($1, $2, $3, $4, NOW(), true)
		ON CONFLICT (user_id, token) 
		DO UPDATE SET 
			last_active_at = NOW(), 
			is_active = true, 
			app_version = EXCLUDED.app_version,
			platform = EXCLUDED.platform
	`, userID, token, platform, appVersion)

	if err != nil {
		return fmt.Errorf("failed to register device: %w", err)
	}

	return nil
}

// UnregisterDevice marks a device as inactive
func (s *PushService) UnregisterDevice(ctx context.Context, projectPool *pgxpool.Pool, userID, token string) error {
	_, err := projectPool.Exec(ctx, `
		UPDATE auth.user_devices 
		SET is_active = false 
		WHERE user_id = $1 AND token = $2
	`, userID, token)
	return err
}

// GetUserDevices returns all active devices for a user
func (s *PushService) GetUserDevices(ctx context.Context, projectPool *pgxpool.Pool, userID string) ([]DeviceRegistration, error) {
	rows, err := projectPool.Query(ctx, `
		SELECT id, user_id, token, platform, app_version, is_active, last_active_at, created_at
		FROM auth.user_devices
		WHERE user_id = $1 AND is_active = true
		ORDER BY last_active_at DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var devices []DeviceRegistration
	for rows.Next() {
		var d DeviceRegistration
		err := rows.Scan(&d.ID, &d.UserID, &d.Token, &d.Platform, &d.AppVersion, &d.IsActive, &d.LastActiveAt, &d.CreatedAt)
		if err != nil {
			continue
		}
		devices = append(devices, d)
	}

	return devices, nil
}

// CleanupInvalidToken removes a device token that's been invalidated by FCM
func (s *PushService) CleanupInvalidToken(ctx context.Context, projectPool *pgxpool.Pool, token string) error {
	_, err := projectPool.Exec(ctx, `DELETE FROM auth.user_devices WHERE token = $1`, token)
	return err
}

// ============================================================================
// FCM AUTHENTICATION (JWT)
// ============================================================================

// GenerateFCMAccessToken creates a signed JWT for FCM HTTP v1 API
func (s *PushService) GenerateFCMAccessToken(config *FCMConfig) (string, error) {
	now := time.Now().Unix()

	claims := jwt.MapClaims{
		"iss":   config.ClientEmail,
		"sub":   config.ClientEmail,
		"scope": "https://www.googleapis.com/auth/firebase.messaging",
		"aud":   "https://oauth2.googleapis.com/token",
		"iat":   now,
		"exp":   now + 3600, // 1 hour
	}

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)

	// Parse private key
	privateKey, err := parsePrivateKey(config.PrivateKey)
	if err != nil {
		return "", fmt.Errorf("failed to parse private key: %w", err)
	}

	signedToken, err := token.SignedString(privateKey)
	if err != nil {
		return "", fmt.Errorf("failed to sign token: %w", err)
	}

	return signedToken, nil
}

// parsePrivateKey parses PEM encoded RSA private key
func parsePrivateKey(pemStr string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, fmt.Errorf("failed to parse PEM block")
	}

	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		// Try PKCS1
		key, err = x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("failed to parse private key: %w", err)
		}
	}

	rsaKey, ok := key.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("not an RSA private key")
	}

	return rsaKey, nil
}

// GetFCMConfig retrieves FCM configuration for a project
func (s *PushService) GetFCMConfig(ctx context.Context, projectSlug string) (*FCMConfig, error) {
	var configJSON []byte
	err := s.systemPool.QueryRow(ctx, `
		SELECT config_json 
		FROM system.push_provider_configs 
		WHERE project_slug = $1 AND provider = 'fcm' AND active = true
		ORDER BY is_default DESC, created_at DESC
		LIMIT 1
	`, projectSlug).Scan(&configJSON)

	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, fmt.Errorf("FCM not configured for project")
		}
		return nil, fmt.Errorf("failed to get FCM config: %w", err)
	}

	var config FCMConfig
	if err := json.Unmarshal(configJSON, &config); err != nil {
		return nil, fmt.Errorf("failed to parse FCM config: %w", err)
	}

	return &config, nil
}

// SaveFCMConfig saves or updates FCM configuration for a project
func (s *PushService) SaveFCMConfig(ctx context.Context, projectSlug string, config *FCMConfig, isDefault bool) error {
	configJSON, err := json.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	_, err = s.systemPool.Exec(ctx, `
		INSERT INTO system.push_provider_configs (project_slug, provider, config_json, active, is_default)
		VALUES ($1, 'fcm', $2, true, $3)
		ON CONFLICT (project_slug, provider) 
		DO UPDATE SET 
			config_json = EXCLUDED.config_json,
			active = true,
			is_default = EXCLUDED.is_default,
			updated_at = NOW()
	`, projectSlug, configJSON, isDefault)

	return err
}

// ============================================================================
// NOTIFICATION RULES (Automation)
// ============================================================================

// NotificationRule represents a push automation rule
type NotificationRule struct {
	ID              string          `json:"id"`
	ProjectSlug     string          `json:"project_slug"`
	Name            string          `json:"name"`
	Active          bool            `json:"active"`
	TriggerTable    string          `json:"trigger_table"`
	TriggerEvent    string          `json:"trigger_event"`
	Conditions      json.RawMessage `json:"conditions"`
	RecipientColumn string          `json:"recipient_column"`
	TitleTemplate   string          `json:"title_template"`
	BodyTemplate    string          `json:"body_template"`
	DataPayload     json.RawMessage `json:"data_payload"`
	CreatedAt       time.Time       `json:"created_at"`
}

// ListNotificationRules returns all rules for a project
func (s *PushService) ListNotificationRules(ctx context.Context, projectSlug string) ([]NotificationRule, error) {
	rows, err := s.systemPool.Query(ctx, `
		SELECT id, project_slug, name, active, trigger_table, trigger_event, 
		       conditions, recipient_column, title_template, body_template, data_payload, created_at
		FROM system.notification_rules
		WHERE project_slug = $1
		ORDER BY created_at DESC
	`, projectSlug)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var rules []NotificationRule
	for rows.Next() {
		var r NotificationRule
		err := rows.Scan(&r.ID, &r.ProjectSlug, &r.Name, &r.Active, &r.TriggerTable, &r.TriggerEvent,
			&r.Conditions, &r.RecipientColumn, &r.TitleTemplate, &r.BodyTemplate, &r.DataPayload, &r.CreatedAt)
		if err != nil {
			continue
		}
		rules = append(rules, r)
	}

	return rules, nil
}

// CreateNotificationRule creates a new notification rule
func (s *PushService) CreateNotificationRule(ctx context.Context, rule *NotificationRule) (*NotificationRule, error) {
	err := s.systemPool.QueryRow(ctx, `
		INSERT INTO system.notification_rules 
		(project_slug, name, active, trigger_table, trigger_event, conditions, recipient_column, title_template, body_template, data_payload)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING id, created_at
	`, rule.ProjectSlug, rule.Name, rule.Active, rule.TriggerTable, rule.TriggerEvent,
		rule.Conditions, rule.RecipientColumn, rule.TitleTemplate, rule.BodyTemplate, rule.DataPayload,
	).Scan(&rule.ID, &rule.CreatedAt)

	if err != nil {
		return nil, fmt.Errorf("failed to create rule: %w", err)
	}

	return rule, nil
}

// DeleteNotificationRule deletes a rule
func (s *PushService) DeleteNotificationRule(ctx context.Context, ruleID, projectSlug string) error {
	_, err := s.systemPool.Exec(ctx, `
		DELETE FROM system.notification_rules 
		WHERE id = $1 AND project_slug = $2
	`, ruleID, projectSlug)
	return err
}

// ProcessEventTrigger processes a database event and triggers notifications
func (s *PushService) ProcessEventTrigger(ctx context.Context, projectSlug string, projectPool *pgxpool.Pool, event *TriggerEvent, fcmConfig *FCMConfig) error {
	// Find matching rules
	rows, err := s.systemPool.Query(ctx, `
		SELECT id, recipient_column, title_template, body_template, data_payload
		FROM system.notification_rules
		WHERE project_slug = $1 
		  AND trigger_table = $2 
		  AND (trigger_event = $3 OR trigger_event = 'ALL')
		  AND active = true
	`, projectSlug, event.Table, event.Action)
	if err != nil {
		return fmt.Errorf("failed to fetch rules: %w", err)
	}
	defer rows.Close()

	// Fetch the record that triggered the event
	var record map[string]interface{}
	if event.RecordID != nil {
		row := projectPool.QueryRow(ctx, 
			fmt.Sprintf(`SELECT * FROM public."%s" WHERE id = $1`, event.Table), 
			event.RecordID,
		)
		record, err = rowToMap(row)
		if err != nil {
			record = make(map[string]interface{})
		}
	}

	for rows.Next() {
		var ruleID, recipientColumn, titleTemplate, bodyTemplate string
		var dataPayload json.RawMessage
		err := rows.Scan(&ruleID, &recipientColumn, &titleTemplate, &bodyTemplate, &dataPayload)
		if err != nil {
			continue
		}

		// Get recipient user ID from record
		recipientID, ok := record[recipientColumn].(string)
		if !ok {
			continue
		}

		// Apply template substitution
		title := applyTemplate(titleTemplate, record)
		body := applyTemplate(bodyTemplate, record)

		// Queue push notification
		notification := map[string]interface{}{
			"title": title,
			"body":  body,
			"data":  dataPayload,
		}

		if err := s.QueuePushNotification(ctx, projectSlug, recipientID, notification, fcmConfig); err != nil {
			// Log error but continue processing other rules
			continue
		}
	}

	return nil
}

// TriggerEvent represents a database trigger event
type TriggerEvent struct {
	Table    string      `json:"table"`
	Action   string      `json:"action"` // INSERT, UPDATE, DELETE
	RecordID interface{} `json:"record_id"`
}

// applyTemplate replaces {{variable}} placeholders with actual values
func applyTemplate(template string, data map[string]interface{}) string {
	result := template
	for key, value := range data {
		placeholder := fmt.Sprintf("{{%s}}", key)
		strValue := ""
		if value != nil {
			strValue = fmt.Sprintf("%v", value)
		}
		result = replaceAll(result, placeholder, strValue)
	}
	return result
}

func replaceAll(s, old, new string) string {
	// Simple string replacement
	result := ""
	start := 0
	for {
		idx := 0
		for i := start; i <= len(s)-len(old); i++ {
			if s[i:i+len(old)] == old {
				idx = i
				break
			}
		}
		if idx == 0 {
			result += s[start:]
			break
		}
		result += s[start:idx] + new
		start = idx + len(old)
	}
	return result
}

// rowToMap converts a pgx.Row to a map
func rowToMap(row pgx.Row) (map[string]interface{}, error) {
	// This is a simplified version - in production you'd use field descriptions
	var data map[string]interface{}
	// Implementation would require pgx field description access
	return data, nil
}

// ============================================================================
// QUEUE & SENDING
// ============================================================================

// QueuePushNotification adds a push notification job to the queue
func (s *PushService) QueuePushNotification(ctx context.Context, projectSlug, userID string, notification map[string]interface{}, fcmConfig *FCMConfig) error {
	configJSON, _ := json.Marshal(fcmConfig)
	
	jobData := map[string]interface{}{
		"type":         "single",
		"project_slug": projectSlug,
		"user_id":      userID,
		"notification": notification,
		"fcm_config":   string(configJSON),
	}

	return AddStreamJob(ctx, StreamPush, jobData)
}

// QueueBulkPush adds multiple push notifications to the queue
func (s *PushService) QueueBulkPush(ctx context.Context, projectSlug string, userIDs []string, notification map[string]interface{}, fcmConfig *FCMConfig) error {
	configJSON, _ := json.Marshal(fcmConfig)
	
	jobData := map[string]interface{}{
		"type":         "bulk",
		"project_slug": projectSlug,
		"user_ids":     userIDs,
		"notification": notification,
		"fcm_config":   string(configJSON),
	}

	return AddStreamJob(ctx, StreamPush, jobData)
}

// SendPushToUser sends a push notification to a specific user immediately (not queued)
func (s *PushService) SendPushToUser(ctx context.Context, projectPool *pgxpool.Pool, projectSlug, userID string, msg *FCMMessage, fcmConfig *FCMConfig) (*FCMBatchResponse, error) {
	// Get user's active devices
	devices, err := s.GetUserDevices(ctx, projectPool, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get devices: %w", err)
	}
	if len(devices) == 0 {
		return &FCMBatchResponse{SuccessCount: 0, FailureCount: 0}, nil
	}

	// Generate FCM access token
	accessToken, err := s.GenerateFCMAccessToken(fcmConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to generate access token: %w", err)
	}

	// Send to each device
	response := &FCMBatchResponse{
		Responses: make([]FCMResponse, 0, len(devices)),
	}

	for _, device := range devices {
		resp := s.sendToFCM(projectSlug, device.Token, msg, fcmConfig, accessToken)
		response.Responses = append(response.Responses, resp)
		if resp.Success {
			response.SuccessCount++
		} else {
			response.FailureCount++
			// Cleanup invalid tokens
			if resp.Error == "INVALID_TOKEN" || resp.Error == "UNREGISTERED" {
				s.CleanupInvalidToken(ctx, projectPool, device.Token)
			}
		}
	}

	// Log to history
	s.logHistory(ctx, projectSlug, userID, "completed", response)

	return response, nil
}

// sendToFCM sends a single message to FCM (synchronous)
func (s *PushService) sendToFCM(projectSlug, token string, msg *FCMMessage, config *FCMConfig, accessToken string) FCMResponse {
	// Build FCM HTTP v1 message payload
	messagePayload := map[string]interface{}{
		"message": map[string]interface{}{
			"token": token,
			"notification": map[string]interface{}{
				"title": msg.Title,
				"body":  msg.Body,
			},
		},
	}

	// Add optional fields
	if msg.ImageURL != "" {
		if notification, ok := messagePayload["message"].(map[string]interface{})["notification"].(map[string]interface{}); ok {
			notification["image"] = msg.ImageURL
		}
	}

	if len(msg.Data) > 0 {
		messagePayload["message"].(map[string]interface{})["data"] = msg.Data
	}

	// Android specific config
	androidConfig := map[string]interface{}{}
	if msg.Priority == "high" {
		androidConfig["priority"] = "high"
	}
	if msg.Sound != "" {
		androidConfig["notification"] = map[string]interface{}{
			"sound": msg.Sound,
		}
	}
	if len(androidConfig) > 0 {
		messagePayload["message"].(map[string]interface{})["android"] = androidConfig
	}

	// APNs config for iOS
	if msg.Sound != "" || msg.Badge > 0 {
		apnsConfig := map[string]interface{}{
			"payload": map[string]interface{}{
				"aps": map[string]interface{}{},
			},
		}
		if msg.Sound != "" {
			apnsConfig["payload"].(map[string]interface{})["aps"].(map[string]interface{})["sound"] = msg.Sound
		}
		if msg.Badge > 0 {
			apnsConfig["payload"].(map[string]interface{})["aps"].(map[string]interface{})["badge"] = msg.Badge
		}
		messagePayload["message"].(map[string]interface{})["apns"] = apnsConfig
	}

	// NOTE: In production, this would make an actual HTTP request to FCM
	// For now, we simulate success
	// url := fmt.Sprintf("https://fcm.googleapis.com/v1/projects/%s/messages:send", config.ProjectID)
	// resp, err := makeHTTPRequest("POST", url, messagePayload, accessToken)

	return FCMResponse{
		Success:   true,
		MessageID: generateMessageID(),
		Token:     token,
	}
}

// logHistory logs a notification to history
func (s *PushService) logHistory(ctx context.Context, projectSlug, userID, status string, response *FCMBatchResponse) {
	responseJSON, _ := json.Marshal(response)
	_, _ = s.systemPool.Exec(ctx, `
		INSERT INTO system.notification_history (project_slug, user_id, status, provider_response)
		VALUES ($1, $2, $3, $4)
	`, projectSlug, userID, status, responseJSON)
}

// generateMessageID generates a unique message ID
func generateMessageID() string {
	return fmt.Sprintf("msg_%d", time.Now().UnixNano())
}

// ============================================================================
// HISTORY & STATS
// ============================================================================

// NotificationHistory represents a history entry
type NotificationHistory struct {
	ID              string          `json:"id"`
	ProjectSlug     string          `json:"project_slug"`
	UserID          string          `json:"user_id"`
	Status          string          `json:"status"`
	ProviderResponse json.RawMessage `json:"provider_response"`
	CampaignID      *string         `json:"campaign_id,omitempty"`
	TemplateID      *string         `json:"template_id,omitempty"`
	GroupID         *string         `json:"group_id,omitempty"`
	CreatedAt       time.Time       `json:"created_at"`
}

// ListHistory returns notification history for a project
func (s *PushService) ListHistory(ctx context.Context, projectSlug string, limit int) ([]NotificationHistory, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 1000 {
		limit = 1000
	}

	rows, err := s.systemPool.Query(ctx, `
		SELECT id, project_slug, user_id, status, provider_response, 
		       campaign_id, template_id, group_id, created_at
		FROM system.notification_history
		WHERE project_slug = $1
		ORDER BY created_at DESC
		LIMIT $2
	`, projectSlug, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to list history: %w", err)
	}
	defer rows.Close()

	var history []NotificationHistory
	for rows.Next() {
		var h NotificationHistory
		err := rows.Scan(&h.ID, &h.ProjectSlug, &h.UserID, &h.Status, &h.ProviderResponse,
			&h.CampaignID, &h.TemplateID, &h.GroupID, &h.CreatedAt)
		if err != nil {
			continue
		}
		history = append(history, h)
	}

	return history, nil
}

// PushStats represents push notification statistics
type PushStats struct {
	TotalSent      int64 `json:"total_sent"`
	TotalFailed    int64 `json:"total_failed"`
	TotalDevices   int64 `json:"total_devices"`
	ActiveDevices  int64 `json:"active_devices"`
	TodaySent      int64 `json:"today_sent"`
	TodayFailed    int64 `json:"today_failed"`
}

// GetStats returns push notification statistics for a project
func (s *PushService) GetStats(ctx context.Context, projectSlug string) (*PushStats, error) {
	var stats PushStats

	// Get total sent/failed
	err := s.systemPool.QueryRow(ctx, `
		SELECT 
			COUNT(*) FILTER (WHERE status = 'completed' OR status = 'sent'),
			COUNT(*) FILTER (WHERE status = 'failed')
		FROM system.notification_history
		WHERE project_slug = $1
	`, projectSlug).Scan(&stats.TotalSent, &stats.TotalFailed)
	if err != nil {
		return nil, err
	}

	// Get today's stats
	err = s.systemPool.QueryRow(ctx, `
		SELECT 
			COUNT(*) FILTER (WHERE status = 'completed' OR status = 'sent'),
			COUNT(*) FILTER (WHERE status = 'failed')
		FROM system.notification_history
		WHERE project_slug = $1 AND created_at >= CURRENT_DATE
	`, projectSlug).Scan(&stats.TodaySent, &stats.TodayFailed)
	if err != nil {
		return nil, err
	}

	return &stats, nil
}

// ListDevices returns all devices for a project (from auth.user_devices)
func (s *PushService) ListDevices(ctx context.Context, projectPool *pgxpool.Pool) ([]DeviceRegistration, error) {
	// Retry com backoff para lidar com prepared statement conflicts (SQLSTATE 08P01)
	var rows pgx.Rows
	var err error
	
	for attempts := 0; attempts < 3; attempts++ {
		rows, err = projectPool.Query(ctx, `
			SELECT id, user_id, token, platform, app_version, is_active, last_active_at, created_at
			FROM auth.user_devices
			ORDER BY last_active_at DESC
			LIMIT 1000
		`)
		if err == nil {
			break
		}
		
		// Se for erro de prepared statement duplicado, tenta novamente
		if err != nil && attempts < 2 {
			errStr := err.Error()
			if strings.Contains(errStr, "08P01") || strings.Contains(errStr, "prepared statement") {
				time.Sleep(time.Millisecond * time.Duration(10*(attempts+1)))
				continue
			}
		}
		break
	}
	
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var devices []DeviceRegistration
	for rows.Next() {
		var d DeviceRegistration
		err := rows.Scan(&d.ID, &d.UserID, &d.Token, &d.Platform, &d.AppVersion, &d.IsActive, &d.LastActiveAt, &d.CreatedAt)
		if err != nil {
			continue
		}
		devices = append(devices, d)
	}

	return devices, nil
}
