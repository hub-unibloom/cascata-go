package environment

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"cascata-backend/internal/services"
	"cascata-backend/internal/domain/branching/deployer"
	"cascata-backend/internal/domain/branching/diff"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// BranchService gerencia operações de branches
type BranchService struct {
	pool *pgxpool.Pool
}

// NewBranchService cria uma nova instância do BranchService
func NewBranchService(pool *pgxpool.Pool) *BranchService {
	return &BranchService{
		pool: pool,
	}
}

// EnsureMainBranch garante que a branch "main" existe para um projeto
func (s *BranchService) EnsureMainBranch(ctx context.Context, projectSlug string) (*Branch, error) {
	var branchID string
	query := `
		INSERT INTO system.branches (
			project_slug,
			name,
			branch_type,
			status,
			is_main,
			parent_branch,
			checksum
		) VALUES (
			$1, 'main', 'environment', 'active', TRUE, NULL, 'main-initial'
		)
		ON CONFLICT (project_slug, name) DO UPDATE SET project_slug = EXCLUDED.project_slug
		RETURNING id
	`
	err := s.pool.QueryRow(ctx, query, projectSlug).Scan(&branchID)
	if err != nil {
		return nil, fmt.Errorf("failed to ensure main branch: %w", err)
	}

	return s.GetBranchByID(ctx, branchID)
}

// CreateBranch cria uma nova branch
func (s *BranchService) CreateBranch(ctx context.Context, projectSlug string, req CreateBranchRequest, createdBy *string) (*Branch, error) {
	// Validações
	if req.Name == "main" {
		return nil, fmt.Errorf("cannot create branch named 'main' - use EnsureMainBranch instead")
	}

	// Branching é inicializado apenas quando o owner cria a primeira branch explicitamente.
	if req.BranchType == BranchTypeEnvironment {
		if _, err := s.EnsureMainBranch(ctx, projectSlug); err != nil {
			return nil, fmt.Errorf("failed to initialize main branch: %w", err)
		}
	}

	if req.BranchType == BranchTypeData && req.ParentBranch == nil {
		return nil, fmt.Errorf("data branches require a parent branch")
	}

	// Freeze and store static snapshot of parent authentication state inside the new branch at creation time.
	// This prevents future changes in 'live/main' auth config from dynamically leaking into previously created branches.
	if req.AuthConfigJSON == nil || *req.AuthConfigJSON == "" {
		parentBranchName := "main"
		if req.ParentBranch != nil && *req.ParentBranch != "" {
			parentBranchName = *req.ParentBranch
		}

		var parentAuthJSON *string

		if parentBranchName == "main" {
			var metadataRaw json.RawMessage
			err := s.pool.QueryRow(ctx,
				"SELECT metadata FROM system.projects WHERE slug = $1",
				projectSlug,
			).Scan(&metadataRaw)
			if err == nil && len(metadataRaw) > 0 {
				var metadata map[string]interface{}
				if json.Unmarshal(metadataRaw, &metadata) == nil {
					if extra, ok := metadata["extra"].(map[string]interface{}); ok {
						branchAuth := map[string]interface{}{
							"auth_config":     extra["auth_config"],
							"auth_strategies": extra["auth_strategies"],
							"linked_tables":   extra["linked_tables"],
						}
						if authBytes, err := json.Marshal(branchAuth); err == nil {
							authStr := string(authBytes)
							parentAuthJSON = &authStr
						}
					}
				}
			}
		} else {
			var parentJSON *string
			err := s.pool.QueryRow(ctx,
				"SELECT auth_config_json FROM system.branches WHERE project_slug = $1 AND name = $2",
				projectSlug, parentBranchName,
			).Scan(&parentJSON)
			if err == nil && parentJSON != nil && *parentJSON != "" {
				parentAuthJSON = parentJSON
			}
		}

		if parentAuthJSON != nil && *parentAuthJSON != "" {
			req.AuthConfigJSON = parentAuthJSON
		}
	}

	// Calcula checksum do conteúdo (including frozen auth snapshot)
	checksum, err := s.calculateChecksum(req)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate checksum: %w", err)
	}

	// Serializa migrations para JSONB
	var migrationsJSONB []byte
	if len(req.Migrations) > 0 {
		migrationsJSONB, err = json.Marshal(req.Migrations)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal migrations: %w", err)
		}
	}

	// Determina expires_at para branches de dados
	var expiresAt *time.Time
	if req.BranchType == BranchTypeData {
		ttl := 168 // 7 dias padrão
		if req.DataBranchTTL != nil {
			ttl = *req.DataBranchTTL
		}
		exp := time.Now().Add(time.Duration(ttl) * time.Hour)
		expiresAt = &exp
	}

	// Para branches de dados, gera nome do banco
	var dataBranchDBName *string
	if req.BranchType == BranchTypeData {
		dbName := fmt.Sprintf("cascata_%s_%s_data", projectSlug, sanitizeBranchName(req.Name))
		dataBranchDBName = &dbName
	}

	// Se for branch de dados, cria o banco temporário ANTES do metadata (evita registros órfãos)
	if req.BranchType == BranchTypeData && dataBranchDBName != nil {
		mode := DataModeCopy // default
		if req.DataMode != nil {
			mode = *req.DataMode
		}

		switch mode {
		case DataModeSchemaOnly:
			if err := s.createSchemaOnlyBranch(ctx, *dataBranchDBName, projectSlug); err != nil {
				return nil, fmt.Errorf("failed to create schema-only branch: %w", err)
			}
		case DataModeReflective:
			if err := s.createReflectiveBranch(ctx, *dataBranchDBName, projectSlug); err != nil {
				return nil, fmt.Errorf("failed to create reflective branch: %w", err)
			}
		default: // DataModeCopy
			if err := s.createDataBranchDB(ctx, *dataBranchDBName, projectSlug, req.SourceSnapshot); err != nil {
				return nil, fmt.Errorf("failed to create data branch database: %w", err)
			}
		}
	}

	query := `
		INSERT INTO system.branches (
			project_slug,
			name,
			branch_type,
			status,
			parent_branch,
			created_by,
			is_main,
			data_branch_db_name,
			data_branch_ttl_hours,
			expires_at,
			migrations,
			functions_sql,
			triggers_sql,
			rls_sql,
			automations_json,
			auth_config_json,
			checksum,
			data_mode
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18
		)
		RETURNING id, project_slug, name, branch_type, status, parent_branch,
			created_at, updated_at, created_by, is_main, data_branch_db_name,
			data_branch_ttl_hours, expires_at, checksum, data_mode
	`

	row := s.pool.QueryRow(ctx, query,
		projectSlug,
		req.Name,
		req.BranchType,
		BranchStatusActive,
		req.ParentBranch,
		createdBy,
		false, // is_main
		dataBranchDBName,
		req.DataBranchTTL,
		expiresAt,
		migrationsJSONB,
		req.FunctionsSQL,
		req.TriggersSQL,
		req.RLSSQL,
		req.AutomationsJSON,
		req.AuthConfigJSON,
		checksum,
		req.DataMode, // $18: data_mode (nil defaults to 'copy' via DB DEFAULT)
	)

	var branch Branch
	err = row.Scan(
		&branch.ID,
		&branch.ProjectSlug,
		&branch.Name,
		&branch.BranchType,
		&branch.Status,
		&branch.ParentBranch,
		&branch.CreatedAt,
		&branch.UpdatedAt,
		&branch.CreatedBy,
		&branch.IsMain,
		&branch.DataBranchDBName,
		&branch.DataBranchTTLHours,
		&branch.ExpiresAt,
		&branch.Checksum,
		&branch.DataMode,
	)
	if err != nil {
		// Se o INSERT falhar mas o banco físico já foi criado, limpa o banco órfão
		if req.BranchType == BranchTypeData && dataBranchDBName != nil {
			_ = s.dropDataBranchDB(ctx, *dataBranchDBName)
		}
		return nil, fmt.Errorf("failed to create branch: %w", err)
	}

	// Se for branch de dados, registra na tabela de tracking
	if req.BranchType == BranchTypeData && dataBranchDBName != nil {
		if err := s.registerDataBranch(ctx, projectSlug, req.Name, *dataBranchDBName, createdBy); err != nil {
			_ = s.DeleteBranch(ctx, projectSlug, req.Name)
			return nil, fmt.Errorf("failed to register data branch: %w", err)
		}
	}

	// Copy all nexus automations (orchestrations) from the parent branch to the new branch
	parentBranchName := "main"
	if req.ParentBranch != nil && *req.ParentBranch != "" {
		parentBranchName = *req.ParentBranch
	}
	copyAutomationsQuery := `
		INSERT INTO system.nexus_automations (
			id, tenant_id, branch_name, name, description, hook_type, 
			table_name, event_type, graph_json, is_active, status, 
			execution_mode, route_pattern, method
		)
		SELECT 
			id, tenant_id, $1, name, description, hook_type, 
			table_name, event_type, graph_json, is_active, status, 
			execution_mode, route_pattern, method
		FROM system.nexus_automations
		WHERE tenant_id = $2 AND branch_name = $3
		ON CONFLICT (id, tenant_id, branch_name) 
		DO UPDATE SET 
			name = EXCLUDED.name,
			description = EXCLUDED.description,
			hook_type = EXCLUDED.hook_type,
			table_name = EXCLUDED.table_name,
			event_type = EXCLUDED.event_type,
			graph_json = EXCLUDED.graph_json,
			is_active = EXCLUDED.is_active,
			status = EXCLUDED.status,
			execution_mode = EXCLUDED.execution_mode,
			route_pattern = EXCLUDED.route_pattern,
			method = EXCLUDED.method
	`
	if _, err := s.pool.Exec(ctx, copyAutomationsQuery, req.Name, projectSlug, parentBranchName); err != nil {
		log.Printf("[BranchService] Warning: failed to copy nexus automations from %s to %s for project %s: %v", parentBranchName, req.Name, projectSlug, err)
	} else {
		log.Printf("[BranchService] Successfully copied nexus automations from %s to %s for project %s", parentBranchName, req.Name, projectSlug)
	}

	// Copy storage metadata objects (buckets, folders) from parent branch to the new branch
	copyStorageQuery := `
		INSERT INTO system.storage_objects (
			project_slug, branch_name, bucket, name, parent_path, 
			full_path, size, mime_type, is_folder, provider, 
			updated_at, rls_enabled
		)
		SELECT 
			project_slug, $1, bucket, name, parent_path, 
			full_path, size, mime_type, is_folder, provider, 
			NOW(), rls_enabled
		FROM system.storage_objects
		WHERE project_slug = $2 AND branch_name = $3
		ON CONFLICT (project_slug, branch_name, bucket, full_path) 
		DO UPDATE SET 
			name = EXCLUDED.name,
			parent_path = EXCLUDED.parent_path,
			size = EXCLUDED.size,
			mime_type = EXCLUDED.mime_type,
			is_folder = EXCLUDED.is_folder,
			provider = EXCLUDED.provider,
			updated_at = NOW(),
			rls_enabled = EXCLUDED.rls_enabled
	`
	if _, err := s.pool.Exec(ctx, copyStorageQuery, req.Name, projectSlug, parentBranchName); err != nil {
		log.Printf("[BranchService] Warning: failed to copy storage objects metadata from %s to %s for project %s: %v", parentBranchName, req.Name, projectSlug, err)
	} else {
		log.Printf("[BranchService] Successfully copied storage objects metadata from %s to %s for project %s", parentBranchName, req.Name, projectSlug)
	}

	// Replicate physical directories for storage buckets on the filesystem
	storageRoot := os.Getenv("STORAGE_ROOT")
	if storageRoot == "" {
		storageRoot = "./storage"
	}
	var parentPath string
	if parentBranchName == "main" {
		parentPath = filepath.Join(storageRoot, projectSlug)
	} else {
		parentPath = filepath.Join(storageRoot, projectSlug, "branches", parentBranchName)
	}
	targetPath := filepath.Join(storageRoot, projectSlug, "branches", req.Name)

	// Recreate directories found in the parent branch's filesystem
	if entries, err := os.ReadDir(parentPath); err == nil {
		for _, entry := range entries {
			if entry.IsDir() && entry.Name() != "branches" && entry.Name() != ".temp" {
				bucketDir := filepath.Join(targetPath, entry.Name())
				if err := os.MkdirAll(bucketDir, 0750); err == nil {
					log.Printf("[BranchService] Replicated filesystem directory for bucket %s in branch %s", entry.Name(), req.Name)
				}
			}
		}
	}

	// Double-safety: check metadata rows for unique buckets to ensure they are created
	if rows, err := s.pool.Query(ctx, `
		SELECT DISTINCT bucket FROM system.storage_objects 
		WHERE project_slug = $1 AND branch_name = $2
	`, projectSlug, parentBranchName); err == nil {
		defer rows.Close()
		for rows.Next() {
			var b string
			if scanErr := rows.Scan(&b); scanErr == nil && b != "" {
				bucketDir := filepath.Join(targetPath, b)
				if err := os.MkdirAll(bucketDir, 0750); err != nil {
					log.Printf("[BranchService] Warning: failed to create physical directory for bucket %s: %v", b, err)
				}
			}
		}
	}

	// Carrega conteúdo completo
	branch.Migrations = req.Migrations
	branch.FunctionsSQL = req.FunctionsSQL
	branch.TriggersSQL = req.TriggersSQL
	branch.RLSSQL = req.RLSSQL
	branch.AutomationsJSON = req.AutomationsJSON
	branch.AuthConfigJSON = req.AuthConfigJSON

	return &branch, nil
}

