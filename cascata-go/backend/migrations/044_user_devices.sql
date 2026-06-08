-- Migration: 044_user_devices.sql
-- Cria tabela de dispositivos para push notifications no schema auth
-- Esta tabela estava faltando e causava erro 500 no endpoint /push/devices

-- Schema auth deve existir (criado pelo 001_init.sql), mas garantimos
CREATE SCHEMA IF NOT EXISTS auth;

-- Tabela de dispositivos registrados para push notifications
CREATE TABLE IF NOT EXISTS auth.user_devices (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id UUID NOT NULL,
    token TEXT NOT NULL, -- FCM token ou APNS token
    platform TEXT NOT NULL CHECK (platform IN ('android', 'ios', 'web', 'desktop')),
    app_version TEXT, -- Versão do app
    device_model TEXT, -- Modelo do dispositivo
    os_version TEXT, -- Versão do OS
    is_active BOOLEAN DEFAULT true,
    last_active_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    
    -- Metadata adicional
    metadata JSONB DEFAULT '{}',
    
    -- Um usuário pode ter múltiplos dispositivos, mas o token deve ser único
    UNIQUE(token)
);

-- Índices para performance
CREATE INDEX IF NOT EXISTS idx_user_devices_user_id ON auth.user_devices(user_id);
CREATE INDEX IF NOT EXISTS idx_user_devices_active ON auth.user_devices(user_id, is_active) WHERE is_active = true;
CREATE INDEX IF NOT EXISTS idx_user_devices_platform ON auth.user_devices(platform);
CREATE INDEX IF NOT EXISTS idx_user_devices_last_active ON auth.user_devices(last_active_at DESC);

-- Função para atualizar updated_at automaticamente
CREATE OR REPLACE FUNCTION auth.update_user_devices_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- Trigger para updated_at
DROP TRIGGER IF EXISTS update_user_devices_updated_at ON auth.user_devices;
CREATE TRIGGER update_user_devices_updated_at
    BEFORE UPDATE ON auth.user_devices
    FOR EACH ROW
    EXECUTE FUNCTION auth.update_user_devices_updated_at();

-- Comentários para documentação
COMMENT ON TABLE auth.user_devices IS 'Dispositivos registrados para push notifications (FCM/APNS)';
COMMENT ON COLUMN auth.user_devices.token IS 'Token FCM (Firebase) ou APNS (Apple)';
COMMENT ON COLUMN auth.user_devices.platform IS 'Plataforma: android, ios, web, desktop';
