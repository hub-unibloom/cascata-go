# 🚨 Panic Mode - Documentação Técnica Completa

## Visão Geral

O **Panic Mode** (modo de emergência) é um sistema de **defesa de borda (Edge Defense)** que permite aos administradores bloquear instantaneamente todos os acessos a um projeto, exceto para o próprio administrador que ativou o bloqueio. Este sistema opera em **memória (Dragonfly/Redis)**, sem depender do PostgreSQL, garantindo resposta imediata mesmo sob ataque.

### Propósito

- **Kill switch de emergência**: Bloqueio instantâneo em caso de comprometimento
- **Isolamento de incidentes**: Contenção de ataques em andamento
- **Manutenção segura**: Permite trabalho administrativo sem tráfego de usuários
- **Compliance**: Proteção regulatória durante análises forenses

---

## 🏗️ Arquitetura Técnica

### Diagrama de Fluxo

```
┌───────────────────────────────────────────────────────────────────────────┐
│                         REQUEST INCOMING                                  │
└───────────────────────────────┬───────────────────────────────────────────┘
                                │
                                ▼
┌───────────────────────────────────────────────────────────────────────────┐
│  MIDDLEWARE CHAIN (Chi Router)                                            │
│  ─────────────────────────────────────────────────────────────────────    │
│  1. HostGuard → 2. AuditLogger → 3. CascataAuth → 4. PANIC MODE → 5.      │
│     DynamicRateLimiter → 6. DynamicBodyParser → Handler Final             │
└───────────────────────────────┬───────────────────────────────────────────┘
                                │
                                ▼
┌─────────────────────────────────────────────────────────────────────────┐
│  PANIC MODE MIDDLEWARE (internal/middleware/security.go)                │
│  ─────────────────────────────────────────────────────────────────────  │
│                                                                         │
│  ┌─────────────┐    ┌─────────────┐    ┌─────────────┐                  │
│  │ Healthcheck │    │ No Project  │    │ Panic Check │                  │
│  │   Skip?     │───▶│   Context?  │───▶│  Dragonfly  │                  │
│  │  (/) (/health)   │   (bypass)  │    │             │                  │
│  └─────────────┘    └─────────────┘    └──────┬──────┘                  │
│                                                 │                       │
│                    ┌────────────────────────────┼──────────────────┐    │
│                    │                            │                  │    │
│                    ▼                            ▼                  │    │
│           ┌─────────────┐              ┌─────────────┐             │    │
│           │   NORMAL    │              │  LOCKDOWN   │             │    │
│           │  Continue   │              │   ACTIVE    │             │    │
│           └─────────────┘              └──────┬──────┘             │    │
│                                               │                    │    │
│                    ┌──────────────────────────┼───────────┐        │    │
│                    │                          │           │        │    │
│                    ▼                          ▼           ▼        │    │
│           ┌───────────────┐        ┌─────────────┐ ┌──────────┐    │    │
│           │  Whitelist    │        │ System      │ │ Bloquear │    │    │
│           │  Check        │        │ Request?    │ │  503     │────┘    │
│           │  (IP/UserID)  │        │  (bypass)   │ │          │         │
│           └──────┬────────┘        └──────┬──────┘ └──────────┘         │
│                  │                        │                             │
│         ┌────────┴────────┐        ┌──────┴────────┐                    │
│         ▼                 ▼        ▼               ▼                    │
│   ┌──────────┐      ┌──────────┐ ┌──────────┐ ┌──────────┐              │
│   │ Permite  │      │ Permite  │ │ Bloquear │ │ Bloquear │              │
│   │  (Admin) │      │  (API)   │ │   503    │ │   503    │              │
│   └──────────┘      └──────────┘ └──────────┘ └──────────┘              │
└─────────────────────────────────────────────────────────────────────────┘
```

### Componentes Principais

#### 1. Dragonfly (Camada de Cache In-Memory)

O Dragonfly (banco Redis-compatível) armazena o estado do panic mode em memória:

```go
// Chaves Redis utilizadas:
panic:{slug}           → "true" (indica panic ativo)
panic:admin:{slug}     → "user_id_ou_ip" (identificador do admin whitelisted)
rps:{slug}             → "123" (requests por segundo, para dashboard)
```

