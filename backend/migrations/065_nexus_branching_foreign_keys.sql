-- Migration 065: Nexus Branching Foreign Keys Fix
-- Corrige as foreign keys do sistema Nexus para suportar a PK composta (id, tenant_id, branch_name)
-- Adiciona branch_name às tabelas filhas e cria foreign keys compostas
-- NOTA: Esta migration deve ser executada APÓS a migration 064 (orphan cleanup)
-- e APÓS a função EnsureNexusAutomationsBranchColumn que atualiza a PK de nexus_automations

-- ============================================================================
-- Verifica se a PK de nexus_automations já está correta
-- ============================================================================

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_index i
        JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = ANY(i.indkey)
        WHERE i.indrelid = 'system.nexus_automations'::regclass
        AND i.indisprimary
        AND a.attname = 'branch_name'
    ) THEN
        RAISE EXCEPTION 'Primary key of nexus_automations does not include branch_name. EnsureNexusAutomationsBranchColumn must run first.';
    END IF;
END $$;

-- ============================================================================
-- nexus_automation_alerts
-- ============================================================================

-- 1. Adiciona coluna branch_name se não existir
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = 'system'
        AND table_name = 'nexus_automation_alerts'
        AND column_name = 'branch_name'
    ) THEN
        ALTER TABLE system.nexus_automation_alerts
        ADD COLUMN branch_name TEXT NOT NULL DEFAULT 'main';
        RAISE NOTICE 'Column branch_name added to system.nexus_automation_alerts';
    END IF;
END $$;

-- 2. Dropa foreign key antiga (se existir)
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.table_constraints
        WHERE constraint_name = 'nexus_automation_alerts_automation_id_fkey'
        AND table_schema = 'system'
    ) THEN
        ALTER TABLE system.nexus_automation_alerts
        DROP CONSTRAINT nexus_automation_alerts_automation_id_fkey;
        RAISE NOTICE 'Dropped old foreign key on nexus_automation_alerts';
    END IF;
END $$;

-- 3. Cria foreign key composta com ON DELETE CASCADE
ALTER TABLE system.nexus_automation_alerts
ADD CONSTRAINT nexus_automation_alerts_automation_id_fkey
FOREIGN KEY (automation_id, tenant_id, branch_name)
REFERENCES system.nexus_automations(id, tenant_id, branch_name)
ON DELETE CASCADE;

-- ============================================================================
-- nexus_execution_log
-- ============================================================================

-- 1. Adiciona coluna branch_name se não existir
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = 'system'
        AND table_name = 'nexus_execution_log'
        AND column_name = 'branch_name'
    ) THEN
        ALTER TABLE system.nexus_execution_log
        ADD COLUMN branch_name TEXT NOT NULL DEFAULT 'main';
        RAISE NOTICE 'Column branch_name added to system.nexus_execution_log';
    END IF;
END $$;

-- 2. Dropa foreign key antiga (se existir)
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.table_constraints
        WHERE constraint_name = 'nexus_execution_log_automation_id_fkey'
        AND table_schema = 'system'
    ) THEN
        ALTER TABLE system.nexus_execution_log
        DROP CONSTRAINT nexus_execution_log_automation_id_fkey;
        RAISE NOTICE 'Dropped old foreign key on nexus_execution_log';
    END IF;
END $$;

-- 3. Cria foreign key composta com ON DELETE CASCADE
ALTER TABLE system.nexus_execution_log
ADD CONSTRAINT nexus_execution_log_automation_id_fkey
FOREIGN KEY (automation_id, tenant_id, branch_name)
REFERENCES system.nexus_automations(id, tenant_id, branch_name)
ON DELETE CASCADE;

-- ============================================================================
-- Comentários explicativos
-- ============================================================================

COMMENT ON COLUMN system.nexus_automation_alerts.branch_name IS 'Branch da automação relacionada (para suporte a branching multitenancy)';
COMMENT ON COLUMN system.nexus_execution_log.branch_name IS 'Branch da automação executada (para suporte a branching multitenancy)';
COMMENT ON CONSTRAINT nexus_automation_alerts_automation_id_fkey ON system.nexus_automation_alerts IS 
'Foreign key composta para suportar PK (id, tenant_id, branch_name) em nexus_automations - garante integridade referencial em ambiente com branching';

COMMENT ON CONSTRAINT nexus_execution_log_automation_id_fkey ON system.nexus_execution_log IS 
'Foreign key composta para suportar PK (id, tenant_id, branch_name) em nexus_automations - garante integridade referencial em ambiente com branching';

-- ============================================================================
-- Índices para performance
-- ============================================================================

CREATE INDEX IF NOT EXISTS idx_nexus_alerts_branch
    ON system.nexus_automation_alerts(branch_name);

CREATE INDEX IF NOT EXISTS idx_nexus_exec_branch
    ON system.nexus_execution_log(branch_name);
