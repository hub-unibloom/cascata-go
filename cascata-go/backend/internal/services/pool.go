package services

import (
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"cascata-backend/internal/config"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PoolConfig struct {
	Max                     int32
	IdleTimeout             time.Duration
	ConnectionTimeout       time.Duration
	StatementTimeout        time.Duration
	UseDirect               bool   // Force direct connection (bypass PgBouncer)
	ConnectionString        string // Optional: External connection string
}

type poolEntry struct {
	pool              *pgxpool.Pool
	lastAccessed      time.Time
	activeConnections int32
	isExternal        bool
}

var (
	pools             = make(map[string]*poolEntry)
	poolsMu           sync.RWMutex
	reaperStop        chan struct{}
	
	SystemPool        *pgxpool.Pool
	
	defaultStatementTimeout = 15 * time.Second
	maxIdleTxTime           = "2 minutes"
	maxActivePools          = 500
	idleThreshold           = 5 * time.Minute
	reaperInterval          = 20 * time.Second
)

// InitSystemPool initializes the main system database pool
func InitSystemPool() {
	if config.SystemDatabaseURL == "" {
		log.Fatal("[PoolService] SYSTEM_DATABASE_URL is not set")
	}

	conf, err := pgxpool.ParseConfig(config.SystemDatabaseURL)
	if err != nil {
		log.Fatalf("[PoolService] Failed to parse system DB URL: %v", err)
	}

	conf.MaxConns = 25
	conf.MaxConnIdleTime = 30 * time.Second

	// PgBouncer / Transaction pooler compatibility for System Pool
	if strings.Contains(config.SystemDatabaseURL, "pgbouncer") {
		conf.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeExec
	}

	pool, err := pgxpool.NewWithConfig(context.Background(), conf)
	if err != nil {
		log.Fatalf("[PoolService] Failed to create system pool: %v", err)
	}

	SystemPool = pool
	log.Println("[PoolService] System Pool Initialized.")

	// Ensure storage_objects table exists in system database (CRITICAL for storage functionality)
	if err := EnsureStorageObjectsTable(context.Background()); err != nil {
		log.Printf("[PoolService:Warn] Failed to ensure storage_objects table: %v", err)
	}

	// Ensure nexus_automations table has the branch_name column
	if err := EnsureNexusAutomationsBranchColumn(context.Background()); err != nil {
		log.Printf("[PoolService:Warn] Failed to ensure branch_name on nexus_automations: %v", err)
	}
}

// InitReaper starts the background maintenance tasks
func InitReaper() {
	if reaperStop != nil {
		close(reaperStop)
	}
	reaperStop = make(chan struct{})

	go func() {
		ticker := time.NewTicker(reaperInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				reapZombies()
				err := killIdleTransactions()
				if err != nil {
					log.Printf("[PoolService] Idle Killer Error: %v", err)
				}
			case <-reaperStop:
				return
			}
		}
	}()
	log.Println("[PoolService] Smart Reaper initialized (Aggressive Mode).")
}

func killIdleTransactions() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	sql := fmt.Sprintf(`
		SELECT pg_terminate_backend(pid)
		FROM pg_stat_activity
		WHERE state = 'idle in transaction'
		AND state_change < NOW() - INTERVAL '%s'
		AND datname IS NOT NULL
		AND pid <> pg_backend_pid()
	`, maxIdleTxTime)

	tag, err := SystemPool.Exec(ctx, sql)
	if err != nil {
		return err
	}

	if tag.RowsAffected() > 0 {
		log.Printf("[PoolService] ☢️ Killed %d zombie transactions (Idle > %s).", tag.RowsAffected(), maxIdleTxTime)
	}
	return nil
}

