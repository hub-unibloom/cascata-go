package services

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// ============================================================================
// PUSH GROUPS & BULK SENDING SERVICE
// ============================================================================

// NotificationGroup represents a user group for targeted push
type NotificationGroup struct {
	ID           string          `json:"id"`
	ProjectSlug  string          `json:"project_slug"`
	Name         string          `json:"name"`
	Description  string          `json:"description"`
	FilterConfig json.RawMessage `json:"filter_config"`
	Active       bool            `json:"active"`
	UserCount    int             `json:"user_count"`
	LastSyncAt   *time.Time      `json:"last_sync_at"`
	CreatedAt    time.Time       `json:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
}

// GroupMember represents a user in a group
type GroupMember struct {
	ID        string    `json:"id"`
	GroupID   string    `json:"group_id"`
	UserID    string    `json:"user_id"`
	ProjectSlug string  `json:"project_slug"`
	AddedAt   time.Time `json:"added_at"`
}

// FilterConfig defines how to filter users for a group
type FilterConfig struct {
	Table      string        `json:"table"`
	Conditions []FilterCondition `json:"conditions"`
}

// FilterCondition represents a single filter condition
type FilterCondition struct {
	Field string      `json:"field"`
	Op    string      `json:"op"` // "eq", "ne", "gt", "lt", "contains", "in"
	Value interface{} `json:"value"`
}

// NotificationCampaign represents a bulk push campaign
type NotificationCampaign struct {
	ID              string                 `json:"id"`
	ProjectSlug     string                 `json:"project_slug"`
	Name            string                 `json:"name"`
	TemplateID      *string                `json:"template_id,omitempty"`
	TargetType      string                 `json:"target_type"` // "user", "group", "all", "query"
	TargetUserID    *string                `json:"target_user_id,omitempty"`
	TargetGroupID   *string                `json:"target_group_id,omitempty"`
	TargetQuery     *string                `json:"target_query,omitempty"`
	TitleOverride   *string                `json:"title_override,omitempty"`
	BodyOverride    *string                `json:"body_override,omitempty"`
	DataOverride    map[string]interface{} `json:"data_override,omitempty"`
	Language        string                 `json:"language"`
	ScheduledAt     *time.Time             `json:"scheduled_at,omitempty"`
	SentAt          *time.Time             `json:"sent_at,omitempty"`
	TotalRecipients int                    `json:"total_recipients"`
	SentCount       int                    `json:"sent_count"`
	FailedCount     int                    `json:"failed_count"`
	Status          string                 `json:"status"` // "pending", "scheduled", "sending", "completed", "failed", "cancelled"
	CreatedBy       *string                `json:"created_by,omitempty"`
	CreatedAt       time.Time              `json:"created_at"`
	UpdatedAt       time.Time              `json:"updated_at"`
}

// PushBulkService handles groups and bulk sending
type PushBulkService struct {
	systemPool *pgxpool.Pool
}

// NewPushBulkService creates a new bulk service
func NewPushBulkService(systemPool *pgxpool.Pool) *PushBulkService {
	return &PushBulkService{systemPool: systemPool}
}

// ============================================================================
// GROUP MANAGEMENT
// ============================================================================

// ListGroups returns all groups for a project
func (s *PushBulkService) ListGroups(ctx context.Context, projectSlug string) ([]NotificationGroup, error) {
	rows, err := s.systemPool.Query(ctx, `
		SELECT id, project_slug, name, description, filter_config, active, user_count, last_sync_at, created_at, updated_at
		FROM system.notification_groups
		WHERE project_slug = $1
		ORDER BY created_at DESC
	`, projectSlug)
	if err != nil {
		return nil, fmt.Errorf("failed to list groups: %w", err)
	}
	defer rows.Close()

	var groups []NotificationGroup
	for rows.Next() {
		var g NotificationGroup
		err := rows.Scan(&g.ID, &g.ProjectSlug, &g.Name, &g.Description, &g.FilterConfig,
			&g.Active, &g.UserCount, &g.LastSyncAt, &g.CreatedAt, &g.UpdatedAt)
		if err != nil {
			continue
		}
		groups = append(groups, g)
	}

	return groups, nil
}

// GetGroup retrieves a group by ID
func (s *PushBulkService) GetGroup(ctx context.Context, groupID string) (*NotificationGroup, error) {
	var g NotificationGroup
	err := s.systemPool.QueryRow(ctx, `
		SELECT id, project_slug, name, description, filter_config, active, user_count, last_sync_at, created_at, updated_at
		FROM system.notification_groups
		WHERE id = $1
	`, groupID).Scan(&g.ID, &g.ProjectSlug, &g.Name, &g.Description, &g.FilterConfig,
		&g.Active, &g.UserCount, &g.LastSyncAt, &g.CreatedAt, &g.UpdatedAt)

	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, fmt.Errorf("group not found")
		}
		return nil, fmt.Errorf("failed to get group: %w", err)
	}

	return &g, nil
}

// CreateGroup creates a new notification group
func (s *PushBulkService) CreateGroup(ctx context.Context, g *NotificationGroup) (*NotificationGroup, error) {
	err := s.systemPool.QueryRow(ctx, `
		INSERT INTO system.notification_groups (project_slug, name, description, filter_config, active)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, user_count, created_at, updated_at
	`, g.ProjectSlug, g.Name, g.Description, g.FilterConfig, g.Active,
	).Scan(&g.ID, &g.UserCount, &g.CreatedAt, &g.UpdatedAt)

	if err != nil {
		return nil, fmt.Errorf("failed to create group: %w", err)
	}

	return g, nil
}

// UpdateGroup updates a group
func (s *PushBulkService) UpdateGroup(ctx context.Context, g *NotificationGroup) (*NotificationGroup, error) {
	err := s.systemPool.QueryRow(ctx, `
		UPDATE system.notification_groups
		SET name = $3, description = $4, filter_config = $5, active = $6, updated_at = NOW()
		WHERE id = $1 AND project_slug = $2
		RETURNING updated_at
	`, g.ID, g.ProjectSlug, g.Name, g.Description, g.FilterConfig, g.Active,
	).Scan(&g.UpdatedAt)

	if err != nil {
		return nil, fmt.Errorf("failed to update group: %w", err)
	}

	return g, nil
}

// DeleteGroup deletes a group and its members
func (s *PushBulkService) DeleteGroup(ctx context.Context, groupID, projectSlug string) error {
	_, err := s.systemPool.Exec(ctx, `
		DELETE FROM system.notification_groups 
		WHERE id = $1 AND project_slug = $2
	`, groupID, projectSlug)
	return err
}

// SyncGroup synchronizes group members based on filter config
func (s *PushBulkService) SyncGroup(ctx context.Context, projectPool *pgxpool.Pool, groupID string) error {
	group, err := s.GetGroup(ctx, groupID)
	if err != nil {
		return err
	}

	var filter FilterConfig
	if err := json.Unmarshal(group.FilterConfig, &filter); err != nil {
		return fmt.Errorf("invalid filter config: %w", err)
	}

	// Build query from filter
	query, args := s.buildFilterQuery(filter)
	
	// Execute query to get matching user IDs
	rows, err := projectPool.Query(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("failed to query users: %w", err)
	}
	defer rows.Close()

	// Clear existing members
	_, err = s.systemPool.Exec(ctx, `DELETE FROM system.notification_group_members WHERE group_id = $1`, groupID)
	if err != nil {
		return fmt.Errorf("failed to clear members: %w", err)
	}

	// Insert new members
	count := 0
	for rows.Next() {
		var userID string
		if err := rows.Scan(&userID); err != nil {
			continue
		}

		_, err = s.systemPool.Exec(ctx, `
			INSERT INTO system.notification_group_members (group_id, user_id, project_slug)
			VALUES ($1, $2, $3)
			ON CONFLICT DO NOTHING
		`, groupID, userID, group.ProjectSlug)
		if err == nil {
			count++
		}
	}

	// Update user count and sync time
	now := time.Now()
	_, err = s.systemPool.Exec(ctx, `
		UPDATE system.notification_groups 
		SET user_count = $2, last_sync_at = $3, updated_at = NOW()
		WHERE id = $1
	`, groupID, count, now)

	return err
}

// buildFilterQuery constructs a SQL query from filter config
func (s *PushBulkService) buildFilterQuery(filter FilterConfig) (string, []interface{}) {
	table := filter.Table
	if table == "" {
		table = "users" // default
	}

	query := fmt.Sprintf(`SELECT id FROM public."%s" WHERE 1=1`, table)
	var args []interface{}
	argCount := 1

	for _, cond := range filter.Conditions {
		switch cond.Op {
		case "eq":
			query += fmt.Sprintf(` AND "%s" = $%d`, cond.Field, argCount)
			args = append(args, cond.Value)
			argCount++
		case "ne":
			query += fmt.Sprintf(` AND "%s" != $%d`, cond.Field, argCount)
			args = append(args, cond.Value)
			argCount++
		case "gt":
			query += fmt.Sprintf(` AND "%s" > $%d`, cond.Field, argCount)
			args = append(args, cond.Value)
			argCount++
		case "lt":
			query += fmt.Sprintf(` AND "%s" < $%d`, cond.Field, argCount)
			args = append(args, cond.Value)
			argCount++
		case "contains":
			query += fmt.Sprintf(` AND "%s" ILIKE $%d`, cond.Field, argCount)
			args = append(args, fmt.Sprintf("%%%v%%", cond.Value))
			argCount++
		case "in":
			if arr, ok := cond.Value.([]interface{}); ok {
				placeholders := ""
				for i := range arr {
					if i > 0 {
						placeholders += ","
					}
					placeholders += fmt.Sprintf("$%d", argCount)
					args = append(args, arr[i])
					argCount++
				}
				query += fmt.Sprintf(` AND "%s" IN (%s)`, cond.Field, placeholders)
			}
		}
	}

	return query, args
}

// GetGroupMembers returns all members of a group
func (s *PushBulkService) GetGroupMembers(ctx context.Context, groupID string) ([]string, error) {
	rows, err := s.systemPool.Query(ctx, `
		SELECT user_id FROM system.notification_group_members WHERE group_id = $1
	`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var members []string
	for rows.Next() {
		var userID string
		if err := rows.Scan(&userID); err == nil {
			members = append(members, userID)
		}
	}

	return members, nil
}

// ============================================================================
// CAMPAIGN / BULK SENDING
// ============================================================================

// ListCampaigns returns all campaigns for a project
func (s *PushBulkService) ListCampaigns(ctx context.Context, projectSlug string) ([]NotificationCampaign, error) {
	rows, err := s.systemPool.Query(ctx, `
		SELECT id, project_slug, name, template_id, target_type, target_user_id, target_group_id, target_query,
		       title_override, body_override, data_override, language, scheduled_at, sent_at,
		       total_recipients, sent_count, failed_count, status, created_by, created_at, updated_at
		FROM system.notification_campaigns
		WHERE project_slug = $1
		ORDER BY created_at DESC
	`, projectSlug)
	if err != nil {
		return nil, fmt.Errorf("failed to list campaigns: %w", err)
	}
	defer rows.Close()

	var campaigns []NotificationCampaign
	for rows.Next() {
		var c NotificationCampaign
		var dataJSON []byte
		err := rows.Scan(&c.ID, &c.ProjectSlug, &c.Name, &c.TemplateID, &c.TargetType, &c.TargetUserID,
			&c.TargetGroupID, &c.TargetQuery, &c.TitleOverride, &c.BodyOverride, &dataJSON, &c.Language,
			&c.ScheduledAt, &c.SentAt, &c.TotalRecipients, &c.SentCount, &c.FailedCount, &c.Status,
			&c.CreatedBy, &c.CreatedAt, &c.UpdatedAt)
		if err != nil {
			continue
		}
		json.Unmarshal(dataJSON, &c.DataOverride)
		campaigns = append(campaigns, c)
	}

	return campaigns, nil
}

// CreateCampaign creates a new push campaign
func (s *PushBulkService) CreateCampaign(ctx context.Context, c *NotificationCampaign) (*NotificationCampaign, error) {
	dataJSON, _ := json.Marshal(c.DataOverride)

	// Sanitize empty strings to nil for UUID fields
	templateID := c.TemplateID
	if templateID != nil && *templateID == "" {
		templateID = nil
	}
	targetUserID := c.TargetUserID
	if targetUserID != nil && *targetUserID == "" {
		targetUserID = nil
	}
	targetGroupID := c.TargetGroupID
	if targetGroupID != nil && *targetGroupID == "" {
		targetGroupID = nil
	}

	// Determine initial status
	status := "pending"
	if c.ScheduledAt != nil && c.ScheduledAt.After(time.Now()) {
		status = "scheduled"
	}

	err := s.systemPool.QueryRow(ctx, `
		INSERT INTO system.notification_campaigns 
		(project_slug, name, template_id, target_type, target_user_id, target_group_id, target_query,
		 title_override, body_override, data_override, language, scheduled_at, status, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		RETURNING id, total_recipients, sent_count, failed_count, created_at, updated_at
	`, c.ProjectSlug, c.Name, templateID, c.TargetType, targetUserID, targetGroupID, c.TargetQuery,
		c.TitleOverride, c.BodyOverride, dataJSON, c.Language, c.ScheduledAt, status, c.CreatedBy,
	).Scan(&c.ID, &c.TotalRecipients, &c.SentCount, &c.FailedCount, &c.CreatedAt, &c.UpdatedAt)

	if err != nil {
		return nil, fmt.Errorf("failed to create campaign: %w", err)
	}

	c.Status = status

	// If not scheduled, queue immediately
	if c.ScheduledAt == nil || !c.ScheduledAt.After(time.Now()) {
		if err := s.QueueCampaign(ctx, c.ID); err != nil {
			// Log error but don't fail creation
		}
	}

	return c, nil
}

// CancelCampaign cancels a pending/scheduled campaign
func (s *PushBulkService) CancelCampaign(ctx context.Context, campaignID, projectSlug string) error {
	_, err := s.systemPool.Exec(ctx, `
		UPDATE system.notification_campaigns 
		SET status = 'cancelled', updated_at = NOW()
		WHERE id = $1 AND project_slug = $2 AND status IN ('pending', 'scheduled')
	`, campaignID, projectSlug)
	return err
}

// QueueCampaign adds a campaign to the processing queue
func (s *PushBulkService) QueueCampaign(ctx context.Context, campaignID string) error {
	// Add to Redis stream for processing
	if dragonfly == nil {
		return fmt.Errorf("queue not initialized")
	}

	payload, _ := json.Marshal(map[string]interface{}{
		"campaign_id": campaignID,
		"type":        "campaign",
	})

	return dragonfly.XAdd(ctx, &redis.XAddArgs{
		Stream: StreamPush,
		Values: map[string]interface{}{
			"payload": string(payload),
			"ts":      time.Now().UnixMilli(),
		},
	}).Err()
}

// ProcessCampaign executes a push campaign
func (s *PushBulkService) ProcessCampaign(ctx context.Context, projectPool *pgxpool.Pool, campaignID string, pushSvc *PushService) error {
	// Get campaign
	var c NotificationCampaign
	var dataJSON []byte
	err := s.systemPool.QueryRow(ctx, `
		SELECT id, project_slug, name, template_id, target_type, target_user_id, target_group_id, target_query,
		       title_override, body_override, data_override, language
		FROM system.notification_campaigns
		WHERE id = $1
	`, campaignID).Scan(&c.ID, &c.ProjectSlug, &c.Name, &c.TemplateID, &c.TargetType, &c.TargetUserID,
		&c.TargetGroupID, &c.TargetQuery, &c.TitleOverride, &c.BodyOverride, &dataJSON, &c.Language)

	if err != nil {
		return fmt.Errorf("campaign not found: %w", err)
	}
	json.Unmarshal(dataJSON, &c.DataOverride)

	// Update status to sending
	_, err = s.systemPool.Exec(ctx, `
		UPDATE system.notification_campaigns SET status = 'sending', updated_at = NOW() WHERE id = $1
	`, campaignID)
	if err != nil {
		return err
	}

	// Get target user IDs
	var userIDs []string
	switch c.TargetType {
	case "user":
		if c.TargetUserID != nil {
			userIDs = []string{*c.TargetUserID}
		}
	case "group":
		if c.TargetGroupID != nil {
			userIDs, _ = s.GetGroupMembers(ctx, *c.TargetGroupID)
		}
	case "all":
		// Get all users with devices
		rows, err := projectPool.Query(ctx, `
			SELECT DISTINCT user_id FROM auth.user_devices WHERE is_active = true
		`)
		if err == nil {
			for rows.Next() {
				var uid string
				if rows.Scan(&uid) == nil {
					userIDs = append(userIDs, uid)
				}
			}
			rows.Close()
		}
	case "query":
		if c.TargetQuery != nil {
			rows, err := projectPool.Query(ctx, *c.TargetQuery)
			if err == nil {
				for rows.Next() {
					var uid string
					if rows.Scan(&uid) == nil {
						userIDs = append(userIDs, uid)
					}
				}
				rows.Close()
			}
		}
	}

	// Update total recipients
	totalRecipients := len(userIDs)
	_, _ = s.systemPool.Exec(ctx, `
		UPDATE system.notification_campaigns SET total_recipients = $2 WHERE id = $1
	`, campaignID, totalRecipients)

	// Get FCM config
	fcmConfig, err := pushSvc.GetFCMConfig(ctx, c.ProjectSlug)
	if err != nil {
		_, _ = s.systemPool.Exec(ctx, `
			UPDATE system.notification_campaigns SET status = 'failed', updated_at = NOW() WHERE id = $1
		`, campaignID)
		return fmt.Errorf("FCM not configured: %w", err)
	}

	// Send to each user (queue individually)
	sentCount := 0
	failedCount := 0

	for _, userID := range userIDs {
		notification := map[string]interface{}{
			"title": c.TitleOverride,
			"body":  c.BodyOverride,
			"data":  c.DataOverride,
		}

		// If using template, render it
		if c.TemplateID != nil {
			templateSvc := NewPushTemplateService(s.systemPool)
			template, err := templateSvc.GetTemplate(ctx, c.ProjectSlug, *c.TemplateID)
			if err == nil {
				content, data, _ := templateSvc.RenderTemplate(ctx, c.ProjectSlug, template.Code, c.Language, c.DataOverride)
				if content != nil {
					notification["title"] = content.Title
					notification["body"] = content.Body
					notification["data"] = data
				}
			}
		}

		err := pushSvc.QueuePushNotification(ctx, c.ProjectSlug, userID, notification, fcmConfig)
		if err != nil {
			failedCount++
		} else {
			sentCount++
		}
	}

	// Update final status
	now := time.Now()
	_, err = s.systemPool.Exec(ctx, `
		UPDATE system.notification_campaigns 
		SET status = 'completed', sent_count = $2, failed_count = $3, sent_at = $4, updated_at = NOW()
		WHERE id = $1
	`, campaignID, sentCount, failedCount, now)

	return err
}
