package services

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
)

// tenantInitLocks previne race condition na inicialização de schemas
var tenantInitLocks = make(map[string]*sync.Mutex)
var tenantInitLocksMu sync.Mutex

// tenantInitCache rastreia quais bancos já foram inicializados neste processo
// para evitar re-executar InitTenantSchemas em cada request
var tenantInitCache = make(map[string]bool)
var tenantInitCacheMu sync.RWMutex

// isTenantInitialized verifica se o banco já foi inicializado no cache local
func isTenantInitialized(dbName string) bool {
	tenantInitCacheMu.RLock()
	defer tenantInitCacheMu.RUnlock()
	return tenantInitCache[dbName]
}

// markTenantInitialized marca o banco como inicializado no cache local
func markTenantInitialized(dbName string) {
	tenantInitCacheMu.Lock()
	defer tenantInitCacheMu.Unlock()
	tenantInitCache[dbName] = true
}

type DatabaseService struct{}

// getTenantConnectionString builds a connection string for tenant databases using superuser
// This avoids TLS/auth issues when connecting to newly created tenant DBs
func getTenantConnectionString(dbName string) string {
	host := os.Getenv("DB_DIRECT_HOST")
	if host == "" {
		host = "db"
	}
	port := os.Getenv("DB_DIRECT_PORT")
	if port == "" {
		port = "5432"
	}
	
	// First try DB_SUPERUSER/DB_SUPERUSER_PASSWORD, fallback to DB_USER/DB_PASS
	user := os.Getenv("DB_SUPERUSER")
	password := os.Getenv("DB_SUPERUSER_PASSWORD")
	
	// If superuser not set, fallback to regular DB_USER/DB_PASS
	if user == "" {
		user = os.Getenv("DB_USER")
	}
	if password == "" {
		password = os.Getenv("DB_PASS")
	}
	
	// Final fallback to postgres defaults
	if user == "" {
		user = "postgres"
	}
	if password == "" {
		password = "postgres"
	}
	
	return fmt.Sprintf("host=%s port=%s dbname=%s user=%s password=%s sslmode=disable",
		host, port, dbName, user, password)
}
func (s *DatabaseService) CreateDatabase(ctx context.Context, dbName string) error {
	// PostgreSQL sanitization (Identifiers cannot be parameters)
	target := pgx.Identifier{dbName}.Sanitize()
	sql := fmt.Sprintf("CREATE DATABASE %s", target)
	
	_, err := SystemPool.Exec(ctx, sql)
	if err != nil {
		log.Printf("[DatabaseService] Error creating database %s: %v", dbName, err)
		return err
	}
	return nil
}

// CreateSnapshot creates a copy of a database using a template
func (s *DatabaseService) CreateSnapshot(ctx context.Context, sourceDb, targetDb string) error {
	// Identifier names in PostgreSQL cannot be parameterized. 
	// We MUST sanitize them using pgx.Identifier format or manual quoting to prevent SQL injection.
	source := pgx.Identifier{sourceDb}.Sanitize()
	target := pgx.Identifier{targetDb}.Sanitize()

	sql := fmt.Sprintf("CREATE DATABASE %s WITH TEMPLATE %s", target, source)
	
	_, err := SystemPool.Exec(ctx, sql)
	if err != nil {
		log.Printf("[DatabaseService] Error creating snapshot %s -> %s: %v", sourceDb, targetDb, err)
		return err
	}
	return nil
}

// DropDatabase removes a database
func (s *DatabaseService) DropDatabase(ctx context.Context, dbName string) error {
	target := pgx.Identifier{dbName}.Sanitize()
	sql := fmt.Sprintf("DROP DATABASE IF EXISTS %s", target)
	
	_, err := SystemPool.Exec(ctx, sql)
	return err
}

// PerformDatabaseSwap renames databases, typically for promotion (draft -> live)
func (s *DatabaseService) PerformDatabaseSwap(ctx context.Context, liveDb, draftDb string) error {
	// ALTER DATABASE RENAME cannot run inside a transaction block in PostgreSQL.
	// We execute these as a sequence of independent commands.
	
	tempDb := fmt.Sprintf("%s_old_%d", liveDb, time.Now().Unix())
	
	liveIdent := pgx.Identifier{liveDb}.Sanitize()
	draftIdent := pgx.Identifier{draftDb}.Sanitize()
	tempIdent := pgx.Identifier{tempDb}.Sanitize()

	// 1. Rename live to temp
	_, err := SystemPool.Exec(ctx, fmt.Sprintf("ALTER DATABASE %s RENAME TO %s", liveIdent, tempIdent))
	if err != nil { return err }

	// 2. Rename draft to live
	_, err = SystemPool.Exec(ctx, fmt.Sprintf("ALTER DATABASE %s RENAME TO %s", draftIdent, liveIdent))
	if err != nil {
		// Attempt rollback of the first rename
		SystemPool.Exec(ctx, fmt.Sprintf("ALTER DATABASE %s RENAME TO %s", tempIdent, liveIdent))
		return err
	}

	// 3. Drop old live (optional/delayed in some systems, here we do it for clean swap)
	go func() {
		// Small delay to ensure all connections are closed
		time.Sleep(5 * time.Second)
		SystemPool.Exec(context.Background(), fmt.Sprintf("DROP DATABASE %s", tempIdent))
	}()

	return nil
}

