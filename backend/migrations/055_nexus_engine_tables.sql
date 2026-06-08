-- ============================================================================
-- NEXUS ENGINE v0 — Tabelas de Automação e Alertas
-- Migração para suportar o novo motor de automações baseado em DAG/FBP.
-- ============================================================================

-- Tabela principal: armazena definições de automações Nexus por tenant.
-- Cada automação contém o grafo completo (nós + arestas) em JSON,
-- o modo de execução (fast_lane ou worker_lane) e configurações de hook.
CREATE TABLE IF NOT EXISTS system.nexus_automations (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       TEXT NOT NULL,
    name            TEXT NOT NULL,
    description     TEXT DEFAULT '',
    hook_type       TEXT NOT NULL CHECK (hook_type IN ('PRE_PERSIST', 'POST_PERSIST', 'WEBHOOK', 'CRON', 'MANUAL')),
    route_pattern   TEXT NOT NULL DEFAULT '*',
    method          TEXT NOT NULL DEFAULT 'ANY' CHECK (method IN ('GET', 'POST', 'PATCH', 'PUT', 'DELETE', 'ANY')),
    table_name      TEXT DEFAULT NULL,
    event_type      TEXT DEFAULT NULL CHECK (event_type IS NULL OR event_type IN ('INSERT', 'UPDATE', 'DELETE', 'ANY')),
    is_active       BOOLEAN NOT NULL DEFAULT false,
    version         INTEGER NOT NULL DEFAULT 1,
    graph_json      JSONB NOT NULL DEFAULT '{}',
    timeout_seconds INTEGER NOT NULL DEFAULT 30,
    execution_mode  TEXT NOT NULL DEFAULT 'fast_lane' CHECK (execution_mode IN ('fast_lane', 'worker_lane')),
    max_retries     INTEGER NOT NULL DEFAULT 3,
    retry_delay_ms  INTEGER NOT NULL DEFAULT 1000,
    priority        INTEGER NOT NULL DEFAULT 5 CHECK (priority BETWEEN 1 AND 10),
    tags            TEXT[] DEFAULT '{}',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Índices de performance para lookups frequentes
CREATE INDEX IF NOT EXISTS idx_nexus_automations_tenant
    ON system.nexus_automations(tenant_id);

CREATE INDEX IF NOT EXISTS idx_nexus_automations_tenant_active
    ON system.nexus_automations(tenant_id, is_active)
    WHERE is_active = true;

CREATE INDEX IF NOT EXISTS idx_nexus_automations_hook_lookup
    ON system.nexus_automations(tenant_id, hook_type, is_active)
    WHERE is_active = true;

CREATE INDEX IF NOT EXISTS idx_nexus_automations_table_event
    ON system.nexus_automations(tenant_id, table_name, event_type, is_active)
    WHERE is_active = true;

-- Tabela de alertas: registra eventos de segurança e falhas do Nexus Engine.
CREATE TABLE IF NOT EXISTS system.nexus_automation_alerts (
    id              BIGSERIAL PRIMARY KEY,
    automation_id   UUID REFERENCES system.nexus_automations(id) ON DELETE SET NULL,
    tenant_id       TEXT NOT NULL,
    alert_type      TEXT NOT NULL CHECK (alert_type IN (
        'AUTO_DISABLED',
        'COMPILATION_ERROR',
        'EXECUTION_TIMEOUT',
        'MAX_RETRIES_EXCEEDED',
        'DLQ_THRESHOLD',
        'SECURITY_VIOLATION',
        'CYCLE_DETECTED'
    )),
    message         TEXT NOT NULL,
    metadata        JSONB DEFAULT '{}',
    resolved        BOOLEAN NOT NULL DEFAULT false,
    resolved_at     TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_nexus_alerts_tenant
    ON system.nexus_automation_alerts(tenant_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_nexus_alerts_unresolved
    ON system.nexus_automation_alerts(tenant_id, resolved)
    WHERE resolved = false;

-- Tabela de execuções: registra cada execução do Nexus Engine.
CREATE TABLE IF NOT EXISTS system.nexus_execution_log (
    id              BIGSERIAL PRIMARY KEY,
    trace_id        TEXT NOT NULL,
    automation_id   UUID REFERENCES system.nexus_automations(id) ON DELETE SET NULL,
    tenant_id       TEXT NOT NULL,
    graph_id        TEXT,
    execution_mode  TEXT NOT NULL,
    status          TEXT NOT NULL CHECK (status IN ('success', 'error', 'timeout', 'skipped')),
    duration_ms     BIGINT NOT NULL DEFAULT 0,
    nodes_executed  INTEGER NOT NULL DEFAULT 0,
    response_code   INTEGER,
    error_message   TEXT,
    trigger_data    JSONB DEFAULT '{}',
    response_data   JSONB DEFAULT '{}',
    node_results    JSONB DEFAULT '{}',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_nexus_exec_tenant
    ON system.nexus_execution_log(tenant_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_nexus_exec_automation
    ON system.nexus_execution_log(automation_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_nexus_exec_trace
    ON system.nexus_execution_log(trace_id);

-- Auto-update de updated_at na tabela de automações
CREATE OR REPLACE FUNCTION system.nexus_automations_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_nexus_automations_updated_at ON system.nexus_automations;
CREATE TRIGGER trg_nexus_automations_updated_at
    BEFORE UPDATE ON system.nexus_automations
    FOR EACH ROW EXECUTE FUNCTION system.nexus_automations_updated_at();
