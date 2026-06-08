package controllers

import (
	"encoding/json"
	"net/http"

	"cascata-backend/internal/services"
)

// QueueController expõe métricas e operações da fila de automações
type QueueController struct{}

// NewQueueController cria um novo controller
func NewQueueController() *QueueController {
	return &QueueController{}
}

// GetQueueStats retorna estatísticas da fila de automações
func (c *QueueController) GetQueueStats(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	
	stats, err := services.GetAutomationQueueStats(ctx)
	if err != nil {
		http.Error(w, `{"error":"Failed to get queue stats"}`, http.StatusInternalServerError)
		return
	}
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}

// RequeueDLQHandler reprocessa um job da DLQ
func (c *QueueController) RequeueDLQHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"Method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	
	var req struct {
		JobID string `json:"job_id"`
	}
	
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"Invalid request body"}`, http.StatusBadRequest)
		return
	}
	
	if req.JobID == "" {
		http.Error(w, `{"error":"job_id is required"}`, http.StatusBadRequest)
		return
	}
	
	ctx := r.Context()
	if err := services.RequeueDLQJob(ctx, req.JobID); err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// HealthCheck retorna status da fila
func (c *QueueController) HealthCheck(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	
	stats, err := services.GetAutomationQueueStats(ctx)
	if err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "unhealthy",
			"error":  err.Error(),
		})
		return
	}
	
	status := "healthy"
	if stats.DLQCount > 100 {
		status = "warning"
	}
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":        status,
		"high_pending":  stats.HighPending,
		"normal_pending": stats.NormalPending,
		"low_pending":   stats.LowPending,
		"dlq_count":     stats.DLQCount,
		"processing":    stats.Processing,
	})
}