// InitAuthSchema initializes the auth schema and roles in a project database
func (s *DatabaseService) InitAuthSchema(ctx context.Context, dbName string) error {
	// Use superuser connection string for tenant DB initialization
	connStr := getTenantConnectionString(dbName)

	conn, err := pgx.Connect(ctx, connStr)
	if err != nil {
		return fmt.Errorf("failed to connect to project db: %w", err)
	}
	defer conn.Close(ctx)

	// Create auth schema and tables
	authSQL := `
		-- Create auth schema
		CREATE SCHEMA IF NOT EXISTS auth;

		-- Create user_concatenation enum in public and auth schemas
		DO $$
		BEGIN
			IF NOT EXISTS (SELECT 1 FROM pg_type t JOIN pg_namespace n ON n.oid = t.typnamespace WHERE t.typname = 'user_concatenation' AND n.nspname = 'public') THEN
				CREATE TYPE public.user_concatenation AS ENUM ('vazio');
			END IF;
			IF NOT EXISTS (SELECT 1 FROM pg_type t JOIN pg_namespace n ON n.oid = t.typnamespace WHERE t.typname = 'user_concatenation' AND n.nspname = 'auth') THEN
				CREATE TYPE auth.user_concatenation AS ENUM ('vazio');
			END IF;
		END $$;

		-- Create roles if they don't exist
		DO $$
		BEGIN
			IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'anon') THEN
				CREATE ROLE anon NOLOGIN;
			END IF;
			IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'authenticated') THEN
				CREATE ROLE authenticated NOLOGIN;
			END IF;
			IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'service_role') THEN
				CREATE ROLE service_role NOLOGIN;
			END IF;
			IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'cascata_api_role') THEN
				CREATE ROLE cascata_api_role NOLOGIN;
			END IF;
		END $$;

		-- Create auth.users table
		CREATE TABLE IF NOT EXISTS auth.users (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
			last_sign_in_at TIMESTAMP WITH TIME ZONE,
			raw_user_meta_data JSONB DEFAULT '{}',
			banned BOOLEAN DEFAULT FALSE,
			email_confirmed_at TIMESTAMP WITH TIME ZONE,
			user_concatenation public.user_concatenation[] DEFAULT '{vazio}'
		);

		-- Ensure column exists if table was already created (Zero Regression)
		ALTER TABLE auth.users ADD COLUMN IF NOT EXISTS user_concatenation public.user_concatenation[] DEFAULT '{vazio}';

		-- Create auth.identities table
		CREATE TABLE IF NOT EXISTS auth.identities (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			user_id UUID NOT NULL REFERENCES auth.users(id) ON DELETE CASCADE,
			provider TEXT NOT NULL,
			identifier TEXT NOT NULL,
			password_hash TEXT,
			identity_data JSONB DEFAULT '{}',
			created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
			last_sign_in_at TIMESTAMP WITH TIME ZONE,
			verified_at TIMESTAMP WITH TIME ZONE,
			created_via_origin TEXT,
			UNIQUE(provider, identifier)
		);

		-- Create auth.refresh_tokens table
		CREATE TABLE IF NOT EXISTS auth.refresh_tokens (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			token_hash TEXT NOT NULL,
			user_id UUID NOT NULL REFERENCES auth.users(id) ON DELETE CASCADE,
			revoked BOOLEAN DEFAULT FALSE,
			created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
			expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
			parent_token UUID,
			ip_address TEXT,
			user_agent TEXT,
			fingerprint_hash TEXT
		);

		-- Index for fingerprint-based session security
		CREATE INDEX IF NOT EXISTS idx_refresh_tokens_fingerprint ON auth.refresh_tokens(fingerprint_hash);

		-- Create auth.audit_log table
		CREATE TABLE IF NOT EXISTS auth.audit_log (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			user_id UUID REFERENCES auth.users(id) ON DELETE SET NULL,
			event TEXT NOT NULL,
			provider TEXT,
			identifier TEXT,
			origin TEXT,
			ip_address TEXT,
			status TEXT,
			policy_name TEXT,
			metadata JSONB DEFAULT '{}',
			created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
		);

		-- Create auth.otp_codes table
		CREATE TABLE IF NOT EXISTS auth.otp_codes (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			provider TEXT NOT NULL,
			identifier TEXT NOT NULL,
			code_hash TEXT NOT NULL,
			expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
			attempts INTEGER DEFAULT 0,
			metadata JSONB DEFAULT '{}',
			ip_address TEXT,
			created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
		);

		-- Create auth.user_devices table (for push notifications)
		CREATE TABLE IF NOT EXISTS auth.user_devices (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			user_id UUID NOT NULL,
			token TEXT NOT NULL,
			platform TEXT NOT NULL CHECK (platform IN ('android', 'ios', 'web', 'desktop')),
			app_version TEXT,
			device_model TEXT,
			os_version TEXT,
			is_active BOOLEAN DEFAULT true,
			last_active_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
			created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
			updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
			metadata JSONB DEFAULT '{}',
			UNIQUE(token)
		);

		-- Create indexes
		CREATE INDEX IF NOT EXISTS idx_identities_user ON auth.identities(user_id);
		CREATE INDEX IF NOT EXISTS idx_identities_provider_identifier ON auth.identities(provider, identifier);
		CREATE INDEX IF NOT EXISTS idx_refresh_tokens_user ON auth.refresh_tokens(user_id);
		CREATE INDEX IF NOT EXISTS idx_refresh_tokens_hash ON auth.refresh_tokens(token_hash);
		CREATE INDEX IF NOT EXISTS idx_otp_codes_expires ON auth.otp_codes(expires_at);
		CREATE INDEX IF NOT EXISTS idx_audit_log_user ON auth.audit_log(user_id);
		CREATE INDEX IF NOT EXISTS idx_audit_log_created ON auth.audit_log(created_at);

		-- Create indexes for auth.user_devices (push notifications)
		CREATE INDEX IF NOT EXISTS idx_user_devices_user_id ON auth.user_devices(user_id);
		CREATE INDEX IF NOT EXISTS idx_user_devices_active ON auth.user_devices(user_id, is_active) WHERE is_active = true;
		CREATE INDEX IF NOT EXISTS idx_user_devices_platform ON auth.user_devices(platform);
		CREATE INDEX IF NOT EXISTS idx_user_devices_last_active ON auth.user_devices(last_active_at DESC);

		-- Create function auth.upsert_user_v2
		CREATE OR REPLACE FUNCTION auth.upsert_user_v2(profile jsonb, auto_verify boolean)
		RETURNS uuid AS $func$
		DECLARE
		    v_user_id uuid;
		    v_current_meta jsonb;
		    v_provider text;
		    v_identifier text;
		BEGIN
		    v_provider := profile->>'provider';
		    v_identifier := profile->>'id';

		    -- [A] Eixo Principal: Identidade
		    SELECT u.id INTO v_user_id 
		    FROM auth.identities i
		    JOIN auth.users u ON i.user_id = u.id
		    WHERE i.provider = v_provider AND i.identifier = v_identifier;

		    IF v_user_id IS NULL THEN
		        -- [B] Eixo Secundário: Cross-Link via Email
		        IF profile->>'email' IS NOT NULL THEN
		            SELECT id INTO v_user_id FROM auth.users 
		            WHERE raw_user_meta_data->>'email' = profile->>'email' 
		            LIMIT 1;
		        END IF;

		        -- [C] Criação de Usuário Neutro
		        IF v_user_id IS NULL THEN
		            INSERT INTO auth.users (raw_user_meta_data, created_at, last_sign_in_at, email_confirmed_at) 
		            VALUES (profile, now(), now(), CASE WHEN auto_verify THEN now() ELSE NULL END)
		            RETURNING id INTO v_user_id;
		        END IF;

		        -- [D] Registro da Identidade
		        INSERT INTO auth.identities (user_id, provider, identifier, identity_data, created_at, last_sign_in_at, verified_at) 
		        VALUES (v_user_id, v_provider, v_identifier, profile, now(), now(), CASE WHEN auto_verify THEN now() ELSE NULL END);
		    ELSE
		        -- [E] Rastro de Acesso
		        UPDATE auth.users SET last_sign_in_at = now() WHERE id = v_user_id;
		        UPDATE auth.identities SET last_sign_in_at = now(), identity_data = profile 
		        WHERE provider = v_provider AND identifier = v_identifier;
		    END IF;

		    -- [F] Sincronização de Metadados
		    SELECT raw_user_meta_data INTO v_current_meta FROM auth.users WHERE id = v_user_id;
		    UPDATE auth.users SET raw_user_meta_data = COALESCE(v_current_meta, '{}'::jsonb) || profile 
		    WHERE id = v_user_id;

		    RETURN v_user_id;
		END;
		$func$ LANGUAGE plpgsql SECURITY DEFINER;

		-- Create function auth.refresh_session_v2
		CREATE OR REPLACE FUNCTION auth.refresh_session_v2(p_old_hash text, p_new_hash text, p_ip text, p_ua text)
		RETURNS TABLE (status text, p_user_id uuid, p_user_meta jsonb) AS $func$
		DECLARE
		    v_token record;
		    v_user_meta jsonb;
		BEGIN
		    -- [A] Localização Atômica
		    SELECT id, user_id, revoked, parent_token INTO v_token 
		    FROM auth.refresh_tokens WHERE token_hash = p_old_hash AND expires_at > now();

		    IF NOT FOUND THEN RETURN QUERY SELECT 'invalid_token'::text, NULL::uuid, NULL::jsonb; RETURN; END IF;
		    IF v_token.revoked THEN RETURN QUERY SELECT 'revoked_reuse_detected'::text, NULL::uuid, NULL::jsonb; RETURN; END IF;

		    -- [B] Invalidação do Token Anterior
		    UPDATE auth.refresh_tokens SET revoked = true WHERE id = v_token.id;

		    -- [C] Rotação e Vínculo
		    INSERT INTO auth.refresh_tokens (token_hash, user_id, expires_at, parent_token, ip_address, user_agent) 
		    VALUES (p_new_hash, v_token.user_id, now() + interval '30 days', v_token.id, p_ip, p_ua);

		    -- [D] Recuperação de Perfil
		    SELECT raw_user_meta_data INTO v_user_meta FROM auth.users WHERE id = v_token.user_id;

		    RETURN QUERY SELECT 'success'::text, v_token.user_id, v_user_meta;
		END;
		$func$ LANGUAGE plpgsql SECURITY DEFINER;

		-- Grant permissions
		GRANT USAGE ON SCHEMA auth TO anon, authenticated, service_role, cascata_api_role;
		GRANT ALL ON ALL TABLES IN SCHEMA auth TO service_role, cascata_api_role;
		GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA auth TO authenticated;
		GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA auth TO authenticated, service_role;
		GRANT EXECUTE ON FUNCTION auth.upsert_user_v2(jsonb, boolean) TO service_role, cascata_api_role;
		GRANT EXECUTE ON FUNCTION auth.refresh_session_v2(text, text, text, text) TO service_role, cascata_api_role;

		-- Create auth.uid() function - Supabase/Neon compatible (NULL-safe)
		-- Returns the current user's UUID from JWT claims
		-- Works with RLS policies and any SQL context, including DDL
		CREATE OR REPLACE FUNCTION auth.uid()
		RETURNS uuid LANGUAGE sql STABLE AS $$
		  SELECT coalesce(
		    nullif(current_setting('request.jwt.claim.sub', true), ''),
		    (current_setting('request.jwt.claims', true)::jsonb ->> 'sub')
		  )::uuid
		$$;

		-- Grant execute to all roles
		GRANT EXECUTE ON FUNCTION auth.uid() TO authenticated, anon, service_role, cascata_api_role;
	`

	_, err = conn.Exec(ctx, authSQL)
	if err != nil {
		return fmt.Errorf("failed to create auth schema: %w", err)
	}

	// Ensure fingerprint_hash column exists on existing tables (idempotent fix for legacy projects)
	ensureColumnSQL := `
		DO $$
		BEGIN
			IF EXISTS (SELECT FROM information_schema.tables WHERE table_schema = 'auth' AND table_name = 'refresh_tokens') THEN
				IF NOT EXISTS (SELECT FROM information_schema.columns WHERE table_schema = 'auth' AND table_name = 'refresh_tokens' AND column_name = 'fingerprint_hash') THEN
					ALTER TABLE auth.refresh_tokens ADD COLUMN fingerprint_hash TEXT;
					CREATE INDEX IF NOT EXISTS idx_refresh_tokens_fingerprint ON auth.refresh_tokens(fingerprint_hash);
				END IF;
			END IF;
		END $$;
	`
	_, err = conn.Exec(ctx, ensureColumnSQL)
	if err != nil {
		log.Printf("[DatabaseService] Warning: failed to ensure fingerprint_hash column: %v", err)
		// Don't fail - this is a best-effort fix
	}

	// CRITICAL: Always ensure auth functions exist (idempotent operation)
	// This handles cases where tenant is created but auth functions are missing
	if err := s.EnsureAuthFunctions(ctx, dbName); err != nil {
		log.Printf("[DatabaseService] Failed to ensure auth functions for %s: %v", dbName, err)
		// Don't fail - functions might already exist
	}

	log.Printf("[DatabaseService] Auth schema initialized for database: %s", dbName)
	return nil
}

