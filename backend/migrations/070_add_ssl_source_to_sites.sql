-- Migration 070: Add ssl_certificate_source to existing sites table
ALTER TABLE system.sites ADD COLUMN IF NOT EXISTS ssl_certificate_source TEXT;
