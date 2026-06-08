# CASCATA — Relatório Completo e Abrangente

## 1. O QUE É O CASCATA

O Cascata é uma **plataforma BaaS (Backend as a Service) auto-hospedada e multi-tenant** com isolamento físico de banco de dados por projeto. Diferente de soluções como Supabase/Firebase (isolamento lógico por schemas), o Cascata cria **um banco PostgreSQL dedicado por projeto** (tenant), garantindo isolamento total de dados.

**Descrição oficial:** *"Plataforma de infraestrutura de dados multi-tenant auto-hospedada e isolamento físico de database com PostgreSQL."*

**Versão atual:** `1.2.0-sovereign` (backend Go)

---

## 2. ARQUITETURA GERAL

### 2.1 Componentes Docker (docker-compose.yml)

```
┌─────────────────────────────────────────────────────────────┐
│                      CASCATA STACK                          │
│                                                             │
│  ┌──────────┐  ┌────────────┐  ┌──────────┐  ┌──────────┐ │
│  │  NGINX   │  │  BACKEND   │  │ FRONTEND │  │ PGBOUNCER│ │
│  │ (Reverse │  │ (Go/Chi)   │  │ (React)  │  │ (Conn    │ │
│  │  Proxy)  │  │ Port 3000  │  │ Vite+SPA │  │  Pool)   │ │
│  └────┬─────┘  └─────┬──────┘  └────┬─────┘  └────┬─────┘ │
│       │              │               │              │       │
│  ┌────┴──────────────┴───────────────┴──────────────┴────┐ │
│  │                   INTERNAL NETWORK                     │ │
│  └────┬──────────────┬───────────────┬──────────────┬────┘ │
│       │              │               │              │       │
│  ┌────┴─────┐  ┌─────┴──────┐  ┌────┴─────┐  ┌────┴─────┐│
│  │PostgreSQL│  │ DRAGONFLY  │  │  QDRANT  │  │  CRYPTO  ││
│  │(Database)│  │ (Redis L2) │  │ (Vector  │  │  ENGINE  ││
│  │          │  │ Cache/Queue│  │  Search) │  │  (Go)    ││
│  └──────────┘  └────────────┘  └──────────┘  └──────────┘│
│                                                             │
│  ┌──────────┐  ┌────────────┐  ┌──────────────────────────┐│
│  │  MinIO   │  │  NGINX     │  │     CERT-CONTROLLER     ││
│  │ (S3 Int) │  │ CONTROLLER │  │   (Let's Encrypt)       ││
│  └──────────┘  └────────────┘  └──────────────────────────┘│
└─────────────────────────────────────────────────────────────┘
```

| Componente | Tecnologia | Propósito |
|-----------|-----------|----------|
| **Backend** | Go + Chi Router | API principal, control plane, data plane |
| **Frontend** | React + Vite + TypeScript | Dashboard administrativo SPA |
| **PostgreSQL** | v15+ | Banco do sistema (`system.*`) + bancos por tenant |
| **PgBouncer** | Transaction pooling | Connection pooling com 10.000 max connections |
| **Dragonfly** | Redis-compatible | Cache L2, filas (streams), rate limiting, sessões |
| **Qdrant** | Vector DB | Busca vetorial para AI/embeddings por projeto |
| **Crypto Engine** | Go microservice | Criptografia AES-256, key management, unseal |
| **MinIO** | S3-compatible | Storage interno (arquivos/uploads) |
| **NGINX** | Reverse proxy | SSL termination, routing, custom domains |
| **NGINX Controller** | Node.js | Recarga dinâmica do NGINX via Docker socket |
| **Cert Controller** | Go | Emissão automática de certificados Let's Encrypt |

### 2.2 Comunicação Interna

- **Unix Domain Sockets** para comunicação backend↔nginx (zero-network hot-path)
- **Dragonfly Streams** para filas assíncronas (webhooks, push, restore)
- **PostgreSQL LISTEN/NOTIFY** para eventos realtime

---

## 3. BOOT SEQUENCE

Quando o backend inicia (`main.go`), a seguinte sequência ocorre:

1. **InitConfig** — Carrega variáveis de ambiente
2. **InitSystemPool** — Conecta ao PostgreSQL do sistema
3. **InitReaper** — Background goroutine que mata pools idle e transações zombie
4. **InitLogging** — Sistema de audit logging assíncrono
5. **InitDragonfly** — Conecta ao Redis/Dragonfly (cache + filas)
6. **UploadWorker.Start** — Worker para upload assíncrono de arquivos grandes
7. **RunMigrations** — Executa todas as 59 migrations SQL em sequência
8. **GetPurgeScheduler** — Scheduler de purge automático de logs
9. **WarmupJWTCache** — Popula cache Dragonfly com JWT secrets de todos os projetos
10. **Auto-Unseal** — Desbloqueia o Crypto Engine se `CASCATA_MASTER_SECRET` existe (5 tentativas)
11. **NexusService.Start** — Inicia o motor de automações (Worker Lane)
12. **GlobalSchemaCache.NexusSvc** — Injeta Nexus no cache de schemas
13. **Router setup** — Registra todas as rotas Chi
14. **HTTP Server** — Escuta na porta 3000 + Unix socket

---

## 4. SISTEMA DE MULTI-TENANCY

### 4.1 Isolamento Físico de Banco

Cada projeto criado no Cascata recebe:
- Um **banco PostgreSQL dedicado** (ex: `cascata_meu_projeto`)
- Schemas internos: `public` (dados do usuário), `auth` (autenticação)
- Uma **collection Qdrant** para embeddings vetoriais
- Um **diretório de storage** isolado em disco/MinIO

### 4.2 Resolução de Projeto (Middleware)

O `ProjectResolver` (`middleware/project.go`, 393 linhas) resolve qual projeto está sendo acessado por 3 estratégias:

| Prioridade | Método | Exemplo |
|-----------|--------|---------|
| 1 | **Custom Domain** | `meuapp.com` → lookup em `system.projects.custom_domain` |
| 2 | **URL Path** | `/api/data/{slug}/...` → extrai slug do path |
| 3 | **API Key** | Header `apikey: xxx` → lookup em `system.projects.anon_key/service_key` |

### 4.3 Pool Dinâmico

Arquivo: `services/pool.go` (393 linhas)

- Pool máximo: **500 conexões ativas** simultâneas
- Idle timeout: **5 minutos** (pools inativos são fechados)
- Reaper: goroutine a cada **20 segundos** que mata pools idle e transações zombie
- Suporta **conexão direta** (bypass PgBouncer) para operações DDL
- Suporta **conexão externa** (BYOD) via connection string customizada

### 4.4 Detecção de Ambiente (Draft/Live)

O middleware detecta se a request é para o ambiente `live` ou `draft`:
- Header `x-cascata-env: draft`
- URL path `/api/data/{slug}/draft/...`

O pool é alocado para `{db_name}_draft` quando o ambiente é draft.

---

## 5. SISTEMA DE AUTENTICAÇÃO

### 5.1 Admin Login (Control Plane)

**Handshake ECDH P-256** para login seguro:
1. Backend gera par de chaves efêmeras ECDH P-256
2. Frontend recebe chave pública + sessionId
3. Frontend criptografa credenciais com shared secret derivado via HKDF
4. Backend decifra e verifica bcrypt hash
5. JWT admin emitido com claims `{role: "admin", type: "dashboard"}`

Suporta **TOTP/2FA** opcional via `CASCATA_OTP_ENABLED`.

### 5.2 Tenant Auth (Sovereign Identity Orchestrator)  (GoTrue-compatible)

Cada projeto tem seu próprio sistema de autenticação (`auth` schema no banco do tenant), funcionando como um **Orquestrador de Identidade Soberano** de nível enterprise.

