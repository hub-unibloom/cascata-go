-- Migration 061: Fix Branching Constraints (Identity Decoupling)
-- Remove restrições de integridade que exigiam que o Owner estivesse na auth.users local.
-- Isso permite que o Orquestrador (Owner) gerencie branches sem precisar existir na tabela de usuários de cada tenant.

DO $$ 
BEGIN 
    -- 1. Limpeza na tabela de branches (Principal ponto de falha)
    IF EXISTS (SELECT 1 FROM information_schema.table_constraints WHERE constraint_name = 'branches_created_by_fkey' AND table_schema = 'system') THEN
        ALTER TABLE system.branches DROP CONSTRAINT branches_created_by_fkey;
        RAISE NOTICE 'Constraint branches_created_by_fkey removed successfully.';
    END IF;

    -- 2. Limpeza na tabela de deploys
    IF EXISTS (SELECT 1 FROM information_schema.table_constraints WHERE constraint_name = 'branch_deploys_triggered_by_fkey' AND table_schema = 'system') THEN
        ALTER TABLE system.branch_deploys DROP CONSTRAINT branch_deploys_triggered_by_fkey;
        RAISE NOTICE 'Constraint branch_deploys_triggered_by_fkey removed successfully.';
    END IF;

    -- 3. Limpeza na tabela de data_branches
    IF EXISTS (SELECT 1 FROM information_schema.table_constraints WHERE constraint_name = 'data_branches_created_by_fkey' AND table_schema = 'system') THEN
        ALTER TABLE system.data_branches DROP CONSTRAINT data_branches_created_by_fkey;
        RAISE NOTICE 'Constraint data_branches_created_by_fkey removed successfully.';
    END IF;
END $$;