// GetBranch busca uma branch por nome
func (s *BranchService) GetBranch(ctx context.Context, projectSlug, branchName string) (*Branch, error) {
	query := `
		SELECT id, project_slug, name, branch_type, status, parent_branch,
			created_at, updated_at, created_by, is_main, data_branch_db_name,
			data_branch_ttl_hours, expires_at, migrations, functions_sql,
			triggers_sql, rls_sql, automations_json, auth_config_json, checksum,
			materialized_db, last_accessed_at, materialization_ttl_hours, data_mode
		FROM system.branches
		WHERE project_slug = $1 AND name = $2
	`

	row := s.pool.QueryRow(ctx, query, projectSlug, branchName)

	var branch Branch
	var migrationsJSONB []byte
	var automationsJSONB []byte
	var authConfigJSONB []byte

	err := row.Scan(
		&branch.ID,
		&branch.ProjectSlug,
		&branch.Name,
		&branch.BranchType,
		&branch.Status,
		&branch.ParentBranch,
		&branch.CreatedAt,
		&branch.UpdatedAt,
		&branch.CreatedBy,
		&branch.IsMain,
		&branch.DataBranchDBName,
		&branch.DataBranchTTLHours,
		&branch.ExpiresAt,
		&migrationsJSONB,
		&branch.FunctionsSQL,
		&branch.TriggersSQL,
		&branch.RLSSQL,
		&automationsJSONB,
		&authConfigJSONB,
		&branch.Checksum,
		&branch.MaterializedDB,
		&branch.LastAccessedAt,
		&branch.MaterializationTTLHours,
		&branch.DataMode,
	)

	if err == pgx.ErrNoRows {
		return nil, fmt.Errorf("branch not found")
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get branch: %w", err)
	}

	// Deserializa JSONB
	if len(migrationsJSONB) > 0 {
		if err := json.Unmarshal(migrationsJSONB, &branch.Migrations); err != nil {
			return nil, fmt.Errorf("failed to unmarshal migrations: %w", err)
		}
	}

	if len(automationsJSONB) > 0 {
		automationsStr := string(automationsJSONB)
		branch.AutomationsJSON = &automationsStr
	}

	if len(authConfigJSONB) > 0 {
		authConfigStr := string(authConfigJSONB)
		branch.AuthConfigJSON = &authConfigStr
	}

	return &branch, nil
}

