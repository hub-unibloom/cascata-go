package diff

import (
	"fmt"
	"strings"
)

// FunctionsDiff implementa a fase de comparação de funções/RPCs
// Responsável por detectar CREATE OR REPLACE FUNCTION via pg_proc
type FunctionsDiff struct {
	ctx      DiffContext
	source   map[string]FunctionInfo
	target   map[string]FunctionInfo
	toCreate []FunctionInfo
	toUpdate []FunctionInfo
	toDrop   []FunctionInfo
}

// Name retorna o identificador desta fase
func (f *FunctionsDiff) Name() string {
	return "functions"
}

// Introspect coleta metadados de funções dos dois ambientes
func (f *FunctionsDiff) Introspect(ctx DiffContext) error {
	f.ctx = ctx
	f.source = make(map[string]FunctionInfo)
	f.target = make(map[string]FunctionInfo)
	f.toCreate = make([]FunctionInfo, 0)
	f.toUpdate = make([]FunctionInfo, 0)
	f.toDrop = make([]FunctionInfo, 0)

	// Coleta funções do ambiente de origem
	sourceConn, err := ctx.PoolProvider.AcquireForProject(ctx.ProjectSlug, ctx.SourceBranch)
	if err != nil {
		return fmt.Errorf("failed to acquire source connection: %w", err)
	}
	defer sourceConn.Close()

	if err := f.collectFunctions(sourceConn, f.source); err != nil {
		return fmt.Errorf("failed to collect source functions: %w", err)
	}

	// Coleta funções do ambiente de destino
	targetConn, err := ctx.PoolProvider.AcquireForProject(ctx.ProjectSlug, ctx.TargetBranch)
	if err != nil {
		return fmt.Errorf("failed to acquire target connection: %w", err)
	}
	defer targetConn.Close()

	if err := f.collectFunctions(targetConn, f.target); err != nil {
		return fmt.Errorf("failed to collect target functions: %w", err)
	}

	// Compara os dois conjuntos
	f.computeDiff()

	return nil
}

// collectFunctions coleta informações de todas as funções do schema public
func (f *FunctionsDiff) collectFunctions(conn PoolConn, result map[string]FunctionInfo) error {
	query := `
		SELECT 
			p.proname as function_name,
			n.nspname as schema_name,
			pg_get_function_arguments(p.oid) as argument_types,
			pg_get_function_result(p.oid) as return_type,
			pg_get_functiondef(p.oid) as function_body,
			l.lanname as language
		FROM pg_proc p
		JOIN pg_namespace n ON p.pronamespace = n.oid
		JOIN pg_language l ON p.prolang = l.oid
		WHERE n.nspname = 'public'
			AND p.prokind = 'f'  -- Apenas funções, não procedures
		ORDER BY p.proname
	`

	rows, err := conn.Query(query)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var functionName, schemaName, argumentTypes, returnType, functionBody, language string

		if err := rows.Scan(&functionName, &schemaName, &argumentTypes, &returnType, &functionBody, &language); err != nil {
			return err
		}

		// Cria uma chave única para a função (nome + argumentos)
		key := f.functionKey(functionName, argumentTypes)

		result[key] = FunctionInfo{
			FunctionName:   functionName,
			Schema:        schemaName,
			ArgumentTypes: strings.Split(argumentTypes, ", "),
			ReturnType:    returnType,
			FunctionBody:  functionBody,
			Language:      language,
		}
	}

	return rows.Err()
}

// functionKey cria uma chave única para uma função baseada no nome e argumentos
func (f *FunctionsDiff) functionKey(name, args string) string {
	return fmt.Sprintf("%s(%s)", name, args)
}

// computeDiff calcula as diferenças entre source e target
func (f *FunctionsDiff) computeDiff() {
	for key, sourceFunc := range f.source {
		targetFunc, exists := f.target[key]

		if !exists {
			// Função existe no source mas não no target = CREATE
			f.toCreate = append(f.toCreate, sourceFunc)
		} else if !f.functionsEqual(sourceFunc, targetFunc) {
			// Função existe em ambos mas com definições diferentes = UPDATE
			// Usamos CREATE OR REPLACE FUNCTION para atualizar
			f.toUpdate = append(f.toUpdate, sourceFunc)
		}
	}

	// Funções que existem no target mas não no source = DROP
	for key, targetFunc := range f.target {
		if _, exists := f.source[key]; !exists {
			f.toDrop = append(f.toDrop, targetFunc)
		}
	}
}

// functionsEqual compara duas funções
func (f *FunctionsDiff) functionsEqual(a, b FunctionInfo) bool {
	// Comparação simplificada - em produção pode normalizar o functionBody
	return a.FunctionName == b.FunctionName &&
		a.Schema == b.Schema &&
		a.ReturnType == b.ReturnType &&
		a.Language == b.Language &&
		a.FunctionBody == b.FunctionBody
}

// GenerateSQL gera os statements SQL para criar/atualizar/remover funções
func (f *FunctionsDiff) GenerateSQL() []string {
	sql := make([]string, 0)

	// DROP FUNCTION
	for _, funcInfo := range f.toDrop {
		args := strings.Join(funcInfo.ArgumentTypes, ", ")
		sql = append(sql, fmt.Sprintf("DROP FUNCTION IF EXISTS public.%s(%s) CASCADE;", funcInfo.FunctionName, args))
	}

	// CREATE OR REPLACE FUNCTION (para novas e atualizações)
	for _, funcInfo := range f.toCreate {
		sql = append(sql, funcInfo.FunctionBody)
	}

	for _, funcInfo := range f.toUpdate {
		sql = append(sql, funcInfo.FunctionBody)
	}

	return sql
}

// Summary retorna um resumo das mudanças desta fase
func (f *FunctionsDiff) Summary() PhaseSummary {
	details := make([]string, 0)

	if len(f.toCreate) > 0 {
		details = append(details, fmt.Sprintf("Create functions: %d", len(f.toCreate)))
	}

	if len(f.toUpdate) > 0 {
		details = append(details, fmt.Sprintf("Update functions: %d", len(f.toUpdate)))
	}

	if len(f.toDrop) > 0 {
		details = append(details, fmt.Sprintf("Drop functions: %d", len(f.toDrop)))
	}

	return PhaseSummary{
		PhaseName: f.Name(),
		Changes:   len(f.toCreate) + len(f.toUpdate) + len(f.toDrop),
		SQL:       f.GenerateSQL(),
		Details:   details,
	}
}