**Capacidades Core já prontas:**
- **UNIVERSAL LOGIN**: Autenticação via Email, CPF, Telefone, Biometria ou Providers Customizados (completamente concluído).
- **MFA MULTI-CAMADAS**: Suporte a TOTP (RFC 6238), OTP via webhook e Passwordless.
- **GESTÃO DE IDENTIDADES**: Link/unlink de múltiplas identidades (ex: logar com CPF ou Email na mesma conta).
- **SOVEREIGN POLICIES**: Políticas granulares de autenticação resolvidas por prioridade (ex: forçar MFA para logins externos, permitir passwordless na intranet).
- **SEGURANÇA ADAPTATIVA**: Smart Lockout (com limites de tentativas híbridos), Panic Revocation (Lockdown global, por usuário ou provider) e Device Fingerprinting ativo.
- **ANTI-REPLAY & OTP DISPATCH**: Tabela dedicada para evitar reuso de códigos TOTP em até 5 minutos, além de controle delegado, auto-primário ou auto-atual para envios de OTP.

**Estratégias suportadas (Prontas):**

| Estratégia | Status | Implementação |
|-----------|--------|--------------|
| Email/Password | Pronto | Signup, Login, Token refresh |
| Magic Link | Pronto | Via email (Resend/SMTP/Webhook) |
| OAuth (Google, Apple, etc.) | Pronto | Redirect + callback flow |
| TOTP (2FA) | Pronto | Setup + Verify + Recovery codes |
| Providers Customizados | Pronto | Ex: Validador de CPF próprio via Webhook |
| Anonymous | Pronto | Guest access sem registro |

**Auth Schema por tenant:**
```sql
auth.users             — Usuários abstratos, agnósticos de provider
auth.identities        — Chaves de autenticação vinculadas (1:N, email, cpf, totp, etc)
auth.refresh_tokens    — Sessões ativas com rotação e device fingerprinting
auth.otp_codes         — Códigos de desafio com expiração e tentativas
auth.used_totp_codes   — Proteção anti-replay de 5 minutos
auth.policies          — Regras de autenticação por contexto
auth.panic_revocations — Alvos neutralizados em lockdowns de emergência
auth.audit_log         — Auditoria contínua para LGPD/GDPR
auth.sessions    — Sessões ativas com refresh tokens
auth.identities  — Identidades vinculadas (multi-provider)
auth.mfa_factors — TOTP factors (2FA)
```

**Features adicionais (100% implementadas):**
- Rate limiting de login com heurística híbrida (IP e Identifier).
- Session management com revogação e token rotation.
- Auth Orchestration via `auth.policies`.
- Rate limiting de login (configurable)
- Device fingerprinting (hash de IP + User-Agent)
- Session management (revoke individual sessions)
- Link/Unlink identities (vincular múltiplos providers a um usuário)
- Auth Orchestration (políticas de segurança configuráveis)


### 5.3 Auth Controller

Arquivo: `controllers/auth.go` (1257 linhas)

Rotas:
```
POST /auth/v1/signup          — Registro de usuário
POST /auth/v1/token           — Login Universal (password, magic_link, oauth, etc)
GET  /auth/v1/user            — Dados do usuário autenticado
GET  /auth/users              — Lista usuários (admin)
POST /auth/users              — Cria usuário (admin)
DELETE /auth/users/{id}       — Remove usuário
GET  /auth/users/{id}/sessions    — Sessions do usuário
DELETE /auth/users/{id}/sessions  — Revoga todas as sessions
DELETE /auth/users/{id}/sessions/{sessionId} — Revoga session específica
GET  /auth/orchestration/policies — Políticas de auth
```

### 5.4 Frontend — AuthConfig

Arquivo: `frontend/pages/AuthConfig.tsx` (3336 linhas — a maior página)

Dashboard completo para controle do Orquestrador de Autenticação. 
Contém 7 seções:
1. **Users** — Diretório de usuários, search, paginação, detalhes
2. **Strategies** — Configuração de providers customizáveis (Email, Google, Custom Webhooks como CPF).
3. **Orchestration** — Políticas de segurança (lockout híbrido, password strength, requisitos de origem).
4. **Messaging** — Configuração de disparo via email ou webhooks para OTP.
5. **Security** — MFA/TOTP universal, device fingerprinting, regras de anti-replay.
6. **Apps** — Multi-App Architecture (App Clients).
7. **Schema** — Visualização rica do auth schema (incluindo identities e policies).

---

## 6. SISTEMA DE DADOS (Data Plane)

### 6.1 Data Controller

Arquivo: `controllers/data.go` (5194 linhas — o maior controller)

É o coração do CRUD. Implementa um **PostgREST-compatible API** com suporte a:

**Operações:**
```
GET    /{tableName}              — Query com filtros PostgREST
POST   /{tableName}              — Insert (single ou batch)
PATCH  /{tableName}              — Update com filtros
DELETE /{tableName}              — Delete com filtros
POST   /rpc/{name}               — Executa PostgreSQL function/RPC
POST   /query                    — Raw SQL query (admin only)
GET    /schemas                  — Lista schemas do banco
GET    /tables                   — Lista tabelas
GET    /tables/{name}/columns    — Colunas de uma tabela
GET    /functions                — Lista functions/RPCs
GET    /triggers                 — Lista triggers
GET    /cron-jobs                — Lista cron jobs
```

**Compatibilidade PostgREST/Supabase:**
- Filtros: `?name=eq.John&age=gt.18&select=id,name`
- Ordenação: `?order=created_at.desc`
- Paginação: `?offset=0&limit=10` ou `Range` header
- Prefer header: `return=representation`, `resolution=merge-duplicates`
- Operadores: `eq`, `neq`, `gt`, `gte`, `lt`, `lte`, `like`, `ilike`, `in`, `is`, `cs`, `cd`, `not`

**Funcionalidades especiais:**
- **HandlePostgrest** — Traduz queries estilo PostgREST para SQL puro
- **Schema Cache** — Cache L1 (sync.Map) + L2 (Dragonfly) para metadata de tabelas
- **Computed Columns** — Fórmulas executadas no insert/update (ex: `{{price}} * {{quantity}}`)
- **Auto-Clock Columns** — Colunas que recebem `NOW()` automaticamente em cada update
- **Locked Columns** — Proteção contra modificação (immutable, insert_only, service_role_only, OTP-protected)
- **Masked Columns** — Ofuscação de dados sensíveis (hide, blur, mask, semi-mask, encrypt)
- **Format Validation** — Validação de padrão regex por coluna
- **Enum Validation** — Validação de valores contra PostgreSQL ENUM types nativos

### 6.2 Schema Cache (Edge-First Architecture)

Arquivo: `services/schema_cache.go` (1006 linhas)

Cache de duas camadas para eliminar round-trips ao PostgreSQL:

```
Request → L1 (sync.Map, in-memory, TTL dinâmico)
       ↓ miss
       → L2 (Dragonfly/Redis, TTL 5min)
       ↓ miss
       → PostgreSQL (information_schema + system.projects metadata)
       → Popula L1 + L2
```

O cache armazena por coluna:
- `formatPattern` — Regex de validação
- `lockLevel` — Nível de proteção (unlocked, immutable, insert_only, ...)
- `maskLevel` — Nível de mascaramento
- `formula` — Fórmula de coluna computada
- `dataType` — Tipo PostgreSQL
- `isNullable` — Se permite NULL

### 6.3 Compatibilidade REST

O Cascata suporta 3 estilos de URL:

| Estilo | URL | Descrição |
|--------|-----|-----------|
| **Sovereign** (moderno) | `/api/data/{slug}/{tableName}` | API nativa do Cascata |
| **Legacy** (Supabase) | `/api/data/{slug}/rest/v1/{tableName}` | Compatível com Supabase/PostgREST/FlutterFlow |
| **Custom Domain** | `https://meuapp.com/{tableName}` | Acesso direto via domínio customizado |

### 6.4 Extensions (PostgreSQL)

Arquivo: `services/extensions.go` (803 linhas)

Gerencia instalação/remoção de extensões PostgreSQL por projeto:
- `uuid-ossp`, `pgcrypto` (pré-instaladas)
- `pg_trgm`, `pg_stat_statements`, `hstore`, `ltree`, `citext`, etc.

**Rotas:**
```
GET  /extensions              — Lista extensões disponíveis/instaladas
POST /extensions/install      — Instala extensão
POST /extensions/uninstall    — Remove extensão
```

### 6.5 ENUM Types

