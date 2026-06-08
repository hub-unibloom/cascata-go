# Análise Sistemática de Lixos e Resíduos no Nexus Workflow Delete

## Data: 2026-05-23
## Contexto: Análise de cleanup incompleto ao deletar workflows/automações no Nexus

---

## Resumo Executivo

A deleção de workflows no sistema Nexus atualmente **NÃO realiza um cleanup completo**, deixando múltiplos resíduos (orphaned resources) em diferentes camadas do sistema. O método `DeleteAutomation` em `internal/controllers/data.go` apenas remove o registro principal da tabela `system.nexus_automations` ou `system.automations`, mas não limpa:

1. **Referências orfãs no banco de dados** (tabelas com FKs ON DELETE SET NULL)
2. **Webhook receivers** que apontam para automações deletadas
3. **Cache do Dagonfly (Redis)** (planos compilados, cache de automações, contadores de falha)
4. **Chaves de auto-disable** no Dagonfly (Redis)
5. **Logs de execução** com automation_id NULL (perda de rastreabilidade)
6. **Alertas** com automation_id NULL (perda de contexto de falhas)

---

## 1. Estrutura de Tabelas Relacionadas a Workflows

### Tabela Principal
- **`system.nexus_automations`** - Tabela canônica do Nexus v0
  - Colunas: id, tenant_id, name, hook_type, graph_json, is_active, status, branch_name, etc.
  - **DELETE ATUAL**: Simples DELETE da tabela

### Tabelas com Dependências (Foreign Keys)

#### 1.1 Tabelas com ON DELETE SET NULL (PROBLEMA CRÍTICO)

**`system.nexus_automation_alerts`** (migration 055)
```sql
automation_id UUID REFERENCES system.nexus_automations(id) ON DELETE SET NULL
```
- **Problema**: Ao deletar automação, alerts ficam com `automation_id = NULL`
- **Impacto**: Perda de contexto histórico - alertas de falhas ficam órfãos
- **Lixos**: Registros de alertas sem referência à automação que os gerou

**`system.nexus_execution_log`** (migration 055)
```sql
automation_id UUID REFERENCES system.nexus_automations(id) ON DELETE SET NULL
```
- **Problema**: Ao deletar automação, logs de execução ficam com `automation_id = NULL`
- **Impacto**: Perda de rastreabilidade - impossível associar execuções históricas à automação
- **Lixos**: Milhares de registros de execução sem contexto

#### 1.2 Tabelas com ON DELETE CASCADE (OK)

**`system.automation_runs`** (migration 034)
```sql
automation_id UUID REFERENCES system.automations(id) ON DELETE CASCADE
```
- **Status**: OK - deleta em cascata
- **Nota**: Esta é a tabela antiga (legado), não a do Nexus v0

#### 1.3 Tabelas sem Foreign Key (PROBLEMA CRÍTICO)

**`system.webhook_receivers`** (migration 039)
```sql
target_type TEXT NOT NULL, -- 'AUTOMATION', 'TABLE'
target_id TEXT NOT NULL, -- Automation ID or Table Name
```
- **Problema**: NÃO tem FK para `system.nexus_automations`
- **Impacto**: Webhook receivers continuam ativos apontando para automações deletadas
- **Lixos**: Receivers que tentam disparar automações inexistentes
- **Comportamento**: Erros silenciosos ou falhas de execução

**`system.webhooks`** (migration 001 - legado)
```sql
-- Tabela antiga, sem referência direta a automações
```
- **Problema**: Pode haver webhooks configurados para disparar automações deletadas
- **Impacto**: Webhooks tentam disparar automações inexistentes

---

## 2. Análise do Código de Deleção Atual

### Arquivo: `internal/controllers/data.go` (linhas 3929-3995)

```go
func (d *DataController) DeleteAutomation(w http.ResponseWriter, r *http.Request) {
    // ... auth checks ...
    
    id := chi.URLParam(r, "id")
    branchName := types.GetBranchName(r.Context())
    
    // Nexus v0: Deletar da tabela canônica
    result, err := services.SystemPool.Exec(r.Context(),
        "DELETE FROM system.nexus_automations WHERE id=$1 AND tenant_id=$2 AND branch_name=$3", 
        id, ctx.Project.Slug, branchName)
    
    // Fallback para tabela antiga
    if rowsAffected == 0 {
        result, err = services.SystemPool.Exec(r.Context(),
            "DELETE FROM system.automations WHERE id=$1", id)
    }
    
    // Invalida o cache para remover a automação deletada do motor Nexus
    if d.NexusSvc != nil {
        d.NexusSvc.InvalidateCache(r.Context(), ctx.Project.Slug)
    }
}
```

