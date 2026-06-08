#!/bin/bash
# Build otimizado do cert-controller - compila fora do Docker para economizar recursos

set -e

echo "=========================================="
echo "Cascata Cert-Controller Builder (Otimizado)"
echo "=========================================="

# Detectar arquitetura
ARCH=$(uname -m)
GOARCH="amd64"
if [ "$ARCH" = "aarch64" ] || [ "$ARCH" = "arm64" ]; then
    GOARCH="arm64"
fi

echo "Arquitetura detectada: $ARCH (GOARCH=$GOARCH)"

# Verificar se Go está instalado
if ! command -v go &> /dev/null; then
    echo "❌ Go não encontrado! Instalando Go 1.24.2..."
    
    # Download e instalação leve do Go
    GO_VERSION="1.24.2"
    wget -q "https://go.dev/dl/go${GO_VERSION}.linux-${GOARCH}.tar.gz" -O /tmp/go.tar.gz
    sudo tar -C /usr/local -xzf /tmp/go.tar.gz
    rm /tmp/go.tar.gz
    
    export PATH=$PATH:/usr/local/go/bin
    echo 'export PATH=$PATH:/usr/local/go/bin' >> ~/.bashrc
fi

echo "✓ Go version: $(go version)"

# Diretório do backend
BACKEND_DIR="$(cd "$(dirname "$0")/backend" && pwd)"
cd "$BACKEND_DIR"

# Gerar go.sum se necessário
if [ ! -f go.sum ]; then
    echo "📦 Gerando go.sum..."
    go mod tidy
fi

# Compilar binário estático (muito mais rápido que build Docker)
echo "🔨 Compilando cert-controller binário..."
CGO_ENABLED=0 GOOS=linux GOARCH=$GOARCH go build -ldflags="-s -w" -o /tmp/cert-controller ./cmd/cert-controller/main.go

echo "✓ Binário compilado: /tmp/cert-controller"
ls -lh /tmp/cert-controller

# Build Docker apenas copiando o binário (ultra-rápido)
echo "🐳 Criando imagem Docker leve..."

# Criar Dockerfile temporário minimalista
cat > /tmp/Dockerfile.cert-controller << 'EOF'
FROM alpine:3.19

RUN apk add --no-cache certbot certbot-nginx openssl ca-certificates curl

RUN mkdir -p /etc/letsencrypt /var/www/html /etc/nginx/conf.d/dynamic

COPY cert-controller /usr/local/bin/cert-controller

HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
    CMD pgrep cert-controller || exit 1

CMD ["/usr/local/bin/cert-controller"]
EOF

# Build Docker com contexto mínimo
cd /tmp
docker build -f Dockerfile.cert-controller -t cascata-cert_controller:latest .

echo "=========================================="
echo "✅ Build completo! Imagem: cascata-cert_controller:latest"
echo "=========================================="
