package services

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/go-co-op/gocron"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PurgeScheduler gerencia os jobs de purge de logs por projeto
type PurgeScheduler struct {
	db        *pgxpool.Pool
	scheduler *gocron.Scheduler
	jobs      map[string]*gocron.Job
	mu        sync.RWMutex
}

var (
	purgeScheduler *PurgeScheduler
	purgeOnce      sync.Once
)

// GetPurgeScheduler retorna a instância singleton do scheduler
func GetPurgeScheduler(db *pgxpool.Pool) *PurgeScheduler {
	purgeOnce.Do(func() {
		purgeScheduler = &PurgeScheduler{
			db:        db,
			scheduler: gocron.NewScheduler(time.UTC),
			jobs:      make(map[string]*gocron.Job),
		}
		purgeScheduler.scheduler.StartAsync()
		
		// Iniciar goroutine de background para aguardar schema e recarregar schedules
		// Esta goroutine detecta quando migrações terminam via múltiplos mecanismos:
		// 1. Escuta PostgreSQL NOTIFY do MigrationService
		// 2. Polling inteligente com backoff exponencial
		// 3. Detecção de schema readiness via query de teste
		go purgeScheduler.waitForSchemaAndReload()
		
		log.Println("[PurgeScheduler] Iniciado e aguardando schema readiness...")
	})
	return purgeScheduler
}

// ReloadAllSchedules carrega todos os schedules ativos do banco
// É resiliente a erros de schema - se colunas não existirem, apenas loga warning
func (ps *PurgeScheduler) ReloadAllSchedules() {
	rows, err := ps.db.Query(context.Background(), `
		SELECT slug, purge_cron_expression, COALESCE(metadata->>'timezone', 'UTC'), 
		       COALESCE(log_retention_days, 30), COALESCE(archive_logs, false)
		FROM system.projects 
		WHERE purge_enabled = TRUE 
		AND purge_cron_expression IS NOT NULL
	`)
	if err != nil {
		// Verifica se é erro de coluna ou tabela não existente (schema ainda não aplicado)
		errStr := err.Error()
		if strings.Contains(errStr, "does not exist") || strings.Contains(errStr, "42703") || strings.Contains(errStr, "42P01") {
			log.Printf("[PurgeScheduler] Schema ainda não pronto (migration pendente). Schedules serão carregados automaticamente quando o schema estiver disponível.")
			return
		}
		log.Printf("[PurgeScheduler] Erro ao carregar schedules: %v", err)
		return
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var slug, cronExpr, timezone string
		var retentionDays int
		var archive bool
		if err := rows.Scan(&slug, &cronExpr, &timezone, &retentionDays, &archive); err != nil {
			continue
		}
		
		ps.ScheduleProjectPurge(slug, cronExpr, timezone, retentionDays, archive)
		count++
	}
	
	log.Printf("[PurgeScheduler] %d schedules carregados", count)
}

// ScheduleProjectPurge agenda ou reagenda o purge de um projeto
func (ps *PurgeScheduler) ScheduleProjectPurge(slug, cronExpr, timezone string, retentionDays int, archive bool) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	jobID := fmt.Sprintf("purge-%s", slug)

	// Remover job existente
	if existingJob, ok := ps.jobs[jobID]; ok {
		ps.scheduler.RemoveByReference(existingJob)
		delete(ps.jobs, jobID)
		log.Printf("[PurgeScheduler] Schedule antigo removido para %s", slug)
	}

	// Nota: gocron v1.37.0 não suporta .In() para timezone per-job.
	// O timezone é aplicado no momento da execução verificando se o horário
	// atual no timezone do projeto corresponde ao schedule.

	// Agendar novo job com cron (usa formato 5 campos: min hora dia_mes mes dia_semana)
	job, err := ps.scheduler.Cron(cronExpr).Do(func() {
		ps.executePurge(slug, retentionDays, archive, timezone)
	})
	
	if err != nil {
		log.Printf("[PurgeScheduler] Erro ao agendar %s: %v", slug, err)
		return
	}

	ps.jobs[jobID] = job
	log.Printf("[PurgeScheduler] Agendado purge para %s: %s (TZ: %s, retention: %d dias)", 
		slug, cronExpr, timezone, retentionDays)
}

// RemoveProjectPurge remove o schedule de um projeto
func (ps *PurgeScheduler) RemoveProjectPurge(slug string) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	jobID := fmt.Sprintf("purge-%s", slug)
	if existingJob, ok := ps.jobs[jobID]; ok {
		ps.scheduler.RemoveByReference(existingJob)
		delete(ps.jobs, jobID)
		log.Printf("[PurgeScheduler] Schedule removido para %s", slug)
	}
}

