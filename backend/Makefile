# Makefile para build local dos binários Cascata

.PHONY: all build cli api clean

# Diretório de saída
BIN_DIR := ./bin

all: build

build: cli

# Compila apenas o CLI (leve, para uso local)
cli:
	@echo "🛠️  Compilando cascata-cli..."
	@mkdir -p $(BIN_DIR)
	@go build -o $(BIN_DIR)/cascata-cli ./cmd/cli/main.go
	@echo "✅ CLI compilado: $(BIN_DIR)/cascata-cli"

# Compila todos os binários (API, Worker, Engine, CLI)
build-all:
	@echo "🛠️  Compilando todos os binários..."
	@mkdir -p $(BIN_DIR)
	@go build -o $(BIN_DIR)/cascata-api ./cmd/server/main.go
	@go build -o $(BIN_DIR)/cascata-worker ./cmd/server/main.go
	@go build -o $(BIN_DIR)/cascata-engine ./cmd/server/main.go
	@go build -o $(BIN_DIR)/cascata-cli ./cmd/cli/main.go
	@echo "✅ Todos os binários compilados em $(BIN_DIR)/"

# Instala o CLI no /usr/local/bin (requer sudo)
install: cli
	@echo "📦 Instalando cascata-cli em /usr/local/bin..."
	@sudo cp $(BIN_DIR)/cascata-cli /usr/local/bin/
	@sudo chmod +x /usr/local/bin/cascata-cli
	@echo "✅ CLI instalado. Use: cascata-cli --help"

# Limpa binários compilados
clean:
	@echo "🧹 Limpando binários..."
	@rm -rf $(BIN_DIR)
	@echo "✅ Limpo!"

# Comandos de desenvolvimento
dev-cli: cli
	./$(BIN_DIR)/cascata-cli

# Exibe ajuda
help:
	@echo "Comandos disponíveis:"
	@echo "  make cli       - Compila apenas o CLI"
	@echo "  make build-all - Compila todos os binários"
	@echo "  make install   - Instala o CLI no sistema (/usr/local/bin)"
	@echo "  make clean     - Remove binários compilados"
