package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	version = "1.0.0"
)

// Colors
var (
	red     = "\033[0;31m"
	green   = "\033[0;32m"
	yellow  = "\033[1;33m"
	blue    = "\033[0;34m"
	cyan    = "\033[0;36m"
	bold    = "\033[1m"
	dim     = "\033[2m"
	reset   = "\033[0m"
)

func main() {
	if len(os.Args) < 2 {
		showHelp()
		os.Exit(0)
	}

	command := os.Args[1]

	switch command {
	case "help", "--help", "-h":
		showHelp()
	case "version", "--version", "-v":
		fmt.Printf("Cascata CLI v%s\n", version)
	case "panic-reset":
		handlePanicReset(os.Args[2:])
	case "panic-status":
		handlePanicStatus(os.Args[2:])
	case "panic-list":
		handlePanicList()
	default:
		fmt.Printf("%s✗ Comando desconhecido: %s%s\n", red, command, reset)
		fmt.Println("Use 'help' para ver os comandos disponíveis")
		os.Exit(1)
	}
}

func showHelp() {
	fmt.Printf(`%s╔══════════════════════════════════════════════════════════════╗%s
%s║              🌊 CASCATA CLI - Admin Tool v%s               ║%s
%s╚══════════════════════════════════════════════════════════════╝%s

%sComandos disponíveis:%s

  %spanic-reset <slug>%s    Desativa o panic mode de um projeto
  %spanic-status <slug>%s  Verifica o status do panic mode
  %spanic-list%s           Lista projetos em panic mode
  %shelp%s                 Mostra esta ajuda
  %sversion%s              Mostra a versão

%sExemplos:%s
  # Desativar panic mode para projeto 'teste'
  cascata-cli panic-reset teste

  # Verificar status antes
  cascata-cli panic-status teste

  # Listar todos em panic mode
  cascata-cli panic-list

%sConfiguração:%s
  As variáveis de ambiente DRAGONFLY_HOST e DRAGONFLY_PORT são usadas
  para conectar ao Dragonfly. Padrão: dragonfly:6379

`, bold, reset, bold, version, reset, bold, reset, bold, reset,
		cyan, reset, cyan, reset, cyan, reset, cyan, reset, cyan, reset,
		bold, reset, bold, reset)
}

func getDragonflyClient() *redis.Client {
	host := os.Getenv("DRAGONFLY_HOST")
	if host == "" {
		host = "dragonfly"
	}
	port := os.Getenv("DRAGONFLY_PORT")
	if port == "" {
		port = "6379"
	}

	addr := fmt.Sprintf("%s:%s", host, port)
	
	// Se DRAGONFLY_URL estiver definido, usa ele
	if url := os.Getenv("DRAGONFLY_URL"); url != "" {
		addr = url
	}

	opts := &redis.Options{
		Addr: addr,
	}

	// Se tiver senha
	if pass := os.Getenv("DRAGONFLY_PASSWORD"); pass != "" {
		opts.Password = pass
	}

	return redis.NewClient(opts)
}

