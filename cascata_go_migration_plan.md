# 🌊 Cascata Backend — Plano de Migração TypeScript → Go

## Resumo do Inventário

| Métrica | Valor |
|---|---|
| **Total de Arquivos .ts** | 57 |
| **Total de Linhas TS** | ~19.510 |
| **Já em Go (crypto-engine)** | ✅ Funcionando |
| **nginx-controller (JS)** | 1 arquivo (48 linhas) |
| **Fases de Migração** | 8 |

---

## Estrutura Go Idiomática (Alvo)

```
backend/
├── cmd/
│   ├── api/main.go              ← Entry point: API Mode
│   ├── worker/main.go           ← Entry point: Worker Mode
│   └── engine/main.go           ← Entry point: Engine Mode
├── internal/
│   ├── config/
│   │   └── config.go            ← Configuração, pools, constantes
│   ├── types/
│   │   └── request.go           ← CascataRequest, contexto de tenant
│   ├── middleware/
│   │   ├── core.go              ← resolveProject, cascataAuth, requireManagement
│   │   ├── security.go          ← CORS, hostGuard, firewall, bodyParser, rateLimiter
│   │   └── logging.go           ← auditLogger, detectSemanticAction
│   ├── routes/
│   │   ├── router.go            ← Montagem principal
│   │   ├── control.go           ← Rotas do Control Plane
│   │   ├── data.go              ← Rotas do Data Plane
│   │   ├── push.go              ← Rotas Push
│   │   └── webhook.go           ← Rotas Webhook
│   ├── controllers/
│   │   ├── admin.go             ← AdminController
│   │   ├── admin_projects.go    ← AdminController (split: CRUD projetos)
│   │   ├── admin_system.go      ← AdminController (split: config sistema)
│   │   ├── ai.go
│   │   ├── backup.go
│   │   ├── branch.go
│   │   ├── data.go              ← DataController (core CRUD)
│   │   ├── data_rls.go          ← DataController (split: RLS/policies)
│   │   ├── data_schema.go       ← DataController (split: schema ops)
│   │   ├── data_auth.go
│   │   ├── data_auth_oauth.go   ← DataAuthController (split: OAuth flows)
│   │   ├── edge.go
│   │   ├── mcp.go
│   │   ├── push.go
│   │   ├── secrets.go
│   │   ├── security.go
│   │   ├── storage.go
│   │   ├── vector.go
│   │   └── webhook.go
│   ├── services/
│   │   ├── pool.go
│   │   ├── crypto.go
│   │   ├── migration.go
│   │   ├── system_log.go
│   │   ├── certificate.go
│   │   ├── auth.go
│   │   ├── auth_sql.go          ← SQL procedures (getInstallSql)
│   │   ├── database.go
│   │   ├── database_schema.go   ← Split: DDL operations
│   │   ├── ratelimit.go
│   │   ├── ratelimit_adaptive.go ← Split: adaptive/CPU logic
│   │   ├── gotrue.go
│   │   ├── storage.go
│   │   ├── storage_indexer.go
│   │   ├── queue.go
│   │   ├── cron.go
│   │   ├── webhook.go
│   │   ├── realtime.go
│   │   ├── edge.go
│   │   ├── automation.go
│   │   ├── backup.go
│   │   ├── s3_backup.go
│   │   ├── import.go
│   │   ├── extension.go
│   │   ├── mcp.go
│   │   ├── root_mcp.go
│   │   ├── ai.go
│   │   ├── gdrive.go
│   │   ├── openapi.go
│   │   ├── postgrest.go
│   │   ├── push.go
│   │   ├── push_processor.go
│   │   └── vault.go             ← Mínimo/legado (crypto-engine é o real)
│   └── utils/
│       ├── helpers.go           ← waitForDatabase, cleanTempUploads, parseBytes, etc.
│       ├── payload_crypto.go    ← Criptografia de payload
│       └── security.go          ← Utilitários de segurança
├── migrations/                  ← .sql.txt (sem mudança, ficam como estão)
├── scripts/
│   └── firewall-sync.sh         ← Sem mudança
├── go.mod
├── go.sum
└── Dockerfile.txt
```

---

## Dependências Go — Política de Segurança

> **Regra:** Priorizar Go stdlib. Pacotes externos SOMENTE se não houver alternativa na stdlib e forem amplamente validados pela comunidade/Google.

