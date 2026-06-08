-- Migration 053: Sistema de Status DRAFT/ACTIVE para Automações
-- Phase: Feature - Orchestration Safety
-- Issue: Previne conflitos de múltiplos workflows ativos no mesmo gatilho

-- 1. Adicionar coluna status (draft, active, paused, archived)
ALTER TABLE system.automations 
    ADD COLUMN IF NOT EXISTS status TEXT DEFAULT 'draft';

-- 2. Atualizar automações existentes para active (backward compatibility)
UPDATE system.automations 
    SET status = 'active' 
    WHERE status IS NULL OR status = '';

-- 3. Criar índice único parcial: apenas 1 ACTIVE por slot (project, trigger_type, table, event)
-- Isso garante no nível do banco que não teremos conflitos
DROP INDEX IF EXISTS idx_automations_unique_active;
CREATE UNIQUE INDEX idx_automations_unique_active 
ON system.automations(project_slug, trigger_type, COALESCE(trigger_config->>'table', ''), COALESCE(trigger_config->>'event', ''))
WHERE status = 'active';

-- 4. Índice para busca rápida por status
CREATE INDEX IF NOT EXISTS idx_automations_status 
ON system.automations(project_slug, status) 
WHERE status IN ('active', 'draft');

-- 5. Trigger para prevenir conflitos de filtros (lógica de overlap será validada na aplicação)
-- Nota: A validação de filtros mutuamente exclusivos é feita no backend antes do UPDATE

COMMENT ON COLUMN system.automations.status IS 'draft=salvando, active=executando, paused=pausado, archived=arquivado';
