
-- Roles globais do sistema (necessárias para RLS)
-- Este arquivo roda apenas na criação do volume, garantindo que as roles existam.
-- As tabelas agora são gerenciadas pelo Migration Runner do Backend.

-- Criar role postgres superuser (necessária para conexões de inicialização)
DO $$ 
BEGIN
  IF NOT EXISTS (SELECT FROM pg_catalog.pg_roles WHERE rolname = 'postgres') THEN
    CREATE ROLE postgres WITH SUPERUSER LOGIN PASSWORD 'postgres';
  END IF;
END $$;

-- Criar role cascata_admin (superuser do sistema Cascata)
DO $$ 
BEGIN
  IF NOT EXISTS (SELECT FROM pg_catalog.pg_roles WHERE rolname = 'cascata_admin') THEN
    CREATE ROLE cascata_admin WITH SUPERUSER LOGIN PASSWORD 'secure_pass';
  END IF;
END $$;

-- Roles de autenticação padrão (anon, authenticated, service_role)
DO $$ 
BEGIN
  IF NOT EXISTS (SELECT FROM pg_catalog.pg_roles WHERE rolname = 'authenticated') THEN
    CREATE ROLE authenticated NOLOGIN;
  END IF;
  IF NOT EXISTS (SELECT FROM pg_catalog.pg_roles WHERE rolname = 'anon') THEN
    CREATE ROLE anon NOLOGIN;
  END IF;
  IF NOT EXISTS (SELECT FROM pg_catalog.pg_roles WHERE rolname = 'service_role') THEN
    CREATE ROLE service_role NOLOGIN;
  END IF;
  IF NOT EXISTS (SELECT FROM pg_catalog.pg_roles WHERE rolname = 'cascata_api_role') THEN
    CREATE ROLE cascata_api_role NOLOGIN;
  END IF;
END $$;

-- Grant cascata_admin to postgres para manter compatibilidade
GRANT cascata_admin TO postgres;

-- ============================================================
-- PG_CRON EXTENSION (System-Level Job Scheduler)
-- Instalado no banco cascata_system - a nível de sistema, não por tenant
-- Isso permite agendamentos centralizados com roteamento via schedule_in_database
-- ============================================================
CREATE EXTENSION IF NOT EXISTS pg_cron;

-- Grant acesso ao cascata_admin
GRANT ALL ON SCHEMA cron TO cascata_admin;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA cron TO cascata_admin;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA cron TO cascata_admin;
