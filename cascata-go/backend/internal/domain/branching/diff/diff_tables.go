package diff

import (
	"fmt"
	"strings"
)

// TablesDiff implementa a fase de comparação de tabelas
// Responsável por detectar CREATE TABLE e DROP TABLE
type TablesDiff struct {
	ctx      DiffContext
	source   map[string]TableInfo
	target   map[string]TableInfo
	toCreate []string
	toDrop   []string
}

// Name retorna o identificador desta fase
func (t *TablesDiff) Name() string {
	return "tables"
}

// Introspect coleta metadados de tabelas dos dois ambientes
// GAP #4 FIX: Usa conexões pré-adquiridas do DiffContext em vez de adquirir novas
func (t *TablesDiff) Introspect(ctx DiffContext) error {
	t.ctx = ctx
	t.source = make(map[string]TableInfo)
	t.target = make(map[string]TableInfo)
	t.toCreate = make([]string, 0)
	t.toDrop = make([]string, 0)

	// GAP #4 FIX: Usa as conexões já adquiridas no contexto
	// Em vez de chamar AcquireForProject para cada fase
	var sourceConn, targetConn PoolConn
	var err error

	if ctx.SourceConn != nil {
		// Conexão já foi adquirida no DiffEngine.Run()
		sourceConn = ctx.SourceConn
	} else {
		// Fallback para acquire individual (caso o contexto não tenha conexões pré-adquiridas)
		sourceConn, err = ctx.PoolProvider.AcquireForProject(ctx.ProjectSlug, ctx.SourceBranch)
		if err != nil {
			return fmt.Errorf("failed to acquire source connection: %w", err)
		}
		defer sourceConn.Close()
	}

	if ctx.TargetConn != nil {
		targetConn = ctx.TargetConn
	} else {
		targetConn, err = ctx.PoolProvider.AcquireForProject(ctx.ProjectSlug, ctx.TargetBranch)
		if err != nil {
			return fmt.Errorf("failed to acquire target connection: %w", err)
		}
		defer targetConn.Close()
	}

	// Coleta tabelas do ambiente de origem (branch)
	if err := t.collectTables(sourceConn, t.source); err != nil {
		return fmt.Errorf("failed to collect source tables: %w", err)
	}

	// Coleta tabelas do ambiente de destino (main)
	if err := t.collectTables(targetConn, t.target); err != nil {
		return fmt.Errorf("failed to collect target tables: %w", err)
	}

	// Compara os dois conjuntos
	t.computeDiff()

	return nil
}

// collectTables coleta informações de todas as tabelas do schema public
func (t *TablesDiff) collectTables(conn PoolConn, result map[string]TableInfo) error {
	query := `
		SELECT 
			table_name,
			column_name,
			data_type,
			is_nullable,
			column_default,
			ordinal_position
		FROM information_schema.columns
		WHERE table_schema = 'public'
		ORDER BY table_name, ordinal_position
	`

	rows, err := conn.Query(query)
	if err != nil {
		return err
	}
	defer rows.Close()

	// Agrupa colunas por tabela
	currentTable := ""
	var tableInfo TableInfo

	for rows.Next() {
		var tableName, columnName, dataType, isNullable string
		var columnDefault *string
		var ordinalPosition int

		if err := rows.Scan(&tableName, &columnName, &dataType, &isNullable, &columnDefault, &ordinalPosition); err != nil {
			return err
		}

		if tableName != currentTable {
			// Nova tabela
			if currentTable != "" {
				result[currentTable] = tableInfo
			}
			currentTable = tableName
			tableInfo = TableInfo{
				Schema:    "public",
				TableName: tableName,
				Columns:   make([]ColumnInfo, 0),
			}
		}

		defaultVal := ""
		if columnDefault != nil {
			defaultVal = *columnDefault
		}

		tableInfo.Columns = append(tableInfo.Columns, ColumnInfo{
			Name:         columnName,
			Type:         dataType,
			Nullable:     isNullable == "YES",
			DefaultValue: defaultVal,
		})
	}

	// Adiciona a última tabela
	if currentTable != "" {
		result[currentTable] = tableInfo
	}

	return rows.Err()
}

// computeDiff calcula as diferenças entre source e target
func (t *TablesDiff) computeDiff() {
	// Tabelas que existem no source mas não no target = CREATE
	for tableName := range t.source {
		if _, exists := t.target[tableName]; !exists {
			t.toCreate = append(t.toCreate, tableName)
		}
	}

	// Tabelas que existem no target mas não no source = DROP
	for tableName := range t.target {
		if _, exists := t.source[tableName]; !exists {
			t.toDrop = append(t.toDrop, tableName)
		}
	}
}

// GenerateSQL gera os statements SQL para criar/dropar tabelas
func (t *TablesDiff) GenerateSQL() []string {
	sql := make([]string, 0)

	// DROP TABLE (primeiro para evitar conflitos de FK)
	for _, tableName := range t.toDrop {
		sql = append(sql, fmt.Sprintf("DROP TABLE IF EXISTS public.%s CASCADE;", tableName))
	}

	// CREATE TABLE
	for _, tableName := range t.toCreate {
		tableInfo := t.source[tableName]
		createStmt := t.generateCreateTable(tableInfo)
		sql = append(sql, createStmt)
	}

	return sql
}

// generateCreateTable gera um CREATE TABLE statement completo
func (t *TablesDiff) generateCreateTable(table TableInfo) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("CREATE TABLE IF NOT EXISTS public.%s (\n", table.TableName))

	columnDefs := make([]string, 0, len(table.Columns))
	for _, col := range table.Columns {
		colDef := fmt.Sprintf("    %s %s", col.Name, col.Type)

		if !col.Nullable {
			colDef += " NOT NULL"
		} else {
			colDef += " NULL"
		}

		if col.DefaultValue != "" {
			colDef += fmt.Sprintf(" DEFAULT %s", col.DefaultValue)
		}

		columnDefs = append(columnDefs, colDef)
	}

	sb.WriteString(strings.Join(columnDefs, ",\n"))
	sb.WriteString("\n);")

	return sb.String()
}

// Summary retorna um resumo das mudanças desta fase
func (t *TablesDiff) Summary() PhaseSummary {
	details := make([]string, 0)

	if len(t.toCreate) > 0 {
		details = append(details, fmt.Sprintf("Create tables: %s", strings.Join(t.toCreate, ", ")))
	}

	if len(t.toDrop) > 0 {
		details = append(details, fmt.Sprintf("Drop tables: %s", strings.Join(t.toDrop, ", ")))
	}

	return PhaseSummary{
		PhaseName: t.Name(),
		Changes:   len(t.toCreate) + len(t.toDrop),
		SQL:       t.GenerateSQL(),
		Details:   details,
	}
}