func handlePanicReset(args []string) {
	if len(args) < 1 {
		fmt.Printf("%s✗ Erro: slug do projeto não fornecido%s\n", red, reset)
		fmt.Println("Uso: cascata-cli panic-reset <slug>")
		os.Exit(1)
	}

	slug := args[0]
	
	// Verificar flag --force
	force := false
	for _, arg := range args {
		if arg == "--force" || arg == "-f" {
			force = true
		}
	}

	client := getDragonflyClient()
	ctx := context.Background()

	// Testar conexão
	if err := client.Ping(ctx).Err(); err != nil {
		fmt.Printf("%s✗ Erro: não foi possível conectar ao Dragonfly%s\n", red, reset)
		fmt.Printf("   %s%s%s\n", yellow, err.Error(), reset)
		fmt.Println("\nVerifique se:")
		fmt.Println("  • O container 'cascata-dragonfly' está rodando")
		fmt.Println("  • As variáveis DRAGONFLY_HOST/PORT estão corretas")
		os.Exit(1)
	}

	panicKey := fmt.Sprintf("panic:%s", slug)
	adminKey := fmt.Sprintf("panic:admin:%s", slug)

	// Verifica se está em panic mode
	val, err := client.Get(ctx, panicKey).Result()
	if err == redis.Nil {
		fmt.Printf("%s⚠️  O projeto '%s' não está em panic mode%s\n", yellow, slug, reset)
		os.Exit(0)
	} else if err != nil {
		fmt.Printf("%s✗ Erro ao verificar panic mode: %v%s\n", red, err, reset)
		os.Exit(1)
	}

	if val != "true" {
		fmt.Printf("%s⚠️  O projeto '%s' não está em panic mode%s\n", yellow, slug, reset)
		os.Exit(0)
	}

	// Mostrar status atual
	admin, _ := client.Get(ctx, adminKey).Result()
	rps, _ := client.Get(ctx, fmt.Sprintf("rps:%s", slug)).Result()

	fmt.Printf("\n%s📊 Status do projeto '%s':%s\n", bold, slug, reset)
	fmt.Println()
	fmt.Printf("  %s🔴 PANIC MODE: ATIVO%s\n", red, reset)
	fmt.Printf("     %sAdmin whitelisted:%s %s\n", yellow, reset, admin)
	fmt.Printf("     %sRequests/segundo:%s %s\n", blue, reset, rps)
	fmt.Println()

	// Confirmação
	if !force {
		fmt.Printf("%s⚠️  ATENÇÃO: Isso irá desativar o panic mode!%s\n", bold+red, reset)
		fmt.Printf("%s    O projeto voltará a aceitar requests normalmente.%s\n", yellow, reset)
		fmt.Println()
		fmt.Print("Tem certeza que deseja continuar? (digite 'DESATIVAR' para confirmar): ")
		
		var confirm string
		fmt.Scanln(&confirm)
		
		if confirm != "DESATIVAR" {
			fmt.Printf("%s✗ Operação cancelada.%s\n", cyan, reset)
			os.Exit(0)
		}
	}

	// Executar o reset
	fmt.Printf("\n%s⏳ Desativando panic mode para '%s'...%s\n", cyan, slug, reset)

	pipe := client.Pipeline()
	pipe.Del(ctx, panicKey)
	pipe.Del(ctx, adminKey)
	_, err = pipe.Exec(ctx)

	if err != nil {
		fmt.Printf("%s✗ Erro ao desativar panic mode: %v%s\n", red, err, reset)
		os.Exit(1)
	}

	fmt.Println()
	fmt.Printf("%s%s✅ Panic mode desativado com sucesso!%s\n", bold, green, reset)
	fmt.Printf("   %sProjeto:%s %s\n", bold, reset, slug)
	fmt.Println()
	fmt.Printf("%sℹ️  O projeto agora aceita requests normalmente.%s\n", cyan, reset)
	fmt.Printf("%s   O admin pode fazer login e reativar o panic mode via dashboard%s\n", cyan, reset)
	fmt.Printf("%s   se necessário (Security → Panic Mode).%s\n", cyan, reset)

	// Registrar audit trail
	logAuditTrail("PANIC_RESET", slug, admin)
}

// logAuditTrail registra eventos de segurança em arquivo para compliance
func logAuditTrail(event string, projectSlug string, previousAdmin string) {
	// Determinar diretório de logs
	logDir := "/var/log/cascata"
	logFile := filepath.Join(logDir, "security.log")

	// Se não tiver permissão em /var/log, usar diretório local
	if _, err := os.Stat(logDir); os.IsNotExist(err) {
		logDir = filepath.Join(os.Getenv("HOME"), ".cascata", "logs")
		logFile = filepath.Join(logDir, "security.log")
	}

	// Criar diretório se não existir
	if err := os.MkdirAll(logDir, 0750); err != nil {
		// Silenciosamente falha - não bloqueia a operação principal
		return
	}

	// Coletar informações do contexto
	timestamp := time.Now().UTC().Format(time.RFC3339)
	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "unknown"
	}

	// Identificar usuário atual
	currentUser := os.Getenv("SUDO_USER")
	if currentUser == "" {
		currentUser = os.Getenv("USER")
	}
	if currentUser == "" {
		currentUser = fmt.Sprintf("uid-%d", os.Getuid())
	}

	// Identificar IP de origem (se disponível via SSH)
	sshConn := os.Getenv("SSH_CONNECTION")
	sourceIP := "local"
	if sshConn != "" {
		parts := strings.Fields(sshConn)
		if len(parts) >= 1 {
			sourceIP = parts[0]
		}
	}

	// Montar entrada de audit
	auditEntry := fmt.Sprintf("[%s] EVENT=%s PROJECT=%s USER=%s IP=%s HOST=%s PREV_ADMIN=%s PID=%d\n",
		timestamp, event, projectSlug, currentUser, sourceIP, hostname, previousAdmin, os.Getpid())

	// Abrir arquivo em modo append (criar se não existir)
	// Permissões 0600 = apenas owner pode ler/escrever
	f, err := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		// Tentar sem as permissões estritas se falhar
		f, err = os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return // Silenciosamente falha
		}
	}
	defer f.Close()

	// Escrever entrada
	f.WriteString(auditEntry)

	// Também imprimir no stderr para captura por ferramentas externas
	fmt.Fprintf(os.Stderr, "%s[AUDIT] %s%s\n", dim, strings.TrimSpace(auditEntry), reset)
}

