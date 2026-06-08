# Camadas de Segurança Pré-Persistência (Pre-Persistence Security Layers)

## Visão Geral

Este documento descreve todas as camadas de segurança que os dados **DEVEM** passar antes de serem persistidos no PostgreSQL. Estas são as últimas barreiras de defesa - a "porta final" que garante que apenas dados válidos, autorizados e processados corretamente tocam o banco de dados.

> ⚠️ **CRITICAL**: Nenhum dado pode burlar estas camadas. Todos os endpoints de escrita (POST, PUT, PATCH) devem passar obrigatoriamente por este pipeline de segurança.

---

## Arquitetura de Defesa em Profundidade

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                    EDGE DEFENSE (Camadas Externas)                          │
│  ├─ DDoS Protection (Layer 1-3 Rate Limiting)                              │
│  ├─ IP Blacklisting                                                          │
│  ├─ Panic Mode (Project Lockdown)                                          │
│  ├─ JWT Authentication                                                       │
│  └─ CORS & Security Headers                                                  │
└─────────────────────────────────────────────────────────────────────────────┘
                                    ↓
┌─────────────────────────────────────────────────────────────────────────────┐
│              PROJECT RESOLUTION & CONTEXT INITIALIZATION                    │
│  ├─ Tenant Identification (slug extraction)                                 │
│  ├─ Database Pool Resolution                                                │
│  └─ User Context Population (JWT claims, role, permissions)                 │
└─────────────────────────────────────────────────────────────────────────────┘
                                    ↓
┌─────────────────────────────────────────────────────────────────────────────┐
│           PRE-PERSISTENCE SECURITY LAYERS (Este Documento)                  │
│  ╔═══════════════════════════════════════════════════════════════════════╗  │
│  ║  1. FORMAT PATTERN VALIDATION (Regex)                                ║  │
│  ║  2. LOCKED COLUMNS STRIPPING (Security Padlock)                     ║  │
│  ║  3. COMPUTED COLUMNS (Fórmulas Matemáticas)                         ║  │
│  ║  4. RLS CONTEXT APPLICATION (Row Level Security)                    ║  │
│  ║  5. SQL EXECUTION (PostgreSQL with RLS)                              ║  │
│  ║  6. AUTOMATION INTERCEPTORS (Sequestro de Query)                    ║  │
│  ║  7. PRIVACY MASKING (Data Obfuscation)                              ║  │
│  ╚═══════════════════════════════════════════════════════════════════════╝  │
└─────────────────────────────────────────────────────────────────────────────┘
                                    ↓
                            CLIENT RESPONSE
