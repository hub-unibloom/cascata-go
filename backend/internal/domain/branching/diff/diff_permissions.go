package diff

import (
	"fmt"
)

// PermissionsDiff implementa a fase de comparação de permissões
// Responsável por detectar GRANT para anon/authenticated em novos objetos
type PermissionsDiff struct {
	ctx    DiffContext
	grants []string
}

// Name retorna o identificador desta fase
func (p *PermissionsDiff) Name() string {
	return "permissions"
}

// Introspect coleta metadados de permissões dos dois ambientes
func (p *PermissionsDiff) Introspect(ctx DiffContext) error {
	p.ctx = ctx
	p.grants = make([]string, 0)

	// Coleta objetos do ambiente de destino (main)
	// Precisamos garantir que novos objetos criados pelo diff tenham as permissões corretas
	targetConn, err := ctx.PoolProvider.AcquireForProject(ctx.ProjectSlug, ctx.TargetBranch)
	if err != nil {
		return fmt.Errorf("failed to acquire target connection: %w", err)
	}
	defer targetConn.Close()

	// Coleta tabelas que precisam de GRANT
	if err := p.collectTableGrants(targetConn); err != nil {
		return fmt.Errorf("failed to collect table grants: %w", err)
	}

	// Coleta funções que precisam de GRANT
	if err := p.collectFunctionGrants(targetConn); err != nil {
		return fmt.Errorf("failed to collect function grants: %w", err)
	}

	return nil
}

// collectTableGrants coleta GRANT statements para tabelas
func (p *PermissionsDiff) collectTableGrants(conn PoolConn) error {
	query := `
		SELECT 
			table_name
		FROM information_schema.tables
		WHERE table_schema = 'public'
			AND table_type = 'BASE TABLE'
		ORDER BY table_name
	`

	rows, err := conn.Query(query)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var tableName string

		if err := rows.Scan(&tableName); err != nil {
			return err
		}

		// Gera GRANT statements para tabelas
		// anon: SELECT
		p.grants = append(p.grants, fmt.Sprintf("GRANT SELECT ON TABLE public.%s TO anon;", tableName))
		// authenticated: SELECT, INSERT, UPDATE, DELETE
		p.grants = append(p.grants, fmt.Sprintf("GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE public.%s TO authenticated;", tableName))
	}

	return rows.Err()
}

// collectFunctionGrants coleta GRANT statements para funções
func (p *PermissionsDiff) collectFunctionGrants(conn PoolConn) error {
	query := `
		SELECT 
			routine_name
		FROM information_schema.routines
		WHERE routine_schema = 'public'
			AND routine_type = 'FUNCTION'
		ORDER BY routine_name
	`

	rows, err := conn.Query(query)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var functionName string

		if err := rows.Scan(&functionName); err != nil {
			return err
		}

		// Gera GRANT statements para funções
		// anon: EXECUTE
		p.grants = append(p.grants, fmt.Sprintf("GRANT EXECUTE ON FUNCTION public.%s TO anon;", functionName))
		// authenticated: EXECUTE
		p.grants = append(p.grants, fmt.Sprintf("GRANT EXECUTE ON FUNCTION public.%s TO authenticated;", functionName))
	}

	return rows.Err()
}

// GenerateSQL gera os statements SQL para conceder permissões
func (p *PermissionsDiff) GenerateSQL() []string {
	return p.grants
}

// Summary retorna um resumo das mudanças desta fase
func (p *PermissionsDiff) Summary() PhaseSummary {
	details := make([]string, 0)
	
	if len(p.grants) > 0 {
		details = append(details, fmt.Sprintf("Grant permissions: %d statements", len(p.grants)))
	}

	return PhaseSummary{
		PhaseName: p.Name(),
		Changes:   len(p.grants),
		SQL:       p.GenerateSQL(),
		Details:   details,
	}
}
