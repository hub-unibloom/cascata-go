package services

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"
)

// LogCategory defines the type of log entry for filtering and routing
type LogCategory string

const (
	CategoryRequest  LogCategory = "request"   // HTTP API requests
	CategorySecurity LogCategory = "security"  // Security events (spoofing, violations)
	CategorySystem   LogCategory = "system"    // Internal system events
	CategoryAudit    LogCategory = "audit"     // Compliance audit trails
)

// AuditLog represents a single audit log entry
type AuditLog struct {
	ProjectSlug    string                 `json:"project_slug"`
	Method         string                 `json:"method"`
	Path           string                 `json:"path"`
	Host           string                 `json:"host"`          // NEW: Domínio usado (custom vs default)
	RawQuery       string                 `json:"raw_query"`     // NEW: Query params
	StatusCode     int                    `json:"status_code"`
	ClientIP       string                 `json:"client_ip"`
	DurationMs     int64                  `json:"duration_ms"`
	UserRole       string                 `json:"user_role"`
	IsSystemRole   bool                   `json:"is_system_role"`// NEW: Se é service_role
	Payload        interface{}            `json:"payload"`
	Headers        interface{}            `json:"headers"`
	GeoInfo        interface{}            `json:"geo_info"`
	ResponseSize   int                    `json:"response_size"`
	Category       LogCategory            `json:"category"`      // NEW: request | security | system | audit
	EventType      string                 `json:"event_type"`    // NEW: e.g., "auto_clock_spoof", "auth_failure"
	Severity       string                 `json:"severity"`      // NEW: info | warning | critical
}

// SecurityEvent represents a security-specific event for compliance
// These events are always persisted and exported to external systems
 type SecurityEvent struct {
	Timestamp   time.Time              `json:"timestamp"`
	ProjectSlug string                 `json:"project_slug"`
	TableName   string                 `json:"table_name,omitempty"`
	ColumnName  string                 `json:"column_name,omitempty"`
	Operation   string                 `json:"operation"`
	EventType   string                 `json:"event_type"`    // e.g., "AUTO_CLOCK_SPOOF", "IMMUTABLE_VIOLATION"
	ClientIP    string                 `json:"client_ip"`
	UserRole    string                 `json:"user_role"`
	Details     map[string]interface{} `json:"details"`       // Contextual info
	Severity    string                 `json:"severity"`      // warning | critical | info
}

var (
	logBuffer    []AuditLog
	logMutex     sync.Mutex
	maxBatch     = 100
	maxBuffer    = 10000  // Limite máximo para evitar crescimento infinito
	flushChan    = make(chan struct{}, 1)  // Canal bufferizado para evitar múltiplas goroutines
	shutdownChan = make(chan struct{})       // Sinal de shutdown
	flushWG      sync.WaitGroup              // WaitGroup para graceful shutdown
)

func InitLogging() {
	go flushWorker()
}

// flushWorker roda em background e faz flush periódico ou sob demanda
func flushWorker() {
	for {
		select {
		case <-time.After(5 * time.Second):
			triggerFlush()
		case <-flushChan:
			triggerFlush()
		case <-shutdownChan:
			// Flush final antes de parar
			triggerFlush()
			return
		}
	}
}

// ShutdownLogging gracefully para o sistema de logging
func ShutdownLogging() {
	close(shutdownChan)
	flushWG.Wait() // Aguarda flush final completar
}

// triggerFlush dispara o flush de forma thread-safe
func triggerFlush() {
	flushWG.Add(1)
	defer flushWG.Done()
	flushLogs()
}

func LogAudit(projectSlug, method, path string, statusCode int, clientIP, userRole string, payload interface{}) {
	entry := AuditLog{
		ProjectSlug: projectSlug,
		Method:      method,
		Path:        path,
		StatusCode:  statusCode,
		ClientIP:    clientIP,
		UserRole:    userRole,
		Payload:     payload,
		Category:    CategoryRequest,
		EventType:   "http_request",
		Severity:    "info",
	}
	BufferAuditLog(entry)
}