**Vantagens do Dragonfly:**
- Resposta em microssegundos (vs milissegundos do PostgreSQL)
- Sem conexões persistentes com banco principal
- Funciona mesmo se PostgreSQL estiver sob ataque DDoS

#### 2. Middleware `PanicMode` (security.go)

```go
func PanicMode(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        // 1. Skip healthchecks
        if r.URL.Path == "/" || r.URL.Path == "/health" {
            next.ServeHTTP(w, r)
            return
        }

        // 2. Verifica panic no Dragonfly
        if services.CheckPanic(ctx.Project.Slug) {
            // 3. Identifica o requester (IP ou UserID do JWT)
            identifier := getClientIP(r)
            if ctx.User != nil {
                if sub, ok := ctx.User["sub"].(string); ok {
                    identifier = sub  // Prefere UserID do JWT
                }
            }

            // 4. Verifica whitelist
            if services.IsAdminWhitelisted(ctx.Project.Slug, identifier) {
                next.ServeHTTP(w, r)  // Admin ativador passa
                return
            }

            // 5. Bloqueia com 503
            w.WriteHeader(http.StatusServiceUnavailable)
            json.NewEncoder(w).Encode(map[string]interface{}{
                "error": "System is currently in Panic Mode (Locked Down).",
                "code":  "PANIC_MODE",
            })
            return
        }

        next.ServeHTTP(w, r)
    })
}
```

#### 3. Services de Rate Limit (ratelimit.go)

Funções core do panic mode:

```go
// CheckPanic - verifica se projeto está em lockdown
func CheckPanic(slug string) bool {
    val, err := dragonfly.Get(context.Background(), "panic:"+slug).Result()
    return err == nil && val == "true"
}

// SetPanic - ativa/desativa panic mode
func SetPanic(slug string, state bool, adminIdentifier string) error {
    key := "panic:" + slug
    adminKey := "panic:admin:" + slug
    
    if state {
        // Ativa: seta flag e salva identificador do admin
        pipe := dragonfly.Pipeline()
        pipe.Set(context.Background(), key, "true", 0)
        pipe.Set(context.Background(), adminKey, adminIdentifier, 0)
        _, err := pipe.Exec(context.Background())
        return err
    }
    // Desativa: deleta as chaves
    return dragonfly.Del(context.Background(), key, adminKey).Err()
}

// IsAdminWhitelisted - verifica se requester pode bypassar
func IsAdminWhitelisted(slug, identifier string) bool {
    admin, err := dragonfly.Get(context.Background(), "panic:admin:"+slug).Result()
    return err == nil && admin == identifier
}

// TrackGlobalRPS - incrementa contador para dashboard
func TrackGlobalRPS(slug string) {
    pipe := dragonfly.Pipeline()
    pipe.Incr(context.Background(), "rps:"+slug)
    pipe.Expire(context.Background(), "rps:"+slug, time.Second)
    pipe.Exec(context.Background())
}
```

#### 4. Security Controller (security.go)

Endpoints da API REST:

```go
// GET /api/data/{slug}/security/status
func (s *SecurityController) GetStatus(w http.ResponseWriter, r *http.Request) {
    panicMode := services.CheckPanic(ctx.Project.Slug)
    currentRps := services.GetCurrentRPS(ctx.Project.Slug)
    
    response := map[string]interface{}{
        "current_rps": currentRps,
        "panic_mode":  panicMode,
    }
    
    if panicMode {
        adminWhitelisted := services.GetPanicAdmin(ctx.Project.Slug)
        response["whitelisted_admin"] = adminWhitelisted
        
        // Verifica se usuário atual é o whitelisted
        currentUser := ""
        if ctx.User != nil {
            if sub, ok := ctx.User["sub"].(string); ok {
                currentUser = sub
            }
        }
        response["you_are_whitelisted"] = (adminWhitelisted == currentUser)
    }
    
    json.NewEncoder(w).Encode(response)
}

// POST /api/data/{slug}/security/panic
func (s *SecurityController) TogglePanic(w http.ResponseWriter, r *http.Request) {
    var body struct{ Enabled bool `json:"enabled"` }
    json.NewDecoder(r.Body).Decode(&body)

    // Captura identificador do admin para whitelist
    adminIdentifier := getClientIP(r)
    if ctx.User != nil {
        if sub, ok := ctx.User["sub"].(string); ok && sub != "" {
            adminIdentifier = sub  // Prefere UserID do JWT
        }
    }

    // 1. Set no Dragonfly (efeito imediato - edge defense)
    services.SetPanic(ctx.Project.Slug, body.Enabled, adminIdentifier)

    // 2. Persiste no PostgreSQL (durabilidade entre restarts)
    // Non-blocking: mesmo se DB falhar, Dragonfly mantém estado
    go func() {
        services.SystemPool.Exec(r.Context(),
            "UPDATE system.projects SET metadata = jsonb_set(...)",
            fmt.Sprintf("%t", body.Enabled), ctx.Project.Slug)
    }()

    json.NewEncoder(w).Encode(map[string]interface{}{
        "success":     true,
        "panic_mode":  body.Enabled,
        "edge_synced": true,
        "whitelisted": adminIdentifier,
    })
}
```

