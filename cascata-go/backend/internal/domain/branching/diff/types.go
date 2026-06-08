package diff

import (
	"time"
)

// DiffResult contém o resultado completo de uma operação de diff
// entre dois ambientes (branch vs main ou draft vs live)
type DiffResult struct {
	SQL       []string       // SQL statements para aplicar as mudanças
	Summaries []PhaseSummary // Resumo de cada fase do diff
	Success   bool           // Se o diff foi gerado com sucesso
	Error     string         // Erro se houver falha
}

// PhaseSummary resume o resultado de uma fase específica do diff
type PhaseSummary struct {
	PhaseName string        // Nome da fase (ex: "tables", "columns")
	Changes   int           // Número de mudanças detectadas
	SQL       []string      // SQL gerado por esta fase
	Duration  time.Duration // Tempo de execução da fase
	Details   []string      // Detalhes específicos das mudanças
}

// DiffPhase define a interface que todas as fases do diff devem implementar
// Cada fase é independente, testável, e não conhece as outras
type DiffPhase interface {
	// Name retorna o identificador único da fase
	Name() string

	// Introspect analisa o estado atual e gera o diff interno
	// Este método deve coletar todas as informações necessárias
	// do banco de dados para gerar o SQL posteriormente
	Introspect(ctx DiffContext) error

	// GenerateSQL converte o diff interno em statements SQL executáveis
	// Este método deve ser idempotente - chamadas múltiplas retornam o mesmo resultado
	GenerateSQL() []string

	// Summary retorna um resumo das mudanças desta fase
	Summary() PhaseSummary
}

// DiffContext fornece o contexto necessário para as fases do diff
type DiffContext struct {
	// PoolProvider oferece acesso aos pools de conexão
	// Isso permite que o diff engine seja testado com mocks
	PoolProvider PoolProvider

	// ProjectSlug identifica o projeto sendo diff'ado
	ProjectSlug string

	// SourceBranch é a branch de origem (ex: "feat/novo-checkout")
	SourceBranch string

	// TargetBranch é a branch de destino (ex: "main")
	TargetBranch string

	// Mode define o tipo de diff sendo executado
	Mode DiffMode

	// GAP #4 FIX: Conexões pré-adquiridas para reutilização em todas as fases
	// Evita N+1 acquire/release cycles que estrangulam o PgBouncer
	SourceConn PoolConn
	TargetConn PoolConn
}

// DiffMode define o tipo de operação de diff
type DiffMode string

const (
	// ModeBranchToMain compara uma branch de ambiente com main
	ModeBranchToMain DiffMode = "branch_to_main"

	// ModeDraftToLive compara draft com live (legado, será removido)
	ModeDraftToLive DiffMode = "draft_to_live"

	// ModeLiveToDraft compara live com draft (legado, será removido)
	ModeLiveToDraft DiffMode = "live_to_draft"
)

// PoolProvider é a interface mínima que o diff engine precisa
// para acessar pools de conexão. Isso permite testes com mocks
// e desacoplamento do pool.go existente
type PoolProvider interface {
	// AcquireForProject retorna uma conexão para o pool do projeto especificado
	AcquireForProject(projectSlug string, env string) (PoolConn, error)

	// AcquireEphemeral cria um pool temporário para a connection string fornecida
	// Útil para dry runs e branches de dados
	AcquireEphemeral(connString string) (PoolConn, error)
}

// PoolConn representa uma conexão de banco de dados
// Esta interface abstrai pgxpool.Conn para permitir testes
type PoolConn interface {
	// Exec executa uma query SQL
	Exec(query string, args ...interface{}) (ExecResult, error)

	// Query executa uma query que retorna linhas
	Query(query string, args ...interface{}) (Rows, error)

	// QueryRow executa uma query que espera exatamente uma linha
	QueryRow(query string, args ...interface{}) Row

	// Begin inicia uma transação
	Begin() (Tx, error)

	// Close fecha a conexão
	Close() error
}

// ExecResult representa o resultado de uma execução SQL
type ExecResult interface {
	RowsAffected() int64
}

// Rows representa um conjunto de linhas de uma query
type Rows interface {
	Next() bool
	Scan(dest ...interface{}) error
	Close() error
	Err() error
}

// Row representa uma única linha de uma query
type Row interface {
	Scan(dest ...interface{}) error
}

// Tx representa uma transação de banco de dados
type Tx interface {
	Exec(query string, args ...interface{}) (ExecResult, error)
	Query(query string, args ...interface{}) (Rows, error)
	QueryRow(query string, args ...interface{}) Row
	Commit() error
	Rollback() error
}

// TableInfo contém metadados sobre uma tabela
type TableInfo struct {
	Schema    string
	TableName string
	Columns   []ColumnInfo
	Indexes   []IndexInfo
	Triggers  []TriggerInfo
}

// ColumnInfo contém metadados sobre uma coluna
type ColumnInfo struct {
	Name         string
	Type         string
	Nullable     bool
	DefaultValue string
	IsPrimaryKey bool
	IsUnique     bool
}

// IndexInfo contém metadados sobre um índice
type IndexInfo struct {
	IndexName string
	TableName string
	IsUnique  bool
	IsPrimary bool
	IndexDef  string
}

// TriggerInfo contém metadados sobre um trigger
type TriggerInfo struct {
	TriggerName string
	TableName   string
	TriggerDef  string
	Timing      string // BEFORE, AFTER, INSTEAD OF
	Events      []string // INSERT, UPDATE, DELETE, TRUNCATE
}

// FunctionInfo contém metadados sobre uma função/RPC
type FunctionInfo struct {
	FunctionName string
	Schema       string
	ArgumentTypes []string
	ReturnType   string
	FunctionBody string
	Language     string
}

// PolicyInfo contém metadados sobre uma RLS policy
type PolicyInfo struct {
	PolicyName string
	TableName  string
	PolicyCmd  string // ALL, SELECT, INSERT, UPDATE, DELETE
	UsingExpr  string
	WithCheckExpr string
	Roles       []string
}