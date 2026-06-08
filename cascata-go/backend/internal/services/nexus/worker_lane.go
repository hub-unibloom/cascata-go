package nexus

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// ============================================================================
// WORKER LANE — Sistema de Filas Assíncrono via Dragonfly
// ============================================================================
// Implementa a Worker Lane para execução assíncrona de grafos POST_PERSIST.
// Usa Dragonfly (Redis-compatible) como broker de mensagens com suporte
// a retry, backoff exponencial e Dead Letter Queue.
// ============================================================================

// Constantes de configuração da Worker Lane.
const (
	DefaultWorkerCount      = 10
	DefaultMaxConcurrency   = 5
	DefaultQueueName        = "nexus:worker:tasks"
	DefaultDLQName          = "nexus:worker:dlq"
	DefaultProcessingSet    = "nexus:worker:processing"
	DefaultHealthCheckInterval = 10 * time.Second
	DefaultTaskTimeout      = 300 * time.Second // 5 minutos
	DefaultMaxRetries       = 3
	DefaultRetryDelay       = 1 * time.Second
	DefaultDLQTTL           = 24 * time.Hour
	DLQAlertThreshold       = 100
)

// WorkerTask representa uma tarefa na fila.
type WorkerTask struct {
	ID            string          `json:"id"`
	AutomationID  string          `json:"automation_id"`
	TenantID      string          `json:"tenant_id"`
	UserUUID      string          `json:"user_uuid"`
	UserRole      string          `json:"user_role"`
	AuthSource    string          `json:"auth_source"`
	TraceID       string          `json:"trace_id"`
	GraphJSON     json.RawMessage `json:"graph_json"`
	TriggerData   map[string]interface{} `json:"trigger_data"`
	Headers       map[string]string      `json:"headers"`
	TriggerType   string          `json:"trigger_type"`
	Route         string          `json:"route"`
	Method        string          `json:"method"`
	Priority      int             `json:"priority"`
	Retries       int             `json:"retries"`
	MaxRetries    int             `json:"max_retries"`
	RetryDelay    time.Duration   `json:"retry_delay_ms"`
	CreatedAt     time.Time       `json:"created_at"`
	ScheduledAt   time.Time       `json:"scheduled_at"`
	LastError     string          `json:"last_error,omitempty"`
	ExecutionMode ExecutionMode   `json:"execution_mode"`
	BranchName    string          `json:"branch_name,omitempty"`
}

type WorkerLane struct {
	rdb            *redis.Client
	engine         *NexusEngine
	systemPool     *pgxpool.Pool
	VaultSvc       SecretResolver
	EnumSvc        EnumResolver
	UserSvc        UserResolver
	queueName      string
	dlqName        string
	processingSet  string
	maxWorkers     int
	maxConcurrency int
	retryPolicy    RetryPolicy
	healthInterval time.Duration

	// Controle de lifecycle
	ctx        context.Context
	cancel     context.CancelFunc
	wg         sync.WaitGroup
	running    int32
	logger     *StructuredLogger
}

// WorkerLaneConfig contém a configuração do Worker Lane.
type WorkerLaneConfig struct {
	RedisClient    *redis.Client
	Engine         *NexusEngine
	SystemPool     *pgxpool.Pool
	MaxWorkers     int
	MaxConcurrency int
	QueueName      string
	DLQName        string
	HealthInterval time.Duration
	RetryPolicy    RetryPolicy
}

