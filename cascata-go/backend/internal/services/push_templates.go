package services

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ============================================================================
// PUSH TEMPLATES SERVICE (I18N Support)
// ============================================================================

// PushTemplate represents a multi-language notification template
type PushTemplate struct {
	ID              string                 `json:"id"`
	ProjectSlug     string                 `json:"project_slug"`
	Code            string                 `json:"code"`
	Name            string                 `json:"name"`
	Description     string                 `json:"description"`
	ContentI18n     map[string]TemplateContent `json:"content_i18n"`
	DataPayload     map[string]interface{}   `json:"data_payload"`
	DefaultLanguage string                 `json:"default_language"`
	Active          bool                   `json:"active"`
	CreatedAt       time.Time              `json:"created_at"`
	UpdatedAt       time.Time              `json:"updated_at"`
}

// TemplateContent holds title and body for a specific language
type TemplateContent struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

// PushTemplateService handles template operations
type PushTemplateService struct {
	systemPool *pgxpool.Pool
}

// NewPushTemplateService creates a new template service
func NewPushTemplateService(systemPool *pgxpool.Pool) *PushTemplateService {
	return &PushTemplateService{systemPool: systemPool}
}

// ListTemplates returns all templates for a project
func (s *PushTemplateService) ListTemplates(ctx context.Context, projectSlug string) ([]PushTemplate, error) {
	rows, err := s.systemPool.Query(ctx, `
		SELECT id, project_slug, code, name, description, content_i18n, data_payload, 
		       default_language, active, created_at, updated_at
		FROM system.notification_templates
		WHERE project_slug = $1
		ORDER BY code ASC
	`, projectSlug)
	if err != nil {
		return nil, fmt.Errorf("failed to list templates: %w", err)
	}
	defer rows.Close()

	var templates []PushTemplate
	for rows.Next() {
		var t PushTemplate
		var contentJSON, dataJSON []byte
		err := rows.Scan(&t.ID, &t.ProjectSlug, &t.Code, &t.Name, &t.Description, 
			&contentJSON, &dataJSON, &t.DefaultLanguage, &t.Active, &t.CreatedAt, &t.UpdatedAt)
		if err != nil {
			continue
		}

		json.Unmarshal(contentJSON, &t.ContentI18n)
		json.Unmarshal(dataJSON, &t.DataPayload)
		templates = append(templates, t)
	}

	return templates, nil
}

// GetTemplate retrieves a template by ID or code
func (s *PushTemplateService) GetTemplate(ctx context.Context, projectSlug string, codeOrID string) (*PushTemplate, error) {
	var t PushTemplate
	var contentJSON, dataJSON []byte

	err := s.systemPool.QueryRow(ctx, `
		SELECT id, project_slug, code, name, description, content_i18n, data_payload,
		       default_language, active, created_at, updated_at
		FROM system.notification_templates
		WHERE project_slug = $1 AND (code = $2 OR id::text = $2)
		LIMIT 1
	`, projectSlug, codeOrID).Scan(
		&t.ID, &t.ProjectSlug, &t.Code, &t.Name, &t.Description,
		&contentJSON, &dataJSON, &t.DefaultLanguage, &t.Active, &t.CreatedAt, &t.UpdatedAt,
	)

	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, fmt.Errorf("template not found")
		}
		return nil, fmt.Errorf("failed to get template: %w", err)
	}

	json.Unmarshal(contentJSON, &t.ContentI18n)
	json.Unmarshal(dataJSON, &t.DataPayload)

	return &t, nil
}

// CreateTemplate creates a new template
func (s *PushTemplateService) CreateTemplate(ctx context.Context, t *PushTemplate) (*PushTemplate, error) {
	contentJSON, _ := json.Marshal(t.ContentI18n)
	dataJSON, _ := json.Marshal(t.DataPayload)

	err := s.systemPool.QueryRow(ctx, `
		INSERT INTO system.notification_templates 
		(project_slug, code, name, description, content_i18n, data_payload, default_language, active)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id, created_at, updated_at
	`, t.ProjectSlug, t.Code, t.Name, t.Description, contentJSON, dataJSON, t.DefaultLanguage, t.Active,
	).Scan(&t.ID, &t.CreatedAt, &t.UpdatedAt)

	if err != nil {
		return nil, fmt.Errorf("failed to create template: %w", err)
	}

	return t, nil
}

// UpdateTemplate updates an existing template
func (s *PushTemplateService) UpdateTemplate(ctx context.Context, t *PushTemplate) (*PushTemplate, error) {
	contentJSON, _ := json.Marshal(t.ContentI18n)
	dataJSON, _ := json.Marshal(t.DataPayload)

	err := s.systemPool.QueryRow(ctx, `
		UPDATE system.notification_templates
		SET code = $3, name = $4, description = $5, content_i18n = $6, 
		    data_payload = $7, default_language = $8, active = $9, updated_at = NOW()
		WHERE id = $1 AND project_slug = $2
		RETURNING updated_at
	`, t.ID, t.ProjectSlug, t.Code, t.Name, t.Description, contentJSON, dataJSON, t.DefaultLanguage, t.Active,
	).Scan(&t.UpdatedAt)

	if err != nil {
		return nil, fmt.Errorf("failed to update template: %w", err)
	}

	return t, nil
}

// DeleteTemplate deletes a template
func (s *PushTemplateService) DeleteTemplate(ctx context.Context, templateID, projectSlug string) error {
	_, err := s.systemPool.Exec(ctx, `
		DELETE FROM system.notification_templates 
		WHERE id = $1 AND project_slug = $2
	`, templateID, projectSlug)
	return err
}

// RenderTemplate renders a template with given language and variables
func (s *PushTemplateService) RenderTemplate(ctx context.Context, projectSlug, templateCode, language string, variables map[string]interface{}) (*TemplateContent, map[string]interface{}, error) {
	template, err := s.GetTemplate(ctx, projectSlug, templateCode)
	if err != nil {
		return nil, nil, err
	}

	// Select language content
	content, exists := template.ContentI18n[language]
	if !exists {
		// Fallback to default language
		content, exists = template.ContentI18n[template.DefaultLanguage]
		if !exists {
			return nil, nil, fmt.Errorf("no content available for language %s or default %s", language, template.DefaultLanguage)
		}
	}

	// Apply variable substitution
	title := s.applyVariables(content.Title, variables)
	body := s.applyVariables(content.Body, variables)

	// Prepare data payload with variable substitution
	renderedData := make(map[string]interface{})
	for k, v := range template.DataPayload {
		if str, ok := v.(string); ok {
			renderedData[k] = s.applyVariables(str, variables)
		} else {
			renderedData[k] = v
		}
	}

	return &TemplateContent{Title: title, Body: body}, renderedData, nil
}

// applyVariables replaces {{variable}} placeholders
func (s *PushTemplateService) applyVariables(text string, variables map[string]interface{}) string {
	result := text
	for key, value := range variables {
		placeholder := fmt.Sprintf("{{%s}}", key)
		strValue := ""
		if value != nil {
			strValue = fmt.Sprintf("%v", value)
		}
		result = replaceString(result, placeholder, strValue)
	}
	return result
}

func replaceString(s, old, new string) string {
	// Simple implementation - can be optimized
	result := ""
	i := 0
	for i < len(s) {
		if i+len(old) <= len(s) && s[i:i+len(old)] == old {
			result += new
			i += len(old)
		} else {
			result += string(s[i])
			i++
		}
	}
	return result
}