### Problemas Identificados

1. **Apenas deleta o registro principal** - Não limpa dependências
2. **Invalida cache por tenant** - Não remove cache específico da automação
3. **Não limpa webhook receivers** - Receivers continuam ativos
4. **Não limpa alertas orfãos** - Alerts ficam com automation_id NULL
5. **Não limpa execution logs orfãos** - Logs ficam com automation_id NULL
6. **Não limpa cache de planos compilados** - planCache em memória
7. **Não limpa contadores de falha** - FailureCounterPrefix no Dagonfly (Redis)
8. **Não limpa chaves de auto-disable** - nexus:disabled:{automation_id} no Dagonfly (Redis)

---

## 3. Cache do Dagonfly (Redis) - Resíduos Não Limpados

### 3.1 Cache de Automações (L2 - Dragonfly)

**Prefix**: `nexus:automations:`

**Chaves criadas em `nexus_hook_resolver.go`:**
```go
// Cache por tenant+hook+method+route
cacheKey := fmt.Sprintf("%s%s:%s:%s:%s", AutomationCachePrefix, tenantBranchKey, hookType, method, route)

// Cache por ID
cacheKey := fmt.Sprintf("%sid:%s", AutomationCachePrefix, automationID)
```

**Problema**: Ao deletar automação, estas chaves NÃO são removidas
- Chaves de cache negativo (`__none__`) podem persistir
- Chaves de cache positivo continuam servindo automações deletadas

### 3.2 Cache de Planos Compilados (L1 + L2)

**Prefix**: `nexus:plan:`

**Chaves criadas em `nexus_hook_resolver.go`:**
```go
planKey := fmt.Sprintf("%s%s:%d", PlanCachePrefix, automation.ID, automation.UpdatedAt.UnixNano())
```

**Armazenamento:**
- **L1**: `sync.Map` em memória (`planCache`)
- **L2**: Dragonfly Redis com TTL de 24h

**Problema**: Ao deletar automação:
- L1 cache (`planCache`) NÃO é limpo
- L2 cache (Dagonfly (Redis)) pode persistir por até 24h
- Planos compilados continuam na memória do worker

### 3.3 Contadores de Falha

**Prefix**: `nexus:failures:`

**Chaves criadas em `nexus_hook_resolver.go`:**
```go
key := fmt.Sprintf("%s%s", FailureCounterPrefix, automationID)
h.RDB.Incr(ctx, key)
h.RDB.Expire(ctx, key, FailureWindowDuration) // 10 minutos
```

**Problema**: Ao deletar automação, contadores de falha NÃO são removidos
- Chaves persistem por até 10 minutos após TTL
- Se automação for recriada com mesmo ID, herda contador antigo

### 3.4 Chaves de Auto-Disable

**Prefix**: `nexus:disabled:`

**Chaves criadas em `nexus_hook_resolver.go`:**
```go
disableKey := fmt.Sprintf("nexus:disabled:%s", automationID)
h.RDB.Set(ctx, disableKey, "1", FailureCounterTTL) // 10 minutos
```

**Problema**: Ao deletar automação, chaves de auto-disable NÃO são removidas
- Chaves persistem por até 10 minutos
- Se automação for recriada com mesmo ID, pode já estar desabilitada

---

## 4. Webhook Receivers - Resíduos Críticos

### Tabela: `system.webhook_receivers`

**Estrutura:**
```sql
CREATE TABLE system.webhook_receivers (
    id UUID PRIMARY KEY,
    project_slug TEXT NOT NULL,
    name TEXT NOT NULL,
    path_slug TEXT NOT NULL,
    target_type TEXT NOT NULL, -- 'AUTOMATION', 'TABLE'
    target_id TEXT NOT NULL, -- Automation ID or Table Name
    is_active BOOLEAN DEFAULT true,
    ...
)
```

### Problema

**NÃO há foreign key** para `system.nexus_automations`. O campo `target_id` é um TEXT livre.

**Cenário de lixo:**
1. Usuário cria webhook receiver com `target_type = 'AUTOMATION'` e `target_id = 'uuid-da-automacao'`
2. Usuário deleta a automação
3. Webhook receiver continua ativo (`is_active = true`)
4. Requisições chegam no endpoint `/api/webhooks/in/:project_slug/:path_slug`
5. Sistema tenta disparar automação inexistente
6. **Resultado**: Erro silencioso ou falha de execução

### Código Afetado

**Arquivo**: `internal/controllers/webhook.go` (linha 295-307)