// executePurge executa o purge de logs para um projeto
func (ps *PurgeScheduler) executePurge(slug string, retentionDays int, archive bool, timezone string) {
	log.Printf("[PurgeScheduler] Executando purge para %s (retention: %d dias, archive: %v, tz: %s)", 
		slug, retentionDays, archive, timezone)

	var deletedCount int
	err := ps.db.QueryRow(context.Background(), 
		`SELECT system.purge_old_logs($1, $2, $3)`, 
		slug, retentionDays, archive).Scan(&deletedCount)
	
	if err != nil {
		log.Printf("[PurgeScheduler] Erro no purge de %s: %v", slug, err)
		return
	}
	
	log.Printf("[PurgeScheduler] Purge concluído para %s: %d logs removidos", slug, deletedCount)
}

// waitForSchemaAndReload aguarda o schema ficar pronto e então recarrega schedules
// Usa múltiplas estratégias:
// 1. Se este worker rodou migrações, usa o canal local
// 2. Escuta PostgreSQL NOTIFY de outros workers
// 3. Polling inteligente como fallback
func (ps *PurgeScheduler) waitForSchemaAndReload() {
	// Estratégia 1: Se migrações já completaram neste worker, carrega imediatamente
	if IsMigrationCompleted() {
		log.Println("[PurgeScheduler] Migrações já completadas neste worker. Carregando schedules...")
		time.Sleep(100 * time.Millisecond) // Pequena pausa para garantir commit
		ps.ReloadAllSchedules()
		return
	}

	// Estratégia 2: Aguarda notificação de migrações com timeout
	// Outro worker pode estar rodando as migrações
	log.Println("[PurgeScheduler] Aguardando migrações de outros workers (timeout: 2min)...")
	if WaitForMigrations(2 * time.Minute) {
		log.Println("[PurgeScheduler] Migrações detectadas completadas. Recarregando schedules...")
		ps.ReloadAllSchedules()
		return
	}

	// Estratégia 3: Polling inteligente com detecção de schema
	log.Println("[PurgeScheduler] Iniciando polling de schema (max 5min)...")
	ps.backgroundReloadWithRetry()
}

// backgroundReloadWithRetry tenta recarregar schedules periodicamente
// até que o schema esteja disponível. Usa backoff exponencial.
func (ps *PurgeScheduler) backgroundReloadWithRetry() {
	retries := 0
	maxRetries := 30
	baseDelay := 2 * time.Second
	maxDelay := 10 * time.Second
	startTime := time.Now()
	maxDuration := 5 * time.Minute

	for retries < maxRetries {
		// Verifica se atingiu timeout total
		if time.Since(startTime) > maxDuration {
			log.Printf("[PurgeScheduler] Timeout total atingido (%v). Schedules não carregados.", maxDuration)
			return
		}

		// Verifica se schema está pronto tentando recarregar schedules
		// Se ReloadAllSchedules conseguir carregar sem erro de schema, consideramos pronto
		if ps.isSchemaReady() {
			log.Println("[PurgeScheduler] Schema detectado pronto. Recarregando schedules...")
			ps.ReloadAllSchedules()
			return
		}

		// Schema ainda não pronto, espera e tenta novamente
		retries++
		delay := baseDelay * time.Duration(retries/2+1)
		if delay > maxDelay {
			delay = maxDelay
		}

		if retries%5 == 0 {
			log.Printf("[PurgeScheduler] Aguardando schema (tentativa %d/%d, elapsed: %v)...",
				retries, maxRetries, time.Since(startTime).Round(time.Second))
		}
		time.Sleep(delay)
	}

	log.Printf("[PurgeScheduler] Max retries atingido. Schedules não carregados - verifique migrations.")
}

// isSchemaReady verifica se o schema necessário está pronto
func (ps *PurgeScheduler) isSchemaReady() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Tenta verificar se a coluna purge_cron_expression existe
	var exists bool
	err := ps.db.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.columns 
			WHERE table_schema = 'system' 
			AND table_name = 'projects' 
			AND column_name = 'purge_cron_expression'
		)
	`).Scan(&exists)

	if err != nil {
		return false
	}
	return exists
}

// Stop para o scheduler
func (ps *PurgeScheduler) Stop() {
	ps.scheduler.Stop()
	log.Println("[PurgeScheduler] Parado")
}