func reapZombies() {
	now := time.Now()
	poolsMu.Lock()
	defer poolsMu.Unlock()

	var closedCount int
	
	// Create sortable list of entries
	type entryWithKey struct {
		key   string
		entry *poolEntry
	}
	var entries []entryWithKey
	for k, v := range pools {
		entries = append(entries, entryWithKey{k, v})
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].entry.lastAccessed.Before(entries[j].entry.lastAccessed)
	})

	for _, ek := range entries {
		if now.Sub(ek.entry.lastAccessed) > idleThreshold {
			ek.entry.pool.Close()
			delete(pools, ek.key)
			closedCount++
		}
	}

	if len(pools) > maxActivePools {
		toRemove := len(pools) - maxActivePools
		log.Printf("[PoolService] Hard Cap Reached (%d). Ejecting %d oldest pools.", len(pools), toRemove)
		
		// Sort remaining again
		var remaining []entryWithKey
		for k, v := range pools {
			remaining = append(remaining, entryWithKey{k, v})
		}
		sort.Slice(remaining, func(i, j int) bool {
			return remaining[i].entry.lastAccessed.Before(remaining[j].entry.lastAccessed)
		})

		for i := 0; i < toRemove && i < len(remaining); i++ {
			remaining[i].entry.pool.Close()
			delete(pools, remaining[i].key)
			closedCount++
		}
	}

	if closedCount > 0 {
		log.Printf("[PoolService] Reaped %d pools.", closedCount)
	}
}

// Get retrieves or creates a pool for a specific database
func Get(dbIdentifier string, cfg *PoolConfig) *pgxpool.Pool {
	poolsMu.Lock()
	defer poolsMu.Unlock()

	if cfg == nil {
		cfg = &PoolConfig{}
	}

	uniqueKey := ""
	if cfg.ConnectionString != "" {
		hash := base64.StdEncoding.EncodeToString([]byte(cfg.ConnectionString))
		if len(hash) > 10 {
			hash = hash[:10]
		}
		uniqueKey = fmt.Sprintf("ext_%s_%s", dbIdentifier, hash)
	} else {
		mode := "pool"
		if cfg.UseDirect {
			mode = "direct"
		}
		uniqueKey = fmt.Sprintf("%s_%s", dbIdentifier, mode)
	}

	if entry, ok := pools[uniqueKey]; ok {
		entry.lastAccessed = time.Now()
		return entry.pool
	}

	var dbURL string
	isExternal := false

	if cfg.ConnectionString != "" {
		dbURL = cfg.ConnectionString
		parsed, err := url.Parse(dbURL)
		if err == nil {
			internalHosts := map[string]bool{
				os.Getenv("DB_DIRECT_HOST"): true,
				os.Getenv("DB_POOL_HOST"):   true,
				"db":                        true,
				"pgbouncer":                 true,
				"localhost":                 true,
				"127.0.0.1":                 true,
			}
			if !internalHosts[parsed.Hostname()] {
				isExternal = true
			}
		} else {
			if !strings.Contains(dbURL, "db") && !strings.Contains(dbURL, "pgbouncer") {
				isExternal = true
			}
		}
	} else {
		usePooler := !cfg.UseDirect
		host := os.Getenv("DB_POOL_HOST")
		if host == "" { host = "pgbouncer" }
		if !usePooler {
			host = os.Getenv("DB_DIRECT_HOST")
			if host == "" { host = "db" }
		}
		port := os.Getenv("DB_POOL_PORT")
		if port == "" { port = "6432" }
		if !usePooler {
			port = os.Getenv("DB_DIRECT_PORT")
			if port == "" { port = "5432" }
		}
		user := url.QueryEscape(os.Getenv("DB_USER"))
		pass := url.QueryEscape(os.Getenv("DB_PASS"))

		dbURL = fmt.Sprintf("postgresql://%s:%s@%s:%s/%s", user, pass, host, port, dbIdentifier)
	}

	pgxCfg, err := pgxpool.ParseConfig(dbURL)
	if err != nil {
		log.Printf("[PoolService] Error parsing DB URL for %s: %v", dbIdentifier, err)
		return nil
	}

	pgxCfg.MaxConns = cfg.Max
	if pgxCfg.MaxConns == 0 {
		pgxCfg.MaxConns = 10
	}
	
	if cfg.IdleTimeout > 0 {
		pgxCfg.MaxConnIdleTime = cfg.IdleTimeout
	} else {
		pgxCfg.MaxConnIdleTime = 60 * time.Second
	}

	// PgBouncer / Transaction pooler compatibility
	if !cfg.UseDirect || strings.Contains(dbURL, "pgbouncer") {
		pgxCfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeExec
	}

	// SSL Logic
	if isExternal {
		// Equivalent to rejectUnauthorized: false in Node
		pgxCfg.ConnConfig.Config.TLSConfig = nil // Let pgx handle SSL mode from URL or default
		if !strings.Contains(dbURL, "sslmode=") {
			dbURL += "&sslmode=require"
			// Recalculate config with sslmode
			pgxCfg, _ = pgxpool.ParseConfig(dbURL)
		}
	}

	// Performance Tuning
	pgxCfg.ConnConfig.Config.RuntimeParams = map[string]string{
		"application_name": fmt.Sprintf("cascata-go-%s", os.Getenv("SERVICE_MODE")),
	}

	pool, err := pgxpool.NewWithConfig(context.Background(), pgxCfg)
	if err != nil {
		log.Printf("[PoolService] Error creating pool for %s: %v", dbIdentifier, err)
		return nil
	}

	pools[uniqueKey] = &poolEntry{
		pool:         pool,
		lastAccessed: time.Now(),
		isExternal:   isExternal,
	}

	return pool
}

