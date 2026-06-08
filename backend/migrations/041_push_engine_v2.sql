
-- Push Engine v2 - Extensões para Templates, Grupos e Bulk Sending
-- Migration: 041_push_engine_v2.sql

-- Tabela de Templates de Notificação (Multi-idioma)
CREATE TABLE IF NOT EXISTS system.notification_templates (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    project_slug TEXT NOT NULL REFERENCES system.projects(slug) ON DELETE CASCADE,
    
    -- Identificação
    code TEXT NOT NULL, -- Código único do template (ex: 'order_created', 'welcome')
    name TEXT NOT NULL, -- Nome amigável para o dashboard
    description TEXT,
    
    -- Conteúdo por idioma (JSONB)
    -- Ex: {"pt": {"title": "Novo Pedido", "body": "Pedido #{{id}} criado"}, "en": {...}}
    content_i18n JSONB NOT NULL DEFAULT '{}',
    
    -- Dados adicionais
    data_payload JSONB DEFAULT '{}', -- Dados invisíveis para o app
    default_language TEXT DEFAULT 'pt',
    
    -- Controle
    active BOOLEAN DEFAULT true,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    
    UNIQUE(project_slug, code)
);

CREATE INDEX IF NOT EXISTS idx_push_templates_project ON system.notification_templates(project_slug, code);
CREATE INDEX IF NOT EXISTS idx_push_templates_active ON system.notification_templates(project_slug, active);