// GetBranchByID busca uma branch por ID
func (s *BranchService) GetBranchByID(ctx context.Context, branchID string) (*Branch, error) {
	query := `
		SELECT id, project_slug, name, branch_type, status, parent_branch,
			created_at, updated_at, created_by, is_main, data_branch_db_name,
			data_branch_ttl_hours, expires_at, migrations, functions_sql,
			triggers_sql, rls_sql, automations_json, auth_config_json, checksum,
			materialized_db, last_accessed_at, materialization_ttl_hours
		FROM system.branches
		WHERE id = $1
	`

	row := s.pool.QueryRow(ctx, query, branchID)

	var branch Branch
	var migrationsJSONB []byte
	var automationsJSONB []byte
	var authConfigJSONB []byte

	err := row.Scan(
		&branch.ID,
		&branch.ProjectSlug,
		&branch.Name,
		&branch.BranchType,
		&branch.Status,
		&branch.ParentBranch,
		&branch.CreatedAt,
		&branch.UpdatedAt,
		&branch.CreatedBy,
		&branch.IsMain,
		&branch.DataBranchDBName,
		&branch.DataBranchTTLHours,
		&branch.ExpiresAt,
		&migrationsJSONB,
		&branch.FunctionsSQL,
		&branch.TriggersSQL,
		&branch.RLSSQL,
		&automationsJSONB,
		&authConfigJSONB,
		&branch.Checksum,
		&branch.MaterializedDB,
		&branch.LastAccessedAt,
		&branch.MaterializationTTLHours,
	)

	if err == pgx.ErrNoRows {
		return nil, fmt.Errorf("branch not found")
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get branch: %w", err)
	}

	// Deserializa JSONB
	if len(migrationsJSONB) > 0 {
		if err := json.Unmarshal(migrationsJSONB, &branch.Migrations); err != nil {
			return nil, fmt.Errorf("failed to unmarshal migrations: %w", err)
		}
	}

	if len(automationsJSONB) > 0 {
		automationsStr := string(automationsJSONB)
		branch.AutomationsJSON = &automationsStr
	}

	if len(authConfigJSONB) > 0 {
		authConfigStr := string(authConfigJSONB)
		branch.AuthConfigJSON = &authConfigStr
	}

	return &branch, nil
}

// ListBranches lista todas as branches de um projeto
func (s *BranchService) ListBranches(ctx context.Context, projectSlug string) (*ListBranchesResponse, error) {
	query := `
		SELECT id, project_slug, name, branch_type, status, parent_branch,
			created_at, updated_at, created_by, is_main, data_branch_db_name,
			data_branch_ttl_hours, expires_at, checksum,
			materialized_db, last_accessed_at, materialization_ttl_hours, data_mode
		FROM system.branches
		WHERE project_slug = $1
		ORDER BY created_at DESC
	`

	rows, err := s.pool.Query(ctx, query, projectSlug)
	if err != nil {
		return nil, fmt.Errorf("failed to list branches: %w", err)
	}
	defer rows.Close()

	branches := make([]Branch, 0)

	for rows.Next() {
		var branch Branch
		err := rows.Scan(
			&branch.ID,
			&branch.ProjectSlug,
			&branch.Name,
			&branch.BranchType,
			&branch.Status,
			&branch.ParentBranch,
			&branch.CreatedAt,
			&branch.UpdatedAt,
			&branch.CreatedBy,
			&branch.IsMain,
			&branch.DataBranchDBName,
			&branch.DataBranchTTLHours,
			&branch.ExpiresAt,
			&branch.Checksum,
			&branch.MaterializedDB,
			&branch.LastAccessedAt,
			&branch.MaterializationTTLHours,
			&branch.DataMode,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan branch: %w", err)
		}
		branches = append(branches, branch)
	}

	return &ListBranchesResponse{
		Branches: branches,
		Total:    len(branches),
	}, nil
}

// UpdateBranch atualiza uma branch
func (s *BranchService) UpdateBranch(ctx context.Context, projectSlug, branchName string, req UpdateBranchRequest) (*Branch, error) {
	// Busca a branch atual
	branch, err := s.GetBranch(ctx, projectSlug, branchName)
	if err != nil {
		return nil, err
	}

	// Não permite modificar branch main
	if branch.IsMain {
		return nil, fmt.Errorf("cannot modify main branch")
	}

	// Calcula novo checksum
	checksum, err := s.calculateChecksumFromBranch(branch, req)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate checksum: %w", err)
	}

	// Serializa migrations para JSONB
	var migrationsJSONB []byte
	if len(req.Migrations) > 0 {
		migrationsJSONB, err = json.Marshal(req.Migrations)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal migrations: %w", err)
		}
	} else {
		migrationsJSONB, _ = json.Marshal(branch.Migrations)
	}

	// Atualiza campos fornecidos
	functionsSQL := req.FunctionsSQL
	if functionsSQL == nil {
		functionsSQL = branch.FunctionsSQL
	}

	triggersSQL := req.TriggersSQL
	if triggersSQL == nil {
		triggersSQL = branch.TriggersSQL
	}

	rlsSQL := req.RLSSQL
	if rlsSQL == nil {
		rlsSQL = branch.RLSSQL
	}

	automationsJSON := req.AutomationsJSON
	if automationsJSON == nil {
		automationsJSON = branch.AutomationsJSON
	}

	authConfigJSON := req.AuthConfigJSON
	if authConfigJSON == nil {
		authConfigJSON = branch.AuthConfigJSON
	}

	query := `
		UPDATE system.branches
		SET migrations = $1,
			functions_sql = $2,
			triggers_sql = $3,
			rls_sql = $4,
			automations_json = $5,
			auth_config_json = $6,
			checksum = $7
		WHERE project_slug = $8 AND name = $9
		RETURNING id, project_slug, name, branch_type, status, parent_branch,
			created_at, updated_at, created_by, is_main, data_branch_db_name,
			data_branch_ttl_hours, expires_at, checksum
	`

	row := s.pool.QueryRow(ctx, query,
		migrationsJSONB,
		functionsSQL,
		triggersSQL,
		rlsSQL,
		automationsJSON,
		authConfigJSON,
		checksum,
		projectSlug,
		branchName,
	)

	var updatedBranch Branch
	err = row.Scan(
		&updatedBranch.ID,
		&updatedBranch.ProjectSlug,
		&updatedBranch.Name,
		&updatedBranch.BranchType,
		&updatedBranch.Status,
		&updatedBranch.ParentBranch,
		&updatedBranch.CreatedAt,
		&updatedBranch.UpdatedAt,
		&updatedBranch.CreatedBy,
		&updatedBranch.IsMain,
		&updatedBranch.DataBranchDBName,
		&updatedBranch.DataBranchTTLHours,
		&updatedBranch.ExpiresAt,
		&updatedBranch.Checksum,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to update branch: %w", err)
	}

	// Carrega conteúdo
	updatedBranch.Migrations = req.Migrations
	if updatedBranch.Migrations == nil {
		updatedBranch.Migrations = branch.Migrations
	}
	updatedBranch.FunctionsSQL = functionsSQL
	updatedBranch.TriggersSQL = triggersSQL
	updatedBranch.RLSSQL = rlsSQL
	updatedBranch.AutomationsJSON = automationsJSON
	updatedBranch.AuthConfigJSON = authConfigJSON

	return &updatedBranch, nil
}

