#!/usr/bin/env bash
# ============================================================================
# CASCATA ORCHESTRATOR - INSTALLER v3.0
# Otimizado para: Ubuntu 24.04 LTS (Noble Numbat)
# Arquitetura: x86_64 / ARM64
# ============================================================================
set -euo pipefail
IFS=$'\n\t'

# ----------------------------------------------------------------------------
# CONFIGURAÇÕES GLOBAIS
# ----------------------------------------------------------------------------
readonly INSTALLER_VERSION="3.0.0-ubuntu2404"
readonly CASCATA_DIR="${HOME}/cascata"
readonly DATA_DIR="/cascata-data"
readonly STATE_FILE="${HOME}/.cascata_install_state"
readonly LOG_DIR="/var/log/cascata"
readonly LOG_FILE="${LOG_DIR}/install.log"
readonly CONFIG_FILE="${CASCATA_DIR}/.env"
readonly STATE_BACKUP="${HOME}/.cascata_install_state.backup"

# Variáveis globais com defaults
SSH_PORT="${SSH_PORT:-22}"
IS_ELITE="${IS_ELITE:-false}"
ADMIN_EMAIL="${ADMIN_EMAIL:-admin@cascata.io}"
ADMIN_PASS="${ADMIN_PASS:-}"
ALLOWED_IP="${ALLOWED_IP:-}"
OTP_ENABLED="${OTP_ENABLED:-false}"

# Cores
readonly RED='\033[0;31m'
readonly GREEN='\033[0;32m'
readonly YELLOW='\033[1;33m'
readonly BLUE='\033[0;34m'
readonly CYAN='\033[0;36m'
readonly MAGENTA='\033[0;35m'
readonly BOLD='\033[1m'
readonly DIM='\033[2m'
readonly NC='\033[0m'

# ----------------------------------------------------------------------------
# UTILITÁRIOS DE LOG
# ----------------------------------------------------------------------------
log_init() {
    sudo mkdir -p "$LOG_DIR" 2>/dev/null || mkdir -p "$LOG_DIR"
    sudo chmod 755 "$LOG_DIR" 2>/dev/null || true
    exec 1> >(tee -a "$LOG_FILE")
    exec 2> >(tee -a "$LOG_FILE" >&2)
}

section() {
    echo ""
    echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
    echo -e "${BOLD}  ▸ $1${NC}"
    echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
    echo ""
}

info() {
    echo -e "${CYAN}ℹ${NC} $1"
}

ok() {
    echo -e "${GREEN}✓${NC} $1"
}

warn() {
    echo -e "${YELLOW}⚠${NC} $1"
}

err() {
    echo -e "${RED}✗ ERRO:${NC} $1" >&2
    exit 1
}

# ----------------------------------------------------------------------------
# GESTÃO DE ESTADO (Idempotência + Persistência)
# ----------------------------------------------------------------------------
load_state() {
    if [[ -f "$STATE_FILE" ]]; then
        # shellcheck source=/dev/null
        source "$STATE_FILE"
        ok "Estado anterior carregado"
    fi
    # Defaults se não definido
    PHASE1_DONE="${PHASE1_DONE:-false}"
    PHASE2_DONE="${PHASE2_DONE:-false}"
    PHASE3_DONE="${PHASE3_DONE:-false}"
    IS_ELITE="${IS_ELITE:-false}"
}

save_state() {
    {
        echo "# Cascata Install State - $(date -u +%Y-%m-%dT%H:%M:%SZ)"
        echo "PHASE1_DONE=$PHASE1_DONE"
        echo "PHASE2_DONE=$PHASE2_DONE"
        echo "PHASE3_DONE=$PHASE3_DONE"
        echo "IS_ELITE=$IS_ELITE"
        echo "ADMIN_EMAIL=$ADMIN_EMAIL"
        echo "SSH_PORT=$SSH_PORT"
        echo "OTP_ENABLED=$OTP_ENABLED"
        [[ -n "${ALLOWED_IP:-}" ]] && echo "ALLOWED_IP=$ALLOWED_IP"
    } > "$STATE_FILE"
    chmod 600 "$STATE_FILE"
}

backup_state() {
    [[ -f "$STATE_FILE" ]] && cp "$STATE_FILE" "$STATE_BACKUP"
}

# ----------------------------------------------------------------------------
# GERADORES DE SEGREDO
# ----------------------------------------------------------------------------
gen_hex_secret() {
    local len="${1:-64}"
    local secret
    secret=$(openssl rand -hex "$((len / 2))" 2>/dev/null || \
             dd if=/dev/urandom bs=1 count="$((len / 2))" 2>/dev/null | xxd -p -c 64)
    printf '%s' "$secret"
}

gen_password() {
    local len="${1:-32}"
    openssl rand -base64 48 2>/dev/null | tr -dc 'a-zA-Z0-9' | head -c "$len"
}

gen_url_safe_password() {
    local len="${1:-32}"
    openssl rand -base64 48 2>/dev/null | tr -dc 'a-zA-Z0-9' | head -c "$len"
}

generate_otp_secret() {
    local secret
    secret=$(openssl rand -hex 20 2>/dev/null || dd if=/dev/urandom bs=1 count=20 2>/dev/null | xxd -p)
    printf '%s' "$(echo "$secret" | tr '[:lower:]' '[:upper:]')"
}

# ----------------------------------------------------------------------------
# VALIDAÇÃO MASTER SECRET (CRÍTICO)
# ----------------------------------------------------------------------------
validate_master_secret() {
    local secret="${1:-}"
    local len="${#secret}"
    
    if [[ -z "$secret" ]]; then
        return 1
    fi
    
    if [[ "$len" -lt 64 ]]; then
        return 1
    fi
    
    # Verificar se é hexadecimal válido
    if [[ ! "$secret" =~ ^[0-9a-fA-F]+$ ]]; then
        return 1
    fi
    
    return 0
}

