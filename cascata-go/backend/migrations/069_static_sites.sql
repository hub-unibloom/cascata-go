-- Migration 069: Static Sites Deployment
CREATE TABLE IF NOT EXISTS system.sites (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_slug TEXT NOT NULL REFERENCES system.projects(slug) ON DELETE CASCADE,
    name VARCHAR(255) NOT NULL,
    domain VARCHAR(255) UNIQUE,
    storage_path VARCHAR(512) NOT NULL,
    ssl_certificate_source TEXT, -- Herda certificado do projeto pai ou usa wildcard
    status VARCHAR(50) NOT NULL DEFAULT 'active', -- active, building, error
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- Trigger to update updated_at timestamp
CREATE OR REPLACE FUNCTION system.update_site_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
   NEW.updated_at = NOW();
   RETURN NEW;
END;
$$ language 'plpgsql';

CREATE OR REPLACE TRIGGER update_site_updated_at BEFORE UPDATE ON system.sites
FOR EACH ROW EXECUTE FUNCTION system.update_site_updated_at_column();