// EnsureAuthFunctions garante que as funções auth.upsert_user_v2 e auth.refresh_session_v2 existam
// Esta função é idempotente (CREATE OR REPLACE) e deve ser chamada sempre
func (s *DatabaseService) EnsureAuthFunctions(ctx context.Context, dbName string) error {
	connStr := getTenantConnectionString(dbName)
	conn, err := pgx.Connect(ctx, connStr)
	if err != nil {
		return fmt.Errorf("failed to connect to project db: %w", err)
	}
	defer conn.Close(ctx)

	// Acquire PostgreSQL Advisory Lock to prevent concurrent CREATE OR REPLACE
	// This ensures only one instance executes the function creation at a time
	lockKey := fmt.Sprintf("auth_functions_%s", dbName)
	_, err = conn.Exec(ctx, "SELECT pg_advisory_lock(hashtext($1))", lockKey)
	if err != nil {
		return fmt.Errorf("failed to acquire advisory lock: %w", err)
	}
	defer conn.Exec(ctx, "SELECT pg_advisory_unlock(hashtext($1))", lockKey)

	functionsSQL := `
		-- Ensure auth.upsert_user_v2 function exists
		CREATE OR REPLACE FUNCTION auth.upsert_user_v2(profile jsonb, auto_verify boolean)
		RETURNS uuid AS $func$
		DECLARE
		    v_user_id uuid;
		    v_current_meta jsonb;
		    v_provider text;
		    v_identifier text;
		BEGIN
		    v_provider := profile->>'provider';
		    v_identifier := profile->>'id';

		    SELECT u.id INTO v_user_id 
		    FROM auth.identities i
		    JOIN auth.users u ON i.user_id = u.id
		    WHERE i.provider = v_provider AND i.identifier = v_identifier;

		    IF v_user_id IS NULL THEN
		        IF profile->>'email' IS NOT NULL THEN
		            SELECT id INTO v_user_id FROM auth.users 
		            WHERE raw_user_meta_data->>'email' = profile->>'email' 
		            LIMIT 1;
		        END IF;

		        IF v_user_id IS NULL THEN
		            INSERT INTO auth.users (raw_user_meta_data, created_at, last_sign_in_at, email_confirmed_at) 
		            VALUES (profile, now(), now(), CASE WHEN auto_verify THEN now() ELSE NULL END)
		            RETURNING id INTO v_user_id;
		        END IF;

		        INSERT INTO auth.identities (user_id, provider, identifier, identity_data, created_at, last_sign_in_at, verified_at) 
		        VALUES (v_user_id, v_provider, v_identifier, profile, now(), now(), CASE WHEN auto_verify THEN now() ELSE NULL END);
		    ELSE
		        UPDATE auth.users SET last_sign_in_at = now() WHERE id = v_user_id;
		        UPDATE auth.identities SET last_sign_in_at = now(), identity_data = profile 
		        WHERE provider = v_provider AND identifier = v_identifier;
		    END IF;

		    SELECT raw_user_meta_data INTO v_current_meta FROM auth.users WHERE id = v_user_id;
		    UPDATE auth.users SET raw_user_meta_data = COALESCE(v_current_meta, '{}'::jsonb) || profile 
		    WHERE id = v_user_id;

		    RETURN v_user_id;
		END;
		$func$ LANGUAGE plpgsql SECURITY DEFINER;

		-- Ensure auth.refresh_session_v2 function exists
		CREATE OR REPLACE FUNCTION auth.refresh_session_v2(p_old_hash text, p_new_hash text, p_ip text, p_ua text)
		RETURNS TABLE (status text, p_user_id uuid, p_user_meta jsonb) AS $func$
		DECLARE
		    v_token record;
		    v_user_meta jsonb;
		BEGIN
		    SELECT id, user_id, revoked, parent_token INTO v_token 
		    FROM auth.refresh_tokens WHERE token_hash = p_old_hash AND expires_at > now();

		    IF NOT FOUND THEN RETURN QUERY SELECT 'invalid_token'::text, NULL::uuid, NULL::jsonb; RETURN; END IF;
		    IF v_token.revoked THEN RETURN QUERY SELECT 'revoked_reuse_detected'::text, NULL::uuid, NULL::jsonb; RETURN; END IF;

		    UPDATE auth.refresh_tokens SET revoked = true WHERE id = v_token.id;

		    INSERT INTO auth.refresh_tokens (token_hash, user_id, expires_at, parent_token, ip_address, user_agent) 
		    VALUES (p_new_hash, v_token.user_id, now() + interval '30 days', v_token.id, p_ip, p_ua);

		    SELECT raw_user_meta_data INTO v_user_meta FROM auth.users WHERE id = v_token.user_id;

		    RETURN QUERY SELECT 'success'::text, v_token.user_id, v_user_meta;
		END;
		$func$ LANGUAGE plpgsql SECURITY DEFINER;

		-- Grant execute permissions
		GRANT EXECUTE ON FUNCTION auth.upsert_user_v2(jsonb, boolean) TO service_role, cascata_api_role, anon, authenticated;
		GRANT EXECUTE ON FUNCTION auth.refresh_session_v2(text, text, text, text) TO service_role, cascata_api_role, anon, authenticated;

		-- Ensure auth.uid() function exists (Supabase/Neon compatible - NULL-safe)
		CREATE OR REPLACE FUNCTION auth.uid()
		RETURNS uuid LANGUAGE sql STABLE AS $$
		  SELECT coalesce(
		    nullif(current_setting('request.jwt.claim.sub', true), ''),
		    (current_setting('request.jwt.claims', true)::jsonb ->> 'sub')
		  )::uuid
		$$;

		GRANT EXECUTE ON FUNCTION auth.uid() TO authenticated, anon, service_role, cascata_api_role;
	`

	_, err = conn.Exec(ctx, functionsSQL)
	if err != nil {
		return fmt.Errorf("failed to ensure auth functions: %w", err)
	}

	return nil
}

// EnsureFingerprintHash garante que a coluna fingerprint_hash exista na tabela auth.refresh_tokens
// Esta função é idempotente e deve ser chamada sempre que um projeto é acessado
// para garantir retrocompatibilidade com projetos criados antes da adição da coluna
func (s *DatabaseService) EnsureFingerprintHash(ctx context.Context, dbName string) error {
	connStr := getTenantConnectionString(dbName)
	conn, err := pgx.Connect(ctx, connStr)
	if err != nil {
		return fmt.Errorf("failed to connect to project db: %w", err)
	}
	defer conn.Close(ctx)

	// Fast check: verify if column exists
	var columnExists bool
	checkSQL := `
		SELECT EXISTS (
			SELECT FROM information_schema.columns 
			WHERE table_schema = 'auth' 
			AND table_name = 'refresh_tokens' 
			AND column_name = 'fingerprint_hash'
		)
	`
	err = conn.QueryRow(ctx, checkSQL).Scan(&columnExists)
	if err != nil {
		return fmt.Errorf("failed to check column existence: %w", err)
	}

	// If column already exists, nothing to do
	if columnExists {
		return nil
	}

	// Column doesn't exist, add it
	log.Printf("[DatabaseService] Adding fingerprint_hash column to auth.refresh_tokens in %s", dbName)
	
	alterSQL := `
		ALTER TABLE auth.refresh_tokens ADD COLUMN fingerprint_hash TEXT;
		CREATE INDEX IF NOT EXISTS idx_refresh_tokens_fingerprint ON auth.refresh_tokens(fingerprint_hash);
	`
	_, err = conn.Exec(ctx, alterSQL)
	if err != nil {
		return fmt.Errorf("failed to add fingerprint_hash column: %w", err)
	}

	log.Printf("[DatabaseService] fingerprint_hash column added successfully to %s", dbName)
	return nil
}

