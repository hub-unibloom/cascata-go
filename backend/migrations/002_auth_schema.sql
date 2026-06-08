-- Migration 001b: Auth Schema Initialization (Sovereign Mode)
-- Initializes the modern, provider-agnostic auth schema for the system database
-- exactly as defined for tenant databases in database.go, ensuring parity and zero regression.

-- Create auth schema
CREATE SCHEMA IF NOT EXISTS auth;

-- Create user_concatenation enum in public and auth schemas
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_type t JOIN pg_namespace n ON n.oid = t.typnamespace WHERE t.typname = 'user_concatenation' AND n.nspname = 'public') THEN
        CREATE TYPE public.user_concatenation AS ENUM ('vazio');
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_type t JOIN pg_namespace n ON n.oid = t.typnamespace WHERE t.typname = 'user_concatenation' AND n.nspname = 'auth') THEN
        CREATE TYPE auth.user_concatenation AS ENUM ('vazio');
    END IF;
END $$;

-- Create roles if they don't exist
DO $$
BEGIN
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'anon') THEN
        CREATE ROLE anon NOLOGIN;
    END IF;
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'authenticated') THEN
        CREATE ROLE authenticated NOLOGIN;
    END IF;
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'service_role') THEN
        CREATE ROLE service_role NOLOGIN;
    END IF;
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'cascata_api_role') THEN
        CREATE ROLE cascata_api_role NOLOGIN;
    END IF;
END $$;

-- Create auth.users table
CREATE TABLE IF NOT EXISTS auth.users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    last_sign_in_at TIMESTAMP WITH TIME ZONE,
    raw_user_meta_data JSONB DEFAULT '{}',
    banned BOOLEAN DEFAULT FALSE,
    email_confirmed_at TIMESTAMP WITH TIME ZONE,
    user_concatenation public.user_concatenation[] DEFAULT '{vazio}'
);

-- Create auth.identities table
CREATE TABLE IF NOT EXISTS auth.identities (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES auth.users(id) ON DELETE CASCADE,
    provider TEXT NOT NULL,
    identifier TEXT NOT NULL,
    password_hash TEXT,
    identity_data JSONB DEFAULT '{}',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    last_sign_in_at TIMESTAMP WITH TIME ZONE,
    verified_at TIMESTAMP WITH TIME ZONE,
    created_via_origin TEXT,
    UNIQUE(provider, identifier)
);

-- Create auth.refresh_tokens table
CREATE TABLE IF NOT EXISTS auth.refresh_tokens (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    token_hash TEXT NOT NULL,
    user_id UUID NOT NULL REFERENCES auth.users(id) ON DELETE CASCADE,
    revoked BOOLEAN DEFAULT FALSE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
    parent_token UUID,
    ip_address TEXT,
    user_agent TEXT,
    fingerprint_hash TEXT
);

-- Create auth.audit_log table
CREATE TABLE IF NOT EXISTS auth.audit_log (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID REFERENCES auth.users(id) ON DELETE SET NULL,
    event TEXT NOT NULL,
    provider TEXT,
    identifier TEXT,
    origin TEXT,
    ip_address TEXT,
    status TEXT,
    policy_name TEXT,
    metadata JSONB DEFAULT '{}',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- Create auth.otp_codes table
CREATE TABLE IF NOT EXISTS auth.otp_codes (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    provider TEXT NOT NULL,
    identifier TEXT NOT NULL,
    code_hash TEXT NOT NULL,
    expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
    attempts INTEGER DEFAULT 0,
    metadata JSONB DEFAULT '{}',
    ip_address TEXT,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- Create auth.user_devices table (for push notifications)
CREATE TABLE IF NOT EXISTS auth.user_devices (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL,
    token TEXT NOT NULL,
    platform TEXT NOT NULL CHECK (platform IN ('android', 'ios', 'web', 'desktop')),
    app_version TEXT,
    device_model TEXT,
    os_version TEXT,
    is_active BOOLEAN DEFAULT true,
    last_active_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    metadata JSONB DEFAULT '{}',
    UNIQUE(token)
);

-- Create indexes
CREATE INDEX IF NOT EXISTS idx_identities_user ON auth.identities(user_id);
CREATE INDEX IF NOT EXISTS idx_identities_provider_identifier ON auth.identities(provider, identifier);
CREATE INDEX IF NOT EXISTS idx_refresh_tokens_user ON auth.refresh_tokens(user_id);
CREATE INDEX IF NOT EXISTS idx_refresh_tokens_hash ON auth.refresh_tokens(token_hash);
CREATE INDEX IF NOT EXISTS idx_refresh_tokens_fingerprint ON auth.refresh_tokens(fingerprint_hash);
CREATE INDEX IF NOT EXISTS idx_otp_codes_expires ON auth.otp_codes(expires_at);
CREATE INDEX IF NOT EXISTS idx_audit_log_user ON auth.audit_log(user_id);
CREATE INDEX IF NOT EXISTS idx_audit_log_created ON auth.audit_log(created_at);
CREATE INDEX IF NOT EXISTS idx_user_devices_user_id ON auth.user_devices(user_id);
CREATE INDEX IF NOT EXISTS idx_user_devices_active ON auth.user_devices(user_id, is_active) WHERE is_active = true;
CREATE INDEX IF NOT EXISTS idx_user_devices_platform ON auth.user_devices(platform);
CREATE INDEX IF NOT EXISTS idx_user_devices_last_active ON auth.user_devices(last_active_at DESC);

-- Grant permissions
GRANT USAGE ON SCHEMA auth TO anon, authenticated, service_role, cascata_api_role;
GRANT ALL ON ALL TABLES IN SCHEMA auth TO service_role, cascata_api_role;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA auth TO authenticated;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA auth TO authenticated, service_role;

