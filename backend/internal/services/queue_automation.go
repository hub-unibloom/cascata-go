package services

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"time"

	"github.com/redis/go-redis/v9"
	"cascata-backend/internal/types"
)

// ============================================================================
// CASCATA AUTOMATION QUEUE SYSTEM
// Dragonfly Streams + Consumer Groups + DLQ + Backoff Exponencial
// ============================================================================

// Variável global para acesso ao serviço de automação pelos workers
var compiledAutomationSvc *CompiledAutomationService

// SetCompiledAutomationService configura o serviço global para os workers
func SetCompiledAutomationService(svc *CompiledAutomationService) {
	compiledAutomationSvc = svc
}

const (
	// Streams de prioridade para automações
	StreamAutomationsHigh   = "cascata-automations:high"
	StreamAutomationsNormal = "cascata-automations:normal"
	StreamAutomationsLow    = "cascata-automations:low"
	StreamDeadLetter        = "cascata-automations:dlq"
	
	// Consumer group para automações
	AutomationConsumerGroup = "automation-workers"
	
	// Configurações padrão
	DefaultMaxRetries     = 3
	DefaultRetryDelayBase = 1000 * time.Millisecond // 1 segundo base
	MaxRetryDelay         = 30 * time.Second
	
	// Prioridades
	PriorityHigh   = 1
	PriorityNormal = 5
	PriorityLow    = 10
)

// AutomationJob representa um job de automação na fila
type AutomationJob struct {
	ID            string             `json:"id"`
	AutomationID  string             `json:"automation_id"`
	ProjectSlug   string             `json:"project_slug"`
	Nodes         []AutomationNode   `json:"nodes"`
	Payload       interface{}        `json:"payload"`
	Attempt       int                `json:"attempt"`        // Tentativa atual (0-indexed)
	MaxAttempts   int                `json:"max_attempts"`   // Máximo de tentativas (default: 3)
	Priority      int                `json:"priority"`       // 1=high, 5=normal, 10=low
	RetryDelayMs  int                `json:"retry_delay_ms,omitempty"` // Delay base entre retries em ms (padrão: 1000ms)
	ScheduledAt   int64              `json:"scheduled_at"`   // Unix ms - para agendamento futuro
	CreatedAt     int64              `json:"created_at"`
	LastError     string             `json:"last_error,omitempty"`
	Context       *AutomationContext `json:"context,omitempty"`
}

// AutomationQueueStats representa métricas da fila
type AutomationQueueStats struct {
	HighPending   int64 `json:"high_pending"`
	NormalPending int64 `json:"normal_pending"`
	LowPending    int64 `json:"low_pending"`
	DLQCount      int64 `json:"dlq_count"`
	Processing    int   `json:"processing"` // Em processamento (estimado)
}

// AddAutomationJob adiciona um job de automação à fila com routing por prioridade
func AddAutomationJob(ctx context.Context, job *AutomationJob) error {
	if dragonfly == nil {
		return fmt.Errorf("Dragonfly not initialized")
	}
	
	// Validações
	if job.AutomationID == "" {
		return fmt.Errorf("automation_id is required")
	}
	if job.ProjectSlug == "" {
		return fmt.Errorf("project_slug is required")
	}
	
	// Aplicar defaults
	if job.ID == "" {
		job.ID = generateJobID()
	}
	if job.MaxAttempts <= 0 {
		job.MaxAttempts = DefaultMaxRetries
	}
	if job.Priority <= 0 {
		job.Priority = PriorityNormal
	}
	if job.RetryDelayMs <= 0 {
		job.RetryDelayMs = 1000 // Default: 1 segundo
	}
	if job.CreatedAt == 0 {
		job.CreatedAt = time.Now().UnixMilli()
	}
	// Validações de limite (production-grade)
	if job.MaxAttempts > 20 {
		job.MaxAttempts = 20 // Cap máximo: 20 tentativas
		log.Printf("[AutomationQueue] Warning: MaxAttempts capped to 20 for job %s", job.ID)
	}
	if job.RetryDelayMs > 300000 {
		job.RetryDelayMs = 300000 // Cap máximo: 5 minutos
		log.Printf("[AutomationQueue] Warning: RetryDelayMs capped to 300000 for job %s", job.ID)
	}
	if job.Priority < 1 {
		job.Priority = 1 // Mínimo: 1 (alta)
	} else if job.Priority > 10 {
		job.Priority = 10 // Máximo: 10 (baixa)
	}
	
	// Determinar stream baseado na prioridade
	stream := getStreamByPriority(job.Priority)
	
	// Se tem scheduled_at no futuro, usar sorted set para delayed queue
	if job.ScheduledAt > 0 && job.ScheduledAt > time.Now().UnixMilli() {
		return addDelayedJob(ctx, job)
	}
	
	// Serializar job
	payload, err := json.Marshal(job)
	if err != nil {
		return fmt.Errorf("failed to marshal job: %w", err)
	}
	
	// Adicionar à stream
	_, err = dragonfly.XAdd(ctx, &redis.XAddArgs{
		Stream: stream,
		Values: map[string]interface{}{
			"payload": string(payload),
			"ts":      time.Now().UnixMilli(),
			"id":      job.ID,
		},
	}).Result()
	
	if err != nil {
		return fmt.Errorf("failed to add job to stream %s: %w", stream, err)
	}
	
	log.Printf("[AutomationQueue] Job %s added to %s (priority: %d)", job.ID, stream, job.Priority)
	return nil
}

