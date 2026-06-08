-- Migration 062: Branch Materialization (Thin Clone On-Demand)
-- Adiciona suporte para materialização efêmera de branches de ambiente.
-- O banco da branch só é criado no momento do "Access" (on-demand),
-- e tem TTL configurável com cleanup automático.

-- 1. Adiciona coluna para nome do banco materializado
-- NULL = branch não materializada (apenas metadados)
-- Valor = nome do banco PostgreSQL ativo
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = 'system'
        AND table_name = 'branches'
        AND column_name = 'materialized_db'
    ) THEN
        ALTER TABLE system.branches
        ADD COLUMN materialized_db VARCHAR(255) DEFAULT NULL;
        RAISE NOTICE 'Column materialized_db added to system.branches';
    END IF;
END $$;

-- 2. Adiciona coluna de último acesso (para TTL de desmaterialização)
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = 'system'
        AND table_name = 'branches'
        AND column_name = 'last_accessed_at'
    ) THEN
        ALTER TABLE system.branches
        ADD COLUMN last_accessed_at TIMESTAMPTZ DEFAULT NULL;
        RAISE NOTICE 'Column last_accessed_at added to system.branches';
    END IF;
END $$;

-- 3. Adiciona coluna de TTL configurável por branch (em horas)
-- Default: 24 horas. O owner pode configurar por branch.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = 'system'
        AND table_name = 'branches'
        AND column_name = 'materialization_ttl_hours'
    ) THEN
        ALTER TABLE system.branches
        ADD COLUMN materialization_ttl_hours INTEGER DEFAULT 24;
        RAISE NOTICE 'Column materialization_ttl_hours added to system.branches';
    END IF;
END $$;

-- 4. Índice para o job de cleanup: encontrar branches materializadas expiradas
CREATE INDEX IF NOT EXISTS idx_branches_materialized_ttl
    ON system.branches(last_accessed_at)
    WHERE materialized_db IS NOT NULL;

-- 5. Comentários
COMMENT ON COLUMN system.branches.materialized_db IS 'Nome do banco PostgreSQL efêmero criado via thin clone. NULL = não materializado.';
COMMENT ON COLUMN system.branches.last_accessed_at IS 'Último acesso ao banco materializado. Usado para TTL de cleanup.';
COMMENT ON COLUMN system.branches.materialization_ttl_hours IS 'TTL em horas para o banco materializado. Default: 24h.';