// hashString gera um hash numérico de uma string para uso no advisory lock
func hashString(s string) int64 {
	var hash int64 = 5381
	for _, c := range s {
		hash = ((hash << 5) + hash) + int64(c)
	}
	if hash < 0 {
		return -hash
	}
	return hash
}

// acquireGlobalSchemaLock adquire um advisory lock global no PostgreSQL
// Isso permite coordenação entre múltiplos containers/processos
func acquireGlobalSchemaLock(ctx context.Context, dbName string) (func(), error) {
	lockID := hashString(dbName)
	
	// Adquire lock no SystemPool (banco system onde todos os containers se conectam)
	_, err := SystemPool.Exec(ctx, "SELECT pg_advisory_lock($1)", lockID)
	if err != nil {
		return nil, fmt.Errorf("failed to acquire global schema lock: %w", err)
	}
	
	return func() {
		// Libera o lock quando a função retorna
		_, _ = SystemPool.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", lockID)
	}, nil
}

// InitTenantSchemas initializes all managed schemas in a tenant database
// This includes: auth, system, extensions, storage
// Uses per-database locking to prevent concurrent initialization (race condition fix)
// Includes caching to skip initialization if already done in this process
func (s *DatabaseService) InitTenantSchemas(ctx context.Context, dbName string) error {
	// FAST PATH: verifica cache local primeiro (sem lock, leitura rápida)
	if isTenantInitialized(dbName) {
		return nil
	}

	// LOCK GLOBAL (PostgreSQL Advisory Lock): previne múltiplos containers inicializando o mesmo banco
	// Isso é crítico porque cada container tem seu próprio tenantInitLocks em memória
	globalUnlock, err := acquireGlobalSchemaLock(ctx, dbName)
	if err != nil {
		return fmt.Errorf("failed to acquire global lock: %w", err)
	}
	defer globalUnlock()

	// DOUBLE-CHECK: verifica cache novamente após adquirir lock global
	// Outra goroutine pode ter inicializado enquanto esperávamos o lock
	if isTenantInitialized(dbName) {
		return nil
	}

	// LOCK LOCAL (in-memory): previne múltiplas goroutines no mesmo processo
	tenantInitLocksMu.Lock()
	lock, exists := tenantInitLocks[dbName]
	if !exists {
		lock = &sync.Mutex{}
		tenantInitLocks[dbName] = lock
	}
	tenantInitLocksMu.Unlock()

	// Adquire lock para este database específico
	lock.Lock()
	defer lock.Unlock()

	// TRIPLE-CHECK: verifica cache uma última vez após adquirir lock local
	if isTenantInitialized(dbName) {
		return nil
	}

	log.Printf("[DatabaseService] Initializing all managed schemas for database: %s", dbName)

	// Step 1: Initialize auth schema
	if err := s.InitAuthSchema(ctx, dbName); err != nil {
		return fmt.Errorf("failed to init auth schema: %w", err)
	}

	// Step 2: Initialize system tables in tenant DB
	if err := s.InitSystemTablesForTenant(ctx, dbName); err != nil {
		return fmt.Errorf("failed to init system tables: %w", err)
	}

	// Step 3: Create extensions and storage schemas
	if err := s.InitExtensionsAndStorageSchemas(ctx, dbName); err != nil {
		return fmt.Errorf("failed to init extensions/storage schemas: %w", err)
	}

	// Step 4: Initialize public schema permissions (CRITICAL for RLS)
	if err := s.InitPublicSchemaPermissions(ctx, dbName); err != nil {
		return fmt.Errorf("failed to init public schema permissions: %w", err)
	}

	// Step 5: Secure any additional schemas (extensions or user-created)
	if err := s.ScanAndSecureNewSchemas(ctx, dbName); err != nil {
		log.Printf("[DatabaseService:Warn] Failed to scan/securing new schemas: %v", err)
		// Don't fail - this is best effort for existing databases
	}

	// Marca como inicializado no cache para evitar re-execução em futuros requests
	markTenantInitialized(dbName)

	log.Printf("[DatabaseService] All managed schemas initialized for database: %s", dbName)
	return nil
}

// InitPublicSchemaPermissions grants permissions on public schema to Cascata roles
// This is CRITICAL for RLS to work - roles need permission to access tables
func (s *DatabaseService) InitPublicSchemaPermissions(ctx context.Context, dbName string) error {
	connStr := getTenantConnectionString(dbName)

	conn, err := pgx.Connect(ctx, connStr)
	if err != nil {
		return fmt.Errorf("failed to connect to tenant db: %w", err)
	}
	defer conn.Close(ctx)

	// Grant permissions on public schema - mirrors TypeScript implementation
	publicPermsSQL := `
		-- Grant schema usage
		GRANT USAGE ON SCHEMA public TO anon, authenticated, service_role, cascata_api_role;
		
		-- Grant table permissions to service_role (full access)
		GRANT ALL ON ALL TABLES IN SCHEMA public TO service_role;
		GRANT ALL ON ALL SEQUENCES IN SCHEMA public TO service_role;
		
		-- Grant table permissions to anon and authenticated (for RLS evaluation)
		GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO anon, authenticated;
		GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO anon, authenticated;
		
		-- Set default privileges for future objects (CRITICAL for new tables)
		ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO anon, authenticated;
		ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT USAGE, SELECT ON SEQUENCES TO anon, authenticated;
	`

	_, err = conn.Exec(ctx, publicPermsSQL)
	if err != nil {
		// Log warning but don't fail - permissions might already be set
		log.Printf("[DatabaseService:Warn] Public schema permissions setup: %v", err)
		// Try to continue - the permissions might already exist
	}

	log.Printf("[DatabaseService] Public schema permissions initialized for database: %s", dbName)
	return nil
}

// ApplySchemaSecurity aplica segurança (GRANT) em qualquer schema
// Usado para schemas criados por extensões ou usuários
func (s *DatabaseService) ApplySchemaSecurity(ctx context.Context, dbName string, schemaName string) error {
	connStr := getTenantConnectionString(dbName)

	conn, err := pgx.Connect(ctx, connStr)
	if err != nil {
		return fmt.Errorf("failed to connect to tenant db: %w", err)
	}
	defer conn.Close(ctx)

	// Sanitize schema name para prevenir SQL injection
	safeSchema := quotePostgresIdentifier(schemaName)

	// Aplicar GRANT no schema
	grantSQL := fmt.Sprintf(`
		GRANT USAGE ON SCHEMA %s TO anon, authenticated, service_role, cascata_api_role;
		GRANT ALL ON ALL TABLES IN SCHEMA %s TO service_role, cascata_api_role;
		GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA %s TO anon, authenticated;
		GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA %s TO anon, authenticated, service_role;
		ALTER DEFAULT PRIVILEGES IN SCHEMA %s GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO anon, authenticated;
		ALTER DEFAULT PRIVILEGES IN SCHEMA %s GRANT USAGE, SELECT ON SEQUENCES TO anon, authenticated, service_role;
	`, safeSchema, safeSchema, safeSchema, safeSchema, safeSchema, safeSchema)

	_, err = conn.Exec(ctx, grantSQL)
	if err != nil {
		log.Printf("[DatabaseService:Warn] Failed to apply security to schema %s: %v", schemaName, err)
		return err
	}

	log.Printf("[DatabaseService] Security applied to schema %s in database %s", schemaName, dbName)
	return nil
}