// addDelayedJob adiciona job agendado para execução futura
func addDelayedJob(ctx context.Context, job *AutomationJob) error {
	key := "cascata-automations:delayed"
	
	payload, err := json.Marshal(job)
	if err != nil {
		return err
	}
	
	// ZADD com score = scheduled_at (quanto menor, mais cedo executa)
	_, err = dragonfly.ZAdd(ctx, key, redis.Z{
		Score:  float64(job.ScheduledAt),
		Member: string(payload),
	}).Result()
	
	return err
}

// getStreamByPriority retorna o stream apropriado baseado na prioridade
func getStreamByPriority(priority int) string {
	switch {
	case priority <= 2:
		return StreamAutomationsHigh
	case priority >= 8:
		return StreamAutomationsLow
	default:
		return StreamAutomationsNormal
	}
}

// generateJobID gera um ID único para o job
func generateJobID() string {
	return fmt.Sprintf("auto_%d_%d", time.Now().UnixMilli(), time.Now().Nanosecond())
}

// InitAutomationQueues inicializa os workers de automação
func InitAutomationQueues() {
	if dragonfly == nil {
		log.Println("[AutomationQueue] Dragonfly not initialized, skipping queue startup")
		return
	}
	
	ctx := context.Background()
	
	// Criar consumer groups (idempotent)
	_ = dragonfly.XGroupCreateMkStream(ctx, StreamAutomationsHigh, AutomationConsumerGroup, "0").Err()
	_ = dragonfly.XGroupCreateMkStream(ctx, StreamAutomationsNormal, AutomationConsumerGroup, "0").Err()
	_ = dragonfly.XGroupCreateMkStream(ctx, StreamAutomationsLow, AutomationConsumerGroup, "0").Err()
	_ = dragonfly.XGroupCreateMkStream(ctx, StreamDeadLetter, AutomationConsumerGroup, "0").Err()
	
	// Iniciar workers para cada stream
	// Workers de alta prioridade: 2 consumers
	go runAutomationWorker(StreamAutomationsHigh, AutomationConsumerGroup, "high-worker-1")
	go runAutomationWorker(StreamAutomationsHigh, AutomationConsumerGroup, "high-worker-2")
	
	// Workers de prioridade normal: 3 consumers
	go runAutomationWorker(StreamAutomationsNormal, AutomationConsumerGroup, "normal-worker-1")
	go runAutomationWorker(StreamAutomationsNormal, AutomationConsumerGroup, "normal-worker-2")
	go runAutomationWorker(StreamAutomationsNormal, AutomationConsumerGroup, "normal-worker-3")
	
	// Workers de baixa prioridade: 1 consumer
	go runAutomationWorker(StreamAutomationsLow, AutomationConsumerGroup, "low-worker-1")
	
	// Iniciar processador de jobs atrasados
	go runDelayedJobProcessor()
	
	log.Println("[AutomationQueue] Workers started (2 high + 3 normal + 1 low + delayed processor)")
}

// runAutomationWorker loop principal do worker com retry e DLQ
func runAutomationWorker(stream, group, consumer string) {
	ctx := context.Background()
	
	for {
		// Ler mensagens do stream
		entries, err := dragonfly.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group:    group,
			Consumer: consumer,
			Streams:  []string{stream, ">"},
			Count:    1,
			Block:    5 * time.Second,
		}).Result()
		
		if err != nil {
			if err != redis.Nil {
				log.Printf("[AutomationQueue:%s] Error reading stream: %v", consumer, err)
			}
			time.Sleep(time.Second)
			continue
		}
		
		// Processar mensagens
		for _, e := range entries {
			for _, msg := range e.Messages {
				processJob(ctx, stream, group, msg)
			}
		}
		
		// Processar mensagens pendentes (que falharam anteriormente)
		processPendingMessages(ctx, stream, group, consumer)
	}
}

