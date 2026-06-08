# 🏗️ Cascata Multi-App Architecture

## Visão Global

O modelo Multi-App do Cascata permite que **um único projeto/tenant hospede múltiplas aplicações clientes**, cada uma com suas próprias credenciais de acesso, origens permitidas (CORS) e configurações de redirecionamento.

---

## 🎯 Problema que Resolve

### Antes (Modelo Tradicional)
```
Projeto "loja1" 
  └─ Anon Key: eyJhbGc... (única)
  └─ Service Key: eyJhbGc... (única)
  └─ Site URL: https://app.com
  
Problemas:
- App Web e Mobile usam a MESMA chave
- Se a chave vaza, TODOS os apps são comprometidos
- Sem granularidade de permissões por app
- Auditoria: não sabemos qual app fez a requisição
```

### Depois (Modelo Multi-App)
```
Projeto "loja1"
  ├─ Anon Key Base (fallback): eyJhbGc...
  ├─ Service Key: eyJhbGc...
  │
  ├─ App Client: "loja-web"
  │   ├─ Anon Key: anon_lojaweb_a3f7b2...
  │   ├─ Site URL: https://loja.unibloom.com.br
  │   └─ Allowed Origins: https://loja.unibloom.com.br
  │
  ├─ App Client: "driver-app"
  │   ├─ Anon Key: anon_driver_f8e1c9...
  │   ├─ Site URL: exp://driver.unibloom.com.br
  │   └─ Allowed Origins: exp://*, https://driver.unibloom.com.br
  │
  └─ App Client: "admin-dashboard"
      ├─ Anon Key: anon_admin_9d2e4a...
      ├─ Site URL: https://admin.unibloom.com.br
      └─ Allowed Origins: https://admin.unibloom.com.br

Vantagens:
- Cada app tem chave isolada
- Revogação granular (desativa apenas um app)
- Auditoria: identificamos qual app fez a requisição
- CORS específico por app (segurança)
```

---

## 🧬 Estrutura de Dados

### Storage (Metadata do Projeto)
```json
{
  "app_clients": [
    {
      "id": "driver-mobile",
      "name": "Driver Mobile App",
      "nonce": "x7k9m2p4q6r8s1t3",
      "site_url": "exp://driver.unibloom.com.br",
      "allowed_origins": ["exp://*", "https://driver.unibloom.com.br"],
      "active": true
    }
  ]
}
```

**Campos importantes:**
- `id`: identificador único (slug do nome ou random)
- `nonce`: valor aleatório gerado na criação, **imutável**, usado no HMAC
- `active`: permite soft-disable sem deletar o registro

### Geração de Anon Keys (HMAC-Based)
```
Formato: anon_<app_id>_<signature>

anon_driver-mobile_a3f7b2e8c9d1...
  │    │            │
  │    │            └─ HMAC-SHA256(app_id + ":" + nonce, Project.JWTSecret)
  │    └─ ID único do app client (ex: "driver-mobile")
  └─ Prefixo identificador

Geração:
  nonce = randomString(16)  // gerado na criação do app, salvo no metadata
  message = app_id + ":" + nonce
  signature = base64url(HMAC-SHA256(message, Project.JWTSecret))
  anon_key = "anon_" + app_id + "_" + signature

Verificação:
  1. Parse: extrai app_id e signature do prefixo
  2. Lookup: busca app_client pelo id no metadata
  3. Check: app_client existe E app_client.active == true
  4. Recalcula HMAC com o nonce armazenado
  5. Compara signatures

Segurança:
  - Chave é NÃO-determinística: precisa do nonce do metadata para verificar
  - Revogação real: deletar app_client ou setar active=false quebra a verificação
  - Stateless apenas após carregar o metadata (já feito no ProjectResolver)
```

---

## 🔐 Fluxo de Autenticação

### 1. Resolução do Projeto
```
Request: GET /api/data/gggg/users
         Headers: apikey: anon_driver_f8e1c9...

Middleware ProjectResolver:
  ├─ Extrai slug "gggg" da URL
  ├─ Busca projeto no system.projects
  ├─ Carrega JWTSecret, AnonKey base, metadata
  └─ Contexto: ctx.Project = {gggg}
```

