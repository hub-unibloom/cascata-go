# Cascata CLI - Emergency Admin Tool

Ferramenta de linha de comando para administração de emergência do Cascata, especialmente para recuperação de acesso quando o **Panic Mode** bloqueia todos os requests.

## 🚨 Quando Usar

Use este CLI quando:
- O administrador ativou **Panic Mode** e depois perdeu acesso (sessão expirou, IP mudou, etc.)
- Não consegue mais fazer login no dashboard
- Precisa desativar o lockdown de emergência

## 🏗️ Build

### Opção 1: Build Local (na VPS)

```bash
# A partir da pasta backend/
cd ~/cascata/backend

# Usando o Makefile (recomendado)
make cli

# Ou compilando diretamente
go build -o bin/cascata-cli ./cmd/cli/main.go

# Instalar no PATH do sistema (opcional)
sudo cp bin/cascata-cli /usr/local/bin/
```

### Opção 2: Build no Container (se não tiver Go instalado)

```bash
# Via Docker
docker run --rm -v $(pwd):/app -w /app golang:1.24-alpine \
  sh -c "cd backend && go build -o cascata-cli ./cmd/cli/main.go"
```

## 🐳 Usando com Docker (Recomendado)

Como o CLI precisa conectar ao Dragonfly (que roda no container `cascata-dragonfly`), o uso via Docker é o mais prático:

```bash
# Build da imagem temporária para o CLI
docker run --rm -v $(pwd):/app -w /app golang:1.24-alpine \
  sh -c "cd backend && go build -o cascata-cli ./cmd/cli/main.go"

# Ou executar diretamente dentro do container backend
docker exec -it cascata-backend /app/cascata-cli panic-reset teste
```

## 📝 Uso

### 1. Desativar Panic Mode (Recuperação de Acesso)

```bash
# Modo interativo com confirmação
./cascata-cli panic-reset <slug>

# Sem confirmação (automação)
./cascata-cli panic-reset <slug> --force
```

**Exemplo:**
```bash
./cascata-cli panic-reset meu-projeto
```

### 2. Verificar Status

```bash
./cascata-cli panic-status <slug>
```

### 3. Listar Projetos em Panic Mode

```bash
./cascata-cli panic-list
```

## ⚙️ Configuração

O CLI usa as seguintes variáveis de ambiente (opcionais):

| Variável | Padrão | Descrição |
|----------|--------|-----------|
| `DRAGONFLY_HOST` | `dragonfly` | Host do Dragonfly |
| `DRAGONFLY_PORT` | `6379` | Porta do Dragonfly |
| `DRAGONFLY_URL` | - | URL completa (ex: `redis://host:port`) |
| `DRAGONFLY_PASSWORD` | - | Senha (se necessário) |

## 🔧 Exemplo de Recuperação Completa

```bash
# 1. Verificar quem está bloqueado
./cascata-cli panic-list

# Saída:
# 🔒 Projetos em PANIC MODE:
#
#   • teste
#     Admin: 191.202.114.197
#     RPS: 0
#
# Use: cascata-cli panic-reset <slug> para desativar

# 2. Desativar o panic mode
./cascata-cli panic-reset teste

# Saída:
# 📊 Status do projeto 'teste':
#   🔴 PANIC MODE: ATIVO
#      Admin whitelisted: 191.202.114.197
#      Requests/segundo: 0
#
# ⚠️  ATENÇÃO: Isso irá desativar o panic mode!
# Tem certeza? (digite 'DESATIVAR'): DESATIVAR
#
# ⏳ Desativando panic mode para 'teste'...
#
# ✅ Panic mode desativado com sucesso!
#    Projeto: teste
#
# ℹ️  O projeto agora aceita requests normalmente.
#    O admin pode fazer login e reativar o panic mode via dashboard
#    se necessário (Security → Panic Mode).
```

## 🆘 Troubleshooting

### Erro de conexão
```
✗ Erro: não foi possível conectar ao Dragonfly
```

**Soluções:**
1. Verifique se o container `cascata-dragonfly` está rodando:
   ```bash
   docker ps | grep dragonfly
   ```

2. Execute o CLI dentro do container:
   ```bash
   docker exec cascata-dragonfly redis-cli ping
   ```

3. Verifique as variáveis de ambiente:
   ```bash
   export DRAGONFLY_HOST=localhost
   export DRAGONFLY_PORT=6379
   ./cascata-cli panic-list
   ```

## 📦 Arquitetura

```
┌─────────────────────────────────────────┐
│         cascata-cli (Go binary)        │
│  - Conecta direto no Dragonfly          │
│  - Não depende de redis-cli             │
│  - Usa go-redis client                  │
└─────────────────────────────────────────┘
                   │
                   ▼
┌─────────────────────────────────────────┐
│   Dragonfly (cascata-dragonfly:6379)   │
│  ┌─────────────┐ ┌─────────────────┐  │
│  │ panic:{slug}│ │ panic:admin:{slug│  │
│  │   "true"    │ │  IP ou UserID    │  │
│  └─────────────┘ └─────────────────┘  │
└─────────────────────────────────────────┘
```

## 📝 Notas

- O CLI é **standalone** - não depende do servidor Cascata estar rodando
- Apenas precisa de acesso de rede ao Dragonfly
- Ideal para situações de emergência onde o backend pode estar instável