// processJob processa um job individual
func processJob(ctx context.Context, stream, group string, msg redis.XMessage) {
	// Parse job
	payloadStr, ok := msg.Values["payload"].(string)
	if !ok {
		log.Printf("[AutomationQueue] Invalid message format, ACKing")
		dragonfly.XAck(ctx, stream, group, msg.ID)
		return
	}
	
	var job AutomationJob
	if err := json.Unmarshal([]byte(payloadStr), &job); err != nil {
		log.Printf("[AutomationQueue] Failed to unmarshal job: %v", err)
		dragonfly.XAck(ctx, stream, group, msg.ID)
		return
	}
	
	// Verificar se é para executar agora (scheduled)
	if job.ScheduledAt > 0 && job.ScheduledAt > time.Now().UnixMilli() {
		// Ainda não é hora, não ACK (vai ser reprocessado)
		log.Printf("[AutomationQueue] Job %s scheduled for later, requeueing", job.ID)
		// Re-add to delayed queue
		addDelayedJob(ctx, &job)
		dragonfly.XAck(ctx, stream, group, msg.ID)
		return
	}
	
	log.Printf("[AutomationQueue] Processing job %s (attempt %d/%d)", job.ID, job.Attempt+1, job.MaxAttempts)
	
	// Executar automação
	start := time.Now()
	err := executeAutomationJob(&job)
	elapsed := time.Since(start)
	
	if err == nil {
		// Sucesso! ACK na mensagem
		dragonfly.XAck(ctx, stream, group, msg.ID)
		log.Printf("[AutomationQueue] Job %s completed in %v", job.ID, elapsed)
		return
	}
	
	// Falha - verificar se deve retry
	job.LastError = err.Error()
	job.Attempt++
	
	log.Printf("[AutomationQueue] Job %s failed (attempt %d/%d): %v", job.ID, job.Attempt, job.MaxAttempts, err)
	
	// Validação: se maxAttempts = 0, não fazer retry (falha imediata vai para DLQ)
	if job.MaxAttempts <= 0 {
		log.Printf("[AutomationQueue] Job %s configured with no retries (maxAttempts=0), moving to DLQ", job.ID)
		moveToDLQ(ctx, &job, msg.ID)
		dragonfly.XAck(ctx, stream, group, msg.ID)
		return
	}
	
	// Verificar se atingiu limite de tentativas
	if job.Attempt >= job.MaxAttempts {
		// Max retries atingido - mover para DLQ
		moveToDLQ(ctx, &job, msg.ID)
		dragonfly.XAck(ctx, stream, group, msg.ID)
		log.Printf("[AutomationQueue] Job %s moved to DLQ after %d attempts", job.ID, job.MaxAttempts)
		return
	}
	
	// Retry com backoff exponencial usando delay configurável do job
	delay := calculateBackoff(job.Attempt, job.RetryDelayMs)
	log.Printf("[AutomationQueue] Job %s retry scheduled in %v (delay: %dms)", job.ID, delay, job.RetryDelayMs)
	
	// ACK atual e re-adicionar com delay
	dragonfly.XAck(ctx, stream, group, msg.ID)
	
	// Agendar retry
	job.ScheduledAt = time.Now().Add(delay).UnixMilli()
	if err := addDelayedJob(ctx, &job); err != nil {
		log.Printf("[AutomationQueue] Failed to schedule retry: %v", err)
	}
}