// DeleteBranch deleta uma branch
func (s *BranchService) DeleteBranch(ctx context.Context, projectSlug, branchName string) error {
	// Busca a branch para verificar se é main
	branch, err := s.GetBranch(ctx, projectSlug, branchName)
	if err != nil {
		return err
	}

	// Não permite deletar branch main
	if branch.IsMain {
		return fmt.Errorf("cannot delete main branch")
	}

	// Se for branch de dados, deleta o banco temporário
	if branch.BranchType == BranchTypeData && branch.DataBranchDBName != nil {
		if err := s.dropDataBranchDB(ctx, *branch.DataBranchDBName); err != nil {
			return fmt.Errorf("failed to drop data branch database: %w", err)
		}
	}

	// Se a branch tem um banco materializado (thin clone), dropa também
	if branch.MaterializedDB != nil && *branch.MaterializedDB != "" {
		if err := s.dropDataBranchDB(ctx, *branch.MaterializedDB); err != nil {
			// Log mas não falha — o banco pode já ter sido dropado pelo TTL
			fmt.Printf("[DeleteBranch] Warning: failed to drop materialized db %s: %v\n", *branch.MaterializedDB, err)
		}
	}

	query := `
		DELETE FROM system.branches
		WHERE project_slug = $1 AND name = $2
	`

	result, err := s.pool.Exec(ctx, query, projectSlug, branchName)
	if err != nil {
		return fmt.Errorf("failed to delete branch: %w", err)
	}

	if result.RowsAffected() == 0 {
		return fmt.Errorf("branch not found")
	}

	return nil
}

// createDataBranchDB cria um banco temporário usando CREATE DATABASE ... TEMPLATE
// GAP #6 FIX: Sanitização rigorosa para prevenir SQL Injection
func (s *BranchService) createDataBranchDB(ctx context.Context, dbName, projectSlug string, sourceSnapshot *string) error {
	// Usa o banco do projeto como template por padrão
	templateDB := fmt.Sprintf("cascata_%s", projectSlug)
	if sourceSnapshot != nil && *sourceSnapshot != "" {
		templateDB = *sourceSnapshot
	}

	// GAP #6 FIX: Sanitização rigorosa do nome do banco
	if !isValidDBName(dbName) {
		return fmt.Errorf("invalid database name: must contain only alphanumeric characters and underscores")
	}
	if sourceSnapshot != nil && *sourceSnapshot != "" {
		if !isValidDBName(*sourceSnapshot) {
			return fmt.Errorf("invalid source snapshot name: security restriction")
		}
	}

	// Limita a 50 caracteres para deixar espaço para prefixos
	if len(dbName) > 50 {
		return fmt.Errorf("database name too long: maximum 50 characters allowed")
	}

	// Usa pgx.Identifier.QuoteString para sanitização adequada
	sanitizedDBName := sanitizeIdentifier(dbName)
	sanitizedTemplateDB := sanitizeIdentifier(templateDB)

	// CRITICAL FIX: Desconecta todas as sessões ativas no banco-fonte antes do TEMPLATE clone
	// O PostgreSQL exige ZERO conexões ativas no banco TEMPLATE durante CREATE DATABASE ... TEMPLATE
	// Isso afeta pools do PgBouncer, conexões lazy do backend e qualquer sessão ativa
	terminateQuery := fmt.Sprintf(
		`SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = '%s' AND pid != pg_backend_pid()`,
		templateDB,
	)
	_, _ = s.pool.Exec(ctx, terminateQuery)

	// Pequeno delay para dar tempo ao PostgreSQL de liberar os slots
	time.Sleep(200 * time.Millisecond)

	query := fmt.Sprintf("CREATE DATABASE %s TEMPLATE %s", sanitizedDBName, sanitizedTemplateDB)

	_, err := s.pool.Exec(ctx, query)
	if err != nil {
		return fmt.Errorf("failed to create database: %w", err)
	}

	return nil
}

// createSchemaOnlyBranch cria uma branch de dados com schema completo mas sem dados
// Estratégia: clona via TEMPLATE (preservando todas as estruturas), depois trunca todas as tabelas
// Isso garante que indexes, constraints, triggers, RLS, sequences, extensions fiquem perfeitos
func (s *BranchService) createSchemaOnlyBranch(ctx context.Context, dbName, projectSlug string) error {
	// Step 1: Clone completo (reutiliza o mecanismo existente — robusto e testado)
	if err := s.createDataBranchDB(ctx, dbName, projectSlug, nil); err != nil {
		return fmt.Errorf("schema-only: clone phase failed: %w", err)
	}

	// Step 2: Conectar ao novo banco e truncar todas as tabelas públicas
	connConfig := s.pool.Config().ConnConfig.Copy()
	connConfig.Database = dbName
	conn, err := pgx.ConnectConfig(ctx, connConfig)
	if err != nil {
		return fmt.Errorf("schema-only: failed to connect to new database: %w", err)
	}
	defer conn.Close(ctx)

	// Busca todas as tabelas públicas do tipo BASE TABLE
	rows, err := conn.Query(ctx, `
		SELECT table_name FROM information_schema.tables 
		WHERE table_schema = 'public' AND table_type = 'BASE TABLE'
		AND table_name NOT LIKE '_deleted_%'
	`)
	if err != nil {
		return fmt.Errorf("schema-only: failed to list tables: %w", err)
	}

	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err == nil {
			tables = append(tables, sanitizeIdentifier(name))
		}
	}
	rows.Close()

	if len(tables) > 0 {
		// TRUNCATE em cascata — remove todos os dados preservando a estrutura
		truncateSQL := fmt.Sprintf("TRUNCATE %s CASCADE", strings.Join(tables, ", "))
		_, err = conn.Exec(ctx, truncateSQL)
		if err != nil {
			return fmt.Errorf("schema-only: failed to truncate tables: %w", err)
		}
		log.Printf("[BranchService] Schema-only branch %s: truncated %d tables", dbName, len(tables))
	}

	return nil
}

