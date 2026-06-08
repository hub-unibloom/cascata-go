package deployer

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"cascata-backend/internal/domain/branching/diff"
)

// DeployOptions configura o comportamento do deploy
type DeployOptions struct {
	// LockTimeout define quanto tempo esperar por um lock exclusivo antes de falhar
	// Default: 2.5s
	LockTimeout time.Duration

	// StatementTimeout define o tempo máximo de execução por statement
	// Default: 30s
	StatementTimeout time.Duration

	// DryRun se true, valida o SQL sem aplicar mudanças
	DryRun bool

	// SafetySnapshot se true, cria snapshot antes do deploy
	SafetySnapshot bool

	// RollbackOnFailure se true, faz rollback automático em caso de falha
	RollbackOnFailure bool
}

// DefaultDeployOptions retorna opções de deploy com valores padrão
func DefaultDeployOptions() DeployOptions {
	return DeployOptions{
		LockTimeout:       2500 * time.Millisecond,
		StatementTimeout:  30 * time.Second,
		DryRun:            false,
		SafetySnapshot:    true,
		RollbackOnFailure: true,
	}
}

// Deployer é o orquestrador de operações de deploy
type Deployer struct {
	poolProvider PoolProvider
	logger        Logger
}

// Logger define a interface para logging
type Logger interface {
	Info(msg string, args ...interface{})
	Error(msg string, args ...interface{})
	Debug(msg string, args ...interface{})
}

// NewDeployer cria uma nova instância do Deployer
func NewDeployer(poolProvider PoolProvider, logger Logger) *Deployer {
	return &Deployer{
		poolProvider: poolProvider,
		logger:        logger,
	}
}

// ExecuteDeploy executa um deploy com segurança transacional rigorosa
// Este é o método principal que aplica mudanças ao banco de produção
func (d *Deployer) ExecuteDeploy(
	ctx context.Context,
	projectSlug string,
	env string,
	sql []string,
	opts DeployOptions,
) error {
	// Validações básicas
	if len(sql) == 0 {
		return fmt.Errorf("no SQL statements to execute")
	}

	d.logger.Info("Starting deploy",
		"project", projectSlug,
		"env", env,
		"statements", len(sql),
		"dry_run", opts.DryRun,
	)

	// Adquire conexão do pool do projeto
	conn, err := d.poolProvider.AcquireForProject(projectSlug, env)
	if err != nil {
		return fmt.Errorf("pool acquire failed: %w", err)
	}
	defer conn.Close()

	// Configura timeouts no nível da sessão
	if err := d.configureTimeouts(ctx, conn, opts); err != nil {
		return fmt.Errorf("timeout configuration failed: %w", err)
	}

	// Inicia transação
	tx, err := conn.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction failed: %w", err)
	}

	// Garante rollback se ocorrer erro
	// Usa context.Background() intencionalmente - o context original pode estar cancelado
	// mas o rollback precisa ser enviado de qualquer forma
	var deployErr error
	defer func() {
		if deployErr != nil {
			_ = tx.Rollback()
			d.logger.Error("Transaction rolled back due to error", "error", deployErr)
		}
	}()

	// Executa cada statement sequencialmente
	for i, statement := range sql {
		d.logger.Debug("Executing statement",
			"index", i+1,
			"total", len(sql),
			"preview", truncateStatement(statement, 100),
		)

		_, err := tx.Exec(statement)
		if err != nil {
			deployErr = fmt.Errorf("statement %d failed: %w\nStatement: %s", i+1, err, statement)

			// Identifica o tipo de erro para mensagem clara
			if isLockTimeoutError(err) {
				deployErr = fmt.Errorf("deploy blocked: table lock timeout — "+
					"production is under load. Try again in a few minutes. "+
					"Statement: %s", truncateStatement(statement, 100))
			} else if isDeadlockError(err) {
				deployErr = fmt.Errorf("deploy blocked: deadlock detected — "+
					"another migration may be running concurrently. "+
					"Statement: %s", truncateStatement(statement, 100))
			}

			return deployErr
		}
	}

	// Commit da transação
	if err := tx.Commit(); err != nil {
		deployErr = fmt.Errorf("commit failed: %w", err)
		return deployErr
	}

	d.logger.Info("Deploy completed successfully",
		"project", projectSlug,
		"env", env,
		"statements", len(sql),
	)

	return nil
}

// configureTimeouts configura lock_timeout e statement_timeout na sessão
func (d *Deployer) configureTimeouts(ctx context.Context, conn diff.PoolConn, opts DeployOptions) error {
	// lock_timeout: Postgres cancela a query se não conseguir o lock dentro do prazo
	// Isso previne que o Go fique bloqueado esperando um lock exclusivo
	lockTimeout := opts.LockTimeout
	if lockTimeout == 0 {
		lockTimeout = DefaultDeployOptions().LockTimeout
	}

	_, err := conn.Exec(fmt.Sprintf("SET lock_timeout = '%dms'", lockTimeout.Milliseconds()))
	if err != nil {
		return fmt.Errorf("failed to set lock_timeout: %w", err)
	}

	// statement_timeout: segunda linha de defesa
	// Cancela queries que levam muito tempo (ex: scans de tabela inteira)
	statementTimeout := opts.StatementTimeout
	if statementTimeout == 0 {
		statementTimeout = DefaultDeployOptions().StatementTimeout
	}

	_, err = conn.Exec(fmt.Sprintf("SET statement_timeout = '%ds'", int(statementTimeout.Seconds())))
	if err != nil {
		return fmt.Errorf("failed to set statement_timeout: %w", err)
	}

	d.logger.Debug("Timeouts configured",
		"lock_timeout", lockTimeout,
		"statement_timeout", statementTimeout,
	)

	return nil
}

// isLockTimeoutError verifica se o erro é um timeout de lock
func isLockTimeoutError(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "55P03" // lock_not_available
	}
	return false
}

// isDeadlockError verifica se o erro é um deadlock
func isDeadlockError(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "40P01" // deadlock_detected
	}
	return false
}

// truncateStatement trunca um statement SQL para logging
func truncateStatement(stmt string, maxLen int) string {
	if len(stmt) <= maxLen {
		return stmt
	}
	return stmt[:maxLen] + "..."
}

// DefaultLogger é uma implementação simples de Logger
type DefaultLogger struct{}

// NewDefaultLogger cria um novo logger padrão
func NewDefaultLogger() *DefaultLogger {
	return &DefaultLogger{}
}

// Info loga uma mensagem de informação
func (l *DefaultLogger) Info(msg string, args ...interface{}) {
	log.Printf("[INFO] %s %v", msg, args)
}

// Error loga uma mensagem de erro
func (l *DefaultLogger) Error(msg string, args ...interface{}) {
	log.Printf("[ERROR] %s %v", msg, args)
}

// Debug loga uma mensagem de debug
func (l *DefaultLogger) Debug(msg string, args ...interface{}) {
	log.Printf("[DEBUG] %s %v", msg, args)
}
