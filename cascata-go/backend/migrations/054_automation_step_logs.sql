-- Migration: Automation Step Logs (n8n-style execution tracking)
-- Created: 2026-01-25

-- Tabela de logs detalhados de execução de automações
CREATE TABLE IF NOT EXISTS system.automation_step_logs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    execution_id TEXT NOT NULL,
    automation_id TEXT NOT NULL,
    project_slug TEXT NOT NULL,
    step_id TEXT NOT NULL,
    node_id TEXT,
    node_type TEXT,
    node_name TEXT,
    level TEXT DEFAULT 'info', -- debug, info, warn, error
    message TEXT,
    input_data JSONB,
    output_data JSONB,
    error_details TEXT,
    duration_ms BIGINT,
    metadata JSONB,
    created_at TIMESTAMPTZ DEFAULT NOW()
);

-- Índices para queries eficientes
CREATE INDEX IF NOT EXISTS idx_automation_step_logs_execution 
    ON system.automation_step_logs(execution_id, created_at);

CREATE INDEX IF NOT EXISTS idx_automation_step_logs_automation 
    ON system.automation_step_logs(automation_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_automation_step_logs_project 
    ON system.automation_step_logs(project_slug, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_automation_step_logs_node 
    ON system.automation_step_logs(execution_id, node_id);

-- Particionamento opcional por data (para alta volumetria)
-- CREATE INDEX IF NOT EXISTS idx_automation_step_logs_created 
--     ON system.automation_step_logs(created_at DESC);

-- Política de retenção: manter logs por 30 dias (configurável)
-- Para implementar, usar pg_cron ou job externo:
-- DELETE FROM system.automation_step_logs WHERE created_at < NOW() - INTERVAL '30 days';

COMMENT ON TABLE system.automation_step_logs IS 'Logs detalhados de execução de automações (estilo n8n)';
COMMENT ON COLUMN system.automation_step_logs.execution_id IS 'ID único da execução do fluxo';
COMMENT ON COLUMN system.automation_step_logs.step_id IS 'ID da etapa dentro da execução';
COMMENT ON COLUMN system.automation_step_logs.level IS 'debug|info|warn|error';
