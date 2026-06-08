package diff

import (
	"fmt"
	"strings"
)

// RLSDiff implementa a fase de comparação de RLS policies
// Responsável por detectar DROP POLICY e CREATE POLICY via pg_policies
type RLSDiff struct {
	ctx      DiffContext
	source   map[string]map[string]PolicyInfo
	target   map[string]map[string]PolicyInfo
	toCreate []PolicyInfo
	toDrop   []PolicyInfo
}

// Name retorna o identificador desta fase
func (r *RLSDiff) Name() string {
	return "rls"
}

// Introspect coleta metadados de RLS policies dos dois ambientes
func (r *RLSDiff) Introspect(ctx DiffContext) error {
	r.ctx = ctx
	r.source = make(map[string]map[string]PolicyInfo)
	r.target = make(map[string]map[string]PolicyInfo)
	r.toCreate = make([]PolicyInfo, 0)
	r.toDrop = make([]PolicyInfo, 0)

	// Coleta policies do ambiente de origem
	sourceConn, err := ctx.PoolProvider.AcquireForProject(ctx.ProjectSlug, ctx.SourceBranch)
	if err != nil {
		return fmt.Errorf("failed to acquire source connection: %w", err)
	}
	defer sourceConn.Close()

	if err := r.collectPolicies(sourceConn, r.source); err != nil {
		return fmt.Errorf("failed to collect source policies: %w", err)
	}

	// Coleta policies do ambiente de destino
	targetConn, err := ctx.PoolProvider.AcquireForProject(ctx.ProjectSlug, ctx.TargetBranch)
	if err != nil {
		return fmt.Errorf("failed to acquire target connection: %w", err)
	}
	defer targetConn.Close()

	if err := r.collectPolicies(targetConn, r.target); err != nil {
		return fmt.Errorf("failed to collect target policies: %w", err)
	}

	// Compara os dois conjuntos
	r.computeDiff()

	return nil
}

// collectPolicies coleta informações de todas as RLS policies
func (r *RLSDiff) collectPolicies(conn PoolConn, result map[string]map[string]PolicyInfo) error {
	query := `
		SELECT 
			policyname,
			tablename,
			cmd as policycmd,
			qual,
			with_check,
			roles
		FROM pg_policies
		WHERE schemaname = 'public'
		ORDER BY tablename, policyname
	`

	rows, err := conn.Query(query)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var policyName, tableName, policyCmd string
		var usingExpr, withCheckExpr *string
		var roles []string

		if err := rows.Scan(&policyName, &tableName, &policyCmd, &usingExpr, &withCheckExpr, &roles); err != nil {
			return err
		}

		if result[tableName] == nil {
			result[tableName] = make(map[string]PolicyInfo)
		}

		uExpr := ""
		if usingExpr != nil { uExpr = *usingExpr }
		wExpr := ""
		if withCheckExpr != nil { wExpr = *withCheckExpr }

		result[tableName][policyName] = PolicyInfo{
			PolicyName:    policyName,
			TableName:     tableName,
			PolicyCmd:     policyCmd,
			UsingExpr:     uExpr,
			WithCheckExpr: wExpr,
			Roles:         roles,
		}
	}

	return rows.Err()
}

// computeDiff calcula as diferenças entre source e target
func (r *RLSDiff) computeDiff() {
	// Para cada tabela no source
	for tableName, sourcePolicies := range r.source {
		targetPolicies := r.target[tableName]

		// Se a tabela não existe no target, consideramos map vazio
		if targetPolicies == nil {
			targetPolicies = make(map[string]PolicyInfo)
		}

		for policyName, sourcePolicy := range sourcePolicies {
			targetPolicy, exists := targetPolicies[policyName]

			if !exists {
				// Policy existe no source mas não no target = CREATE
				r.toCreate = append(r.toCreate, sourcePolicy)
			} else if !r.policiesEqual(sourcePolicy, targetPolicy) {
				// Policy existe em ambos mas com definições diferentes
				// DROP + CREATE (ALTER POLICY não suporta mudança de expressão)
				r.toDrop = append(r.toDrop, targetPolicy)
				r.toCreate = append(r.toCreate, sourcePolicy)
			}
		}

		// Policies que existem no target mas não no source = DROP
		for policyName, targetPolicy := range targetPolicies {
			if _, exists := sourcePolicies[policyName]; !exists {
				r.toDrop = append(r.toDrop, targetPolicy)
			}
		}
	}
}

// policiesEqual compara duas policies
func (r *RLSDiff) policiesEqual(a, b PolicyInfo) bool {
	return a.PolicyName == b.PolicyName &&
		a.TableName == b.TableName &&
		a.PolicyCmd == b.PolicyCmd &&
		a.UsingExpr == b.UsingExpr &&
		a.WithCheckExpr == b.WithCheckExpr &&
		r.rolesEqual(a.Roles, b.Roles)
}

// rolesEqual compara arrays de roles
func (r *RLSDiff) rolesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// GenerateSQL gera os statements SQL para criar/remover policies
func (r *RLSDiff) GenerateSQL() []string {
	sql := make([]string, 0)

	// DROP POLICY
	for _, policy := range r.toDrop {
		sql = append(sql, fmt.Sprintf("DROP POLICY IF EXISTS %s ON public.%s;", policy.PolicyName, policy.TableName))
	}

	// CREATE POLICY
	for _, policy := range r.toCreate {
		sql = append(sql, r.generateCreatePolicy(policy))
	}

	return sql
}

// generateCreatePolicy gera um CREATE POLICY statement
func (r *RLSDiff) generateCreatePolicy(policy PolicyInfo) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("CREATE POLICY %s ON public.%s", policy.PolicyName, policy.TableName))

	// FOR command
	sb.WriteString(fmt.Sprintf(" FOR %s", policy.PolicyCmd))

	// TO roles
	if len(policy.Roles) > 0 {
		sb.WriteString(fmt.Sprintf(" TO %s", strings.Join(policy.Roles, ", ")))
	} else {
		sb.WriteString(" TO public")
	}

	// USING expression
	if policy.UsingExpr != "" {
		sb.WriteString(fmt.Sprintf(" USING (%s)", policy.UsingExpr))
	}

	// WITH CHECK expression
	if policy.WithCheckExpr != "" {
		sb.WriteString(fmt.Sprintf(" WITH CHECK (%s)", policy.WithCheckExpr))
	}

	sb.WriteString(";")

	return sb.String()
}

// Summary retorna um resumo das mudanças desta fase
func (r *RLSDiff) Summary() PhaseSummary {
	details := make([]string, 0)

	if len(r.toCreate) > 0 {
		details = append(details, fmt.Sprintf("Create policies: %d", len(r.toCreate)))
	}

	if len(r.toDrop) > 0 {
		details = append(details, fmt.Sprintf("Drop policies: %d", len(r.toDrop)))
	}

	return PhaseSummary{
		PhaseName: r.Name(),
		Changes:   len(r.toCreate) + len(r.toDrop),
		SQL:       r.GenerateSQL(),
		Details:   details,
	}
}
