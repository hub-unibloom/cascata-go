-- Migration 045: Security Locks System Functions
-- Cria a tabela e funções necessárias para o sistema de security locks
-- Isso garante que apply_security_locks exista no banco de sistema

-- Criar schema system se não existir
CREATE SCHEMA IF NOT EXISTS system;

-- Tabela de security locks dinâmicos
CREATE TABLE IF NOT EXISTS system.dynamic_security_locks (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    project_slug TEXT NOT NULL,
    table_name TEXT NOT NULL,
    column_name TEXT NOT NULL,
    lock_type TEXT NOT NULL,
    metadata JSONB DEFAULT '{}'::jsonb,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(project_slug, table_name, column_name)
);

-- Índice para lookup rápido
CREATE INDEX IF NOT EXISTS idx_dynamic_locks_fast_lookup ON system.dynamic_security_locks (project_slug, table_name);

-- Função para enforce de locks dinâmicos (usada em triggers)
CREATE OR REPLACE FUNCTION system.enforce_dynamic_locks()
RETURNS TRIGGER AS $$
DECLARE
    _project_slug TEXT;
    _is_otp_verified TEXT;
    _request_role TEXT;
    _lock_record RECORD;
    _old_value JSONB;
    _new_value JSONB;
BEGIN
    _project_slug := current_setting('request.jwt.claim.project_slug', true);
    IF _project_slug IS NULL THEN
        _project_slug := current_setting('app.current_project_slug', true);
    END IF;

    IF _project_slug IS NOT NULL THEN
        _is_otp_verified := current_setting('request.jwt.claim.otp_verified', true);
        _request_role := current_setting('request.jwt.claim.role', true);
        
        IF TG_OP = 'UPDATE' THEN
            _old_value := to_jsonb(OLD);
            _new_value := to_jsonb(NEW);
        ELSIF TG_OP = 'INSERT' THEN
            _new_value := to_jsonb(NEW);
            _old_value := '{}'::jsonb;
        END IF;

        FOR _lock_record IN 
            SELECT column_name, lock_type, metadata
            FROM system.dynamic_security_locks 
            WHERE project_slug = _project_slug AND table_name = TG_TABLE_NAME
        LOOP
            -- Immutability on INSERT: permitir INSERT mas bloquear UPDATES
            IF TG_OP = 'INSERT' THEN
                IF _lock_record.lock_type = 'service_role_only' AND coalesce(_request_role, 'service_role') IN ('anon', 'authenticated') THEN
                    RAISE EXCEPTION USING ERRCODE = 'PDC04', MESSAGE = 'Security Lock Violation: Column "' || _lock_record.column_name || '" requires SERVICE_ROLE system privileges to set during insertion.';
                END IF;
            END IF;

            -- Mutation Interception (Value effectively changed)
            IF _new_value ? _lock_record.column_name AND (_old_value ->> _lock_record.column_name IS DISTINCT FROM _new_value ->> _lock_record.column_name) THEN
                
                -- 'insert_only' and 'immutable' bloqueiam UPDATES
                IF _lock_record.lock_type IN ('insert_only', 'immutable') AND TG_OP = 'UPDATE' THEN
                    RAISE EXCEPTION USING ERRCODE = 'PDC02', MESSAGE = 'Security Lock Violation: Column "' || _lock_record.column_name || '" is locked (' || _lock_record.lock_type || ') and cannot be updated.';
                END IF;
                
                IF _lock_record.lock_type = 'service_role_only' AND coalesce(_request_role, 'service_role') IN ('anon', 'authenticated') THEN
                    RAISE EXCEPTION USING ERRCODE = 'PDC04', MESSAGE = 'Security Lock Violation: Column "' || _lock_record.column_name || '" requires SERVICE_ROLE system privileges to mutate.';
                END IF;

                IF _lock_record.lock_type IN ('code_protected', 'otp_protected') AND TG_OP = 'UPDATE' THEN
                    -- Universal Padlock: Accept either the old otp_verified claim OR the new stepup_verified_providers context
                    _is_otp_verified := coalesce(current_setting('request.jwt.claim.otp_verified', true), 'false');
                    
                    IF _is_otp_verified != 'true' THEN
                        DECLARE
                            _stepup_providers TEXT := current_setting('request.stepup.verified_providers', true);
                            _allowed_providers JSONB := coalesce(
                                _lock_record.metadata->'allowed_factors', 
                                _lock_record.metadata->'methods', 
                                '["totp", "otp", "passkey"]'::jsonb
                            );
                        BEGIN
                            -- If stepup providers are present, check if any matches the allowed factors
                            IF _stepup_providers IS NULL OR _stepup_providers = '' OR NOT (_allowed_providers ? _stepup_providers) THEN
                                RAISE EXCEPTION USING ERRCODE = 'PDC01', MESSAGE = '{"error": "step_up_required", "message": "Security Lock Violation: Valid Step-Up Authorization Ring is required to mutate column \"' || _lock_record.column_name || '\".", "required_factors": ' || _allowed_providers::text || '}';
                            END IF;
                        END;
                    END IF;
                END IF;
            END IF;
        END LOOP;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql SECURITY DEFINER;

