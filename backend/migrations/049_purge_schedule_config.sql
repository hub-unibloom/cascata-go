-- Migration 049: Configurable Log Purge Schedule per Project
-- Usa o timezone existente em metadata->>'timezone'

-- =============================================================================
-- 1. ADD PURGE SCHEDULE COLUMNS TO PROJECTS TABLE
-- =============================================================================

ALTER TABLE system.projects 
ADD COLUMN IF NOT EXISTS purge_cron_expression TEXT DEFAULT '0 4 * * *',
ADD COLUMN IF NOT EXISTS purge_enabled BOOLEAN DEFAULT TRUE;

COMMENT ON COLUMN system.projects.purge_cron_expression IS 'Cron expression for log purge (e.g., 0 4 * * * for 4 AM daily). Timezone is taken from metadata->>timezone';
COMMENT ON COLUMN system.projects.purge_enabled IS 'Whether automatic log purge is enabled';

-- =============================================================================
-- 2. FUNCTION TO GET ALL ACTIVE PURGE SCHEDULES (for Go backend scheduler)
-- =============================================================================

CREATE OR REPLACE FUNCTION system.get_active_purge_schedules()
RETURNS TABLE (
    project_slug TEXT,
    project_id UUID,
    cron_expression TEXT,
    timezone TEXT,
    retention_days INTEGER,
    archive_enabled BOOLEAN
) AS $$
BEGIN
    RETURN QUERY
    SELECT 
        p.slug,
        p.id,
        p.purge_cron_expression,
        COALESCE(p.metadata->>'timezone', 'UTC') as timezone,
        COALESCE(p.log_retention_days, 30),
        COALESCE(p.archive_logs, false)
    FROM system.projects p
    WHERE p.purge_enabled = TRUE
    AND p.purge_cron_expression IS NOT NULL
    ORDER BY p.slug;
END;
$$ LANGUAGE plpgsql STABLE SECURITY DEFINER;

-- =============================================================================
-- 4. UPDATE EXISTING PROJECTS WITH DEFAULT VALUES
-- =============================================================================

UPDATE system.projects 
SET purge_cron_expression = '0 4 * * *',
    purge_enabled = TRUE
WHERE purge_cron_expression IS NULL;

-- =============================================================================
-- 5. INDEX FOR PERFORMANCE
-- =============================================================================

CREATE INDEX IF NOT EXISTS idx_projects_purge_enabled ON system.projects(purge_enabled) WHERE purge_enabled = TRUE;

-- =============================================================================
-- 6. AUDIT LOG TRIGGER FOR CONFIGURATION CHANGES
-- =============================================================================

CREATE OR REPLACE FUNCTION system.audit_purge_config_change()
RETURNS TRIGGER AS $$
DECLARE
    old_tz TEXT;
    new_tz TEXT;
BEGIN
    IF OLD.purge_cron_expression IS DISTINCT FROM NEW.purge_cron_expression
       OR OLD.purge_enabled IS DISTINCT FROM NEW.purge_enabled THEN
        
        -- Get timezone from metadata
        old_tz := COALESCE(OLD.metadata->>'timezone', 'UTC');
        new_tz := COALESCE(NEW.metadata->>'timezone', 'UTC');
        
        INSERT INTO system.admin_audit_log (
            project_id, user_id, action_type, action_description, 
            ip_address, user_agent, metadata
        ) VALUES (
            NEW.id,
            COALESCE(current_setting('cascata.current_user_id', true), 'system'),
            'UPDATE_PURGE_SCHEDULE',
            format('Purge schedule updated: cron=%s, tz=%s, enabled=%s',
                   NEW.purge_cron_expression, new_tz, NEW.purge_enabled),
            COALESCE(current_setting('cascata.client_ip', true), '127.0.0.1'),
            'database',
            jsonb_build_object(
                'old_cron', OLD.purge_cron_expression,
                'new_cron', NEW.purge_cron_expression,
                'old_tz', old_tz,
                'new_tz', new_tz,
                'old_enabled', OLD.purge_enabled,
                'new_enabled', NEW.purge_enabled
            )
        );
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql SECURITY DEFINER;

-- Drop existing trigger if any
DROP TRIGGER IF EXISTS trg_audit_purge_config ON system.projects;

-- Create trigger
CREATE TRIGGER trg_audit_purge_config
AFTER UPDATE ON system.projects
FOR EACH ROW
EXECUTE FUNCTION system.audit_purge_config_change();
