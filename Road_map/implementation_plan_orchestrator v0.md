# Nexus Engine (Cascata Orchestrator v1.0) - Plano de Implementação Master

Este documento é a **especificação técnica definitiva** para a construção do Nexus Engine v0. Ele representa a fusão entre a arquitetura de defesa de borda do Cascata e um motor de orquestração de inteligência agêntica de grau industrial.

## 🧬 Filosofia Nexus: "Inteligência Orquestrada e Blindada"

O Nexus Engine não é apenas um sistema de automação; é uma **plataforma de inteligência orquestrada**. Ele transforma a blindagem do Cascata em poder de execução cognitivo, permitindo que cada requisição seja interceptada, analisada por IA, enriquecida com memória semântica e processada por agentes autônomos antes mesmo de tocar o banco de dados.

### Pilares Fundamentais:
1.  **Isolamento Total (GoFlow):** Componentes como processos independentes em goroutines, sem estado compartilhado.
2.  **Orquestração Complexa (GoFlow v2):** Suporte a DAGs arbitrários, branching, aggregation, subdags e foreach.
3.  **Reatividade Visual (XYFlow):** O frontend é um espelho vivo e animado do backend.
4.  **IA-First (Agentic Layer):** Nascido para agentes. Cada nó pode ser ferramenta (Tool). Cada fluxo pode pensar.
5.  **Computação Avançada (QDant):** Integração nativa com o motor QDant para processamento de alta performance.

---

## 🏗️ Camada de Defesa e Sequestro (Layer 4)

O Nexus opera na **Layer 4** da arquitetura de defesa. O fluxo real de uma requisição é:
`[LAYER 0: Nginx] → [LAYER 3.5: Strikes] → [LAYER 1: IP Cap] → [LAYER 2: JWT] → [LAYER 3: Rate Limit] → [LAYER 4: Nexus / PostgreSQL]`.

### 1. Sequestro Pré-Evento (PRE_PERSIST)
*   **Mecanismo:** Intercepta a requisição imediatamente antes do handler padrão de dados.
*   **Hijack:** O banco de dados é ignorado pelo fluxo padrão. A automação assume a resposta final.
*   **Fast Lane (Síncrono):** Execução na mesma goroutine do request HTTP. Latência ultra-baixa.
*   **Response Node:** Obrigatório em fluxos de sequestro para devolver o output ao cliente.

### 2. Pós-Evento (POST_PERSIST)
*   **Worker Lane (Assíncrono):** Dispara após o sucesso da operação no banco.
*   **Fire-and-Forget:** O cliente recebe a resposta e o fluxo roda em background via **Dragonfly (Redis)**.

### 3. Data Privacy Override (Universal Padlock)
*   O nó **Privacy Guard** aplica regras de anonimização (masking/redacting) baseadas no perfil do usuário e contexto da Layer 3 antes do retorno.

---

## 🧠 A Camada Agêntica (Nexus AI Brain)

O Nexus v0 é o ambiente definitivo para **Super Agentes**.

1.  **Agent Node (The Mastermind):** Implementa o padrão **ReAct (Reasoning + Acting)**. Recebe um objetivo e usa ferramentas (Tools) autonomamente para resolvê-lo.
2.  **Nodes as Tools:** Qualquer nó do Nexus (HTTP, Data, QDant, Subdag) é automaticamente registrado como uma ferramenta para a IA.
3.  **Memory Architecture:**
    *   **Working Memory:** Nexus State (em memória).
    *   **Short-term:** Contexto de sessão no Dragonfly.
    *   **Long-term Semantic:** Conhecimento persistente via `pgvector` (RAG).
4.  **Cost Controller:** Controle rigoroso de budget de tokens e custos de LLM por Tenant e Execução.

---

## ⚙️ Especificação do Core Backend (Go)

### 1. Information Packets (IP)
Os dados fluem em pacotes com metadados indestrutíveis: `TraceID`, `TenantID`, `UserUUID`, `AuthSource`. Garantia de isolamento multi-tenant absoluto.

