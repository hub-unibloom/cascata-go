-- Criar schema auth se não existir
CREATE SCHEMA IF NOT EXISTS auth;

-- Criar tabela auth.users
CREATE TABLE IF NOT EXISTS auth.users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email VARCHAR(255) UNIQUE,
    encrypted_password VARCHAR(255),
    email_confirmed_at TIMESTAMPTZ,
    raw_app_meta_data JSONB DEFAULT '{}'::jsonb,
    raw_user_meta_data JSONB DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ DEFAULT now(),
    updated_at TIMESTAMPTZ DEFAULT now(),
    last_sign_in_at TIMESTAMPTZ,
    phone VARCHAR(15) UNIQUE,
    phone_confirmed_at TIMESTAMPTZ,
    confirmation_token VARCHAR(255),
    email_change_token VARCHAR(255),
    recovery_token VARCHAR(255),
    banned_until TIMESTAMPTZ,
    is_sso_user BOOLEAN DEFAULT false,
    deleted_at TIMESTAMPTZ
);

-- Criar tabela auth.identities
CREATE TABLE IF NOT EXISTS auth.identities (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES auth.users(id) ON DELETE CASCADE,
    provider TEXT NOT NULL,
    identifier TEXT NOT NULL,
    identity_data JSONB DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ DEFAULT now(),
    updated_at TIMESTAMPTZ DEFAULT now(),
    last_sign_in_at TIMESTAMPTZ,
    verified_at TIMESTAMPTZ,
    UNIQUE(provider, identifier)
);

-- Criar indexes
CREATE INDEX IF NOT EXISTS idx_users_email ON auth.users(email);
CREATE INDEX IF NOT EXISTS idx_identities_user_id ON auth.identities(user_id);
CREATE INDEX IF NOT EXISTS idx_identities_provider_identifier ON auth.identities(provider, identifier);

-- Criar função upsert_user_v2
CREATE OR REPLACE FUNCTION auth.upsert_user_v2(profile jsonb, auto_verify boolean)
RETURNS uuid AS $func$
DECLARE
    v_user_id uuid;
    v_current_meta jsonb;
    v_provider text;
    v_identifier text;
BEGIN
    v_provider := profile->>'provider';
    v_identifier := profile->>'id';

    -- [A] Eixo Principal: Identidade
    SELECT u.id INTO v_user_id 
    FROM auth.identities i
    JOIN auth.users u ON i.user_id = u.id
    WHERE i.provider = v_provider AND i.identifier = v_identifier;

    IF v_user_id IS NULL THEN
        -- [B] Eixo Secundário: Cross-Link via Email
        IF profile->>'email' IS NOT NULL THEN
            SELECT id INTO v_user_id FROM auth.users 
            WHERE raw_user_meta_data->>'email' = profile->>'email' 
            LIMIT 1;
        END IF;

        -- [C] Criação de Usuário Neutro
        IF v_user_id IS NULL THEN
            INSERT INTO auth.users (raw_user_meta_data, created_at, last_sign_in_at, email_confirmed_at) 
            VALUES (profile, now(), now(), CASE WHEN auto_verify THEN now() ELSE NULL END)
            RETURNING id INTO v_user_id;
        END IF;

        -- [D] Registro da Identidade
        INSERT INTO auth.identities (user_id, provider, identifier, identity_data, created_at, last_sign_in_at, verified_at) 
        VALUES (v_user_id, v_provider, v_identifier, profile, now(), now(), CASE WHEN auto_verify THEN now() ELSE NULL END);
    ELSE
        -- [E] Rastro de Acesso
        UPDATE auth.users SET last_sign_in_at = now() WHERE id = v_user_id;
        UPDATE auth.identities SET last_sign_in_at = now(), identity_data = profile 
        WHERE provider = v_provider AND identifier = v_identifier;
    END IF;

    -- [F] Sincronização de Metadados
    SELECT raw_user_meta_data INTO v_current_meta FROM auth.users WHERE id = v_user_id;
    UPDATE auth.users SET raw_user_meta_data = COALESCE(v_current_meta, '{}'::jsonb) || profile 
    WHERE id = v_user_id;

    RETURN v_user_id;
END;
$func$ LANGUAGE plpgsql SECURITY DEFINER;

-- Criar função refresh_session_v2 (também usada)
CREATE OR REPLACE FUNCTION auth.refresh_session_v2(p_old_hash text, p_new_hash text, p_ip text, p_ua text)
RETURNS TABLE (status text, p_user_id uuid, p_user_meta jsonb) AS $func$
DECLARE
    v_token record;
    v_user_meta jsonb;
BEGIN
    -- [A] Localização Atômica
    SELECT id, user_id, revoked, parent_token INTO v_token 
    FROM auth.refresh_tokens WHERE token_hash = p_old_hash AND expires_at > now();

    IF NOT FOUND THEN RETURN QUERY SELECT 'invalid_token'::text, NULL::uuid, NULL::jsonb; RETURN; END IF;
    IF v_token.revoked THEN RETURN QUERY SELECT 'revoked_reuse_detected'::text, NULL::uuid, NULL::jsonb; RETURN; END IF;

    -- [B] Invalidação do Token Anterior
    UPDATE auth.refresh_tokens SET revoked = true WHERE id = v_token.id;

    -- [C] Rotação e Vínculo
    INSERT INTO auth.refresh_tokens (token_hash, user_id, expires_at, parent_token, ip_address, user_agent) 
    VALUES (p_new_hash, v_token.user_id, now() + interval '30 days', v_token.id, p_ip, p_ua);

    -- [D] Recuperação de Perfil
    SELECT raw_user_meta_data INTO v_user_meta FROM auth.users WHERE id = v_token.user_id;

    RETURN QUERY SELECT 'success'::text, v_token.user_id, v_user_meta;
END;
$func$ LANGUAGE plpgsql SECURITY DEFINER;