Gerenciamento de tipos ENUM nativos do PostgreSQL:
```
GET    /enum-types          — Lista ENUMs do projeto
POST   /enum-types          — Cria novo ENUM
PATCH  /enum-types/{name}   — Modifica ENUM (ADD/RENAME value)
DELETE /enum-types/{name}   — Remove ENUM
```

---

## 7. SISTEMA DE STORAGE

### 7.1 Storage Service

Arquivo: `services/storage.go` (1865 linhas)

**8 providers suportados:**

| Provider | Status | Tipo |
|---------|--------|------|
| **Local** (filesystem) | Implementado | Default |
| **MinIO** (S3 interno) | Implementado | Self-hosted S3 |
| **AWS S3** | Implementado | Cloud |
| **Cloudinary** | Implementado | Cloud (imagens/vídeo) |
| **ImageKit** | Implementado | Cloud (imagens) |
| **Cloudflare Images** | Implementado | Cloud (imagens) |
| **Google Drive** | Parcial | Cloud |
| **Dropbox** | Parcial | Cloud |

**Funcionalidades:**
- Upload multipart com streaming
- Upload assíncrono via worker (arquivos grandes)
- Signed URLs para upload direto (bypass backend)
- Organização em buckets com pastas
- RLS por bucket (Row Level Security)
- Download direto com Content-Disposition
- Search global por nome/tipo
- Move/Copy de arquivos entre buckets
- Sync de bucket (reconciliação filesystem ↔ banco)
- **Governance Modal** — UI para configurar limites por "sector" (images, videos, docs, etc.)

### 7.2 Storage Controller

Arquivo: `controllers/storage.go` (1340 linhas)

```
GET    /storage/buckets          — Lista buckets
POST   /storage/buckets          — Cria bucket
PATCH  /storage/buckets/{name}   — Renomeia bucket
DELETE /storage/buckets/{name}   — Remove bucket + conteúdo
GET    /storage/search           — Busca global
POST   /storage/move             — Move arquivos
GET    /storage/{bucket}/list    — Lista conteúdo
POST   /storage/{bucket}/folder  — Cria pasta
POST   /storage/{bucket}/upload  — Upload arquivo
POST   /storage/{bucket}/sign    — Signed URL para upload direto
GET    /storage/{bucket}/sync    — Reconcilia filesystem ↔ DB
DELETE /storage/{bucket}/object  — Remove arquivo
GET    /storage/{bucket}/object/* — Download/visualização
```

### 7.3 Frontend — Storage Explorer

Arquivo: `frontend/pages/StorageExplorer.tsx` (921 linhas)

- File manager visual com grid/list view
- Upload drag & drop
- Preview de imagens inline
- Governance modal para limites por tipo de arquivo (15 "sectors")
- Provider switching (Local, S3, Cloudinary, etc.)

---

## 8. SISTEMA DE SEGURANÇA

### 8.1 Camadas de Defesa (Edge Defense)

O Cascata implementa **Múltiplas camadas de defesa** atuando de ponta a ponta ANTES da persistência de dados. Todas estas camadas estão concluídas e plenamente funcionais:

```text
┌─────────────────────────────────────────────────────────────────────┐
│  LAYER 0: Nginx Edge Defense                                        │
│  ├── Bloqueio sem User-Agent (bots maliciosos)                      │
│  ├── Rate Limit: 5000 req/s por IP (zone=edge_ddos)                 │
│  ├── Connection Limit: 50 conn/IP (anti-slowloris)                  │
│  ├── Injeção: X-Tenant-Slug (do server_name/subdomínio)              │
│  └── Backend: Nginx (kernel space, zero overhead)                   │
│  🎯 Descarta DDoS bruto antes de chegar no Go/Dragonfly               │
├─────────────────────────────────────────────────────────────────────┤
│  LAYER 1: IP Hard Cap (OPCIONAL)                                    │
│  ├── Chave: edge:layer1:ip:{ip}                                     │
│  ├── Limite: via ENV EDGE_IP_HARD_CAP (ex: 1000 req/min)          │
│  ├── Se ENV não setada = Layer 1 DESABILITADO                       │
│  └── Backend: Dragonfly (Redis)                                   │
│  🎯 Bloqueia DDoS puro, bots sem token (se habilitado)              │
├─────────────────────────────────────────────────────────────────────┤
│  LAYER 2: JWT Parse Local COM Verificação de Assinatura             │
│  ├── Extrai UUID do JWT e verifica assinatura (HMAC)                │
│  ├── Chave JWT: Cacheada no Dragonfly (project:{slug}:jwt_secret)   │
│  ├── Tenant ID: X-Tenant-Slug header (do Nginx) ou URL/subdomínio   │
│  ├── Operação: Parse + Verify (~microssegundos)                     │
│  ├── Saída: userUUID + authSource (jwt/apikey/anon)                 │
│  └── Backend: Dragonfly (cache de chaves)                         │
│  🎯 Identifica usuário SEM DB lookup + evita JWT forjado            │
├─────────────────────────────────────────────────────────────────────┤
│  LAYER 3: Rate Limit Inteligente (4 Regras do Banco)                │
│  ├── Chave: edge:layer3:{ip}:{uuid}:{tenant}:{rule_type}            │
│  ├── Regras: Lidas de system.rate_limits (cacheado no Dragonfly)    │
│  ├── 4 Tipos Detectados:                                            │
│  │   • global:  qualquer endpoint não específico                     │
│  │   • auth:    /auth/*, /login, /signup                           │
│  │   • table:   /tables/{nome}/*                                   │
│  │   • rpc:     /rpc/{função}                                       │
│  └── Backend: Dragonfly (contadores) + PostgreSQL (regras)          │
│  🎯 Granularidade: cada UUID tem seu próprio limite                 │
├─────────────────────────────────────────────────────────────────────┤
│  LAYER 3.5: Ban Progressivo (Strikes)                               │
│  ├── Chave: ban:active:{ip} e ban:strikes:{ip}:{uuid}                │
│  ├── TTL Progressivo: 1min → 10min → 1h → 6h → 24h                 │
│  ├── Ativa após 3+ strikes em 24h                                   │
│  └── Backend: Dragonfly (Redis)                                     │
│  🎯 Atacantes persistentes são banidos progressivamente             │
├─────────────────────────────────────────────────────────────────────┤
│  LAYER 4: automação depois PostgreSQL (Handlers) ou pós postgress, se não tiver automação ele roda direto no postgres e volta para o cascata go que faz a limpeza com data privacy. │
│  └──Evento de automação gatilho de sequesto de pŕe Evento de PG.18  │
│  └──Só recebe requests que passaram nas 5 camadas anteriores        │
│  └──Evento de automação gatilho de sequesto de pós Evento de PG.18  │
│  🎯 PostgreSQL protegido - nunca tocado por ataques                 │
   └──Evento de Data Privacy Override (Parte do pacote Universal Padlock)│
└─────────────────────────────────────────────────────────────────────┘
```

**Tenancy Resolution & Pre-Persistence Validation**
Após o Edge Defense, ocorre a **PROJECT RESOLUTION**:
- Tenant Identification (slug extraction).
- Database Pool Resolution.
- User Context Population (JWT claims, role, permissions).

**Pre-Persistence Security Layers (Dentro do Controller):**
1. **FORMAT PATTERN VALIDATION (Regex)**: Validação de input contra injeções e patterns não conformes.
2. **LOCKED COLUMNS STRIPPING (Security Padlock)**: Descarte sumário de dados cujo o cliente tenta sobrepor a colunas trancadas.
3. **COMPUTED COLUMNS**: Preenchimento via fórmulas matemáticas antes de enviar para persistência.
4. **RLS CONTEXT APPLICATION**: Bind de `set_config` para Role e UUID.
5. **SQL EXECUTION**: Disparo para PostgreSQL (já com RLS injetado).
6. **AUTOMATION INTERCEPTORS**: Nexus captura eventos e hooks baseando-se no que ocorreu.
7. **PRIVACY MASKING (Data Obfuscation)**: Tratamento de saída (Blur, Mask) antes de retornar ao Client Response.

### 8.2 Panic Mode

Ativação instantânea que bloqueia TODAS as requests de dados para um projeto:
```
POST /security/panic  → Ativa/desativa panic mode
```
Usa Dragonfly para propagação instantânea entre workers.