### 2. Nexus Runtime
Cada nó é um `Component` Go. O motor utiliza o **Kahn's Algorithm** para validação topológica e detecção de ciclos em tempo de compilação.

---

## 📅 Roadmap de Implementação (Fases Detalhadas)

### Fase 1: Fundação do Nexus Core FBP (Backend)
- [ ] Implementar `backend/internal/services/nexus/component.go` (Interface base).
- [ ] Implementar `backend/internal/services/nexus/graph/graph.go` (Estrutura DAG).
- [ ] Implementar `backend/internal/services/nexus/nexus_engine.go` (Core Runtime).
- [ ] Implementar `backend/internal/services/nexus/nexus_compiler.go` (JSON -> ExecutionPlan).
- [ ] Implementar `backend/internal/services/nexus/nexus_state.go` (Variable Resolver & Interpolation).
- [ ] Implementar `backend/internal/services/nexus/packet.go` (Information Packet Specification).
- [ ] Implementar Component Registry básico (Trigger, Response, Condition, Transform, Switch, Merge, Split).
- [ ] Criar sistema de filas Worker Lane via Dragonfly.

### Fase 2: Integração de Defesa & Sequestro (Layer 4)
- [ ] Implementar `nexus_hook_resolver.go` (Lógica de matching de automações).
- [ ] Acoplar hooks em `backend/internal/controllers/data.go` para **PRE_PERSIST Hijack**.
- [ ] Implementar RLS Bridge para Data Nodes (Segurança nativa do PostgreSQL).
- [ ] Implementar **Privacy Guard Node** (Integração Universal Padlock).
- [ ] Criar sistema de monitoramento de falhas e fail-safe de automação.

### Fase 3: Data, QDant & Ferramentas
- [ ] Implementar Data Node (CRUD com RLS).
- [ ] Implementar HTTP Node (Sandboxed com allowlist).
- [ ] Implementar **QDant Node** (Estatísticas, Anomalias, Forecast).
- [ ] Implementar `ai/tool_registry.go` (Auto-Discovery de ferramentas).
- [ ] Implementar Aggregator e Subdag Nodes.

### Fase 4: Camada Agêntica (O Cérebro)
- [ ] Implementar `backend/internal/services/nexus/ai/llm_connector.go` (OpenAI, Gemini, Anthropic, Ollama).
- [ ] Implementar **Agent Node** (Ciclo ReAct, Function Calling).
- [ ] Implementar Chain Node e Prompt Template Node.
- [ ] Implementar Memory Manager (Short-term/Semantic pgvector).
- [ ] Implementar **Cost Controller** (Budgeting e Rate Limit de tokens).

### Fase 5: Nexus Architect (Frontend XYFlow)
- [ ] Instanciar XYFlow no `AutomationManager.tsx`.
- [ ] Desenvolver biblioteca de `CustomNodes` (Status indicators, handles coloridos).
- [ ] Implementar **VariableMapper.tsx** (Conexão visual de variáveis com auto-complete).
- [ ] Criar interface do Agent Builder e Memory Explorer.

### Fase 6: NexusStream (Telemetria Real-time)
- [ ] Implementar `backend/internal/services/nexus/nexus_stream.go` (WebSocket Manager).
- [ ] Desenvolver protocolo de eventos (graph.started, node.output, agent.thought).
- [ ] Implementar **LiveDebugger.tsx** e **Edge Animation** (Fios iluminados e partículas).
- [ ] Implementar funcionalidade de Replay e Time-travel debugging.

---

## 🧪 Plano de Verificação e Stress Testing

1.  **Carga Extrema:** 10.000 requests simultâneos em PRE_PERSIST para validar isolamento de goroutines.
2.  **Segurança Multi-Tenant:** Tentar injetar ferramentas ou memória de outro tenant (Isolamento via `tenant_id` no DB e State).
3.  **Validação de Super Agente:** Criar um fluxo onde o agente intercepta um request, consulta o banco via Data Tool, analisa com QDant, consulta memória semântica e responde em <3s.

---
**Este documento encerra o planejamento e serve como especificação única para a execução do Nexus Engine v0.**
