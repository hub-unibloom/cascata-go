# Vault ↔ Edge Functions Integration Walkthrough

We have integrated the Cascata Vault (`system.project_secrets`) with the Edge Functions runtime. This allows Edge Functions to automatically access secrets via `env.SECRET_NAME` inside their Javascript runtime.

## Summary of Changes

### 1. Camada de Serviço (Vault)
- **Arquivo**: [vault.go](file:///home/cocorico/Documentos/proejetos/cascata%20go/backend/internal/services/vault.go)
- **Modificações**:
  - Adicionado o import `"time"` para gerenciar o tempo de vida do cache (TTL).
  - Implementado o método `ResolveAllRuntimeSecrets(ctx, projectSlug)`:
    - Consulta no banco todos os segredos do projeto (`type <> 'folder'`).
    - Filtra no código Go usando a função padrão `NormalizeVaultPolicy` para garantir que apenas segredos com as políticas `runtime` ou `exportable` sejam revelados.
    - Decripta os valores usando o serviço criptográfico soberano `CryptoSvc.Decrypt()`.
    - Implementa cache-aside usando o DragonflyDB com a chave `vault:runtime:{project_slug}` e um TTL de **45 segundos**.
  - Implementado a função global `InvalidateVaultRuntimeCache(ctx, projectSlug)` para invalidar o cache da chave acima no DragonflyDB.

### 2. Camada de Controller (Edge Functions)
- **Arquivo**: [edge.go](file:///home/cocorico/Documentos/proejetos/cascata%20go/backend/internal/controllers/edge.go)
- **Modificações**:
  - Injetado `vaultSvc *services.VaultService` no `EdgeController` e no construtor `NewEdgeController`.
  - Adicionado import `"log"`.
  - Atualizado o método `InvokeFunction` para recuperar os segredos do Vault e mesclá-los com as `env_vars` locais da função:
    - Busca segredos por meio de `c.vaultSvc.ResolveAllRuntimeSecrets`. Se falhar, registra um log de aviso mas não impede a execução da função (tolerância a falhas).
    - Executa o merge respeitando a **precedência local**: se houver colisão de nomes, a variável de ambiente local da Edge Function sobrescreve o valor do Vault.

### 3. Invalidação de Cache (Secrets)
- **Arquivo**: [secrets.go](file:///home/cocorico/Documentos/proejetos/cascata%20go/backend/internal/controllers/secrets.go)
- **Modificações**:
  - Nos endpoints `SetSecret` (após INSERT de segredo com sucesso) e `DeleteSecret` (após DELETE do segredo com sucesso), adicionada a chamada para `services.InvalidateVaultRuntimeCache(r.Context(), ctx.Project.Slug)`. Isso garante invalidação reativa e imediata no DragonflyDB.

### 4. Inicialização de Dependências (Main)
- **Arquivo**: [main.go](file:///home/cocorico/Documentos/proejetos/cascata%20go/backend/cmd/server/main.go)
- **Modificações**:
  - Atualizada a instanciação do `EdgeController` para receber `services.NewVaultService(&cryptoSvc)`.

### 5. Guia Visual no Front-end (UI)
- **Arquivo**: [ProjectVaultModal.tsx](file:///home/cocorico/Documentos/proejetos/cascata%20go/frontend/components/ProjectVaultModal.tsx)
- **Modificações**:
  - Adicionado um bloco de texto explicativo e reativo abaixo do seletor de "Release Policy" no modal de criação de segredos do Vault. Ele descreve explicitamente para o desenvolvedor o comportamento de cada política em tempo de execução (especificando que a política `runtime` torna o segredo disponível em Edge Functions via `env.NAME` e em Automações, protegendo contra vazamentos na interface).


---

## Plano de Verificação Manual (Na VPS / Ambiente de Testes Externo)

Como a máquina local não possui o projeto instalado ou em execução (conforme as Leis 1 e 2 definidas pelo usuário), a validação deverá ser feita externamente. A seguir, está o roteiro de testes detalhado:

1. **Invocação Básica de Segredo**:
   - Crie um segredo no painel/Vault com nome `teste` e valor desejado.
   - Deploy de uma Edge Function com código semelhante a:
     ```javascript
     export default async function(req) {
       return { valor: env.teste || 'NAO_ENCONTRADO' };
     }
     ```
   - Invoque a função e confirme se ela retorna o valor correto do Vault.

2. **Precedência Local**:
   - Na configuração da mesma Edge Function, defina uma variável de ambiente local com nome `teste` e valor `"valor_local"`.
   - Invoque a função novamente e confirme se o valor retornado é o `"valor_local"` (a variável local da função deve sobrescrever o Vault).

3. **Invalidação Reativa e Cache**:
   - Modifique o valor do segredo `teste` no Vault.
   - Invoque a Edge Function imediatamente em seguida. O valor retornado deve ser o novo valor atualizado (confirmando o funcionamento do `InvalidateVaultRuntimeCache` no `SetSecret`).
   - delete o segredo no Vault.
   - Invoque a função imediatamente. O valor deve sumir e cair no fallback (`'NAO_ENCONTRADO'`), confirmando o funcionamento do `InvalidateVaultRuntimeCache` no `DeleteSecret`.