# ----------------------------------------------------------------------------
# PRE-FLIGHT CHECKS
# ----------------------------------------------------------------------------
preflight_checks() {
    section "VERIFICAÇÕES PRÉ-INSTALAÇÃO"
    
    # Ubuntu 24.04 apenas
    if [[ ! -f /etc/os-release ]]; then
        err "Não foi possível detectar o sistema operativo"
    fi
    
    source /etc/os-release
    
    if [[ "$ID" != "ubuntu" ]] || [[ "${VERSION_ID:-}" != "24.04" ]]; then
        warn "Este script é otimizado para Ubuntu 24.04 LTS"
        warn "Sistema detectado: $ID ${VERSION_ID:-desconhecido}"
        read -rp "Continuar mesmo assim? [s/N]: " force_continue
        [[ "$force_continue" =~ ^[Ss]$ ]] || exit 1
    fi
    
    # Verificar privilégios sudo
    if ! sudo -n true 2>/dev/null; then
        warn "Este script requer privilégios sudo"
        warn "Configure sudo sem senha ou execute interativamente"
    fi
    
    # Verificar arquitetura
    local arch
    arch=$(uname -m)
    if [[ "$arch" != "x86_64" && "$arch" != "aarch64" ]]; then
        warn "Arquitetura $arch não testada. Use x86_64 ou ARM64."
    fi
    
    # Verificar conectividade
    if ! curl -fsSL --max-time 10 https://github.com > /dev/null 2>&1; then
        warn "Conectividade com internet limitada. Docker pulls podem falhar."
    fi
    
    # Criar diretório de trabalho
    mkdir -p "$CASCATA_DIR"
    cd "$CASCATA_DIR" || err "Não foi possível acessar $CASCATA_DIR"
    
    ok "Verificações concluídas"
}

# ----------------------------------------------------------------------------
# FASE 1: INSTALAÇÃO DOCKER
# ----------------------------------------------------------------------------
install_docker() {
    section "FASE 1 - Instalação do Docker"
    
    if command -v docker &>/dev/null && docker compose version &>/dev/null 2>&1; then
        ok "Docker já instalado"
        PHASE1_DONE=true
        save_state
        return 0
    fi
    
    info "Atualizando repositórios..."
    sudo apt-get update -qq
    
    info "Instalando dependências..."
    sudo apt-get install -y -qq \
        ca-certificates \
        curl \
        gnupg \
        lsb-release \
        software-properties-common \
        apt-transport-https
    
    info "Adicionando repositório oficial Docker..."
    sudo install -m 0755 -d /etc/apt/keyrings
    curl -fsSL https://download.docker.com/linux/ubuntu/gpg | \
        sudo gpg --dearmor -o /etc/apt/keyrings/docker.gpg
    sudo chmod a+r /etc/apt/keyrings/docker.gpg
    
    echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] \
        https://download.docker.com/linux/ubuntu \
        $(lsb_release -cs) stable" | \
        sudo tee /etc/apt/sources.list.d/docker.list > /dev/null
    
    info "Instalando Docker Engine..."
    sudo apt-get update -qq
    sudo apt-get install -y -qq docker-ce docker-ce-cli containerd.io docker-compose-plugin
    
    info "Configurando Docker..."
    sudo usermod -aG docker "${USER}" 2>/dev/null || true
    sudo systemctl enable docker
    sudo systemctl start docker
    
    # Log rotation
    sudo mkdir -p /etc/docker
    echo '{"log-driver": "json-file", "log-opts": {"max-size": "10m", "max-file": "3"}}' | \
        sudo tee /etc/docker/daemon.json > /dev/null
    
    ok "Docker instalado com sucesso"
    ok "IMPORTANTE: Faça logout e login novamente para usar Docker sem sudo"
    
    PHASE1_DONE=true
    save_state
}

