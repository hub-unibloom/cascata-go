-- ============================================================================
-- NEXUS v0.1.0 hardening
-- Unifica status/is_active e permite gatilhos SELECT para sequestro de leitura.
-- ============================================================================

ALTER TABLE system.nexus_automations
    DROP CONSTRAINT IF EXISTS nexus_automations_event_type_check;

ALTER TABLE system.nexus_automations
    ADD CONSTRAINT nexus_automations_event_type_check
    CHECK (event_type IS NULL OR event_type IN ('SELECT', 'INSERT', 'UPDATE', 'DELETE', 'ANY'));

UPDATE system.nexus_automations
SET is_active = (status = 'active');

CREATE INDEX IF NOT EXISTS idx_nexus_automations_active_status
    ON system.nexus_automations(tenant_id, hook_type, status, is_active)
    WHERE status = 'active' AND is_active = true;
