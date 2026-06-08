#!/bin/bash
#
# PANIC RESET - Cascata Emergency Lockdown Recovery Tool
# ========================================================
# Este script é um WRAPPER que detecta automaticamente qual método
# usar para conectar ao Dragonfly:
#
#   1. Se redis-cli disponível → usa redis-cli diretamente
#   2. Se não tiver redis-cli → compila e usa o CLI Go (cascata-cli)
#   3. Se estiver no Docker → executa dentro do container
#
# O Panic Mode é armazenado no Dragonfly (Redis) em memória:
#   - panic:{slug}         -> "true" (indica que está ativo)
#   - panic:admin:{slug}   -> identificador do admin (IP ou UserID)
#   - rps:{slug}          -> requests por segundo (para dashboard)
#
# Infraestrutura:
#   - Dragonfly roda no container 'cascata-dragonfly' porta 6379
#   - O middleware PanicMode verifica ANTES de cada request
#   - Apenas o admin whitelisted ou system requests passam
#
# Uso:
#   ./panic-reset.sh                    # Modo interativo
#   ./panic-reset.sh <slug>             # Desativa panic para projeto
#   ./panic-reset.sh --list             # Lista projetos em panic mode
#   ./panic-reset.sh --status <slug>    # Verifica status do projeto
#   ./panic-reset.sh --force <slug>     # Força reset sem confirmação
#

set -e

# Cores para output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m' # No Color

# Diretórios
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
BACKEND_DIR="$PROJECT_ROOT/backend"
CLI_BINARY="$BACKEND_DIR/cascata-cli"

# Configuração de conexão Dragonfly
DRAGONFLY_HOST="${DRAGONFLY_HOST:-dragonfly}"
DRAGONFLY_PORT="${DRAGONFLY_PORT:-6379}"
DRAGONFLY_URL="${DRAGONFLY_URL:-}"

# Detecta se estamos dentro de um container Docker
IN_DOCKER=false
if [ -f /.dockerenv ] || grep -q docker /proc/1/cgroup 2>/dev/null; then
    IN_DOCKER=true
fi

# Verifica se redis-cli está disponível
has_redis_cli() {
    command -v redis-cli >/dev/null 2>&1
}

# Verifica se Go está disponível
has_go() {
    command -v go >/dev/null 2>&1
}

# Verifica se o CLI Go já está compilado
has_cli_binary() {
    [ -f "$CLI_BINARY" ] && [ -x "$CLI_BINARY" ]
}

# Compila o CLI Go se necessário
ensure_cli_binary() {
    if has_cli_binary; then
        return 0
    fi
    
    echo -e "${CYAN}⏳ Compilando cascata-cli (primeira execução)...${NC}"
    
    if ! has_go; then
        echo -e "${RED}✗ Erro: Go não está instalado${NC}"
        echo -e "${YELLOW}  Instale Go 1.24+ ou use o método Docker:${NC}"
        echo "    docker exec -it cascata-backend /app/cascata-cli <comando>"
        exit 1
    fi
    
    cd "$BACKEND_DIR"
    if go build -o cascata-cli ./cmd/cli/main.go; then
        echo -e "${GREEN}✅ CLI compilado com sucesso!${NC}"
        echo ""
    else
        echo -e "${RED}✗ Falha ao compilar o CLI${NC}"
        exit 1
    fi
}