```

---

## As 7 Camadas de Segurança Pré-Persistência

### 1. FORMAT PATTERN VALIDATION (Regex Validation)

**Propósito**: Garantir que os dados estejam no formato correto antes de serem salvos.

**Funcionamento**:
- Extrai o `formatPattern` do comentário da coluna (formato: `||FORMAT:pattern`)
- Valida o valor contra o regex configurado
- Rejeita a requisição inteira se qualquer campo for inválido

**Exemplos de Patterns**:
```
email     → ^[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}$
cpf       → ^\d{3}\.\d{3}\.\d{3}-\d{2}$
phone_br  → ^\(?\d{2}\)?[\s-]?\d{4,5}-?\d{4}$
custom    → Qualquer regex definido pelo usuário
```

**Resposta em caso de falha**:
```json
{
  "error": "validation failed for column 'ssn': value \"abc\" does not match the required format. Expected format: ^\\d{3}-?\\d{2}-?\\d{4}$"
}
```

**Onde está implementado**:
- `backend/internal/controllers/data.go` - `HandlePostgrest()` (linhas 577-603)
- `backend/internal/utils/sql.go` - `InsertRows()` (linhas 289-286)
- `backend/internal/utils/sql.go` - `UpdateRows()` (linhas 368-400)

---

### 2. LOCKED COLUMNS STRIPPING (Security Padlock)

**Propósito**: Impedir a modificação de colunas marcadas como imutáveis (immutable).

**Funcionamento**:
- Verifica o `lockLevel` de cada coluna no metadata do projeto
- Remove do payload quaisquer colunas marcadas como `"immutable"`
- Loga a remoção para auditoria

**Níveis de Lock**:
```
unlocked           → Pode ser lido/escrito normalmente
immutable          → Nunca pode ser modificado após criação
insert_only        → Só pode ser definido no INSERT, não no UPDATE
service_role_only  → Só service_role pode modificar
otp_protected      → Requer OTP para modificação
```

**Exemplo**:
```go
// Coluna 'created_at' marcada como immutable
// Payload tenta modificar: {"created_at": "2024-01-01", "name": "John"}
// Após stripping: {"name": "John"}
// Log: "[HandlePostgrest] Stripped locked column 'created_at' from POST"
```

**Onde está implementado**:
- `backend/internal/controllers/data.go` - `HandlePostgrest()` (linhas 605-612)
- `backend/internal/utils/sql.go` - `InsertRows()` (linhas 279-287)
- `backend/internal/utils/sql.go` - `UpdateRows()` (linhas 356-361)

---

### 3. COMPUTED COLUMNS (Fórmulas Matemáticas Customizadas)

**Propósito**: Calcular valores automaticamente baseados em fórmulas matemáticas antes da persistência.

**Funcionamento**:
- Avalia fórmulas no momento do INSERT/UPDATE
- Suporta ordenação topológica (dependências entre colunas)
- Modo strict: falha a operação se a fórmula der erro
- Modo non-strict: continua com NULL se a fórmula falhar

**Exemplos de Fórmulas**:
```javascript
// Coluna 'total' calculada de preço × quantidade
{{price}} * {{quantity}}

// Coluna 'full_name' concatenando nome e sobrenome
{{first_name}} + " " + {{last_name}}

// Coluna 'tax' calculando imposto sobre valor
{{amount}} * 0.18

// Dependência encadeada
// desconto = {{valor}} * 0.10
// total = {{valor}} - {{desconto}}  → total depende de desconto
```

**Resposta em caso de falha (strict mode)**:
```json
{
  "error": "Computed column 'total' formula error: division by zero"
}
```

**Onde está implementado**:
- `backend/internal/controllers/data.go` - `HandlePostgrest()` (linhas 614-641)
- `backend/internal/controllers/data.go` - `InsertRows()` (linhas 270-307)
- `backend/internal/controllers/data.go` - `UpdateRows()` (linhas 392-427)
- `backend/internal/services/computed.go` - Serviço de avaliação de fórmulas

---

### 4. RLS CONTEXT APPLICATION (Row Level Security)

**Propósito**: Aplicar o contexto de segurança PostgreSQL para que as políticas RLS funcionem corretamente.

**Funcionamento**:
- Configura a role do usuário via `SET LOCAL ROLE`
- Define JWT claims como variáveis de sessão PostgreSQL
- Todas as queries subsequentes executam com este contexto

**Variáveis Configuradas**:
```sql
SET LOCAL ROLE authenticated;                    -- ou anon, service_role
SET LOCAL "request.jwt.claim.sub" = 'user-uuid';
SET LOCAL "request.jwt.claim.role" = 'authenticated';
SET LOCAL "request.jwt.claim.email" = 'user@example.com';
SET LOCAL statement_timeout = '30000';         -- 30s timeout
```

**Políticas RLS Exemplo**:
```sql
-- Usuários só veem seus próprios registros
CREATE POLICY user_isolation ON orders
    FOR ALL
    TO authenticated
    USING (user_id = current_setting('request.jwt.claim.sub')::uuid);