---

## 🎯 Casos de Uso e Exemplos

### Caso 1: Ataque em Andamento (Data Exfiltration)

**Cenário:** Detectou-se comportamento anômalo - alguém está fazendo dump massivo da tabela `users` via API.

**Ação:** Ativar panic mode imediatamente.

```bash
# Via CLI (admin já logado no dashboard)
curl -X POST "https://api.seudominio.com/api/data/prod-api/security/panic" \
  -H "Authorization: Bearer {admin_token}" \
  -H "Content-Type: application/json" \
  -d '{"enabled": true}'

# Resposta:
{
  "success": true,
  "panic_mode": true,
  "edge_synced": true,
  "whitelisted": "admin@empresa.com"  // UserID do JWT
}
```

**Efeito:**
- ✅ Todas as requisições de usuários bloqueadas (503)
- ✅ Admin que ativou continua com acesso (whitelisted)
- ✅ Healthchecks continuam funcionando (monitoramento)
- ✅ System requests (automações internas) passam

---

### Caso 2: Manutenção Crítica (Sem Janela de Manutenção)

**Cenário:** Precisa alterar estrutura do banco sem risco de writes concorrentes.

**Ação:** Ativar panic, fazer alterações, desativar.

```bash
# 1. Ativa panic
./scripts/panic-reset.sh --cli panic-enable meu-projeto

# 2. Faz manutenção (apenas você tem acesso)
psql -h localhost -U admin -d cascata_meu-projeto
> ALTER TABLE sensitive_data ADD COLUMN encrypted_field TEXT;

# 3. Desativa via script de reset (ou dashboard)
./scripts/panic-reset.sh meu-projeto
```

---

### Caso 3: Perda de Acesso ao Dashboard (Admin Lockout)

**Cenário:** Admin ativou panic mode, mas perdeu acesso (sessão expirou, IP mudou, celular com OTP quebrado).

**Ação:** Usar script de emergência `panic-reset.sh`.

```bash
# No servidor (requer acesso SSH com OTP configurado)
cd ~/cascata
./scripts/panic-reset.sh hteste2233

# Saída:
🔒 Projetos em PANIC MODE:
  • hteste2233
    Admin: admin@empresa.com
    RPS: 0

⏳ Desativando panic mode para 'hteste2233'...

✅ Panic mode desativado com sucesso!
   Projeto: hteste2233

ℹ️  O projeto agora aceita requests normalmente.
```

---

### Caso 4: Monitoramento de RPS Durante Incidente

```bash
# Verificar status atual
curl "https://api.seudominio.com/api/data/prod-api/security/status" \
  -H "Authorization: Bearer {token}"

# Resposta com panic ativo:
{
  "current_rps": 1472,  // Ainda havia tráfego antes do bloqueio
  "panic_mode": true,
  "whitelisted_admin": "admin@empresa.com",
  "you_are_whitelisted": true
}
```

---

## 🔧 Comandos Práticos

### Via Dashboard Web

1. Acesse **Security → Panic Mode**
2. Toggle switch **"Emergency Lockdown"**
3. Confirme com OTP de 6 dígitos
4. Dashboard mostrará banner vermelho **"PANIC MODE ACTIVE"**

### Via API REST