# Função para executar comando Redis (várias estratégias)
exec_redis() {
    local cmd="$1"
    
    # Estratégia 1: redis-cli disponível localmente
    if has_redis_cli; then
        if [ -n "$DRAGONFLY_URL" ]; then
            redis-cli -u "$DRAGONFLY_URL" $cmd
        else
            redis-cli -h "$DRAGONFLY_HOST" -p "$DRAGONFLY_PORT" $cmd
        fi
        return 0
    fi
    
    # Estratégia 2: Dentro do container
    if [ "$IN_DOCKER" = true ]; then
        # Tenta redis-cli dentro do container
        if command -v redis-cli >/dev/null 2>&1; then
            redis-cli -h "$DRAGONFLY_HOST" -p "$DRAGONFLY_PORT" $cmd
            return 0
        fi
    fi
    
    # Estratégia 3: Fora do container - tenta via docker exec no dragonfly
    if docker ps --format '{{.Names}}' 2>/dev/null | grep -q "cascata-dragonfly"; then
        docker exec cascata-dragonfly redis-cli $cmd
        return 0
    fi
    
    # Estratégia 4: Usa o CLI Go compilado
    ensure_cli_binary
    
    # Converte comando redis para comando CLI
    # KEYS panic:* → cascata-cli panic-list
    # GET panic:slug → cascata-cli panic-status slug
    # DEL panic:slug → cascata-cli panic-reset slug --force
    
    case "$cmd" in
        "KEYS panic:*")
            "$CLI_BINARY" panic-list
            ;;
        "GET panic:"*)
            local slug="${cmd#GET panic:}"
            # Extrai apenas o slug (remove possíveis espaços/extras)
            slug=$(echo "$slug" | awk '{print $1}')
            "$CLI_BINARY" panic-status "$slug" 2>/dev/null | grep -q "ATIVO" && echo "true" || echo ""
            ;;
        "GET panic:admin:"*)
            local slug="${cmd#GET panic:admin:}"
            slug=$(echo "$slug" | awk '{print $1}')
            "$CLI_BINARY" panic-status "$slug" 2>/dev/null | grep "Admin whitelisted" | awk '{print $NF}' || echo "N/A"
            ;;
        "GET rps:"*)
            local slug="${cmd#GET rps:}"
            slug=$(echo "$slug" | awk '{print $1}')
            "$CLI_BINARY" panic-status "$slug" 2>/dev/null | grep "Requests/segundo" | awk '{print $NF}' || echo "0"
            ;;
        "DEL panic:"*)
            # Extrai o slug da chave
            local keys_str="${cmd#DEL }"
            local slug=""
            for key in $keys_str; do
                if [[ "$key" == panic:* ]] && [[ "$key" != panic:admin:* ]] && [[ "$key" != panic:user:* ]]; then
                    slug="${key#panic:}"
                    break
                fi
            done
            if [ -n "$slug" ]; then
                "$CLI_BINARY" panic-reset "$slug" --force >/dev/null 2>&1 && echo "1" || echo "0"
            else
                echo "0"
            fi
            ;;
        *)
            echo -e "${RED}✗ Comando não suportado via CLI: $cmd${NC}" >&2
            return 1
            ;;
    esac
}

# Lista todos os projetos em panic mode
list_panic_projects() {
    echo -e "${BOLD}🔒 Projetos em PANIC MODE:${NC}"
    echo ""
    
    local keys
    keys=$(exec_redis "KEYS panic:*" 2>/dev/null | grep -v "panic:admin:" | grep -v "panic:user:" || true)
    
    if [ -z "$keys" ]; then
        echo -e "${GREEN}   Nenhum projeto está em panic mode.${NC}"
        return 0
    fi
    
    for key in $keys; do
        local slug="${key#panic:}"
        local admin_key="panic:admin:$slug"
        local admin
        admin=$(exec_redis "GET $admin_key" 2>/dev/null || echo "N/A")
        local rps
        rps=$(exec_redis "GET rps:$slug" 2>/dev/null || echo "0")
        
        echo -e "${RED}  • $slug${NC}"
        echo -e "    ${YELLOW}Admin:${NC} $admin"
        echo -e "    ${BLUE}RPS:${NC} ${rps:-0}"
        echo ""
    done
    
    echo -e "${CYAN}Use: ./panic-reset.sh <slug> para desativar${NC}"
}

# Verifica status de um projeto específico
check_status() {
    local slug="$1"
    local panic_key="panic:$slug"
    local admin_key="panic:admin:$slug"
    
    echo -e "${BOLD}📊 Status do projeto '${slug}':${NC}"
    echo ""
    
    local panic_val
    panic_val=$(exec_redis "GET $panic_key" 2>/dev/null || echo "")
    local admin_val
    admin_val=$(exec_redis "GET $admin_key" 2>/dev/null || echo "N/A")
    local rps
    rps=$(exec_redis "GET rps:$slug" 2>/dev/null || echo "0")
    
    if [ "$panic_val" = "true" ]; then
        echo -e "  ${RED}🔴 PANIC MODE: ATIVO${NC}"
        echo -e "  ${YELLOW}   Admin whitelisted:${NC} $admin_val"
        echo -e "  ${BLUE}   Requests/segundo:${NC} $rps"
        echo ""
        echo -e "  ${CYAN}➜ O projeto está bloqueando requests.${NC}"
    else
        echo -e "  ${GREEN}🟢 PANIC MODE: INATIVO${NC}"
        echo -e "  ${BLUE}   Requests/segundo:${NC} $rps"
        echo ""
        echo -e "  ${GREEN}➜ O projeto está operando normalmente.${NC}"
    fi
}