| Necessidade (Node) | Go Equivalente | Tipo |
|---|---|---|
| `express` | `net/http` + `ServeMux` (Go 1.22+) | **stdlib** ✅ |
| `dotenv` | `os.Getenv()` | **stdlib** ✅ |
| `crypto` | `crypto/aes`, `crypto/cipher`, `crypto/sha256`, `crypto/hmac` | **stdlib** ✅ |
| `multer` (upload) | `mime/multipart`, `net/http` multipart reader | **stdlib** ✅ |
| `path`, `fs`, `os` | `path/filepath`, `os`, `io/fs` | **stdlib** ✅ |
| `cluster` (multi-process) | Goroutines + `sync` | **stdlib** ✅ |
| `node-fetch` | `net/http` Client | **stdlib** ✅ |
| `uuid` | `crypto/rand` + format (ou `google/uuid`) | **stdlib**/Google |
| `pg` (node-postgres) | `jackc/pgx/v5` | Externo — padrão da indústria |
| `jsonwebtoken` | `golang-jwt/jwt/v5` | Externo — padrão da indústria |
| `ioredis` / `redis` | `redis/go-redis/v9` | Externo — padrão da indústria |
| `ws` (WebSocket) | `nhooyr.io/websocket` ou `gorilla/websocket` | Externo — padrão da indústria |
| `bcrypt` | `golang.org/x/crypto/bcrypt` | **Google oficial** ✅ |
| `argon2` | `golang.org/x/crypto/argon2` | **Google oficial** ✅ |

> [!IMPORTANT]
> **Zero pacotes NPM.** Zero `node_modules`. O Go binary será estático, compilado, sem runtime externo. Isso elimina toda uma categoria de supply chain attacks que afeta o ecossistema Node.

---

## Fases de Migração

### Fase 0 — Fundação Go (Scaffold)
> Cria o módulo Go, estrutura de diretórios, Dockerfile e go.mod.

| # | Tarefa | Status |
|---|---|---|
| 0.1 | `go mod init cascata-backend` | ⬜ |
| 0.2 | Criar estrutura de diretórios (`cmd/`, `internal/`, etc.) | ⬜ |
| 0.3 | `Dockerfile.txt` para build multi-stage Go | ⬜ |
| 0.4 | Atualizar `docker-compose.yml` (imagem Go) | ⬜ |

---

### Fase 1 — Camada Base (Zero Dependências Internas)
> Estes arquivos não dependem de nenhum outro arquivo do projeto. São os alicerces.

| # | Arquivo TS Origem | Linhas | → Arquivo(s) Go Destino | Complexidade | Status |
|---|---|---|---|---|---|
| 1.1 | `src/types.ts` | 29 | `internal/types/request.go` | 🟢 Simples | ⬜ |
| 1.2 | `src/config/main.ts` | 84 | `internal/config/config.go` | 🟢 Simples | ⬜ |
| 1.3 | `src/utils/SecurityUtils.ts` | 52 | `internal/utils/security.go` | 🟢 Simples | ⬜ |
| 1.4 | `src/utils/index.ts` | 535 | `internal/utils/helpers.go` | 🟡 Médio | ⬜ |
| 1.5 | `src/utils/PayloadCrypto.ts` | 249 | `internal/utils/payload_crypto.go` | 🟡 Médio | ⬜ |

**Subtotal Fase 1:** 5 arquivos TS → 5 arquivos Go | ~949 linhas

---

### Fase 2 — Serviços de Infraestrutura (Dependem só da Fase 1)
> Serviços fundamentais que os demais consomem. Pool de banco, crypto, migrations, logging.

| # | Arquivo TS Origem | Linhas | → Arquivo(s) Go Destino | Complexidade | Status |
|---|---|---|---|---|---|
| 2.1 | `services/PoolService.ts` | 261 | `internal/services/pool.go` | 🟡 Médio | ⬜ |
| 2.2 | `services/CryptoService.ts` | 166 | `internal/services/crypto.go` | 🟡 Médio | ⬜ |
| 2.3 | `services/MigrationService.ts` | 96 | `internal/services/migration.go` | 🟢 Simples | ⬜ |
| 2.4 | `services/SystemLogService.ts` | 234 | `internal/services/system_log.go` | 🟡 Médio | ⬜ |
| 2.5 | `services/CertificateService.ts` | 207 | `internal/services/certificate.go` | 🟡 Médio | ⬜ |
| 2.6 | `services/VaultService.ts` | 135 | `internal/services/vault.go` | 🟢 Simples | ⬜ |

