package services

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"
	"cascata-backend/internal/types"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	slugCache   = make(map[string]*slugCacheEntry)
	slugCacheMu sync.RWMutex
)

type slugCacheEntry struct {
	project *types.Project
	expiry  time.Time
}

// GetProjectByDomain retrieves a project by its custom domain
func GetProjectByDomain(ctx context.Context, domain string) *types.Project {
	var p types.Project
	var metadataBytes []byte

	var customDomain *string
	var blocklist []string
	err := SystemPool.QueryRow(ctx, 
		"SELECT id, name, slug, db_name, custom_domain, status, jwt_secret, anon_key, service_key, blocklist, metadata FROM system.projects WHERE custom_domain = $1", 
		domain).Scan(&p.ID, &p.Name, &p.Slug, &p.DbName, &customDomain, &p.Status, &p.JWTSecret, &p.AnonKey, &p.ServiceKey, &blocklist, &metadataBytes)
	
	if err != nil {
		return nil
	}
	
	if customDomain != nil {
		p.CustomDomain = *customDomain
	}

	p.Blocklist = blocklist

	// Unmarshal metadata manually
	json.Unmarshal(metadataBytes, &p.Metadata)

	// Extract 'extra' field manually (it has json:"-" tag)
	var metadataMap map[string]interface{}
	if err := json.Unmarshal(metadataBytes, &metadataMap); err == nil {
		if extra, ok := metadataMap["extra"].(map[string]interface{}); ok {
			p.Metadata.Extra = extra
		}
	}

	// Build App Client index for O(1) lookups (runtime cache)
	if len(p.Metadata.AppClients) > 0 {
		p.AppClientIndex = BuildAppClientIndex(p.Metadata.AppClients, p.JWTSecret)
	}

	return &p
}

// GetProjectBySlug retrieves a project by its URL slug
func GetProjectBySlug(ctx context.Context, slug string) *types.Project {
	// 1. Tenta Cache Local (Performance Hot-Path)
	slugCacheMu.RLock()
	entry, ok := slugCache[slug]
	slugCacheMu.RUnlock()
	
	if ok && time.Now().Before(entry.expiry) {
		return entry.project
	}

	var p types.Project
	var metadataBytes []byte

	var customDomain *string
	var blocklist []string // pgx handles []string for text[]
	
	query := "SELECT id, name, slug, db_name, custom_domain, status, jwt_secret, anon_key, service_key, blocklist, metadata FROM system.projects WHERE slug = $1"
	err := SystemPool.QueryRow(ctx, query, slug).Scan(
		&p.ID, &p.Name, &p.Slug, &p.DbName, &customDomain, &p.Status, 
		&p.JWTSecret, &p.AnonKey, &p.ServiceKey, &blocklist, &metadataBytes)
	
	if err != nil {
		log.Printf("[ProjectService] Lookup failed for slug '%s': %v", slug, err)
		return nil
	}

	// ... resto do parse ...
	if customDomain != nil { p.CustomDomain = *customDomain }
	p.Blocklist = blocklist
	if p.Blocklist == nil { p.Blocklist = []string{} }

	if err := json.Unmarshal(metadataBytes, &p.Metadata); err != nil {
		log.Printf("[ProjectService] Metadata unmarshal failed for %s: %v", slug, err)
	}

	var metadataMap map[string]interface{}
	if err := json.Unmarshal(metadataBytes, &metadataMap); err == nil {
		if extra, ok := metadataMap["extra"].(map[string]interface{}); ok {
			p.Metadata.Extra = extra
		}
	}

	if len(p.Metadata.AppClients) > 0 {
		p.AppClientIndex = BuildAppClientIndex(p.Metadata.AppClients, p.JWTSecret)
	}

	// 2. Salva no Cache Local (TTL curto para garantir consistência)
	slugCacheMu.Lock()
	slugCache[slug] = &slugCacheEntry{
		project: &p,
		expiry:  time.Now().Add(1 * time.Minute),
	}
	slugCacheMu.Unlock()

	return &p
}