// createReflectiveBranch cria uma branch reflexiva usando postgres_fdw
// Estratégia: clona via TEMPLATE (copiando todas as estruturas e o schema system intacto),
// depois conecta ao novo banco, dropa as tabelas públicas locais e as importa via FDW do Live.
// Isso garante que tabelas como system.assets, triggers, extensões e schemas do sistema fiquem 100% preservadas e operacionais.
func (s *BranchService) createReflectiveBranch(ctx context.Context, dbName, projectSlug string) error {
	liveDB := fmt.Sprintf("cascata_%s", projectSlug)

	// Validação de segurança
	if !isValidDBName(dbName) {
		return fmt.Errorf("reflective: invalid database name")
	}
	if !isValidDBName(liveDB) {
		return fmt.Errorf("reflective: invalid live database name")
	}

	// Step 1: Clone completo via TEMPLATE (assim preservamos o schema 'system', extensões, permissões)
	if err := s.createDataBranchDB(ctx, dbName, projectSlug, nil); err != nil {
		return fmt.Errorf("reflective: clone phase failed: %w", err)
	}

	// Step 2: Conectar ao novo banco
	connConfig := s.pool.Config().ConnConfig.Copy()
	connConfig.Database = dbName
	conn, err := pgx.ConnectConfig(ctx, connConfig)
	if err != nil {
		_ = s.dropDataBranchDB(ctx, dbName)
		return fmt.Errorf("reflective: failed to connect to new database: %w", err)
	}
	defer conn.Close(ctx)

	// Step 3: Listar todas as tabelas públicas (BASE TABLE)
	rows, err := conn.Query(ctx, `
		SELECT table_name FROM information_schema.tables 
		WHERE table_schema = 'public' AND table_type = 'BASE TABLE'
		AND table_name NOT LIKE '_deleted_%'
	`)
	if err != nil {
		_ = s.dropDataBranchDB(ctx, dbName)
		return fmt.Errorf("reflective: failed to list public tables: %w", err)
	}

	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err == nil {
			tables = append(tables, sanitizeIdentifier(name))
		}
	}
	rows.Close()

	// Step 4: Drop das tabelas públicas locais para podermos importar como estrangeiras
	if len(tables) > 0 {
		dropSQL := fmt.Sprintf("DROP TABLE %s CASCADE", strings.Join(tables, ", "))
		_, err = conn.Exec(ctx, dropSQL)
		if err != nil {
			_ = s.dropDataBranchDB(ctx, dbName)
			return fmt.Errorf("reflective: failed to drop public tables: %w", err)
		}
	}

	// Step 5: Instalar a extensão, configurar foreign server, user mapping e importar
	username := connConfig.User

	// Sequência atômica de setup do FDW
	fdwStatements := []string{
		`CREATE EXTENSION IF NOT EXISTS postgres_fdw`,

		// 2. Criar foreign server apontando para o banco Live do tenant
		// Como ambos os bancos estão no MESMO cluster Postgres, a conexão é local e instantânea
		fmt.Sprintf(`CREATE SERVER cascata_live_source FOREIGN DATA WRAPPER postgres_fdw OPTIONS (dbname '%s')`, liveDB),

		// 3. Mapear o usuário atual para o servidor (mesmas credenciais, mesmo cluster)
		fmt.Sprintf(`CREATE USER MAPPING FOR CURRENT_USER SERVER cascata_live_source OPTIONS (user '%s')`, username),

		// 4. Importar TODAS as tabelas do schema public do Live como foreign tables
		// Isso cria instantaneamente uma foreign table para cada tabela real do Live
		// Leitura é direta e real-time, sem cópia de dados
		`IMPORT FOREIGN SCHEMA public FROM SERVER cascata_live_source INTO public`,
	}

	for i, stmt := range fdwStatements {
		_, err := conn.Exec(ctx, stmt)
		if err != nil {
			_ = s.dropDataBranchDB(ctx, dbName)
			return fmt.Errorf("reflective: FDW setup step %d failed: %w", i+1, err)
		}
	}

	log.Printf("[BranchService] Reflective branch %s created successfully with FDW bridge to %s", dbName, liveDB)
	return nil
}

// dropDataBranchDB deleta um banco temporário de branch de dados
// GAP #6 FIX: Sanitização rigorosa para prevenir SQL Injection
func (s *BranchService) dropDataBranchDB(ctx context.Context, dbName string) error {
	// GAP #6 FIX: Sanitização rigorosa do nome do banco
	if !isValidDBName(dbName) {
		return fmt.Errorf("invalid database name: must contain only alphanumeric characters and underscores")
	}

	if len(dbName) > 50 {
		return fmt.Errorf("database name too long: maximum 50 characters allowed")
	}

	sanitizedDBName := sanitizeIdentifier(dbName)
	query := fmt.Sprintf("DROP DATABASE IF EXISTS %s", sanitizedDBName)

	_, err := s.pool.Exec(ctx, query)
	if err != nil {
		return fmt.Errorf("failed to drop database: %w", err)
	}

	return nil
}

// isValidDBName verifica se o nome do banco é válido (apenas alphanumeric + underscore)
func isValidDBName(name string) bool {
	if len(name) == 0 {
		return false
	}
	for _, c := range name {
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_') {
			return false
		}
	}
	return true
}

// sanitizeIdentifier usa pgx.Identifier para sanitização segura de identifiers SQL
func sanitizeIdentifier(name string) string {
	return pgx.Identifier{name}.Sanitize()
}

// registerDataBranch registra uma branch de dados na tabela de tracking
func (s *BranchService) registerDataBranch(ctx context.Context, projectSlug, branchName, dbName string, createdBy *string) error {
	templateDB := fmt.Sprintf("cascata_%s", projectSlug)
	expiresAt := time.Now().Add(168 * time.Hour) // 7 dias padrão

	query := `
		INSERT INTO system.data_branches (
			project_slug, branch_name, db_name, template_db,
			status, created_by, expires_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7)
	`

	_, err := s.pool.Exec(ctx, query,
		projectSlug,
		branchName,
		dbName,
		templateDB,
		BranchStatusActive,
		createdBy,
		expiresAt,
	)

	if err != nil {
		return fmt.Errorf("failed to register data branch: %w", err)
	}

	return nil
}

// calculateChecksum calcula SHA-256 do conteúdo da branch
func (s *BranchService) calculateChecksum(req CreateBranchRequest) (string, error) {
	data := map[string]interface{}{
		"migrations":       req.Migrations,
		"functions_sql":    req.FunctionsSQL,
		"triggers_sql":     req.TriggersSQL,
		"rls_sql":          req.RLSSQL,
		"automations_json": req.AutomationsJSON,
		"auth_config_json": req.AuthConfigJSON,
	}

	jsonData, err := json.Marshal(data)
	if err != nil {
		return "", err
	}

	hash := sha256.Sum256(jsonData)
	return hex.EncodeToString(hash[:]), nil
}

// calculateChecksumFromBranch calcula checksum a partir de uma branch existente e request de update
func (s *BranchService) calculateChecksumFromBranch(branch *Branch, req UpdateBranchRequest) (string, error) {
	migrations := req.Migrations
	if migrations == nil {
		migrations = branch.Migrations
	}

	functionsSQL := req.FunctionsSQL
	if functionsSQL == nil {
		functionsSQL = branch.FunctionsSQL
	}

	triggersSQL := req.TriggersSQL
	if triggersSQL == nil {
		triggersSQL = branch.TriggersSQL
	}

	rlsSQL := req.RLSSQL
	if rlsSQL == nil {
		rlsSQL = branch.RLSSQL
	}

	automationsJSON := req.AutomationsJSON
	if automationsJSON == nil {
		automationsJSON = branch.AutomationsJSON
	}

	authConfigJSON := req.AuthConfigJSON
	if authConfigJSON == nil {
		authConfigJSON = branch.AuthConfigJSON
	}

	data := map[string]interface{}{
		"migrations":       migrations,
		"functions_sql":    functionsSQL,
		"triggers_sql":     triggersSQL,
		"rls_sql":          rlsSQL,
		"automations_json": automationsJSON,
		"auth_config_json": authConfigJSON,
	}

	jsonData, err := json.Marshal(data)
	if err != nil {
		return "", err
	}

	hash := sha256.Sum256(jsonData)
	return hex.EncodeToString(hash[:]), nil
}

