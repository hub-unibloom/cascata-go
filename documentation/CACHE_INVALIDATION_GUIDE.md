# Guia de Invalidação de Cache - Edge Defense

## ⚠️ Importância

O cache do Dragonfly tem TTL de 24h. Se não invalidar explicitamente, requests com dados antigos (JWT secrets, rate limits) continuam funcionando por até 24h após alteração no banco.

---

## 🔧 Funções Disponíveis

Localizadas em: `backend/internal/middleware/intelligent_edge.go`

```go
// Invalida apenas JWT secret cache
func InvalidateJWTSecret(ctx context.Context, tenantSlug string) error

// Invalida apenas rate limit cache
func InvalidateRateLimitCache(ctx context.Context, tenantSlug string) error

// Invalida TODOS os caches do projeto (recomendado)
func InvalidateProjectCache(ctx context.Context, tenantSlug string) error
```

---

## 📍 Pontos de Integração Obrigatórios

### 1. Deleção de Projeto (CRÍTICO)

**Quando implementar DeleteProject:**

```go
func (c *AdminController) DeleteProject(w http.ResponseWriter, r *http.Request) {
    slug := chi.URLParam(r, "slug")
    
    // 1. Deleta do banco
    _, err := services.SystemPool.Exec(r.Context(), 
        "DELETE FROM system.projects WHERE slug = $1", slug)
    if err != nil {
        http.Error(w, `{"error":"Delete failed"}`, 500)
        return
    }
    
    // 2. INVALIDA CACHE IMEDIATAMENTE
    if err := middleware.InvalidateProjectCache(r.Context(), slug); err != nil {
        log.Printf("[DeleteProject] Warning: Cache invalidation failed: %v", err)
        // Não falha a operação, mas loga o warning
    }
    
    // 3. (Opcional) Limpa blocklist/bans do IP se necessário
    
    json.NewEncoder(w).Encode(map[string]bool{"success": true})
}
```

**Por que:** Se não invalidar, tokens do projeto deletado continuam válidos por 24h.

---

### 2. Rotação de JWT Secret (CRÍTICO)

**Quando implementar RotateJWTSecret:**

```go
func (c *AdminController) RotateJWTSecret(w http.ResponseWriter, r *http.Request) {
    ctx := r.Context().Value(types.CascataCtxKey).(*types.CascataRequest)
    
    // 1. Gera novo secret
    newSecret := c.CryptoSvc.GenerateKey() + c.CryptoSvc.GenerateKey()
    
    // 2. Cifra e salva no banco
    jwtCipher, _ := c.CryptoSvc.Encrypt("sse", newSecret)
    _, err := services.SystemPool.Exec(r.Context(),
        "UPDATE system.projects SET jwt_secret = $1 WHERE slug = $2",
        jwtCipher, ctx.Project.Slug)
    if err != nil {
        http.Error(w, `{"error":"Rotation failed"}`, 500)
        return
    }
    
    // 3. INVALIDA CACHE DO SECRET ANTIGO
    if err := middleware.InvalidateJWTSecret(r.Context(), ctx.Project.Slug); err != nil {
        log.Printf("[RotateJWTSecret] Warning: Failed to invalidate cache: %v", err)
    }
    
    // 4. Preenche cache com novo secret (opcional, próximo request faz isso)
    // ou chame CacheJWTSecret explicitamente se quiser warm cache
    
    json.NewEncoder(w).Encode(map[string]string{
        "message": "JWT secret rotated successfully",
        "new_secret_preview": newSecret[:8] + "...",
    })
}
```

**Por que:** Tokens com secret antigo continuariam válidos por 24h se não invalidar.

⚠️ **IMPORTANTE - Breaking Change:**
Quando você rotaciona o JWT secret e invalida o cache, **todos os usuários logados com tokens ativos assinados com o secret antigo terão seus JWTs invalidados imediatamente**. O Layer 2 vai rejeitar a assinatura e retornar 401/403.