// ScanAndSecureNewSchemas verifica e aplica segurança em schemas não protegidos
func (s *DatabaseService) ScanAndSecureNewSchemas(ctx context.Context, dbName string) error {
	connStr := getTenantConnectionString(dbName)

	conn, err := pgx.Connect(ctx, connStr)
	if err != nil {
		return fmt.Errorf("failed to connect to tenant db: %w", err)
	}
	defer conn.Close(ctx)

	// Lista de schemas protegidos (já tratados nas funções de init)
	protectedSchemas := map[string]bool{
		"public": true,
		"auth": true,
		"extensions": true,
		"storage": true,
		"system": true, // system é interno, não precisa de GRANT
		"information_schema": true,
		"pg_catalog": true,
		"pg_toast": true,
	}

	// Buscar todos os schemas exceto os protegidos
	rows, err := conn.Query(ctx, `
		SELECT schema_name FROM information_schema.schemata 
		WHERE schema_name NOT LIKE 'pg_%' 
		AND schema_name NOT IN ('information_schema', 'public', 'auth', 'extensions', 'storage', 'system')
	`)
	if err != nil {
		return fmt.Errorf("failed to list schemas: %w", err)
	}
	defer rows.Close()

	var schemasToSecure []string
	for rows.Next() {
		var schema string
		if err := rows.Scan(&schema); err == nil {
			if !protectedSchemas[schema] {
				schemasToSecure = append(schemasToSecure, schema)
			}
		}
	}

	// Aplicar segurança em cada schema encontrado
	for _, schema := range schemasToSecure {
		if err := s.ApplySchemaSecurity(ctx, dbName, schema); err != nil {
			log.Printf("[DatabaseService:Warn] Failed to secure schema %s: %v", schema, err)
		}
	}

	return nil
}

// InitSystemTablesForTenant initializes system tables in a tenant database (not system database)
func (s *DatabaseService) InitSystemTablesForTenant(ctx context.Context, dbName string) error {
	connStr := getTenantConnectionString(dbName)

	conn, err := pgx.Connect(ctx, connStr)
	if err != nil {
		return fmt.Errorf("failed to connect to tenant db: %w", err)
	}
	defer conn.Close(ctx)

	// Create system schema
	_, err = conn.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS system")
	if err != nil {
		return fmt.Errorf("failed to create system schema: %w", err)
	}

	// Create uuid-ossp extension for UUID generation (required for uuid_generate_v4())
	_, err = conn.Exec(ctx, "CREATE EXTENSION IF NOT EXISTS \"uuid-ossp\"")
	if err != nil {
		log.Printf("[DatabaseService:Warn] Failed to create uuid-ossp extension: %v", err)
		// Don't fail - try to continue, gen_random_uuid() might be available
	}

	// Create tenant-specific system tables (per-project isolation)
	tables := []struct {
		name string
		sql  string
	}{
		{
			name: "ui_settings",
			sql: `CREATE TABLE IF NOT EXISTS system.ui_settings (
				table_name TEXT NOT NULL,
				settings JSONB DEFAULT '{}',
				created_at TIMESTAMPTZ DEFAULT now(),
				updated_at TIMESTAMPTZ DEFAULT now(),
				PRIMARY KEY (table_name)
			)`,
		},
		{
			name: "automations",
			sql: `CREATE TABLE IF NOT EXISTS system.automations (
				id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
				name TEXT NOT NULL,
				description TEXT,
				trigger_type TEXT NOT NULL,
				trigger_config JSONB DEFAULT '{}',
				nodes JSONB NOT NULL DEFAULT '[]',
				is_active BOOLEAN DEFAULT true,
				created_at TIMESTAMPTZ DEFAULT now(),
				updated_at TIMESTAMPTZ DEFAULT now()
			)`,
		},
		{
			name: "rate_limits",
			sql: `CREATE TABLE IF NOT EXISTS system.rate_limits (
				id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
				route_pattern TEXT NOT NULL,
				method TEXT NOT NULL DEFAULT '*',
				rate_limit INTEGER DEFAULT 10,
				burst_limit INTEGER DEFAULT 5,
				window_seconds INTEGER DEFAULT 1,
				created_at TIMESTAMPTZ DEFAULT now(),
				updated_at TIMESTAMPTZ DEFAULT now(),
				UNIQUE(route_pattern, method)
			)`,
		},
		{
			name: "api_key_groups",
			sql: `CREATE TABLE IF NOT EXISTS system.api_key_groups (
				id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
				name TEXT NOT NULL,
				rate_limit INTEGER DEFAULT 100,
				burst_limit INTEGER DEFAULT 50,
				window_seconds INTEGER DEFAULT 1,
				scopes JSONB DEFAULT '[]',
				created_at TIMESTAMPTZ DEFAULT now(),
				updated_at TIMESTAMPTZ DEFAULT now()
			)`,
		},
		{
			name: "api_keys",
			sql: `CREATE TABLE IF NOT EXISTS system.api_keys (
				id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
				name TEXT NOT NULL,
				key_hash TEXT NOT NULL,
				lookup_index TEXT NOT NULL,
				prefix TEXT DEFAULT 'sk_live_',
				scopes JSONB DEFAULT '["*"]',
				rate_limit INTEGER,
				burst_limit INTEGER,
				expires_at TIMESTAMPTZ,
				last_used_at TIMESTAMPTZ,
				is_active BOOLEAN DEFAULT true,
				group_id UUID REFERENCES system.api_key_groups(id),
				created_at TIMESTAMPTZ DEFAULT now(),
				updated_at TIMESTAMPTZ DEFAULT now()
			)`,
		},
		{
			name: "project_extensions",
			sql: `CREATE TABLE IF NOT EXISTS system.project_extensions (
				id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
				extension_name TEXT NOT NULL,
				installed_version TEXT,
				installed_at TIMESTAMPTZ DEFAULT now(),
				UNIQUE(extension_name)
			)`,
		},
		{
			name: "project_assets",
			sql: `CREATE TABLE IF NOT EXISTS system.project_assets (
				id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
				project_slug TEXT NOT NULL,
				name TEXT NOT NULL,
				type TEXT NOT NULL DEFAULT 'folder',
				parent_id UUID REFERENCES system.project_assets(id),
				metadata JSONB DEFAULT '{}',
				created_at TIMESTAMPTZ DEFAULT now(),
				updated_at TIMESTAMPTZ DEFAULT now()
			)`,
		},
		{
			name: "native_asset_organization",
			sql: `CREATE TABLE IF NOT EXISTS system.native_asset_organization (
				id SERIAL PRIMARY KEY,
				project_slug TEXT NOT NULL,
				native_id TEXT NOT NULL,
				asset_type TEXT NOT NULL,
				parent_id UUID NULL REFERENCES system.project_assets(id) ON DELETE SET NULL,
				created_at TIMESTAMPTZ DEFAULT NOW(),
				updated_at TIMESTAMPTZ DEFAULT NOW(),
				UNIQUE(project_slug, native_id)
			)`,
		},
		{
			name: "asset_history",
			sql: `CREATE TABLE IF NOT EXISTS system.asset_history (
				id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
				asset_id UUID REFERENCES system.project_assets(id) ON DELETE CASCADE,
				metadata JSONB DEFAULT '{}',
				created_by TEXT,
				created_at TIMESTAMPTZ DEFAULT now()
			)`,
		},
	}

	for _, table := range tables {
		_, err := conn.Exec(ctx, table.sql)
		if err != nil {
			if isDuplicateTableError(err) {
				log.Printf("[DatabaseService] Table system.%s already exists, skipping", table.name)
				continue
			}
			return fmt.Errorf("failed to create table %s: %w", table.name, err)
		}
		log.Printf("[DatabaseService] Tenant table system.%s ensured", table.name)
	}

	// Create indexes
	indexes := []string{
		"CREATE INDEX IF NOT EXISTS idx_automations_active ON system.automations(is_active)",
		"CREATE INDEX IF NOT EXISTS idx_api_keys_lookup ON system.api_keys(lookup_index)",
		"CREATE INDEX IF NOT EXISTS idx_api_keys_active ON system.api_keys(is_active)",
		"CREATE INDEX IF NOT EXISTS idx_tenant_project_assets_slug ON system.project_assets(project_slug)",
		"CREATE INDEX IF NOT EXISTS idx_tenant_project_assets_parent ON system.project_assets(parent_id)",
		"CREATE INDEX IF NOT EXISTS idx_tenant_asset_history_asset ON system.asset_history(asset_id)",
	}

	for _, idxSQL := range indexes {
		_, err := conn.Exec(ctx, idxSQL)
		if err != nil {
			log.Printf("[DatabaseService:Warn] Failed to create index: %v", err)
		}
	}

	// Step 6: Initialize Security Lock Engine (TIER-3 PADLOCK)
	// Injetar o motor de security locks dinâmicos como em TypeScript
	if err := s.InjectSecurityLockEngine(ctx, dbName); err != nil {
		log.Printf("[DatabaseService:Warn] Failed to inject security lock engine: %v", err)
		// Don't fail - locks can be applied later
	}

	return nil
}