// executeAutomationJob executa a automação propriamente dita
func executeAutomationJob(job *AutomationJob) error {
	log.Printf("[executeAutomationJob] START - JobID=%s, AutomationID=%s, ProjectSlug=%s", 
		job.ID, job.AutomationID, job.ProjectSlug)
	
	// Obter serviço - usa o global ou cria um temporário
	var svc *CompiledAutomationService
	
	// Tentar obter do contexto global se existir
	if compiledAutomationSvc != nil {
		svc = compiledAutomationSvc
	} else {
		// Fallback: criar um serviço temporário
		cryptoSvc := &CryptoService{}
		svc = NewCompiledAutomationService(cryptoSvc)
	}
	
	// Preparar contexto
	automationCtx := job.Context
	if automationCtx == nil {
		automationCtx = &AutomationContext{
			ProjectSlug: job.ProjectSlug,
			Vars:        make(map[string]interface{}),
		}
	}
	
	// CRITICAL: Recuperar ProjectPool do banco (perdido na serialização da fila)
	// O *pgxpool.Pool não pode ser serializado em JSON, então precisamos recuperá-lo
	if automationCtx.ProjectPool == nil {
		log.Printf("[executeAutomationJob] ProjectPool is nil, attempting recovery from database")
		
		// Buscar projeto do banco de dados do sistema
		var project types.Project
		err := SystemPool.QueryRow(context.Background(),
			`SELECT id, slug, db_name, metadata FROM system.projects WHERE slug = $1`,
			job.ProjectSlug).Scan(&project.ID, &project.Slug, &project.DbName, &project.Metadata)
		
		if err != nil {
			log.Printf("[executeAutomationJob] FAILED to recover project from DB: %v", err)
			return fmt.Errorf("failed to recover project pool: %w", err)
		}
		
		// Obter pool usando o serviço de projetos
		pool, err := GetProjectPool(&project, "live")
		if err != nil {
			log.Printf("[executeAutomationJob] FAILED to get project pool: %v", err)
			return fmt.Errorf("failed to get project pool: %w", err)
		}
		
		automationCtx.ProjectPool = pool
		log.Printf("[executeAutomationJob] ProjectPool RECOVERED successfully for project %s", job.ProjectSlug)
	} else {
		log.Printf("[executeAutomationJob] ProjectPool already present (unexpected in async job)")
	}
	
	// Executar síncrono (já estamos em worker async)
	start := time.Now()
	result, err := svc.ExecuteSync(context.Background(), job.AutomationID, job.ProjectSlug, job.Nodes, job.Payload, automationCtx)
	elapsed := time.Since(start)
	
	if err != nil {
		log.Printf("[executeAutomationJob] FAILED after %v: %v", elapsed, err)
		return err
	}
	
	log.Printf("[executeAutomationJob] SUCCESS - JobID=%s completed in %v, result type=%T", 
		job.ID, elapsed, result)
	return nil
}

// calculateBackoff calcula delay exponencial com jitter baseado no delay configurável do job
func calculateBackoff(attempt int, retryDelayMs int) time.Duration {
	// Usar delay configurável ou default
	baseDelay := time.Duration(retryDelayMs) * time.Millisecond
	if baseDelay <= 0 {
		baseDelay = DefaultRetryDelayBase
	}
	
	// Exponencial: 2^attempt * base
	// attempt 1: 2x, attempt 2: 4x, attempt 3: 8x
	delay := time.Duration(math.Pow(2, float64(attempt))) * baseDelay
	
	// Cap no máximo
	if delay > MaxRetryDelay {
		delay = MaxRetryDelay
	}
	
	// Adicionar jitter (±25%) para evitar thundering herd
	jitter := time.Duration(float64(delay) * 0.25 * (float64(time.Now().UnixNano()%100) / 100.0))
	delay = delay + jitter - (delay / 4)
	
	return delay
}

// moveToDLQ move um job para a fila de dead letter
func moveToDLQ(ctx context.Context, job *AutomationJob, originalID string) {
	payload, _ := json.Marshal(job)
	
	dragonfly.XAdd(ctx, &redis.XAddArgs{
		Stream: StreamDeadLetter,
		Values: map[string]interface{}{
			"payload":       string(payload),
			"ts":            time.Now().UnixMilli(),
			"original_id":   job.ID,
			"final_error":   job.LastError,
			"total_attempts": job.Attempt,
		},
	}).Result()
	
	// Também logar no banco para análise
	go func() {
		_, _ = SystemPool.Exec(context.Background(),
			`INSERT INTO system.automation_runs
				(automation_id, project_slug, status, execution_time_ms, trigger_payload, error_message)
			 VALUES ($1, $2, $3, $4, $5, $6)`,
			job.AutomationID, job.ProjectSlug, "failed_queue", 0, "{}",
			fmt.Sprintf("DLQ after %d attempts: %s", job.Attempt, job.LastError))
	}()
}