### 2. Validação da API Key
```
Middleware CascataAuth:
  ├─ Header "apikey": "anon_driver-mobile_a3f7b2..."
  ├─ Verifica se é AnonKey base → Não
  ├─ Verifica prefixo "anon_" → Sim
  ├─ Parse: app_id = "driver-mobile", signature = "a3f7b2..."
  ├─ Lookup: busca app_client no metadata por id
  ├─ Encontrado? active == true? → Sim
  ├─ HMAC verify: HMAC("driver-mobile:x7k9m2p4...", JWTSecret) == "a3f7b2..."
  ├─ Match! → ctx.UserRole = RoleAnon
  └─ ctx.AppClient = {id: "driver-mobile", name: "Driver Mobile App", ...}

Falha em qualquer passo → rejeita (não revela qual passo falhou)
```

### 3. CORS Check (App-Specific)
```
Middleware CORS:
  ├─ Origin: "https://driver.unibloom.com.br"
  ├─ Se ctx.AppClient existe:
  │   └─ Verifica se origin está em AppClient.AllowedOrigins
  └─ Se não existe: usa AllowedOrigins global do projeto
```

---

## 🎛️ Casos de Uso

### Caso 1: SaaS com Múltiplos Frontends
```
Projeto: "meu-saas"
  ├─ Web App (Next.js)     → anon_web_x1b2c3...
  ├─ Mobile App (Flutter)  → anon_mobile_y4d5e6...
  ├─ Admin Portal (React)  → anon_admin_z7a8b9...
  └─ Embed Widget (JS)     → anon_widget_c0d1e2...
```

### Caso 2: White-Label / Multi-Tenant
```
Projeto: "plataforma-branca"
  ├─ Cliente A (Marca X)   → anon_clienta_m3n4o5...
  ├─ Cliente B (Marca Y)   → anon_clientb_p6q7r8...
  └─ Cliente C (Marca Z)   → anon_clientc_s9t0u1...
```

### Caso 3: Ambientes Isolados
```
Projeto: "fintech-app"
  ├─ Produção              → anon_prod_a1b2c3...
  ├─ Staging               → anon_staging_d4e5f6...
  └─ Dev/Local             → anon_dev_g7h8i9...
```

---

## 🔄 Lifecycle de um App Client

### Criação (Dashboard)
```
1. Admin clica "New App Client"
2. Preenche: name, site_url, allowed_origins
3. Sistema gera:
   - app_id: slugify(name) → "driver-mobile" (ou random id)
   - nonce: randomString(16) → "x7k9m2p4q6r8s1t3"
   - message: "driver-mobile:x7k9m2p4q6r8s1t3"
   - signature: base64url(HMAC-SHA256(message, Project.JWTSecret))
   - anon_key: "anon_driver-mobile_a3f7b2..."
4. Salva em metadata.app_clients (incluindo nonce, active=true)
5. Exibe anon_key ao admin (só uma vez - nonce nunca é exibido novamente)
```

### Uso (Runtime)
```
App envia requisição:
  apikey: anon_driver-mobile_a3f7b2...

Sistema valida:
  - HMAC correto?
  - Origem permitida?
  - Rate limit por app?

Auditoria:
  - Log: "User X acessou table Y via AppClient driver-mobile"
```

### Revogação
```
Opção 1: Soft Disable (recomendado)
  → Seta active = false
  → Chave continua matematicamente válida, mas middleware rejeita
  → Reversível (basta setar active = true)

Opção 2: Deletar App Client
  → Remove do metadata (incluindo o nonce)
  → Verificação falha porque nonce não existe mais
  → Irreversível (chave permanece invalidada)

Opção 3: Rotate JWT Secret
  → Todas as HMAC-based keys invalidadas imediatamente
  → Requer regeneração de todas as chaves
  → Último recurso (afeta todos os apps)

⚠️ Por que o nonce é essencial:
   Sem nonce, HMAC(app_id, secret) é determinístico — qualquer um com o 
   app_id e JWTSecret pode reconstruir a chave. Com nonce, apenas quem 
   tem o nonce armazenado (o servidor) pode validar.
```

---

## 🏛️ Vantagens Arquiteturais

| Aspecto | Modelo Tradicional | Multi-App |
|---------|-------------------|-----------|
| **Segurança** | Uma chave = ponto único de falha | Chaves isoladas, blast radius limitado |
| **Auditoria** | "Alguém fez X" | "App Y fez X às Z" |
| **CORS** | Global (permissivo ou restritivo) | Por app (preciso) |
| **Rate Limit** | Por projeto | Por projeto E por app |
| **Revogação** | Todos os apps afetados | Apenas um app |
| **Onboarding** | Compartilhar chave mestre | Gerar chave específica |

---

## 🚧 Considerações de Implementação

