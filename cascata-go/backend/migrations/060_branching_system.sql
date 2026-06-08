-- Migration 060: Branching System
-- Sistema de Branching Privacy-First por Design
-- Implementa branches de ambiente e branches de dados

-- Tabela principal de branches
-- Force removal of legacy foreign key constraint if it exists (Zero Regression fix)
DO $$ 
BEGIN 
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'branches' AND table_schema = 'system') THEN
        IF EXISTS (SELECT 1 FROM information_schema.table_constraints WHERE constraint_name = 'branches_created_by_fkey' AND table_schema = 'system') THEN
            ALTER TABLE system.branches DROP CONSTRAINT branches_created_by_fkey;
        END IF;
    END IF;
END $$;

CREATE TABLE IF NOT EXISTS system.branches (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_slug VARCHAR(255) NOT NULL,
    
    -- Identificação da branch
    name VARCHAR(255) NOT NULL, -- ex: "feat/novo-checkout", "main"
    branch_type VARCHAR(50) NOT NULL CHECK (branch_type IN ('environment', 'data')),
    
    -- Estado da branch
    status VARCHAR(50) NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'merged', 'deleted', 'expired')),
    
    -- Metadados
    parent_branch VARCHAR(255), -- branch de origem (para branches de ambiente)
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_by UUID, -- ID do usuário que criou
    
    -- Configurações específicas
    is_main BOOLEAN NOT NULL DEFAULT FALSE, -- branch "main" é imutável
    data_branch_db_name VARCHAR(255), -- nome do banco para branches de dados
    data_branch_ttl_hours INTEGER DEFAULT 168, -- 7 dias padrão
    expires_at TIMESTAMPTZ, -- quando a branch expira (para branches de dados)
    
    -- Conteúdo da branch (para branches de ambiente)
    migrations JSONB, -- array de migrations: [{"version": "001", "sql": "..."}]
    functions_sql TEXT,
    triggers_sql TEXT,
    rls_sql TEXT,
    automations_json JSONB,
    auth_config_json JSONB,
    
    -- Checksum para integridade
    checksum VARCHAR(64), -- SHA-256 do conteúdo
    
    -- Constraints
    UNIQUE(project_slug, name),
    FOREIGN KEY (project_slug) REFERENCES system.projects(slug) ON DELETE CASCADE
);

-- Índices para performance
CREATE INDEX idx_branches_project_slug ON system.branches(project_slug);
CREATE INDEX idx_branches_status ON system.branches(status);
CREATE INDEX idx_branches_expires_at ON system.branches(expires_at) WHERE expires_at IS NOT NULL;
CREATE INDEX idx_branches_type ON system.branches(branch_type);

-- Tabela de histórico de deploys
-- Force removal of legacy foreign key constraint if it exists (Zero Regression fix)
DO $$ 
BEGIN 
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'branch_deploys' AND table_schema = 'system') THEN
        IF EXISTS (SELECT 1 FROM information_schema.table_constraints WHERE constraint_name = 'branch_deploys_triggered_by_fkey' AND table_schema = 'system') THEN
            ALTER TABLE system.branch_deploys DROP CONSTRAINT branch_deploys_triggered_by_fkey;
        END IF;
    END IF;
END $$;

CREATE TABLE IF NOT EXISTS system.branch_deploys (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    branch_id UUID NOT NULL,
    
    -- Informações do deploy
    source_branch VARCHAR(255) NOT NULL,
    target_branch VARCHAR(255) NOT NULL,
    status VARCHAR(50) NOT NULL CHECK (status IN ('pending', 'running', 'success', 'failed', 'rolled_back')),
    
    -- Resultado do diff
    diff_result JSONB, -- resultado completo do diff engine
    sql_statements TEXT[], -- SQL statements gerados
    
    -- Timing
    started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMPTZ,
    duration_ms INTEGER,
    
    -- Metadados
    triggered_by UUID,
    error_message TEXT,
    snapshot_name VARCHAR(255), -- nome do snapshot de segurança se usado
    
    FOREIGN KEY (branch_id) REFERENCES system.branches(id) ON DELETE CASCADE
);

-- Índices para performance
CREATE INDEX idx_branch_deploys_branch_id ON system.branch_deploys(branch_id);
CREATE INDEX idx_branch_deploys_status ON system.branch_deploys(status);
CREATE INDEX idx_branch_deploys_started_at ON system.branch_deploys(started_at DESC);

-- Tabela de branches de dados (para tracking separado)
-- Force removal of legacy foreign key constraint if it exists (Zero Regression fix)
DO $$ 
BEGIN 
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'data_branches' AND table_schema = 'system') THEN
        IF EXISTS (SELECT 1 FROM information_schema.table_constraints WHERE constraint_name = 'data_branches_created_by_fkey' AND table_schema = 'system') THEN
            ALTER TABLE system.data_branches DROP CONSTRAINT data_branches_created_by_fkey;
        END IF;
    END IF;