// runDelayedJobProcessor processa jobs agendados
func runDelayedJobProcessor() {
	ctx := context.Background()
	key := "cascata-automations:delayed"
	
	for {
		now := time.Now().UnixMilli()
		
		// Buscar jobs que estão prontos para executar (score <= now)
		jobs, err := dragonfly.ZRangeByScoreWithScores(ctx, key, &redis.ZRangeBy{
			Min:   "0",
			Max:   fmt.Sprintf("%d", now),
			Count: 10,
		}).Result()
		
		if err != nil {
			time.Sleep(5 * time.Second)
			continue
		}
		
		for _, z := range jobs {
			var job AutomationJob
			if err := json.Unmarshal([]byte(z.Member.(string)), &job); err != nil {
				continue
			}
			
			// Remover do sorted set
			dragonfly.ZRem(ctx, key, z.Member)
			
			// Re-adicionar à fila normal
			job.ScheduledAt = 0 // Reset scheduled time
			if err := AddAutomationJob(ctx, &job); err != nil {
				log.Printf("[AutomationQueue:Delayed] Failed to requeue job %s: %v", job.ID, err)
			}
		}
		
		time.Sleep(time.Second)
	}
}

// processPendingMessages processa mensagens pendentes (não ACKed)
func processPendingMessages(ctx context.Context, stream, group, consumer string) {
	// Obter mensagens pendentes
	pending, err := dragonfly.XPendingExt(ctx, &redis.XPendingExtArgs{
		Stream: stream,
		Group:  group,
		Start:  "-",
		End:    "+",
		Count:  10,
		Consumer: consumer,
	}).Result()
	
	if err != nil || len(pending) == 0 {
		return
	}
	
	for _, p := range pending {
		// Claim a mensagem (se estiver travada por mais de 30s)
		if p.Idle > 30*time.Second {
			claimed, err := dragonfly.XClaim(ctx, &redis.XClaimArgs{
				Stream:   stream,
				Group:    group,
				Consumer: consumer,
				MinIdle:  30 * time.Second,
				Messages: []string{p.ID},
			}).Result()
			
			if err != nil {
				continue
			}
			
			for _, msg := range claimed {
				processJob(ctx, stream, group, msg)
			}
		}
	}
}

// GetAutomationQueueStats retorna estatísticas da fila
func GetAutomationQueueStats(ctx context.Context) (*AutomationQueueStats, error) {
	if dragonfly == nil {
		return nil, fmt.Errorf("Dragonfly not initialized")
	}
	
	stats := &AutomationQueueStats{}
	
	// XLEN para cada stream
	high, _ := dragonfly.XLen(ctx, StreamAutomationsHigh).Result()
	normal, _ := dragonfly.XLen(ctx, StreamAutomationsNormal).Result()
	low, _ := dragonfly.XLen(ctx, StreamAutomationsLow).Result()
	dlq, _ := dragonfly.XLen(ctx, StreamDeadLetter).Result()
	
	stats.HighPending = high
	stats.NormalPending = normal
	stats.LowPending = low
	stats.DLQCount = dlq
	
	// Estimar processamento via XPENDING
	pendingHigh, _ := dragonfly.XPending(ctx, StreamAutomationsHigh, AutomationConsumerGroup).Result()
	pendingNormal, _ := dragonfly.XPending(ctx, StreamAutomationsNormal, AutomationConsumerGroup).Result()
	pendingLow, _ := dragonfly.XPending(ctx, StreamAutomationsLow, AutomationConsumerGroup).Result()
	
	// XPending retorna *redis.XPending que tem campo Count
	if pendingHigh != nil {
		stats.Processing += int(pendingHigh.Count)
	}
	if pendingNormal != nil {
		stats.Processing += int(pendingNormal.Count)
	}
	if pendingLow != nil {
		stats.Processing += int(pendingLow.Count)
	}
	
	return stats, nil
}

// RequeueDLQJob reprocessa um job da DLQ
func RequeueDLQJob(ctx context.Context, jobID string) error {
	// Buscar na DLQ
	messages, err := dragonfly.XRange(ctx, StreamDeadLetter, "-", "+").Result()
	if err != nil {
		return err
	}
	
	for _, msg := range messages {
		payloadStr, ok := msg.Values["payload"].(string)
		if !ok {
			continue
		}
		
		var job AutomationJob
		if err := json.Unmarshal([]byte(payloadStr), &job); err != nil {
			continue
		}
		
		if job.ID == jobID {
			// Reset attempt e requeue
			job.Attempt = 0
			job.LastError = ""
			job.ScheduledAt = 0
			
			// Remover da DLQ
			dragonfly.XDel(ctx, StreamDeadLetter, msg.ID)
			
			// Re-adicionar à fila
			return AddAutomationJob(ctx, &job)
		}
	}
	
	return fmt.Errorf("job %s not found in DLQ", jobID)
}