**Subtotal Fase 2:** 6 arquivos TS → 6 arquivos Go | ~1.099 linhas

---

### Fase 3 — Serviços Core (Dependem das Fases 1+2)
> Os motores pesados do sistema. Auth, Database, RateLimit são os maiores e mais complexos.

| # | Arquivo TS Origem | Linhas | → Arquivo(s) Go Destino | Complexidade | Status |
|---|---|---|---|---|---|
| 3.1 | `services/RateLimitService.ts` | 980 | `internal/services/ratelimit.go` + `ratelimit_adaptive.go` | 🔴 **Complexo** | ⬜ |
| 3.2 | `services/AuthService.ts` | 938 | `internal/services/auth.go` + `auth_sql.go` | 🔴 **Complexo** | ⬜ |
| 3.3 | `services/DatabaseService.ts` | 1040 | `internal/services/database.go` + `database_schema.go` | 🔴 **Complexo** | ⬜ |
| 3.4 | `services/GoTrueService.ts` | 616 | `internal/services/gotrue.go` | 🔴 **Complexo** | ⬜ |
| 3.5 | `services/StorageService.ts` | 394 | `internal/services/storage.go` | 🟡 Médio | ⬜ |
| 3.6 | `services/QueueService.ts` | 271 | `internal/services/queue.go` | 🟡 Médio | ⬜ |
| 3.7 | `services/CronService.ts` | 69 | `internal/services/cron.go` | 🟢 Simples | ⬜ |
| 3.8 | `services/WebhookService.ts` | 105 | `internal/services/webhook.go` | 🟢 Simples | ⬜ |
| 3.9 | `services/RealtimeService.ts` | 599 | `internal/services/realtime.go` | 🔴 **Complexo** | ⬜ |

**Subtotal Fase 3:** 9 arquivos TS → 11 arquivos Go | ~5.012 linhas

> [!WARNING]
> Esta fase concentra **~50% da complexidade total**. Os 3 gigantes (RateLimit, Auth, Database) somam ~3.000 linhas de lógica densa. Recomendo migrar um por vez com testes isolados.

---

### Fase 4 — Serviços de Feature (Dependem das Fases 1+2+3)
> Serviços de funcionalidade específica. Cada um é relativamente auto-contido.

| # | Arquivo TS Origem | Linhas | → Arquivo(s) Go Destino | Complexidade | Status |
|---|---|---|---|---|---|
| 4.1 | `services/EdgeService.ts` | 291 | `internal/services/edge.go` | 🟡 Médio | ⬜ |
| 4.2 | `services/AutomationService.ts` | 784 | `internal/services/automation.go` | 🔴 Complexo | ⬜ |
| 4.3 | `services/BackupService.ts` | 353 | `internal/services/backup.go` | 🟡 Médio | ⬜ |
| 4.4 | `services/S3BackupService.ts` | 159 | `internal/services/s3_backup.go` | 🟡 Médio | ⬜ |
| 4.5 | `services/ImportService.ts` | 552 | `internal/services/import.go` | 🟡 Médio | ⬜ |
| 4.6 | `services/ExtensionService.ts` | 689 | `internal/services/extension.go` | 🔴 Complexo | ⬜ |
| 4.7 | `services/McpService.ts` | 374 | `internal/services/mcp.go` | 🟡 Médio | ⬜ |
| 4.8 | `services/RootMcpService.ts` | 116 | `internal/services/root_mcp.go` | 🟢 Simples | ⬜ |
| 4.9 | `services/AiService.ts` | 116 | `internal/services/ai.go` | 🟢 Simples | ⬜ |
| 4.10 | `services/GDriveService.ts` | 240 | `internal/services/gdrive.go` | 🟡 Médio | ⬜ |
| 4.11 | `services/OpenApiService.ts` | 296 | `internal/services/openapi.go` | 🟡 Médio | ⬜ |
| 4.12 | `services/PostgrestService.ts` | 353 | `internal/services/postgrest.go` | 🟡 Médio | ⬜ |
| 4.13 | `services/StorageIndexer.ts` | 138 | `internal/services/storage_indexer.go` | 🟢 Simples | ⬜ |
| 4.14 | `services/PushService.ts` | 80 | `internal/services/push.go` | 🟢 Simples | ⬜ |
| 4.15 | `services/PushProcessor.ts` | 73 | `internal/services/push_processor.go` | 🟢 Simples | ⬜ |

