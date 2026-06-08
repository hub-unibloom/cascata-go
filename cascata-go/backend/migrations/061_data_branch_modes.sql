-- Migration 061: Data Branch Modes
-- Adiciona suporte a modos de operação de branches de dados:
--   copy        → Clone 100% via CREATE DATABASE TEMPLATE (comportamento existente, default)
--   reflective  → Foreign tables via postgres_fdw, leitura do Live sem cópia
--   schema_only → Banco vazio com apenas o schema DDL aplicado

-- Coluna data_mode na tabela principal de branches
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns 
        WHERE table_schema = 'system' AND table_name = 'branches' AND column_name = 'data_mode'
    ) THEN
        ALTER TABLE system.branches 
        ADD COLUMN data_mode VARCHAR(50) DEFAULT 'copy' 
        CHECK (data_mode IN ('copy', 'reflective', 'schema_only'));
    END IF;
END $$;

-- Coluna data_mode na tabela de tracking de data_branches
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns 
        WHERE table_schema = 'system' AND table_name = 'data_branches' AND column_name = 'data_mode'
    ) THEN
        ALTER TABLE system.data_branches 
        ADD COLUMN data_mode VARCHAR(50) DEFAULT 'copy'
        CHECK (data_mode IN ('copy', 'reflective', 'schema_only'));
    END IF;
END $$;

COMMENT ON COLUMN system.branches.data_mode IS 'Modo de operação: copy (clone 100%), reflective (FDW), schema_only (vazio)';
COMMENT ON COLUMN system.data_branches.data_mode IS 'Modo de operação usado na criação desta data branch';
