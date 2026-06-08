-- Migration 046: System Certificates Table
-- Cria a tabela system.certificates para gerenciamento de certificados SSL/TLS
-- Necessária para o cert-controller funcionar corretamente

-- Criar schema system se não existir
CREATE SCHEMA IF NOT EXISTS system;

-- Tabela de certificados SSL/TLS
CREATE TABLE IF NOT EXISTS system.certificates (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    domain TEXT NOT NULL,
    provider TEXT NOT NULL DEFAULT 'letsencrypt',
    email TEXT,
    cert_path TEXT,
    key_path TEXT,
    chain_path TEXT,
    ssl_certificate_source TEXT,
    status TEXT NOT NULL DEFAULT 'pending',
    error_message TEXT,
    expires_at TIMESTAMP WITH TIME ZONE,
    last_renewed_at TIMESTAMP WITH TIME ZONE,
    auto_renew BOOLEAN DEFAULT true,
    metadata JSONB DEFAULT '{}'::jsonb,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(domain)
);

-- Índices para busca rápida
CREATE INDEX IF NOT EXISTS idx_certificates_domain ON system.certificates(domain);
CREATE INDEX IF NOT EXISTS idx_certificates_status ON system.certificates(status);
CREATE INDEX IF NOT EXISTS idx_certificates_expires ON system.certificates(expires_at);
CREATE INDEX IF NOT EXISTS idx_certificates_auto_renew ON system.certificates(auto_renew) WHERE auto_renew = true;

-- Tabela de tarefas de certificados (queue para cert-controller)
CREATE TABLE IF NOT EXISTS system.certificate_tasks (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    domain TEXT NOT NULL,
    type TEXT NOT NULL, -- 'issue', 'renew', 'delete', 'reload'
    provider TEXT NOT NULL DEFAULT 'letsencrypt',
    email TEXT,
    status TEXT NOT NULL DEFAULT 'pending',
    error_message TEXT,
    result_message TEXT,
    attempts INTEGER DEFAULT 0,
    max_attempts INTEGER DEFAULT 3,
    metadata JSONB DEFAULT '{}'::jsonb,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    completed_at TIMESTAMP WITH TIME ZONE,
    started_at TIMESTAMP WITH TIME ZONE
);

-- Índices para a tabela de tarefas
CREATE INDEX IF NOT EXISTS idx_cert_tasks_status ON system.certificate_tasks(status);
CREATE INDEX IF NOT EXISTS idx_cert_tasks_domain ON system.certificate_tasks(domain);
CREATE INDEX IF NOT EXISTS idx_cert_tasks_created ON system.certificate_tasks(created_at);
CREATE INDEX IF NOT EXISTS idx_cert_tasks_pending ON system.certificate_tasks(status, created_at) WHERE status = 'pending';

-- Função para atualizar o timestamp de updated_at automaticamente
CREATE OR REPLACE FUNCTION system.update_certificate_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = CURRENT_TIMESTAMP;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- Trigger para atualizar updated_at em certificates
DROP TRIGGER IF EXISTS trg_certificates_updated_at ON system.certificates;
CREATE TRIGGER trg_certificates_updated_at
    BEFORE UPDATE ON system.certificates
    FOR EACH ROW
    EXECUTE FUNCTION system.update_certificate_updated_at();

-- Trigger para atualizar updated_at em certificate_tasks
DROP TRIGGER IF EXISTS trg_cert_tasks_updated_at ON system.certificate_tasks;
CREATE TRIGGER trg_cert_tasks_updated_at
    BEFORE UPDATE ON system.certificate_tasks
    FOR EACH ROW
    EXECUTE FUNCTION system.update_certificate_updated_at();

-- Função para buscar certificados expirando (para renovação automática)
CREATE OR REPLACE FUNCTION system.get_expiring_certificates(days_threshold INTEGER DEFAULT 30)
RETURNS TABLE(id UUID, domain TEXT, expires_at TIMESTAMP WITH TIME ZONE, days_until_expiry INTEGER) AS $$
BEGIN
    RETURN QUERY
    SELECT 
        c.id,
        c.domain,
        c.expires_at,
        EXTRACT(DAY FROM (c.expires_at - CURRENT_TIMESTAMP))::INTEGER as days_until_expiry
    FROM system.certificates c
    WHERE c.auto_renew = true
    AND c.status = 'active'
    AND (c.expires_at - CURRENT_TIMESTAMP) < INTERVAL '1 day' * days_threshold
    ORDER BY c.expires_at ASC;
END;
$$ LANGUAGE plpgsql STABLE SECURITY DEFINER;

-- Função para criar uma tarefa de renovação automaticamente
CREATE OR REPLACE FUNCTION system.create_renewal_task(expiry_days INTEGER DEFAULT 30)
RETURNS INTEGER AS $$
DECLARE
    task_count INTEGER := 0;
    cert RECORD;
BEGIN
    FOR cert IN SELECT * FROM system.get_expiring_certificates(expiry_days)
    LOOP
        -- Verificar se já existe uma tarefa pendente para este domínio
        IF NOT EXISTS (
            SELECT 1 FROM system.certificate_tasks 
            WHERE domain = cert.domain 
            AND type = 'renew' 
            AND status = 'pending'
        ) THEN
            INSERT INTO system.certificate_tasks (domain, type, provider, status)
            SELECT cert.domain, 'renew', c.provider, 'pending'
            FROM system.certificates c
            WHERE c.domain = cert.domain;
            
            task_count := task_count + 1;
        END IF;
    END LOOP;
    
    RETURN task_count;
END;
$$ LANGUAGE plpgsql SECURITY DEFINER;

-- View para resumo de certificados
CREATE OR REPLACE VIEW system.certificates_summary AS
SELECT 
    c.domain,
    c.status,
    c.provider,
    c.expires_at,
    c.auto_renew,
    CASE 
        WHEN c.expires_at IS NULL THEN 'unknown'
        WHEN c.expires_at < CURRENT_TIMESTAMP THEN 'expired'
        WHEN (c.expires_at - CURRENT_TIMESTAMP) < INTERVAL '7 days' THEN 'critical'
        WHEN (c.expires_at - CURRENT_TIMESTAMP) < INTERVAL '30 days' THEN 'warning'
        ELSE 'healthy'
    END as health_status,
    (SELECT COUNT(*) FROM system.certificate_tasks t WHERE t.domain = c.domain AND t.status = 'pending') as pending_tasks
FROM system.certificates c;