**Subtotal Fase 4:** 15 arquivos TS → 15 arquivos Go | ~4.614 linhas

---

### Fase 5 — Camada de Middleware (Dependem dos Services)
> Os middlewares consomem serviços para resolver projetos, autenticar, limitar taxa, etc.

| # | Arquivo TS Origem | Linhas | → Arquivo(s) Go Destino | Complexidade | Status |
|---|---|---|---|---|---|
| 5.1 | `src/middlewares/core.ts` | 487 | `internal/middleware/core.go` | 🔴 **Complexo** | ⬜ |
| 5.2 | `src/middlewares/security.ts` | 241 | `internal/middleware/security.go` | 🟡 Médio | ⬜ |
| 5.3 | `src/middlewares/logging.ts` | 187 | `internal/middleware/logging.go` | 🟡 Médio | ⬜ |

**Subtotal Fase 5:** 3 arquivos TS → 3 arquivos Go | ~915 linhas

---

### Fase 6 — Controllers (Dependem de Middlewares + Services)
> Handlers HTTP. Cada controller chama serviços e usa o contexto preparado pelos middlewares.

| # | Arquivo TS Origem | Linhas | → Arquivo(s) Go Destino | Complexidade | Status |
|---|---|---|---|---|---|
| 6.1 | `controllers/AdminController.ts` | 1079 | `admin.go` + `admin_projects.go` + `admin_system.go` | 🔴 **Complexo** | ⬜ |
| 6.2 | `controllers/DataController.ts` | 1360 | `data.go` + `data_rls.go` + `data_schema.go` | 🔴 **Complexo** | ⬜ |
| 6.3 | `controllers/DataAuthController.ts` | 978 | `data_auth.go` + `data_auth_oauth.go` | 🔴 **Complexo** | ⬜ |
| 6.4 | `controllers/BranchController.ts` | 716 | `branch.go` | 🔴 Complexo | ⬜ |
| 6.5 | `controllers/StorageController.ts` | 487 | `storage.go` | 🟡 Médio | ⬜ |
| 6.6 | `controllers/McpController.ts` | 320 | `mcp.go` | 🟡 Médio | ⬜ |
| 6.7 | `controllers/SecurityController.ts` | 272 | `security.go` | 🟡 Médio | ⬜ |
| 6.8 | `controllers/BackupController.ts` | 255 | `backup.go` | 🟡 Médio | ⬜ |
| 6.9 | `controllers/WebhookController.ts` | 143 | `webhook.go` | 🟢 Simples | ⬜ |
| 6.10 | `controllers/AiController.ts` | 134 | `ai.go` | 🟢 Simples | ⬜ |
| 6.11 | `controllers/SecretsController.ts` | 123 | `secrets.go` | 🟢 Simples | ⬜ |
| 6.12 | `controllers/PushController.ts` | 111 | `push.go` | 🟢 Simples | ⬜ |
| 6.13 | `controllers/EdgeController.ts` | 101 | `edge.go` | 🟢 Simples | ⬜ |
| 6.14 | `controllers/VectorController.ts` | 49 | `vector.go` | 🟢 Simples | ⬜ |

**Subtotal Fase 6:** 14 arquivos TS → 19 arquivos Go | ~6.128 linhas

---

### Fase 7 — Rotas + Entry Points (Integração Final)
> Monta tudo junto: rotas conectam controllers, e os entry points bootam o servidor.

| # | Arquivo TS Origem | Linhas | → Arquivo(s) Go Destino | Complexidade | Status |
|---|---|---|---|---|---|
| 7.1 | `src/routes/index.ts` | 16 | `internal/routes/router.go` | 🟢 Simples | ⬜ |
| 7.2 | `src/routes/control.routes.ts` | 99 | `internal/routes/control.go` | 🟢 Simples | ⬜ |
| 7.3 | `src/routes/data.routes.ts` | 192 | `internal/routes/data.go` | 🟡 Médio | ⬜ |
| 7.4 | `src/routes/push.routes.ts` | 22 | `internal/routes/push.go` | 🟢 Simples | ⬜ |
| 7.5 | `src/routes/webhook.routes.ts` | 16 | `internal/routes/webhook.go` | 🟢 Simples | ⬜ |
| 7.6 | `server.ts` | 455 | `cmd/api/main.go` + `cmd/worker/main.go` + `cmd/engine/main.go` | 🔴 **Complexo** | ⬜ |