// sanitizeBranchName sanitiza o nome da branch para uso em nome de banco
func sanitizeBranchName(name string) string {
	// Normaliza para minúsculas e substitui caracteres não alfanuméricos por underscore
	name = strings.ToLower(name)
	result := ""
	for _, c := range name {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			result += string(c)
		} else {
			result += "_"
		}
	}
	// Remove underscores duplicados para limpeza estética
	for strings.Contains(result, "__") {
		result = strings.ReplaceAll(result, "__", "_")
	}
	return strings.Trim(result, "_")
}

// GetBranchByName é um alias para GetBranch (compatibilidade)
func (s *BranchService) GetBranchByName(ctx context.Context, projectSlug, branchName string) (*Branch, error) {
	return s.GetBranch(ctx, projectSlug, branchName)
}

// ============================================================================
// MATERIALIZAÇÃO ON-DEMAND (Thin Clone Architecture)
// ============================================================================

// AccessBranch materializa a branch on-demand e retorna informações de conexão.
// Para environment branches: cria um thin clone via pg_dump --schema-only se necessário.
// Para data branches: verifica se o banco dedicado existe.
// Em ambos os casos, atualiza last_accessed_at para controle de TTL.
func (s *BranchService) AccessBranch(ctx context.Context, projectSlug, branchName string) (*AccessBranchResponse, error) {
	branch, err := s.GetBranch(ctx, projectSlug, branchName)
	if err != nil {
		return nil, err
	}

	if branch.Status != BranchStatusActive {
		return nil, fmt.Errorf("branch '%s' is not active (status: %s)", branchName, branch.Status)
	}

	// Branch main = banco principal, sem materialização necessária
	if branch.IsMain {
		sourceDB := fmt.Sprintf("cascata_%s", projectSlug)
		return &AccessBranchResponse{
			Success:       true,
			Message:       "Main branch is always live — connected to production database.",
			Branch:        *branch,
			DatabaseName:  sourceDB,
			EnvIdentifier: "live",
			Materialized:  false,
		}, nil
	}

	response := &AccessBranchResponse{
		Success: true,
		Branch:  *branch,
	}

	switch branch.BranchType {
	case BranchTypeEnvironment:
		err = s.accessEnvironmentBranch(ctx, branch, projectSlug, response)
	case BranchTypeData:
		err = s.accessDataBranch(ctx, branch, projectSlug, response)
	default:
		return nil, fmt.Errorf("unknown branch type: %s", branch.BranchType)
	}

	if err != nil {
		return nil, err
	}

	// Atualiza last_accessed_at para controle de TTL
	_, _ = s.pool.Exec(ctx,
		`UPDATE system.branches SET last_accessed_at = NOW() WHERE id = $1`,
		branch.ID,
	)

	return response, nil
}

// accessEnvironmentBranch materializa uma branch de ambiente via thin clone
// ISOLATION: Cada branch é independente. O banco materializado pertence SOMENTE a esta branch.
// Quando já materializado e existente → reconecta sem tocar.
// Quando precisa rematerializar (TTL expirou) → clona schema de main + reaplica migrations salvas.
// NOTA: A rematerialização usa o schema ATUAL de main como base, mas as migrations salvas
// pela SnapshotBranchState garantem que o estado da branch é restaurado corretamente.
func (s *BranchService) accessEnvironmentBranch(ctx context.Context, branch *Branch, projectSlug string, response *AccessBranchResponse) error {
	sourceDB := fmt.Sprintf("cascata_%s", projectSlug)
	targetDB := fmt.Sprintf("cascata_%s_%s_env", projectSlug, sanitizeBranchName(branch.Name))

	// ANINHAMENTO INTELIGENTE DE BRANCHES (Parent Branch Resolution)
	// Se a branch atual possui uma branch pai e ela não for o 'main'
	if branch.ParentBranch != nil && *branch.ParentBranch != "" && *branch.ParentBranch != "main" {
		parent, err := s.GetBranch(ctx, projectSlug, *branch.ParentBranch)
		if err == nil && parent != nil {
			// Se a branch pai estiver materializada ativamente no postgres, clonamos direto do schema dela!
			if parent.MaterializedDB != nil && *parent.MaterializedDB != "" {
				var exists bool
				if err := s.pool.QueryRow(ctx,
					`SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = $1)`,
					*parent.MaterializedDB,
				).Scan(&exists); err == nil && exists {
					sourceDB = *parent.MaterializedDB
					log.Printf("[accessEnvironmentBranch] Nested branch active: Cloned from materialized parent branch '%s' (%s) instead of main.", parent.Name, sourceDB)
				}
			}
		}
	}

	// Verifica se já está materializada e o banco ainda existe
	if branch.MaterializedDB != nil && *branch.MaterializedDB != "" {
		// Verifica se o banco ainda existe (pode ter sido dropado pelo TTL)
		var exists bool
		if err := s.pool.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = $1)`,
			*branch.MaterializedDB,
		).Scan(&exists); err == nil && exists {
			// ISOLAMENTO GARANTIDO: Banco existe, reconecta sem tocar nele.
			// Nenhuma operação de schema é feita — o banco está exatamente como foi deixado.
			response.DatabaseName = *branch.MaterializedDB
			response.EnvIdentifier = branch.Name
			response.Materialized = false
			response.Message = fmt.Sprintf("Reconnected to environment branch '%s'. Database '%s' is active.", branch.Name, *branch.MaterializedDB)

			// Calcula TTL restante
			if branch.LastAccessedAt != nil && branch.MaterializationTTLHours != nil {
				ttlEnd := branch.LastAccessedAt.Add(time.Duration(*branch.MaterializationTTLHours) * time.Hour)
				remaining := time.Until(ttlEnd)
				if remaining > 0 {
					response.ExpiresIn = remaining.Round(time.Minute).String()
				}
			}
			return nil
		}
		// Banco foi dropado (TTL ou manual) — rematerializa abaixo
		log.Printf("[accessEnvironmentBranch] Branch '%s' bank was dropped (TTL or manual). Will rematerialize with %d saved migrations.", branch.Name, len(branch.Migrations))
	}

	// Validações de nome
	if !isValidDBName(targetDB) || len(targetDB) > 63 {
		return fmt.Errorf("generated database name is invalid: %s", targetDB)
	}

	// Cria o thin clone via pg_dump --schema-only | psql
	// NOTA: Isso clona o schema ATUAL de main. As migrations salvas serão reaplicadas
	// pelo materializeThinClone para restaurar o estado exclusivo desta branch.
	if err := s.materializeThinClone(ctx, branch, sourceDB, targetDB); err != nil {
		return fmt.Errorf("failed to materialize thin clone: %w", err)
	}

	// Registra materialização no banco
	_, err := s.pool.Exec(ctx,
		`UPDATE system.branches
		 SET materialized_db = $1, last_accessed_at = NOW()
		 WHERE id = $2`,
		targetDB, branch.ID,
	)
	if err != nil {
		// Tenta cleanup se o update falhar
		_ = s.dropDataBranchDB(ctx, targetDB)
		return fmt.Errorf("failed to record materialization: %w", err)
	}

	response.DatabaseName = targetDB
	response.EnvIdentifier = branch.Name
	response.Materialized = true
	response.Message = fmt.Sprintf("Environment branch '%s' materialized as thin clone '%s'. Schema-only, no production data.", branch.Name, targetDB)

	ttlHours := 24
	if branch.MaterializationTTLHours != nil {
		ttlHours = *branch.MaterializationTTLHours
	}
	response.ExpiresIn = fmt.Sprintf("%dh0m", ttlHours)

	return nil
}

// accessDataBranch verifica se o banco dedicado da data branch existe
func (s *BranchService) accessDataBranch(ctx context.Context, branch *Branch, projectSlug string, response *AccessBranchResponse) error {
	if branch.DataBranchDBName == nil || *branch.DataBranchDBName == "" {
		return fmt.Errorf("data branch '%s' has no associated database", branch.Name)
	}

	// Verifica se o banco existe
	var exists bool
	if err := s.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = $1)`,
		*branch.DataBranchDBName,
	).Scan(&exists); err != nil {
		return fmt.Errorf("failed to verify data branch database: %w", err)
	}

	if !exists {
		return fmt.Errorf("data branch database '%s' does not exist — it may have expired or been deleted", *branch.DataBranchDBName)
	}

	response.DatabaseName = *branch.DataBranchDBName
	response.EnvIdentifier = branch.Name
	response.Materialized = false
	response.Message = fmt.Sprintf("Connected to data branch '%s'. Full clone database '%s'.", branch.Name, *branch.DataBranchDBName)

	if branch.ExpiresAt != nil {
		remaining := time.Until(*branch.ExpiresAt)
		if remaining > 0 {
			response.ExpiresIn = remaining.Round(time.Minute).String()
		}
	}

	return nil
}

