-- Migration 067: Edge Functions Enhancements
-- Adds advanced features: versioning, configuration, metrics, and environment variables

-- Add new columns to system.edge_functions
ALTER TABLE system.edge_functions 
ADD COLUMN IF NOT EXISTS timeout_ms INTEGER DEFAULT 5000,
ADD COLUMN IF NOT EXISTS memory_limit_mb INTEGER DEFAULT 128,
ADD COLUMN IF NOT EXISTS env_vars JSONB DEFAULT '{}'::jsonb,
ADD COLUMN IF NOT EXISTS version INTEGER DEFAULT 1,
ADD COLUMN IF NOT EXISTS last_invoked_at TIMESTAMPTZ,
ADD COLUMN IF NOT EXISTS invocation_count BIGINT DEFAULT 0,
ADD COLUMN IF NOT EXISTS error_count BIGINT DEFAULT 0,
ADD COLUMN IF NOT EXISTS last_error TEXT,
ADD COLUMN IF NOT EXISTS last_error_at TIMESTAMPTZ;

-- Create indexes for performance
CREATE INDEX IF NOT EXISTS idx_edge_functions_last_invoked ON system.edge_functions(last_invoked_at);
CREATE INDEX IF NOT EXISTS idx_edge_functions_invocation_count ON system.edge_functions(invocation_count);
CREATE INDEX IF NOT EXISTS idx_edge_functions_version ON system.edge_functions(version);

-- Create edge_functions_history table for versioning
CREATE TABLE IF NOT EXISTS system.edge_functions_history (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    function_id UUID NOT NULL,
    project_slug TEXT NOT NULL,
    name TEXT NOT NULL,
    content TEXT NOT NULL,
    runtime TEXT DEFAULT 'javascript',
    version INTEGER NOT NULL,
    metadata JSONB DEFAULT '{}'::jsonb,
    timeout_ms INTEGER DEFAULT 5000,
    memory_limit_mb INTEGER DEFAULT 128,
    env_vars JSONB DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ DEFAULT now(),
    created_by TEXT,
    change_reason TEXT
);

-- Create indexes for history table
CREATE INDEX IF NOT EXISTS idx_edge_functions_history_function_id ON system.edge_functions_history(function_id);
CREATE INDEX IF NOT EXISTS idx_edge_functions_history_project_slug ON system.edge_functions_history(project_slug);
CREATE INDEX IF NOT EXISTS idx_edge_functions_history_name ON system.edge_functions_history(name);
CREATE INDEX IF NOT EXISTS idx_edge_functions_history_version ON system.edge_functions_history(version);

-- Enable RLS on history table (idempotent)
DO $$ BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_tables
    WHERE tablename = 'edge_functions_history'
    AND rowsecurity = true
  ) THEN
    ALTER TABLE system.edge_functions_history ENABLE ROW LEVEL SECURITY;
  END IF;
END $$;

-- RLS Policies for history table (idempotent)
DO $$ BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_policies
    WHERE tablename = 'edge_functions_history'
    AND policyname = 'edge_functions_history_admin_all'
  ) THEN
    CREATE POLICY "edge_functions_history_admin_all" ON system.edge_functions_history
      FOR ALL USING (auth.role() = 'service_role')
      WITH CHECK (auth.role() = 'service_role');
  END IF;
END $$;

DO $$ BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_policies
    WHERE tablename = 'edge_functions_history'
    AND policyname = 'edge_functions_history_read'
  ) THEN
    CREATE POLICY "edge_functions_history_read" ON system.edge_functions_history
      FOR SELECT USING (auth.role() = 'service_role');
  END IF;
END $$;

-- Grant permissions (idempotent)
DO $$ BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM information_schema.role_table_grants
    WHERE table_name = 'edge_functions'
    AND grantee = 'authenticated'
    AND privilege_type = 'SELECT'
  ) THEN
    GRANT SELECT, INSERT, UPDATE, DELETE ON system.edge_functions TO authenticated;
  END IF;
END $$;

DO $$ BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM information_schema.role_table_grants
    WHERE table_name = 'edge_functions'
    AND grantee = 'anon'
    AND privilege_type = 'SELECT'
  ) THEN
    GRANT SELECT ON system.edge_functions TO anon;
  END IF;
END $$;

DO $$ BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM information_schema.role_usage_grants
    WHERE object_schema = 'system'
    AND grantee = 'authenticated'
  ) THEN
    GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA system TO authenticated;
  END IF;
END $$;

DO $$ BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM information_schema.role_table_grants
    WHERE table_name = 'edge_functions_history'
    AND grantee = 'authenticated'
    AND privilege_type = 'SELECT'
  ) THEN
    GRANT SELECT, INSERT ON system.edge_functions_history TO authenticated;
  END IF;
END $$;

DO $$ BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM information_schema.role_table_grants
    WHERE table_name = 'edge_functions_history'
    AND grantee = 'anon'
    AND privilege_type = 'SELECT'
  ) THEN
    GRANT SELECT ON system.edge_functions_history TO anon;
  END IF;
END $$;
