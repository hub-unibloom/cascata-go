# 🛡️ Guia de Proteção DDoS - Cascata + Cloudflare

Este documento explica como o Cascata protege contra diferentes tipos de ataques DDoS e como maximizar a proteção.

## 📊 Arquitetura de Defesa em Profundidade

```
┌─────────────────────────────────────────────────────────────┐
│  LAYER 3/4 - INFRAESTRUTURA (Cloudflare Free)              │
│  ├─ Volumétricos (100Gbps+)                                 │
│  ├─ SYN Floods                                              │
│  ├─ UDP Amplification                                       │
│  └─ ICMP Floods                                             │
└────────────────────┬────────────────────────────────────────┘
                     │
┌────────────────────▼────────────────────────────────────────┐
│  LAYER 7 - APLICAÇÃO (Cascata Edge Defense)                │
│  ├─ HTTP Flooding (Layer 1)                                │
│  ├─ API Abuse (Layer 3)                                    │
│  ├─ Brute Force (Layer 3)                                  │
│  ├─ Progressive Bans (Layer 3.5)                          │
│  └─ Smart Lockout                                          │
└────────────────────┬────────────────────────────────────────┘
                     │
┌────────────────────▼────────────────────────────────────────┐
│  LAYER 7+ - AUTHENTICAÇÃO (GoTrue/Cascata)                 │
│  ├─ JWT Rate Limiting                                       │
│  ├─ Challenge/Response                                      │
│  ├─ MFA Protection                                          │
│  └─ Panic Revocation                                        │
└─────────────────────────────────────────────────────────────┘
```

---

## 🌩️ Layer 3/4: Proteção Volumétrica (Cloudflare)

### O que protege?

| Ataque | Descrição | Proteção |
|--------|-----------|----------|
| **UDP Flood** | 100Gbps+ de pacotes UDP | ✅ Cloudflare absorve |
| **SYN Flood** | Milhões de conexões半-abertas | ✅ Cloudflare filtra |
| **ICMP Flood** | Ping da morte em massa | ✅ Cloudflare bloqueia |
| **DNS Amplification** | Explora servidores DNS | ✅ Cloudflare DNS |
| **NTP Amplification** | Explota servidores NTP | ✅ Cloudflare bloqueia |

### Como funciona?

1. **Anycast Network**: 275+ datacenters globais absorvem tráfego
2. **Traffic Scrubbing**: Limpa tráfego malicioso antes de chegar no servidor
3. **Challenge Pages**: Suspeitos recebem CAPTCHA/JavaScript challenge
4. **Rate Limiting**: Conexões por IP limitadas automaticamente

### Limitações do plano Gratuito

- ⚠️ Sem proteção contra ataques Layer 7 avançados (botnets sofisticadas)
- ⚠️ Sem suporte prioritário (auto-atendimento)
- ⚠️ Sem análise forense detalhada
- ✅ Proteção básica volumétrica: **ILIMITADA**

---

## 🔒 Layer 7: Proteção de Aplicação (Cascata)

### Sistema de Camadas Progressivas

```
Requisição → Layer 1 (IP Check) 
                  ↓
           Layer 2 (JWT Verify)
                  ↓
           Layer 3 (Rate Limit)
                  ↓
           Layer 3.5 (Progressive Ban)
                  ↓
           ALLOW / DENY
```

### Layer 1: Connection Guard

```go
// Verificações rápidas (nanossegundos)
- IP blocklist global
- Geolocation (se configurado)
- ASN reputation
- Request fingerprint
```

**Respostas**:
- `429` - Rate limit atingido
- `403` - IP bloqueado permanentemente

### Layer 2: JWT Verification

```go
// Validação criptográfica
- Signature verification (Ed25519/HS256)
- Expiration check
- Issuer validation
- Claims verification
```

**Headers de Rate Limit**:
```http
X-RateLimit-Rule: jwt_auth
X-RateLimit-Limit: 100
X-RateLimit-Remaining: 45
X-RateLimit-Auth: jwt
```

### Layer 3: Smart Rate Limiting

#### Por Identificador
```yaml
# Por usuário autenticado
authenticated:
  limit: 1000/hour
  burst: 100

# Por IP anônimo
anonymous:
  limit: 100/hour
  burst: 10
```

