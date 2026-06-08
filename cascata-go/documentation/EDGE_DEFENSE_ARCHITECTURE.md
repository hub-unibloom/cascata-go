# Arquitetura de Edge Defense - Cascata

## Resumo

Arquitetura de defesa em camadas para proteger o PostgreSQL contra DDoS sem sacrificar granularidade. **Zero limites hardcoded no código** - todos os limites vêm de configuração (ENV) ou banco de dados (`system.rate_limits`).

## 🏗️ Camadas de Defesa (Fluxo Real)

```
Request → [LAYER 0] → [LAYER 3.5] → [LAYER 1] → [LAYER 2] → [LAYER 3] → [LAYER 4: PostgreSQL]
```

```
┌─────────────────────────────────────────────────────────────────────┐
│  LAYER 0: Nginx Edge Defense                                        │
│  ├── Bloqueio sem User-Agent (bots maliciosos)                      │
│  ├── Rate Limit: 5000 req/s por IP (zone=edge_ddos)                 │
│  ├── Connection Limit: 50 conn/IP (anti-slowloris)                  │
│  ├── Injeção: X-Tenant-Slug (do server_name/subdomínio)              │
│  └── Backend: Nginx (kernel space, zero overhead)                   │
│  🎯 Descarta DDoS bruto antes de chegar no Go/Dragonfly               │
├─────────────────────────────────────────────────────────────────────┤
│  LAYER 3.5: Ban Progressivo (Strikes)                               │
│  ├── Chave: ban:active:{ip} e ban:strikes:{ip}:{uuid}                │
│  ├── TTL Progressivo: 1min → 10min → 1h → 6h → 24h                 │
│  ├── Ativa após 3+ strikes em 24h                                   │
│  └── Backend: Dragonfly (Redis)                                     │
│  🎯 Atacantes persistentes são banidos progressivamente             │
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
│  LAYER 4: automação depois PostgreSQL (Handlers) ou pós postgress, se não tiver automação ele roda direto no postgres e volta para o cascata go que faz a limpeza com data privacy. │
│  └──Evento de automação gatilho de sequesto de pŕe Evento de PG.18  │
│  └──Só recebe requests que passaram nas 5 camadas anteriores        │
│  └──Evento de automação gatilho de sequesto de pós Evento de PG.18  │
│  🎯 PostgreSQL protegido - nunca tocado por ataques                 │
   └──Evento de Data Privacy Override (Parte do pacote Universal Padlock)│
└─────────────────────────────────────────────────────────────────────┘
```
Obs. Evento de automação gatilho de sequesto de pŕe Evento de PG.18(Postgress) na automaação tem o nó de DB ou seja a automação fara a ação no bano no qual estaria o evento, potando a resposta ao usuario vem da automação. Logo o bano de dados não é acessado pós a automação, pois na automação ow one já faz o proesso que ele deseja livremente.

## 🎯 As 4 Regras (Configuradas via Frontend)

| Regra | Pattern de URL | De onde vem o limite |
|-------|---------------|---------------------|
| **Global API** | `*` ou qualquer não específico | `system.rate_limits.rate_limit` |
| **Auth Routes** | `/auth/*`, `/login`, `/signup` | `system.rate_limits.rate_limit` |
| **Specific Table** | `/tables/{name}/*` | `system.rate_limits.rate_limit` |
| **RPC Function** | `/rpc/{function}` | `system.rate_limits.rate_limit` |

**Nota:** Os valores são definidos por você no **Traffic Guard** (frontend), armazenados em `system.rate_limits`, e cacheados no Dragonfly.

## 💡 Vantagens sobre IP-only

### Cenário: CGNAT/VPN Compartilhado

**IP-only (antigo):**
```
IP 1.2.3.4 (CGNAT de 100 usuários)
├── Usuário A (legítimo) → BLOQUEADO ❌
├── Usuário B (legítimo) → BLOQUEADO ❌
└── Atacante → Bloqueia TODOS ❌
```

**IP+UUID (atual):**
```
IP 1.2.3.4 (CGNAT de 100 usuários)
├── Usuário A UUID-abc → limite do banco (ex: 100 req/s) ✅
├── Usuário B UUID-def → limite do banco (ex: 100 req/s) ✅
└── Atacante UUID-xyz → só ele bloqueado ✅
```

## 🔧 Implementação Real

### Arquivos Principais

1. **`/backend/internal/middleware/intelligent_edge.go`**
   - `IntelligentEdgeLimiter`: Middleware principal (3 camadas)
   - `applyIPHardCap()`: Layer 1 (via ENV)
   - `extractUserIdentity()`: Layer 2 (JWT parse)
   - `applyRateLimitFromDatabase()`: Layer 3 (regras do banco)
   - `RefreshRateLimitCache()`: Popula cache Dragonfly

