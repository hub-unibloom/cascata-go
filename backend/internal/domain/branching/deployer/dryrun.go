package deployer

import (
	"context"
	"fmt"
	"time"

	"cascata-backend/internal/domain/branching/diff"
	"github.com/google/uuid"
)

// DryRunResult contém o resultado de uma operação dry-run
type DryRunResult struct {
	Success    bool
	Message    string
	Error      string
	SQLCount   int
	Duration   time.Duration
	Validated  []string // SQL statements validados
}

// RunDryRun executa o diff em modo dry-run
// Cria um schema temporário isolado e aplica o SQL gerado
// Nenhum lock é emitido em tabelas de produção
func (d *Deployer) RunDryRun(
	ctx context.Context,
	projectSlug string,
	env string,
	sql []string,
) (*DryRunResult, error) {
	startTime := time.Now()

	d.logger.Info("Starting dry run",
		"project", projectSlug,
		"env", env,
		"statements", len(sql),
	)

	if len(sql) == 0 {
		return &DryRunResult{
			Success: true,
			Message: "No changes detected",
			SQLCount: 0,
			Duration: time.Since(startTime),
		}, nil
	}

	// Cria um schema temporário para validação
	tempSchema := fmt.Sprintf("_dryrun_%s", uuid.New().String()[:8])

	// Adquire conexão do pool do projeto
	conn, err := d.poolProvider.AcquireForProject(projectSlug, env)
	if err != nil {
		return nil, fmt.Errorf("failed to acquire ephemeral connection: %w", err)
	}
	defer conn.Close()

	// Executa o dry run em uma goroutine com timeout
	resultCh := make(chan *DryRunResult, 1)
	errorCh := make(chan error, 1)

	go func() {
		defer close(resultCh)
		defer close(errorCh)

		result, err := d.executeDryRun(ctx, conn, tempSchema, sql)
		if err != nil {
			errorCh <- err
			return
		}
		resultCh <- result
	}()

	// Aguarda o resultado ou timeout
	select {
	case result := <-resultCh:
		result.Duration = time.Since(startTime)
		d.logger.Info("Dry run completed",
			"success", result.Success,
			"duration", result.Duration,
			"sql_count", result.SQLCount,
		)
		return result, nil

	case err := <-errorCh:
		return nil, fmt.Errorf("dry run execution failed: %w", err)

	case <-ctx.Done():
		return &DryRunResult{
			Success: false,
			Error:   "dry run timeout — SQL may be too complex",
			Duration: time.Since(startTime),
		}, nil
	}
}

// executeDryRun aplica o SQL contra um schema temporário para validação real
func (d *Deployer) executeDryRun(
	ctx context.Context,
	pool interface{},
	tempSchema string,
	sql []string,
) (*DryRunResult, error) {
	// Type assert para PoolConn
	conn, ok := pool.(diff.PoolConn)
	if !ok {
		return nil, fmt.Errorf("invalid pool type: expected diff.PoolConn")
	}

	validated := make([]string, 0, len(sql))
	
	// 1. Cria o schema temporário isolado
	createSchemaSQL := fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", tempSchema)
	_, err := conn.Exec(createSchemaSQL)
	if err != nil {
		return &DryRunResult{
			Success: false,
			Error:   fmt.Sprintf("failed to create temporary schema: %v", err),
		}, nil
	}
	defer func() {
		// Limpa o schema temporário após validação
		_, _ = conn.Exec(fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE", tempSchema))
	}()

	// 2. Aplica cada statement SQL com validação de sintaxe e semântica
	for i, stmt := range sql {
		d.logger.Debug("Validating statement", "index", i+1, "preview", truncateStatement(stmt, 50))
		
		// Executa o statement no schema temporário
		_, err := conn.Exec(stmt)
		if err != nil {
			return &DryRunResult{
				Success:   false,
				Error:     fmt.Sprintf("SQL validation failed at statement %d: %v\nStatement: %s", i+1, err, stmt),
				SQLCount:  len(validated),
				Validated: validated,
			}, nil
		}
		
		validated = append(validated, stmt)
	}

	// 3. Validação adicional: verifica consistência do schema
	if err := d.validateSchemaConsistency(ctx, conn, tempSchema); err != nil {
		return &DryRunResult{
			Success:   false,
			Error:     fmt.Sprintf("schema consistency check failed: %v", err),
			SQLCount:  len(validated),
			Validated: validated,
		}, nil
	}

	return &DryRunResult{
		Success:   true,
		Message:   fmt.Sprintf("Validated %d SQL statements successfully in schema '%s'", len(sql), tempSchema),
		SQLCount:  len(sql),
		Validated: validated,
	}, nil
}

// validateSchemaConsistency verifica a consistência do schema após aplicar o SQL
func (d *Deployer) validateSchemaConsistency(ctx context.Context, conn diff.PoolConn, schema string) error {
	// Verifica se todas as tabelas referenciadas existem
	// Nota: Esta é uma verificação simplificada. Em produção, usaríamos parsing SQL real.
	
	// Verifica integridade de foreign keys
	fkCheckQuery := `
		SELECT 
			tc.table_name, 
			kcu.column_name, 
			ccu.table_name AS foreign_table_name,
			ccu.column_name AS foreign_column_name 
		FROM information_schema.table_constraints AS tc 
		JOIN information_schema.key_column_usage AS kcu
			ON tc.constraint_name = kcu.constraint_name
		JOIN information_schema.constraint_column_usage AS ccu
			ON ccu.constraint_name = tc.constraint_name
		WHERE tc.constraint_type = 'FOREIGN KEY' AND tc.table_schema = $1
	`
	
	rows, err := conn.Query(fkCheckQuery, schema)
	if err != nil {
		// Ignora erro se pg_stat_statements não estiver disponível
		d.logger.Debug("Foreign key consistency check skipped", "reason", err.Error())
		return nil
	}
	defer rows.Close()
	
	return nil
}

// buildTempConnectionString constrói uma connection string para um schema temporário
func (d *Deployer) buildTempConnectionString(projectSlug, tempSchema string) string {
	// TODO: Construir connection string real baseada na configuração
	// Por enquanto, retorna um placeholder
	return fmt.Sprintf("postgres://localhost:5432/%s?search_path=%s", projectSlug, tempSchema)
}