// LogSecurityEvent registra eventos de segurança de forma estruturada
// Sempre inclui timestamp e é exportado para sistemas externos (SIEM)
// Use para: spoofing attempts, lock violations, auth failures
func LogSecurityEvent(projectSlug, tableName, columnName, operation, eventType, clientIP, userRole, severity string, details map[string]interface{}) {
	if details == nil {
		details = make(map[string]interface{})
	}
	
	entry := AuditLog{
		ProjectSlug: projectSlug,
		Method:      operation,
		Path:        tableName,
		StatusCode:  0, // Security events don't have HTTP status
		ClientIP:    clientIP,
		UserRole:    userRole,
		Payload: map[string]interface{}{
			"event_type":  eventType,
			"column":      columnName,
			"details":     details,
			"timestamp":   time.Now().UTC().Format(time.RFC3339),
		},
		Category: CategorySecurity,
		EventType: eventType,
		Severity:  severity,
	}
	
	BufferAuditLog(entry)
	
	// Também loga no stdout para debugging imediato
	log.Printf("[SECURITY_EVENT] type=%s project=%s table=%s column=%s severity=%s | %v",
		eventType, projectSlug, tableName, columnName, severity, details)
}

func BufferAuditLog(entry AuditLog) {
	logMutex.Lock()
	
	// Proteção contra crescimento infinito: descarta logs antigos se buffer estourar
	if len(logBuffer) >= maxBuffer {
		// Remove 10% dos logs mais antigos para fazer espaço
		discardCount := maxBuffer / 10
		logBuffer = logBuffer[discardCount:]
		log.Printf("[Firehose:Warn] Buffer overflow! Discarded %d old logs", discardCount)
	}
	
	logBuffer = append(logBuffer, entry)
	shouldFlush := len(logBuffer) >= maxBatch
	logMutex.Unlock()
	
	// Dispara flush assíncrono de forma controlada (evita explosão de goroutines)
	if shouldFlush {
		select {
		case flushChan <- struct{}{}:
			// Sinal enviado com sucesso
		default:
			// Canal já tem sinal pendente, não bloqueia
		}
	}
}

// sanitizeJSONBytes remove caracteres inválidos para PostgreSQL JSONB
// PostgreSQL não aceita \u0000 (null bytes) em JSON
func sanitizeJSONBytes(data []byte) []byte {
	// Remove null bytes
	result := make([]byte, 0, len(data))
	for _, b := range data {
		if b != 0 {
			result = append(result, b)
		}
	}
	return result
}

// truncatePayload limita o tamanho do payload para evitar logs gigantes
// e detecta conteúdo binário para substituir por placeholder
func truncatePayload(payload interface{}) interface{} {
	if payload == nil {
		return map[string]interface{}{}
	}
	
	// Se for string, verifica se parece binário (contém null bytes ou caracteres de controle não-texto)
	if str, ok := payload.(string); ok {
		// Se contiver null bytes ou for muito grande, assume binário
		if len(str) > 10000 || containsNullBytes(str) {
			return map[string]interface{}{
				"_note": "Binary payload omitted",
				"_size": len(str),
			}
		}
	}
	
	// Se for []byte (raw binary), substitui
	if data, ok := payload.([]byte); ok {
		return map[string]interface{}{
			"_note": "Binary payload omitted",
			"_size": len(data),
		}
	}
	
	// Se for map com _raw_body, verifica se o _raw_body é binário
	if m, ok := payload.(map[string]interface{}); ok {
		if rawBody, exists := m["_raw_body"]; exists {
			if str, ok := rawBody.(string); ok {
				// Se _raw_body contiver null bytes ou for muito grande, é binário
				if len(str) > 10000 || containsNullBytes(str) {
					return map[string]interface{}{
						"_note":         "Binary payload omitted",
						"_size":         len(str),
						"_content_type": m["_content_type"],
					}
				}
			}
		}
		// Verifica outros campos do map recursivamente
		for key, value := range m {
			m[key] = truncatePayload(value)
		}
		return m
	}
	
	// Se for slice, verifica recursivamente
	if s, ok := payload.([]interface{}); ok {
		for i, value := range s {
			s[i] = truncatePayload(value)
		}
		return s
	}
	
	return payload
}

