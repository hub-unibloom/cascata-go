-- Adiciona coluna updated_at à tabela system.ui_settings
-- Phase: Infrastructure Sync
-- Issue: Correção do erro "column updated_at of relation ui_settings does not exist"

-- 1. Add updated_at column to system.ui_settings
ALTER TABLE system.ui_settings ADD COLUMN IF NOT EXISTS updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP;

-- 2. Ensure the update_updated_at_column function exists
CREATE OR REPLACE FUNCTION system.update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = CURRENT_TIMESTAMP;
    RETURN NEW;
END;
$$ language 'plpgsql';

-- 3. Apply trigger to system.ui_settings
DROP TRIGGER IF EXISTS trg_ui_settings_updated_at ON system.ui_settings;
CREATE TRIGGER trg_ui_settings_updated_at
    BEFORE UPDATE ON system.ui_settings
    FOR EACH ROW
    EXECUTE FUNCTION system.update_updated_at_column();
