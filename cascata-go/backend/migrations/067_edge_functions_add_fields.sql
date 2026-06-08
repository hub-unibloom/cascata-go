-- Migration 067: Add configuration fields to edge_functions
-- Adds env_vars, imports, and timeout fields for edge function configuration

-- Add new columns to system.edge_functions
ALTER TABLE system.edge_functions ADD COLUMN IF NOT EXISTS env_vars JSONB DEFAULT '{}'::jsonb;
ALTER TABLE system.edge_functions ADD COLUMN IF NOT EXISTS imports TEXT[] DEFAULT ARRAY[]::TEXT[];
ALTER TABLE system.edge_functions ADD COLUMN IF NOT EXISTS timeout INT DEFAULT 5000;
ALTER TABLE system.edge_functions ADD COLUMN IF NOT EXISTS version INT DEFAULT 1;

-- Create history table for version tracking
CREATE TABLE IF NOT EXISTS system.edge_functions_history (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    function_id UUID NOT NULL,
    project_slug TEXT NOT NULL,
    name TEXT NOT NULL,
    content TEXT NOT NULL,
    version INT NOT NULL,
    created_by TEXT,
    change_reason TEXT,
    created_at TIMESTAMPTZ DEFAULT now()
);

-- Create indexes for history table
CREATE INDEX IF NOT EXISTS idx_edge_functions_history_function_id ON system.edge_functions_history(function_id);
CREATE INDEX IF NOT EXISTS idx_edge_functions_history_project_slug ON system.edge_functions_history(project_slug);

-- Grant permissions on history table (idempotent)
DO $$ BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM information_schema.role_table_grants
    WHERE table_name = 'edge_functions_history'
    AND grantee = 'authenticated'
    AND privilege_type = 'SELECT'
  ) THEN
    GRANT SELECT, INSERT, UPDATE, DELETE ON system.edge_functions_history TO authenticated;
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

-- Grant sequence permissions (idempotent)
DO $$ BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM information_schema.role_usage_grants
    WHERE object_schema = 'system'
    AND grantee = 'authenticated'
  ) THEN
    GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA system TO authenticated;
  END IF;
END $$;

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