### Backward Compatibility
```
App Clients são OPCIONAIS
- Projetos sem app_clients continuam funcionando com AnonKey base
- Middleware tenta match na AnonKey base primeiro
- Depois tenta match em App Client keys
```

### Performance
```
Validação de HMAC é O(1) após carregar metadata
- Não requer query adicional ao banco (metadata já vem no ctx.Project)
- Cacheável no middleware

Otimização para O(n) loop:
- Cache do projeto inclui mapa: anon_prefix → app_client
- DragonflyDB (já na stack) armazena esse mapa
- Invalidação automática quando metadata é atualizado
- Resultado: lookup O(1) em vez de loop O(n)

Exemplo de cache:
  projects:gggg:app_index = {
    "anon_driver-mobile_a3f7b2...": {id, name, allowed_origins, ...},
    "anon_web-x1b2c3...": {...}
  }
```

### Limites
```
Recomendado: máximo 100 App Clients por projeto (com cache O(1))
- Metadata tem limite de tamanho (JSONB ~1MB)
- Sem cache: loop O(n) na validação
- Com cache: O(1) para lookup, O(n) apenas na reconstrução do índice
- Para >100 apps: considerar tabela separada + cache distribuído
```

---

## 🎓 Decisões de Design

### Por que HMAC em vez de JWT para Anon Keys?
```
JWT:
  ✓ Self-contained
  ✓ Expiração integrada
  ✗ Maior (300+ chars)
  ✗ Overhead de parsing
  ✗ Precisa de biblioteca

HMAC Prefixado:
  ✓ Compacto (50-60 chars)
  ✓ Stateless
  ✓ Fácil de gerar/validar
  ✗ Sem expiração integrada (mas pode adicionar timestamp)
  
Escolha: HMAC para anon keys (simplicidade), JWT para sessões de usuário
```

### Por que Storage em Metadata e não Tabela Separada?
```
Tabela Separada (app_clients):
  ✓ Normalizado
  ✓ Queries complexas fáceis
  ✗ JOIN necessário em toda requisição
  ✗ Mais uma conexão/tabela

Metadata JSONB:
  ✓ Carregado junto com projeto (1 query)
  ✓ Cacheável como objeto
  ✓ Flexível (schema evolutivo)
  ✗ Não normalizado
  ✗ Limitado por tamanho do JSONB

Escolha: Metadata (performance > normalização para hot path)
```

---

## 📋 Checklist de Implementação

- [ ] Estrutura AppClient no types
- [ ] Função GenerateAppAnonKey(app_id, nonce, secret) → string
- [ ] Função ValidateAppAnonKey(key, appClientsMap) → (appClient, valid)
- [ ] Middleware: integração na verificação de apikey
- [ ] CORS: verificação por AppClient
- [ ] Dashboard: UI para criar/listar/deletar App Clients
- [ ] API: endpoints CRUD para App Clients
- [ ] Auditoria: taggear logs com app_client_id
- [ ] Rate Limit: separação por AppClient
- [ ] Documentação: guia para desenvolvedores

---

## 🔮 Evoluções Futuras

### Scoped Permissions
```
App Client com permissões limitadas:
  {
    "allowed_tables": ["public.products", "public.orders"],
    "allowed_operations": ["SELECT", "INSERT"],
    "denied_tables": ["admin.config"]
  }
```

### App-Specific JWT Secrets
```
Cada App Client pode ter seu próprio JWT Secret:
  - Isolamento total de sessões
  - Revogação independente
  - Diferentes expirações por app
```

### App Client Tiers
```
  Free Tier: 1 App Client
  Pro Tier: 10 App Clients
  Enterprise: Ilimitado + Scoped Permissions
```

---

## Resumo Executivo

O Multi-App Architecture transforma o Cascata de um "banco por projeto" para uma **"plataforma de dados multi-face"**. Cada aplicação cliente (web, mobile, embed, white-label) recebe credenciais únicas e isoladas, permitindo:

1. **Segurança granular**: Compromisso de um app não afeta os outros
2. **Auditoria precisa**: Sabemos EXATAMENTE qual app fez cada requisição
3. **CORS estrito**: Cada app acessa apenas de suas origens permitidas
4. **Governança**: Rate limits, quotas e permissões por app

A implementação via HMAC prefixado com nonce oferece o equilíbrio perfeito entre segurança (revogação real), performance (stateless após lookup) e simplicidade.

---

*Documento versão 1.0 - Arquitetura Multi-App do Cascata*