```go
err = services.SystemPool.QueryRow(r.Context(),
    `SELECT id, ...
     FROM system.nexus_automations
     WHERE tenant_id   = $1
       AND branch_name = $2
       AND hook_type   = 'WEBHOOK'
       AND graph_json->'nodes'->0->'config'->>'path_slug' = $3
       AND is_active   = true`,
    projectSlug, branchName, pathSlug,
).Scan(&receiver.ID, ...)
```

**Problema**: Query busca automação por `path_slug` no `graph_json`, não por `target_id` de `webhook_receivers`. Isso indica uma **inconsistência de design**:

- A tabela `webhook_receivers` tem `target_id` mas não é usada
- O sistema busca automação diretamente em `nexus_automations` pelo `path_slug` no JSON
- **Conclusão**: `webhook_receivers` parece ser legado ou parcialmente implementado

---

## 5. Sistema de Branches - Resíduos Potenciais

### Tabela: `system.branches` (migration 060)

```sql
CREATE TABLE system.branches (
    ...
    automations_json JSONB, -- Conteúdo da branch (para branches de ambiente)
    ...
)
```

### Problema

A coluna `automations_json` pode conter snapshots de automações por branch.

**Cenário de lixo:**
1. Usuário cria branch de ambiente com automação X
2. Snapshot da automação X é salvo em `branches.automations_json`
3. Usuário deleta automação X do main
4. Branch ainda contém referência à automação deletada
5. Se branch for mergeado, pode tentar restaurar automação deletada

---

## 6. Mapeamento Completo de Resíduos por Camada

### Camada 1: Banco de Dados (PostgreSQL)

| Tabela | Tipo de FK | Comportamento Delete | Resíduo | Impacto |
|--------|-----------|---------------------|---------|---------|
| `system.nexus_automations` | - | DELETE direto | - | OK |
| `system.nexus_automation_alerts` | ON DELETE SET NULL | automation_id → NULL | Alerts orfãos | Perda de contexto |
| `system.nexus_execution_log` | ON DELETE SET NULL | automation_id → NULL | Logs orfãos | Perda de rastreabilidade |
| `system.automation_runs` | ON DELETE CASCADE | DELETE em cascata | - | OK |
| `system.webhook_receivers` | Sem FK | Nenhuma ação | Receivers ativos | Erros de execução |
| `system.webhooks` | Sem FK | Nenhuma ação | Webhooks ativos | Erros de execução |
| `system.branches` | Sem FK (JSON) | Nenhuma ação | Snapshots em JSON | Inconsistência de merge |

### Camada 2: Cache (Dagonfly (Redis))

| Cache Type | Prefix | Chave | TTL | Cleanup Atual | Resíduo |
|------------|--------|-------|-----|---------------|---------|
| Automações (L2) | `nexus:automations:` | `{tenant}:{hook}:{method}:{route}` | 5 min | InvalidateCache por tenant | Chaves específicas persistem |
| Automações por ID (L2) | `nexus:automations:` | `id:{uuid}` | 5 min | Nenhum | Chaves persistem até TTL |
| Planos compilados (L1) | - | `{id}:{timestamp}` | Sem TTL | Nenhum | Persiste na memória |
| Planos compilados (L2) | `nexus:plan:` | `{id}:{timestamp}` | 24h | Nenhum | Chaves persistem até TTL |
| Contadores de falha | `nexus:failures:` | `{uuid}` | 10 min | Nenhum | Chaves persistem até TTL |
| Auto-disable | `nexus:disabled:` | `{uuid}` | 10 min | Nenhum | Chaves persistem até TTL |

### Camada 3: Memória (Go)

| Cache Type | Estrutura | Cleanup Atual | Resíduo |
|------------|-----------|---------------|---------|
| Planos compilados | `sync.Map` (planCache) | Nenhum | Planos persistem em memória |
| Contadores atômicos | `atomic.Uint64` (traceCounter) | Nenhum | Sem impacto (apenas contador) |

---

---

## 9. Conclusão

O sistema atual de deleção de workflows no Nexus é **incompleto e propenso a acumular resíduos**. Os principais problemas são:

1. **Foreign Keys com ON DELETE SET NULL** - Criam registros orfãos em vez de deletar
2. **Ausência de Foreign Keys** - Webhook receivers não têm referência formal às automações
3. **Cache não limpo** - Dagonfly (Redis) e memória acumulam dados obsoletos
4. **InvalidateCache genérico** - Invalida por tenant, não por automação específica

**Impacto estimado**: Em um sistema com uso moderado, é possível acumular milhares de registros orfãos em poucas semanas, resultando em:
- Crescimento desnecessário do banco de dados
- Erros silenciosos na execução de webhooks
- Perda de rastreabilidade histórica
- Inconsistências no sistema de branches