Isso é o **comportamento correto de segurança** — tokens antigos não devem funcionar após rotação. Mas é um breaking change para usuários ativos. Recomendações:

1. **Comunique previamente** aos usuários sobre a manutenção
2. **Implemente refresh token** para renovação automática (usuários nem percebem)
3. **Escolha janela de baixo tráfego** para a rotação
4. **Monitore logs** de `JWT verification failed` após a rotação

```go
// Log esperado após rotação (não é bug, é comportamento correto):
// [SECURITY_WARNING] JWT verification failed for tenant=xyz: jwt verification failed: ...
```

---

### 3. Atualização de Projeto com Novo JWT Secret

**Se UpdateProject aceitar alteração de jwt_secret:**

```go
// Em admin.go UpdateProject, adicione:
if body.JwtSecret != "" && body.JwtSecret != ctx.Project.JwtSecret {
    // Novo JWT secret foi fornecido - invalida cache
    if err := middleware.InvalidateJWTSecret(r.Context(), ctx.Project.Slug); err != nil {
        log.Printf("[UpdateProject] Warning: Failed to invalidate JWT cache: %v", err)
    }
    
    // Cifra e adiciona ao update...
}
```

---

### 4. Deleção de Rate Limit (JÁ IMPLEMENTADO)

✅ **Status:** Já implementado em `security.go DeleteRateLimit`

```go
// Atualiza cache no Dragonfly para o edge limiter
go func() {
    time.Sleep(100 * time.Millisecond) // Delay para garantir commit
    if err := middleware.InvalidateRateLimitCache(r.Context(), ctx.Project.Slug); err != nil {
        log.Printf("[DeleteRateLimit] Warning: Failed to invalidate cache: %v", err)
    }
    middleware.RefreshRateLimitCache(r.Context(), ctx.Project.Slug)
}()
```

---

## 🎯 Resumo das Chaves de Cache

| Chave | TTL | Quando Invalidar |
|-------|-----|------------------|
| `project:{slug}:jwt_secret` | 24h | Rotação de secret, Deleção de projeto |
| `ratelimit:config:{slug}:{type}` | 1h | Alteração/deleção de regras |
| `edge:layer1:ip:{ip}` | 1min | N/A (volátil) |
| `edge:layer3:{ip}:{uuid}:{slug}:{type}` | window_seconds | N/A (volátil) |
| `ban:strikes:{ip}:{uuid}` | 24h | N/A (volátil) |
| `ban:active:{ip}` | progressivo | N/A (volátil) |

---

## ⚡ Estratégia Recomendada

### Opção A: Invalidação Imediata (Implementada)
- ✅ Cache é deletado imediatamente
- ✅ Próximo request busca do banco
- ⚠️ Pequena penalidade de performance no primeiro request

### Opção B: TTL Curto + Warm Cache (Alternativa)
```go
// TTL de 5-15 minutos em vez de 24h
// + Pre-warm do cache quando alterar
```
- ✅ Dados stale por menos tempo
- ⚠️ Mais load no PostgreSQL

**Recomendação:** Mantenha 24h TTL + invalidação explícita (Opção A).

---

## 🚨 Checklist de Segurança

- [ ] DeleteProject chama `InvalidateProjectCache()`
- [ ] RotateJWTSecret chama `InvalidateJWTSecret()`
- [ ] UpdateProject (se alterar jwt_secret) chama `InvalidateJWTSecret()`
- [ ] DeleteRateLimit já implementado ✅
- [ ] Logs de warning quando invalidação falha
- [ ] Não falha a operação principal se invalidação falhar

---

## 📝 Nota sobre Criação de Projeto

Quando projeto é criado, o cache é **populado** (não invalidado):

```go
// Em admin.go CreateProject (JÁ IMPLEMENTADO)
if err := middleware.CacheJWTSecret(r.Context(), body.Slug, body.JwtSecret); err != nil {
    log.Printf("[CreateProject] Warning: Failed to cache JWT secret: %v", err)
}
```

Isso garante que o Layer 2 já tenha a chave disponível sem DB lookup.
