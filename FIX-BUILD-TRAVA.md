# 🔧 Solução: Build Travando com "Broken Pipe"

## Problema
O build do `cert_controller` trava durante `exporting layers` ou `unpacking` e a conexão SSH cai com:
```
client_loop: send disconnect: Broken pipe
```

## Causa
**Exaustão de recursos da VPS** (memória/CPU). O build Docker do cert-controller compila Go dentro do container, consumindo muitos recursos.

## Soluções (em ordem de preferência)

### ✅ Solução 1: Build Separado (Recomendado)

Compile o binário Go **fora do Docker** (diretamente na VPS) e depois construa a imagem Docker:

```bash
# 1. Ir para diretório do projeto
cd ~/cascata

# 2. Executar script de build otimizado
./build-cert-controller.sh

# 3. Após o build bem-sucedido, modificar docker-compose.yml
# Descomente a linha 'image:' no serviço cert_controller:
sed -i 's/# image: cascata-cert_controller:latest/image: cascata-cert_controller:latest/' docker-compose.yml

# 4. Subir stack (sem rebuild)
docker compose up -d
```

### ✅ Solução 2: Build com Limites de Recursos

Use as configurações já adicionadas ao `docker-compose.yml`:

```bash
# O docker-compose já tem limites de memória/CPU configurados
# Simplesmente tente novamente:
docker compose build cert_controller --no-cache
docker compose up -d
```

### ✅ Solução 3: Aumentar Swap (VPS Pequenas)

Se sua VPS tem pouca RAM (< 2GB), adicione swap:

```bash
# Criar 2GB de swap
sudo fallocate -l 2G /swapfile
sudo chmod 600 /swapfile
sudo mkswap /swapfile
sudo swapon /swapfile

# Tornar permanente
echo '/swapfile none swap sw 0 0' | sudo tee -a /etc/fstab

# Verificar
free -h
```

### ✅ Solução 4: Build Manual em Duas Etapas

Se nada funcionar, faça o build manualmente:

```bash
# Etapa 1: Compilar binário Go na VPS
cd ~/cascata/backend
export PATH=$PATH:/usr/local/go/bin
go mod tidy
CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o cert-controller ./cmd/cert-controller/main.go

# Etapa 2: Copiar binário para contexto de build
cp cert-controller /tmp/
cp cmd/cert-controller/Dockerfile.optimized /tmp/Dockerfile

# Etapa 3: Build Docker apenas copiando o binário
cd /tmp
docker build -f Dockerfile -t cascata-cert_controller:latest .

# Etapa 4: Usar imagem no docker-compose
# Edite docker-compose.yml e descomente:
# image: cascata-cert_controller:latest

cd ~/cascata
docker compose up -d
```

## 🔍 Diagnóstico Rápido

Verifique os recursos da VPS antes de buildar:

```bash
# Memória disponível
free -h

# CPU cores
nproc

# Espaço em disco
df -h

# Se memória < 1GB livre, use Solução 1 ou 3
```

## ⚠️ Configurações Aplicadas

As seguintes mudanças já foram feitas nos arquivos:

1. **`docker-compose.yml`**: 
   - Limites de memória/CPU para build
   - Limites de runtime para o container
   - Comentário explicando como usar imagem pré-construída

2. **`build-cert-controller.sh`**:
   - Script automatizado de build fora do Docker
   - Detecta arquitetura automaticamente
   - Instala Go se necessário

3. **`Dockerfile.optimized`**:
   - Dockerfile minimalista que só copia binário pré-compilado

## 🚀 Fluxo Recomendado para VPS Pequenas

```bash
# 1. Preparar (uma vez)
cd ~/cascata
chmod +x build-cert-controller.sh

# 2. Build do cert-controller (fora do Docker)
./build-cert-controller.sh

# 3. Modificar compose para usar imagem pré-construída
sed -i 's/# image: cascata-cert_controller:latest/image: cascata-cert_controller:latest/' docker-compose.yml

# 4. Subir todos os serviços
docker compose up -d

# 5. (Opcional) Voltar ao modo build após ter mais recursos
# sed -i 's/image: cascata-cert_controller:latest/# image: cascata-cert_controller:latest/' docker-compose.yml
```

## 📋 Checklist

- [ ] VPS tem pelo menos 1GB RAM livre?
- [ ] Swap configurado (se RAM < 2GB)?
- [ ] Script `build-cert-controller.sh` executado?
- [ ] `docker-compose.yml` modificado para usar `image:`?
- [ ] Containers subiram sem erros? (`docker ps`)

## 🆘 Se Nada Funcionar

Como último recurso, você pode:
1. Buildar localmente (sua máquina) e fazer push da imagem para Docker Hub
2. Usar GitHub Actions para build automático
3. Aumentar temporariamente os recursos da VPS durante o build

---

**Nota**: O código Go está correto. O problema é puramente de infraestrutura/recursos.
