package types

import (
	"context"
	"mime/multipart"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
)

// CascataUserRole defines the valid roles in the system
type CascataUserRole string

const (
	RoleService       CascataUserRole = "service_role"
	RoleAuthenticated CascataUserRole = "authenticated"
	RoleAnon          CascataUserRole = "anon"
)

// --- Context Key (Go idiomatic pattern) ---
type contextKey struct{ name string }

var CascataCtxKey = &contextKey{"cascata"}

// --- Metadata Structs (Go typed, no map[string]interface{} dot-access) ---

type ProjectSecurityConfig struct {
	MaxJsonSize string `json:"max_json_size"`
}

type McpPerimeter struct {
	AllowedIPs  []string `json:"allowed_ips"`
	AllowedURLs []string `json:"allowed_urls"`
}

type AiGovernanceConfig struct {
	McpEnabled   bool         `json:"mcp_enabled"`
	McpPerimeter McpPerimeter `json:"mcp_perimeter"`
}

type DbConfig struct {
	MaxConnections int `json:"max_connections"`
}

// ComputedColumnDef defines a computed column with formula, return type, and strict mode
// ReturnType: "text", "int", "float", "numeric", "money", "boolean", "timestamp"
// StrictMode: if true, formula errors fail the entire operation; if false, errors result in NULL
type ComputedColumnDef struct {
	Formula    string `json:"formula"`
	ReturnType string `json:"return_type,omitempty"` // PostgreSQL type for the computed result
	StrictMode bool   `json:"strict_mode,omitempty"` // If true, errors fail the operation; if false, errors = NULL
}

// AutoClockColumnDef defines an auto-clock column that gets NOW() on every update
type AutoClockColumnDef struct {
	Type      string `json:"type"`       // PostgreSQL data type (timestamp, timestamptz, date, etc.)
	CreatedAt string `json:"created_at,omitempty"` // ISO timestamp when the auto-clock was configured
}

// TableSecurityDef defines step-up requirements for whole-table CRUD operations.
// Operations accepts canonical values ("read", "create", "update", "delete")
// and aliases such as "write", "crud", and "all".
type TableSecurityDef struct {
	Operations     []string `json:"operations"`
	AllowedFactors []string `json:"allowed_factors"`
}

// LogExportProvider represents supported external log providers
type LogExportProvider string

const (
	ProviderDatadog LogExportProvider = "datadog"
	ProviderSplunk  LogExportProvider = "splunk"
	ProviderLoki    LogExportProvider = "loki"
	ProviderELK     LogExportProvider = "elk"
	ProviderS3      LogExportProvider = "s3"
	ProviderOTLP    LogExportProvider = "otlp"
)

// LogExportMode represents the export mode
type LogExportMode string

const (
	LogExportModeSidecar LogExportMode = "sidecar"
	LogExportModeNative  LogExportMode = "native"
)

