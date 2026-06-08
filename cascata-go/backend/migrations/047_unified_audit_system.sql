
-- Migration 047: Unified Audit System & Admin Audit Trail
-- Purpose: Fix logging inconsistencies and add comprehensive audit trail for compliance

-- ============================================================================
-- PART 1: Ensure system.api_logs exists with correct structure
-- ============================================================================

-- Create api_logs table if it doesn't exist (idempotent)
CREATE TABLE IF NOT EXISTS system.api_logs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_slug TEXT NOT NULL,
    method TEXT,
    path TEXT,
    status_code INTEGER,
    client_ip TEXT,
    duration_ms BIGINT,
    user_role TEXT,
    payload JSONB DEFAULT '{}'::jsonb,
    headers JSONB DEFAULT '{}'::jsonb,
    geo_info JSONB DEFAULT '{}'::jsonb,
    response_size INTEGER DEFAULT 0,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- Ensure all columns exist (for tables created by earlier migrations)
ALTER TABLE system.api_logs ADD COLUMN IF NOT EXISTS method TEXT;
ALTER TABLE system.api_logs ADD COLUMN IF NOT EXISTS path TEXT;
ALTER TABLE system.api_logs ADD COLUMN IF NOT EXISTS status_code INTEGER;
ALTER TABLE system.api_logs ADD COLUMN IF NOT EXISTS client_ip TEXT;
ALTER TABLE system.api_logs ADD COLUMN IF NOT EXISTS duration_ms BIGINT;
ALTER TABLE system.api_logs ADD COLUMN IF NOT EXISTS user_role TEXT;
ALTER TABLE system.api_logs ADD COLUMN IF NOT EXISTS payload JSONB DEFAULT '{}'::jsonb;
ALTER TABLE system.api_logs ADD COLUMN IF NOT EXISTS headers JSONB DEFAULT '{}'::jsonb;
ALTER TABLE system.api_logs ADD COLUMN IF NOT EXISTS geo_info JSONB DEFAULT '{}'::jsonb;
ALTER TABLE system.api_logs ADD COLUMN IF NOT EXISTS response_size INTEGER DEFAULT 0;
ALTER TABLE system.api_logs ADD COLUMN IF NOT EXISTS created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW();

