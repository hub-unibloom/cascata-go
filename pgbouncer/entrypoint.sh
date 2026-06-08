#!/bin/sh
# PgBouncer Entrypoint com Logging Detalhado
# Cada etapa do processo de boot é logada para debug

set -e

# Cores para logs (quando suportado)
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Timestamp format
TIMESTAMP() {
    date '+%Y-%m-%d %H:%M:%S'
}

# Funções de log
LOG_INFO() {
    echo "[$(TIMESTAMP)] [PgBouncer-BOOT] [INFO] $1"
}

LOG_WARN() {
    echo "[$(TIMESTAMP)] [PgBouncer-BOOT] [WARN] $1"
}

LOG_ERROR() {
    echo "[$(TIMESTAMP)] [PgBouncer-BOOT] [ERROR] $1"
}

LOG_DEBUG() {
    if [ "${DEBUG:-false}" = "true" ]; then
        echo "[$(TIMESTAMP)] [PgBouncer-BOOT] [DEBUG] $1"
    fi
}

LOG_STEP() {
    echo "[$(TIMESTAMP)] [PgBouncer-BOOT] [STEP] ===== $1 ====="
}

# ============================================================
# ETAPA 1: INICIALIZAÇÃO
# ============================================================
LOG_STEP "1/10 - Inicializando PgBouncer Entrypoint"
LOG_INFO "Container iniciado com PID: $$"
LOG_INFO "Ambiente: DB_HOST=${DB_HOST:-<não definido>}, DB_PORT=${DB_PORT:-5432}"
LOG_INFO "Usuário atual: $(id)"

# ============================================================
# ETAPA 2: VERIFICAÇÃO DE VARIÁVEIS DE AMBIENTE
# ============================================================
LOG_STEP "2/10 - Verificando variáveis de ambiente"

REQUIRED_VARS="DB_HOST DB_USER DB_PASSWORD"
MISSING_VARS=""

for var in $REQUIRED_VARS; do
    if [ -z "$(eval echo \$$var)" ]; then
        MISSING_VARS="$MISSING_VARS $var"
        LOG_ERROR "Variável obrigatória $var não está definida!"
    else
        LOG_INFO "✓ $var está definida"
    fi
done

if [ -n "$MISSING_VARS" ]; then
    LOG_ERROR "Variáveis faltantes:$MISSING_VARS"
    LOG_ERROR "Abortando..."
    exit 1
fi

# ============================================================
# ETAPA 3: VERIFICAÇÃO DE REDE/DNS
# ============================================================
LOG_STEP "3/10 - Verificando resolução DNS"

LOG_INFO "Testando resolução DNS para host: ${DB_HOST}"
LOG_INFO "DNS interno do Docker (resolv.conf):"
cat /etc/resolv.conf | while read line; do
    LOG_INFO "  $line"
done

LOG_INFO "Servidores de nomes configurados:"
grep "nameserver" /etc/resolv.conf | while read line; do
    LOG_INFO "  → $line"
done

# Tentar resolver o hostname com diferentes métodos
LOG_INFO "Tentando resolver ${DB_HOST}..."

# Método 1: getent
LOG_INFO "[DNS-TEST] Método 1: getent hosts"
if getent hosts "$DB_HOST" > /dev/null 2>&1; then
    DB_IP=$(getent hosts "$DB_HOST" | awk '{ print $1 }')
    LOG_INFO "[DNS-TEST] ✓ getent SUCESSO: ${DB_HOST} → ${DB_IP}"
else
    LOG_WARN "[DNS-TEST] ✗ getent FALHOU"
fi

# Método 2: nslookup
LOG_INFO "[DNS-TEST] Método 2: nslookup"
if nslookup "$DB_HOST" > /dev/null 2>&1; then
    LOG_INFO "[DNS-TEST] ✓ nslookup SUCESSO"
    nslookup "$DB_HOST" 2>&1 | grep -E "Address|Name:" | while read line; do
        LOG_INFO "[DNS-TEST]   $line"
    done
else
    LOG_WARN "[DNS-TEST] ✗ nslookup FALHOU"
fi

# Método 3: ping (só para testar se responde)
LOG_INFO "[DNS-TEST] Método 3: ping (1 pacote)"
if ping -c 1 -W 2 "$DB_HOST" > /dev/null 2>&1; then
    LOG_INFO "[DNS-TEST] ✓ ping SUCESSO"
else
    LOG_WARN "[DNS-TEST] ✗ ping FALHOU (host pode não responder ICMP)"