```bash
# Verificar status
curl -X GET "https://api.seudominio.com/api/data/{slug}/security/status" \
  -H "apikey: {service_key}" \
  -H "Authorization: Bearer {admin_jwt}"

# Ativar panic
curl -X POST "https://api.seudominio.com/api/data/{slug}/security/panic" \
  -H "apikey: {service_key}" \
  -H "Authorization: Bearer {admin_jwt}" \
  -H "Content-Type: application/json" \
  -d '{"enabled": true}'

# Desativar panic
curl -X POST "https://api.seudominio.com/api/data/{slug}/security/panic" \
  -H "apikey: {service_key}" \
  -H "Authorization: Bearer {admin_jwt}" \
  -H "Content-Type: application/json" \
  -d '{"enabled": false}'
```

### Via CLI de Emergência

```bash
# Compilar CLI (primeira vez)
cd ~/cascata/backend
go build -o cascata-cli ./cmd/cli/main.go

# Modos de uso:

# 1. Desativar panic (modo automático - detecta estratégia)
./scripts/panic-reset.sh nome-do-projeto

# 2. Usar CLI Go diretamente (requer DRAGONFLY_HOST)
DRAGONFLY_HOST=localhost ./backend/cascata-cli panic-reset nome-projeto

# 3. Verificar status
./backend/cascata-cli panic-status nome-projeto

# 4. Listar projetos em panic
./backend/cascata-cli panic-list
```

---

## 🛡️ Segurança e Acesso

### Quem Pode Ativar/Desativar?

| Papel | Ação | Detalhes |
|-------|------|----------|
| `service_role` | ✅ Ativar/Desativar | Admin com JWT válido |
| `authenticated` | ❌ Não pode | Usuários comuns |
| `anon` | ❌ Não pode | Acesso anônimo |
| SSH + OTP | ✅ Reset emergencial | Via `panic-reset.sh` no servidor |

### Whitelisting - Como Funciona?

Quando o panic é ativado, o sistema captura o **identificador** do admin:

```
┌─────────────────────────────────────────────────────────────┐
│  Identificador do Admin (ordem de prioridade)               │
├─────────────────────────────────────────────────────────────┤
│  1. JWT claim "sub" (UserID) ← mais confiável               │
│     Ex: "admin@empresa.com" ou "uuid-do-usuario"            │
│                                                             │
│  2. IP do cliente (fallback)                                │
│     Ex: "191.202.114.197"                                   │
│     Headers verificados: X-Real-Ip, X-Forwarded-For         │
└─────────────────────────────────────────────────────────────┘
```

**Importante:** Se o admin usar UserID (JWT), ele pode mudar de IP e continuar acessando. Se usar IP, deve manter o mesmo IP.

### Proteção do Terminal (SSH OTP)

O acesso ao script `panic-reset.sh` requer:

1. **Chave SSH** cadastrada no provedor (AWS/GCP/Azure)
2. **OTP TOTP** (Google/Microsoft Authenticator) - configurado pelo `install.sh.txt`
3. **Permissão** no grupo `docker` (ou `sudo` para `docker exec`)

**Fluxo SSH:**
```
ssh -i chave.pem ubuntu@ip-da-vps
├─▶ Challenge: Authenticator code: ______  ← Digita OTP de 6 dígitos
└─▶ Acesso concedido ao terminal
```

Isso garante que mesmo que alguém roube a chave SSH, precisa do celular com o app de autenticação.

---

## ⚙️ Configuração e Persistência

### Estado em Memória vs Persistente

| Camada | Tecnologia | Persistência | Propósito |
|--------|-----------|--------------|-----------|
| **Edge** | Dragonfly (Redis) | ❌ Volátil (RAM) | Lockdown imediato, performance |
| **Durable** | PostgreSQL | ✅ Persistente | Recuperação após restart |

**Sincronização:**

```go
// Ao ativar panic:
// 1. Dragonfly: imediato (sub-milissegundo)
services.SetPanic(slug, true, adminID)

// 2. PostgreSQL: background (não bloqueia)
go func() {
    db.Exec("UPDATE system.projects SET metadata = ...")
}()
```

### Recuperação Após Restart

Quando o backend reinicia, verifica projetos com panic ativo no PostgreSQL:

```sql
SELECT slug, metadata->'security'->>'panic_mode' 
FROM system.projects 
WHERE metadata->'security'->>'panic_mode' = 'true';
```

E sincroniza com Dragonfly automaticamente.

---

## 🚨 Troubleshooting

### Erro: "não foi possível conectar ao Dragonfly"

```
dial tcp: lookup dragonfly on 127.0.0.53:53: server misbehaving
```

**Causa:** CLI tentando resolver hostname `dragonfly` fora da rede Docker.

