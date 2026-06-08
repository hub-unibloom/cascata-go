package controllers

import (
	"encoding/json"
	"net/http"

	"cascata-backend/internal/middleware"
	"cascata-backend/internal/services"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PushController handles all push notification endpoints
type PushController struct {
	pushService     *services.PushService
	templateService *services.PushTemplateService
	bulkService     *services.PushBulkService
}

// NewPushController creates a new push controller
func NewPushController(systemPool *pgxpool.Pool) *PushController {
	return &PushController{
		pushService:     services.NewPushService(systemPool),
		templateService: services.NewPushTemplateService(systemPool),
		bulkService:     services.NewPushBulkService(systemPool),
	}
}

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

// RegisterDevice registers a device token
func (pc *PushController) RegisterDevice(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	projectPool := middleware.GetProjectPool(r)
	if projectPool == nil {
		writeError(w, http.StatusBadRequest, "Project not found")
		return
	}

	var req struct {
		Token      string `json:"token"`
		Platform   string `json:"platform"`
		AppVersion string `json:"app_version"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Token == "" {
		writeError(w, http.StatusBadRequest, "token is required")
		return
	}

	userID, ok := middleware.GetUserID(ctx)
	if !ok {
		writeError(w, http.StatusUnauthorized, "User not authenticated")
		return
	}

	if err := pc.pushService.RegisterDevice(ctx, projectPool, userID, req.Token, req.Platform, req.AppVersion); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

// UnregisterDevice unregisters a device token
func (pc *PushController) UnregisterDevice(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	projectPool := middleware.GetProjectPool(r)
	if projectPool == nil {
		writeError(w, http.StatusBadRequest, "Project not found")
		return
	}

	var req struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Token == "" {
		writeError(w, http.StatusBadRequest, "token is required")
		return
	}

	userID, ok := middleware.GetUserID(ctx)
	if !ok {
		writeError(w, http.StatusUnauthorized, "User not authenticated")
		return
	}

	if err := pc.pushService.UnregisterDevice(ctx, projectPool, userID, req.Token); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

// ListDevices lists all registered devices
func (pc *PushController) ListDevices(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	projectPool := middleware.GetProjectPool(r)
	if projectPool == nil {
		writeError(w, http.StatusBadRequest, "Project not found")
		return
	}

	devices, err := pc.pushService.ListDevices(ctx, projectPool)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, devices)
}

// SendPush sends a push notification to a specific user
func (pc *PushController) SendPush(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	projectSlug := middleware.GetProjectSlug(ctx)

	var req struct {
		UserID     string                 `json:"user_id"`
		Title      string                 `json:"title"`
		Body       string                 `json:"body"`
		Data       map[string]interface{} `json:"data"`
		TemplateID string                 `json:"template_id"`
		Language   string                 `json:"language"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.UserID == "" || req.Title == "" || req.Body == "" {
		writeError(w, http.StatusBadRequest, "user_id, title and body are required")
		return
	}

	userRole, _ := middleware.GetUserRole(ctx)
	if userRole != "service_role" {
		writeError(w, http.StatusForbidden, "Only service_role can send push notifications")
		return
	}

	fcmConfig, err := pc.pushService.GetFCMConfig(ctx, projectSlug)
	if err != nil {
		writeError(w, http.StatusBadRequest, "FCM not configured: "+err.Error())
		return
	}

	notification := map[string]interface{}{
		"title": req.Title,
		"body":  req.Body,
		"data":  req.Data,
	}

	if req.TemplateID != "" {
		template, err := pc.templateService.GetTemplate(ctx, projectSlug, req.TemplateID)
		if err == nil {
			lang := req.Language
			if lang == "" {
				lang = template.DefaultLanguage
			}
			content, data, _ := pc.templateService.RenderTemplate(ctx, projectSlug, template.Code, lang, req.Data)
			if content != nil {
				notification["title"] = content.Title
				notification["body"] = content.Body
				notification["data"] = data
			}
		}
	}

	if err := pc.pushService.QueuePushNotification(ctx, projectSlug, req.UserID, notification, fcmConfig); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"success": true, "status": "queued"})
}