-- Tabela de Grupos de Usuários para Push (Segmentação)
CREATE TABLE IF NOT EXISTS system.notification_groups (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    project_slug TEXT NOT NULL REFERENCES system.projects(slug) ON DELETE CASCADE,
    
    -- Identificação
    name TEXT NOT NULL,
    description TEXT,
    
    -- Filtros dinâmicos (JSONB)
    -- Ex: {"table": "users", "conditions": [{"field": "role", "op": "eq", "value": "premium"}]}
    filter_config JSONB NOT NULL DEFAULT '{}',
    
    -- Controle
    active BOOLEAN DEFAULT true,
    user_count INTEGER DEFAULT 0, -- Cache do número de usuários (atualizado periodicamente)
    last_sync_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_push_groups_project ON system.notification_groups(project_slug);

-- Tabela de Membros dos Grupos (Many-to-Many)
CREATE TABLE IF NOT EXISTS system.notification_group_members (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    group_id UUID NOT NULL REFERENCES system.notification_groups(id) ON DELETE CASCADE,
    user_id UUID NOT NULL,
    project_slug TEXT NOT NULL,
    added_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    
    UNIQUE(group_id, user_id)
);

CREATE INDEX IF NOT EXISTS idx_push_group_members ON system.notification_group_members(group_id, user_id);
CREATE INDEX IF NOT EXISTS idx_push_group_members_user ON system.notification_group_members(project_slug, user_id);

-- Tabela de Campanhas/Bulk Sends
CREATE TABLE IF NOT EXISTS system.notification_campaigns (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    project_slug TEXT NOT NULL REFERENCES system.projects(slug) ON DELETE CASCADE,
    
    -- Configuração
    name TEXT NOT NULL,
    template_id UUID REFERENCES system.notification_templates(id),
    
    -- Target (um ou outro)
    target_type TEXT NOT NULL CHECK (target_type IN ('user', 'group', 'all', 'query')),
    target_user_id UUID, -- Para envio individual
    target_group_id UUID REFERENCES system.notification_groups(id), -- Para envio em grupo
    target_query TEXT, -- Query SQL customizada para segmentação avançada
    
    -- Conteúdo (se não usar template, ou override)
    title_override TEXT,
    body_override TEXT,
    data_override JSONB DEFAULT '{}',
    language TEXT DEFAULT 'pt',
    
    -- Agendamento
    scheduled_at TIMESTAMP WITH TIME ZONE, -- NULL = enviar imediatamente
    sent_at TIMESTAMP WITH TIME ZONE,
    
    -- Estatísticas
    total_recipients INTEGER DEFAULT 0,
    sent_count INTEGER DEFAULT 0,
    failed_count INTEGER DEFAULT 0,
    
    -- Controle
    status TEXT DEFAULT 'pending' CHECK (status IN ('pending', 'scheduled', 'sending', 'completed', 'failed', 'cancelled')),
    created_by UUID, -- User ID que criou
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_push_campaigns_project ON system.notification_campaigns(project_slug, status);
CREATE INDEX IF NOT EXISTS idx_push_campaigns_scheduled ON system.notification_campaigns(status, scheduled_at) WHERE status = 'scheduled';

-- Extensão da tabela de histórico para suportar campanhas
-- Adicionar referência à campanha (se fizer parte de uma)
ALTER TABLE system.notification_history 
ADD COLUMN IF NOT EXISTS campaign_id UUID REFERENCES system.notification_campaigns(id),
ADD COLUMN IF NOT EXISTS template_id UUID REFERENCES system.notification_templates(id),
ADD COLUMN IF NOT EXISTS group_id UUID REFERENCES system.notification_groups(id);

-- Tabela de Configurações de Push por Projeto (Firebase, APNS)
CREATE TABLE IF NOT EXISTS system.push_provider_configs (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    project_slug TEXT NOT NULL REFERENCES system.projects(slug) ON DELETE CASCADE,
    
    -- Provedor
    provider TEXT NOT NULL CHECK (provider IN ('fcm', 'apns', 'onesignal', 'expo')),
    
    -- Configuração criptografada (Firebase JSON, APNS cert, etc)
    config_json JSONB NOT NULL, -- {project_id, client_email, private_key...}
    
    -- Status
    active BOOLEAN DEFAULT true,
    is_default BOOLEAN DEFAULT false, -- Provedor padrão do projeto
    
    -- Metadados
    last_tested_at TIMESTAMP WITH TIME ZONE,
    last_test_status TEXT,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    
    UNIQUE(project_slug, provider)
);

CREATE INDEX IF NOT EXISTS idx_push_configs_project ON system.push_provider_configs(project_slug, active);

-- Tabela de Eventos/Triggers para métricas em tempo real
CREATE TABLE IF NOT EXISTS system.push_events (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    project_slug TEXT NOT NULL,
    
    event_type TEXT NOT NULL, -- 'delivered', 'opened', 'dismissed', 'bounced'
    notification_id UUID REFERENCES system.notification_history(id),
    campaign_id UUID REFERENCES system.notification_campaigns(id),
    
    user_id UUID,
    device_token TEXT,
    platform TEXT, -- 'android', 'ios', 'web'
    
    metadata JSONB DEFAULT '{}', -- Dados extras do evento
    
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_push_events_project ON system.push_events(project_slug, event_type, created_at);
CREATE INDEX IF NOT EXISTS idx_push_events_notification ON system.push_events(notification_id);

-- Função para atualizar timestamps automaticamente
CREATE OR REPLACE FUNCTION update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ language 'plpgsql';

-- Triggers para updated_at
DROP TRIGGER IF EXISTS update_notification_templates_updated_at ON system.notification_templates;
CREATE TRIGGER update_notification_templates_updated_at BEFORE UPDATE ON system.notification_templates
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

DROP TRIGGER IF EXISTS update_notification_groups_updated_at ON system.notification_groups;
CREATE TRIGGER update_notification_groups_updated_at BEFORE UPDATE ON system.notification_groups
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

DROP TRIGGER IF EXISTS update_notification_campaigns_updated_at ON system.notification_campaigns;
CREATE TRIGGER update_notification_campaigns_updated_at BEFORE UPDATE ON system.notification_campaigns
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

DROP TRIGGER IF EXISTS update_push_provider_configs_updated_at ON system.push_provider_configs;
CREATE TRIGGER update_push_provider_configs_updated_at BEFORE UPDATE ON system.push_provider_configs
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