// LogExportExporterConfig holds configuration for a single exporter
type LogExportExporterConfig struct {
	ID          string            `json:"id"`
	Provider    LogExportProvider `json:"provider"`
	Name        string            `json:"name"`
	Enabled     bool              `json:"enabled"`
	Endpoint    string            `json:"endpoint,omitempty"`
	APIKey      string            `json:"api_key,omitempty"`
	Token       string            `json:"token,omitempty"`
	Index       string            `json:"index,omitempty"`
	Source      string            `json:"source,omitempty"`
	ServiceName string            `json:"service_name,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
	BatchSize   int               `json:"batch_size,omitempty"`
	TimeoutSec  int               `json:"timeout_sec,omitempty"`
	// S3 specific
	S3Bucket    string `json:"s3_bucket,omitempty"`
	S3Region    string `json:"s3_region,omitempty"`
	S3AccessKey string `json:"s3_access_key,omitempty"`
	S3SecretKey string `json:"s3_secret_key,omitempty"`
}

// LogExportConfig holds the complete log export configuration for a project
type LogExportConfig struct {
	Enabled     bool                      `json:"enabled"`
	Mode        LogExportMode             `json:"mode"`
	APIKey      string                    `json:"api_key"`
	Exporters   []LogExportExporterConfig `json:"exporters"`
	FallbackToFile bool                   `json:"fallback_to_file"`
	DeadLetterPath string                `json:"dead_letter_path,omitempty"`
}

type ProjectMetadata struct {
	Timezone        string                 `json:"timezone"`
	AllowedOrigins  []string               `json:"allowed_origins"`
	DraftSyncActive bool                   `json:"draft_sync_active"`
	SchemaExposure  bool                   `json:"schema_exposure"` // Enable schema discovery for Supabase/FlutterFlow SDK compatibility
	Security        ProjectSecurityConfig  `json:"security"`
	AiGovernance    AiGovernanceConfig     `json:"ai_governance"`
	DbConfig        DbConfig               `json:"db_config"`
	MaskedColumns   map[string]map[string]string `json:"masked_columns"`
	LockedColumns   map[string]map[string]interface{} `json:"locked_columns"`
	ComputedColumns map[string]map[string]ComputedColumnDef `json:"computed_columns"` // table -> column -> {formula, returnType}
	AutoClockColumns map[string]map[string]AutoClockColumnDef `json:"auto_clock_columns"` // table -> column -> {type}
	TableSecurity  map[string]TableSecurityDef `json:"table_security"` // table -> whole-table step-up policy
	Secrets         map[string]interface{} `json:"secrets"`
	LogExport       LogExportConfig        `json:"log_export"`
	AppClients      []AppClient            `json:"app_clients"`
	ExternalDbUrl   string                 `json:"external_db_url,omitempty"`   // BYOD: External PostgreSQL connection string
	ReadReplicaUrl  string                 `json:"read_replica_url,omitempty"`  // BYOD: Read replica connection string
	Extra           map[string]interface{} `json:"-"` // Catch-all for untyped fields
}

// Project represents a tenant in the system
type Project struct {
	ID               string          `json:"id"`
	Name             string          `json:"name"`
	Slug             string          `json:"slug"`
	DbName           string          `json:"db_name"`
	CustomDomain     string          `json:"custom_domain"`
	Status           string          `json:"status"`
	JWTSecret        string          `json:"jwt_secret"`
	AnonKey          string          `json:"anon_key"`
	ServiceKey       string          `json:"service_key"`
	Blocklist        []string        `json:"blocklist"`
	LogRetentionDays int             `json:"log_retention_days"`
	ArchiveLogs      bool            `json:"archive_logs"`
	Metadata         ProjectMetadata `json:"metadata"`
	
	// Runtime cache for O(1) App Client lookups (not persisted)
	AppClientIndex map[string]*AppClient `json:"-"`
}

// ResolutionMethod defines how the project was resolved
type ResolutionMethod string

const (
	ResolutionByDomain ResolutionMethod = "domain"  // Via custom_domain
	ResolutionBySlug   ResolutionMethod = "slug"    // Via URL path
	ResolutionByAPIKey ResolutionMethod = "apikey"  // Via apikey header
	ResolutionSystem   ResolutionMethod = "system"  // System/God mode
)

// AppClient represents an app-specific client configuration (Identity-Aware Key Bridging)
type AppClient struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Nonce          string   `json:"nonce"`
	SiteURL        string   `json:"site_url"`
	AllowedOrigins []string `json:"allowed_origins"`
	AllowedTables  []string `json:"allowed_tables"` // Table-level access control (empty = all tables)
	BlockedTables  []string `json:"blocked_tables"` // Tables explicitly blocked for this app
	Active         bool     `json:"active"`
}

// CascataRequest complements the standard http.Request with Cascata-specific context
type CascataRequest struct {
	// Project Context (Resolved by Middleware)
	Project          *Project
	ProjectPool      *pgxpool.Pool    // Concrete Pool for this project context
	TargetEnv        string           // live or draft
	ResolutionMethod ResolutionMethod // How the project was resolved
	AccessPlane      string           // "data", "auth", "control", "system"

	// App Client Context (Identity-Aware Key Bridging)
	AppClient       *AppClient         // Resolved app client for multi-app routing

	// Authentication Context
	User            map[string]interface{} // Decoded JWT claims (MapClaims)
	UserRole        CascataUserRole
	IsSystemRequest bool
	IsDashboardAuth bool // Authenticated via dashboard/system JWT
	StepUpProviders string // Comma-separated list of verified step-up factors (e.g. "totp,biometria")

	// File Upload Context (Handled by Multipart Middleware)
	File  *multipart.FileHeader
	Files []*multipart.FileHeader

	// Standard Request Metadata
	Params  map[string]string
	Query   map[string][]string
	Headers http.Header
	Method  string
	Path    string
	URL     string
	IP      string
}

// NewCascataRequest builds a CascataRequest from an http.Request
func NewCascataRequest(r *http.Request) *CascataRequest {
	return &CascataRequest{
		Method:  r.Method,
		Path:    r.URL.Path,
		URL:     r.URL.String(),
		Headers: r.Header,
		Query:   r.URL.Query(),
		IP:      r.RemoteAddr,
	}
}

// GetUserID extracts the user ID from the context
// Returns the user ID and true if found, empty string and false otherwise
func GetUserID(ctx context.Context) (string, bool) {
	val := ctx.Value(CascataCtxKey)
	if val == nil {
		return "", false
	}
	
	cascataReq, ok := val.(*CascataRequest)
	if !ok || cascataReq == nil {
		return "", false
	}
	
	// Try to get user ID from User map (JWT claims)
	if cascataReq.User != nil {
		if userID, ok := cascataReq.User["sub"].(string); ok {
			return userID, true
		}
		if userID, ok := cascataReq.User["user_id"].(string); ok {
			return userID, true
		}
		if userID, ok := cascataReq.User["id"].(string); ok {
			return userID, true
		}
	}
	
	return "", false
}

// GetBranchName extracts the active branch name from context.
// Returns "main" if no branch context is active or if TargetEnv is "live".
func GetBranchName(ctx context.Context) string {
	val := ctx.Value(CascataCtxKey)
	if val == nil {
		return "main"
	}
	cascataReq, ok := val.(*CascataRequest)
	if !ok || cascataReq == nil {
		return "main"
	}
	if cascataReq.TargetEnv == "live" || cascataReq.TargetEnv == "" {
		return "main"
	}
	return cascataReq.TargetEnv
}
