# Implementação da Arquitetura MCP (Model Context Protocol)

Este documento detalha o planejamento arquitetural para introduzir o protocolo MCP de maneira robusta, escalável e segura no Cascata, sem a necessidade de reescrever as milhares de rotas e lógicas de segurança (RLS, Rate Limiting, JWT) já existentes no sistema.

## Visão Geral da Arquitetura

O Model Context Protocol (MCP) é baseado em JSON-RPC. A grande "sacada" para não reescrever as rotas é **usar o MCP como um Adapter/Proxy Interno**. Em vez de recriar a lógica do banco de dados para os agentes de IA, o servidor MCP atuará traduzindo as chamadas de "tools" (ferramentas) do MCP para chamadas HTTP internas ou execuções diretas das camadas de serviço, herdando automaticamente *todo* o mecanismo de segurança já polido.

### Os 3 Níveis de Acesso

1. **MCP Worner (Global)**
   - **Endpoint**: `/api/mcp/message` (Protegido por `RequireManagementRole`).
   - **Poder**: Gerenciamento de toda a infraestrutura, criação de tenants, configurações globais.
   - **Toggle**: Controlado em `SystemSettings.tsx` (Global Governance).

2. **MCP Worner Tenancy (Semi-Global)**
   - **Endpoint**: `/api/data/{slug}/mcp/message` (Protegido pelo `CascataAuth` com papel de Owner do projeto).
   - **Poder**: Total sobre o tenant (tabelas, RPCs, Storage, etc), mas isolado em seu próprio universo.
   - **Toggle**: Controlado em `ProjectIntelligence.tsx`.

3. **MCP Consumer (End-user)**
   - **Endpoint**: `/api/data/{slug}/mcp/message` (Protegido pelo `CascataAuth` com token de usuário final ou Key Group restrito).
   - **Poder**: Idêntico à API REST pública/privada. O agente só poderá ler/escrever o que o RLS do Postgres e as Policies do Cascata permitirem.
   - **Customização**: O tenant poderá gerar chaves específicas para consumidores MCP, limitando quais ferramentas (tools) esse agente enxerga (ex: um MCP só para "estoque").

## Proposed Changes

---

### Backend: MCP Router & Dispatcher

Criaremos um sistema de "Tool Registry" que mapeia funções MCP para ações internas.

#### [NEW] `backend/internal/mcp/registry.go`
- Registro dinâmico de ferramentas (Tools).
- **Global Tools**: `list_projects`, `create_project`, `system_stats`.
- **Tenant Tools**: `list_tables`, `execute_sql`, `manage_rls`, `deploy_edge_function`.
- **Consumer Tools**: `query_table`, `insert_rows`, `execute_rpc`.

#### [MODIFY] `backend/internal/controllers/mcp.go`
- **Reescrita do `HandleMessage`**: O controlador deixará de ser um mock. Ele fará o parse do JSON-RPC 2.0 e extrairá o nome da "tool".
- **Internal Loopback**: Para ferramentas de dados (ex: `query_table`), o controlador MCP construirá uma requisição HTTP interna simulada (usando `httptest.ResponseRecorder` ou extraindo a lógica para a camada de serviço) e a enviará para o `DataController` ou `AdminController`. Isso garante que o **DynamicRateLimiter**, **AppClientTableAccess** e **PostgREST adapter** sejam acionados perfeitamente, garantindo risco ZERO de bypass de segurança.

#### [MODIFY] `backend/cmd/server/main.go`
- Nenhuma alteração estrutural nas rotas é necessária! A injeção de dependências do `McpController` pode precisar do `chi.Router` ou dos demais controllers para realizar o roteamento interno.

---

### Frontend: Interface Premium e Dinâmica

Visualmente, a aplicação já possui um excelente nível, mas precisamos criar as interfaces de gerenciamento dos *Consumers* de forma que impressionem o usuário, utilizando a estética premium requerida.

#### [MODIFY] `frontend/pages/ProjectIntelligence.tsx`
- **Toggle de Acesso MCP**: Manteremos o design existente do master switch, integrando-o totalmente à API real.
- **[NEW] Seção de "MCP Consumers"**: 
  - Interface glassmorphism vibrante para listar, criar e gerenciar "Agentes Consumidores".
  - Opção para gerar as URLs e os *Connection Configs* (JSON do Cursor/Windsurf) prontos para uso com 1 clique (Copy to Clipboard).
  - Micro-interações ao expandir detalhes do consumidor.

#### [MODIFY] `frontend/pages/SystemSettings.tsx`
- **Global Governance Switch**: Conectar o toggle do MCP Worner à configuração global do sistema no backend.

## Estratégia de Execução (O Segredo da Simplicidade)

Para evitar inflar o código, **as "tools" que o MCP expõe para o modelo de IA serão genéricas no nível de dados**:
Em vez de expor 1000 ferramentas como `get_users`, `get_products`, exporemos `select_rows(table_name, select_query, filters)` e `execute_rpc(rpc_name, params)`.
A IA de hoje (como Gemini, Claude, GPT-4) é inteligente o suficiente para primeiro chamar uma ferramenta `list_tables` e `get_schema`, entender a estrutura do banco e depois usar `select_rows` na tabela correta. Isso mantém nosso backend extremamente simples, mantendo uma superfície de ataque pequena.

> [!IMPORTANT]
> **Segurança Intacta**: A beleza dessa abordagem é que a IA vai passar pelo mesmo afunilamento que uma chamada cURL do FrontEnd passaria. O RLS do banco de dados decidirá o destino final.

> [!TIP]
> **Performance**: Como as chamadas serão roteadas internamente na memória da aplicação Go, a latência de "tradução" do JSON-RPC para a rota interna será menor que 1 milissegundo.

## User Review Required
> [!WARNING]
> O uso do loopback interno (chamar nossos próprios controllers dentro do código Go) é uma técnica muito eficiente, mas requer que passemos o Contexto (`types.CascataCtxKey`) corretamente. Você aprova essa abordagem de **Adapter/Proxy Interno** onde o MCP usa as mesmas engrenagens do REST?

## Open Questions
1. **Frontend do Consumer**: Para os "MCP Consumers", você quer que geremos na tela o arquivo de configuração `.json` no padrão do Cursor/Claude Desktop para que o usuário final apenas copie e cole na sua IDE?
2. **Tools Genéricas**: Concorda em fornecermos ferramentas genéricas de dados (`select_rows`, `insert_rows`, `get_schema`) ao invés de criar dinamicamente uma ferramenta pra cada tabela (que poluiria a janela de contexto da IA)?
