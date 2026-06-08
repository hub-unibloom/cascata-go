-- AI semantic entity linking support
CREATE EXTENSION IF NOT EXISTS unaccent;
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE EXTENSION IF NOT EXISTS fuzzystrmatch;

CREATE TABLE IF NOT EXISTS system.schema_aliases (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    project_slug TEXT NOT NULL REFERENCES system.projects(slug) ON DELETE CASCADE,
    entity_type TEXT NOT NULL CHECK (entity_type IN ('table', 'column')),
    table_name TEXT NOT NULL,
    column_name TEXT,
    alias TEXT NOT NULL,
    weight INT DEFAULT 100,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_schema_aliases_project ON system.schema_aliases (project_slug);
CREATE INDEX IF NOT EXISTS idx_schema_aliases_alias_trgm ON system.schema_aliases USING gin (alias gin_trgm_ops);
CREATE UNIQUE INDEX IF NOT EXISTS idx_schema_aliases_unique
ON system.schema_aliases (project_slug, entity_type, table_name, COALESCE(column_name, ''), alias);
