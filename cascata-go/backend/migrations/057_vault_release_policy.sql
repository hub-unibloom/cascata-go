-- Explicit secret release policy for the Synergy Vault.
--
-- metadata.release_policy:
--   exportable  -> may be revealed to UI/API/runtime callers.
--   runtime     -> may be materialized only inside trusted runtime paths.
--   verify_only -> may be used for HMAC/verification, never returned as plaintext.
--   sign_only   -> reserved for future signing operations, never returned as plaintext.

UPDATE system.project_secrets
SET metadata = COALESCE(metadata, '{}'::jsonb) || jsonb_build_object('release_policy', 'runtime')
WHERE type <> 'folder'
  AND NOT (COALESCE(metadata, '{}'::jsonb) ? 'release_policy');

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'project_secrets_release_policy_valid'
          AND conrelid = 'system.project_secrets'::regclass
    ) THEN
        ALTER TABLE system.project_secrets
        ADD CONSTRAINT project_secrets_release_policy_valid
        CHECK (
            type = 'folder'
            OR COALESCE(metadata->>'release_policy', 'runtime') IN ('exportable', 'runtime', 'verify_only', 'sign_only')
        );
    END IF;
END $$;
