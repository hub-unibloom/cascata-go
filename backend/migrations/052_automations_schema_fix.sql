-- Migration 052: Correção do schema da tabela automations
-- Phase: Infrastructure Sync
-- Issue: Adiciona colunas faltantes usadas pelo CompiledAutomationService

-- Adicionar colunas de configuração de queue (usadas em compiled_automation_service.go:206)
ALTER TABLE system.automations 
    ADD COLUMN IF NOT EXISTS project_slug TEXT,
    ADD COLUMN IF NOT EXISTS queue_retries INTEGER DEFAULT 3,
    ADD COLUMN IF NOT EXISTS queue_retry_delay INTEGER DEFAULT 1000,
    ADD COLUMN IF NOT EXISTS queue_priority INTEGER DEFAULT 5,
    ADD COLUMN IF NOT EXISTS global_timeout_ms INTEGER DEFAULT 3000;

-- Criar índice para project_slug se não existir
CREATE INDEX IF NOT EXISTS idx_automations_project_slug 
    ON system.automations(project_slug) 
    WHERE project_slug IS NOT NULL;

-- Garantir que project_slug seja preenchido para automações existentes (fallback para '_system_')
UPDATE system.automations 
    SET project_slug = '_system_' 
    WHERE project_slug IS NULL;