// InjectSecurityLockEngine injeta o motor de security locks dinâmicos no banco do tenant
// Replica a funcionalidade do TypeScript DatabaseService.injectSecurityLockEngine()
func (s *DatabaseService) InjectSecurityLockEngine(ctx context.Context, dbName string) error {
	connStr := getTenantConnectionString(dbName)

	conn, err := pgx.Connect(ctx, connStr)
	if err != nil {
		return fmt.Errorf("failed to connect to tenant db for lock engine: %w", err)
	}
	defer conn.Close(ctx)

	// Criar schema system (se ainda não existir)
	_, err = conn.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS system")
	if err != nil {
		return fmt.Errorf("failed to create system schema for locks: %w", err)
	}

	// Criar extensão uuid-ossp se necessário
	_, _ = conn.Exec(ctx, "CREATE EXTENSION IF NOT EXISTS \"uuid-ossp\"")

	// Criar tabela dynamic_security_locks
	lockTableSQL := `
		CREATE TABLE IF NOT EXISTS system.dynamic_security_locks (
			id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
			project_slug TEXT NOT NULL,
			table_name TEXT NOT NULL,
			column_name TEXT NOT NULL,
			lock_type TEXT NOT NULL,
			metadata JSONB DEFAULT '{}'::jsonb,
			created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(project_slug, table_name, column_name)
		)
	`
	_, err = conn.Exec(ctx, lockTableSQL)
	if err != nil {
		return fmt.Errorf("failed to create dynamic_security_locks table: %w", err)
	}

	// Criar índice para lookup rápido
	_, _ = conn.Exec(ctx, `
		CREATE INDEX IF NOT EXISTS idx_dynamic_locks_fast_lookup 
		ON system.dynamic_security_locks (project_slug, table_name)
	`)

	// Criar função enforce_dynamic_locks (trigger function)
	enforceFuncSQL := `
		CREATE OR REPLACE FUNCTION system.enforce_dynamic_locks()
		RETURNS TRIGGER AS $$
		DECLARE
			_project_slug TEXT;
			_is_otp_verified TEXT;
			_request_role TEXT;
			_lock_record RECORD;
			_old_value JSONB;
			_new_value JSONB;
		BEGIN
			_project_slug := current_setting('request.jwt.claim.project_slug', true);
			IF _project_slug IS NULL THEN
				_project_slug := current_setting('app.current_project_slug', true);
			END IF;

			IF _project_slug IS NOT NULL THEN
				_is_otp_verified := current_setting('request.jwt.claim.otp_verified', true);
				_request_role := current_setting('request.jwt.claim.role', true);
				
				IF TG_OP = 'UPDATE' THEN
					_old_value := to_jsonb(OLD);
					_new_value := to_jsonb(NEW);
				ELSIF TG_OP = 'INSERT' THEN
					_new_value := to_jsonb(NEW);
					_old_value := '{}'::jsonb;
				END IF;

				FOR _lock_record IN 
					SELECT column_name, lock_type, metadata
					FROM system.dynamic_security_locks 
					WHERE project_slug = _project_slug AND table_name = TG_TABLE_NAME
				LOOP
					IF TG_OP = 'INSERT' THEN
						IF _lock_record.lock_type = 'service_role_only' AND coalesce(_request_role, 'service_role') IN ('anon', 'authenticated') THEN
							RAISE EXCEPTION USING ERRCODE = 'PDC04', MESSAGE = 'Security Lock Violation: Column "' || _lock_record.column_name || '" requires SERVICE_ROLE system privileges to set during insertion.';
						END IF;
					END IF;

					IF _new_value ? _lock_record.column_name AND (_old_value ->> _lock_record.column_name IS DISTINCT FROM _new_value ->> _lock_record.column_name) THEN
						IF _lock_record.lock_type IN ('insert_only', 'immutable') AND TG_OP = 'UPDATE' THEN
							RAISE EXCEPTION USING ERRCODE = 'PDC02', MESSAGE = 'Security Lock Violation: Column "' || _lock_record.column_name || '" is locked (' || _lock_record.lock_type || ') and cannot be updated.';
						END IF;
						
						IF _lock_record.lock_type = 'service_role_only' AND coalesce(_request_role, 'service_role') IN ('anon', 'authenticated') THEN
							RAISE EXCEPTION USING ERRCODE = 'PDC04', MESSAGE = 'Security Lock Violation: Column "' || _lock_record.column_name || '" requires SERVICE_ROLE system privileges to mutate.';
						END IF;

						IF _lock_record.lock_type IN ('code_protected', 'otp_protected') AND TG_OP = 'UPDATE' THEN
							_is_otp_verified := coalesce(current_setting('request.jwt.claim.otp_verified', true), 'false');
							
							IF _is_otp_verified != 'true' THEN
								DECLARE
									_stepup_providers TEXT := current_setting('request.stepup.verified_providers', true);
									_allowed_providers JSONB := coalesce(_lock_record.metadata->'allowed_factors', '["totp", "otp", "passkey"]'::jsonb);
								BEGIN
									IF _stepup_providers IS NULL OR _stepup_providers = '' OR NOT (_allowed_providers ? _stepup_providers) THEN
										RAISE EXCEPTION USING ERRCODE = 'PDC01', MESSAGE = '{"error": "step_up_required", "message": "Security Lock Violation: Valid Step-Up Authorization Ring is required to mutate column \"' || _lock_record.column_name || '\".", "required_factors": ' || _allowed_providers::text || '}';
									END IF;
								END;
							END IF;
						END IF;
					END IF;
				END LOOP;
			END IF;
			RETURN NEW;
		END;
		$$ LANGUAGE plpgsql SECURITY DEFINER
	`
	_, err = conn.Exec(ctx, enforceFuncSQL)
	if err != nil {
		return fmt.Errorf("failed to create enforce_dynamic_locks function: %w", err)
	}

	// Criar função apply_security_locks (principal função chamada pelo AdminController)
	// Suporta tanto strings simples ("immutable") quanto objetos JSON com metadata
	applyLocksFuncSQL := `
		CREATE OR REPLACE FUNCTION system.apply_security_locks(_project_slug TEXT, _table_name TEXT, _locked_columns JSONB)
		RETURNS VOID AS $$
		DECLARE
			_col_name TEXT;
			_col_value JSONB;
			_lock_type TEXT;
			_metadata JSONB;
			_has_dynamic BOOLEAN := FALSE;
			_table_exists BOOLEAN;
		BEGIN
			-- Check if table exists
			SELECT EXISTS (
				SELECT FROM information_schema.tables 
				WHERE table_schema = 'public' AND table_name = _table_name
			) INTO _table_exists;

			-- Limpar locks anteriores para esta tabela/projeto
			DELETE FROM system.dynamic_security_locks WHERE project_slug = _project_slug AND table_name = _table_name;
			
			-- Inserir novos locks (suporta string simples OU objeto com metadata)
			FOR _col_name, _col_value IN SELECT key, value FROM jsonb_each(_locked_columns)
			LOOP
				IF jsonb_typeof(_col_value) = 'object' AND _col_value ? 'lock_type' THEN
					_lock_type := _col_value->>'lock_type';
					_metadata := _col_value - 'lock_type';
				ELSE
					_lock_type := _col_value #>> '{}';
					_metadata := '{}'::jsonb;
				END IF;

				INSERT INTO system.dynamic_security_locks (project_slug, table_name, column_name, lock_type, metadata)
				VALUES (_project_slug, _table_name, _col_name, _lock_type, _metadata);
				_has_dynamic := TRUE;
			END LOOP;

			-- Executar triggers apenas se a tabela existir
			IF _table_exists THEN
				-- Remover trigger existente se houver
				EXECUTE format('DROP TRIGGER IF EXISTS trg_dynamic_locks_%s ON public.%I', _table_name, _table_name);

				-- Criar trigger se houver locks dinâmicos
				IF _has_dynamic THEN
					EXECUTE format('CREATE TRIGGER trg_dynamic_locks_%s BEFORE INSERT OR UPDATE ON public.%I FOR EACH ROW EXECUTE FUNCTION system.enforce_dynamic_locks()', _table_name, _table_name);
				END IF;
			END IF;
		END;
		$$ LANGUAGE plpgsql SECURITY DEFINER
	`
	_, err = conn.Exec(ctx, applyLocksFuncSQL)
	if err != nil {
		return fmt.Errorf("failed to create apply_security_locks function: %w", err)
	}

	// Criar função apply_auto_clock_triggers (Auto Clock Update Engine)
	applyAutoClockFuncSQL := `
		CREATE OR REPLACE FUNCTION system.apply_auto_clock_triggers(_project_slug TEXT, _table_name TEXT, _auto_clock_columns JSONB)
		RETURNS VOID AS $$
		DECLARE
			_col_name TEXT;
			_col_type TEXT;
			_trigger_name TEXT;
			_func_name TEXT;
			_now_expr TEXT;
			_table_exists BOOLEAN;
		BEGIN
			-- Check if table exists
			SELECT EXISTS (
				SELECT FROM information_schema.tables 
				WHERE table_schema = 'public' AND table_name = _table_name
			) INTO _table_exists;

			IF NOT _table_exists THEN
				RETURN;
			END IF;

			-- Remover triggers auto_clock existentes para esta tabela (limpeza)
			FOR _trigger_name IN 
				SELECT tgname 
				FROM pg_trigger t 
				JOIN pg_class c ON t.tgrelid = c.oid 
				JOIN pg_namespace n ON c.relnamespace = n.oid 
				WHERE n.nspname = 'public' 
				AND c.relname = _table_name 
				AND tgname LIKE 'trg_auto_clock_%'
			LOOP
				EXECUTE format('DROP TRIGGER IF EXISTS %I ON public.%I', _trigger_name, _table_name);
			END LOOP;
			
			-- Remover funções auto_clock órfãs
			FOR _func_name IN 
				SELECT proname 
				FROM pg_proc p 
				JOIN pg_namespace n ON p.pronamespace = n.oid 
				WHERE n.nspname = 'public' 
				AND proname LIKE 'fn_auto_clock_%_' || _table_name
			LOOP
				EXECUTE format('DROP FUNCTION IF EXISTS public.%I()', _func_name);
			END LOOP;
			
			-- Criar novos triggers para cada coluna auto_clock
			FOR _col_name, _col_type IN 
				SELECT key, value->>'type' 
				FROM jsonb_each(_auto_clock_columns)
			LOOP
				-- Determinar expressão NOW() apropriada para o tipo
				IF _col_type LIKE 'timestamp%' THEN
					_now_expr := 'NOW()';
				ELSIF _col_type = 'date' THEN
					_now_expr := 'CURRENT_DATE';
				ELSIF _col_type = 'time' THEN
					_now_expr := 'CURRENT_TIME';
				ELSE
					_now_expr := 'NOW()';
				END IF;
				
				-- Criar função do trigger específica para esta coluna
				_func_name := 'fn_auto_clock_' || _col_name || '_' || _table_name;
				EXECUTE format('
					CREATE OR REPLACE FUNCTION public.%I() 
					RETURNS TRIGGER AS $func$
					BEGIN
						NEW.%I := %s;
						RETURN NEW;
					END;
					$func$ LANGUAGE plpgsql SECURITY DEFINER
				', _func_name, _col_name, _now_expr);
				
				-- Criar trigger BEFORE UPDATE
				_trigger_name := 'trg_auto_clock_' || _col_name || '_' || _table_name;
				EXECUTE format('
					CREATE TRIGGER %I 
					BEFORE UPDATE ON public.%I 
					FOR EACH ROW 
					EXECUTE FUNCTION public.%I()
				', _trigger_name, _table_name, _func_name);
				
				-- Log de auditoria
				INSERT INTO system.security_events (project_slug, event_type, details)
				VALUES (_project_slug, 'AUTO_CLOCK_ENABLED', jsonb_build_object(
					'table', _table_name,
					'column', _col_name,
					'type', _col_type,
					'created_at', NOW()
				));
			END LOOP;
		END;
		$$ LANGUAGE plpgsql SECURITY DEFINER
	`
	_, err = conn.Exec(ctx, applyAutoClockFuncSQL)
	if err != nil {
		return fmt.Errorf("failed to create apply_auto_clock_triggers function: %w", err)
	}

	// Criar função auxiliar is_column_locked
	_, _ = conn.Exec(ctx, `
		CREATE OR REPLACE FUNCTION system.is_column_locked(_project_slug TEXT, _table_name TEXT, _column_name TEXT)
		RETURNS BOOLEAN AS $$
		BEGIN
			RETURN EXISTS (
				SELECT 1 FROM system.dynamic_security_locks 
				WHERE project_slug = _project_slug 
				AND table_name = _table_name 
				AND column_name = _column_name
			);
		END;
		$$ LANGUAGE plpgsql STABLE SECURITY DEFINER
	`)

	// Criar função auxiliar list_security_locks
	_, _ = conn.Exec(ctx, `
		CREATE OR REPLACE FUNCTION system.list_security_locks(_project_slug TEXT)
		RETURNS TABLE(table_name TEXT, column_name TEXT, lock_type TEXT, created_at TIMESTAMP WITH TIME ZONE) AS $$
		BEGIN
			RETURN QUERY
			SELECT dsl.table_name, dsl.column_name, dsl.lock_type, dsl.created_at
			FROM system.dynamic_security_locks dsl
			WHERE dsl.project_slug = _project_slug
			ORDER BY dsl.table_name, dsl.column_name;
		END;
		$$ LANGUAGE plpgsql STABLE SECURITY DEFINER
	`)

	log.Printf("[DatabaseService] Security Lock Engine (TIER-3 PADLOCK) injected for database: %s", dbName)
	return nil
}