# ----------------------------------------------------------------------------
# CONFIGURAR ADMIN
# ----------------------------------------------------------------------------
configure_admin() {
    section "Configuração do Administrador"
    
    echo -e "${BOLD}Deseja configurar email e senha do administrador?${NC}"
    echo -e "${DIM}(Se não configurar, serão usados valores padrão inseguros)${NC}"
    echo ""
    
    local configure_admin
    read -rp "Configurar agora? [S/n]: " configure_admin
    configure_admin="${configure_admin:-S}"
    
    if [[ "$configure_admin" =~ ^[Ss]$ ]]; then
        while true; do
            read -rp "  Email do admin: " ADMIN_EMAIL
            if [[ "$ADMIN_EMAIL" =~ ^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$ ]]; then
                break
            fi
            warn "Email inválido. Tente novamente."
        done
        
        while true; do
            read -rsp "  Senha (mínimo 12 caracteres): " ADMIN_PASS
            echo ""
            if [[ ${#ADMIN_PASS} -ge 12 ]]; then
                read -rsp "  Confirme a senha: " ADMIN_PASS2
                echo ""
                if [[ "$ADMIN_PASS" == "$ADMIN_PASS2" ]]; then
                    break
                else
                    warn "Senhas não coincidem"
                fi
            else
                warn "Senha muito curta. Use pelo menos 12 caracteres."
            fi
        done
        
        ok "Credenciais configuradas"
    else
        ADMIN_EMAIL="admin@cascata.io"
        ADMIN_PASS=$(gen_password 16)
        warn "Usando credenciais automáticas (INSEGURAS - MUDE APÓS LOGIN!)"
        echo ""
        echo -e "${BOLD}${RED}  SENHA GERADA: ${ADMIN_PASS}${NC}"
        echo -e "${DIM}  Anote esta senha!${NC}"
        echo ""
    fi
}

# ----------------------------------------------------------------------------
# FASE 2: SETUP E SEGREDOS
# ----------------------------------------------------------------------------
setup_secrets() {
    section "FASE 2 - Geração de Segredos"
    
    info "Gerando chaves criptográficas..."
    
    local DB_USER="cascata_admin"
    local DB_NAME="cascata_system"
    local DB_PASS JWT_SECRET CTRL_SECRET MASTER_SECRET
    
    # Gerar novos segredos ou recuperar do .env existente
    if [[ -f "$CONFIG_FILE" ]]; then
        info "Arquivo .env existente encontrado. Verificando segredos..."
        
        # Recuperar segredos existentes
        DB_PASS=$(grep "^DB_PASS=" "$CONFIG_FILE" 2>/dev/null | cut -d'=' -f2 || echo "")
        JWT_SECRET=$(grep "^SYSTEM_JWT_SECRET=" "$CONFIG_FILE" 2>/dev/null | cut -d'=' -f2 || echo "")
        CTRL_SECRET=$(grep "^INTERNAL_CTRL_SECRET=" "$CONFIG_FILE" 2>/dev/null | cut -d'=' -f2 || echo "")
        MASTER_SECRET=$(grep "^CASCATA_MASTER_SECRET=" "$CONFIG_FILE" 2>/dev/null | cut -d'=' -f2 || echo "")
    fi
    
    # Gerar segredos faltantes
    [[ -z "$DB_PASS" ]] && DB_PASS=$(gen_url_safe_password 32)
    [[ -z "$JWT_SECRET" ]] && JWT_SECRET=$(gen_hex_secret 64)
    [[ -z "$CTRL_SECRET" ]] && CTRL_SECRET=$(gen_hex_secret 64)
    
    # ============================================================
    # MASTER SECRET - LÓGICA CRÍTICA CORRIGIDA
    # ============================================================
    
    # 1. Tentar recuperar existente
    if [[ -z "$MASTER_SECRET" && -f "$CONFIG_FILE" ]]; then
        MASTER_SECRET=$(grep "^CASCATA_MASTER_SECRET=" "$CONFIG_FILE" 2>/dev/null | cut -d'=' -f2 || echo "")
        if [[ -n "$MASTER_SECRET" ]]; then
            info "Master Secret recuperada do .env"
        fi
    fi
    
    # 2. Validar Master Secret existente
    local ms_valid=true
    local ms_len="${#MASTER_SECRET}"
    
    if [[ -z "$MASTER_SECRET" ]]; then
        ms_valid=false
        info "Master Secret não encontrada"
    elif [[ "$ms_len" -lt 64 ]]; then
        ms_valid=false
        warn "Master Secret existente inválida (${ms_len} caracteres, necessário 64+)"
    fi
    
    # 3. Se inválida, gerar nova
    if [[ "$ms_valid" == "false" ]]; then
        # ════════════════════════════════════════════════════════════════════════
        # KEYSTORE INCOMPATIBILITY GUARD
        # Se existe um keystore mas a chave mudou (era inválida/ausente),
        # o crypto-engine não conseguirá abrir o arquivo e ficará SEALED
        # silenciosamente. Detectamos e limpamos antes do docker up.
        # ════════════════════════════════════════════════════════════════════════
        local KEYSTORE_PATH="${DATA_DIR:-/cascata-data}/crypto/keys.enc"
        if [[ -f "$KEYSTORE_PATH" ]]; then
            warn "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
            warn "KEYSTORE INCOMPATÍVEL DETECTADO"
            warn "A Master Secret mudou. O keystore existente é incompatível."
            warn "Remover para permitir boot limpo com nova chave."
            warn "Dados do banco são PRESERVADOS. Apenas chaves de projeto mudarão."
            warn "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
            echo ""
            read -rp "Remover keystore incompatível e gerar novo? [S/n]: " reset_ks
            if [[ ! "$reset_ks" =~ ^[Nn]$ ]]; then
                sudo rm -f "$KEYSTORE_PATH"
                ok "Keystore antigo removido. Novo será criado no próximo boot."
            else
                warn "Keystore mantido. Sistema pode ficar em modo SEALED."
                warn "Correção manual: sudo rm -f $KEYSTORE_PATH && docker compose restart cascata-crypto-engine"
            fi
        fi
        MASTER_SECRET=$(gen_hex_secret 64)
        info "Nova Master Secret gerada (${#MASTER_SECRET} caracteres)"
    else
        info "Master Secret válida confirmada (${ms_len} caracteres)"
    fi
    
    # 4. Validação final obrigatória
    if [[ -z "$MASTER_SECRET" ]] || [[ "${#MASTER_SECRET}" -lt 64 ]]; then
        err "FALHA CRÍTICA: Master Secret inválida após geração. Abortando."
    fi
    
    # ============================================================
    # ESCOLHA DO MODO DE SOBERANIA
    # ============================================================
    section "Nível de Soberania Digital"
    
    echo -e "${BOLD}Escolha o modo de operação:${NC}"
    echo -e "  1) ${CYAN}Standard${NC} - Master Secret salva no .env (conveniência)"
    echo -e "  2) ${RED}Sovereign Elite${NC} - Master Secret APENAS na RAM (máxima segurança)"
    echo ""
    
    local sov_choice
    while true; do
        read -rp "Sua escolha [1/2]: " sov_choice
        if [[ "$sov_choice" == "1" || "$sov_choice" == "2" ]]; then
            break
        fi
        warn "Opção inválida. Digite 1 ou 2."
    done
    
    if [[ "$sov_choice" == "2" ]]; then
        IS_ELITE=true
        
        section "ALERTA DE SEGURANÇA MÁXIMA"
        warn "Modo Sovereign Elite ativado!"
        warn "A Master Secret NÃO será salva no arquivo .env"
        warn "Você precisará fornecer esta chave manualmente após cada reboot!"
        echo ""
        echo -e "${MAGENTA}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
        echo -e "${BOLD}  SALVE ESTA CHAVE EM LOCAL SEGURO E OFFLINE:${NC}"
        echo -e "${YELLOW}  ${MASTER_SECRET}${NC}"
        echo -e "${MAGENTA}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
        echo ""
        
        read -rp "Confirma que salvou a chave? [S/n]: " confirm_saved
        [[ "$confirm_saved" =~ ^[Nn]$ ]] && err "Instalação cancelada. Salve a chave e execute novamente."
    else
        IS_ELITE=false
    fi
    
    # ============================================================
    # PERSISTIR NO .ENV
    # ============================================================
    info "Atualizando arquivo de configuração..."
    
    # Criar ou atualizar .env
    {
        echo "# Cascata Orchestrator - Environment Configuration"
        echo "# Gerado em: $(date -u +%Y-%m-%dT%H:%M:%SZ)"
        echo "# Modo: $([[ "$IS_ELITE" == "true" ]] && echo "Sovereign Elite" || echo "Standard")"
        echo ""
        echo "# Database"
        echo "DB_USER=${DB_USER}"
        echo "DB_PASS=${DB_PASS}"
        echo "DB_NAME=${DB_NAME}"
        echo ""
        echo "# Secrets"
        echo "SYSTEM_JWT_SECRET=${JWT_SECRET}"
        echo "INTERNAL_CTRL_SECRET=${CTRL_SECRET}"
        if [[ "$IS_ELITE" == "false" ]]; then
            echo "CASCATA_MASTER_SECRET=${MASTER_SECRET}"
        fi
        echo ""
        echo "# Features"
        echo "CASCATA_OTP_ENABLED=false"
        echo "CASCATA_ALLOWED_IP=${ALLOWED_IP}"
    } > "$CONFIG_FILE"
    
    chmod 600 "$CONFIG_FILE"
    
    if [[ "$IS_ELITE" == "false" ]]; then
        local saved_key
        saved_key=$(grep "^CASCATA_MASTER_SECRET=" "$CONFIG_FILE" | cut -d'=' -f2)
        ok "Configuração salva (${#saved_key} caracteres)"
    else
        ok "Configuração salva (modo Elite - sem Master Secret no arquivo)"
    fi
    
    # Exportar para uso imediato
    export MASTER_SECRET
    export INTERNAL_CTRL_SECRET="${CTRL_SECRET}"
    
    PHASE2_DONE=true
    save_state
}

# ----------------------------------------------------------------------------
# CONFIGURAR OTP
# ----------------------------------------------------------------------------
configure_otp() {
    section "Autenticação de Dois Fatores (OTP)"
    
    echo -e "${BOLD}Habilitar OTP (Google/Microsoft Authenticator)?${NC}"
    read -rp "[S/n]: " enable_otp
    enable_otp="${enable_otp:-S}"
    
    if [[ ! "$enable_otp" =~ ^[Ss]$ ]]; then
        OTP_ENABLED="false"
        info "OTP não habilitado"
        return 0
    fi
    
    local otp_secret
    otp_secret=$(generate_otp_secret)
    
    ok "Segredo OTP gerado"
    
    # Mostrar QR code se qrencode disponível
    if command -v qrencode &>/dev/null; then
        local otp_uri="otpauth://totp/Cascata:${ADMIN_EMAIL}?secret=${otp_secret}&issuer=Cascata"
        echo ""
        echo -e "${MAGENTA}QR Code para escaneamento:${NC}"
        qrencode -t ANSIUTF8 "$otp_uri" 2>/dev/null || true
        echo ""
    fi
    
    echo -e "${YELLOW}Chave manual: ${otp_secret}${NC}"
    echo ""
    read -rp "Pressione ENTER após escanear o QR Code..." dummy
    
    # Salvar OTP no .env (criptografado se possível)
    local otp_enc
    local ctrl_secret
    ctrl_secret=$(grep "^INTERNAL_CTRL_SECRET=" "$CONFIG_FILE" 2>/dev/null | cut -d'=' -f2 || echo "")
    
    if [[ -n "$ctrl_secret" ]] && command -v openssl &>/dev/null; then
        otp_enc=$(echo -n "$otp_secret" | openssl enc -aes-256-cbc -pbkdf2 -pass "pass:${ctrl_secret}" -base64 -A 2>/dev/null || echo "$otp_secret")
    else
        otp_enc="$otp_secret"
    fi
    
    # Atualizar .env
    if [[ -f "$CONFIG_FILE" ]]; then
        grep -v "^CASCATA_OTP" "$CONFIG_FILE" > "${CONFIG_FILE}.tmp" || true
        mv "${CONFIG_FILE}.tmp" "$CONFIG_FILE"
        echo "CASCATA_OTP_ENABLED=true" >> "$CONFIG_FILE"
        echo "CASCATA_OTP_SECRET=${otp_enc}" >> "$CONFIG_FILE"
    fi
    
    OTP_ENABLED="true"
    ok "OTP configurado"
    save_state
}

# ----------------------------------------------------------------------------
# RESTRIÇÃO DE IP
# ----------------------------------------------------------------------------
configure_ip_restriction() {
    section "Restrição de Acesso por IP"
    
    echo -e "${BOLD}Restringir acesso administrativo a um IP específico?${NC}"
    echo -e "${DIM}(Deixe em branco para permitir qualquer IP)${NC}"
    echo ""
    
    read -rp "IP Restrito (ex: 203.0.113.10): " ip_input
    
    if [[ -n "$ip_input" ]]; then
        # Validar formato IP
        if [[ "$ip_input" =~ ^[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}(/[0-9]{1,2})?$ ]]; then
            ALLOWED_IP="$ip_input"
            
            if [[ -f "$CONFIG_FILE" ]]; then
                grep -v "^CASCATA_ALLOWED_IP=" "$CONFIG_FILE" > "${CONFIG_FILE}.tmp" || true
                mv "${CONFIG_FILE}.tmp" "$CONFIG_FILE"
                echo "CASCATA_ALLOWED_IP=${ALLOWED_IP}" >> "$CONFIG_FILE"
            fi
            
            ok "Acesso restrito ao IP: ${ALLOWED_IP}"
            save_state
        else
            warn "Formato de IP inválido. Ignorando restrição."
        fi
    else
        info "Sem restrição de IP"
    fi
}

# ----------------------------------------------------------------------------
# DIRETÓRIOS E SSL
# ----------------------------------------------------------------------------
setup_directories() {
    section "Criação de Diretórios"
    
    info "Criando estrutura de dados em ${DATA_DIR}..."
    
    sudo mkdir -p \
        "${DATA_DIR}/pg" \
        "${DATA_DIR}/qdrant" \
        "${DATA_DIR}/dragonfly" \
        "${DATA_DIR}/storage" \
        "${DATA_DIR}/certs" \
        "${DATA_DIR}/acme_challenge" \
        "${DATA_DIR}/nginx_dynamic" \
        "${DATA_DIR}/crypto"
    
    # Permissões
    sudo chown -R "${USER}:${USER}" "$DATA_DIR"
    sudo chmod 711 "$DATA_DIR"
    sudo chown -R 65532:65532 "${DATA_DIR}/crypto"
    sudo chmod 700 "${DATA_DIR}/crypto"
    
    # Restringir outros diretórios
    find "$DATA_DIR" -maxdepth 1 -not -name "crypto" -not -name "$(basename "$DATA_DIR")" -type d -exec sudo chmod 700 {} \;
    
    ok "Diretórios criados com permissões seguras"
    
    # SSL Bootstrap
    local cert_path="${DATA_DIR}/certs/live/system"
    if [[ ! -f "${cert_path}/fullchain.pem" ]]; then
        info "Gerando certificado SSL auto-assinado temporário..."
        sudo mkdir -p "$cert_path"
        sudo openssl req -x509 -nodes -days 365 -newkey rsa:2048 \
            -keyout "${cert_path}/privkey.pem" \
            -out "${cert_path}/fullchain.pem" \
            -subj "/C=BR/ST=SP/L=Cascata/O=System/CN=localhost" 2>/dev/null
        sudo chmod 600 "${cert_path}/privkey.pem"
        sudo chmod 644 "${cert_path}/fullchain.pem"
        ok "Certificado temporário gerado"
    fi
}

# ----------------------------------------------------------------------------
# INICIALIZAR CONTAINERS
# ----------------------------------------------------------------------------
start_containers() {
    section "Inicialização dos Containers"
    
    # Verificar docker-compose.yml
    if [[ ! -f "docker-compose.yml" ]]; then
        err "Arquivo docker-compose.yml não encontrado em ${CASCATA_DIR}"
    fi
    
    info "Construindo e iniciando containers..."
    
    if ! docker compose up -d --build 2>&1; then
        err "Falha ao iniciar containers. Verifique: docker compose logs"
    fi
    
    ok "Containers iniciados"
    
    # Health check
    info "Aguardando saúde dos containers..."
    local retry=0
    local max_retry=30
    
    while [[ $retry -lt $max_retry ]]; do
        local healthy
        healthy=$(docker compose ps --format json 2>/dev/null | \
            python3 -c "
import sys, json
data = sys.stdin.read().strip()
if not data: print('0'); sys.exit(0)
try:
    containers = [json.loads(line) for line in data.split('\n') if line.strip()]
    healthy = sum(1 for c in containers if 'healthy' in str(c.get('Health', c.get('Status', ''))).lower())
    total = len(containers)
    print(f'{healthy}/{total}')
except: print('0/0')
" 2>/dev/null || echo "0/0")
        
        if [[ "$healthy" == *"/"* ]]; then
            local hc_ok hc_total
            hc_ok=$(echo "$healthy" | cut -d'/' -f1)
            hc_total=$(echo "$healthy" | cut -d'/' -f2)
            
            if [[ "$hc_ok" -eq "$hc_total" && "$hc_total" -gt 0 ]]; then
                ok "Todos os containers saudáveis (${hc_ok}/${hc_total})"
                break
            fi
            
            info "Aguardando... (${hc_ok}/${hc_total}) [${retry}/${max_retry}]"
        fi
        
        sleep 3
        retry=$((retry + 1))
    done
    
    if [[ $retry -eq $max_retry ]]; then
        warn "Timeout aguardando containers. Verifique: docker compose ps"
    fi
    
    # Smoke test
    sleep 2
    if curl -fsSL --max-time 10 http://localhost/ > /dev/null 2>&1; then
        ok "Cascata respondendo na porta 80"
    else
        warn "Smoke test falhou - verifique os logs"
    fi
}

# ----------------------------------------------------------------------------
# HARDENING DO SISTEMA
# ----------------------------------------------------------------------------
hardening_system() {
    section "Hardening do Sistema Operacional"
    
    echo -e "${RED}${BOLD}╔══════════════════════════════════════════════════════════════╗${NC}"
    echo -e "${RED}${BOLD}║  MODO FORTALEZA - HARDENING DE SEGURANÇA                   ║${NC}"
    echo -e "${RED}${BOLD}║  • SSH: Chave + OTP obrigatórios                            ║${NC}"
    echo -e "${RED}${BOLD}║  • Firewall: Entrada 22/80/443, saída restrita              ║${NC}"
    echo -e "${RED}${BOLD}║  • Root: Desabilitado no SSH                               ║${NC}"
    echo -e "${RED}${BOLD}║  • Auditoria: Logs imutáveis                               ║${NC}"
    echo -e "${RED}${BOLD}╚══════════════════════════════════════════════════════════════╝${NC}"
    echo ""
    
    read -rp "Iniciar hardening? [S/n]: " do_harden
    do_harden="${do_harden:-S}"
    
    if [[ ! "$do_harden" =~ ^[Ss]$ ]]; then
        warn "Hardening ignorado"
        PHASE3_DONE=true
        save_state
        return 0
    fi
    
    # Verificar chave SSH
    section "Verificação de Chave SSH"
    
    if [[ -f ~/.ssh/authorized_keys ]] && [[ -s ~/.ssh/authorized_keys ]]; then
        local key_count
        key_count=$(wc -l < ~/.ssh/authorized_keys)
        ok "${key_count} chave(s) SSH encontrada(s)"
    else
        warn "Nenhuma chave SSH encontrada!"
        echo "Cole sua chave pública SSH (ou deixe vazio para continuar com senha):"
        local ssh_key
        read -r ssh_key
        
        if [[ -n "$ssh_key" ]]; then
            mkdir -p ~/.ssh
            chmod 700 ~/.ssh
            echo "$ssh_key" >> ~/.ssh/authorized_keys
            chmod 600 ~/.ssh/authorized_keys
            ok "Chave SSH adicionada"
        else
            warn "Continuando sem chave SSH - hardening parcial"
        fi
    fi
    
    # Token de hardening
    local token
    token=$(openssl rand -hex 4 2>/dev/null || echo "cascata$(date +%s)")
    
    echo ""
    echo -e "${YELLOW}Token de hardening: ${BOLD}${token}${NC}"
    echo -e "${DIM}Arquivos de configuração usarão este identificador${NC}"
    echo ""
    
    # Instalar pacotes de segurança
    section "Instalando Ferramentas de Segurança"
    
    sudo apt-get update -qq
    sudo DEBIAN_FRONTEND=noninteractive apt-get install -y -qq \
        ufw fail2ban auditd \
        libpam-google-authenticator \
        apparmor apparmor-utils \
        unattended-upgrades \
        2>/dev/null || warn "Alguns pacotes podem não ter sido instalados"
    
    ok "Pacotes de segurança instalados"
    
    # Configurar OTP no SSH
    section "OTP no SSH"
    
    if command -v google-authenticator &>/dev/null; then
        local ssh_otp_secret
        ssh_otp_secret=$(generate_otp_secret)
        
        # Configurar google-authenticator
        local ga_file="${HOME}/.google_authenticator"
        
        # Gerar códigos de recuperação
        local scratch_codes
        scratch_codes=$(for _ in {1..5}; do shuf -i 10000000-99999999 -n1 2>/dev/null || echo "$RANDOM$RANDOM"; done)
        
        {
            echo "$ssh_otp_secret"
            echo '" RATE_LIMIT 3 30'
            echo '" WINDOW_SIZE 3'
            echo '" DISALLOW_REUSE'
            echo '" TOTP_AUTH'
            echo "$scratch_codes"
        } > "$ga_file"
        
        chmod 600 "$ga_file"
        
        ok "OTP SSH configurado"
        
        echo ""
        echo -e "${MAGENTA}QR Code para SSH OTP:${NC}"
        local ssh_otp_uri="otpauth://totp/SSH:$(whoami)@$(hostname)?secret=${ssh_otp_secret}&issuer=Cascata-SSH"
        qrencode -t ANSIUTF8 "$ssh_otp_uri" 2>/dev/null || echo "URI: $ssh_otp_uri"
        echo ""
        
        echo -e "${RED}CÓDIGOS DE RECUPERAÇÃO (guarde offline):${NC}"
        echo "$scratch_codes"
        echo ""
        
        read -rp "Pressione ENTER após anotar os códigos..." dummy
        
        # Configurar PAM
        if [[ -f /etc/pam.d/sshd ]]; then
            if ! grep -q "pam_google_authenticator" /etc/pam.d/sshd; then
                sudo sed -i 's/^@include common-auth/# @include common-auth/' /etc/pam.d/sshd
                sudo sed -i '1s|^|auth required pam_google_authenticator.so\n|' /etc/pam.d/sshd
                ok "PAM configurado para OTP"
            fi
        fi
    fi
    
    # Configurar SSH
    section "Configuração SSH Hardened"
    
    read -rp "Porta SSH personalizada [22]: " custom_port
    SSH_PORT="${custom_port:-22}"
    
    if ! [[ "$SSH_PORT" =~ ^[0-9]+$ ]] || [[ "$SSH_PORT" -lt 1 ]] || [[ "$SSH_PORT" -gt 65535 ]]; then
        warn "Porta inválida, usando 22"
        SSH_PORT=22
    fi
    
    # Backup
    sudo cp /etc/ssh/sshd_config /etc/ssh/sshd_config.backup."$(date +%s)" 2>/dev/null || true
    
    # Configuração hardening
    sudo tee "/etc/ssh/sshd_config.d/cascata-${token}.conf" > /dev/null <<EOF
# Cascata SSH Hardening
Port ${SSH_PORT}
PermitRootLogin no
PasswordAuthentication no
PubkeyAuthentication yes
ChallengeResponseAuthentication yes
UsePAM yes
X11Forwarding no
AllowAgentForwarding no
AllowTcpForwarding no
PermitEmptyPasswords no
HostbasedAuthentication no
IgnoreRhosts yes
LoginGraceTime 30
ClientAliveInterval 300
ClientAliveCountMax 2
MaxAuthTries 3
MaxSessions 3
Ciphers chacha20-poly1305@openssh.com,aes256-gcm@openssh.com
MACs hmac-sha2-512-etm@openssh.com,hmac-sha2-256-etm@openssh.com
KexAlgorithms curve25519-sha256,curve25519-sha256@libssh.org
LogLevel VERBOSE
SyslogFacility AUTH
Banner /etc/issue.net
AuthenticationMethods publickey,keyboard-interactive
EOF
    
    # Banner legal
    sudo tee /etc/issue.net > /dev/null <<'EOF'
╔══════════════════════════════════════════════════════════════════════╗
║  SISTEMA PRIVADO - ACESSO RESTRITO E MONITORADO                     ║
║  Acesso não autorizado é crime e será reportado às autoridades.     ║
╚══════════════════════════════════════════════════════════════════════╝
EOF
    
    # Validar e recarregar
    if sudo sshd -t -f /etc/ssh/sshd_config 2>/dev/null; then
        sudo systemctl reload sshd 2>/dev/null || sudo systemctl restart sshd
        ok "SSH recarregado com hardening"
    else
        warn "Erro na validação do SSH config"
    fi
    
    save_state
    
    # Firewall UFW
    section "Firewall UFW"
    
    sudo ufw --force reset > /dev/null 2>&1 || true
    sudo ufw default deny incoming
    sudo ufw default deny outgoing
    sudo ufw default deny forward
    
    # Entradas
    sudo ufw allow in "${SSH_PORT}/tcp" comment 'SSH'
    sudo ufw allow in 80/tcp comment 'HTTP'
    sudo ufw allow in 443/tcp comment 'HTTPS'
    
    # Saídas
    sudo ufw allow out 53/udp comment 'DNS'
    sudo ufw allow out 53/tcp comment 'DNS'
    sudo ufw allow out 123/udp comment 'NTP'
    sudo ufw allow out 443/tcp comment 'HTTPS'
    sudo ufw allow out 80/tcp comment 'HTTP'
    sudo ufw allow out on lo
    sudo ufw allow in on lo
    
    # Docker networks
    sudo ufw allow out to 172.16.0.0/12 comment 'Docker'
    sudo ufw allow in from 172.16.0.0/12 comment 'Docker'
    
    # IP específico
    if [[ -n "${ALLOWED_IP:-}" ]]; then
        sudo ufw allow in from "$ALLOWED_IP" comment 'Admin IP'
    fi
    
    sudo ufw --force enable
    sudo systemctl enable ufw
    
    ok "Firewall ativado"
    sudo ufw status numbered 2>/dev/null | head -20 || true
    
    # Fail2Ban
    section "Fail2Ban"
    
    sudo tee "/etc/fail2ban/jail.d/cascata-${token}.conf" > /dev/null <<EOF
[DEFAULT]
bandtime = 3600
findtime = 600
maxretry = 3
ignoreip = 127.0.0.1/8 ::1

[sshd]
enabled = true
port = ${SSH_PORT}
filter = sshd
logpath = /var/log/auth.log
maxretry = 3
bandtime = 86400
EOF
    
    sudo systemctl enable fail2ban --quiet 2>/dev/null || true
    sudo systemctl restart fail2ban 2>/dev/null || true
    ok "Fail2Ban configurado"
    
    # Auditd
    section "Sistema de Auditoria"
    
    if command -v auditctl &>/dev/null; then
        sudo tee "/etc/audit/rules.d/cascata-${token}.rules" > /dev/null <<EOF
# Cascata Audit Rules
-w /etc/passwd -p wa -k identity
-w /etc/shadow -p wa -k identity
-w /etc/ssh/sshd_config -p wa -k ssh_config
-w ${CONFIG_FILE} -p wa -k cascata_secrets
-w ${DATA_DIR} -p wa -k cascata_data
-w /usr/bin/sudo -p x -k privilege
-a always,exit -F arch=b64 -S connect -k network
-e 2
EOF
        sudo systemctl enable auditd --quiet 2>/dev/null || true
        sudo systemctl restart auditd 2>/dev/null || true
        ok "Auditd ativado"
    fi
    
    # Kernel hardening
    section "Hardening do Kernel"
    
    sudo tee "/etc/sysctl.d/cascata-${token}-harden.conf" > /dev/null <<'EOF'
# Kernel Hardening
net.ipv4.conf.all.rp_filter = 1
net.ipv4.conf.all.accept_redirects = 0
net.ipv4.conf.all.accept_source_route = 0
net.ipv4.tcp_syncookies = 1
net.ipv4.icmp_echo_ignore_broadcasts = 1
net.ipv4.conf.all.log_martians = 1
kernel.dmesg_restrict = 1
kernel.kptr_restrict = 2
fs.suid_dumpable = 0
kernel.yama.ptrace_scope = 2
net.core.somaxconn = 8192
EOF
    
    sudo sysctl --system > /dev/null 2>&1 || true
    ok "Hardening do kernel aplicado"
    
    # Atualizações automáticas
    section "Atualizações Automáticas"
    
    sudo tee /etc/apt/apt.conf.d/50unattended-upgrades-cascata > /dev/null <<'EOF'
Unattended-Upgrade::Allowed-Origins {
    "${distro_id}:${distro_codename}-security";
};
Unattended-Upgrade::AutoFixInterruptedDpkg "true";
Unattended-Upgrade::Remove-Unused-Dependencies "true";
Unattended-Upgrade::Automatic-Reboot "false";
EOF
    
    sudo systemctl enable unattended-upgrades --quiet 2>/dev/null || true
    ok "Atualizações de segurança automáticas ativadas"
    
    # Arquivos imutáveis
    section "Proteção de Arquivos"
    
    local immutable_files=(
        "/etc/ssh/sshd_config.d/cascata-${token}.conf"
        "/etc/fail2ban/jail.d/cascata-${token}.conf"
        "/etc/sysctl.d/cascata-${token}-harden.conf"
        "/etc/issue.net"
    )
    
    for file in "${immutable_files[@]}"; do
        if [[ -f "$file" ]]; then
            sudo chattr +i "$file" 2>/dev/null && ok "Imutável: $file" || true
        fi
    done
    
    PHASE3_DONE=true
    save_state
    
    section "Hardening Concluído"
    
    echo -e "${GREEN}Configurações aplicadas:${NC}"
    echo -e "  ✓ SSH: Porta ${SSH_PORT}, Chave + OTP, Root desabilitado"
    echo -e "  ✓ Firewall: UFW ativo com políticas restritivas"
    echo -e "  ✓ Fail2Ban: 3 tentativas = ban 24h"
    echo -e "  ✓ Auditoria: Logs imutáveis"
    echo -e "  ✓ Kernel: Proteções anti-spoofing/flood"
    echo ""
    echo -e "${YELLOW}Para conectar:${NC} ssh -p ${SSH_PORT} $(whoami)@SEU_IP"
}

# ----------------------------------------------------------------------------
# UNSEAL AUTOMÁTICO (Modo Elite)
# ----------------------------------------------------------------------------
elite_unseal() {
    if [[ "$IS_ELITE" != "true" ]]; then
        return 0
    fi
    
    if [[ -z "${MASTER_SECRET:-}" ]]; then
        warn "Modo Elite ativo mas MASTER_SECRET não disponível em memória"
        warn "Desbloqueio manual necessário via painel web ou curl"
        return 0
    fi
    
    section "Desbloqueio Sovereign Elite"
    info "Aguardando backend e crypto-engine ficarem prontos (máx. 60s)..."
    
    local max_attempts=12
    local attempt=0
    local response
    
    while [[ $attempt -lt $max_attempts ]]; do
        attempt=$((attempt + 1))
        sleep 5
        
        # O endpoint /auth/sovereign/unseal é público (sem JWT) — ver auth.go whitelist
        # X-Crypto-Auth NÃO é necessário aqui (é um header interno do crypto-engine,
        # não do backend Go). O backend já injeta X-Crypto-Auth internamente ao repassar
        # a chamada para o crypto-engine via doCryptoRequest().
        response=$(curl -s --max-time 10 -X POST \
            http://localhost/api/control/auth/sovereign/unseal \
            -H "Content-Type: application/json" \
            -d "{\"master_secret\": \"${MASTER_SECRET}\"}" 2>/dev/null || echo '{"success":false,"error":"curl_failed"}')
        
        if echo "$response" | grep -q '"success":true'; then
            ok "Engine desbloqueada com sucesso! (tentativa ${attempt}/${max_attempts})"
            return 0
        fi
        
        info "  Tentativa ${attempt}/${max_attempts}: aguardando serviços... [resp: $(echo "$response" | head -c 80)]"
    done
    
    warn "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    warn "Unseal automático falhou após ${max_attempts} tentativas (${attempt} x 5s = $((attempt * 5))s)"
    warn ""
    warn "Para desbloquear manualmente após o sistema estar online:"
    warn "  curl -X POST http://SEU_DOMINIO/api/control/auth/sovereign/unseal \\"
    warn "    -H 'Content-Type: application/json' \\"
    warn "    -d '{\"master_secret\": \"SUA_CHAVE_64_CHARS\"}'"
    warn "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
}

# ----------------------------------------------------------------------------
# RESUMO FINAL
# ----------------------------------------------------------------------------
show_summary() {
    section "Instalação Concluída!"
    
    local public_ip
    public_ip=$(curl -fsSL --max-time 5 https://api.ipify.org 2>/dev/null || \
                hostname -I | awk '{print $1}' || \
                echo "SEU_IP")
    
    echo -e "${GREEN}${BOLD}"
    echo "┌──────────────────────────────────────────────────────────┐"
    echo "│  Cascata Orchestrator está em execução!                 │"
    echo "└──────────────────────────────────────────────────────────┘"
    echo -e "${NC}"
    
    echo -e "${BOLD}Acesso:${NC}"
    echo -e "  Painel: ${CYAN}http://${public_ip}${NC} $([[ "$OTP_ENABLED" == "true" ]] && echo "+ OTP")"
    echo -e "  SSH:    ${CYAN}ssh -p ${SSH_PORT} $(whoami)@${public_ip}${NC} $([[ "$PHASE3_DONE" == "true" ]] && echo "+ OTP")"
    echo ""
    
    echo -e "${BOLD}Credenciais:${NC}"
    echo -e "  Email: ${ADMIN_EMAIL}"
    if [[ "$ADMIN_EMAIL" == "admin@cascata.io" ]]; then
        echo -e "  Senha: ${RED}${ADMIN_PASS}${NC} (ALTERE APÓS LOGIN!)"
    fi
    echo ""
    
    if [[ "$IS_ELITE" == "true" ]]; then
        echo -e "${RED}${BOLD}Modo Elite Ativo:${NC}"
        echo -e "  ${YELLOW}Guarde a Master Secret offline! Sem ela, os dados estão perdidos.${NC}"
        echo ""
    fi
    
    echo -e "${DIM}Log completo: ${LOG_FILE}${NC}"
    echo ""
    
    # Recibo
    cat > "${CASCATA_DIR}/.cascata_install_receipt" <<EOF
# Cascata Install Receipt
VERSION=${INSTALLER_VERSION}
DATE=$(date -u +%Y-%m-%dT%H:%M:%SZ)
ADMIN_EMAIL=${ADMIN_EMAIL}
IS_ELITE=${IS_ELITE}
OTP_ENABLED=${OTP_ENABLED}
SSH_PORT=${SSH_PORT}
EOF
    chmod 600 "${CASCATA_DIR}/.cascata_install_receipt"
}

# ----------------------------------------------------------------------------
# MAIN
# ----------------------------------------------------------------------------
main() {
    # Inicializar logging
    log_init
    
    echo -e "${BOLD}${CYAN}"
    echo "   ____   _    ____   ____ _____  _    ____ _____  _    "
    echo "  / ___| | |  / ___| / ___|_   _|/ \  |  _ \_   _|/ \   "
    echo "  | |     | | | |     \___ \ | | / _ \ | | | || | / _ \  "
    echo "  | |___  | | | |___   ___) || |/ ___ \| |_| || |/ ___ \ "
    echo "   \____| |_|  \____| |____/ |_/_/   \_\\____/ |_/_/   \_\\"
    echo ""
    echo -e "${NC}"
    echo -e "${BOLD}  Installer v${INSTALLER_VERSION} - Ubuntu 24.04 LTS${NC}"
    echo ""
    
    # Carregar estado
    load_state
    
    # Verificações
    preflight_checks
    
    # Fase 1: Docker
    if [[ "$PHASE1_DONE" == "false" ]]; then
        install_docker
    else
        ok "Fase 1 (Docker) já concluída"
    fi
    
    # Fase 2: Setup
    if [[ "$PHASE2_DONE" == "false" ]] || ! validate_master_secret "${MASTER_SECRET:-}"; then
        configure_admin
        setup_secrets
        configure_otp
        configure_ip_restriction
        setup_directories
        start_containers
    else
        ok "Fase 2 (Setup) já concluída"
    fi
    
    # Fase 3: Hardening
    if [[ "$PHASE3_DONE" == "false" ]]; then
        hardening_system
    else
        ok "Fase 3 (Hardening) já concluída"
    fi
    
    # Unseal automático (Elite)
    elite_unseal
    
    # Resumo
    show_summary
}

# Tratamento de erros
trap 'err "Instalação interrompida na linha $LINENO"' ERR
trap 'echo -e "\n${YELLOW}Instalação cancelada pelo usuário${NC}"; exit 130' INT TERM

# Executar
main "$@"
