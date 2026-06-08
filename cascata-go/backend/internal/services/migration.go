package services

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	MigrationLockID = 8675309
	MigrationNotificationChannel = "cascata_migrations_complete"
)

// MigrationCompletionTracker rastreia se migrações foram completadas neste worker
var (
	migrationCompletedOnce sync.Once
	migrationCompletedChan = make(chan struct{}, 1)
)

type MigrationService struct{}

// RunMigrations checks and applies SQL migrations to the system database
func RunMigrations(pool *pgxpool.Pool, migrationsRoot string) error {
	log.Println("[MigrationService] Initializing...")
	
	ctx := context.Background()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("failed to acquire connection for migrations: %v", err)
	}
	defer conn.Release()

	// 1. Try Distributed Lock (Non-Blocking)
	var hasLock bool
	err = conn.QueryRow(ctx, fmt.Sprintf("SELECT pg_try_advisory_lock(%d)", MigrationLockID)).Scan(&hasLock)
	if err != nil {
		return fmt.Errorf("failed to check advisory lock: %v", err)
	}

	if !hasLock {
		log.Println("[MigrationService] Another instance holds the lock. Skipping migrations check to allow fast boot.")
		return nil
	}

	log.Println("[MigrationService] Lock acquired. Starting checks...")
	defer func() {
		_, _ = conn.Exec(ctx, fmt.Sprintf("SELECT pg_advisory_unlock(%d)", MigrationLockID))
		log.Println("[MigrationService] Lock released.")
	}()

	// Ensure system schema and migrations table exist
	_, err = conn.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS system")
	if err != nil {
		return fmt.Errorf("failed to create system schema: %v", err)
	}

	_, err = conn.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS system.migrations (
			id SERIAL PRIMARY KEY,
			name TEXT UNIQUE NOT NULL,
			applied_at TIMESTAMP DEFAULT NOW()
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to create migrations table: %v", err)
	}

	if _, err := os.Stat(migrationsRoot); os.IsNotExist(err) {
		log.Printf("[MigrationService] Warning: Migrations folder not found at %s", migrationsRoot)
		return nil
	}

	entries, err := os.ReadDir(migrationsRoot)
	if err != nil {
		return fmt.Errorf("failed to read migrations directory: %v", err)
	}

	var files []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".sql") {
			files = append(files, entry.Name())
		}
	}
	sort.Strings(files)

	for _, file := range files {
		var exists int
		err := conn.QueryRow(ctx, "SELECT count(*) FROM system.migrations WHERE name = $1", file).Scan(&exists)
		if err != nil {
			log.Printf("[MigrationService] Error checking migration %s: %v", file, err)
			continue
		}

		if exists == 0 {
			// Legacy check: Se o arquivo agora é .sql, verifique se ele já foi aplicado como .sql.txt
			if strings.HasSuffix(file, ".sql") {
				legacyName := file + ".txt"
				err = conn.QueryRow(ctx, "SELECT count(*) FROM system.migrations WHERE name = $1", legacyName).Scan(&exists)
				if err != nil {
					log.Printf("[MigrationService] Error checking legacy migration %s: %v", legacyName, err)
				}
			}
		}

		if exists == 0 {
			log.Printf("[MigrationService] Applying: %s", file)
			
			content, err := os.ReadFile(filepath.Join(migrationsRoot, file))
			if err != nil {
				log.Printf("[MigrationService] Error reading migration %s: %v", file, err)
				continue
			}

			sql := strings.TrimSpace(string(content))
			if sql == "" {
				_, _ = conn.Exec(ctx, "INSERT INTO system.migrations (name) VALUES ($1)", file)
				continue
			}

			// Execute migration in a transaction
			tx, err := conn.Begin(ctx)
			if err != nil {
				log.Printf("[MigrationService] Error starting transaction for %s: %v", file, err)
				continue
			}

			_, err = tx.Exec(ctx, sql)
			if err != nil {
				tx.Rollback(ctx)
				log.Printf("[MigrationService] CRITICAL FAILURE in %s: %v", file, err)
				return fmt.Errorf("migration %s failed: %v", file, err)
			}

			_, err = tx.Exec(ctx, "INSERT INTO system.migrations (name) VALUES ($1)", file)
			if err != nil {
				tx.Rollback(ctx)
				log.Printf("[MigrationService] Error recording migration %s: %v", file, err)
				continue
			}

			if err := tx.Commit(ctx); err != nil {
				log.Printf("[MigrationService] Error committing migration %s: %v", file, err)
				continue
			}
			log.Printf("[MigrationService] Success: %s", file)
		}
	}

	// TODO: Phase 4 - CertificateService.RebuildNginxConfigs(pool)
	// For now we skip or log a message.
	log.Println("[MigrationService] Deployment-level sync triggers pending Phase 4 integration.")

	// Notificar outros workers que migrações completaram (via PostgreSQL NOTIFY)
	// Isso permite que PurgeScheduler e outros serviços saibam quando o schema está pronto
	_, err = conn.Exec(ctx, fmt.Sprintf("NOTIFY %s, 'migrations_complete'", MigrationNotificationChannel))
	if err != nil {
		log.Printf("[MigrationService] Warning: Failed to send migration completion notification: %v", err)
		// Não falha - a notificação é otimização, não requisito
	} else {
		log.Println("[MigrationService] Migration completion notification sent")
	}

	// Sinalizar localmente que migrações completaram neste worker
	migrationCompletedOnce.Do(func() {
		close(migrationCompletedChan)
	})

	return nil
}

// WaitForMigrations aguarda até que migrações sejam completadas (com timeout)
// Retorna true se migrações completaram, false se timeout
func WaitForMigrations(timeout time.Duration) bool {
	select {
	case <-migrationCompletedChan:
		return true
	case <-time.After(timeout):
		return false
	}
}

// IsMigrationCompleted verifica se migrações já foram completadas neste worker
func IsMigrationCompleted() bool {
	select {
	case <-migrationCompletedChan:
		return true
	default:
		return false
	}
}
