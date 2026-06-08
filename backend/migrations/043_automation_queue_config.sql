-- MIGRATION: Configurações de Queue por Automação
-- Worner decide: retries, delay e prioridade por automação!

-- Adicionar colunas de configuração de queue na tabela automations
ALTER TABLE system.automations 
    ADD COLUMN IF NOT EXISTS queue_retries INTEGER DEFAULT 3,
    ADD COLUMN IF NOT EXISTS queue_retry_delay INTEGER DEFAULT 1000, -- em ms
    ADD COLUMN IF NOT EXISTS queue_priority INTEGER DEFAULT 5; -- 1=high, 5=normal, 10=low

-- Adicionar coluna global_timeout_ms se não existir
ALTER TABLE system.automations 
    ADD COLUMN IF NOT EXISTS global_timeout_ms INTEGER DEFAULT 3000;

-- Comentários explicativos
COMMENT ON COLUMN system.automations.queue_retries IS 'Número máximo de tentativas em caso de falha (0 = sem retry, padrão: 3)';
COMMENT ON COLUMN system.automations.queue_retry_delay IS 'Delay base entre retries em milissegundos (padrão: 1000ms = 1s)';
COMMENT ON COLUMN system.automations.queue_priority IS 'Prioridade na fila: 1=alta, 5=normal, 10=baixa (padrão: 5)';
COMMENT ON COLUMN system.automations.global_timeout_ms IS 'Timeout global da execução em milissegundos (padrão: 3000ms)';

-- Índice para queries por prioridade (otimização)
CREATE INDEX IF NOT EXISTS idx_automations_queue_priority 
    ON system.automations(queue_priority) 
    WHERE is_active = true;
