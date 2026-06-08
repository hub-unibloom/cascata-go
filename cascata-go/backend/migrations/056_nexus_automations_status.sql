-- ============================================================================
-- NEXUS ENGINE v0.1.1 — Adição de Status de Ciclo de Vida
-- Adiciona a coluna 'status' para suportar rascunhos e controle de produção.
-- ============================================================================

ALTER TABLE system.nexus_automations 
ADD COLUMN IF NOT EXISTS status TEXT DEFAULT 'draft' CHECK (status IN ('draft', 'active', 'archived'));

-- Atualiza automações existentes para 'active' se estiverem marcadas como is_active
UPDATE system.nexus_automations SET status = 'active' WHERE is_active = true;