**Subtotal Fase 7:** 6 arquivos TS → 8 arquivos Go | ~800 linhas

> [!NOTE]
> O `server.ts` atual tem 3 modos (API, Worker, Engine) num único arquivo. Em Go, cada modo vira seu próprio `main.go` em `cmd/`, que é o padrão idiomático.

---

### Fase 8 — Auxiliares (Independentes)
> Componentes satélite que não fazem parte do backend principal.

| # | Arquivo Origem | Linhas | → Arquivo(s) Go Destino | Complexidade | Status |
|---|---|---|---|---|---|
| 8.1 | `nginx-controller/index.js` | 48 | `nginx-controller/main.go` | 🟢 Simples | ⬜ |

**Subtotal Fase 8:** 1 arquivo JS → 1 arquivo Go | ~48 linhas

---

## Resumo de Progresso Global

| Fase | Arquivos TS | → Arquivos Go | Linhas TS | Status |
|---|---|---|---|---|
| **0 — Scaffold** | — | 4 | — | ⬜ Pendente |
| **1 — Base** | 5 | 5 | 949 | ⬜ Pendente |
| **2 — Infra Services** | 6 | 6 | 1.099 | ⬜ Pendente |
| **3 — Core Services** | 9 | 11 | 5.012 | ⬜ Pendente |
| **4 — Feature Services** | 15 | 15 | 4.614 | ⬜ Pendente |
| **5 — Middlewares** | 3 | 3 | 915 | ⬜ Pendente |
| **6 — Controllers** | 14 | 19 | 6.128 | ⬜ Pendente |
| **7 — Routes + Server** | 6 | 8 | 800 | ⬜ Pendente |
| **8 — Auxiliares** | 1 | 1 | 48 | ⬜ Pendente |
| **TOTAL** | **57 (+2 não-TS)** | **~72** | **~19.565** | ⬜ 0% |

---

## Regras de Migração por Arquivo

1. **Ler o .ts inteiro** antes de escrever qualquer .go
2. **Nunca traduzir literalmente** — adaptar à arquitetura Go (structs, interfaces, error handling)
3. **Um arquivo por vez** — entregar compilável, mesmo que isolado
4. **Marcar ✅ no checklist** após cada arquivo convertido
5. **Se 1 TS vira N Go** — converter todos os N antes de marcar completo
6. **Testar compilação** (`go build ./...`) após cada arquivo
7. **Zero `any`/`interface{}`** sempre que possível — Go forte e tipado
8. **Error handling explícito** — sem `try/catch` genérico, cada erro tratado
9. **Migrations .sql.txt** — não migrar, ficam como estão (são SQL puro)

## O Que NÃO Muda

| Item | Motivo |
|---|---|
| `migrations/*.sql.txt` | SQL puro, agnóstico de linguagem |
| `scripts/firewall-sync.sh` | Shell script, já é portável |
| `frontend/` (TSX) | Continua React/TSX, sem alteração |
| `crypto-engine/` | **Já é Go** ✅ |
| Banco PostgreSQL | Infraestrutura Docker, sem mudança |
| Nginx configs | Infraestrutura Docker, sem mudança |
| `docker-compose.yml` | Atualiza só a imagem do backend |

---

## Ganhos Esperados com Go

| Aspecto | Node/TS (Atual) | Go (Alvo) |
|---|---|---|
| **startup a frio** | ~2-5s | ~50-200ms |
| **memória por worker** | ~80-150MB | ~10-30MB |
| **concorrência** | cluster de 4 processos | goroutines (milhares) |
| **binary size** | node_modules ~200MB+ | ~15-30MB estático |
| **supply chain attack** | 500+ pacotes npm | ~5 deps externas |
| **type safety** | compile-time (mas `any`) | compile-time (zero `any`) |
| **GC pauses** | imprevisível (V8) | <1ms p99 |
