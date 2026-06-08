-- Migration 066: Edge Functions System
-- Creates the system.edge_functions table for serverless JavaScript function execution

-- Create system.edge_functions table
CREATE TABLE IF NOT EXISTS system.edge_functions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_slug TEXT NOT NULL,
    name TEXT NOT NULL,
    content TEXT NOT NULL,
    runtime TEXT DEFAULT 'javascript',
    status TEXT DEFAULT 'active',
    metadata JSONB DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ DEFAULT now(),
    updated_at TIMESTAMPTZ DEFAULT now(),
    UNIQUE(project_slug, name)
);

-- Create indexes for performance
CREATE INDEX IF NOT EXISTS idx_edge_functions_project_slug ON system.edge_functions(project_slug);
CREATE INDEX IF NOT EXISTS idx_edge_functions_status ON system.edge_functions(status);
CREATE INDEX IF NOT EXISTS idx_edge_functions_name ON system.edge_functions(name);

-- Add updated_at trigger
CREATE OR REPLACE FUNCTION system.update_edge_functions_updated_at()
RETURNS TRIGGER AS $func$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$func$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trigger_update_edge_functions_updated_at ON system.edge_functions;
DO $$ BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM information_schema.triggers
    WHERE event_object_table = 'edge_functions'
    AND trigger_name = 'trigger_update_edge_functions_updated_at'
  ) THEN
    CREATE TRIGGER trigger_update_edge_functions_updated_at
      BEFORE UPDATE ON system.edge_functions
      FOR EACH ROW
      EXECUTE FUNCTION system.update_edge_functions_updated_at();
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

-- Enable RLS (idempotent)
DO $$ BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_tables
    WHERE tablename = 'edge_functions'
    AND rowsecurity = true
  ) THEN
    ALTER TABLE system.edge_functions ENABLE ROW LEVEL SECURITY;
  END IF;
END $$;

-- RLS Policies: Admins can do everything, others can only read active functions (idempotent)
DO $$ BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_policies
    WHERE tablename = 'edge_functions'
    AND policyname = 'edge_functions_admin_all'
  ) THEN
    CREATE POLICY "edge_functions_admin_all" ON system.edge_functions
      FOR ALL USING (auth.role() = 'service_role')
      WITH CHECK (auth.role() = 'service_role');
  END IF;
END $$;

DO $$ BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_policies
    WHERE tablename = 'edge_functions'
    AND policyname = 'edge_functions_read_active'
  ) THEN
    CREATE POLICY "edge_functions_read_active" ON system.edge_functions
      FOR SELECT USING (status = 'active');
  END IF;
END $$;

DO $$ BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_policies
    WHERE tablename = 'edge_functions'
    AND policyname = 'edge_functions_insert'
  ) THEN
    CREATE POLICY "edge_functions_insert" ON system.edge_functions
      FOR INSERT WITH CHECK (auth.role() = 'service_role');
  END IF;
END $$;

DO $$ BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_policies
    WHERE tablename = 'edge_functions'
    AND policyname = 'edge_functions_update'
  ) THEN
    CREATE POLICY "edge_functions_update" ON system.edge_functions
      FOR UPDATE USING (auth.role() = 'service_role')
      WITH CHECK (auth.role() = 'service_role');
  END IF;
END $$;

DO $$ BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_policies
    WHERE tablename = 'edge_functions'
    AND policyname = 'edge_functions_delete'
  ) THEN
    CREATE POLICY "edge_functions_delete" ON system.edge_functions
      FOR DELETE USING (auth.role() = 'service_role');
  END IF;
END $$;