// InitExtensionsAndStorageSchemas creates extensions and storage schemas in tenant DB
func (s *DatabaseService) InitExtensionsAndStorageSchemas(ctx context.Context, dbName string) error {
	connStr := getTenantConnectionString(dbName)

	conn, err := pgx.Connect(ctx, connStr)
	if err != nil {
		return fmt.Errorf("failed to connect to tenant db: %w", err)
	}
	defer conn.Close(ctx)

	// Create extensions schema (for PostgreSQL extensions like PostGIS, pgvector)
	_, err = conn.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS extensions")
	if err != nil {
		return fmt.Errorf("failed to create extensions schema: %w", err)
	}

	// Create storage schema (for storage objects)
	_, err = conn.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS storage")
	if err != nil {
		return fmt.Errorf("failed to create storage schema: %w", err)
	}

	// Grant permissions to Cascata roles
	grantSQL := `
		GRANT USAGE ON SCHEMA extensions TO anon, authenticated, service_role, cascata_api_role;
		GRANT USAGE ON SCHEMA storage TO anon, authenticated, service_role, cascata_api_role;
		GRANT ALL ON ALL TABLES IN SCHEMA extensions TO service_role, cascata_api_role;
		GRANT ALL ON ALL TABLES IN SCHEMA storage TO service_role, cascata_api_role;
		GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA extensions TO authenticated;
		GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA storage TO authenticated;
		GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA extensions TO authenticated, service_role;
		GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA storage TO authenticated, service_role;
	`
	_, err = conn.Exec(ctx, grantSQL)
	if err != nil {
		log.Printf("[DatabaseService:Warn] Failed to grant permissions on extensions/storage: %v", err)
		// Don't fail, just log warning
	}

	return nil
}

