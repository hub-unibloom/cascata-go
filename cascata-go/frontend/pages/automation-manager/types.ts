// ============================================================================
// NEXUS v0 — TIPOS CANÔNICOS
// ============================================================================
// Todos os tipos do Nexus v0 vivem aqui. Nenhuma referência a v2/v3 deve existir.

/**
 * Modo de execução do fluxo.
 * - 'linear':  Nós executam em sequência (A → B → C). Um só começa após o anterior terminar.
 * - 'parallel': Nós sem dependência direta executam em goroutines simultâneas.
 */
export type ExecutionMode = 'linear' | 'parallel';

/**
 * Modo de execução individual de um nó.
 * - 'sequential': (Pistola) Uma execução por vez.
 * - 'concurrent': (Metralhadora) Múltiplas execuções simultâneas.
 */
export type NodeExecutionMode = 'sequential' | 'concurrent';

/**
 * Tipo do nó na biblioteca Nexus.
 */
export type NexusNodeType = 'trigger' | 'action' | 'ai' | 'tool' | 'condition';

/**
 * Nó Nexus — unidade de execução do grafo.
 */
export interface NexusNode {
   /** ID único do nó na instância (ex: 'node_1716500000000') */
   id: string;

   /** ID da definição na biblioteca (ex: 'pre_event_trigger', 'database_action') */
   node_id: string;

   /** Categoria do nó */
   type: NexusNodeType;

   /** Label visível no canvas */
   label: string;

   /** Posição X no canvas */
   x: number;

   /** Posição Y no canvas */
   y: number;

   /** Configuração específica do nó (filtros, mapeamentos, etc) */
   config: Record<string, any>;

   /** IDs dos próximos nós no grafo */
   next: string[];

   /** Configuração de execução individual */
   execution?: {
      mode: NodeExecutionMode;
      max_concurrency?: number; // Só aplicável em modo 'concurrent'
      timeout_ms?: number;
   };
}

/**
 * Automação Nexus — o blueprint completo de um fluxo.
 */
export interface Automation {
   id: string;
   name: string;
   description: string;
   is_active: boolean;
   status?: 'draft' | 'active' | 'paused' | 'archived';

   /** Nós do grafo Nexus */
   nodes: NexusNode[];

   /** Tipo de trigger (ex: 'API_INTERCEPT', 'CRON', 'WEBHOOK') */
   trigger_type: string;

   /** Configuração do trigger */
   trigger_config: Record<string, any>;

   /** Modo de execução global do fluxo */
   execution_mode?: ExecutionMode;

   /** Timestamps */
   created_at?: string;
   updated_at?: string;
}

/**
 * Payload enviado pelo NexusArchitect ao salvar.
 */
export interface NexusSavePayload {
   name: string;
   nodes: NexusNode[];
   edges?: Array<{
      id?: string;
      source: string;
      sourceHandle?: string | null;
      target: string;
      targetHandle?: string | null;
      type_hint?: string;
   }>;
   execution_mode: ExecutionMode;
}

export interface ExecutionRun {
   id: string;
   automation_id: string;
   status: 'success' | 'error' | 'failed';
   execution_time_ms: number;
   error_message?: string | null;
   trigger_payload?: any;
   final_output?: any;
   created_at: string;
}

export interface StepLog {
   step_id: string;
   node_id: string;
   node_type: string;
   node_name?: string;
   level: 'debug' | 'info' | 'warn' | 'error';
   message: string;
   input_data?: any;
   output_data?: any;
   error_details?: string;
   duration_ms?: number;
   metadata?: any;
   created_at: string;
}

export interface ExecutionDetail {
   execution_id: string;
   logs: StepLog[];
   count: number;
}

export interface AutomationStats {
   total_runs: number;
   success_count: number;
   failed_count: number;
   avg_ms: number;
   last_run_at: string | null;
}

export const SYSTEM_RPC_PREFIXES = ['uuid_', 'pg_', 'armor', 'crypt', 'digest', 'hmac', 'gen_', 'encrypt', 'decrypt', 'pissh_', 'notify_', 'dearmor', 'fips_mode'];