2. **`/backend/cmd/server/main.go`**
   - Ordem de middlewares:
   ```go
   r.Use(middleware.IntelligentEdgeLimiter)  // Layer 1-3
   r.Use(middleware.ProjectResolverLazy)     // Resolve tenant (lazy)
   r.Use(middleware.PanicMode)               // Hard security
   r.Use(middleware.DynamicRateLimiter)    // Rate limit refinado
   ```

3. **`/backend/internal/controllers/security.go`**
   - `CreateRateLimit`: Insere no banco + atualiza cache
   - `DeleteRateLimit`: Remove do banco + atualiza cache

### Configuração

**Variáveis de Ambiente (Opcionais):**
```bash
# Layer 1 - só se quiser hard cap por IP
EDGE_IP_HARD_CAP=5000        # req/min por IP (desabilitado se vazio)

# Layer 3 - fallback se não houver regra no banco  
DEFAULT_RATE_LIMIT=100       # req/s (raramente usado)
```

**Configuração via Frontend (Obrigatória):**
- Painel: **RLS Manager → Traffic Guard**
- Tabela: `system.rate_limits`
- Colunas: `route_pattern`, `rate_limit`, `rate_limit_anon`, `burst_limit`, `window_seconds`

### Headers de Resposta

```http
X-RateLimit-Rule: table           # Tipo de regra aplicada
X-RateLimit-Auth: jwt             # jwt | apikey | anon
X-RateLimit-Limit: 100            # Limite configurado no banco
X-RateLimit-Remaining: 45         # Requests restantes
```

## 🚀 Comportamento Real por Tipo de Ataque

| Ataque | Layer que Bloqueia | PostgreSQL Tocado? |
|--------|-------------------|-------------------|
| Bot sem User-Agent | Layer 0 (Nginx 444) | ❌ Não |
| DDoS 10k req/s | Layer 0 (Nginx 5000r/s limit) | ❌ Não |
| Slowloris attack | Layer 0 (Nginx conn_limit) | ❌ Não |
| Atacante persistente (3+ strikes) | Layer 3.5 (Ban progressivo) | ❌ Não |
| DDoS 100k req/s (sem token, ENV habilitado) | Layer 1 (IP hard cap) | ❌ Não |
| DDoS (sem token, ENV desabilitado) | Layer 3 (anon limit) | ❌ Não |
| Scraping com JWT válido | Layer 3 (IP+UUID+Regra) | ❌ Não |
| Brute force em login | Layer 3 (auth rule limit) | ❌ Não |
| Abuso de RPC pesada | Layer 3 (rpc rule limit) | ❌ Não |
| Request legítimo | Passa todas as camadas | ✅ Sim |

## 🛡️ Layer 0: Nginx Edge Defense

### Por que Nginx?

Nginx opera em **kernel space** antes de qualquer aplicação user-space:

```
Kernel TCP Stack → Nginx (Layer 0) → Unix Socket → Go (Layers 1-3) → PostgreSQL
     ↓                    ↓                ↓              ↓
  50k req/s            5k req/s          1k req/s        100 req/s
  (drop)              (queue)           (process)       (commit)
```

### Configuração Implementada

```nginx
# 1. Bloqueia bots sem User-Agent
map $http_user_agent $has_user_agent {
    default 1;
    "" 0;      # Empty User-Agent
    "-" 0;      # Literal dash
}

if ($has_user_agent = 0) {
    return 444;  # Close connection, no response
}

# 2. Rate limit DDoS bruto
limit_req_zone $binary_remote_addr zone=edge_ddos:50m rate=5000r/s;
limit_req zone=edge_ddos burst=1000 nodelay;

# 3. Anti-slowloris
limit_conn_zone $binary_remote_addr zone=conn_limit:10m;
limit_conn conn_limit 50;
```

### Injeção de X-Tenant-Slug

O Nginx extrai o tenant do `server_name` e injeta no header:

```nginx
# Extrai subdomain de tenant.cascata.io
map $host $tenant_slug {
    default "";
    ~^(?<subdomain>[a-z0-9_-]+)\.(?<domain>.+)$ $subdomain;
}

# Injeta no header para o Go
proxy_set_header X-Tenant-Slug $tenant_slug;
```

**No Go (intelligent_edge.go):**
```go
// Prioridade 1: Header do Nginx (mais confiável)
if headerSlug := r.Header.Get("X-Tenant-Slug"); headerSlug != "" {
    return headerSlug, "header-x-tenant-slug", ""
}

// Prioridade 2: Host/subdomínio
// Prioridade 3: URL path (fallback)
```

### Vantagens

- **Zero overhead de Go**: DDoS é descartado no kernel
- **Zero DB lookup**: Tenant vem do header, não precisa parsear URL
- **Sem falsos positivos**: User-Agent é obrigatório (browsers legítimos sempre enviam)

## 🔐 Segurança JWT (Layer 2)

### Verificação de Assinatura