# Desativa panic mode para um projeto
disable_panic() {
    local slug="$1"
    local force="$2"
    local panic_key="panic:$slug"
    local admin_key="panic:admin:$slug"
    
    # Verifica se existe
    local current
    current=$(exec_redis "GET $panic_key" 2>/dev/null || echo "")
    
    if [ "$current" != "true" ]; then
        echo -e "${YELLOW}⚠️  O projeto '${slug}' não está em panic mode.${NC}"
        return 0
    fi
    
    # Mostra status atual
    check_status "$slug"
    echo ""
    
    # Confirmação (a menos que --force)
    if [ "$force" != true ]; then
        echo -e "${RED}${BOLD}⚠️  ATENÇÃO: Isso irá desativar o panic mode!${NC}"
        echo -e "${YELLOW}    O projeto voltará a aceitar requests normalmente.${NC}"
        echo ""
        read -p "Tem certeza que deseja continuar? (digite 'DESATIVAR' para confirmar): " confirm
        
        if [ "$confirm" != "DESATIVAR" ]; then
            echo -e "${CYAN}✗ Operação cancelada.${NC}"
            exit 0
        fi
    fi
    
    # Executa o reset
    echo -e "${CYAN}⏳ Desativando panic mode para '${slug}'...${NC}"
    
    local result
    result=$(exec_redis "DEL $panic_key $admin_key" 2>/dev/null || echo "0")
    
    if [ "$result" -ge 1 ]; then
        echo ""
        echo -e "${GREEN}${BOLD}✅ Panic mode desativado com sucesso!${NC}"
        echo -e "   Projeto: ${BOLD}${slug}${NC}"
        echo ""
        echo -e "${CYAN}ℹ️  O projeto agora aceita requests normalmente.${NC}"
        echo -e "${CYAN}   O admin pode fazer login e reativar o panic mode via dashboard${NC}"
        echo -e "${CYAN}   se necessário (Security → Panic Mode).${NC}"
    else
        echo -e "${YELLOW}⚠️  Não foi possível confirmar a desativação.${NC}"
        exit 1
    fi
}

# Detecta qual método de conexão será usado
get_connection_method() {
    if has_redis_cli; then
        echo "redis-cli"
    elif [ "$IN_DOCKER" = true ] && command -v redis-cli >/dev/null 2>&1; then
        echo "docker-internal"
    elif docker ps --format '{{.Names}}' 2>/dev/null | grep -q "cascata-dragonfly"; then
        echo "docker-exec"
    else
        echo "cascata-cli (Go)"
    fi
}

# Modo interativo
interactive_mode() {
    local method
    method=$(get_connection_method)
    
    echo -e "${BOLD}${BLUE}"
    echo "╔══════════════════════════════════════════════════════════════╗"
    echo "║     🚨 CASCATA PANIC RESET - Emergency Recovery Tool 🚨     ║"
    echo "╚══════════════════════════════════════════════════════════════╝"
    echo -e "${NC}"
    echo "Este utilitário desativa o PANIC MODE de projetos quando"
    echo "o administrador perde acesso (sessão expirada, IP mudou, etc.)"
    echo ""
    echo -e "${CYAN}Método de conexão detectado:${NC} ${GREEN}${method}${NC}"
    echo ""
    
    # Mostra projetos em panic
    list_panic_projects
    echo ""
    
    # Pergunta qual projeto resetar
    read -p "Digite o slug do projeto para desativar panic (ou Enter para sair): " slug
    
    if [ -z "$slug" ]; then
        echo -e "${CYAN}Saindo...${NC}"
        exit 0
    fi
    
    disable_panic "$slug" false
}