#### Por Operação
```yaml
# Queries simples
select: 1000/min

# Mutations (insert/update/delete)
write: 100/min

# Auth operations
login: 5/min
signup: 3/min
```

### Layer 3.5: Progressive Ban System

**Strikes System**:

```
Strike 1-2:  Warning (log only)
Strike 3-5:  15 min ban
Strike 6-9:  1 hour ban
Strike 10+:  Permanent blocklist
```

**Headers de Ban**:
```http
X-Ban-Status: progressive
X-Ban-Reason: too_many_strikes
Retry-After: 900
X-RateLimit-Layer: 3.5
```

**Códigos de Erro**:
```json
{
  "error": "Access temporarily suspended",
  "code": "progressive_ban",
  "strikes": 5,
  "retry_after": 900
}
```

---

## 🔐 Layer 7+: Proteção de Autenticação

### JWT Rate Limiting

```yaml
# Por usuário
jwt_validation:
  limit: 1000/hour
  per_user: true

# Por IP (anônimo)
jwt_validation_anon:
  limit: 60/hour
  per_ip: true
```

### Challenge/Response

Para operações sensíveis:
1. **TOTP**: Time-based one-time password
2. **OTP via Webhook**: Código enviado para SMS/email
3. **Magic Link**: Link único por email

### Panic Revocation

```http
POST /auth/v1/revoke-all
Authorization: Bearer <token>

Response: 200 OK
{
  "revoked_sessions": 5,
  "affected_devices": ["iPhone", "Chrome", "Firefox"]
}
```

Invalida **todos** os tokens ativos do usuário imediatamente.

---

## 📈 Capacidade de Sobrevivência

### Sem Cloudflare (Apenas Cascata)

| Ataque | Capacidade | Resultado |
|--------|------------|-----------|
| 10k req/s | 🟢 Sobrevive | Rate limit ativa |
| 100k req/s | 🔴 Falha | Saturação rede/servidor |
| 1Gbps volumétrico | 🔴 Falha | Link saturado |
| Slowloris | 🟡 Parcial | Timeouts ajudam |

### Com Cloudflare Free

| Ataque | Capacidade | Resultado |
|--------|------------|-----------|
| 10k req/s | 🟢 Sobrevive | Cache + rate limit |
| 100k req/s | 🟢 Sobrevive | CF absorve 95% |
| 1Gbps volumétrico | 🟢 Sobrevive | Scrubbing center |
| 10Gbps+ | 🟢 Sobrevive | Anycast absorve |
| Slowloris | 🟢 Sobrevive | Connection limits |

---

## 🚨 Resposta a Incidentes

### Detectando Ataque

#### Via Logs
```bash
# Aumento anormal de requisições
tail -f /var/log/cascata/access.log | grep "429\|403"

# IPs mais ativos
awk '{print $1}' access.log | sort | uniq -c | sort -rn | head -20

# User agents suspeitos
grep -i "bot\|spider\|curl" access.log | wc -l
```

#### Via Dashboard
1. **Real-time Stats**: Requisições/segundo
2. **Error Rate**: % de 429/403
3. **Top IPs**: Identificar abusadores
4. **Geolocation**: Origem dos ataques

### Mitigação Automática

O sistema já faz:
1. ✅ Blocklist automático de IPs (strikes >= 10)
2. ✅ Rate limiting adaptativo
3. ✅ Progressive bans
4. ✅ Panic mode (quando ativado)

### Ações Manuais (Admin)

```bash
# Bloquear IP manualmente
POST /admin/block-ip
{
  "ip": "192.0.2.1",
  "reason": "DDoS attack",
  "duration": "24h"
}

# Ativar panic mode
POST /admin/panic-mode
{
  "enabled": true,
  "allowed_ips": ["admin-office-ip"]
}

# Flush blocklist (emergência)
POST /admin/flush-blocklist
```

---

## 🛠️ Hardening Recomendado

### 1. Cloudflare (Obrigatório)

```yaml
# Mínimo para proteção DDoS
cloudflare:
  proxy: enabled      # 🟧 Orange cloud
  ssl: full_strict    # TLS 1.3
  always_https: true  # Redirect HTTP
  min_tls: "1.2"
```