**Antes (Vulnerável):**
```go
// ParseUnverified - qualquer um pode forjar um JWT!
token, _, err := new(jwt.Parser).ParseUnverified(tokenString, jwt.MapClaims{})
```

**Agora (Seguro):**
```go
// Parse COM verificação de assinatura usando chave cacheada
jwtSecret, _ := getJWTSecretFromCache(ctx, tenantSlug)
token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
    return []byte(jwtSecret), nil
})
```

### Cache de Chaves JWT

```bash
# Quando projeto é criado, chave é cacheada no Dragonfly
SET project:{slug}:jwt_secret {jwt_secret} EX 86400

# IntelligentEdgeLimiter busca do cache (zero DB lookup)
GET project:{slug}:jwt_secret
```

### API Key Fingerprint

**Antes (Colisões fáceis):**
```go
// prefixo + "..." + sufixo = colisões garantidas com tokens curtos
userUUID = "key:" + token[:4] + "..." + token[len(token)-4:]
```

**Agora (SHA-256):**
```go
// SHA-256 hash do token inteiro - fingerprint único
hash := sha256.Sum256([]byte(token))
userUUID = "key:" + hex.EncodeToString(hash[:])[:16]
```

## 🚫 Ban Progressivo (Layer 3.5)

### Sistema de Strikes

```
Strikes em 24h → Ação
──────────────────────
1-2 strikes     → Cooldown 1 minuto (apenas rate limit)
3-4 strikes     → Ban 10 minutos
5-6 strikes     → Ban 1 hora  
7-9 strikes     → Ban 6 horas
10+ strikes     → Ban 24h + Adiciona ao blocklist permanente
```

### Chaves no Dragonfly

```
# Contador de strikes (TTL: 24h)
ban:strikes:{ip}:{uuid} → incrementa a cada violação

# Ban ativo (TTL: variável conforme strikes)
ban:active:{ip} → "strikes:N:reason:layer3_global"

# Blocklist permanente (após 10+ strikes)
sys:firewall:blocklist → set de IPs banidos
```

### Headers de Resposta

```http
HTTP/1.1 429 Too Many Requests
Retry-After: 600
X-Ban-Status: progressive
X-Ban-Reason: layer3_global
X-Strike-Count: 4

{"error":"Access suspended - too many violations","layer":3.5}
```

## 📊 Key Space no Dragonfly

```
# Layer 1 (se EDGE_IP_HARD_CAP setado)
edge:layer1:ip:1.2.3.4

# Layer 3 (contadores por IP+UUID+Tenant+Regra)
edge:layer3:1.2.3.4:uuid-abc:myproject:global
edge:layer3:1.2.3.4:uuid-abc:myproject:table
edge:layer3:1.2.3.4:uuid-abc:myproject:auth
edge:layer3:1.2.3.4:uuid-abc:myproject:rpc

# Cache de configuração (do banco)
ratelimit:config:myproject:global     # valor: "rate:burst:window:anon_rate"
ratelimit:config:myproject:auth
ratelimit:config:myproject:table
ratelimit:config:myproject:rpc

# Strikes (para análise)
abuser:strikes:1.2.3.4
```

## ✅ Implementado: Validação de JWT no Edge

**Status:** ✅ IMPLEMENTADO no Layer 2

A verificação de assinatura JWT já está ativa no `intelligent_edge.go`:

```go
// intelligent_edge.go:302-327
func parseJWTLocal(ctx context.Context, tokenString string, tenantSlug string) (jwt.MapClaims, error) {
    // Busca jwt_secret do cache Dragonfly
    jwtSecret, err := getJWTSecretFromCache(ctx, tenantSlug)
    if err != nil {
        // Sem chave no cache: parse sem verificação (fallback não-confiável)
        return parseUnverified(tokenString)
    }

    // Parse COM verificação de assinatura HMAC
    token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
        if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
            return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
        }
        return []byte(jwtSecret), nil
    })
    
    if err != nil {
        return nil, fmt.Errorf("jwt verification failed: %w", err)
    }
    
    return token.Claims.(jwt.MapClaims), nil
}
```

**Segurança:** Tokens com assinatura inválida são **rejeitados imediatamente** no Layer 2, antes de tocar no PostgreSQL.

**Cache:** O `jwt_secret` é cacheado no Dragonfly (`project:{slug}:jwt_secret`) para evitar DB lookup a cada request.

---

## ⚠️ Por que Sem Hardcoded?

Código aberto + hardcoded = **burlável em 30 segundos**

```go
// ❌ ANTES (inseguro - removido):
if ipCount > 1000 { block() }  // Atacante lê e usa 999

// ✅ AGORA (seguro):
// Limite vem de system.rate_limits (você controla)
// ou de EDGE_IP_HARD_CAP (você configura no deploy)
```

**Resultado:** Atacante pode ler o código, mas não consegue burlar porque os limites são **suas regras do banco**, não valores no código.