// SendBulkPush sends push notifications to multiple users
func (pc *PushController) SendBulkPush(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	projectSlug := middleware.GetProjectSlug(ctx)

	var req struct {
		UserIDs    []string               `json:"user_ids"`
		GroupID    string                 `json:"group_id"`
		Title      string                 `json:"title"`
		Body       string                 `json:"body"`
		Data       map[string]interface{} `json:"data"`
		TemplateID string                 `json:"template_id"`
		Language   string                 `json:"language"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Title == "" || req.Body == "" {
		writeError(w, http.StatusBadRequest, "title and body are required")
		return
	}

	userRole, _ := middleware.GetUserRole(ctx)
	if userRole != "service_role" {
		writeError(w, http.StatusForbidden, "Only service_role can send bulk push")
		return
	}

	var userIDs []string
	if req.GroupID != "" {
		members, err := pc.bulkService.GetGroupMembers(ctx, req.GroupID)
		if err != nil {
			writeError(w, http.StatusBadRequest, "Group not found")
			return
		}
		userIDs = members
	} else {
		userIDs = req.UserIDs
	}

	if len(userIDs) == 0 {
		writeError(w, http.StatusBadRequest, "No target users specified")
		return
	}

	fcmConfig, err := pc.pushService.GetFCMConfig(ctx, projectSlug)
	if err != nil {
		writeError(w, http.StatusBadRequest, "FCM not configured")
		return
	}

	notification := map[string]interface{}{
		"title": req.Title,
		"body":  req.Body,
		"data":  req.Data,
	}

	if req.TemplateID != "" {
		template, err := pc.templateService.GetTemplate(ctx, projectSlug, req.TemplateID)
		if err == nil {
			lang := req.Language
			if lang == "" {
				lang = template.DefaultLanguage
			}
			content, data, _ := pc.templateService.RenderTemplate(ctx, projectSlug, template.Code, lang, req.Data)
			if content != nil {
				notification["title"] = content.Title
				notification["body"] = content.Body
				notification["data"] = data
			}
		}
	}

	if err := pc.pushService.QueueBulkPush(ctx, projectSlug, userIDs, notification, fcmConfig); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"success": true, "status": "queued", "recipients": len(userIDs)})
}

// ListRules lists all notification rules
func (pc *PushController) ListRules(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	projectSlug := middleware.GetProjectSlug(ctx)

	rules, err := pc.pushService.ListNotificationRules(ctx, projectSlug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, rules)
}

// CreateRule creates a new notification rule
func (pc *PushController) CreateRule(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	projectSlug := middleware.GetProjectSlug(ctx)

	var rule services.NotificationRule
	if err := json.NewDecoder(r.Body).Decode(&rule); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	rule.ProjectSlug = projectSlug

	created, err := pc.pushService.CreateNotificationRule(ctx, &rule)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, created)
}

// DeleteRule deletes a notification rule
func (pc *PushController) DeleteRule(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	projectSlug := middleware.GetProjectSlug(ctx)
	ruleID := chi.URLParam(r, "id")

	if err := pc.pushService.DeleteNotificationRule(ctx, ruleID, projectSlug); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

// ListTemplates lists all push templates
func (pc *PushController) ListTemplates(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	projectSlug := middleware.GetProjectSlug(ctx)

	templates, err := pc.templateService.ListTemplates(ctx, projectSlug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, templates)
}

// CreateTemplate creates a new push template
func (pc *PushController) CreateTemplate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	projectSlug := middleware.GetProjectSlug(ctx)

	var template services.PushTemplate
	if err := json.NewDecoder(r.Body).Decode(&template); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	template.ProjectSlug = projectSlug

	created, err := pc.templateService.CreateTemplate(ctx, &template)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, created)
}

// UpdateTemplate updates a push template
func (pc *PushController) UpdateTemplate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	projectSlug := middleware.GetProjectSlug(ctx)
	templateID := chi.URLParam(r, "id")

	var template services.PushTemplate
	if err := json.NewDecoder(r.Body).Decode(&template); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	template.ID = templateID
	template.ProjectSlug = projectSlug

	updated, err := pc.templateService.UpdateTemplate(ctx, &template)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, updated)
}

// DeleteTemplate deletes a push template
func (pc *PushController) DeleteTemplate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	projectSlug := middleware.GetProjectSlug(ctx)
	templateID := chi.URLParam(r, "id")

	if err := pc.templateService.DeleteTemplate(ctx, templateID, projectSlug); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

// ListGroups lists all notification groups
func (pc *PushController) ListGroups(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	projectSlug := middleware.GetProjectSlug(ctx)

	groups, err := pc.bulkService.ListGroups(ctx, projectSlug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, groups)
}

// CreateGroup creates a new notification group
func (pc *PushController) CreateGroup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	projectSlug := middleware.GetProjectSlug(ctx)

	var group services.NotificationGroup
	if err := json.NewDecoder(r.Body).Decode(&group); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	group.ProjectSlug = projectSlug

	created, err := pc.bulkService.CreateGroup(ctx, &group)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, created)
}

// UpdateGroup updates a notification group
func (pc *PushController) UpdateGroup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	projectSlug := middleware.GetProjectSlug(ctx)
	groupID := chi.URLParam(r, "id")

	var group services.NotificationGroup
	if err := json.NewDecoder(r.Body).Decode(&group); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	group.ID = groupID
	group.ProjectSlug = projectSlug

	updated, err := pc.bulkService.UpdateGroup(ctx, &group)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, updated)
}

// DeleteGroup deletes a notification group
func (pc *PushController) DeleteGroup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	projectSlug := middleware.GetProjectSlug(ctx)
	groupID := chi.URLParam(r, "id")

	if err := pc.bulkService.DeleteGroup(ctx, groupID, projectSlug); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

// SyncGroup synchronizes group members
func (pc *PushController) SyncGroup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	projectPool := middleware.GetProjectPool(r)
	if projectPool == nil {
		writeError(w, http.StatusBadRequest, "Project not found")
		return
	}
	groupID := chi.URLParam(r, "id")

	if err := pc.bulkService.SyncGroup(ctx, projectPool, groupID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

// ListCampaigns lists all push campaigns
func (pc *PushController) ListCampaigns(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	projectSlug := middleware.GetProjectSlug(ctx)

	campaigns, err := pc.bulkService.ListCampaigns(ctx, projectSlug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, campaigns)
}

// CreateCampaign creates a new push campaign
func (pc *PushController) CreateCampaign(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	projectSlug := middleware.GetProjectSlug(ctx)

	var campaign services.NotificationCampaign
	if err := json.NewDecoder(r.Body).Decode(&campaign); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	campaign.ProjectSlug = projectSlug

	if userID, ok := middleware.GetUserID(ctx); ok {
		campaign.CreatedBy = &userID
	}

	created, err := pc.bulkService.CreateCampaign(ctx, &campaign)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, created)
}

// CancelCampaign cancels a pending campaign
func (pc *PushController) CancelCampaign(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	projectSlug := middleware.GetProjectSlug(ctx)
	campaignID := chi.URLParam(r, "id")

	if err := pc.bulkService.CancelCampaign(ctx, campaignID, projectSlug); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

// ListHistory lists notification history
func (pc *PushController) ListHistory(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	projectSlug := middleware.GetProjectSlug(ctx)

	history, err := pc.pushService.ListHistory(ctx, projectSlug, 50)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, history)
}

// GetStats returns push notification statistics
func (pc *PushController) GetStats(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	projectSlug := middleware.GetProjectSlug(ctx)

	stats, err := pc.pushService.GetStats(ctx, projectSlug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, stats)
}

// GetFCMConfig returns FCM configuration
func (pc *PushController) GetFCMConfig(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	projectSlug := middleware.GetProjectSlug(ctx)

	config, err := pc.pushService.GetFCMConfig(ctx, projectSlug)
	if err != nil {
		// Retorna 200 com objeto vazio para não quebrar frontend em projetos novos
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"project_id":   "",
			"client_email": "",
			"configured":   false,
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"project_id":   config.ProjectID,
		"client_email": config.ClientEmail,
		"configured":   true,
	})
}

// SaveFCMConfig saves FCM configuration
func (pc *PushController) SaveFCMConfig(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	projectSlug := middleware.GetProjectSlug(ctx)

	var config services.FCMConfig
	if err := json.NewDecoder(r.Body).Decode(&config); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if config.ProjectID == "" || config.ClientEmail == "" || config.PrivateKey == "" {
		writeError(w, http.StatusBadRequest, "project_id, client_email, and private_key are required")
		return
	}

	if err := pc.pushService.SaveFCMConfig(ctx, projectSlug, &config, true); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}