fi

# ============================================================
# ETAPA 4: RESOLUÇÃO DE IP PARA CONFIGURAÇÃO
# ============================================================
LOG_STEP "4/10 - Resolvendo IP do banco de dados"

# Tentar obter o IP do host
DB_IP=""

# Primeira tentativa: getent
DB_IP=$(getent hosts "$DB_HOST" | awk '{ print $1 }' | head -n1)

if [ -n "$DB_IP" ]; then
    LOG_INFO "✓ IP resolvido via getent: ${DB_IP}"
else
    # Segunda tentativa: nslookup
    DB_IP=$(nslookup "$DB_HOST" 2>/dev/null | grep -A1 "Name:" | grep "Address:" | awk '{ print $2 }' | head -n1)
    if [ -n "$DB_IP" ]; then
        LOG_INFO "✓ IP resolvido via nslookup: ${DB_IP}"
    fi
fi

if [ -z "$DB_IP" ]; then
    LOG_WARN "⚠ Não foi possível resolver IP para ${DB_HOST}"
    LOG_WARN "⚠ Usando hostname diretamente (pode causar timeouts no evdns2)"
    DB_IP="$DB_HOST"
else
    LOG_INFO "✓ Host ${DB_HOST} resolvido para IP: ${DB_IP}"
    
    # Verificar se é um IP privado
    case "$DB_IP" in
        10.*|172.1[6-9].*|172.2[0-9].*|172.3[0-1].*|192.168.*)
            LOG_INFO "✓ IP é da rede privada Docker (esperado)"
            ;;
        *)
            LOG_WARN "⚠ IP não parece ser da rede Docker privada: ${DB_IP}"
            ;;
    esac
fi

# ============================================================
# ETAPA 5: CONFIGURAÇÃO DO PGBOUNCER
# ============================================================
LOG_STEP "5/10 - Configurando PgBouncer"

# Criar diretório de configuração se não existir
mkdir -p /etc/pgbouncer

# Criar arquivo de usuários
LOG_INFO "Criando userlist.txt..."
{
    echo "\"${DB_USER}\" \"${DB_PASSWORD}\""
} > /etc/pgbouncer/userlist.txt

LOG_INFO "✓ userlist.txt criado para usuário: ${DB_USER}"

# Criar configuração do PgBouncer
LOG_INFO "Gerando pgbouncer.ini..."

# Se temos IP resolvido, usamos ele; senão, usamos o hostname
cat > /etc/pgbouncer/pgbouncer.ini << EOF
################## Auto generated ##################
[databases]
* = host=${DB_IP} port=${DB_PORT:-5432} auth_user=${DB_USER}

[pgbouncer]
listen_addr = 0.0.0.0
listen_port = ${LISTEN_PORT:-6432}
unix_socket_dir = 
user = postgres
auth_file = /etc/pgbouncer/userlist.txt
auth_type = ${AUTH_TYPE:-scram-sha-256}
auth_query = ${AUTH_QUERY:-SELECT usename, passwd FROM pg_shadow WHERE usename=\$1}
pool_mode = ${POOL_MODE:-transaction}
max_client_conn = ${MAX_CLIENT_CONN:-10000}
default_pool_size = ${DEFAULT_POOL_SIZE:-20}
ignore_startup_parameters = ${IGNORE_STARTUP_PARAMETERS:-extra_float_digits,binary_as_text}

# Log settings - VERBOSE para debug
admin_users = ${ADMIN_USERS:-${DB_USER}}
log_connections = 1
log_disconnections = 1
log_pooler_errors = 1
server_idle_timeout = 600
server_lifetime = 3600
server_connect_timeout = 15

# DNS settings - IMPORTANTE para evitar evdns2
# Desabilitar resolução assíncrona do libevent
EOF

LOG_INFO "✓ pgbouncer.ini criado"
LOG_INFO "Configuração gerada:"
LOG_INFO "  Host: ${DB_IP}"
LOG_INFO "  Porta: ${DB_PORT:-5432}"
LOG_INFO "  Usuário: ${DB_USER}"

# ============================================================
# ETAPA 6: VERIFICAÇÃO DA CONECTIVIDADE
# ============================================================
LOG_STEP "6/10 - Verificando conectividade com PostgreSQL"

TIMEOUT=10
RETRIES=3
CONNECTED=false

