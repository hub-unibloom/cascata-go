package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"cascata-backend/internal/types"
)

// OTLPService handles log export via OTLP protocol
type OTLPService struct {
	httpClient        *http.Client
	collectorEndpoint string
}

// NewOTLPService creates a new OTLP service
func NewOTLPService() *OTLPService {
	endpoint := os.Getenv("OTEL_COLLECTOR_ENDPOINT")
	if endpoint == "" {
		endpoint = "http://otel-collector:4318"
	}

	return &OTLPService{
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		collectorEndpoint: endpoint,
	}
}

// LogRecord represents a single log entry in OTLP format
type LogRecord struct {
	Timestamp  int64                  `json:"timestamp"`
	Severity   string                 `json:"severity_text,omitempty"`
	Body       map[string]interface{} `json:"body"`
	Attributes map[string]interface{} `json:"attributes,omitempty"`
}

// ExportLogsRequest represents the OTLP logs export request
type ExportLogsRequest struct {
	ResourceLogs []ResourceLog `json:"resourceLogs"`
}

type ResourceLog struct {
	Resource  Resource   `json:"resource"`
	ScopeLogs []ScopeLog `json:"scopeLogs"`
}

type Resource struct {
	Attributes []Attribute `json:"attributes"`
}

type Attribute struct {
	Key   string         `json:"key"`
	Value AttributeValue `json:"value"`
}

type AttributeValue struct {
	StringValue string `json:"stringValue,omitempty"`
}

type ScopeLog struct {
	Scope      Scope       `json:"scope"`
	LogRecords []LogRecord `json:"logRecords"`
}

type Scope struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// AuditLogEntry represents an internal audit log entry for export
type AuditLogEntry struct {
	ID           string                 `json:"id"`
	ProjectSlug  string                 `json:"project_slug"`
	Method       string                 `json:"method"`
	Path         string                 `json:"path"`
	StatusCode   int                    `json:"status_code"`
	ClientIP     string                 `json:"client_ip"`
	DurationMs   int64                  `json:"duration_ms"`
	UserRole     string                 `json:"user_role"`
	Payload      map[string]interface{} `json:"payload,omitempty"`
	Headers      map[string]interface{} `json:"headers,omitempty"`
	GeoInfo      map[string]interface{} `json:"geo_info,omitempty"`
	ResponseSize int                    `json:"response_size"`
	CreatedAt    time.Time              `json:"created_at"`
}