// materializeThinClone cria um banco vazio e copia apenas o schema via pg_dump --schema-only
// ANTIFRAGILIDADE: Fecha pool local antes do DROP para evitar colisão 42P04
func (s *BranchService) materializeThinClone(ctx context.Context, branch *Branch, sourceDB, targetDB string) error {
	// 1. Verifica se o banco já existe para garantir idempotência (Antifragilidade)
	var exists bool
	checkSQL := `SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = $1)`
	_ = s.pool.QueryRow(ctx, checkSQL, targetDB).Scan(&exists)

	if !exists {
		createSQL := fmt.Sprintf("CREATE DATABASE %s", pgx.Identifier{targetDB}.Sanitize())
		if _, err := s.pool.Exec(ctx, createSQL); err != nil {
			return fmt.Errorf("failed to create database %s: %w", targetDB, err)
		}
	} else {
		// Se já existe, mas chegamos aqui, significa que a materialização anterior
		// pode estar incompleta ou o registro no system.branches sumiu.
		// CRÍTICO: Sequência correta para evitar 42P04:
		// 1. Fecha o pool do backend para este banco (evita conexões zumbis)
		// 2. Termina backends externos
		// 3. DROP DATABASE WITH (FORCE)
		// 4. Recria o banco

		log.Printf("[materializeThinClone] Collision detected for %s - initiating safe drop sequence", targetDB)

		// Passo 1: Fecha/invalida o pool do backend para este banco
		// Isso previne que o próprio backend tente reconectar durante o DROP
		services.Reload(targetDB)

		// Pequeno delay para garantir que o pool foi fechado
		time.Sleep(100 * time.Millisecond)

		// Passo 2: Termina TODAS as conexões ativas (backends externos + qualquer resquício)
		terminateSQL := fmt.Sprintf(
			"SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1 AND pid <> pg_backend_pid()",
		)
		_, _ = s.pool.Exec(ctx, terminateSQL, targetDB)

		// Passo 3: DROP DATABASE WITH (FORCE) - PostgreSQL 13+
		// FORCE mata qualquer conexão remanescente e força o drop
		dropSQL := fmt.Sprintf("DROP DATABASE %s WITH (FORCE)", pgx.Identifier{targetDB}.Sanitize())
		_, err := s.pool.Exec(ctx, dropSQL)
		if err != nil {
			log.Printf("[materializeThinClone] DROP WITH FORCE failed for %s: %v - retrying with legacy approach", targetDB, err)
			// Fallback: abordagem legada caso FORCE não funcione
			dropSQL = fmt.Sprintf("DROP DATABASE %s", pgx.Identifier{targetDB}.Sanitize())
			_, _ = s.pool.Exec(ctx, dropSQL)
		}

		// Passo 4: Recria o banco limpo
		createSQL := fmt.Sprintf("CREATE DATABASE %s", pgx.Identifier{targetDB}.Sanitize())
		if _, err := s.pool.Exec(ctx, createSQL); err != nil {
			return fmt.Errorf("failed to recreate database %s after collision: %w", targetDB, err)
		}

		log.Printf("[materializeThinClone] Safe drop/recreate completed for %s", targetDB)
	}

	// 2. Executa pg_dump --schema-only do banco source e aplica no target
	// O pg_dump gera DDL completo: tabelas, sequences, enums, constraints, indexes,
	// functions, triggers, RLS policies, grants — tudo sem dados.
	if err := s.execSchemaClone(ctx, sourceDB, targetDB); err != nil {
		// Cleanup: dropa o banco se a clonagem falhou
		_ = s.dropDataBranchDB(ctx, targetDB)
		return fmt.Errorf("schema clone failed: %w", err)
	}

	// GAP 1 FIX: Como o pg_dump roda com --no-privileges, as tabelas nascem
	// sem os grants para anon/authenticated, o que gera 500 no data_controller.
	// Forçamos a re-aplicação das permissões na public após o clone.
	dbSvc := services.DatabaseService{}
	if err := dbSvc.InitPublicSchemaPermissions(ctx, targetDB); err != nil {
		log.Printf("[materializeThinClone] Warning: failed to init permissions for %s: %v", targetDB, err)
	}

	// GAP 3 FIX: Hydration. Reaplicar migrations salvas pelo SnapshotBranchState.
	// RECURSIVE HYDRATION: Se o clone foi feito a partir do main, mas temos ancestrais não materializados,
	// acumulamos as migrations deles para garantir que o schema seja montado por completo.
	var allMigrations []Migration

	// Se clonamos de main, acumulamos as migrations dos pais não materializados
	if sourceDB == fmt.Sprintf("cascata_%s", branch.ProjectSlug) {
		currentParentName := branch.ParentBranch
		var parentsToHydrate []Migration
		
		for currentParentName != nil && *currentParentName != "" && *currentParentName != "main" {
			parentBranch, err := s.GetBranch(ctx, branch.ProjectSlug, *currentParentName)
			if err != nil {
				break
			}
			
			// Se o pai está materializado fisicamente no Postgres, nós paramos o acúmulo,
			// porque o clone do schema a partir deste pai já traria todas essas migrations aplicadas!
			if parentBranch.MaterializedDB != nil && *parentBranch.MaterializedDB != "" {
				var exists bool
				if err := s.pool.QueryRow(ctx,
					`SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = $1)`,
					*parentBranch.MaterializedDB,
				).Scan(&exists); err == nil && exists {
					break // Já está contemplado no clone
				}
			}
			
			// Acumula as migrations do pai (na frente, pois vieram antes na linha do tempo)
			if len(parentBranch.Migrations) > 0 {
				parentsToHydrate = append(parentBranch.Migrations, parentsToHydrate...)
			}
			currentParentName = parentBranch.ParentBranch
		}
		
		allMigrations = append(allMigrations, parentsToHydrate...)
	}

	// Por fim, adicionamos as migrations da própria branch
	allMigrations = append(allMigrations, branch.Migrations...)

	if len(allMigrations) > 0 {
		log.Printf("[materializeThinClone] Hydrating branch %s with %d total migrations (including ancestors if applicable)", branch.Name, len(allMigrations))
		
		// Conecta ao banco recém-criado para executar o SQL salvo
		// Vamos usar um pool efêmero para aplicar o SQL
		poolProvider := deployer.NewPoolAdapter()
		targetConn, err := poolProvider.AcquireForProject(branch.ProjectSlug, branch.Name)
		if err == nil {
			defer targetConn.Close()
			for _, m := range allMigrations {
				if m.SQL != "" {
					_, err := targetConn.Exec(m.SQL)
					if err != nil {
						log.Printf("[materializeThinClone] Failed to apply hydration migration %s: %v", m.Version, err)
					}
				}
			}
			log.Printf("[materializeThinClone] Hydration completed for branch %s", branch.Name)
		} else {
			log.Printf("[materializeThinClone] Failed to acquire connection for hydration: %v", err)
		}
	}

	return nil
}