```

**Onde está implementado**:
- `backend/internal/controllers/data.go` - `ExecuteMultiStatementSQL()` (linhas 1777-1816)

---

### 5. SQL EXECUTION (PostgreSQL with RLS)

**Propósito**: Executar a query final no PostgreSQL com todo o contexto de segurança aplicado.

**Funcionamento**:
- Executa em transação (BEGIN/COMMIT/ROLLBACK)
- Todos os comandos anteriores (RLS setup) estão ativos
- PostgreSQL aplica automaticamente as políticas RLS
- Retorna resultado da última query SELECT

**Exemplo de Execução**:
```go
// SQL gerado pelo sistema
INSERT INTO public.orders (user_id, product, total)
VALUES ('uuid', 'Laptop', 1500.00)
RETURNING *;

// Executa com contexto RLS:
// - Role: authenticated
// - JWT sub: user-123
// - PostgreSQL aplica políticas RLS automaticamente
```

**Onde está implementado**:
- `backend/internal/controllers/data.go` - `ExecuteMultiStatementSQL()` (linhas 1746-1865)

---

### 6. AUTOMATION INTERCEPTORS (Sequestro de Query)

**Propósito**: Permitir que automações interceptem e modifiquem a resposta da API após a execução no banco.

**Funcionamento**:
- Executa após o SQL (dados já estão no banco)
- Pode modificar, enriquecer ou até remover dados da resposta
- Útil para: envio de emails, notificações, integrações externas
- Trigger types: `API_INTERCEPT`, `AFTER_INSERT`, `AFTER_UPDATE`

**Casos de Uso**:
```javascript
// Após criar pedido, enviar email de confirmação
// Após atualizar status, notificar webhook externo
// Enriquecer resposta com dados calculados em tempo real
```

**IMPORTANTE**: 
- Não impede a persistência (dados já foram salvos)
- Modifica apenas o que o cliente recebe de volta
- Pode executar ações paralelas (emails, webhooks)

**Onde está implementado**:
- `backend/internal/controllers/data.go` - `HandlePostgrest()` (linhas 697-717)
- `backend/internal/controllers/data.go` - `InsertRows()` (linhas 322-335)
- `backend/internal/controllers/data.go` - `UpdateRows()` (linhas 435-448)
- `backend/internal/services/compiled_automation.go` - Serviço de automação

---

### 7. PRIVACY MASKING (Data Obfuscation)

**Propósito**: Ocultar ou mascarar dados sensíveis na resposta para o cliente.

**Funcionamento**:
- Aplica máscaras baseadas no `maskLevel` de cada coluna
- Executa como última camada (após automações)
- Não afeta dados no banco, apenas a resposta

**Níveis de Mask**:
```
unmasked    → Dados retornados normalmente
hide        → Campo é removido completamente da resposta
blur        → Valor é censurado (ex: "***")
mask        → Aplica máscara parcial (ex: "***-**-6789" para SSN)
semi-mask   → Revela parte do dado (ex: "j***@gmail.com")
encrypt     → Dado criptografado na resposta
```

**Exemplos**:
```javascript
// Sem máscara
{ "ssn": "123-45-6789", "email": "john@example.com" }

// Com máscara (mask + semi-mask)
{ "ssn": "***-**-6789", "email": "j***@example.com" }