// NewWorkerLane cria uma nova instância do Worker Lane.
func NewWorkerLane(cfg WorkerLaneConfig, vaultSvc SecretResolver, enumSvc EnumResolver, userSvc UserResolver) *WorkerLane {
	if cfg.MaxWorkers <= 0 {
		cfg.MaxWorkers = DefaultWorkerCount
	}
	if cfg.MaxConcurrency <= 0 {
		cfg.MaxConcurrency = DefaultMaxConcurrency
	}
	if cfg.QueueName == "" {
		cfg.QueueName = DefaultQueueName
	}
	if cfg.DLQName == "" {
		cfg.DLQName = DefaultDLQName
	}
	if cfg.HealthInterval <= 0 {
		cfg.HealthInterval = DefaultHealthCheckInterval
	}
	if cfg.RetryPolicy.MaxRetries <= 0 {
		cfg.RetryPolicy.MaxRetries = DefaultMaxRetries
	}
	if cfg.RetryPolicy.BackoffBase <= 0 {
		cfg.RetryPolicy.BackoffBase = DefaultRetryDelay
	}
	if cfg.RetryPolicy.BackoffMax <= 0 {
		cfg.RetryPolicy.BackoffMax = 30 * time.Second
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &WorkerLane{
		rdb:            cfg.RedisClient,
		engine:         cfg.Engine,
		systemPool:     cfg.SystemPool,
		VaultSvc:       vaultSvc,
		EnumSvc:        enumSvc,
		UserSvc:        userSvc,
		queueName:      cfg.QueueName,
		dlqName:        cfg.DLQName,
		processingSet:  DefaultProcessingSet,
		maxWorkers:     cfg.MaxWorkers,
		maxConcurrency: cfg.MaxConcurrency,
		retryPolicy:    cfg.RetryPolicy,
		healthInterval: cfg.HealthInterval,
		ctx:            ctx,
		cancel:         cancel,
		logger:         NewStructuredLogger("WorkerLane"),
	}
}

// Start inicia os workers do Worker Lane.
func (w *WorkerLane) Start() error {
	if !atomic.CompareAndSwapInt32(&w.running, 0, 1) {
		return fmt.Errorf("worker lane already running")
	}

	w.logger.Info("worker_lane.starting", map[string]interface{}{
		"workers":     w.maxWorkers,
		"concurrency": w.maxConcurrency,
		"queue":       w.queueName,
		"dlq":         w.dlqName,
	})

	// Inicia workers
	for i := 0; i < w.maxWorkers; i++ {
		w.wg.Add(1)
		go w.workerLoop(i)
	}

	// Inicia health check
	w.wg.Add(1)
	go w.healthCheckLoop()

	return nil
}

// Stop para os workers gracefully.
func (w *WorkerLane) Stop() {
	if !atomic.CompareAndSwapInt32(&w.running, 1, 0) {
		return
	}

	w.logger.Info("worker_lane.stopping", nil)
	w.cancel()
	w.wg.Wait()
	w.logger.Info("worker_lane.stopped", nil)
}

// Enqueue adiciona uma tarefa à fila.
func (w *WorkerLane) Enqueue(ctx context.Context, task *WorkerTask) error {
	if task.ID == "" {
		task.ID = uuid.New().String()
	}
	if task.CreatedAt.IsZero() {
		task.CreatedAt = time.Now().UTC()
	}
	if task.ScheduledAt.IsZero() {
		task.ScheduledAt = task.CreatedAt
	}
	if task.MaxRetries <= 0 {
		task.MaxRetries = w.retryPolicy.MaxRetries
	}
	if task.Priority <= 0 {
		task.Priority = 5
	}

	taskJSON, err := json.Marshal(task)
	if err != nil {
		return fmt.Errorf("worker_lane: failed to marshal task: %w", err)
	}

	// Usa LPUSH para FIFO (BRPOP consome do lado direito)
	if err := w.rdb.LPush(ctx, w.queueName, string(taskJSON)).Err(); err != nil {
		return fmt.Errorf("worker_lane: failed to enqueue task: %w", err)
	}

	w.logger.Info("task.enqueued", map[string]interface{}{
		"task_id":       task.ID,
		"automation_id": task.AutomationID,
		"tenant_id":     task.TenantID,
		"priority":      task.Priority,
		"trace_id":      task.TraceID,
	})

	return nil
}

// workerLoop é o loop principal de cada worker.
func (w *WorkerLane) workerLoop(workerID int) {
	defer w.wg.Done()

	w.logger.Info("worker.started", map[string]interface{}{"worker_id": workerID})

	for {
		select {
		case <-w.ctx.Done():
			w.logger.Info("worker.stopped", map[string]interface{}{"worker_id": workerID})
			return
		default:
		}

		// BRPOP com timeout de 1s (para checar cancelamento periodicamente)
		result, err := w.rdb.BRPop(w.ctx, 1*time.Second, w.queueName).Result()
		if err != nil {
			if err == redis.Nil || w.ctx.Err() != nil {
				continue
			}
			w.logger.Error("worker.pop_error", map[string]interface{}{
				"worker_id": workerID,
				"error":     err.Error(),
			})
			time.Sleep(500 * time.Millisecond)
			continue
		}

		if len(result) < 2 {
			continue
		}

		taskJSON := result[1]

		var task WorkerTask
		if err := json.Unmarshal([]byte(taskJSON), &task); err != nil {
			w.logger.Error("worker.unmarshal_error", map[string]interface{}{
				"worker_id": workerID,
				"error":     err.Error(),
			})
			continue
		}

		// Marca como em processamento
		w.rdb.SAdd(w.ctx, w.processingSet, task.ID)

		// Processa a tarefa
		w.processTask(workerID, &task)

		// Remove do set de processamento
		w.rdb.SRem(w.ctx, w.processingSet, task.ID)
	}
}

// processTask processa uma tarefa individual.
func (w *WorkerLane) processTask(workerID int, task *WorkerTask) {
	startedAt := time.Now()

	w.logger.Info("task.processing", map[string]interface{}{
		"worker_id":     workerID,
		"task_id":       task.ID,
		"automation_id": task.AutomationID,
		"tenant_id":     task.TenantID,
		"retry":         task.Retries,
		"trace_id":      task.TraceID,
	})

	// Hot-swap de fila: se houver uma nova versão ativa da automação no banco de dados para este branch/main,
	// nós utilizamos a nova versão em vez do GraphJSON enfileirado originalmente (garantindo hot-swap graceful).
	graphJSON := task.GraphJSON
	branchName := task.BranchName
	if branchName == "" {
		branchName = "main"
	}

	var dbGraphJSON []byte
	var isDbActive bool
	err := w.systemPool.QueryRow(w.ctx, `
		SELECT graph_json, is_active FROM system.nexus_automations 
		WHERE id = $1 AND tenant_id = $2 AND branch_name = $3
	`, task.AutomationID, task.TenantID, branchName).Scan(&dbGraphJSON, &isDbActive)

	if err == nil && isDbActive && len(dbGraphJSON) > 0 {
		graphJSON = dbGraphJSON
		w.logger.Info("task.hot_swapped", map[string]interface{}{
			"task_id":       task.ID,
			"automation_id": task.AutomationID,
			"tenant_id":     task.TenantID,
			"branch":        branchName,
		})
	}

	// Compila o grafo
	plan, err := w.engine.Compile(graphJSON)
	if err != nil {
		w.handleTaskFailure(task, fmt.Errorf("compilation failed: %w", err))
		return
	}

	// Monta o NexusState
	state := NewNexusState(
		&TriggerContext{
			Type:    task.TriggerType,
			Payload: task.TriggerData,
			Headers: task.Headers,
			Method:  task.Method,
			Route:   task.Route,
		},
		&SecurityContext{
			TenantID:   task.TenantID,
			UserUUID:   task.UserUUID,
			UserRole:   task.UserRole,
			AuthSource: task.AuthSource,
			TraceID:    task.TraceID,
			Timestamp:  task.CreatedAt.Format(time.RFC3339),
		},
		&SystemContext{
			AutomationID:  task.AutomationID,
			ExecutionMode: string(ModeWorkerLane),
		},
	)
	state.SetSecretResolver(w.VaultSvc)
	state.SetEnumResolver(w.EnumSvc)
	state.SetUserResolver(w.UserSvc)

	// Cria contexto com timeout
	taskTimeout := DefaultTaskTimeout
	if plan.Timeout > 0 {
		taskTimeout = plan.Timeout
	}

	taskCtx, cancel := context.WithTimeout(w.ctx, taskTimeout)
	defer cancel()

	// Executa o grafo
	result, err := w.engine.ExecGraph(taskCtx, plan, state)
	duration := time.Since(startedAt).Milliseconds()

	if err != nil {
		w.logger.Error("task.failed", map[string]interface{}{
			"worker_id":     workerID,
			"task_id":       task.ID,
			"automation_id": task.AutomationID,
			"error":         err.Error(),
			"duration_ms":   duration,
		})
		w.handleTaskFailure(task, err)
		return
	}

	w.logger.Info("task.completed", map[string]interface{}{
		"worker_id":      workerID,
		"task_id":        task.ID,
		"automation_id":  task.AutomationID,
		"status":         result.Status,
		"duration_ms":    duration,
		"nodes_executed": result.NodesExecuted,
	})

	// 5. Registra execução no Log Central (Sinergia de Telemetria)
	RecordExecution(w.ctx, w.systemPool, w.logger, result, task.AutomationID, task.TenantID, task.TriggerData)
}

// handleTaskFailure trata falhas com retry ou DLQ.
func (w *WorkerLane) handleTaskFailure(task *WorkerTask, err error) {
	task.Retries++
	task.LastError = err.Error()

	if task.Retries > task.MaxRetries {
		// Move para DLQ
		w.moveToDLQ(task)
		return
	}

	// Calcula backoff exponencial
	backoff := w.retryPolicy.BackoffBase * time.Duration(1<<uint(task.Retries-1))
	if backoff > w.retryPolicy.BackoffMax {
		backoff = w.retryPolicy.BackoffMax
	}

	task.ScheduledAt = time.Now().Add(backoff)

	w.logger.Warn("task.retry_scheduled", map[string]interface{}{
		"task_id":  task.ID,
		"retry":    task.Retries,
		"max":      task.MaxRetries,
		"backoff":  backoff.String(),
		"next_at":  task.ScheduledAt.Format(time.RFC3339),
	})

	// Re-enfileira com delay
	go func() {
		time.Sleep(backoff)
		if err := w.Enqueue(context.Background(), task); err != nil {
			w.logger.Error("task.retry_failed", map[string]interface{}{
				"task_id": task.ID,
				"error":   err.Error(),
			})
			w.moveToDLQ(task)
		}
	}()
}

// moveToDLQ move uma tarefa para a Dead Letter Queue.
func (w *WorkerLane) moveToDLQ(task *WorkerTask) {
	taskJSON, err := json.Marshal(task)
	if err != nil {
		w.logger.Error("dlq.marshal_error", map[string]interface{}{
			"task_id": task.ID,
			"error":   err.Error(),
		})
		return
	}

	// Adiciona à DLQ com TTL
	pipe := w.rdb.Pipeline()
	pipe.LPush(w.ctx, w.dlqName, string(taskJSON))
	// Garante que a DLQ tem TTL
	pipe.Expire(w.ctx, w.dlqName, DefaultDLQTTL)

	if _, err := pipe.Exec(w.ctx); err != nil {
		w.logger.Error("dlq.push_error", map[string]interface{}{
			"task_id": task.ID,
			"error":   err.Error(),
		})
		return
	}

	w.logger.Warn("task.moved_to_dlq", map[string]interface{}{
		"task_id":       task.ID,
		"automation_id": task.AutomationID,
		"tenant_id":     task.TenantID,
		"retries":       task.Retries,
		"last_error":    task.LastError,
	})

	// Verifica alerta de DLQ
	w.checkDLQAlert()
}

// healthCheckLoop verifica periodicamente a saúde do Worker Lane.
func (w *WorkerLane) healthCheckLoop() {
	defer w.wg.Done()

	ticker := time.NewTicker(w.healthInterval)
	defer ticker.Stop()

	for {
		select {
		case <-w.ctx.Done():
			return
		case <-ticker.C:
			w.performHealthCheck()
		}
	}
}

// performHealthCheck executa verificação de saúde.
func (w *WorkerLane) performHealthCheck() {
	queueLen, err := w.rdb.LLen(w.ctx, w.queueName).Result()
	if err != nil {
		w.logger.Error("health.queue_check_error", map[string]interface{}{
			"error": err.Error(),
		})
		return
	}

	processingCount, _ := w.rdb.SCard(w.ctx, w.processingSet).Result()
	dlqLen, _ := w.rdb.LLen(w.ctx, w.dlqName).Result()

	w.logger.Info("health.check", map[string]interface{}{
		"queue_length":    queueLen,
		"processing":      processingCount,
		"dlq_length":      dlqLen,
		"workers_running": w.maxWorkers,
	})

	if dlqLen >= DLQAlertThreshold {
		w.logger.Error("health.dlq_alert", map[string]interface{}{
			"dlq_length": dlqLen,
			"threshold":  DLQAlertThreshold,
			"message":    "DLQ has accumulated too many failed tasks",
		})
	}
}

// checkDLQAlert verifica se o DLQ excedeu o limiar de alerta.
func (w *WorkerLane) checkDLQAlert() {
	dlqLen, err := w.rdb.LLen(w.ctx, w.dlqName).Result()
	if err != nil {
		return
	}

	if dlqLen >= DLQAlertThreshold {
		w.logger.Error("dlq.threshold_exceeded", map[string]interface{}{
			"dlq_length": dlqLen,
			"threshold":  DLQAlertThreshold,
		})
	}
}

// ============================================================================
// DLQ MANAGER — Gerenciamento de Dead Letter Queues
// ============================================================================

// DLQManager gerencia operações administrativas sobre a DLQ.
type DLQManager struct {
	rdb    *redis.Client
	dlqName string
	logger *StructuredLogger
}

// NewDLQManager cria um novo gerenciador de DLQ.
func NewDLQManager(rdb *redis.Client, dlqName string) *DLQManager {
	if dlqName == "" {
		dlqName = DefaultDLQName
	}
	return &DLQManager{
		rdb:     rdb,
		dlqName: dlqName,
		logger:  NewStructuredLogger("DLQManager"),
	}
}

// ListTasks lista todas as tarefas na DLQ.
func (d *DLQManager) ListTasks(ctx context.Context, offset, limit int64) ([]WorkerTask, int64, error) {
	total, err := d.rdb.LLen(ctx, d.dlqName).Result()
	if err != nil {
		return nil, 0, fmt.Errorf("dlq: failed to get length: %w", err)
	}

	if total == 0 {
		return []WorkerTask{}, 0, nil
	}

	rawTasks, err := d.rdb.LRange(ctx, d.dlqName, offset, offset+limit-1).Result()
	if err != nil {
		return nil, 0, fmt.Errorf("dlq: failed to list tasks: %w", err)
	}

	tasks := make([]WorkerTask, 0, len(rawTasks))
	for _, raw := range rawTasks {
		var task WorkerTask
		if err := json.Unmarshal([]byte(raw), &task); err != nil {
			continue
		}
		tasks = append(tasks, task)
	}

	return tasks, total, nil
}

// RetryTask move uma tarefa da DLQ de volta para a fila principal.
func (d *DLQManager) RetryTask(ctx context.Context, taskID string, queueName string) error {
	// Busca a tarefa na DLQ
	rawTasks, err := d.rdb.LRange(ctx, d.dlqName, 0, -1).Result()
	if err != nil {
		return fmt.Errorf("dlq: failed to list tasks: %w", err)
	}

	for i, raw := range rawTasks {
		var task WorkerTask
		if err := json.Unmarshal([]byte(raw), &task); err != nil {
			continue
		}

		if task.ID == taskID {
			// Remove da DLQ
			d.rdb.LRem(ctx, d.dlqName, 1, raw)

			// Reseta retries e re-enfileira
			task.Retries = 0
			task.LastError = ""
			task.ScheduledAt = time.Now().UTC()

			taskJSON, _ := json.Marshal(task)
			d.rdb.LPush(ctx, queueName, string(taskJSON))

			d.logger.Info("dlq.task_retried", map[string]interface{}{
				"task_id": taskID,
				"index":   i,
			})

			return nil
		}
	}

	return fmt.Errorf("dlq: task %s not found", taskID)
}

// PurgeTasks remove todas as tarefas da DLQ.
func (d *DLQManager) PurgeTasks(ctx context.Context) (int64, error) {
	count, err := d.rdb.LLen(ctx, d.dlqName).Result()
	if err != nil {
		return 0, fmt.Errorf("dlq: failed to get length: %w", err)
	}

	if err := d.rdb.Del(ctx, d.dlqName).Err(); err != nil {
		return 0, fmt.Errorf("dlq: failed to purge: %w", err)
	}

	d.logger.Info("dlq.purged", map[string]interface{}{
		"tasks_removed": count,
	})

	return count, nil
}

// PurgeExpired remove tarefas mais antigas que o TTL da DLQ.
func (d *DLQManager) PurgeExpired(ctx context.Context, maxAge time.Duration) (int64, error) {
	rawTasks, err := d.rdb.LRange(ctx, d.dlqName, 0, -1).Result()
	if err != nil {
		return 0, fmt.Errorf("dlq: failed to list tasks: %w", err)
	}

	cutoff := time.Now().Add(-maxAge)
	var removed int64

	for _, raw := range rawTasks {
		var task WorkerTask
		if err := json.Unmarshal([]byte(raw), &task); err != nil {
			continue
		}

		if task.CreatedAt.Before(cutoff) {
			d.rdb.LRem(ctx, d.dlqName, 1, raw)
			removed++

			d.logger.Info("dlq.task_expired", map[string]interface{}{
				"task_id":    task.ID,
				"created_at": task.CreatedAt.Format(time.RFC3339),
				"age":        time.Since(task.CreatedAt).String(),
			})
		}
	}

	return removed, nil
}

// Stats retorna estatísticas da DLQ.
func (d *DLQManager) Stats(ctx context.Context) (map[string]interface{}, error) {
	total, err := d.rdb.LLen(ctx, d.dlqName).Result()
	if err != nil {
		return nil, fmt.Errorf("dlq: failed to get stats: %w", err)
	}

	stats := map[string]interface{}{
		"total_tasks":      total,
		"alert_threshold":  DLQAlertThreshold,
		"alert_active":     total >= DLQAlertThreshold,
		"ttl":              DefaultDLQTTL.String(),
	}

	// Agrupa por tenant
	if total > 0 && total <= 1000 {
		rawTasks, _ := d.rdb.LRange(ctx, d.dlqName, 0, -1).Result()
		tenantCounts := make(map[string]int)
		for _, raw := range rawTasks {
			var task WorkerTask
			if err := json.Unmarshal([]byte(raw), &task); err != nil {
				continue
			}
			tenantCounts[task.TenantID]++
		}
		stats["by_tenant"] = tenantCounts
	}

	return stats, nil
}

// init function to suppress unused import warning
func init() {
	_ = log.Println
}