END $$;

CREATE TABLE IF NOT EXISTS system.data_branches (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_slug VARCHAR(255) NOT NULL,
    branch_name VARCHAR(255) NOT NULL,
    
    -- Configuração do banco de dados
    db_name VARCHAR(255) NOT NULL UNIQUE, -- ex: cascata_minha-app_feat-checkout_data
    template_db VARCHAR(255) NOT NULL, -- banco usado como template
    
    -- Estado
    status VARCHAR(50) NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'expired', 'deleted')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL,
    
    -- Metadados
    created_by UUID,
    size_bytes BIGINT DEFAULT 0,
    row_count_estimate INTEGER DEFAULT 0,
    
    FOREIGN KEY (project_slug) REFERENCES system.projects(slug) ON DELETE CASCADE
);

-- Índices para performance
CREATE INDEX idx_data_branches_project_slug ON system.data_branches(project_slug);
CREATE INDEX idx_data_branches_status ON system.data_branches(status);
CREATE INDEX idx_data_branches_expires_at ON system.data_branches(expires_at);

-- Trigger para atualizar updated_at
CREATE OR REPLACE FUNCTION system.update_branch_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trigger_update_branch_updated_at
    BEFORE UPDATE ON system.branches
    FOR EACH ROW
    EXECUTE FUNCTION system.update_branch_updated_at();

-- Função para criar a branch "main" automaticamente
CREATE OR REPLACE FUNCTION system.ensure_main_branch(p_project_slug VARCHAR(255))
RETURNS UUID AS $$
DECLARE
    v_branch_id UUID;
BEGIN
    -- Tenta inserir a branch main se não existir
    INSERT INTO system.branches (
        project_slug,
        name,
        branch_type,
        status,
        is_main,
        parent_branch,
        checksum
    ) VALUES (
        p_project_slug,
        'main',
        'environment',
        'active',
        TRUE,
        NULL,
        'main-initial'
    )
    ON CONFLICT (project_slug, name) DO NOTHING
    RETURNING id INTO v_branch_id;
    
    -- Se já existia, busca o ID
    IF v_branch_id IS NULL THEN
        SELECT id INTO v_branch_id
        FROM system.branches
        WHERE project_slug = p_project_slug AND name = 'main';
    END IF;
    
    RETURN v_branch_id;
END;
$$ LANGUAGE plpgsql;

-- Função para limpar branches de dados expiradas
CREATE OR REPLACE FUNCTION system.cleanup_expired_data_branches()
RETURNS INTEGER AS $$
DECLARE
    v_deleted_count INTEGER;
BEGIN
    -- Marca branches expiradas como deleted
    UPDATE system.data_branches
    SET status = 'deleted'
    WHERE status = 'active'
      AND expires_at < NOW();
    
    GET DIAGNOSTICS v_deleted_count = ROW_COUNT;
    
    -- Log da operação
    RAISE NOTICE 'Cleaned up % expired data branches', v_deleted_count;
    
    RETURN v_deleted_count;
END;
$$ LANGUAGE plpgsql;

-- Grant permissions
GRANT SELECT, INSERT, UPDATE, DELETE ON system.branches TO authenticated;
GRANT SELECT ON system.branches TO anon;

GRANT SELECT, INSERT, UPDATE, DELETE ON system.branch_deploys TO authenticated;
GRANT SELECT ON system.branch_deploys TO anon;

GRANT SELECT, INSERT, UPDATE, DELETE ON system.data_branches TO authenticated;
GRANT SELECT ON system.data_branches TO anon;

GRANT EXECUTE ON FUNCTION system.ensure_main_branch(VARCHAR) TO authenticated;
GRANT EXECUTE ON FUNCTION system.cleanup_expired_data_branches() TO authenticated;

-- Comentários
COMMENT ON TABLE system.branches IS 'Branches de ambiente e dados para o sistema de branching Cascata';
COMMENT ON TABLE system.branch_deploys IS 'Histórico de operações de deploy de branches';
COMMENT ON TABLE system.data_branches IS 'Branches de dados (bancos temporários para testes)';
COMMENT ON COLUMN system.branches.branch_type IS 'Tipo de branch: environment (schema/estrutura) ou data (banco completo)';
COMMENT ON COLUMN system.branches.is_main IS 'Branch main é imutável e sempre existe';
COMMENT ON COLUMN system.branches.data_branch_ttl_hours IS 'TTL em horas para branches de dados (padrão: 168 = 7 dias)';
COMMENT ON COLUMN system.data_branches.db_name IS 'Nome do banco PostgreSQL criado para a branch de dados';