// Com hide
{ "email": "j***@example.com" }  // ssn removido completamente
```

**Onde está implementado**:
- `backend/internal/controllers/data.go` - `HandlePostgrest()` (linhas 755-758)
- `backend/internal/controllers/data.go` - `InsertRows()` (linhas 337-339)
- `backend/internal/controllers/data.go` - `UpdateRows()` (linhas 450-452)
- `backend/internal/controllers/data.go` - `ApplyMaskingTier()` (função dedicada)

---

## Mapeamento de Endpoints para Camadas

### Endpoints Proprietários (Cascata Native)

| Endpoint | Método | Camadas Aplicadas |
|----------|--------|-------------------|
| `/tables/:tableName/rows` | POST | 1→2→3→4→5→6→7 |
| `/tables/:tableName` | PUT/PATCH | 1→2→3→4→5→6→7 |
| `/tables/:tableName/rows` | DELETE | 4→5→6→7 |
| `/tables/:tableName` | GET | 4→5→7 |

### Endpoints REST/PostgREST (Compatibilidade)

| Endpoint | Método | Camadas Aplicadas |
|----------|--------|-------------------|
| `/rest/v1/:tableName` | POST | 1→2→3→4→5→6→7 |
| `/:tableName` | POST | 1→2→3→4→5→6→7 |
| `/rest/v1/:tableName` | PATCH/PUT | 1→2→3→4→5→6→7 |
| `/:tableName` | PATCH/PUT | 1→2→3→4→5→6→7 |
| `/rest/v1/:tableName` | DELETE | 4→5→6→7 |
| `/:tableName` | DELETE | 4→5→6→7 |
| `/rest/v1/:tableName` | GET | 4→5→7 |
| `/:tableName` | GET | 4→5→7 |

> **NOTA**: Antes das correções, os endpoints REST (`/rest/v1/*` e `/*`) **NÃO** passavam pelas camadas 1, 2, 3 e 6. Agora todos os endpoints passam por TODAS as camadas de segurança.

---

## Configuração das Camadas

### Format Pattern (Camada 1)

**Via TableCreatorDrawer (Frontend)**:
```typescript
// Coluna com preset de formato
{
  name: "email",
  type: "text",
  formatPreset: "email",  // ou "custom" + formatPattern
  description: "User email"
}

// Gera SQL:
// COMMENT ON COLUMN public.users.email IS 'User email||FORMAT:email';
```

**Via SQL direto**:
```sql
COMMENT ON COLUMN public.users.ssn IS 'Social Security||FORMAT:^\d{3}-?\d{2}-?\d{4}$';
```

### Locked Columns (Camada 2)

**Via API de Metadata**:
```json
POST /api/projects/:slug/metadata/locked-columns
{
  "table": "orders",
  "column": "created_at",
  "level": "immutable"
}
```

**Ou via TableCreatorDrawer**:
- Clicar no ícone de cadeado (🔒) na coluna
- Selecionar nível de proteção

### Computed Columns (Camada 3)

**Via API**:
```json
POST /api/projects/:slug/metadata/computed-columns
{
  "table": "orders",
  "column": "total",
  "formula": "{{price}} * {{quantity}}",
  "return_type": "numeric",
  "strict_mode": true
}
```

**Via TableCreatorDrawer**:
- Clicar no ícone de calculadora (🧮)
- Digitar fórmula usando sintaxe `{{column_name}}`

### RLS Policies (Camada 4)

**Via Security Panel (Frontend)**:
- Navegar até "RLS Policies"
- Criar nova política
- Definir: tabela, operação, role, condição USING, condição WITH CHECK

**Via SQL**:
```sql
ALTER TABLE orders ENABLE ROW LEVEL SECURITY;

CREATE POLICY user_orders ON orders
    FOR ALL
    TO authenticated
    USING (user_id = current_setting('request.jwt.claim.sub')::uuid)
    WITH CHECK (user_id = current_setting('request.jwt.claim.sub')::uuid);
```

### Privacy Masking (Camada 7)

**Via API**:
```json
POST /api/projects/:slug/metadata/masked-columns
{
  "table": "users",
  "column": "ssn",
  "level": "mask"
}
```

**Via TableCreatorDrawer**:
- Clicar no ícone de máscara (👁️)
- Selecionar nível de ofuscação

---

## Fluxo de Falha (Failure Handling)

Cada camada pode falhar e interromper o fluxo:

```
┌─────────────────────────────────────────────────────────────┐
│ 1. FORMAT VALIDATION                                        │
│    Falha? → HTTP 400 + mensagem de erro de validação        │
│              (nenhum dado toca o banco)                     │
└─────────────────────────────────────────────────────────────┘
                              ↓ (sucesso)
┌─────────────────────────────────────────────────────────────┐
│ 1.1. AUTOMATION INTERCEPTORS /Sequestro de Request          │
│    Falha? → HTTP 500 + erro da automação                    │
│(dados ainda não foram salvos - serve para omplementar       |
|   infomaaçẽos usando automações por exemplo)                │
└─────────────────────────────────────────────────────────────┘
                              ↓ (sucesso)
┌─────────────────────────────────────────────────────────────┐
│ 2. LOCKED COLUMNS                                           │
│    Falha? → Nunca falha (apenas remove silenciosamente)     │
│              (log de auditoria gerado)                      │
└─────────────────────────────────────────────────────────────┘
                              ↓ (sucesso)
┌─────────────────────────────────────────────────────────────┐
│ 3. COMPUTED COLUMNS                                         │
│    Falha? → HTTP 400 + erro da fórmula (se strict_mode)     │
│              ou continua com NULL (se non-strict)           │
└─────────────────────────────────────────────────────────────┘
                              ↓ (sucesso) SOMENTE AGORA QUE O POSTGRES  ENTRA EM AÇÃO DE VERDADE.
┌─────────────────────────────────────────────────────────────┐
│ 4. RLS CONTEXT                                              │
│    Falha? → HTTP 500 + erro de configuração RLS             │
└─────────────────────────────────────────────────────────────┘
                              ↓ (sucesso)
┌─────────────────────────────────────────────────────────────┐
│ 5. SQL EXECUTION                                            │
│    Falha? → HTTP 400/500 + erro do PostgreSQL               │
│              (RLS violations, constraints, etc)             │
└─────────────────────────────────────────────────────────────┘
                              ↓ (sucesso)
┌─────────────────────────────────────────────────────────────┐
│ 6. AUTOMATION INTERCEPTORS                                  │
│    Falha? → HTTP 500 + erro da automação                    |
│              (dados JÁ foram salvos - cuidado!)             |
|  Origem do Gatilho "Evento de Banco"                        |
└─────────────────────────────────────────────────────────────┘
                              ↓ (sucesso)
┌─────────────────────────────────────────────────────────────┐
│ 7. PRIVACY MASKING                                          │
│    Falha? → Nunca falha (best-effort masking)               │
└─────────────────────────────────────────────────────────────┘
                              ↓
                       HTTP 200/201
```

---

## Auditoria e Logging

Todas as camadas de segurança geram logs para auditoria:

```
[HandlePostgrest] Stripped locked column 'created_at' from POST
[UpdateRows DEBUG] table=orders, pkCol=id, pkExists=true
[ComputedService] Evaluating formula: {{price}} * {{quantity}}
[ExecuteMultiStatementSQL] RLS setup for role: authenticated
[Automation] Executing interceptors for table: orders, operation: INSERT
[Masking] Applied mask to column: ssn
```

---

## Checklist de Segurança para Desenvolvedores

Antes de adicionar novos endpoints de escrita, verifique:

- [ ] O endpoint chama `HandlePostgrest()` ou `InsertRows()`/`UpdateRows()`?
- [ ] Se não, todas as 7 camadas de segurança estão implementadas manualmente?
- [ ] Format Pattern Validation está aplicada?
- [ ] Locked Columns stripping está aplicado?
- [ ] Computed Columns evaluation está aplicada?
- [ ] RLS Context está sendo configurado antes do SQL?
- [ ] Automation Interceptors estão sendo chamados após execução?
- [ ] Privacy Masking está sendo aplicado na resposta?
- [ ] Testes cobrem tentativas de burlar cada camada?

---

## Conclusão

Este sistema de 7 camadas garante que:

1. **Dados válidos**: Apenas dados no formato correto passam
2. **Imutabilidade respeitada**: Colunas locked não podem ser modificadas
3. **Cálculos automáticos**: Fórmulas são sempre avaliadas
4. **Isolamento total**: RLS garante que usuários só vejam seus dados
5. **Automações ativas**: Todas as integrações são executadas
6. **Privacidade protegida**: Dados sensíveis são mascarados

**Nenhum dado pode tocar o PostgreSQL sem passar por todas estas validações.**
