package environment

import (
	"time"
)

// Branch representa uma branch no sistema Cascata
type Branch struct {
	ID              string    `json:"id"`
	ProjectSlug     string    `json:"project_slug"`
	Name            string    `json:"name"`            // ex: "main", "feat/novo-checkout"
	BranchType      BranchType `json:"branch_type"`     // "environment" ou "data"
	Status          BranchStatus `json:"status"`
	ParentBranch    *string   `json:"parent_branch,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
	CreatedBy       *string   `json:"created_by,omitempty"`
	IsMain          bool      `json:"is_main"`
	DataBranchDBName *string  `json:"data_branch_db_name,omitempty"`
	DataBranchTTLHours *int    `json:"data_branch_ttl_hours,omitempty"`
	ExpiresAt       *time.Time `json:"expires_at,omitempty"`
	DataMode        *string    `json:"data_mode,omitempty"` // copy, reflective, schema_only
	
	// Materialização on-demand (Thin Clone)
	// materialized_db é NULL quando a branch existe apenas como metadados
	// e é preenchido com o nome do banco quando o usuário faz "Access"
	MaterializedDB  *string    `json:"materialized_db,omitempty"`
	LastAccessedAt  *time.Time `json:"last_accessed_at,omitempty"`
	MaterializationTTLHours *int `json:"materialization_ttl_hours,omitempty"`
	
	// Conteúdo da branch (para branches de ambiente)
	Migrations      []Migration `json:"migrations,omitempty"`
	FunctionsSQL    *string     `json:"functions_sql,omitempty"`
	TriggersSQL     *string     `json:"triggers_sql,omitempty"`
	RLSSQL          *string     `json:"rls_sql,omitempty"`
	AutomationsJSON *string     `json:"automations_json,omitempty"`
	AuthConfigJSON  *string     `json:"auth_config_json,omitempty"`
	
	Checksum        string     `json:"checksum"`
}

// BranchType define o tipo de branch
type BranchType string

const (
	BranchTypeEnvironment BranchType = "environment" // Branch de ambiente (schema, estrutura)
	BranchTypeData       BranchType = "data"       // Branch de dados (banco completo)
)

// DataMode define o modo de operação para branches de dados
type DataMode string

const (
	DataModeCopy       DataMode = "copy"        // Clone 100% via CREATE DATABASE TEMPLATE (default)
	DataModeReflective DataMode = "reflective"  // Foreign tables via postgres_fdw — leitura do Live, escrita local
	DataModeSchemaOnly DataMode = "schema_only" // Banco vazio com apenas o schema DDL aplicado
)

// BranchStatus define o status de uma branch
type BranchStatus string

const (
	BranchStatusActive   BranchStatus = "active"   // Branch ativa e pronta para uso
	BranchStatusMerged   BranchStatus = "merged"   // Branch foi mergeada em main
	BranchStatusDeleted  BranchStatus = "deleted"  // Branch foi deletada
	BranchStatusExpired  BranchStatus = "expired"  // Branch expirou (para branches de dados)
)

// Migration representa uma migration SQL versionada
type Migration struct {
	Version string `json:"version"` // ex: "001", "002"
	SQL     string `json:"sql"`     // SQL da migration
}

// DataBranch representa uma branch de dados (banco temporário)
type DataBranch struct {
	ID              string    `json:"id"`
	ProjectSlug     string    `json:"project_slug"`
	BranchName      string    `json:"branch_name"`
	DBName          string    `json:"db_name"`          // Nome do banco PostgreSQL
	TemplateDB      string    `json:"template_db"`      // Banco usado como template
	Status          BranchStatus `json:"status"`
	CreatedAt       time.Time `json:"created_at"`
	ExpiresAt       time.Time `json:"expires_at"`
	CreatedBy       *string   `json:"created_by,omitempty"`
	SizeBytes       int64     `json:"size_bytes"`
	RowCountEstimate int      `json:"row_count_estimate"`
}

// BranchDeploy representa uma operação de deploy de branch
type BranchDeploy struct {
	ID              string    `json:"id"`
	BranchID        string    `json:"branch_id"`
	SourceBranch    string    `json:"source_branch"`
	TargetBranch    string    `json:"target_branch"`
	Status          DeployStatus `json:"status"`
	DiffResult      *string   `json:"diff_result,omitempty"` // JSON do diff result
	SQLStatements   []string  `json:"sql_statements,omitempty"`
	StartedAt       time.Time `json:"started_at"`
	CompletedAt     *time.Time `json:"completed_at,omitempty"`
	DurationMs      *int      `json:"duration_ms,omitempty"`
	TriggeredBy     *string   `json:"triggered_by,omitempty"`
	ErrorMessage    *string   `json:"error_message,omitempty"`
	SnapshotName    *string   `json:"snapshot_name,omitempty"`
}

// DeployStatus define o status de um deploy
type DeployStatus string

const (
	DeployStatusPending    DeployStatus = "pending"    // Aguardando execução
	DeployStatusRunning    DeployStatus = "running"    // Em execução
	DeployStatusSuccess    DeployStatus = "success"    // Sucesso
	DeployStatusFailed     DeployStatus = "failed"     // Falha
	DeployStatusRolledBack DeployStatus = "rolled_back" // Rollback executado
)

// CreateBranchRequest é o request para criar uma nova branch
type CreateBranchRequest struct {
	Name            string     `json:"name"`
	BranchType      BranchType `json:"branch_type"`
	ParentBranch    *string    `json:"parent_branch,omitempty"`
	DataBranchTTL   *int       `json:"data_branch_ttl_hours,omitempty"` // Para branches de dados
	SourceSnapshot  *string    `json:"source_snapshot,omitempty"`       // Snapshot físico de origem se aberto de checkpoint
	DataMode        *DataMode  `json:"data_mode,omitempty"`             // Modo: copy (default), reflective, schema_only
	
	// Conteúdo inicial (para branches de ambiente)
	Migrations      []Migration `json:"migrations,omitempty"`
	FunctionsSQL    *string     `json:"functions_sql,omitempty"`
	TriggersSQL     *string     `json:"triggers_sql,omitempty"`
	RLSSQL          *string     `json:"rls_sql,omitempty"`
	AutomationsJSON *string     `json:"automations_json,omitempty"`
	AuthConfigJSON  *string     `json:"auth_config_json,omitempty"`
}

// UpdateBranchRequest é o request para atualizar uma branch
type UpdateBranchRequest struct {
	Migrations      []Migration `json:"migrations,omitempty"`
	FunctionsSQL    *string     `json:"functions_sql,omitempty"`
	TriggersSQL     *string     `json:"triggers_sql,omitempty"`
	RLSSQL          *string     `json:"rls_sql,omitempty"`
	AutomationsJSON *string     `json:"automations_json,omitempty"`
	AuthConfigJSON  *string     `json:"auth_config_json,omitempty"`
}

// DeployBranchRequest é o request para fazer deploy de uma branch
type DeployBranchRequest struct {
	SourceBranch    string `json:"source_branch"`
	TargetBranch    string `json:"target_branch"`
	DryRun          bool   `json:"dry_run"`
	SafetySnapshot  bool   `json:"safety_snapshot"`
}

// ListBranchesResponse é a resposta de listagem de branches
type ListBranchesResponse struct {
	Branches []Branch `json:"branches"`
	Total    int      `json:"total"`
}

// GetBranchResponse é a resposta de obtenção de uma branch
type GetBranchResponse struct {
	Branch Branch `json:"branch"`
}

// CreateBranchResponse é a resposta de criação de branch
type CreateBranchResponse struct {
	Branch Branch `json:"branch"`
}

// UpdateBranchResponse é a resposta de atualização de branch
type UpdateBranchResponse struct {
	Branch Branch `json:"branch"`
}

// DeleteBranchResponse é a resposta de deleção de branch
type DeleteBranchResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// DeployBranchResponse é a resposta de deploy de branch
type DeployBranchResponse struct {
	DeployID string `json:"deploy_id"`
	Status   DeployStatus `json:"status"`
	Message  string `json:"message"`
	DiffResult *string `json:"diff_result,omitempty"`
}

// AccessBranchRequest é o request para acessar (materializar) uma branch
type AccessBranchRequest struct {
	BranchName string `json:"branch_name"`
}

// AccessBranchResponse é a resposta do Access — contém o env identifier
// que o frontend deve usar no header X-Cascata-Env para todas as requests subsequentes
type AccessBranchResponse struct {
	Success        bool       `json:"success"`
	Message        string     `json:"message"`
	Branch         Branch     `json:"branch"`
	DatabaseName   string     `json:"database_name"`       // Nome do banco real (thin/fat clone)
	EnvIdentifier  string     `json:"env_identifier"`      // Valor para o header X-Cascata-Env
	Materialized   bool       `json:"materialized"`        // true se o banco foi criado agora
	ExpiresIn      string     `json:"expires_in,omitempty"` // TTL restante legível (ex: "23h45m")
}