### 2. Cascata Config

```yaml
# Rate limits conservadores
rate_limits:
  anonymous:
    requests_per_minute: 30
    burst: 5
  
  authenticated:
    requests_per_minute: 300
    burst: 50

# Progressive bans agressivos
progressive_ban:
  enabled: true
  max_strikes: 10
  ban_durations: ["15m", "1h", "24h", "permanent"]

# IP blocklist
blocklist:
  enabled: true
  sync_interval: "10s"
  storage: dragonfly
```

### 3. Infraestrutura

```yaml
# Nginx (se usado)
nginx:
  worker_processes: auto
  worker_connections: 4096
  
  # Rate limit layer 4
  limit_req_zone:
    - zone: one
      size: 10m
      rate: 10r/s
  
  # Connection limits
  limit_conn_zone:
    - zone: addr
      size: 10m

# Firewall (iptables/ufw)
firewall:
  # Bloquear portas desnecessárias
  allow:
    - 443/tcp  # HTTPS
    - 80/tcp   # HTTP (redirect)
  drop:
    - 22/tcp   # SSH (use VPN)
    - 3306/tcp # Database
    - 6379/tcp # Redis/Dragonfly
```

---

## 📊 Monitoring & Alerting

### Métricas Críticas

```prometheus
# Taxa de erros 429/403
rate(cascata_http_requests_total{status=~"429|403"}[5m])

# Requisições por IP top
topk(10, sum by (ip) (rate(cascata_requests_total[5m])))

# Strikes ativos
sum(cascata_ip_strikes_total)

# IPs no blocklist
cascata_blocklist_size

# Cache hit ratio (se usando CF)
rate(cloudflare_cache_hits[5m]) / rate(cloudflare_requests[5m])
```

### Alertas

```yaml
# High error rate
alert: HighErrorRate
expr: rate(cascata_http_5xx[5m]) > 0.1
for: 5m

# Possible DDoS
alert: PossibleDDOS
expr: rate(cascata_http_429[5m]) > 100
for: 2m

# Blocklist growing fast
alert: RapidBlocklistGrowth
expr: increase(cascata_blocklist_adds[10m]) > 1000
```

---

## 🎓 Boas Práticas

### Para Desenvolvedores

1. **Implemente backoff exponencial** no SDK
2. **Respeite headers 429** (Retry-After)
3. **Use cache local** para reduzir requisições
4. **Batch operations** quando possível
5. **Evite polling** agressivo - use Realtime/SSE

### Para DevOps

1. **Monitore sempre** métricas de rate limit
2. **Tenha runbook** para ataques DDoS
3. **Teste periodicamente** com ferramentas como `wrk`, `ab`
4. **Mantenha Cloudflare** como primeira linha
5. **Backup dos dados** fora do servidor principal

---

## 📞 Quando Solicitar Ajuda Profissional

Contrate proteção paga se:
- Ataques >100Gbps recorrentes
- Ataques sofisticados (botnets, CAPTCHA bypass)
- Necessidade de SLA garantido
- Requisitos de compliance (PCI-DSS, etc)

**Opções**:
- Cloudflare Pro/Business ($20-200/mês)
- AWS Shield Advanced ($3000/mês + taxas)
- Akamai Prolexic (Enterprise)

---

## ✅ Checklist de Segurança DDoS

### Antes do Launch

- [ ] Cloudflare configurado com proxy laranja
- [ ] SSL/TLS em modo Full/Strict
- [ ] Rate limits configurados
- [ ] Progressive ban ativado
- [ ] IP blocklist funcionando
- [ ] Monitoramento configurado
- [ ] Runbook de incidente documentado

### Durante Operação

- [ ] Dashboard monitorado diariamente
- [ ] Alertas testados mensalmente
- [ ] Simulação de ataque (stress test) trimestral
- [ ] Revisão de logs semanal
- [ ] Atualização de assinaturas de ataque

---

**Lembre-se**: Nenhum sistema é 100% imune, mas com **Cloudflare + Cascata Edge Defense**, você tem proteção de nível enterprise gratuita contra a grande maioria dos ataques.