// InvalidateProjectCache removes a project from the local struct cache
func InvalidateProjectCache(slug string) {
	slugCacheMu.Lock()
	delete(slugCache, slug)
	slugCacheMu.Unlock()
}

// GetProjectByApiKey retrieves a project by its anon_key or service_key (Sticky Tenancy)
func GetProjectByApiKey(ctx context.Context, key string) *types.Project {
	var p types.Project
	var metadataBytes []byte

	var customDomain *string
	var blocklist []string
	// Buscamos em ambas as colunas de chaves
	err := SystemPool.QueryRow(ctx, 
		"SELECT id, name, slug, db_name, custom_domain, status, jwt_secret, anon_key, service_key, blocklist, metadata FROM system.projects WHERE anon_key = $1 OR service_key = $1", 
		key).Scan(&p.ID, &p.Name, &p.Slug, &p.DbName, &customDomain, &p.Status, &p.JWTSecret, &p.AnonKey, &p.ServiceKey, &blocklist, &metadataBytes)
	
	if err != nil {
		return nil
	}
	
	if customDomain != nil {
		p.CustomDomain = *customDomain
	}
	p.Blocklist = blocklist

	json.Unmarshal(metadataBytes, &p.Metadata)

	// Extract 'extra' field manually (it has json:"-" tag)
	var metadataMap map[string]interface{}
	if err := json.Unmarshal(metadataBytes, &metadataMap); err == nil {
		if extra, ok := metadataMap["extra"].(map[string]interface{}); ok {
			p.Metadata.Extra = extra
		}
	}

	// Build App Client index for O(1) lookups (runtime cache)
	if len(p.Metadata.AppClients) > 0 {
		p.AppClientIndex = BuildAppClientIndex(p.Metadata.AppClients, p.JWTSecret)
	}

	return &p
}

// GetProjectPool provides a connection pool for a specific project and environment
// EAGER version: conecta e inicializa schemas imediatamente
func GetProjectPool(project *types.Project, env string) (*pgxpool.Pool, error) {
	var dbName string
	var connString string
	
	// BYOD: Se o projeto tem external_db_url configurado, usa o banco externo
	if project.Metadata.ExternalDbUrl != "" {
		connString = project.Metadata.ExternalDbUrl
		// Nota: env "draft" não faz sentido para banco externo
		// branches de dados ficam no Cascata local nesse caso
		dbName = "external"
	} else {
		dbName = project.DbName
	}

	// ISOLATION GUARD: Se não for o ambiente 'live' (ou 'main', que é sinônimo),
	// resolvemos o banco da branch. JAMAIS fazemos fallback silencioso para live.
	// Cada branch é isolada e independente — se não está materializada, é ERRO.
	if env != "" && env != "live" && env != "main" && project.Metadata.ExternalDbUrl == "" {
		var branchDBName *string
		err := SystemPool.QueryRow(context.Background(),
			`SELECT COALESCE(materialized_db, data_branch_db_name)
			 FROM system.branches
			 WHERE project_slug = $1 AND name = $2 AND status = 'active'`,
			project.Slug, env).Scan(&branchDBName)

		if err == nil && branchDBName != nil && *branchDBName != "" {
			dbName = *branchDBName
			log.Printf("[ProjectPool] Isolation Active: Redirecting %s to branch DB %s", project.Slug, dbName)
		} else {
			// SEGURANÇA: Não fazemos fallback silencioso para live.
			// Se a branch não está materializada, retornamos ERRO.
			// O owner deve chamar /branch/access primeiro.
			log.Printf("[ProjectPool:BLOCK] Branch %s not materialized for project %s. Refusing fallback to Live.", env, project.Slug)
			return nil, fmt.Errorf("branch '%s' is not materialized — call /branch/access first to activate it", env)
		}
	}

	// Dynamic Pool Configuration with production defaults
	maxConns := int32(project.Metadata.DbConfig.MaxConnections)
	if maxConns <= 0 {
		maxConns = 10 // Sovereign Safe Default
	}

	var pool *pgxpool.Pool
	var err error
	
	// BYOD: Se tem connString externa, cria pool direto
	if connString != "" {
		pool, err = pgxpool.New(context.Background(), connString)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize external connection pool: %w", err)
		}
	} else {
		// Use the Pool Service to get or create a pool local
		pool = Get(dbName, &PoolConfig{
			Max: maxConns,
		})

		if pool == nil {
			return nil, fmt.Errorf("failed to initialize connection pool for %s", dbName)
		}
	}

	// Initialize ALL tenant schemas synchronously (blocking) to ensure they exist before any queries
	// This fixes the issue where GetSchemas returns only "public" because managed schemas don't exist yet
	dbSvc := DatabaseService{}
	if err := dbSvc.InitTenantSchemas(context.Background(), dbName); err != nil {
		log.Printf("[ProjectService] Tenant schemas init failed for %s: %v", dbName, err)
		// Don't return error - pool is still usable even if schema init fails
	}

	// CRITICAL: Always ensure auth functions exist (idempotent operation)
	// This handles cases where the tenant is "initialized" in cache but auth functions are missing
	if err := dbSvc.EnsureAuthFunctions(context.Background(), dbName); err != nil {
		log.Printf("[ProjectService] Failed to ensure auth functions for %s: %v", dbName, err)
		// Don't fail - the functions might already exist
	}

	return pool, nil
}

