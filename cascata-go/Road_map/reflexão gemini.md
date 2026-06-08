Edited branching_system_audit.md
Edited branching_system_audit.md
Edited branching_system_audit.md
Edited branching_system_audit.md
Edited branching_system_audit.md
Edited branching_system_audit.md
Edited branching_system_audit.md
Edited branching_system_audit.md
Edited branching_system_audit.md
Edited pool_iface.go
Edited pool_iface.go
Edited pool_iface.go
Edited pool_iface.go
Edited go.mod
Edited dryrun.go
Edited snapshot.go
Edited snapshot.go
Edited diff_tables.go
Edited diff_tables.go
Edited engine.go
Edited engine.go
Edited engine.go
Edited types.go
Edited types.go
Edited main.go
Edited main.go
Edited branch.go
Edited branch.go
Edited branch.go
Edited branch.go
Edited branch.go
Edited manager.go
Edited manager.go
Edited manager.go
Edited types.go
Edited App.tsx
Edited App.tsx
Viewed branching_system_audit.md:194-220
Viewed project.go:168-230

Que excelente progresso! Resolver os GAPs 1 a 4 e 6 a 9 de uma vez limpa todo o "barulho" da infraestrutura e deixa o caminho livre para a grande decisão da **Fase 5**.

O GAP #5 é o coração do sistema de branching. A limitação atual de tentar guardar "schema como metadados JSON" é um beco sem saída. Tentar fazer o Studio (que foi feito para ler de um banco real) interpretar JSON para simular um banco é o caminho para criar um sistema lento, cheio de bugs e que nunca terá 100% de fidelidade ao PostgreSQL.

Eu refleti profundamente sobre a arquitetura do Cascata e a experiência de uso que você quer entregar. Aqui está a minha recomendação de arquitetura "Sovereign/Enterprise" para resolver isso de forma elegante, escalável e com impacto quase zero de refatoração no Diff Engine.

---

### A Proposta de Arquitetura: Unified Shadow DBs ("Thin vs Fat Clones")

A regra de ouro é: **No Cascata, TODA branch ativa precisa ter um banco de dados real por trás.** O frontend e o DiffEngine precisam conversar com o Postgres de verdade. A diferença entre uma branch de `environment` e de `data` será apenas a **estratégia de clonagem**.

#### 1. Environment Branches (Thin Clones / Schema-Only)
- **A Lógica:** Quando o usuário cria uma branch de ambiente (`feat-ui-nova`), o backend cria um banco de dados vazio e copia **apenas a estrutura (schema)** do banco principal, ignorando todos os dados.
- **A Magia Técnica:**
  ```sql
  CREATE DATABASE cascata_projeto_feat_ui;
  -- E por baixo dos panos o Go faz:
  -- pg_dump --schema-only -d cascata_projeto | psql -d cascata_projeto_feat_ui
  ```
- **Por que é Genial?**
  - **Instantâneo & Leve:** Criar um banco só com o schema leva milissegundos e consome quase zero de disco (só o peso dos catálogos do PG, ~5MB), não importa se o banco principal tem 100GB.
  - **Isolamento Real:** É um Postgres real. O usuário "Entra" na branch no Studio e todas as abas (Tables, RLS, Functions) funcionam perfeitamente.
  - **Seguro para Design:** Como não tem os dados de produção, é perfeito para o desenvolvedor brincar, alterar relacionamentos e desenhar a arquitetura sem medo.

#### 2. Data Branches (Fat Clones / Full Copy)
- **A Lógica:** O comportamento clássico de um clone pesado, ideal para testes de QA e debugging com dados reais.
- **A Magia Técnica:** 
  ```sql
  CREATE DATABASE cascata_projeto_teste_pesado TEMPLATE cascata_projeto;
  ```
- **Por que manter?** Copia tudo bit-a-bit (schema + dados). Demora um pouco mais se o banco for gigante, e consome disco equivalente, mas fornece um ambiente de staging perfeito (daí a necessidade do TTL para não estourar o disco).

---

### Como isso destrava o "Access" e o DiffEngine? (A Sinergia)

Se adotarmos o modelo de **Shadow DBs**, o quebra-cabeça se encaixa perfeitamente de ponta a ponta:

1. **O Roteamento Mágico (`GetProjectPool`)**:
   Ao invés de tentar simular schemas, o `GetProjectPool(project, env)` passa a usar o `env`.
   - Se `env == "live"` → Conecta em `cascata_slug`.
   - Se `env == "feat-ui"` → Conecta em `cascata_slug_feat_ui`.

2. **O Botão "Access" (Frontend)**:
   - Quando o usuário clica em "Access" na modal de branches, você simplesmente altera uma variável global (ou LocalStorage) chamada `currentEnv`.
   - As chamadas da API do Studio passam a enviar um header `X-Cascata-Env: feat-ui`.
   - O Middleware do Go lê esse header e entrega o pool daquele banco shadow.
   - O Studio pisca, recarrega, e **boom**: o usuário está navegando e alterando a arquitetura isolada, usando 100% da interface que já existe, sem mudar 1 linha de código nos componentes de UI!

3. **O DiffEngine Trabalhando como Desenhado**:
   Quando o usuário decide fazer o merge (Deploy), o DiffEngine já sabe o que fazer:
   - `SourceConn`: conecta no banco `cascata_slug_feat_ui`.
   - `TargetConn`: conecta no banco `cascata_slug`.
   - Ele compara as tabelas de um banco real com o outro, gera os `ALTER TABLE`, aplica no Target e depois dropa o banco da Source.

### Qual é o Papel da Tabela `system.branches` agora?
Ela deixa de tentar guardar "códigos SQL quebrados" naquelas colunas de texto/jsonb, e passa a ser uma tabela elegante de **Tracking e Histórico**. Ela só vai precisar saber:
- O nome do banco atrelado a ela (`db_name`).
- Quem criou, quando expira.
- E, após o deploy, guardar o JSON do log de merge em `branch_deploys` para auditoria.

### Resumo do Plano de Batalha (Fase 5)
1. Garantir que o ambiente do Cascata consiga rodar `pg_dump` e `pg_restore/psql` internamente para clonar schemas rapidamente (ou usar código Go para iterar e criar o DDL).
2. Atualizar o `CreateBranch` para, caso seja "environment", criar um banco vazio e injetar o DDL do main.
3. Atualizar o Middleware/Services para que as rotas aceitem um "Env Context" que direciona para a connection pool correta.
