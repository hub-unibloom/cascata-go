package services

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"time"
)

// AdminActionType represents the type of administrative action
type AdminActionType string

const (
	// Security Actions
	ActionSecurityPanicToggle   AdminActionType = "security_panic_toggle"
	ActionSecurityBlockIP       AdminActionType = "security_block_ip"
	ActionSecurityUnblockIP     AdminActionType = "security_unblock_ip"
	ActionSecurityPolicyCreate  AdminActionType = "security_policy_create"
	ActionSecurityPolicyDelete  AdminActionType = "security_policy_delete"
	ActionSecurityRateLimitSet  AdminActionType = "security_rate_limit_set"
	
	// CLI Actions
	ActionCLICommand AdminActionType = "cli_command"
	
	// User Management
	ActionUserCreate AdminActionType = "user_create"
	ActionUserDelete AdminActionType = "user_delete"
	ActionUserUpdate AdminActionType = "user_update"
	
	// Project Management
	ActionProjectCreate AdminActionType = "project_create"
	ActionProjectUpdate AdminActionType = "project_update"
	ActionProjectDelete AdminActionType = "project_delete"
	
	// Backup & Restore
	ActionBackupCreate  AdminActionType = "backup_create"
	ActionBackupRestore AdminActionType = "backup_restore"
	
	// Certificate Management
	ActionCertCreate AdminActionType = "cert_create"
	ActionCertDelete AdminActionType = "cert_delete"
	
	// Vault/Secrets
	ActionSecretCreate AdminActionType = "secret_create"
	ActionSecretDelete AdminActionType = "secret_delete"
	ActionSecretReveal AdminActionType = "secret_reveal"
)

// ActorType represents who performed the action
type ActorType string

const (
	ActorCLI        ActorType = "cli"
	ActorUser       ActorType = "user"
	ActorSystem     ActorType = "system"
	ActorAutomation ActorType = "automation"
)

// ActionStatus represents the result of an action
type ActionStatus string

const (
	StatusSuccess ActionStatus = "success"
	StatusFailure ActionStatus = "failure"
	StatusPartial ActionStatus = "partial"
	StatusTimeout ActionStatus = "timeout"
)

// AdminAuditEntry represents a single administrative action log entry
type AdminAuditEntry struct {
	ID                  string                 `json:"id"`
	ActionType          AdminActionType        `json:"action_type"`
	ActorType           ActorType              `json:"actor_type"`
	ActorID             string                 `json:"actor_id"`
	ActorIP             net.IP                 `json:"actor_ip,omitempty"`
	TargetType          string                 `json:"target_type,omitempty"`
	TargetID            string                 `json:"target_id,omitempty"`
	ActionDescription   string                 `json:"action_description"`
	ActionMetadata      map[string]interface{} `json:"action_metadata,omitempty"`
	CLICommand          string                 `json:"cli_command,omitempty"`
	CLIExitCode         *int                   `json:"cli_exit_code,omitempty"`
	CLIStdout           string                 `json:"cli_stdout,omitempty"`
	CLIStderr           string                 `json:"cli_stderr,omitempty"`
	CLIDurationMs       *int                   `json:"cli_duration_ms,omitempty"`
	Status              ActionStatus           `json:"status"`
	ErrorMessage        string                 `json:"error_message,omitempty"`
	SessionID           string                 `json:"session_id,omitempty"`
	RequestID           string                 `json:"request_id,omitempty"`
	Fingerprint         string                 `json:"fingerprint,omitempty"`
	CreatedAt           time.Time              `json:"created_at"`
	PreviousHash        string                 `json:"previous_hash,omitempty"`
}

// LogAdminAction logs an administrative action to the audit trail
func LogAdminAction(ctx context.Context, entry AdminAuditEntry) (*string, error) {
	metadataJSON := map[string]interface{}{}
	if entry.ActionMetadata != nil {
		metadataJSON = entry.ActionMetadata
	}
	
	metadataBytes, _ := json.Marshal(metadataJSON)
	
	var actorIP interface{}
	if entry.ActorIP != nil {
		actorIP = entry.ActorIP.String()
	} else {
		actorIP = nil
	}
	
	var cliExitCode interface{}
	if entry.CLIExitCode != nil {
		cliExitCode = *entry.CLIExitCode
	} else {
		cliExitCode = nil
	}
	
	var id string
	err := SystemPool.QueryRow(ctx, 
		`SELECT system.log_admin_action($1, $2, $3, $4::inet, $5, $6, $7, $8::jsonb, $9, $10, $11, $12, $13, $14)`,
		entry.ActionType,
		entry.ActorType,
		entry.ActorID,
		actorIP,
		entry.TargetType,
		entry.TargetID,
		entry.ActionDescription,
		metadataBytes,
		entry.CLICommand,
		cliExitCode,
		entry.Status,
		entry.ErrorMessage,
		entry.SessionID,
		entry.RequestID,
	).Scan(&id)
	
	if err != nil {
		// Log error but don't fail the operation - audit failure shouldn't block functionality
		log.Printf("[AdminAudit:Error] Failed to log admin action %s: %v", entry.ActionType, err)
		return nil, err
	}
	
	log.Printf("[AdminAudit] Logged %s action by %s (%s) - ID: %s", 
		entry.ActionType, entry.ActorType, entry.ActorID, id)
	
	return &id, nil
}