// SendLogsToCollector sends logs to OTel Collector with project headers
func (s *OTLPService) SendLogsToCollector(ctx context.Context, projectSlug string, apiKey string, entries []AuditLogEntry) error {
	if len(entries) == 0 {
		return nil
	}

	req := s.buildExportRequest(projectSlug, entries)

	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("failed to marshal OTLP request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST",
		s.collectorEndpoint+"/v1/logs",
		bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create HTTP request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-Project-Slug", projectSlug)
	httpReq.Header.Set("X-API-Key", apiKey)

	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("failed to send logs to collector: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("collector returned error: %d - %s", resp.StatusCode, string(bodyBytes))
	}

	log.Printf("[OTLP] Successfully sent %d logs for project %s", len(entries), projectSlug)
	return nil
}

// buildExportRequest creates an OTLP ExportLogsRequest from audit entries
func (s *OTLPService) buildExportRequest(projectSlug string, entries []AuditLogEntry) ExportLogsRequest {
	logRecords := make([]LogRecord, 0, len(entries))

	for _, entry := range entries {
		body := map[string]interface{}{
			"message": fmt.Sprintf("%s %s %d", entry.Method, entry.Path, entry.StatusCode),
			"method":  entry.Method,
			"path":    entry.Path,
			"status":  entry.StatusCode,
		}

		attrs := map[string]interface{}{
			"project_slug":  entry.ProjectSlug,
			"client_ip":     entry.ClientIP,
			"user_role":     entry.UserRole,
			"duration_ms":   entry.DurationMs,
			"response_size": entry.ResponseSize,
		}

		if entry.GeoInfo != nil {
			for k, v := range entry.GeoInfo {
				attrs["geo."+k] = v
			}
		}

		logRecords = append(logRecords, LogRecord{
			Timestamp:  entry.CreatedAt.UnixNano(),
			Severity:   s.mapStatusToSeverity(entry.StatusCode),
			Body:       body,
			Attributes: attrs,
		})
	}

	return ExportLogsRequest{
		ResourceLogs: []ResourceLog{
			{
				Resource: Resource{
					Attributes: []Attribute{
						{Key: "service.name", Value: AttributeValue{StringValue: "cascata"}},
						{Key: "project.slug", Value: AttributeValue{StringValue: projectSlug}},
					},
				},
				ScopeLogs: []ScopeLog{
					{
						Scope: Scope{
							Name:    "cascata-audit-logs",
							Version: "1.0.0",
						},
						LogRecords: logRecords,
					},
				},
			},
		},
	}
}

// mapStatusToSeverity maps HTTP status code to log severity
func (s *OTLPService) mapStatusToSeverity(statusCode int) string {
	switch {
	case statusCode >= 500:
		return "ERROR"
	case statusCode >= 400:
		return "WARN"
	default:
		return "INFO"
	}
}

// ExportLogsNative exports logs directly to a native endpoint
func (s *OTLPService) ExportLogsNative(ctx context.Context, endpoint string, apiKey string, headers map[string]string, entries []AuditLogEntry) error {
	if len(entries) == 0 {
		return nil
	}

	req := s.buildExportRequest("native", entries)

	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("failed to marshal OTLP request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", endpoint+"/v1/logs", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create HTTP request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)

	for k, v := range headers {
		httpReq.Header.Set(k, v)
	}

	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("failed to send logs to native endpoint: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("native endpoint returned error: %d - %s", resp.StatusCode, string(bodyBytes))
	}

	return nil
}

// WriteDeadLetter writes failed logs to dead letter queue
func (s *OTLPService) WriteDeadLetter(projectSlug string, entries []AuditLogEntry, deadLetterPath string) error {
	if deadLetterPath == "" {
		deadLetterPath = "/var/log/cascata/deadletter"
	}

	if err := os.MkdirAll(deadLetterPath, 0755); err != nil {
		return fmt.Errorf("failed to create dead letter directory: %w", err)
	}

	timestamp := time.Now().UTC().Format("20060102_150405")
	filename := fmt.Sprintf("%s/deadletter_%s_%s.jsonl", deadLetterPath, projectSlug, timestamp)

	file, err := os.OpenFile(filename, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("failed to open dead letter file: %w", err)
	}
	defer file.Close()

	for _, entry := range entries {
		line, err := json.Marshal(entry)
		if err != nil {
			log.Printf("[OTLP:Error] Failed to marshal dead letter entry: %v", err)
			continue
		}
		if _, err := file.WriteString(string(line) + "\n"); err != nil {
			log.Printf("[OTLP:Error] Failed to write dead letter entry: %v", err)
		}
	}

	log.Printf("[OTLP] Written %d entries to dead letter: %s", len(entries), filename)
	return nil
}

// ValidateLogExportConfig validates the export configuration
func (s *OTLPService) ValidateLogExportConfig(config types.LogExportConfig) error {
	if !config.Enabled {
		return nil
	}

	if config.Mode != types.LogExportModeSidecar && config.Mode != types.LogExportModeNative {
		return fmt.Errorf("invalid export mode: %s", config.Mode)
	}

	if config.APIKey == "" {
		return fmt.Errorf("API key is required for log export")
	}

	for _, exporter := range config.Exporters {
		if !exporter.Enabled {
			continue
		}

		switch exporter.Provider {
		case types.ProviderDatadog, types.ProviderSplunk, types.ProviderLoki, types.ProviderELK, types.ProviderS3, types.ProviderOTLP:
			// Valid providers
		default:
			return fmt.Errorf("invalid provider: %s", exporter.Provider)
		}

		if config.Mode == types.LogExportModeNative && exporter.Endpoint == "" {
			return fmt.Errorf("endpoint is required for native mode exporter: %s", exporter.Name)
		}
	}

	return nil
}

// GenerateAPIKey generates a new API key for log export
func (s *OTLPService) GenerateAPIKey() string {
	return "cascata_le_" + generateRandomString(32)
}

func generateRandomString(length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, length)
	for i := range b {
		b[i] = charset[time.Now().UnixNano()%int64(len(charset))]
	}
	return string(b)
}