// CloseAll closes all managed pools
func CloseAll() {
	poolsMu.Lock()
	defer poolsMu.Unlock()
	for k, entry := range pools {
		entry.pool.Close()
		delete(pools, k)
	}
	if SystemPool != nil {
		SystemPool.Close()
	}
	log.Println("[PoolService] All pools closed.")
}

// Reload closes and removes a specific pool to force recreation
// This fixes "cached plan must not change result type" errors after DDL
// ISOLATION FIX: Uses exact match (dbIdentifier + suffix) instead of substring.
// Previously strings.Contains caused Reload("cascata_proj") to also kill
// "cascata_proj_branch1_env_pool" — destroying OTHER branches' pools.
func Reload(dbIdentifier string) {
	poolsMu.Lock()
	defer poolsMu.Unlock()

	// Exact match: the pool key is always "{dbIdentifier}_{mode}" (e.g. "cascata_proj_pool")
	// or "ext_{dbIdentifier}_{hash}" for external connections.
	// We match ONLY keys that are exactly this database, not substrings.
	poolSuffix := fmt.Sprintf("%s_pool", dbIdentifier)
	directSuffix := fmt.Sprintf("%s_direct", dbIdentifier)

	for key, entry := range pools {
		if key == poolSuffix || key == directSuffix || key == dbIdentifier {
			entry.pool.Close()
			delete(pools, key)
			log.Printf("[PoolService] Reloaded pool for %s (key: %s)", dbIdentifier, key)
		}
	}
}

// EnsureStorageObjectsTable ensures the storage_objects table exists in the system database
// This is CRITICAL for storage functionality - the table tracks metadata for all storage objects
func EnsureStorageObjectsTable(ctx context.Context) error {
	if SystemPool == nil {
		return fmt.Errorf("SystemPool not initialized")
	}

	// Create the table with all necessary columns and constraints
	createTableSQL := `
		CREATE TABLE IF NOT EXISTS system.storage_objects (
			id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
			project_slug TEXT NOT NULL,
			branch_name TEXT NOT NULL DEFAULT 'main',
			bucket TEXT NOT NULL,
			name TEXT NOT NULL,
			parent_path TEXT NOT NULL DEFAULT '',
			full_path TEXT NOT NULL,
			is_folder BOOLEAN DEFAULT false,
			size BIGINT DEFAULT 0,
			mime_type TEXT,
			provider TEXT DEFAULT 'local',
			external_id TEXT,
			rls_enabled BOOLEAN DEFAULT true,
			created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
			updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
			UNIQUE(project_slug, branch_name, bucket, full_path)
		)
	`

	_, err := SystemPool.Exec(ctx, createTableSQL)
	if err != nil {
		return fmt.Errorf("failed to create storage_objects table: %w", err)
	}

	// Migrate existing table to add branch_name column if missing, and adjust unique constraint
	migrationSQL := `
		ALTER TABLE system.storage_objects ADD COLUMN IF NOT EXISTS branch_name TEXT NOT NULL DEFAULT 'main';
		DO $$
		BEGIN
			-- Drop the old unique constraint if it exists
			IF EXISTS (
				SELECT 1 FROM information_schema.table_constraints 
				WHERE constraint_name = 'storage_objects_project_slug_bucket_full_path_key' 
				  AND table_schema = 'system'
			) THEN
				ALTER TABLE system.storage_objects DROP CONSTRAINT storage_objects_project_slug_bucket_full_path_key;
			END IF;
			
			-- Add the new unique constraint if it doesn't exist
			IF NOT EXISTS (
				SELECT 1 FROM information_schema.table_constraints 
				WHERE constraint_name = 'storage_objects_project_slug_branch_name_bucket_full_path_key' 
				  AND table_schema = 'system'
			) THEN
				ALTER TABLE system.storage_objects ADD CONSTRAINT storage_objects_project_slug_branch_name_bucket_full_path_key UNIQUE (project_slug, branch_name, bucket, full_path);
			END IF;
		END $$;
	`
	_, _ = SystemPool.Exec(ctx, migrationSQL)

	if err != nil {
		return fmt.Errorf("failed to create storage_objects table: %w", err)
	}

	// Create indexes for performance
	indexes := []string{
		`CREATE INDEX IF NOT EXISTS idx_storage_parent ON system.storage_objects (project_slug, bucket, parent_path)`,
		`CREATE INDEX IF NOT EXISTS idx_storage_search ON system.storage_objects (project_slug, name text_pattern_ops)`,
		`CREATE INDEX IF NOT EXISTS idx_storage_bucket_rls ON system.storage_objects (project_slug, bucket, rls_enabled) WHERE is_folder = true AND parent_path = ''`,
	}

	for _, idxSQL := range indexes {
		_, err := SystemPool.Exec(ctx, idxSQL)
		if err != nil {
			log.Printf("[PoolService:Warn] Failed to create storage_objects index: %v", err)
			// Don't fail - indexes are optional for functionality
		}
	}

	log.Println("[PoolService] storage_objects table ensured in system database")
	return nil
}