// LogCLICommand is a convenience function for logging CLI commands (like panic-reset)
func LogCLICommand(ctx context.Context, command string, args []string, exitCode int, stdout, stderr string, durationMs int, actorIP string) (*string, error) {
	// Truncate stdout/stderr if too long (prevent DB bloat)
	maxOutputLength := 10000
	if len(stdout) > maxOutputLength {
		stdout = stdout[:maxOutputLength] + "... [truncated]"
	}
	if len(stderr) > maxOutputLength {
		stderr = stderr[:maxOutputLength] + "... [truncated]"
	}
	
	fullCommand := command
	if len(args) > 0 {
		fullCommand = fmt.Sprintf("%s %s", command, joinArgs(args))
	}
	
	status := StatusSuccess
	if exitCode != 0 {
		status = StatusFailure
	}
	
	var ip net.IP
	if actorIP != "" {
		ip = net.ParseIP(actorIP)
	}
	
	entry := AdminAuditEntry{
		ActionType:        ActionCLICommand,
		ActorType:         ActorCLI,
		ActorID:           "panic-reset-cli", // Can be customized per CLI tool
		ActorIP:           ip,
		ActionDescription: fmt.Sprintf("CLI command executed: %s", command),
		ActionMetadata: map[string]interface{}{
			"args": args,
			"raw_command": fullCommand,
		},
		CLICommand:    fullCommand,
		CLIExitCode:   &exitCode,
		CLIStdout:     stdout,
		CLIStderr:     stderr,
		CLIDurationMs: &durationMs,
		Status:        status,
		CreatedAt:     time.Now(),
	}
	
	return LogAdminAction(ctx, entry)
}

// LogSecurityAction logs security-related actions (panic mode, IP blocks, etc)
func LogSecurityAction(ctx context.Context, actionType AdminActionType, actorID, actorIP, targetID, description string, metadata map[string]interface{}) (*string, error) {
	var ip net.IP
	if actorIP != "" {
		ip = net.ParseIP(actorIP)
	}
	
	entry := AdminAuditEntry{
		ActionType:        actionType,
		ActorType:         ActorUser,
		ActorID:           actorID,
		ActorIP:           ip,
		TargetType:        "project",
		TargetID:          targetID,
		ActionDescription: description,
		ActionMetadata:    metadata,
		Status:            StatusSuccess,
		CreatedAt:         time.Now(),
	}
	
	return LogAdminAction(ctx, entry)
}

// GetProjectAuditTrail retrieves audit trail for a project (compliance reporting)
func GetProjectAuditTrail(ctx context.Context, projectSlug string, startDate, endDate time.Time, limit int) ([]map[string]interface{}, error) {
	rows, err := SystemPool.Query(ctx,
		`SELECT * FROM system.get_project_audit_trail($1, $2, $3, $4)`,
		projectSlug, startDate, endDate, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	results := []map[string]interface{}{}
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			continue
		}
		row := make(map[string]interface{})
		for i, fd := range rows.FieldDescriptions() {
			if i < len(vals) {
				row[fd.Name] = vals[i]
			}
		}
		results = append(results, row)
	}
	return results, nil
}

// SearchAPILogs searches API logs with filters
func SearchAPILogs(ctx context.Context, projectSlug, clientIP string, statusCode *int, startDate, endDate time.Time, limit int) ([]map[string]interface{}, error) {
	var statusCodePtr interface{}
	if statusCode != nil {
		statusCodePtr = *statusCode
	}

	rows, err := SystemPool.Query(ctx,
		`SELECT * FROM system.search_api_logs($1, $2, $3, $4, $5, $6)`,
		projectSlug, clientIP, statusCodePtr, startDate, endDate, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	results := []map[string]interface{}{}
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			continue
		}
		row := make(map[string]interface{})
		for i, fd := range rows.FieldDescriptions() {
			if i < len(vals) {
				row[fd.Name] = vals[i]
			}
		}
		results = append(results, row)
	}
	return results, nil
}

// GetAPILogs retrieves API logs for a project
func GetAPILogs(ctx context.Context, projectSlug string, limit int) ([]map[string]interface{}, error) {
	rows, err := SystemPool.Query(ctx,
		`SELECT id, method, path, status_code, client_ip, duration_ms, user_role,
		        payload, headers, geo_info, response_size, created_at
		 FROM system.api_logs
		 WHERE project_slug = $1
		 ORDER BY created_at DESC
		 LIMIT $2`,
		projectSlug, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	results := []map[string]interface{}{}
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			continue
		}
		row := make(map[string]interface{})
		for i, fd := range rows.FieldDescriptions() {
			if i < len(vals) {
				row[fd.Name] = vals[i]
			}
		}
		results = append(results, row)
	}
	return results, nil
}

// PurgeOldLogs purges old logs based on retention policy
func PurgeOldLogs(ctx context.Context, projectSlug string, days int) (int, error) {
	var count int
	err := SystemPool.QueryRow(ctx, 
		`SELECT system.purge_old_logs($1, $2)`,
		projectSlug, days).Scan(&count)
	return count, err
}

// PurgeOldAdminLogs purges old admin audit logs
func PurgeOldAdminLogs(ctx context.Context, days int) (int, error) {
	var count int
	err := SystemPool.QueryRow(ctx, 
		`SELECT system.purge_old_admin_logs($1)`,
		days).Scan(&count)
	return count, err
}

// Helper function to join CLI args safely
func joinArgs(args []string) string {
	result := ""
	for _, arg := range args {
		if containsSpace(arg) {
			result += fmt.Sprintf(" \"%s\"", arg)
		} else {
			result += fmt.Sprintf(" %s", arg)
		}
	}
	return result
}

func containsSpace(s string) bool {
	for _, c := range s {
		if c == ' ' || c == '\t' || c == '\n' {
			return true
		}
	}
	return false
}

// InitAdminAudit initializes the admin audit system
func InitAdminAudit() {
	log.Println("[AdminAudit] Administrative audit trail system initialized")
}
