-- Migration 064: Nexus Orphan Data Cleanup
-- Limpa dados órfãos nas tabelas nexus_automation_alerts e nexus_execution_log
-- Remove registros com automation_id que não existem mais em nexus_automations
-- Esta migration deve executar ANTES da 065 (nexus_branching_foreign_keys)

-- ============================================================================
-- Limpeza de nexus_automation_alerts
-- ============================================================================

DO $$
DECLARE
    v_deleted_count INTEGER;
BEGIN
    -- Remove alertas onde automation_id não existe em nexus_automations
    DELETE FROM system.nexus_automation_alerts
    WHERE automation_id IS NOT NULL
    AND NOT EXISTS (
        SELECT 1 FROM system.nexus_automations
        WHERE nexus_automations.id = nexus_automation_alerts.automation_id
    );
    
    GET DIAGNOSTICS v_deleted_count = ROW_COUNT;
    RAISE NOTICE 'Deleted % orphaned records from nexus_automation_alerts', v_deleted_count;
END $$;

-- ============================================================================
-- Limpeza de nexus_execution_log
-- ============================================================================

DO $$
DECLARE
    v_deleted_count INTEGER;
BEGIN
    -- Remove logs de execução onde automation_id não existe em nexus_automations
    DELETE FROM system.nexus_execution_log
    WHERE automation_id IS NOT NULL
    AND NOT EXISTS (
        SELECT 1 FROM system.nexus_automations
        WHERE nexus_automations.id = nexus_execution_log.automation_id
    );
    
    GET DIAGNOSTICS v_deleted_count = ROW_COUNT;
    RAISE NOTICE 'Deleted % orphaned records from nexus_execution_log', v_deleted_count;
END $$;

-- ============================================================================
-- Verificação adicional: Remove registros onde automation_id é NULL
-- ============================================================================

DO $$
DECLARE
    v_deleted_alerts INTEGER;
    v_deleted_logs INTEGER;
BEGIN
    -- Remove alertas com automation_id NULL (já não são úteis sem referência)
    DELETE FROM system.nexus_automation_alerts
    WHERE automation_id IS NULL;
    
    GET DIAGNOSTICS v_deleted_alerts = ROW_COUNT;
    
    -- Remove logs com automation_id NULL (já não são úteis sem referência)
    DELETE FROM system.nexus_execution_log
    WHERE automation_id IS NULL;
    
    GET DIAGNOSTICS v_deleted_logs = ROW_COUNT;
    
    IF v_deleted_alerts > 0 OR v_deleted_logs > 0 THEN
        RAISE NOTICE 'Deleted % alerts with NULL automation_id', v_deleted_alerts;
        RAISE NOTICE 'Deleted % execution logs with NULL automation_id', v_deleted_logs;
    END IF;
END $$;

-- ============================================================================
-- Relatório final
-- ============================================================================

DO $$
DECLARE
    v_alerts_count INTEGER;
    v_logs_count INTEGER;
BEGIN
    SELECT COUNT(*) INTO v_alerts_count FROM system.nexus_automation_alerts;
    SELECT COUNT(*) INTO v_logs_count FROM system.nexus_execution_log;
    
    RAISE NOTICE '=== Nexus Orphan Cleanup Complete ===';
    RAISE NOTICE 'Remaining nexus_automation_alerts: %', v_alerts_count;
    RAISE NOTICE 'Remaining nexus_execution_log: %', v_logs_count;
    RAISE NOTICE 'All orphaned records have been removed. Safe to apply foreign key constraints.';
END $$;