// InitSystemTables initializes missing system tables if they don't exist
// Idempotent: safe to call multiple times
func (s *DatabaseService) InitSystemTables(ctx context.Context) error {
	// Create system schema first (idempotent)
	_, err := SystemPool.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS system")
	if err != nil {
		return fmt.Errorf("failed to create system schema: %w", err)
	}

	// Create tables one by one with proper IF NOT EXISTS
	tables := []struct {
		name string
		sql  string
	}{
		{
			name: "project_assets",
			sql: `CREATE TABLE IF NOT EXISTS system.project_assets (
				id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
				project_slug TEXT NOT NULL,
				name TEXT NOT NULL,
				type TEXT NOT NULL DEFAULT 'folder',
				parent_id UUID REFERENCES system.project_assets(id),
				metadata JSONB DEFAULT '{}',
				created_at TIMESTAMPTZ DEFAULT now(),
				updated_at TIMESTAMPTZ DEFAULT now()
			)`,
		},
		{
			name: "ui_settings",
			sql: `CREATE TABLE IF NOT EXISTS system.ui_settings (
				project_slug TEXT NOT NULL,
				table_name TEXT NOT NULL,
				settings JSONB DEFAULT '{}',
				created_at TIMESTAMPTZ DEFAULT now(),
				updated_at TIMESTAMPTZ DEFAULT now(),
				PRIMARY KEY (project_slug, table_name)
			)`,
		},
		{
			name: "audit_logs",
			sql: `CREATE TABLE IF NOT EXISTS system.audit_logs (
				id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
				project_slug TEXT NOT NULL,
				method TEXT NOT NULL,
				path TEXT NOT NULL,
				status_code INTEGER NOT NULL,
				client_ip TEXT,
				duration_ms INTEGER,
				user_role TEXT,
				payload JSONB,
				headers JSONB,
				geo_info JSONB,
				response_size BIGINT,
				created_at TIMESTAMPTZ DEFAULT now()
			)`,
		},
		{
			name: "automations",
			sql: `CREATE TABLE IF NOT EXISTS system.automations (
				id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
				project_slug TEXT NOT NULL,
				name TEXT NOT NULL,
				description TEXT,
				trigger_type TEXT NOT NULL,
				trigger_config JSONB DEFAULT '{}',
				nodes JSONB NOT NULL DEFAULT '[]',
				is_active BOOLEAN DEFAULT true,
				created_at TIMESTAMPTZ DEFAULT now(),
				updated_at TIMESTAMPTZ DEFAULT now()
			)`,
		},
		{
			name: "rate_limits",
			sql: `CREATE TABLE IF NOT EXISTS system.rate_limits (
				id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
				project_slug TEXT NOT NULL,
				route_pattern TEXT NOT NULL,
				method TEXT NOT NULL DEFAULT '*',
				rate_limit INTEGER DEFAULT 10,
				burst_limit INTEGER DEFAULT 5,
				rate_limit_anon INTEGER DEFAULT 10,
				burst_limit_anon INTEGER DEFAULT 5,
				rate_limit_auth INTEGER DEFAULT 20,
				burst_limit_auth INTEGER DEFAULT 10,
				window_seconds INTEGER DEFAULT 1,
				message_anon TEXT,
				message_auth TEXT,
				crud_limits JSONB DEFAULT '{}',
				group_limits JSONB DEFAULT '{}',
				created_at TIMESTAMPTZ DEFAULT now(),
				updated_at TIMESTAMPTZ DEFAULT now(),
				UNIQUE(project_slug, route_pattern, method)
			)`,
		},
		{
			name: "api_key_groups",
			sql: `CREATE TABLE IF NOT EXISTS system.api_key_groups (
				id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
				project_slug TEXT NOT NULL,
				name TEXT NOT NULL,
				rate_limit INTEGER DEFAULT 100,
				burst_limit INTEGER DEFAULT 50,
				window_seconds INTEGER DEFAULT 1,
				rejection_message TEXT,
				nerf_config JSONB DEFAULT '{"enabled": false}',
				crud_limits JSONB DEFAULT '{}',
				scopes JSONB DEFAULT '[]',
				created_at TIMESTAMPTZ DEFAULT now(),
				updated_at TIMESTAMPTZ DEFAULT now()
			)`,
		},
		{
			name: "api_keys",
			sql: `CREATE TABLE IF NOT EXISTS system.api_keys (
				id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
				project_slug TEXT NOT NULL,
				name TEXT NOT NULL,
				key_hash TEXT NOT NULL,
				lookup_index TEXT NOT NULL,
				prefix TEXT DEFAULT 'sk_live_',
				scopes JSONB DEFAULT '["*"]',
				rate_limit INTEGER,
				burst_limit INTEGER,
				expires_at TIMESTAMPTZ,
				last_used_at TIMESTAMPTZ,
				is_active BOOLEAN DEFAULT true,
				group_id UUID REFERENCES system.api_key_groups(id),
				created_at TIMESTAMPTZ DEFAULT now(),
				updated_at TIMESTAMPTZ DEFAULT now()
			)`,
		},
		{
			name: "api_logs",
			sql: `CREATE TABLE IF NOT EXISTS system.api_logs (
				id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
				project_slug TEXT NOT NULL,
				api_key_id UUID,
				method TEXT NOT NULL,
				path TEXT NOT NULL,
				status_code INTEGER,
				client_ip TEXT,
				user_agent TEXT,
				duration_ms INTEGER,
				request_size BIGINT,
				response_size BIGINT,
				created_at TIMESTAMPTZ DEFAULT now()
			)`,
		},
		{
			name: "extension_registry",
			sql: `CREATE TABLE IF NOT EXISTS system.extension_registry (
				extension_name TEXT PRIMARY KEY,
				source_image TEXT,
				status TEXT DEFAULT 'available',
				file_size_bytes BIGINT,
				injected_at TIMESTAMPTZ,
				created_at TIMESTAMPTZ DEFAULT now(),
				updated_at TIMESTAMPTZ DEFAULT now()
			)`,
		},
		{
			name: "project_extensions",
			sql: `CREATE TABLE IF NOT EXISTS system.project_extensions (
				id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
				project_slug TEXT NOT NULL,
				extension_name TEXT NOT NULL,
				installed_version TEXT,
				installed_at TIMESTAMPTZ DEFAULT now(),
				UNIQUE(project_slug, extension_name)
			)`,
		},
		{
			name: "assets",
			sql: `CREATE TABLE IF NOT EXISTS system.assets (
				id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
				project_slug TEXT NOT NULL,
				name TEXT NOT NULL,
				type TEXT NOT NULL DEFAULT 'file',
				parent_id UUID REFERENCES system.assets(id),
				metadata JSONB DEFAULT '{}',
				created_at TIMESTAMPTZ DEFAULT now(),
				updated_at TIMESTAMPTZ DEFAULT now()
			)`,
		},
		{
			name: "asset_history",
			sql: `CREATE TABLE IF NOT EXISTS system.asset_history (
				id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
				asset_id UUID REFERENCES system.assets(id) ON DELETE CASCADE,
				project_slug TEXT NOT NULL,
				content TEXT,
				metadata JSONB DEFAULT '{}',
				created_by TEXT,
				created_at TIMESTAMPTZ DEFAULT now()
			)`,
		},
	}

	// Create each table
	for _, table := range tables {
		_, err := SystemPool.Exec(ctx, table.sql)
		if err != nil {
			// Check if error is because table already exists (shouldn't happen with IF NOT EXISTS, but just in case)
			if isDuplicateTableError(err) {
				log.Printf("[DatabaseService] Table system.%s already exists, skipping", table.name)
				continue
			}
			return fmt.Errorf("failed to create table %s: %w", table.name, err)
		}
		log.Printf("[DatabaseService] Table system.%s ensured", table.name)
	}

	// Create indexes separately (idempotent)
	indexes := []string{
		"CREATE INDEX IF NOT EXISTS idx_project_assets_slug ON system.project_assets(project_slug)",
		"CREATE INDEX IF NOT EXISTS idx_project_assets_parent ON system.project_assets(parent_id)",
		"CREATE INDEX IF NOT EXISTS idx_assets_slug ON system.assets(project_slug)",
		"CREATE INDEX IF NOT EXISTS idx_assets_parent ON system.assets(parent_id)",
		"CREATE INDEX IF NOT EXISTS idx_asset_history_asset ON system.asset_history(asset_id)",
		"CREATE INDEX IF NOT EXISTS idx_audit_logs_slug ON system.audit_logs(project_slug)",
		"CREATE INDEX IF NOT EXISTS idx_audit_logs_created ON system.audit_logs(created_at)",
		"CREATE INDEX IF NOT EXISTS idx_automations_slug ON system.automations(project_slug)",
		"CREATE INDEX IF NOT EXISTS idx_rate_limits_slug ON system.rate_limits(project_slug)",
		"CREATE INDEX IF NOT EXISTS idx_api_keys_slug ON system.api_keys(project_slug)",
		"CREATE INDEX IF NOT EXISTS idx_api_keys_lookup ON system.api_keys(lookup_index)",
		"CREATE INDEX IF NOT EXISTS idx_api_logs_slug ON system.api_logs(project_slug)",
		"CREATE INDEX IF NOT EXISTS idx_api_key_groups_slug ON system.api_key_groups(project_slug)",
		"CREATE INDEX IF NOT EXISTS idx_project_extensions_slug ON system.project_extensions(project_slug)",
	}

	for _, idxSQL := range indexes {
		_, err := SystemPool.Exec(ctx, idxSQL)
		if err != nil {
			log.Printf("[DatabaseService:Warn] Failed to create index: %v", err)
			// Don't fail on index creation, just log
		}
	}

	log.Printf("[DatabaseService] System tables initialized successfully")
	return nil
}

// isDuplicateTableError checks if error is about table already existing
// quotePostgresIdentifier safely quotes a PostgreSQL identifier
func quotePostgresIdentifier(name string) string {
	return pgx.Identifier{name}.Sanitize()
}

func isDuplicateTableError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return strings.Contains(errStr, "already exists") || strings.Contains(errStr, "duplicate key")
}
