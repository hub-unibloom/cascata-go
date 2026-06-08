# 🌩️ Cloudflare Integration - Guia de Configuração

O Cascata oferece integração nativa com **Cloudflare Free** para proteção DDoS, CDN global e SSL/TLS gratuito.

## ✅ O que você ganha (100% Gratuito)

| Feature | Benefício | Custo |
|---------|-----------|-------|
| **DDoS Protection** | Proteção básica contra ataques volumétricos | Grátis |
| **CDN Global** | Cache em 275+ datacenters mundiais | Grátis |
| **SSL/TLS** | Certificado HTTPS automático | Grátis |
| **DNS Rápido** | Resolver global (1.1.1.1) | Grátis |
| **Brotli** | Compressão avançada | Grátis |
| **IPv6** | Suporte completo IPv6 | Grátis |

---

## 🚀 Configuração Passo a Passo

### 1. Criar Conta Cloudflare

1. Acesse [cloudflare.com](https://cloudflare.com)
2. Clique em "Sign Up" (gratuito)
3. Verifique email

### 2. Adicionar Domínio

1. Clique em "Add Site"
2. Digite seu domínio (ex: `meu-app.com`)
3. Selecione plano **Free**
4. Cloudflare escaneará registros DNS existentes

### 3. Configurar DNS

Aponte seu domínio/subdomínio para o servidor Cascata:

```
Type:    A
Name:    app (ou @ para root)
Value:   SEU_IP_CASCATA
TTL:     Auto
Proxy:   🟧 LARANJA (Ativado)  ← IMPORTANTE!
```

> ⚠️ **O ícone LARANJA (Orange Cloud) é obrigatório para proteção DDoS!**
> Cinza = apenas DNS (sem proteção)

### 4. Alterar Nameservers

Cloudflare fornecerá 2 nameservers (ex: `bob.ns.cloudflare.com`, `lara.ns.cloudflare.com`).

No seu registrador de domínio (GoDaddy, Registro.br, etc):
1. Acesse configuração de DNS/nameservers
2. Substitua pelos nameservers do Cloudflare
3. Aguarde propagação (5 min - 24h)

### 5. Configurar SSL/TLS

No painel Cloudflare:
1. Vá em **SSL/TLS** → **Overview**
2. Selecione modo: **`Full (strict)`** (recomendado) ou `Full`
3. Ative **"Always Use HTTPS"**

### 6. Otimizações Recomendadas

**Speed** → **Optimization**:
- ✅ Auto Minify: CSS, JS, HTML
- ✅ Brotli: Ativado
- ✅ Early Hints: Ativado

**Caching**:
- Caching Level: Standard
- Browser Cache TTL: 4 hours

---

## 🔍 Verificação no Cascata

### API de Verificação

```bash
# Verificar status do seu domínio
curl https://api.cascata.io/api/data/seu-projeto/cloudflare/health

# Resposta:
{
  "protected": true,
  "domain": "app.meu-app.com",
  "message": "Domínio protegido com Cloudflare (DDoS + CDN ativos)"
}
```

### Dashboard Admin

No painel admin do Cascata, vá em:
**Projeto** → **Settings** → **Domain** → **Cloudflare Status**

Você verá:
- Score de otimização (0-100)
- Proteção DDoS: Ativa/Inativa
- Recomendações personalizadas

---

## 🛡️ Entendendo a Proteção

### Camadas de Proteção

```
Atacante → Cloudflare Edge → Cascata Server
    ↓            ↓              ↓
  Bloqueio    Cache Hit      Rate Limit
   L3/L4       (CDN)         (L7 App)
```

**Cloudflare protege:**
- ❌ Ataques volumétricos (Gbps de tráfego)
- ❌ SYN floods
- ❌ UDP amplification
- ❌ Slowloris (parcial)

**Cascata protege:**
- ❌ Brute force login
- ❌ API abuse
- ❌ JWT tampering
- ❌ SQL injection

### Headers de Verificação

Quando protegido, você verá headers como:

```http
CF-Ray: 7d1234567890-SAO
CF-Cache-Status: HIT
CF-IPCountry: BR
```

---

## 📊 Métricas de Proteção

### Score de Otimização

| Score | Status | Ação |
|-------|--------|------|
| 90-100 | 🟢 Excelente | Configurado perfeitamente |
| 70-89 | 🟡 Bom | Pequenas melhorias possíveis |
| 50-69 | 🟠 Regular | Requer ajustes |
| 0-49 | 🔴 Crítico | Sem proteção DDoS |

### Componentes do Score

- **Proxy ativo**: 40 pontos (essencial)
- **SSL/TLS**: 30 pontos (segurança)
- **DNS válido**: 20 pontos (funcionamento)
- **HTTPS forçado**: 10 pontos (extra)

---

## 🚨 Troubleshooting

### "Domínio não protegido"

**Causa**: Ícone cinza (DNS only) em vez de laranja (Proxied)

**Solução**:
1. Cloudflare Dashboard → DNS
2. Clique no ícone cinza (nuvem)
3. Deve ficar 🟧 laranja
4. Aguarde 2-5 minutos

### "SSL/TLS não configurado"

**Causa**: Modo SSL incorreto

**Solução**:
1. SSL/TLS → Overview
2. Selecione `Full` ou `Full (strict)`
3. NÃO use `Flexible` (inseguro)

### "DNS não aponta para Cascata"

**Causa**: IP errado no registro A

**Solução**:
1. Obtenha IP do servidor Cascata
2. Atualize registro A no Cloudflare
3. Aguarde propagação DNS

---

## 🔗 Recursos Úteis

- [Cloudflare Dashboard](https://dash.cloudflare.com)
- [Docs: DNS Records](https://developers.cloudflare.com/dns/manage-dns-records/)
- [Docs: SSL/TLS](https://developers.cloudflare.com/ssl/)
- [Docs: DDoS Protection](https://developers.cloudflare.com/ddos-protection/)

---

## 💡 Dicas Avançadas

### Page Rules (Gratuito: 3 rules)

Configure em **Rules** → **Page Rules**:

```
URL: *seu-dominio.com/api/*
Settings:
  - Cache Level: Cache Everything
  - Edge Cache TTL: 2 hours
  - Browser Cache TTL: 30 minutes
```

### WebSockets

Cloudflare Free suporta WebSockets nativamente - nenhuma configuração extra necessária para Realtime do Cascata!

### API Token (Opcional)

Para purge automático de cache:
1. My Profile → API Tokens
2. Create Token → "Custom token"
3. Permissions: Zone.Cache Purge (Purge)
4. Zone Resources: Include - Specific zone - seu-dominio.com
5. Salve token no dashboard Cascata

---

## ✅ Checklist Final

Antes de colocar em produção:

- [ ] Conta Cloudflare criada
- [ ] Domínio adicionado
- [ ] DNS aponta para IP Cascata
- [ ] 🟧 Ícone LARANJA ativado
- [ ] SSL/TLS em modo Full
- [ ] Always Use HTTPS ativado
- [ ] Teste: `curl -I https://seu-dominio.com` retorna headers CF-
- [ ] Verificação API retorna `"protected": true`

---

**Dúvidas?** Abra uma issue no GitHub ou consulte a comunidade Discord.
