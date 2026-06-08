-- API Keys Vault Integration (Glory Edition)
-- This migration adds a reference to the Vault for API Keys.
-- Sensitive key material will now reside in the system.project_secrets (protected by CCE).

ALTER TABLE system.api_keys ADD COLUMN IF NOT EXISTS vault_item_id UUID;

-- Optional: Add index for performance
CREATE INDEX IF NOT EXISTS idx_api_keys_vault_id ON system.api_keys(vault_item_id);