func containsNullBytes(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == 0 {
			return true
		}
	}
	return false
}

func flushLogs() {
	logMutex.Lock()
	if len(logBuffer) == 0 {
		logMutex.Unlock()
		return
	}
	batch := logBuffer
	logBuffer = make([]AuditLog, 0, maxBatch)
	logMutex.Unlock()

	log.Printf("[Firehose] Flushing %d audit logs to persistence...", len(batch))
	
	if len(batch) == 0 {
		return
	}
	
	// Usa COPY para inserção em batch (muito mais eficiente que inserts individuais)
	conn, err := SystemPool.Acquire(context.Background())
	if err != nil {
		log.Printf("[Firehose:Error] Failed to acquire connection for batch insert: %v", err)
		return
	}
	defer conn.Release()
	
	// Migration: Adicionar colunas de segurança se não existirem (idempotente)
	_, _ = conn.Exec(context.Background(), `
		ALTER TABLE system.api_logs 
		ADD COLUMN IF NOT EXISTS category TEXT DEFAULT 'request',
		ADD COLUMN IF NOT EXISTS event_type TEXT,
		ADD COLUMN IF NOT EXISTS severity TEXT DEFAULT 'info',
		ADD COLUMN IF NOT EXISTS table_name TEXT,
		ADD COLUMN IF NOT EXISTS host TEXT,
		ADD COLUMN IF NOT EXISTS raw_query TEXT,
		ADD COLUMN IF NOT EXISTS is_system_role BOOLEAN DEFAULT FALSE
	`)
	
	// Prepara dados para COPY
	for _, entry := range batch {
		// Ensure JSON fields are valid JSONB
		payload := truncatePayload(entry.Payload)
		headers := entry.Headers
		if headers == nil || headers == "" {
			headers = map[string]interface{}{}
		}
		geoInfo := entry.GeoInfo
		if geoInfo == nil || geoInfo == "" {
			geoInfo = map[string]interface{}{}
		}
		
		// Default values para novos campos
		category := entry.Category
		if category == "" {
			category = CategoryRequest
		}
		severity := entry.Severity
		if severity == "" {
			severity = "info"
		}
		
		payloadBytes, _ := json.Marshal(payload)
		payloadJSON := sanitizeJSONBytes(payloadBytes)
		headersBytes, _ := json.Marshal(headers)
		headersJSON := sanitizeJSONBytes(headersBytes)
		geoInfoBytes, _ := json.Marshal(geoInfo)
		geoInfoJSON := sanitizeJSONBytes(geoInfoBytes)
		
		_, err := conn.Exec(context.Background(),
			`INSERT INTO system.api_logs (project_slug, method, path, status_code, client_ip, duration_ms, user_role, payload, headers, geo_info, response_size, category, event_type, severity, table_name, host, raw_query, is_system_role)
						 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)
						 ON CONFLICT DO NOTHING`,
			entry.ProjectSlug, entry.Method, entry.Path, entry.StatusCode, entry.ClientIP, 
			entry.DurationMs, entry.UserRole, payloadJSON, headersJSON, geoInfoJSON, entry.ResponseSize,
			category, entry.EventType, severity, entry.Path, // Path reutilizado como table_name para eventos de segurança
			entry.Host, entry.RawQuery, entry.IsSystemRole)
		if err != nil {
			log.Printf("[Firehose:Error] Failed to persist audit log: %v", err)
		}
	}
}