// GetProjectPoolLazy provides a connection pool WITHOUT initializing schemas
// LAZY version: não executa queries no banco do tenant - ideal para rate limiting
// Os schemas serão inicializados automaticamente na primeira query real
func GetProjectPoolLazy(project *types.Project, env string) (*pgxpool.Pool, error) {
	var dbName string
	var connString string
	
	// BYOD: Se o projeto tem external_db_url configurado, usa o banco externo
	if project.Metadata.ExternalDbUrl != "" {
		connString = project.Metadata.ExternalDbUrl
		// Nota: env "draft" não faz sentido para banco externo
		dbName = "external"
	} else {
		dbName = project.DbName
	}

	// ISOLATION GUARD: Se não for o ambiente 'live' (ou 'main'), resolvemos o banco da branch (LAZY)
	// Mesma lógica de isolamento que GetProjectPool — sem fallback silencioso.
	if env != "" && env != "live" && env != "main" {
		var branchDBName *string
		err := SystemPool.QueryRow(context.Background(),
			`SELECT COALESCE(materialized_db, data_branch_db_name)
			 FROM system.branches
			 WHERE project_slug = $1 AND name = $2 AND status = 'active'`,
			project.Slug, env).Scan(&branchDBName)

		if err == nil && branchDBName != nil && *branchDBName != "" {
			dbName = *branchDBName
		} else {
			return nil, fmt.Errorf("branch '%s' is not materialized — call /branch/access first to activate it", env)
		}
	}

	// Dynamic Pool Configuration with production defaults
	maxConns := int32(project.Metadata.DbConfig.MaxConnections)
	if maxConns <= 0 {
		maxConns = 10 // Sovereign Safe Default
	}

	var pool *pgxpool.Pool
	var err error
	
	// BYOD: Se tem connString externa, cria pool direto
	if connString != "" {
		pool, err = pgxpool.New(context.Background(), connString)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize external connection pool: %w", err)
		}
	} else {
		// Use the Pool Service to get or create a pool local
		// NOTA: O pool é criado mas NÃO executa queries de inicialização
		pool = Get(dbName, &PoolConfig{
			Max: maxConns,
		})

		if pool == nil {
			return nil, fmt.Errorf("failed to initialize connection pool for %s", dbName)
		}
	}

	// LAZY: Não chama InitTenantSchemas aqui!
	// Schemas serão inicializados automaticamente em GetProjectPool (non-lazy) quando necessário

	// CRITICAL: Always ensure auth functions exist (idempotent, fast check)
	// This is needed because OAuth callback uses lazy pool and needs auth.upsert_user_v2
	dbSvc := DatabaseService{}
	if err := dbSvc.EnsureAuthFunctions(context.Background(), dbName); err != nil {
		log.Printf("[ProjectService] Lazy: Failed to ensure auth functions for %s: %v", dbName, err)
	}

	return pool, nil
}
