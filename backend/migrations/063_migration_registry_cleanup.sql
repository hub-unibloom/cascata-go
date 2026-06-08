-- Migration 063: Migration Registry Cleanup
-- Remove registros de migrations que falharam ou foram renomeadas
-- Isso permite que as novas versões sejam aplicadas corretamente

-- Remove registros das migrations antigas que foram renomeadas
DELETE FROM system.migrations 
WHERE name IN (
    '064_edge_functions.sql',      -- Renomeada para 066
    '065_nexus_branching_foreign_keys.sql' -- Renomeada para 065 (nova versão)
);

-- Log da limpeza
DO $$
BEGIN
    RAISE NOTICE 'Migration registry cleanup complete - old migration records removed to allow new versions to apply';
END $$;
