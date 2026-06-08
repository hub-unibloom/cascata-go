
-- Adiciona suporte a RLS (Row Level Security) para buckets
-- Por padrão, todos os buckets novos têm RLS ativo

-- Adiciona coluna rls_enabled na tabela storage_objects
ALTER TABLE system.storage_objects ADD COLUMN IF NOT EXISTS rls_enabled BOOLEAN DEFAULT true;

-- Atualiza buckets existentes para ter rls_enabled = true (comportamento padrão seguro)
UPDATE system.storage_objects 
SET rls_enabled = true 
WHERE is_folder = true AND parent_path = '';

-- Cria índice para consultas rápidas de buckets com RLS
CREATE INDEX IF NOT EXISTS idx_storage_bucket_rls ON system.storage_objects (project_slug, bucket, rls_enabled) WHERE is_folder = true AND parent_path = '';