-- Create essential indexes
CREATE INDEX IF NOT EXISTS idx_api_logs_project_slug ON system.api_logs(project_slug);
CREATE INDEX IF NOT EXISTS idx_api_logs_created_at ON system.api_logs(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_api_logs_client_ip ON system.api_logs(client_ip);
CREATE INDEX IF NOT EXISTS idx_api_logs_status_code ON system.api_logs(status_code);
CREATE INDEX IF NOT EXISTS idx_api_logs_response_size ON system.api_logs(response_size DESC);

-- Composite index for common query patterns
CREATE INDEX IF NOT EXISTS idx_api_logs_project_time ON system.api_logs(project_slug, created_at DESC);

-- ============================================================================
-- PART 2: Drop inconsistent audit_logs table if it exists (cleanup)
-- ============================================================================

-- If somehow audit_logs was created but not api_logs, migrate data then drop
DO $$
BEGIN
    IF EXISTS (SELECT FROM information_schema.tables 
               WHERE table_schema = 'system' AND table_name = 'audit_logs')
       AND EXISTS (SELECT FROM information_schema.tables 
                   WHERE table_schema = 'system' AND table_name = 'api_logs')
    THEN
        -- Migrate any data from audit_logs to api_logs
        INSERT INTO system.api_logs (
            id, project_slug, method, path, status_code, client_ip, 
            duration_ms, user_role, payload, headers, geo_info, response_size, created_at
        )
        SELECT 
            COALESCE(id, gen_random_uuid()),
            project_slug,
            method,
            path,
            status_code,
            client_ip,
            duration_ms,
            user_role,
            COALESCE(payload, '{}'::jsonb),
            COALESCE(headers, '{}'::jsonb),
            COALESCE(geo_info, '{}'::jsonb),
            COALESCE(response_size, 0),
            COALESCE(created_at, NOW())
        FROM system.audit_logs
        ON CONFLICT (id) DO NOTHING;
        
        -- Drop the inconsistent table
        DROP TABLE system.audit_logs;
        RAISE NOTICE 'Migrated data from audit_logs to api_logs and dropped inconsistent table';
    ELSIF EXISTS (SELECT FROM information_schema.tables 
                  WHERE table_schema = 'system' AND table_name = 'audit_logs')
    THEN
        -- Just rename if api_logs doesn't exist
        ALTER TABLE system.audit_logs RENAME TO api_logs;
        RAISE NOTICE 'Renamed audit_logs to api_logs for consistency';
    END IF;
END $$;

-- ============================================================================
-- PART 3: Immutability Triggers for api_logs
-- ============================================================================

-- Function to enforce log immutability
CREATE OR REPLACE FUNCTION system.enforce_log_immutability()
RETURNS TRIGGER AS $$
BEGIN
    IF (TG_OP = 'UPDATE') THEN
        RAISE EXCEPTION 'Security Alert: Audit logs are immutable. Updates are not allowed.';
    ELSIF (TG_OP = 'DELETE') THEN
        -- Verifica se a flag de manutenção está ativa na sessão atual
        IF current_setting('cascata.maintenance_mode', true) <> 'true' THEN
            RAISE EXCEPTION 'Security Alert: Audit logs cannot be deleted manually. Use system.purge_old_logs().';
        END IF;
    END IF;
    RETURN OLD;
END;
$$ LANGUAGE plpgsql;

-- Apply trigger
DROP TRIGGER IF EXISTS trg_immutable_logs ON system.api_logs;
CREATE TRIGGER trg_immutable_logs
    BEFORE UPDATE OR DELETE ON system.api_logs
    FOR EACH ROW EXECUTE FUNCTION system.enforce_log_immutability();

-- ============================================================================
-- PART 4: Admin Audit Trail for CLI and Administrative Actions
-- ============================================================================

-- Table for administrative actions (CLI, dashboard, security operations)
CREATE TABLE IF NOT EXISTS system.admin_audit_log (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    
    -- Action categorization
    action_type TEXT NOT NULL, -- 'cli_command', 'panic_toggle', 'security_block_ip', 
                               -- 'security_unblock_ip', 'user_create', 'user_delete',
                               -- 'project_create', 'backup_restore', 'cert_create', etc.
    
    -- Actor information
    actor_type TEXT NOT NULL, -- 'cli', 'user', 'system', 'automation'
    actor_id TEXT, -- user_id for users, 'panic-reset-cli' for CLI, etc.
    actor_ip INET, -- IP address when applicable
    
    -- Target information
    target_type TEXT, -- 'project', 'user', 'ip', 'system', 'certificate'
    target_id TEXT, -- slug, user_id, IP, etc.
    
    -- Action details
    action_description TEXT NOT NULL,
    action_metadata JSONB DEFAULT '{}'::jsonb, -- Flexible storage for action-specific data
    
    -- CLI-specific fields
    cli_command TEXT, -- Full command executed (for CLI actions)
    cli_exit_code INTEGER, -- Exit code (0 = success)
    cli_stdout TEXT, -- Standard output
    cli_stderr TEXT, -- Standard error
    cli_duration_ms INTEGER, -- Execution time
    
    -- Result tracking
    status TEXT NOT NULL DEFAULT 'success', -- 'success', 'failure', 'partial', 'timeout'
    error_message TEXT,
    
    -- Compliance fields
    session_id TEXT, -- For linking related actions
    request_id TEXT, -- For tracing across services
    fingerprint TEXT, -- Device/browser fingerprint when applicable
    
    -- Timestamp (immutable)
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    
    -- Tamper-proof: hash of previous record for chain verification (optional advanced)
    previous_hash TEXT
);

-- Indexes for admin audit log
CREATE INDEX IF NOT EXISTS idx_admin_audit_action_type ON system.admin_audit_log(action_type);
CREATE INDEX IF NOT EXISTS idx_admin_audit_actor ON system.admin_audit_log(actor_type, actor_id);
CREATE INDEX IF NOT EXISTS idx_admin_audit_target ON system.admin_audit_log(target_type, target_id);
CREATE INDEX IF NOT EXISTS idx_admin_audit_created_at ON system.admin_audit_log(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_admin_audit_session ON system.admin_audit_log(session_id);

-- Composite index for common compliance queries
CREATE INDEX IF NOT EXISTS idx_admin_audit_actor_time ON system.admin_audit_log(actor_id, created_at DESC);

-- Immutability trigger for admin_audit_log
DROP TRIGGER IF EXISTS trg_immutable_admin_audit ON system.admin_audit_log;
CREATE TRIGGER trg_immutable_admin_audit
    BEFORE UPDATE OR DELETE ON system.admin_audit_log
    FOR EACH ROW EXECUTE FUNCTION system.enforce_log_immutability();

-- ============================================================================
-- PART 5: Functions for Audit Trail Management
-- ============================================================================

-- Function to log administrative actions (to be called from Go backend)
CREATE OR REPLACE FUNCTION system.log_admin_action(
    p_action_type TEXT,
    p_actor_type TEXT,
    p_actor_id TEXT,
    p_actor_ip INET,
    p_target_type TEXT,
    p_target_id TEXT,
    p_action_description TEXT,
    p_action_metadata JSONB DEFAULT '{}',
    p_cli_command TEXT DEFAULT NULL,
    p_cli_exit_code INTEGER DEFAULT NULL,
    p_status TEXT DEFAULT 'success',
    p_error_message TEXT DEFAULT NULL,
    p_session_id TEXT DEFAULT NULL,
    p_request_id TEXT DEFAULT NULL
) RETURNS UUID AS $$
DECLARE
    v_id UUID;
BEGIN
    INSERT INTO system.admin_audit_log (
        action_type,
        actor_type,
        actor_id,
        actor_ip,
        target_type,
        target_id,
        action_description,
        action_metadata,
        cli_command,
        cli_exit_code,
        status,
        error_message,
        session_id,
        request_id
    ) VALUES (
        p_action_type,
        p_actor_type,
        p_actor_id,
        p_actor_ip,
        p_target_type,
        p_target_id,
        p_action_description,
        p_action_metadata,
        p_cli_command,
        p_cli_exit_code,
        p_status,
        p_error_message,
        p_session_id,
        p_request_id
    )
    RETURNING id INTO v_id;
    
    RETURN v_id;
END;
$$ LANGUAGE plpgsql SECURITY DEFINER;

-- Function to get audit trail for a project (compliance report)
CREATE OR REPLACE FUNCTION system.get_project_audit_trail(
    p_project_slug TEXT,
    p_start_date TIMESTAMP WITH TIME ZONE DEFAULT NOW() - INTERVAL '30 days',
    p_end_date TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    p_limit INTEGER DEFAULT 1000
) RETURNS TABLE (
    id UUID,
    action_type TEXT,
    actor_type TEXT,
    actor_id TEXT,
    action_description TEXT,
    target_type TEXT,
    target_id TEXT,
    status TEXT,
    created_at TIMESTAMP WITH TIME ZONE,
    metadata JSONB
) AS $$
BEGIN
    RETURN QUERY
    SELECT 
        aal.id,
        aal.action_type,
        aal.actor_type,
        aal.actor_id,
        aal.action_description,
        aal.target_type,
        aal.target_id,
        aal.status,
        aal.created_at,
        aal.action_metadata as metadata
    FROM system.admin_audit_log aal
    WHERE aal.target_id = p_project_slug
      AND aal.target_type = 'project'
      AND aal.created_at BETWEEN p_start_date AND p_end_date
    ORDER BY aal.created_at DESC
    LIMIT p_limit;
END;
$$ LANGUAGE plpgsql SECURITY DEFINER;

-- Function to purge old admin logs (GDPR/compliance retention)
CREATE OR REPLACE FUNCTION system.purge_old_admin_logs(p_days INTEGER)
RETURNS INTEGER AS $$
DECLARE
    count INTEGER;
BEGIN
    -- Activate maintenance mode
    PERFORM set_config('cascata.maintenance_mode', 'true', true);
    
    WITH deleted AS (
        DELETE FROM system.admin_audit_log 
        WHERE created_at < NOW() - (p_days || ' days')::INTERVAL
        RETURNING id
    )
    SELECT count(*) INTO count FROM deleted;
    
    RETURN count;
END;
$$ LANGUAGE plpgsql SECURITY DEFINER;

-- Function to search logs (compliance/forensics)
CREATE OR REPLACE FUNCTION system.search_api_logs(
    p_project_slug TEXT,
    p_client_ip TEXT DEFAULT NULL,
    p_status_code INTEGER DEFAULT NULL,
    p_start_date TIMESTAMP WITH TIME ZONE DEFAULT NOW() - INTERVAL '7 days',
    p_end_date TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    p_limit INTEGER DEFAULT 100
) RETURNS TABLE (
    id UUID,
    method TEXT,
    path TEXT,
    status_code INTEGER,
    client_ip TEXT,
    duration_ms BIGINT,
    user_role TEXT,
    created_at TIMESTAMP WITH TIME ZONE
) AS $$
BEGIN
    RETURN QUERY
    SELECT 
        al.id,
        al.method,
        al.path,
        al.status_code,
        al.client_ip,
        al.duration_ms,
        al.user_role,
        al.created_at
    FROM system.api_logs al
    WHERE al.project_slug = p_project_slug
      AND al.created_at BETWEEN p_start_date AND p_end_date
      AND (p_client_ip IS NULL OR al.client_ip = p_client_ip)
      AND (p_status_code IS NULL OR al.status_code = p_status_code)
    ORDER BY al.created_at DESC
    LIMIT p_limit;
END;
$$ LANGUAGE plpgsql SECURITY DEFINER;

-- ============================================================================
-- PART 6: Compliance Views
-- ============================================================================

-- View for security incidents (failed auth, blocked IPs, large responses)
CREATE OR REPLACE VIEW system.security_incidents AS
SELECT 
    id,
    project_slug,
    method,
    path,
    status_code,
    client_ip,
    user_role,
    response_size,
    created_at,
    CASE 
        WHEN status_code IN (401, 403) THEN 'authentication_failure'
        WHEN status_code >= 500 THEN 'server_error'
        WHEN response_size > 10 * 1024 * 1024 THEN 'large_data_transfer'
        ELSE 'other'
    END as incident_type
FROM system.api_logs
WHERE status_code >= 400 
   OR response_size > 10 * 1024 * 1024;

-- View for admin activity summary (dashboard)
CREATE OR REPLACE VIEW system.admin_activity_summary AS
SELECT 
    DATE(created_at) as activity_date,
    action_type,
    actor_type,
    actor_id,
    target_type,
    COUNT(*) as action_count,
    COUNT(CASE WHEN status = 'success' THEN 1 END) as success_count,
    COUNT(CASE WHEN status = 'failure' THEN 1 END) as failure_count
FROM system.admin_audit_log
GROUP BY DATE(created_at), action_type, actor_type, actor_id, target_type
ORDER BY activity_date DESC, action_count DESC;

-- ============================================================================
-- PART 7: Comments and Documentation
-- ============================================================================

COMMENT ON TABLE system.api_logs IS 'Immutable audit trail of all API requests. Used for security monitoring, compliance, and forensics.';
COMMENT ON TABLE system.admin_audit_log IS 'Administrative action audit trail. Tracks CLI commands, security operations, and management actions for compliance (ISO 27001, SOC2).';
COMMENT ON FUNCTION system.log_admin_action IS 'Logs administrative actions for compliance. Should be called for all security-sensitive operations.';
COMMENT ON FUNCTION system.get_project_audit_trail IS 'Returns compliance-ready audit trail for a specific project.';
COMMENT ON VIEW system.security_incidents IS 'Security-relevant events including auth failures and anomalous data transfers.';