### 8.3 Row Level Security (RLS)

Arquivo: `controllers/security.go` (514 linhas)

```
GET    /policies                          — Lista RLS policies
POST   /policies                          — Cria policy
DELETE /policies/{table}/{name}           — Remove policy
GET    /security/status                   — Status geral de segurança
```

### 8.4 API Keys & Hard Security (100% Concluído e Operante)

O sistema de **Hard Security e Gerenciamento de Chaves** já está com suas features concluídas, englobando tudo que é essencial para Enterprise:

- **Key Groups e API Keys**:
  - Geração segura com hash (bcrypt) gerando chaves `sk_live_*`.
  - Grupos de Chaves (`Key Groups`) com suportes nativos de Quotas Acumulativas, Nerf Configurations (Degraded Mode), Rejection Messages customizadas, limites granulares de CRUD (pesos para read vs write) e Multi-dimensional Time Windows (diário, semanal, mensal).
  - Listagem e Remoção com dependências atreladas, migração de API keys entre grupos já desenvolvidos e prontos na stack Go + Dragonfly.
- **Nerf (Degraded Mode)**: Quando uma key expira, o sistema a rotula como "DEGRADED" em vez de barrar imediatamente caso configurado, validando e mitigando interrupções bruscas (tudo funcional e auditado).
- **Rate Limiting Avançado e Traffic Rules**:
  - CRUD granular de regras com Time Windows e Operation Weights (onde reads valem X e creates valem Y).
  - Mensagens de Rejeição e quotas totalmente geridas via Redis (Dragonfly) e PostgreSQL para consistência sem overhead.
  - Rate Limits baseados em perfis (Anon/Auth) geridos dinamicamente e com Burst Limits.
- **Panic Mode**:
  - Toggle de emergência validado operando a pleno vapor. Utiliza `Dragonfly + PostgreSQL` para travar 100% o tenant ou o origin/provider num piscar de olhos, garantindo que "Aperte o botão, tudo para". Tudo operante no backend Go e no dashboard de status.

*Nota: Todas as funcionalidades listadas acima para manipulação de chaves, migração, custom messages, time windows e panic modes estão plenamente operantes no backend e na UI do RLS Manager (Hard Security).*

Rotas principais da API de Security:
```
GET    /security/status                   — Status geral de segurança (RPS + panic) via Dragonfly
POST   /security/panic                    — Toggle de Panic Mode

GET    /api-keys                          — Lista API keys atreladas aos grupos
POST   /api-keys                          — Cria API key (sk_live_* hash)
PATCH  /api-keys/{id}                     — Atualiza (is_active, expiração)
POST   /api-keys/{id}/migrate             — Migração manual de grupo de chave
DELETE /api-keys/{id}                     — Remove API key

GET    /security/key-groups               — Lista grupos de keys
POST   /security/key-groups               — Cria grupo (com nerf, weights, quotas e limits)
PATCH  /security/key-groups/{id}          — Atualiza grupo
DELETE /security/key-groups/{id}          — Remove grupo

GET    /rate-limits                       — Lista traffic rules e limits globais
POST   /rate-limits                       — Cria regra com mensageria e pesos
DELETE /rate-limits/{id}                  — Remove regra
```

### 8.5 Rate Limiting
Regras granulares do Dynamic Rate Limiter já prontas e aplicáveis por:
- Endpoint/path pattern
- Método HTTP
- Role (anon, authenticated, service_role)
- IP range
- Window + max requests + Burst Limits + Pesos das Operações

### 8.6 Fortress Mode

Modo de segurança **Default Deny** ativado via `CASCATA_FORTRESS_MODE=enabled`:
- Nível 4: Sistema — só admin + MFA
- Nível 3: Control — service_role obrigatório
- Nível 2: Data — JWT verificado obrigatório
- Nível 1: Public — só health checks

### 8.7 Frontend — RLS Manager / RLS Designer

Arquivos: `frontend/pages/RLSManager.tsx` (1877 linhas), `frontend/pages/RLSDesigner.tsx` (917 linhas)

- Construtor visual de policies RLS
- Templates pré-definidos (select-own, insert-own, etc.)
- Preview de SQL gerado
- Toggle de RLS por tabela

---

## 9. CRYPTO ENGINE

### 9.1 Arquitetura

Microserviço Go separado (`crypto-engine/`) que gerencia toda a criptografia:

```
crypto-engine/
├── internal/
│   ├── api/handlers.go       — HTTP API (518 linhas)
│   ├── crypto/aes.go         — AES-256-GCM encryption
│   ├── crypto/tarpit.go      — Anti-bruteforce delay
│   ├── kek/derive.go         — Key derivation (HKDF)
│   └── keystore/store.go     — Persistent key storage
```

### 9.2 Endpoints

```
POST /v1/encrypt              — Criptografa plaintext
POST /v1/decrypt              — Decifra ciphertext
POST /v1/encrypt-batch        — Batch encryption
POST /v1/decrypt-batch        — Batch decryption
POST /v1/keys/rotate          — Rotação de chave
POST /v1/secrets/store/{id}   — Armazena secret
GET  /v1/secrets/retrieve/{id} — Recupera secret
POST /v1/sys/unseal           — Desbloqueia engine
POST /v1/sys/rekey            — Re-key da master key
GET  /v1/sys/status           — Status (sealed/unsealed)
GET  /v1/sys/fingerprint      — Fingerprint da chave ativa
```

### 9.3 Modelo Seal/Unseal

O Crypto Engine inicia **selado** (sealed). Para funcionar precisa ser "unsealed" com a `CASCATA_MASTER_SECRET`. O backend tenta auto-unseal no boot (5 tentativas com retry).

Enquanto selado: criação de projetos e criptografia ficam indisponíveis.

---

## 10. SECRETS VAULT

### 10.1 Vault Service

Arquivo: `services/vault.go` (309 linhas)

Armazenamento seguro de secrets por projeto, criptografados pelo Crypto Engine.

**Release Policies:**
- `exportable` — Pode ser revelado na UI
- `runtime` — Só acessível em automações/runtime
- `verify_only` — Só para verificação de assinatura
- `sign_only` — Só para assinatura

**Access Purposes:**
- `ui_reveal` — Dashboard mostra o valor
- `automation_runtime` — Automações Nexus acessam
- `rpc_runtime` — RPCs acessam
- `webhook_verify` — Verificação de webhook signature
- `internal_system` — Uso interno do sistema

### 10.2 Secrets Controller

Arquivo: `controllers/secrets.go` (222 linhas)

```
GET    /vault                    — Lista secrets (sem valores)
POST   /vault                    — Cria/atualiza secret
POST   /vault/{id}/reveal        — Revela valor (requer policy)
DELETE /vault/{id}               — Remove secret
POST   /vault/stats              — Estatísticas do vault
```

---

## 11. SISTEMA DE PUSH NOTIFICATIONS

### 11.1 Push Service

Arquivo: `services/push.go` (743 linhas) + `services/push_bulk.go` (553 linhas) + `services/push_templates.go` (221 linhas)

Integração com **Firebase Cloud Messaging (FCM)** para push notifications.

**Features:**
- Registro/desregistro de devices
- Envio individual e bulk (batch)
- Templates I18N com variáveis
- Grupos de segmentação (SQL-based)
- Campanhas com schedule
- Rules (automação de push baseada em eventos DB)
- Histórico completo
- Analytics (success rate, delivery stats)

### 11.2 Push Controller

Arquivo: `controllers/push.go` (605 linhas)