for i in $(seq 1 $RETRIES); do
    LOG_INFO "Tentativa $i/$RETRIES: nc -z ${DB_IP} ${DB_PORT:-5432} (timeout ${TIMEOUT}s)..."
    
    if nc -z -w $TIMEOUT "$DB_IP" "${DB_PORT:-5432}" 2>/dev/null; then
        LOG_INFO "✓ CONECTIVIDADE OK - Porta ${DB_PORT:-5432} está aberta em ${DB_IP}"
        CONNECTED=true
        break
    else
        LOG_WARN "✗ Tentativa $i falhou - aguardando 2s..."
        sleep 2
    fi
done

if [ "$CONNECTED" = "false" ]; then
    LOG_ERROR "✗ NÃO FOI POSSÍVEL CONECTAR AO POSTGRESQL"
    LOG_ERROR "Host: ${DB_IP}, Porta: ${DB_PORT:-5432}"
    LOG_ERROR "Verifique se o serviço 'db' está saudável"
    
    # Listar containers na rede
    LOG_INFO "Containers na rede (se disponível):"
    cat /etc/hosts | grep -v "^#" | while read line; do
        LOG_INFO "  $line"
    done
    
    # Não abortar - deixar o PgBouncer tentar mesmo assim
    LOG_WARN "Continuando mesmo assim - PgBouncer vai tentar reconectar automaticamente"
else
    LOG_INFO "✓ PostgreSQL está acessível"
fi

# ============================================================
# ETAPA 7: TESTE DE RESOLUÇÃO DNS REVERSA
# ============================================================
LOG_STEP "7/10 - Testando resolução DNS reversa"

if [ -n "$DB_IP" ] && [ "$DB_IP" != "$DB_HOST" ]; then
    LOG_INFO "Verificando resolução reversa de ${DB_IP}..."
    REVERSE_HOST=$(getent hosts "$DB_IP" 2>/dev/null | awk '{print $2}' | head -n1)
    if [ -n "$REVERSE_HOST" ]; then
        LOG_INFO "✓ Resolução reversa: ${DB_IP} → ${REVERSE_HOST}"
    else
        LOG_WARN "⚠ Sem resolução reversa para ${DB_IP}"
    fi
fi

# ============================================================
# ETAPA 8: VERIFICAÇÃO DE PERMISSÕES
# ============================================================
LOG_STEP "8/10 - Verificando permissões de arquivos"

LOG_INFO "Permissões em /etc/pgbouncer:"
ls -la /etc/pgbouncer/ | while read line; do
    LOG_INFO "  $line"
done

LOG_INFO "Verificando se userlist.txt é legível..."
if [ -r /etc/pgbouncer/userlist.txt ]; then
    LOG_INFO "✓ userlist.txt está legível"
else
    LOG_ERROR "✗ userlist.txt não está legível!"
fi

LOG_INFO "Verificando se pgbouncer.ini é legível..."
if [ -r /etc/pgbouncer/pgbouncer.ini ]; then
    LOG_INFO "✓ pgbouncer.ini está legível"
else
    LOG_ERROR "✗ pgbouncer.ini não está legível!"
fi

# ============================================================
# ETAPA 9: SUMÁRIO
# ============================================================
LOG_STEP "9/10 - Sumário da configuração"

echo "
╔════════════════════════════════════════════════════════════╗
║           PGBOUNCER CONFIGURAÇÃO DE BOOT                  ║
╠════════════════════════════════════════════════════════════╣
  Database Host:    ${DB_HOST}
  Database IP:      ${DB_IP}
  Database Port:    ${DB_PORT:-5432}
  Pool Port:        ${LISTEN_PORT:-6432}
  Pool Mode:        ${POOL_MODE:-transaction}
  Max Connections:  ${MAX_CLIENT_CONN:-10000}
  Default Pool:     ${DEFAULT_POOL_SIZE:-20}
  Auth Type:        ${AUTH_TYPE:-scram-sha-256}
  Admin Users:      ${ADMIN_USERS:-${DB_USER}}
  Conectividade:    ${CONNECTED}
╚════════════════════════════════════════════════════════════╝
"

# ============================================================
# ETAPA 10: INICIAR PGBOUNCER
# ============================================================
LOG_STEP "10/10 - Iniciando PgBouncer"

LOG_INFO "Todos os checks completados!"
LOG_INFO "Iniciando PgBouncer com: $@"
LOG_INFO "PID do PgBouncer será logado na saída abaixo:"
echo ""

# Executar PgBouncer com exec para substituir o shell
exec "$@"