-- Função principal apply_security_locks (chamada pelo AdminController)
-- Suporta tanto strings simples ("immutable") quanto objetos JSON com metadata
-- Ex: {"saldo": {"lock_type": "code_protected", "allowed_factors": ["totp", "passkey"]}}
CREATE OR REPLACE FUNCTION system.apply_security_locks(_project_slug TEXT, _table_name TEXT, _locked_columns JSONB)
RETURNS VOID AS $$
DECLARE
    _col_name TEXT;
    _col_value JSONB;
    _lock_type TEXT;
    _metadata JSONB;
    _has_dynamic BOOLEAN := FALSE;
BEGIN
    -- Remover trigger existente se houver
    EXECUTE format('DROP TRIGGER IF EXISTS trg_dynamic_locks_%s ON public.%I', _table_name, _table_name);
    
    -- Limpar locks anteriores para esta tabela/projeto
    DELETE FROM system.dynamic_security_locks WHERE project_slug = _project_slug AND table_name = _table_name;
    
    -- Inserir novos locks (suporta string simples OU objeto com metadata)
    FOR _col_name, _col_value IN SELECT key, value FROM jsonb_each(_locked_columns)
    LOOP
        -- Detectar se o valor é um objeto com lock_type (novo formato) ou uma string simples (formato legado)
        IF jsonb_typeof(_col_value) = 'object' THEN
            IF _col_value ? 'lock_type' THEN
                _lock_type := _col_value->>'lock_type';
            ELSIF _col_value ? 'lockLevel' THEN
                _lock_type := _col_value->>'lockLevel';
            ELSE
                _lock_type := _col_value #>> '{}';
            END IF;
            
            _metadata := _col_value - 'lock_type' - 'lockLevel'; -- Remove both to clean metadata
            
            -- Normalizar methods para allowed_factors se existir e allowed_factors não existir
            IF (_metadata ? 'methods') AND NOT (_metadata ? 'allowed_factors') THEN
                -- Se for string (ex: "totp,passkey"), converteremos em array simplificado ou apenas deixamos
                -- O trigger _enforce lida com fallback, mas podemos renomear a chave para padronização.
                -- Como não podemos fazer split simples em pgsql jsonb sem complexidade, o trigger resolve isso.
                _metadata := _metadata;
            END IF;
        ELSE
            -- Formato legado: string simples como "immutable"
            _lock_type := _col_value #>> '{}';
            _metadata := '{}'::jsonb;
        END IF;

        INSERT INTO system.dynamic_security_locks (project_slug, table_name, column_name, lock_type, metadata)
        VALUES (_project_slug, _table_name, _col_name, _lock_type, _metadata);
        _has_dynamic := TRUE;
    END LOOP;

    -- Criar trigger se houver locks dinâmicos
    IF _has_dynamic THEN
        EXECUTE format('CREATE TRIGGER trg_dynamic_locks_%s BEFORE INSERT OR UPDATE ON public.%I FOR EACH ROW EXECUTE FUNCTION system.enforce_dynamic_locks()', _table_name, _table_name);
    END IF;
END;
$$ LANGUAGE plpgsql SECURITY DEFINER;

-- Função auxiliar para verificar se uma coluna está lockada
CREATE OR REPLACE FUNCTION system.is_column_locked(_project_slug TEXT, _table_name TEXT, _column_name TEXT)
RETURNS BOOLEAN AS $$
BEGIN
    RETURN EXISTS (
        SELECT 1 FROM system.dynamic_security_locks 
        WHERE project_slug = _project_slug 
        AND table_name = _table_name 
        AND column_name = _column_name
    );
END;
$$ LANGUAGE plpgsql STABLE SECURITY DEFINER;

-- Função para listar locks de um projeto
CREATE OR REPLACE FUNCTION system.list_security_locks(_project_slug TEXT)
RETURNS TABLE(table_name TEXT, column_name TEXT, lock_type TEXT, created_at TIMESTAMP WITH TIME ZONE) AS $$
BEGIN
    RETURN QUERY
    SELECT dsl.table_name, dsl.column_name, dsl.lock_type, dsl.created_at
    FROM system.dynamic_security_locks dsl
    WHERE dsl.project_slug = _project_slug
    ORDER BY dsl.table_name, dsl.column_name;
END;
$$ LANGUAGE plpgsql STABLE SECURITY DEFINER;