```
POST   /push/devices/register       — Registra device FCM
POST   /push/devices/unregister     — Remove device
GET    /push/devices                 — Lista devices

POST   /push/send                    — Envia push individual
POST   /push/send-bulk               — Envia push em massa

GET    /push/rules                   — Lista regras automáticas
POST   /push/rules                   — Cria regra
DELETE /push/rules/{id}              — Remove regra

GET    /push/templates               — Lista templates I18N
POST   /push/templates               — Cria template
PUT    /push/templates/{id}          — Atualiza template
DELETE /push/templates/{id}          — Remove template

GET    /push/groups                   — Lista grupos
POST   /push/groups                   — Cria grupo
PUT    /push/groups/{id}              — Atualiza grupo
DELETE /push/groups/{id}              — Remove grupo
POST   /push/groups/{id}/sync        — Sincroniza membros

GET    /push/campaigns               — Lista campanhas
POST   /push/campaigns               — Cria campanha
POST   /push/campaigns/{id}/cancel   — Cancela campanha

GET    /push/history                  — Histórico de envios
GET    /push/stats                    — Analytics

GET    /push/config                   — Configuração FCM
POST   /push/config                   — Salva configuração FCM
```

### 11.3 Frontend — Push Manager

Arquivo: `frontend/pages/PushManager.tsx` (754 linhas)

UI completa para gerenciamento de push com:
- Dashboard de métricas
- Composer de notificações
- Gerenciamento de devices/grupos
- Templates I18N
- Campanhas
- Histórico

---

## 12. WEBHOOKS

### 12.1 Webhook Service

Arquivo: `services/webhook.go` (114 linhas)

- Disparo automático baseado em eventos de banco (INSERT, UPDATE, DELETE)
- Filtros condicionais por campo (eq, neq, contains, starts_with)
- Enfileiramento via Dragonfly Stream
- Retry policy configurável
- Fallback URL

### 12.2 Webhook Controller

Arquivo: `controllers/webhook.go` (479 linhas)

**Outgoing (Cascata → Externo):**
```
GET    /webhooks/receivers         — Lista webhooks configurados
POST   /webhooks/receivers         — Cria webhook
DELETE /webhooks/receivers/{id}    — Remove webhook
```

**Incoming (Externo → Cascata):**
```
ANY /webhook/{pathSlug}                           — Gateway limpo
ANY /api/webhooks/in/{projectSlug}/{pathSlug}      — Gateway legado
```

Webhook receivers suportam:
- HMAC signature verification
- Nexus automation trigger
- Table insertion automática
- Custom transformation via Nexus graph

---

## 13. EDGE FUNCTIONS (Serverless)

### 13.1 Edge Service

Arquivo: `services/edge.go` (97 linhas)

Executa **JavaScript** em sandbox via **Goja** (VM JS para Go):

Features:
- Variáveis de ambiente injetadas (`env`)
- Request context injetado (`ctx`)
- Bridge para database (`db.query(sql, params)`)
- Timeout configurável
- Panic recovery
- Context cancellation propagation

### 13.2 Edge Controller

```
GET  /edge/functions           — Lista edge functions
POST /edge/deploy              — Deploy de nova function
```

As functions são armazenadas em `system.edge_functions` e executadas on-demand.

---

## 14. REALTIME (Server-Sent Events)

### 14.1 Realtime Service

Arquivo: `services/realtime.go` (162 linhas)

Implementa **SSE (Server-Sent Events)** para streaming de mudanças em tempo real:

1. Cliente conecta via `GET /realtime`
2. Backend cria listener PostgreSQL `LISTEN cascata_events` no banco do tenant
3. Triggers `notify_changes()` em cada tabela disparam `NOTIFY cascata_events`
4. Backend broadcasta payload via SSE para todos os clientes conectados

**Suporte a Draft:** Listeners são separados por `slug:env`, então draft tem seu próprio stream.

**Keep-alive:** Ping a cada 15 segundos.

---

## 15. AI / CASCATA ARCHITECT

### 15.1 AI Service

Arquivo: `services/ai.go` (509 linhas) + `services/ai_intelligence.go` (609 linhas)

**Cascata Architect** é um assistente AI integrado que:
- Entende o schema do banco do projeto
- Gera SQL (DDL e DML) automaticamente
- Detecta intents: `general`, `schema`, `query`, `create`, `alter`, `fix`, `docs`, `routes`
- Executa tool calls (function calling com OpenAI API)
- Suporta streaming de respostas
- Histórico de sessões por projeto
- Token estimation e context windowing

### 15.2 AI Controller

Arquivo: `controllers/ai.go` (533 linhas)

```
POST /ai/chat                    — Chat com o Architect (streaming)
GET  /ai/sessions                — Lista sessões
POST /ai/sessions/search         — Busca em sessões
PATCH /ai/sessions/{id}          — Atualiza sessão (título)
DELETE /ai/sessions/{id}         — Remove sessão
GET  /ai/history/{session_id}    — Histórico de mensagens
PATCH /ai/history/{id}           — Edita mensagem
DELETE /ai/history/{id}          — Remove mensagem
GET  /docs/openapi               — Spec OpenAPI 3.0 gerado automaticamente
GET  /docs/pages                 — Páginas de documentação
```

### 15.3 OpenAPI Generator

Arquivo: `services/openapi.go` (597 linhas)

Gera automaticamente spec **OpenAPI 3.0.0** baseado no schema real do banco:
- Todas as tabelas → paths (GET, POST, PATCH, DELETE)
- Colunas → request/response schemas
- Edge functions → paths adicionais
- Servers: Sovereign + Legacy (Supabase-style)

### 15.4 Frontend — Cascata Architect

Arquivo: `frontend/components/CascataArchitect.tsx` (1849 linhas)

Chat AI flutuante com:
- Input de texto e voz (Web Speech API)
- Streaming de respostas (SSE)
- Renderização de SQL com syntax highlighting
- Execução direta de SQL gerado
- Histórico de sessões navegável
- Suporte a markdown (via `marked`)

---

## 16. NEXUS ENGINE (Automação Visual)

### 16.1 Arquitetura

O Nexus é um **motor de automação baseado em grafos** (Flow-Based Programming):

```
services/nexus/
├── nexus_service.go           — Fachada principal (319 linhas)
├── nexus_engine.go            — Motor de execução (830 linhas)
├── nexus_compiler.go          — Compilador JSON → Graph (615 linhas)
├── nexus_state.go             — Estado de execução (812 linhas)
├── nexus_hook_resolver.go     — Resolver de hooks Pre/Post-Persist (1000 linhas)
├── nexus_audit.go             — Auditoria de execuções (105 linhas)
├── standard_library.go        — Componentes padrão (1005 linhas)
├── component.go               — Base abstrata de componente (252 linhas)
├── packet.go                  — Information Packet (dados em trânsito) (180 linhas)
├── worker_lane.go             — Worker assíncrono com Redis streams (675 linhas)
├── graph/
│   ├── graph.go               — Estrutura de grafo (317 linhas)
│   └── topology_validator.go  — Validação topológica (306 linhas)
├── node_data.go               — Nó de operação de dados (417 linhas)
├── node_http.go               — Nó HTTP request (168 linhas)
├── node_qdrant.go             — Nó de operação vetorial (267 linhas)
├── node_foreach.go            — Loop foreach (144 linhas)
├── node_subdag.go             — Sub-grafo encapsulado (81 linhas)
├── rls_bridge.go              — Bridge para RLS (122 linhas)
└── debug_state.go             — Debug de estado (73 linhas)
```

### 16.2 Componentes Standard Library

| Componente | Tipo | Função |
|-----------|------|--------|
| **Trigger** | Entrada | Ponto de entrada do fluxo (table event, webhook, cron, manual) |
| **Response** | Saída | Retorna resposta HTTP (status + body) |
| **Condition** | Lógica | IF/ELSE com branching true/false |
| **Transform** | Dados | Manipulação de payload (map, pick, merge) |
| **Switch** | Lógica | Multi-path routing (como switch/case) |
| **Merge** | Dados | Combina múltiplos inputs |
| **Split** | Dados | Divide array em itens individuais |
| **ErrorHandler** | Controle | Captura erros com fallback |
| **DataNode** | Dados | CRUD em tabela (select, insert, update, delete) |
| **HTTPNode** | Integração | HTTP request externo (GET, POST, etc.) |
| **QdrantNode** | Vetorial | Operações em Qdrant (search, upsert, delete) |
| **ForEachNode** | Loop | Itera sobre array executando sub-fluxo |
| **SubDagNode** | Composição | Executa sub-grafo encapsulado |

### 16.3 Hook Resolver (Pre/Post-Persist)

