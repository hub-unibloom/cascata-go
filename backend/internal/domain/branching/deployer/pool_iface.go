package deployer

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"cascata-backend/internal/domain/branching/diff"
	"cascata-backend/internal/services"
	"cascata-backend/internal/types"
)

// PoolProvider é a interface mínima que o deployer precisa
// para acessar pools de conexão. Isso permite testes com mocks
// e desacoplamento do pool.go existente
type PoolProvider interface {
	// AcquireForProject retorna uma conexão para o pool do projeto especificado
	// env pode ser "live" ou o nome de uma branch
	AcquireForProject(projectSlug string, env string) (diff.PoolConn, error)

	// AcquireEphemeral cria um pool temporário para a connection string fornecida
	// Útil para dry runs e branches de dados
	AcquireEphemeral(connString string) (diff.PoolConn, error)
}

// PoolAdapter adapta o pool.go existente para a interface PoolProvider
// Este é o ponte entre o código legado e o novo módulo de branching
type PoolAdapter struct {
	// Injeção de dependências para acesso ao services
	getProjectBySlug func(context.Context, string) *types.Project
	getProjectPool   func(*types.Project, string) (*pgxpool.Pool, error)
}

// NewPoolAdapter cria um novo adaptador para o pool existente
// Injeta as funções necessárias do services package
func NewPoolAdapter() *PoolAdapter {
	return &PoolAdapter{
		getProjectBySlug: services.GetProjectBySlug,
		getProjectPool:   services.GetProjectPool,
	}
}

// AcquireForProject implementa a interface PoolProvider
// Resolve slug → project → pool → diff.PoolConn
func (a *PoolAdapter) AcquireForProject(projectSlug string, env string) (diff.PoolConn, error) {
	ctx := context.Background()
	
	// 1. Resolve o slug para obter o projeto
	project := a.getProjectBySlug(ctx, projectSlug)
	if project == nil {
		return nil, fmt.Errorf("project not found for slug: %s", projectSlug)
	}

	// 2. Obtém o pool do projeto para o ambiente especificado
	// Para branches de ambiente, usamos o mesmo pool mas com schema diferente
	// Nota: env "live" usa o schema público/default, branches usam schemas isolados
	pool, err := a.getProjectPool(project, env)
	if err != nil {
		return nil, fmt.Errorf("failed to acquire pool for project %s, env %s: %w", projectSlug, env, err)
	}

	// 3. Adquire uma conexão do pool
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to acquire connection from pool: %w", err)
	}

	// 4. Retorna wrapper que gerencia o release da conexão
	return &ManagedConn{
		Conn:  conn,
		pool:  pool,
		env:   env,
		slug:  projectSlug,
	}, nil
}

// AcquireEphemeral implementa a interface PoolProvider
func (a *PoolAdapter) AcquireEphemeral(connString string) (diff.PoolConn, error) {
	// Cria um pool temporário com timeout curto
	config, err := pgxpool.ParseConfig(connString)
	if err != nil {
		return nil, err
	}

	// Configurações para pools efêmeros (menos conexões, timeout curto)
	config.MaxConns = 5
	config.MinConns = 0
	config.HealthCheckPeriod = 1 * time.Minute
	config.MaxConnLifetime = 10 * time.Minute

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, err
	}

	// Adquire uma conexão do pool temporário
	conn, err := pool.Acquire(ctx)
	if err != nil {
		pool.Close()
		return nil, err
	}

	// Retorna um wrapper que fecha o pool quando a conexão é fechada
	return &EphemeralConn{Conn: conn, pool: pool}, nil
}

// ManagedConn é um wrapper para conexões gerenciadas que libera a conexão de volta ao pool
type ManagedConn struct {
	Conn  *pgxpool.Conn
	pool  *pgxpool.Pool
	env   string
	slug  string
}

// Exec implementa diff.PoolConn
func (m *ManagedConn) Exec(query string, args ...interface{}) (diff.ExecResult, error) {
	return m.Conn.Exec(context.Background(), query, args...)
}

// Query implementa diff.PoolConn
func (m *ManagedConn) Query(query string, args ...interface{}) (diff.Rows, error) {
	rows, err := m.Conn.Query(context.Background(), query, args...)
	if err != nil {
		return nil, err
	}
	return &RowsWrapper{rows: rows}, nil
}

// QueryRow implementa diff.PoolConn
func (m *ManagedConn) QueryRow(query string, args ...interface{}) diff.Row {
	return m.Conn.QueryRow(context.Background(), query, args...)
}

// Begin implementa diff.PoolConn
func (m *ManagedConn) Begin() (diff.Tx, error) {
	tx, err := m.Conn.Begin(context.Background())
	if err != nil {
		return nil, err
	}
	return &TxWrapper{tx: tx}, nil
}

// Close implementa diff.PoolConn - apenas libera a conexão de volta ao pool
func (m *ManagedConn) Close() error {
	m.Conn.Release()
	return nil
}

// EphemeralConn é um wrapper para conexões efêmeras que fecha o pool quando a conexão é fechada
type EphemeralConn struct {
	Conn  *pgxpool.Conn
	pool  *pgxpool.Pool
}

// Exec implementa diff.PoolConn
func (e *EphemeralConn) Exec(query string, args ...interface{}) (diff.ExecResult, error) {
	return e.Conn.Exec(context.Background(), query, args...)
}

// Query implementa diff.PoolConn
func (e *EphemeralConn) Query(query string, args ...interface{}) (diff.Rows, error) {
	rows, err := e.Conn.Query(context.Background(), query, args...)
	if err != nil {
		return nil, err
	}
	return &RowsWrapper{rows: rows}, nil
}

// QueryRow implementa diff.PoolConn
func (e *EphemeralConn) QueryRow(query string, args ...interface{}) diff.Row {
	return e.Conn.QueryRow(context.Background(), query, args...)
}

// Begin implementa diff.PoolConn
func (e *EphemeralConn) Begin() (diff.Tx, error) {
	tx, err := e.Conn.Begin(context.Background())
	if err != nil {
		return nil, err
	}
	return &TxWrapper{tx: tx}, nil
}

// Close implementa diff.PoolConn
func (e *EphemeralConn) Close() error {
	defer e.pool.Close()
	e.Conn.Release()
	return nil
}

// RowsWrapper adapta pgx.Rows para diff.Rows
type RowsWrapper struct {
	rows pgx.Rows
}

func (r *RowsWrapper) Next() bool {
	return r.rows.Next()
}

func (r *RowsWrapper) Scan(dest ...interface{}) error {
	return r.rows.Scan(dest...)
}

func (r *RowsWrapper) Close() error {
	r.rows.Close()
	return nil
}

func (r *RowsWrapper) Err() error {
	return r.rows.Err()
}

// TxWrapper adapta pgx.Tx para diff.Tx
type TxWrapper struct {
	tx pgx.Tx
}

func (t *TxWrapper) Exec(query string, args ...interface{}) (diff.ExecResult, error) {
	return t.tx.Exec(context.Background(), query, args...)
}

func (t *TxWrapper) Query(query string, args ...interface{}) (diff.Rows, error) {
	rows, err := t.tx.Query(context.Background(), query, args...)
	if err != nil {
		return nil, err
	}
	return &RowsWrapper{rows: rows}, nil
}

func (t *TxWrapper) QueryRow(query string, args ...interface{}) diff.Row {
	return t.tx.QueryRow(context.Background(), query, args...)
}

func (t *TxWrapper) Commit() error {
	return t.tx.Commit(context.Background())
}

func (t *TxWrapper) Rollback() error {
	return t.tx.Rollback(context.Background())
}