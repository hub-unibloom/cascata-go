-- Verificar o metadata do projeto gggg
SELECT 
    id, 
    slug, 
    name,
    metadata,
    metadata->'extra' as extra,
    metadata->'extra'->'auth_config' as auth_config,
    metadata->'extra'->'auth_config'->'providers' as providers,
    metadata->'extra'->'auth_config'->'providers'->'google' as google_config
FROM system.projects 
WHERE slug = 'gggg';