# Help
show_help() {
    echo -e "${BOLD}Cascata Panic Reset - Emergency Lockdown Recovery${NC}"
    echo ""
    echo -e "${CYAN}Uso:${NC}"
    echo "  ./panic-reset.sh                      Modo interativo (recomendado)"
    echo "  ./panic-reset.sh <slug>               Desativa panic para projeto"
    echo "  ./panic-reset.sh --list               Lista projetos em panic mode"
    echo "  ./panic-reset.sh --status <slug>      Verifica status do projeto"
    echo "  ./panic-reset.sh --force <slug>       Desativa sem confirmação"
    echo "  ./panic-reset.sh --cli <cmd>          Usa CLI Go diretamente (mais rápido)"
    echo "  ./panic-reset.sh --help               Mostra esta ajuda"
    echo ""
    echo -e "${CYAN}Como funciona:${NC}"
    echo "  Este script detecta automaticamente o método de conexão:"
    echo "    1. Se redis-cli disponível → usa redis-cli diretamente"
    echo "    2. Se não tiver redis-cli → compila e usa o CLI Go (cascata-cli)"
    echo "    3. Se estiver no Docker → executa dentro do container"
    echo ""
    echo -e "${CYAN}Variáveis de Ambiente:${NC}"
    echo "  DRAGONFLY_HOST    Host do Dragonfly (padrão: dragonfly)"
    echo "  DRAGONFLY_PORT    Porta do Dragonfly (padrão: 6379)"
    echo "  DRAGONFLY_URL     URL completa (ex: redis://host:port)"
    echo ""
    echo -e "${CYAN}Exemplos:${NC}"
    echo "  # Desativar panic para projeto 'meu-projeto'"
    echo "  ./panic-reset.sh meu-projeto"
    echo ""
    echo "  # Verificar status antes"
    echo "  ./panic-reset.sh --status meu-projeto"
    echo ""
    echo "  # Listar todos em panic mode"
    echo "  ./panic-reset.sh --list"
    echo ""
    echo -e "${CYAN}Usando o CLI Go diretamente (mais rápido):${NC}"
    echo "  cd backend && go build -o cascata-cli ./cmd/cli/main.go"
    echo "  ./cascata-cli panic-reset meu-projeto"
    echo ""
    echo -e "${YELLOW}⚠️  ATENÇÃO: Use com cuidado! O panic mode é uma medida de segurança.${NC}"
}

# Delega para o CLI Go compilado
use_cli_directly() {
    ensure_cli_binary
    shift  # Remove '--cli'
    # Quando executando no host (fora do Docker), usar localhost
    # pois o Dragonfly expõe a porta 6379 no host via docker-compose
    if [ "$IN_DOCKER" = false ]; then
        export DRAGONFLY_HOST="${DRAGONFLY_HOST:-localhost}"
    fi
    exec "$CLI_BINARY" "$@"
}

# Main
main() {
    # Se o primeiro argumento for --cli, delega diretamente para o CLI Go
    if [ "${1:-}" = "--cli" ]; then
        use_cli_directly "$@"
    fi
    
    # Parse argumentos
    case "${1:-}" in
        --help|-h)
            show_help
            exit 0
            ;;
        --list|-l)
            list_panic_projects
            exit 0
            ;;
        --status|-s)
            if [ -z "${2:-}" ]; then
                echo -e "${RED}✗ Erro: --status requer um slug${NC}"
                echo "Uso: ./panic-reset.sh --status <slug>"
                exit 1
            fi
            check_status "$2"
            exit 0
            ;;
        --force|-f)
            if [ -z "${2:-}" ]; then
                echo -e "${RED}✗ Erro: --force requer um slug${NC}"
                echo "Uso: ./panic-reset.sh --force <slug>"
                exit 1
            fi
            disable_panic "$2" true
            exit 0
            ;;
        "")
            # Sem argumentos - modo interativo
            interactive_mode
            ;;
        -*)
            echo -e "${RED}✗ Opção desconhecida: $1${NC}"
            echo "Use --help para ver as opções disponíveis"
            exit 1
            ;;
        *)
            # Argumento sem prefixo = slug
            disable_panic "$1" false
            ;;
    esac
}

main "$@"