// execSchemaClone executa pg_dump --schema-only piped para psql
func (s *BranchService) execSchemaClone(ctx context.Context, sourceDB, targetDB string) error {
	// Captura configurações do ambiente para conexão direta (bypass pgbouncer para dumps)
	host := os.Getenv("DB_DIRECT_HOST")
	if host == "" {
		host = "db" // Fallback padrão Docker
	}
	port := os.Getenv("DB_DIRECT_PORT")
	if port == "" {
		port = "5432"
	}
	user := os.Getenv("DB_USER")
	if user == "" {
		user = "postgres"
	}
	pass := os.Getenv("DB_PASS")

	// Monta os comandos com flags explícitas de host e usuário para evitar Unix Sockets
	// O pg_dump gera DDL completo: tabelas, sequences, enums, etc., sem dados.
	pgDumpCmd := fmt.Sprintf(
		"pg_dump -h %s -p %s -U %s --schema-only --no-owner --no-privileges -d %s | psql -h %s -p %s -U %s -d %s",
		host, port, user, sourceDB,
		host, port, user, targetDB,
	)

	// Executa com timeout de contexto (30s é mais que suficiente para schema-only)
	// Usamos 'sh' para máxima compatibilidade com imagens Docker (Alpine/Slim)
	cmdCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, "sh", "-c", pgDumpCmd)

	// Injeta PGPASSWORD no ambiente do comando para autenticação automática
	cmd.Env = os.Environ()
	if pass != "" {
		cmd.Env = append(cmd.Env, fmt.Sprintf("PGPASSWORD=%s", pass))
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("pg_dump --schema-only failed: %w\nOutput: %s", err, string(output))
	}

	return nil
}

// SnapshotBranchState salva o estado atual da branch materializada no system.branches
// Compara a branch com a main para gerar e armazenar o SQL das mudanças
// ISOLATION FIX: APPEND migrations instead of replacing them.
// Previously, each snapshot overwrote ALL migrations with just the latest diff,
// destroying the branch's migration history.
func (s *BranchService) SnapshotBranchState(ctx context.Context, projectSlug, branchName string) error {
	branch, err := s.GetBranch(ctx, projectSlug, branchName)
	if err != nil {
		return err
	}

	if branch.MaterializedDB == nil || *branch.MaterializedDB == "" {
		return nil // Sem banco materializado, nada a salvar
	}

	log.Printf("[SnapshotBranchState] Starting snapshot for branch %s", branchName)

	poolProvider := deployer.NewPoolAdapter()
	diffCtx := diff.DiffContext{
		PoolProvider: poolProvider,
		ProjectSlug:  projectSlug,
		SourceBranch: branchName,
		TargetBranch: "live", // "live" is main
		Mode:         diff.ModeBranchToMain,
	}

	engine := diff.NewDiffEngine(diffCtx)
	diffResult, err := engine.Run(ctx)
	if err != nil {
		return fmt.Errorf("failed to run diff for snapshot: %w", err)
	}

	if !diffResult.Success {
		return fmt.Errorf("diff failed: %s", diffResult.Error)
	}

	// ISOLATION FIX: Only save if there are actual changes.
	// Do NOT overwrite existing migrations — APPEND to them.
	if len(diffResult.SQL) > 0 {
		// Start with existing migrations from the branch
		allMigrations := make([]Migration, 0)
		if len(branch.Migrations) > 0 {
			allMigrations = append(allMigrations, branch.Migrations...)
		}

		// Append the new snapshot as a new migration version
		newVersion := time.Now().Format("20060102150405")
		allMigrations = append(allMigrations, Migration{
			Version: newVersion,
			SQL:     strings.Join(diffResult.SQL, "\n"),
		})

		migrationsJSON, err := json.Marshal(allMigrations)
		if err != nil {
			return fmt.Errorf("failed to marshal migrations for snapshot: %w", err)
		}

		_, err = s.pool.Exec(ctx, "UPDATE system.branches SET migrations = $1 WHERE id = $2", migrationsJSON, branch.ID)
		if err != nil {
			return fmt.Errorf("failed to save snapshot metadata: %w", err)
		}

		log.Printf("[SnapshotBranchState] Snapshot completed for branch %s — appended migration %s (%d SQL stmts)", branchName, newVersion, len(diffResult.SQL))
	} else {
		log.Printf("[SnapshotBranchState] No changes detected for branch %s — snapshot skipped", branchName)
	}

	return nil
}

// DematerializeBranch dropa o banco materializado e limpa os metadados
// Chamado pelo job de TTL ou pelo DeleteBranch
func (s *BranchService) DematerializeBranch(ctx context.Context, projectSlug, branchName string) error {
	branch, err := s.GetBranch(ctx, projectSlug, branchName)
	if err != nil {
		return err
	}

	if branch.MaterializedDB == nil || *branch.MaterializedDB == "" {
		return nil // Já desmaterializada, noop
	}

	// GAP 3 FIX: Salva as alterações feitas na branch antes de destruir
	if err := s.SnapshotBranchState(ctx, projectSlug, branchName); err != nil {
		log.Printf("[DematerializeBranch] Warning: failed to snapshot branch %s before dematerialization: %v", branchName, err)
	}

	// 1. Dropa o banco
	if err := s.dropDataBranchDB(ctx, *branch.MaterializedDB); err != nil {
		return fmt.Errorf("failed to drop materialized database: %w", err)
	}

	// 2. Limpa os metadados
	_, err = s.pool.Exec(ctx,
		`UPDATE system.branches
		 SET materialized_db = NULL, last_accessed_at = NULL
		 WHERE id = $1`,
		branch.ID,
	)
	if err != nil {
		return fmt.Errorf("failed to clear materialization metadata: %w", err)
	}

	return nil
}

// CleanupExpiredMaterializations varre branches com materialização expirada e as desmaterializa.
// Chamado pelo job de TTL (gocron, a cada hora).
// Delega para DematerializeBranch que usa o mesmo caminho de cleanup que DeleteBranch.
func (s *BranchService) CleanupExpiredMaterializations(ctx context.Context) (int, error) {
	// Busca branches materializadas que passaram do TTL
	query := `
		SELECT project_slug, name
		FROM system.branches
		WHERE materialized_db IS NOT NULL
		  AND last_accessed_at IS NOT NULL
		  AND last_accessed_at + (COALESCE(materialization_ttl_hours, 24) || ' hours')::INTERVAL < NOW()
		  AND status = 'active'
	`

	rows, err := s.pool.Query(ctx, query)
	if err != nil {
		// Se a coluna não existe ainda (migração em curso em outro worker), apenas ignoramos este ciclo
		// Isso evita erros ruidosos durante o boot multi-container
		return 0, nil
	}
	defer rows.Close()

	cleaned := 0
	for rows.Next() {
		var slug, name string
		if err := rows.Scan(&slug, &name); err != nil {
			continue
		}

		if err := s.DematerializeBranch(ctx, slug, name); err != nil {
			// Log mas continua — não falha o batch inteiro por causa de uma branch
			fmt.Printf("[BranchCleanup] Failed to dematerialize %s/%s: %v\n", slug, name, err)
			continue
		}

		fmt.Printf("[BranchCleanup] Dematerialized expired branch %s/%s\n", slug, name)
		cleaned++
	}

	return cleaned, nil
}	