O `NexusHookResolver` intercepta operações de dados ANTES ou DEPOIS de serem persistidas:
- **Pre-Persist:** Pode modificar, bloquear ou redirecionar a operação
- **Post-Persist:** Executa após a operação (notificações, sync, audit)

### 16.4 Worker Lane

Processamento assíncrono via Redis Streams:
- Fila `nexus:queue:{projectSlug}`
- Dead Letter Queue (DLQ) para falhas
- Concurrency control (max workers configurável)
- Retry com exponential backoff

### 16.5 Frontend — Nexus Architect

Arquivo: `frontend/components/NexusArchitect.tsx` (2972 linhas)

Editor visual de automações usando **React Flow**:
- Drag & drop de nós
- Conexão visual entre portas
- Configuração inline de cada nó
- Mini-map para navegação
- Save/Load de automações
- Test execution

---

## 17. SISTEMA DE BACKUP

### 17.1 Backup Service

Arquivo: `services/backup.go` (214 linhas)

Gera backup completo como arquivo ZIP contendo:
1. `manifest.json` — Metadados do projeto
2. `system/secrets.json` — JWT secret, anon key, service key
3. `vector/snapshot.qdrant` — Snapshot da collection Qdrant
4. `schema/structure.sql` — pg_dump do schema
5. `system/auth_data.sql` — Dump do schema auth
6. `data/{table}.csv` — Export CSV de cada tabela
7. `storage/` — Todos os arquivos do storage

### 17.2 Backup Controller

Arquivo: `controllers/backup.go` (318 linhas)

```
GET    /backups/policies                  — Lista policies de backup
POST   /backups/policies                  — Cria policy
POST   /backups/validate                  — Valida configuração
PATCH  /backups/policies/{id}             — Atualiza policy
DELETE /backups/policies/{id}             — Remove policy
POST   /backups/policies/{id}/run         — Trigger manual (INCOMPLETO — mock)
GET    /backups/history                   — Histórico de backups
GET    /backups/history/{id}/download     — Download de backup
POST   /backups/history/{id}/restore      — Restore (retorna 501 — NÃO IMPLEMENTADO)
```

### 17.3 Frontend — Project Backups

Arquivo: `frontend/pages/ProjectBackups.tsx` (990 linhas)

UI completa com:
- Timeline de backups
- Configuração de policies (manual, cron, pré-deploy)
- Snapshot instantâneo
- Download de backup
- Restore wizard (UI pronta, backend incompleto)

---

## 18. SISTEMA DRAFT/LIVE (Environment Branching)

### 18.1 Conceito

Database Branching inspirado em Git:
- **Live** = main branch (produção)
- **Draft** = feature branch (desenvolvimento)
- **Deploy** = merge/PR

### 18.2 Branch Controller

Arquivo: `controllers/branch.go` (201 linhas)

```
GET    /branch/status       — Status do ambiente
GET    /branch/diff         — Diff entre Live e Draft (STUB no Go)
GET    /branch/snapshots    — Lista snapshots
POST   /branch/create       — Cria Draft (clone do Live)
POST   /branch/deploy       — Deploy Draft → Live
DELETE /branch/draft        — Descarta Draft
```

### 18.3 Estado Atual (Go vs TypeScript legado)

O sistema está **~20% implementado** no Go. Ver relatório anterior (`relatorio_draft_live.md`) para análise detalhada de gaps.

---

## 19. SISTEMA DE LOGS E OBSERVABILIDADE

### 19.1 Logging Service

Arquivo: `services/logging.go` (333 linhas)

- Buffer assíncrono de logs com flush periódico
- Categorias: `request`, `security`, `system`, `audit`
- Severidades: `info`, `warning`, `critical`
- Security events separados para compliance

### 19.2 OTLP Export

Arquivo: `services/otlp.go` (323 linhas)

Export de logs para sistemas externos via **OpenTelemetry Protocol**:

**Providers suportados:**
| Provider | Protocolo |
|---------|----------|
| Datadog | OTLP/HTTP |
| Splunk | HEC |
| Grafana Loki | Push API |
| ELK/Elasticsearch | Bulk API |
| S3 | Put Object |
| OTLP genérico | OTLP/HTTP |

### 19.3 Frontend — Project Logs

Arquivo: `frontend/pages/ProjectLogs.tsx` (1267 linhas)

- Visualização de logs com filtros avançados
- Export para CSV/JSON
- Configuração de exportadores externos
- Geração de API key para acesso externo
- Purge scheduling

### 19.4 Purge Scheduler

Arquivo: `services/purge_scheduler.go` (248 linhas)

Limpa logs antigos automaticamente baseado em:
- `log_retention_days` por projeto
- Tiered retention (configurável por tipo de log)

---

## 20. CERTIFICADOS SSL E CUSTOM DOMAINS

### 20.1 Certificate Service

Arquivo: `services/certificate.go` (495 linhas)

- Emissão automática via **Let's Encrypt (Certbot)**
- Suporte a **Cloudflare PEM** (certificates manuais)
- Geração dinâmica de configuração NGINX
- Reload de NGINX via controller

### 20.2 Cloudflare Integration

Arquivo: `services/cloudflare.go` (265 linhas)

Detecta se domínio está usando Cloudflare proxy e valida configuração:
- SSL mode (flexible, full, strict)
- DNS validation
- DDoS protection status

### 20.3 Admin Certificate Routes

```
GET    /system/certificates/status           — Lista certificados
GET    /system/certificates/task/{taskId}    — Status de task
POST   /system/certificates                  — Cria certificado
POST   /system/ssl-check                     — Verifica SSL
DELETE /system/certificates/{domain}         — Remove certificado
POST   /system/rebuild-nginx                 — Rebuild configuração NGINX
```

---

## 21. APP CLIENTS (Multi-App Architecture)

### 21.1 Conceito

Um projeto Cascata pode ter múltiplos **App Clients**, cada um com:
- API key única (`nonce`)
- `site_url` e `allowed_origins` (CORS)
- **Table-level access control** (whitelist/blacklist de tabelas)

Isso permite ter um app mobile e um web app compartilhando o mesmo banco, mas com permissões diferentes.

### 21.2 Controller

Arquivo: `controllers/appclient.go` (340 linhas)

```
GET    /app-clients               — Lista App Clients
POST   /app-clients               — Cria App Client
GET    /app-clients/{id}          — Detalhe
PUT    /app-clients/{id}          — Atualiza
DELETE /app-clients/{id}          — Remove
POST   /app-clients/{id}/rotate   — Rotação de chave
```

### 21.3 Middleware

Arquivo: `middleware/table_access.go` (182 linhas)

Intercepta requests e verifica se o App Client tem acesso à tabela sendo acessada.

---

## 22. MCP GATEWAY (AI Agent Protocol)

### 22.1 MCP Controller

Arquivo: `controllers/mcp.go` (73 linhas)

Implementa o **Model Context Protocol** para permitir que modelos AI (Claude, GPT, etc.) acessem dados do Cascata:

```
GET  /mcp/sse          — Conexão SSE para AI agents
POST /mcp/message      — Mensagens JSON-RPC 2.0
```

Governança:
- Habilitação por projeto (`metadata.ai_governance.mcp_enabled`)
- Perímetro de IPs/URLs permitidos

**Status:** Básico/stub — retorna tools disponíveis mas não executa operações reais.

---

## 23. QUEUE SYSTEM

### 23.1 Queue Service

Arquivo: `services/queue.go` (256 linhas)

Filas baseadas em **Dragonfly Redis Streams**:

| Stream | Propósito |
|--------|----------|
| `cascata-webhooks` | Delivery de webhooks |
| `cascata-push` | Push notifications |
| `cascata-restore` | Operações de restore |

### 23.2 Queue Controller

Arquivo: `controllers/queue.go` (91 linhas)

```
GET  /health/queue                      — Health check das filas
GET  /api/control/queue/stats           — Métricas das filas
POST /api/control/queue/dlq/requeue     — Re-enfileira itens do DLQ
```

---

## 24. AUTOMATION SYSTEM (Compiled Automations — Legado)

Além do Nexus (visual), existe um sistema de automações **compiladas** (código-based):

### 24.1 Services