// EnsureNexusAutomationsBranchColumn ensures system.nexus_automations has the branch_name column and correct primary key constraint
func EnsureNexusAutomationsBranchColumn(ctx context.Context) error {
	if SystemPool == nil {
		return fmt.Errorf("SystemPool not initialized")
	}
	_, err := SystemPool.Exec(ctx, `
		ALTER TABLE system.nexus_automations ADD COLUMN IF NOT EXISTS branch_name TEXT NOT NULL DEFAULT 'main';
	`)
	if err != nil {
		return err
	}

	// Check if primary key already includes tenant_id and branch_name to avoid unnecessary and failing migrations
	var pkColumns []string
	rows, err := SystemPool.Query(ctx, `
		SELECT a.attname
		FROM pg_index i
		JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = ANY(i.indkey)
		WHERE i.indrelid = 'system.nexus_automations'::regclass
		AND i.indisprimary
		ORDER BY a.attnum;
	`)
	if err != nil {
		log.Printf("[PoolService:Warn] Failed to check primary key structure: %v", err)
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var col string
		if err := rows.Scan(&col); err != nil {
			return err
		}
		pkColumns = append(pkColumns, col)
	}

	// If PK already has the correct structure (id, tenant_id, branch_name), skip migration
	if len(pkColumns) == 3 && pkColumns[0] == "id" && pkColumns[1] == "tenant_id" && pkColumns[2] == "branch_name" {
		log.Println("[PoolService] Primary key already has correct structure (id, tenant_id, branch_name), skipping migration")
		return nil
	}

	// Re-create primary key constraint to include branch_name and tenant_id for branch-isolated identical IDs
	// This requires dropping foreign keys first, then recreating them after PK change
	log.Println("[PoolService] Updating primary key to include branch_name and tenant_id")
	
	// Drop foreign keys that depend on the current primary key
	// Note: Foreign keys will be recreated properly by migration 065 with composite keys
	SystemPool.Exec(ctx, `ALTER TABLE system.nexus_automation_alerts DROP CONSTRAINT IF EXISTS nexus_automation_alerts_automation_id_fkey;`)
	SystemPool.Exec(ctx, `ALTER TABLE system.nexus_execution_log DROP CONSTRAINT IF EXISTS nexus_execution_log_automation_id_fkey;`)
	
	// Drop and recreate primary key
	_, err = SystemPool.Exec(ctx, `
		ALTER TABLE system.nexus_automations DROP CONSTRAINT IF EXISTS nexus_automations_pkey;
		ALTER TABLE system.nexus_automations ADD CONSTRAINT nexus_automations_pkey PRIMARY KEY (id, tenant_id, branch_name);
	`)
	if err != nil {
		log.Printf("[PoolService:Error] Failed to update primary key: %v", err)
		return err
	}
	
	log.Println("[PoolService] Successfully updated primary key. Foreign keys will be recreated by migration 065")
	return nil
}