**Solução:**
```bash
# Opção 1: Usar localhost (porta mapeada no docker-compose)
DRAGONFLY_HOST=localhost ./scripts/panic-reset.sh --cli panic-reset projeto

# Opção 2: Modo automático (recomendado - detecta estratégia)
./scripts/panic-reset.sh projeto

# Opção 3: Dentro do container
docker exec cascata-backend /app/cascata-cli panic-reset projeto
```

### Erro: "O projeto não está em panic mode"

Verifique se está usando o slug correto:

```bash
# Lista projetos em panic
./scripts/panic-reset.sh --list

# Verifica status específico
./scripts/panic-reset.sh --status meu-projeto
```

### Panic Ativo mas Requests Passando

Possíveis causas:
1. **Healthcheck** - URLs `/` e `/health` sempre passam (intencional)
2. **System Request** - Requisições internas com `IsSystemRequest: true` passam
3. **Whitelisted** - Você é o admin que ativou (verifique `you_are_whitelisted: true`)
4. **Cache** - Dragonfly pode ter sido reiniciado. Verifique no dashboard.

---

## 📊 Métricas e Observabilidade

### Métricas Disponíveis

```bash
# RPS (Requests Per Second)
GET rps:{slug}  → valor inteiro, auto-expira em 1 segundo

# Estado do panic
GET panic:{slug}  → "true" ou nil

# Admin whitelisted
GET panic:admin:{slug}  → identificador do admin
```

### Dashboard

No painel **Security → Overview**:
- 🟢 **Online**: Projeto operando normalmente
- 🔴 **Panic Mode**: Projeto em lockdown (apenas admin whitelisted)
- 📊 **RPS Gauge**: Requests por segundo em tempo real

---

## 🔗 Integração com Outros Sistemas

### Webhooks de Segurança

Quando panic mode é ativado, evento é emitido:

```json
{
  "event": "security.panic_enabled",
  "project": "prod-api",
  "admin": "admin@empresa.com",
  "timestamp": "2026-04-14T21:30:00Z",
  "rps_at_activation": 1472
}
```

### Automações

Exemplo: Slack notification quando panic ativado:

```go
// No handler TogglePanic, após ativação:
if body.Enabled {
    slack.Send(fmt.Sprintf(
        "🚨 PANIC MODE ativado para %s por %s (RPS: %d)",
        ctx.Project.Slug, adminIdentifier, currentRps
    ))
}
```

---

## 📚 Referências Técnicas

### Arquivos Fonte (Go)

| Arquivo | Função | Descrição |
|---------|--------|-----------|
| `internal/middleware/security.go:222-281` | `PanicMode()` | Middleware de bloqueio |
| `internal/services/ratelimit.go:680-769` | `CheckPanic()`, `SetPanic()`, `IsAdminWhitelisted()` | Core logic |
| `internal/controllers/security.go:62-131` | `GetStatus()`, `TogglePanic()` | API REST endpoints |
| `cmd/server/main.go:161` | Middleware registration | Cadeia de middlewares |
| `scripts/panic-reset.sh` | Emergency CLI | Reset de emergência via SSH |
| `cmd/cli/main.go` | Go CLI | CLI compilado para operações |

### Rotas da API

| Método | Rota | Descrição |
|--------|------|-----------|
| GET | `/api/data/{slug}/security/status` | Status atual e RPS |
| POST | `/api/data/{slug}/security/panic` | Ativar/desativar panic |

---

## 📝 Changelog

- **v3.0** (2026-04): Implementação completa em Go
  - Migração do middleware TypeScript para Go
  - CLI de emergência `cascata-cli`
  - Script `panic-reset.sh` com múltiplas estratégias de conexão
  - Whitelisting por UserID (JWT) ou IP

---

## 💡 Boas Práticas

1. **Teste o procedimento** antes do incidente real
   ```bash
   # Faça um teste em staging
   ./scripts/panic-reset.sh --status projeto-staging
   ```

2. **Guarde os códigos de recuperação SSH** offline (impressos)

3. **Monitore RPS** - picos súbitos podem indicar ataque antes do panic

4. **Documente o contato** do admin com acesso SSH + OTP para emergências

5. **Não deixe panic ativo por muito tempo** - afeta disponibilidade do serviço

---

**Documentação mantida pela equipe Cascata. Última atualização: Abril 2026.**