func handlePanicStatus(args []string) {
	if len(args) < 1 {
		fmt.Printf("%s✗ Erro: slug do projeto não fornecido%s\n", red, reset)
		fmt.Println("Uso: cascata-cli panic-status <slug>")
		os.Exit(1)
	}

	slug := args[0]
	client := getDragonflyClient()
	ctx := context.Background()

	if err := client.Ping(ctx).Err(); err != nil {
		fmt.Printf("%s✗ Erro: não foi possível conectar ao Dragonfly: %v%s\n", red, err, reset)
		os.Exit(1)
	}

	panicKey := fmt.Sprintf("panic:%s", slug)
	adminKey := fmt.Sprintf("panic:admin:%s", slug)

	val, err := client.Get(ctx, panicKey).Result()
	admin, _ := client.Get(ctx, adminKey).Result()
	rps, _ := client.Get(ctx, fmt.Sprintf("rps:%s", slug)).Result()

	fmt.Printf("\n%s📊 Status do projeto '%s':%s\n", bold, slug, reset)
	fmt.Println()

	if err == redis.Nil || val != "true" {
		fmt.Printf("  %s🟢 PANIC MODE: INATIVO%s\n", green, reset)
		fmt.Printf("     %sRequests/segundo:%s %s\n", blue, reset, rps)
		fmt.Println()
		fmt.Printf("  %s➜ O projeto está operando normalmente.%s\n", green, reset)
	} else {
		fmt.Printf("  %s🔴 PANIC MODE: ATIVO%s\n", red, reset)
		fmt.Printf("     %sAdmin whitelisted:%s %s\n", yellow, reset, admin)
		fmt.Printf("     %sRequests/segundo:%s %s\n", blue, reset, rps)
		fmt.Println()
		fmt.Printf("  %s➜ O projeto está bloqueando requests.%s\n", cyan, reset)
	}
}

func handlePanicList() {
	client := getDragonflyClient()
	ctx := context.Background()

	if err := client.Ping(ctx).Err(); err != nil {
		fmt.Printf("%s✗ Erro: não foi possível conectar ao Dragonfly: %v%s\n", red, err, reset)
		os.Exit(1)
	}

	fmt.Printf("\n%s🔒 Projetos em PANIC MODE:%s\n", bold, reset)
	fmt.Println()

	// Buscar todas as chaves panic:*
	keys, err := client.Keys(ctx, "panic:*").Result()
	if err != nil {
		fmt.Printf("%s✗ Erro ao buscar projetos: %v%s\n", red, err, reset)
		os.Exit(1)
	}

	// Filtrar apenas as chaves principais (não admin, não user)
	var panicKeys []string
	for _, key := range keys {
		if !strings.Contains(key, "panic:admin:") && !strings.Contains(key, "panic:user:") {
			panicKeys = append(panicKeys, key)
		}
	}

	if len(panicKeys) == 0 {
		fmt.Printf("%s   Nenhum projeto está em panic mode.%s\n", green, reset)
		fmt.Println()
		return
	}

	for _, key := range panicKeys {
		slug := strings.TrimPrefix(key, "panic:")
		
		val, _ := client.Get(ctx, key).Result()
		if val != "true" {
			continue
		}

		admin, _ := client.Get(ctx, fmt.Sprintf("panic:admin:%s", slug)).Result()
		rps, _ := client.Get(ctx, fmt.Sprintf("rps:%s", slug)).Result()

		fmt.Printf("%s  • %s%s\n", red, slug, reset)
		fmt.Printf("    %sAdmin:%s %s\n", yellow, reset, admin)
		fmt.Printf("    %sRPS:%s %s\n", blue, reset, rps)
		fmt.Println()
	}

	fmt.Printf("%sUse: cascata-cli panic-reset <slug> para desativar%s\n", cyan, reset)
	fmt.Println()
}
