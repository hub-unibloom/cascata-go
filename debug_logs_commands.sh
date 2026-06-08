#!/bin/bash
# Script para coletar todos os logs necessários para debug do erro 404 em domínios tenancy

echo "=========================================="
echo "1. VERIFICAR CONFIGURAÇÕES NGINX"
echo "=========================================="
echo "--- Arquivos no diretório dynamic ---"
docker exec cascata-nginx ls -la /etc/nginx/conf.d/dynamic/

echo ""
echo "--- Conteúdo do config do projeto teste3 ---"
docker exec cascata-nginx cat /etc/nginx/conf.d/dynamic/10_proj_teste3.conf 2>/dev/null || echo "ARQUIVO NÃO ENCONTRADO"

echo ""
echo "--- Conteúdo do config do dashboard ---"
docker exec cascata-nginx cat /etc/nginx/conf.d/dynamic/00_system_dashboard.conf 2>/dev/null || echo "ARQUIVO NÃO ENCONTRADO"

echo ""
echo "--- Teste de sintaxe do nginx ---"
docker exec cascata-nginx nginx -t 2>&1

echo ""
echo "=========================================="
echo "2. LOGS DO NGINX (últimas 50 linhas)"
echo "=========================================="
echo "--- Access Log ---"
docker logs cascata-nginx --tail 50 2>&1 | grep -E "(teste3|404|error)" || docker logs cascata-nginx --tail 30

echo ""
echo "=========================================="
echo "3. LOGS DO BACKEND CONTROL"
echo "=========================================="
docker logs cascata-backend --tail 100 2>&1 | grep -E "(RebuildNginx|teste3|404|Project|Domain)" | tail -30

echo ""
echo "=========================================="
echo "4. LOGS DO BACKEND DATA"
echo "=========================================="
docker logs cascata-backend-data --tail 100 2>&1 | grep -E "(teste3|404|Project|tabela_tesste)" | tail -30

echo ""
echo "=========================================="
echo "5. INFORMAÇÕES DO PROJETO NO BANCO"
echo "=========================================="
docker exec cascata-db psql -U postgres -d cascata_system -c "
SELECT slug, custom_domain, ssl_certificate_source, status 
FROM system.projects 
WHERE slug = 'teste3' OR custom_domain LIKE '%teste3%'
"

echo ""
echo "=========================================="
echo "6. TESTE DE RESOLUÇÃO DNS"
echo "=========================================="
echo "--- Resolução do domínio ---"
nslookup teste3.unibloom.com.br 2>/dev/null || dig teste3.unibloom.com.br +short

echo ""
echo "=========================================="
echo "7. TESTE DE REQUISIÇÃO DIRETA"
echo "=========================================="
echo "--- Teste via HTTP interno (bypass Cloudflare) ---"
curl -s -o /dev/null -w "%{http_code} %{url_effective}\n" \
  -H "Host: teste3.unibloom.com.br" \
  -H "apikey: 7aeb6bf34ad8dfcd4f0fbbd9cccf761e6a5f8e874b0ae1a40d01405c447b46300ed26b7cec0b033b790c2720855db28532075a7c69db926b585eb4b87621140e" \
  http://localhost/api/data/teste3/tabela_tesste?select=id

echo "--- Teste via HTTPS (via Cloudflare) ---"
curl -s -o /dev/null -w "%{http_code} %{url_effective}\n" \
  -H "apikey: 7aeb6bf34ad8dfcd4f0fbbd9cccf761e6a5f8e874b0ae1a40d01405c447b46300ed26b7cec0b033b790c2720855db28532075a7c69db926b585eb4b87621140e" \
  "https://teste3.unibloom.com.br/tabela_tesste?select=id" 2>&1 | head -5

echo ""
echo "=========================================="
echo "8. VERIFICAR ESTRUTURA DA REQUISIÇÃO"
echo "=========================================="
echo "--- Headers recebidos pelo backend ---"
docker logs cascata-nginx --tail 20 2>&1 | grep -E "(Host|teste3)"

echo ""
echo "=========================================="
echo "COLETA DE LOGS CONCLUÍDA"
echo "=========================================="