| Arquivo | Linhas | Função |
|---------|--------|--------|
| `services/automation.go` | 1562 | Motor principal de automações |
| `services/compiled_automation.go` | 412 | Compilação de automações |
| `services/compiled_automation_service.go` | 629 | Serviço de execução |
| `services/compiled_nodes_http_sql.go` | 508 | Nós HTTP e SQL |
| `services/compiled_nodes_data_rpc.go` | 626 | Nós de dados e RPC |
| `services/compiled_nodes_logic.go` | 793 | Nós de lógica |
| `services/compiled_field_pipeline.go` | 369 | Pipeline de campos |
| `services/queue_automation.go` | 603 | Fila de automações |

### 24.2 Rotas

```
GET    /automations                     — Lista automações
POST   /automations                     — Cria/atualiza automação
PUT    /automations/{id}                — Atualiza
PATCH  /automations/{id}                — Patch parcial
DELETE /automations/{id}                — Remove
POST   /automations/{id}/activate       — Ativa
POST   /automations/{id}/deactivate     — Desativa
GET    /automations/stats               — Estatísticas
POST   /automations/{id}/test           — Testa automação
GET    /automations/runs                — Runs
GET    /automations/runs/{id}/logs      — Logs de uma run
GET    /automations/step-logs           — Logs de steps
GET    /automations/executions          — Lista de execuções
```

---

## 25. SDKs

O Cascata fornece SDKs para **5 plataformas**:

### 25.1 Web SDK (TypeScript)

Arquivo: `sdks/web/src/` (exporta 11 serviços)

```typescript
export { CascataClient, createClient }
export { AuthService }
export { AdvancedAuthService, PayloadCrypto, generateDeviceFingerprint }
export { DatabaseService, PostgrestQueryBuilder }
export { RealtimeService }
export { StorageService }
export { EdgeService }
export { PushService }
export { OfflineService }
```

### 25.2 Android SDK (Kotlin)

```
sdks/android/cascata-sdk/src/main/kotlin/com/cascata/sdk/
├── CascataClient.kt
├── AuthService.kt
├── AdvancedAuthService.kt
├── DatabaseService.kt
├── StorageService.kt
├── RealtimeService.kt
├── EdgeService.kt
├── PushService.kt
├── BatchService.kt
├── OfflineService.kt
├── StorageProgressService.kt
├── RetryInterceptor.kt
└── Models.kt
```

### 25.3 iOS SDK (Swift)

```
sdks/ios/CascataSDK/Sources/CascataSDK/
├── CascataClient.swift
├── AuthService.swift
├── AdvancedAuthService.swift
├── DatabaseService.swift
├── StorageService.swift
├── RealtimeService.swift
├── EdgeService.swift
├── PushService.swift
├── BatchService.swift
├── OfflineService.swift
├── StorageProgressService.swift
├── RetryInterceptor.swift
└── Models.swift
```

### 25.4 Flutter SDK (Dart)

```
sdks/flutter/lib/
├── cascata_sdk.dart
├── src/
│   ├── services/
│   │   ├── auth_service.dart
│   │   ├── advanced_auth_service.dart
│   │   ├── database_service.dart
│   │   ├── storage_service.dart
│   │   ├── realtime_service.dart
│   │   ├── edge_service.dart
│   │   ├── push_service.dart
│   │   ├── batch_service.dart
│   │   └── offline_service.dart
│   ├── models/
│   │   ├── auth_response.dart
│   │   ├── session.dart
│   │   ├── postgrest_response.dart
│   │   ├── realtime_event.dart
│   │   ├── file_object.dart
│   │   ├── bucket.dart
│   │   └── push_device.dart
│   ├── enums/
│   ├── exceptions/
│   └── utils/
```

### 25.5 Frontend SDK (Internal)

Arquivo: `frontend/lib/cascata-sdk.ts` (304 linhas)

SDK interno usado pelo dashboard para comunicar com o backend.

---

## 26. DATABASE EXPLORER (Frontend)

Arquivo: `frontend/pages/DatabaseExplorer.tsx` (2884 linhas)

O maior componente frontend. Implementa um **administrador visual de banco de dados**:

- Lista de tabelas com search
- **TablePanel** — Grid de dados com paginação
- **TableCreatorDrawer** — Criação/edição de tabelas com tipo picker, constraints, defaults
- **SQL Console** — Execução de queries SQL raw
- **Extensions Modal** — Gerenciamento de extensões PostgreSQL
- **Enum Types Modal** — Gerenciamento de tipos ENUM
- **Column Impact Modal** — Análise de impacto antes de modificar colunas
- Export em PDF, CSV, JSON
- Recycle Bin (tabelas deletadas)
- Drag-to-reorder colunas
- Inline editing de células
- Bulk operations

---

## 27. PROJECT DETAIL / PROJECT SETTINGS

### 27.1 Project Detail

Arquivo: `frontend/pages/ProjectDetail.tsx` (1226 linhas)

Página principal do projeto com:
- Sidebar de navegação (Database, Auth, Storage, Logs, etc.)
- Cards de status (DB size, connections, row counts)
- Environment switcher (Live/Draft)
- API endpoint display

### 27.2 Project Settings

Arquivo: `frontend/pages/ProjectSettings.tsx` (1072 linhas)

Configurações avançadas:
- Timezone
- CORS (Allowed Origins)
- Custom Domain + SSL
- Cloudflare integration
- BYOD (External DB URL)
- AI Governance (MCP enable/disable, perimeter)
- Schema Exposure toggle
- Danger Zone (delete project)

### 27.3 System Settings

Arquivo: `frontend/pages/SystemSettings.tsx` (1064 linhas)

Configurações globais do Cascata:
- Admin management
- Certificate management
- Public IP display
- System health

---

## 28. RPC MANAGER

Arquivo: `frontend/pages/RPCManager.tsx` (1642 linhas)

Editor visual de **PostgreSQL Functions/RPCs**:
- Listagem de functions com filtros
- SQL editor com syntax highlighting
- Execução de teste inline
- Versionamento de assets (histórico)
- Folders para organização
- Create/Edit/Delete de functions, triggers e cron jobs

---

## 29. COMPUTED COLUMNS (Formula Engine)

### 29.1 Computed Service

Arquivo: `services/computed.go` (561 linhas)

Motor de fórmulas para colunas computadas:

**Sintaxe:** `{{field_name}}` para variáveis

**Funções suportadas:**
- Matemáticas: `+`, `-`, `*`, `/`, `SUM()`, `ROUND()`, `ABS()`
- String: `UPPER()`, `LOWER()`, `CONCAT()`, `TRIM()`, `SUBSTRING()`
- Lógica: `IF(condition, then, else)`
- Agregação: `COUNT()`, `AVG()`, `MIN()`, `MAX()`

### 29.2 Math Parser

Arquivo: `services/math_parser.go` (413 linhas) + `services/math_node.go` (257 linhas)

Parser de expressões matemáticas com suporte a:
- Operações básicas (+, -, *, /)
- Parênteses
- Funções (ROUND, ABS, CEIL, FLOOR, etc.)
- Variáveis com contexto

---

## 30. DISTRIBUTED LOCK

Arquivo: `services/distributed_lock.go` (172 linhas)

Locks distribuídos via Dragonfly para operações concorrentes:
- `AcquireLock(key, ttl)` — Tenta adquirir lock
- `ReleaseLock(key)` — Libera lock
- `WithLock(key, fn)` — Executa função com lock

Usado para: migrações, deploys, backup operations.

---

## 31. MIGRATIONS

59 migration files em `backend/migrations/`:

| # | Arquivo | Propósito |
|---|--------|----------|
| 001 | `init.sql` | Schema system (projects, assets, webhooks, api_logs, ui_settings, admin_users) |
| 002 | `auth_schema.sql` | Auth schema (users, sessions, identities, mfa) |
| 003-004 | `rate_limits*.sql` | Rate limiting tables |
| 005 | `ai_memory.sql` | AI session memory |
| 006 | `asset_history.sql` | Versionamento de assets |
| 007 | `ai_sessions.sql` | AI chat sessions |
| 008 | `auth_hardening.sql` | Auth security improvements |
| 009 | `immutable_logs.sql` | Logs imutáveis |
| 010 | `auth_confirmation.sql` | Email confirmation flow |
| 011-012 | `webhook_*.sql` | Webhook filters e reliability |
| 013 | `auth_helpers.sql` | Auth helper functions |
| 014 | `push_engine.sql` | Push notifications base |
| 015 | `storage_tracking.sql` | Storage metadata tracking |
| 016 | `fix_notify_payload.sql` | Fix realtime notifications |
| 017 | `harden_default_privileges.sql` | Security hardening |
| 018 | `backup_system.sql` | Backup policies e history |
| 019 | `granular_security.sql` | Granular security controls |
| 020 | `crud_rate_limits.sql` | Per-operation rate limits |
| 021 | `nerf_logic.sql` | Nerf logic |
| 022 | `secure_hashing.sql` | Secure password hashing |
| 023 | `fix_schema_integrity.sql` | Schema integrity fixes |
| 024 | `secure_vault.sql` | Vault for secrets |
| 025 | `perf_optimization.sql` | Performance indexes |
| 026 | `log_telemetry.sql` | Log telemetry columns |
| 027 | `auth_fingerprint.sql` | Device fingerprinting |
| 028 | `async_ops.sql` | Async operations table |
| 029 | `extension_registry.sql` | Extension management |
| 030 | `identity_verification.sql` | Identity verification |
| 031 | `assets_updated_at.sql` | Asset timestamps |
| 032 | `rls_atomic_blindagem.sql` | RLS atomic operations |
| 033 | `identity_first_auth.sql` | Identity-first auth flow |
| 034 | `cascata_automations.sql` | Automation tables |
| 035 | `automation_ops.sql` | Automation operations |
| 036 | `advanced_quotas.sql` | Advanced quotas |
| 037 | `tiered_retention.sql` | Tiered log retention |
| 038 | `webhooks_updated_at.sql` | Webhook timestamps |
| 039 | `webhook_receivers.sql` | Webhook receivers (incoming) |
| 040 | `auth_orchestration.sql` | Auth orchestration policies |
| 041 | `push_engine_v2.sql` + `totp_fingerprinting.sql` | Push v2 + TOTP |
| 042 | `sovereign_audit_panic.sql` | Audit + Panic mode |
| 043 | `automation_queue_config.sql` | Queue config |
| 044 | `user_devices.sql` | User devices table |
| 045 | `security_locks_system.sql` | Column locks (immutable, insert_only, etc.) |
| 046 | `system_certificates.sql` | SSL certificate management |
| 047 | `unified_audit_system.sql` | Unified audit (15050 bytes — o maior) |
| 048 | `bucket_rls_default.sql` | Default RLS para buckets |
| 049 | `purge_schedule_config.sql` | Purge scheduling |
| 050 | `ensure_fingerprint_hash.sql` | Fingerprint hashing |
| 051 | `ui_settings_updated_at.sql` | UI settings timestamps |
| 052 | `ai_schema_aliases.sql` + `automations_schema_fix.sql` | AI aliases + Automations fix |
| 053 | `automation_status.sql` | Automation status tracking |
| 054 | `automation_step_logs.sql` | Step-level logging |
| 055 | `nexus_engine_tables.sql` | Nexus engine tables |
| 056 | `nexus_automations_status.sql` | Nexus automation status |
| 057 | `vault_release_policy.sql` | Vault release policies |
| 058 | `nexus_0_1_0_hardening.sql` | Nexus hardening |
| 059 | `api_keys_vault_integration.sql` | API keys vault integration |

---

## 32. API DOCS (Frontend)

Arquivo: `frontend/pages/APIDocs.tsx` (2105 linhas)

Documentação interativa de API com:
- Geração automática de exemplos (cURL, JavaScript, Python, Dart, Kotlin, Swift)
- Suporte a Swagger 2.0 e OpenAPI 3.0
- Routing style toggle (Sovereign vs Legacy)
- Environment toggle (Live vs Draft)
- Code snippets para cada operação
- Try-it-out para testar endpoints

---

## 33. INFRASTRUCTURE SCRIPTS

| Arquivo | Propósito |
|--------|----------|
| `scripts/firewall-sync.sh` | Sincroniza regras de firewall |
| `scripts/panic-reset.sh` | Reset de panic mode |
| `database/init.sql` | Init SQL do PostgreSQL |
| `database/phantom_linker.sh` | Linker de referências |
| `database/docker-entrypoint-cascata.sh` | Entrypoint customizado do PostgreSQL |
| `pgbouncer/entrypoint.sh` | Entrypoint do PgBouncer |
| `nginx-controller/index.js` | Controller de reload do NGINX |

---

## 34. CONTAGEM DE CÓDIGO

### Backend (Go)
| Diretório | Total de Linhas |
|----------|----------------|
| Controllers | **13.125** |
| Services | **31.422** (incluindo Nexus: ~7.688) |
| Middleware | **2.926** |
| Types | **242** |
| Utils | ~200 |
| Migrations | 59 arquivos SQL |
| **TOTAL BACKEND** | **~48.000 linhas Go** |

### Frontend (React/TypeScript)
| Diretório | Total de Linhas |
|----------|----------------|
| Pages | **17.783** |
| Components | **13.637** |
| Libs | **1.847** |
| **TOTAL FRONTEND** | **~38.000 linhas TSX** |

### SDKs
| Plataforma | Linguagem |
|-----------|----------|
| Web | TypeScript |
| Android | Kotlin |
| iOS | Swift |
| Flutter | Dart |

### Crypto Engine
| Componente | Linhas |
|-----------|--------|
| Handlers + Crypto + Keystore | ~1.500 |

### **TOTAL ESTIMADO: ~90.000+ linhas de código**

---

## 35. RESUMO DE COMPLETUDE POR SISTEMA

| # | Sistema | Backend Go | Frontend | Status |
|---|---------|-----------|----------|--------|
| 1 | Multi-Tenancy & Pool | Completo | Completo | Produção |
| 2 | Auth (Signup/Login/JWT) | Completo | Completo | Produção |
| 3 | Data CRUD (PostgREST) | Completo | Completo | Produção |
| 4 | Schema Cache (Edge-First) | Completo | N/A | Produção |
| 5 | Realtime (SSE) | Completo | Completo | Produção |
| 6 | Storage | Completo | Completo | Produção |
| 7 | RLS/Security | Completo | Completo | Produção |
| 8 | Rate Limiting | Completo | Parcial | Produção |
| 9 | Crypto Engine | Completo | N/A | Produção |
| 10 | Vault (Secrets) | Completo | Completo | Produção |
| 11 | Push Notifications | Completo | Completo | Produção |
| 12 | Webhooks (Outgoing) | Completo | Completo | Produção |
| 13 | Webhooks (Incoming) | Completo | Parcial | Produção |
| 14 | Edge Functions | Completo | Parcial | Produção |
| 15 | AI Architect | Completo | Completo | Produção |
| 16 | OpenAPI Generator | Completo | Completo | Produção |
| 17 | Nexus Automations | Completo | Completo | Produção |
| 18 | Compiled Automations | Completo | Completo | Produção |
| 19 | App Clients | Completo | Completo | Produção |
| 20 | Certificates/SSL | Completo | Completo | Produção |
| 21 | Cloudflare Integration | Completo | Completo | Produção |
| 22 | Logs/Observability | Completo | Completo | Produção |
| 23 | OTLP Export | Completo | Completo | Produção |
| 24 | Queue System | Completo | Parcial | Produção |
| 25 | MCP Gateway | Stub | N/A | Alpha |
| 26 | Backup | Parcial* | Completo | Em progresso |
| 27 | Draft/Live | Parcial** | Completo | Em progresso |
| 28 | BYOD (Eject DB) | Parcial*** | Completo | Em progresso |
| 29 | .CAF Import/Export | Backend TS não portado | Completo | Bloqueado |
| 30 | SDKs (4 plataformas) | N/A | N/A | Publicados |

\* Backup: TriggerManual é mock, RestoreBackup retorna 501
\*\* Draft/Live: Diff engine é stub, Deploy só faz swap
\*\*\* BYOD: Frontend tem UI, backend ignora external_db_url
