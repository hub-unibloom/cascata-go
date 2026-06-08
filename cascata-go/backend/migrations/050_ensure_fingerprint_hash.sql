-- Migration 050: Ensure fingerprint_hash column exists on auth.refresh_tokens
-- This migration ensures backward compatibility for projects created before migration 041
-- and fixes the "Coluna fingerprint_hash inexistente" error

BEGIN;

DO $$
BEGIN
    -- Check if auth schema exists
    IF EXISTS (SELECT FROM information_schema.schemata WHERE schema_name = 'auth') THEN
        
        -- Check if refresh_tokens table exists
        IF EXISTS (SELECT FROM information_schema.tables WHERE table_schema = 'auth' AND table_name = 'refresh_tokens') THEN
            
            -- Add fingerprint_hash column if it doesn't exist
            IF NOT EXISTS (
                SELECT FROM information_schema.columns 
                WHERE table_schema = 'auth' 
                AND table_name = 'refresh_tokens' 
                AND column_name = 'fingerprint_hash'
            ) THEN
                ALTER TABLE auth.refresh_tokens 
                ADD COLUMN fingerprint_hash TEXT;
                
                RAISE NOTICE 'Added fingerprint_hash column to auth.refresh_tokens';
            END IF;
            
            -- Create index if it doesn't exist
            IF NOT EXISTS (
                SELECT FROM pg_indexes 
                WHERE schemaname = 'auth' 
                AND tablename = 'refresh_tokens' 
                AND indexname = 'idx_refresh_tokens_fingerprint'
            ) THEN
                CREATE INDEX idx_refresh_tokens_fingerprint 
                ON auth.refresh_tokens(fingerprint_hash);
                
                RAISE NOTICE 'Created idx_refresh_tokens_fingerprint index';
            END IF;
            
        END IF;
        
    END IF;
END $$;

COMMIT;
