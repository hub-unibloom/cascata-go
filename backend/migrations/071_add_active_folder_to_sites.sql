-- Migration 071: Add active_folder field to sites table for versioning support
-- This allows selecting which subfolder within the site's storage path is currently active
ALTER TABLE system.sites ADD COLUMN IF NOT EXISTS active_folder VARCHAR(255) DEFAULT '';
