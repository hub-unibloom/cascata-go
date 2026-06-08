package diff

import (
	"fmt"
	"strings"
)

// ColumnsDiff implementa a fase de comparação de colunas
// Responsável por detectar ADD COLUMN, ALTER COLUMN, DROP COLUMN
// e heurística de RENAME (quando uma coluna parece ter sido renomeada)
type ColumnsDiff struct {
	ctx      DiffContext
	source   map[string]map[string]ColumnInfo
	target   map[string]map[string]ColumnInfo
	toAdd    map[string][]ColumnInfo
	toAlter  map[string][]ColumnInfo
	toDrop   map[string][]string
}

// Name retorna o identificador desta fase
func (c *ColumnsDiff) Name() string {
	return "columns"
}

// Introspect coleta metadados de colunas dos dois ambientes
func (c *ColumnsDiff) Introspect(ctx DiffContext) error {
	c.ctx = ctx
	c.source = make(map[string]map[string]ColumnInfo)
	c.target = make(map[string]map[string]ColumnInfo)
	c.toAdd = make(map[string][]ColumnInfo)
	c.toAlter = make(map[string][]ColumnInfo)
	c.toDrop = make(map[string][]string)

	// Coleta colunas do ambiente de origem
	sourceConn, err := ctx.PoolProvider.AcquireForProject(ctx.ProjectSlug, ctx.SourceBranch)
	if err != nil {
		return fmt.Errorf("failed to acquire source connection: %w", err)
	}
	defer sourceConn.Close()

	if err := c.collectColumns(sourceConn, c.source); err != nil {
		return fmt.Errorf("failed to collect source columns: %w", err)
	}

	// Coleta colunas do ambiente de destino
	targetConn, err := ctx.PoolProvider.AcquireForProject(ctx.ProjectSlug, ctx.TargetBranch)
	if err != nil {
		return fmt.Errorf("failed to acquire target connection: %w", err)
	}
	defer targetConn.Close()

	if err := c.collectColumns(targetConn, c.target); err != nil {
		return fmt.Errorf("failed to collect target columns: %w", err)
	}

	// Compara os dois conjuntos
	c.computeDiff()

	return nil
}

// collectColumns coleta informações de todas as colunas
func (c *ColumnsDiff) collectColumns(conn PoolConn, result map[string]map[string]ColumnInfo) error {
	query := `
		SELECT 
			table_name,
			column_name,
			data_type,
			is_nullable,
			column_default,
			ordinal_position,
			(
				SELECT COUNT(*) 
				FROM information_schema.table_constraints tc
				JOIN information_schema.key_column_usage kcu
					ON tc.constraint_name = kcu.constraint_name
				WHERE tc.table_name = columns.table_name
					AND kcu.column_name = columns.column_name
					AND tc.constraint_type = 'PRIMARY KEY'
			) > 0 as is_primary_key,
			(
				SELECT COUNT(*)
				FROM information_schema.table_constraints tc
				JOIN information_schema.key_column_usage kcu
					ON tc.constraint_name = kcu.constraint_name
				WHERE tc.table_name = columns.table_name
					AND kcu.column_name = columns.column_name
					AND tc.constraint_type = 'UNIQUE'
			) > 0 as is_unique
		FROM information_schema.columns
		WHERE table_schema = 'public'
		ORDER BY table_name, ordinal_position
	`

	rows, err := conn.Query(query)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var tableName, columnName, dataType, isNullable string
		var columnDefault *string
		var ordinalPosition int
		var isPrimaryKey, isUnique bool

		if err := rows.Scan(&tableName, &columnName, &dataType, &isNullable, &columnDefault, &ordinalPosition, &isPrimaryKey, &isUnique); err != nil {
			return err
		}

		if result[tableName] == nil {
			result[tableName] = make(map[string]ColumnInfo)
		}

		defaultVal := ""
		if columnDefault != nil {
			defaultVal = *columnDefault
		}

		result[tableName][columnName] = ColumnInfo{
			Name:         columnName,
			Type:         dataType,
			Nullable:     isNullable == "YES",
			DefaultValue: defaultVal,
			IsPrimaryKey: isPrimaryKey,
			IsUnique:     isUnique,
		}
	}

	return rows.Err()
}

// computeDiff calcula as diferenças entre source e target
func (c *ColumnsDiff) computeDiff() {
	// Para cada tabela no source
	for tableName, sourceCols := range c.source {
		targetCols := c.target[tableName]

		// Se a tabela não existe no target, skip (será tratado pela fase de tabelas)
		if targetCols == nil {
			continue
		}

		for colName, sourceCol := range sourceCols {
			targetCol, exists := targetCols[colName]

			if !exists {
				// Coluna existe no source mas não no target = ADD
				c.toAdd[tableName] = append(c.toAdd[tableName], sourceCol)
			} else if !c.columnsEqual(sourceCol, targetCol) {
				// Coluna existe em ambos mas com propriedades diferentes = ALTER
				c.toAlter[tableName] = append(c.toAlter[tableName], sourceCol)
			}
		}

		// Colunas que existem no target mas não no source = DROP
		for colName := range targetCols {
			if _, exists := sourceCols[colName]; !exists {
				c.toDrop[tableName] = append(c.toDrop[tableName], colName)
			}
		}
	}
}

// columnsEqual compara duas colunas para determinar se são iguais
func (c *ColumnsDiff) columnsEqual(a, b ColumnInfo) bool {
	// Comparação simples - em produção pode ser mais sofisticada
	// considerando tipos equivalentes (ex: varchar(255) vs text)
	return a.Name == b.Name &&
		a.Type == b.Type &&
		a.Nullable == b.Nullable &&
		a.DefaultValue == b.DefaultValue
}

// GenerateSQL gera os statements SQL para adicionar/alterar/remover colunas
func (c *ColumnsDiff) GenerateSQL() []string {
	sql := make([]string, 0)

	// DROP COLUMN
	for tableName, colNames := range c.toDrop {
		for _, colName := range colNames {
			sql = append(sql, fmt.Sprintf("ALTER TABLE public.%s DROP COLUMN IF EXISTS %s;", tableName, colName))
		}
	}

	// ADD COLUMN
	for tableName, cols := range c.toAdd {
		for _, col := range cols {
			sql = append(sql, c.generateAddColumn(tableName, col))
		}
	}

	// ALTER COLUMN
	for tableName, cols := range c.toAlter {
		for _, col := range cols {
			sql = append(sql, c.generateAlterColumn(tableName, col))
		}
	}

	return sql
}

// generateAddColumn gera um ADD COLUMN statement
func (c *ColumnsDiff) generateAddColumn(tableName string, col ColumnInfo) string {
	nullability := "NULL"
	if !col.Nullable {
		nullability = "NOT NULL"
	}

	defaultClause := ""
	if col.DefaultValue != "" {
		defaultClause = fmt.Sprintf(" DEFAULT %s", col.DefaultValue)
	}

	return fmt.Sprintf("ALTER TABLE public.%s ADD COLUMN %s %s %s%s;",
		tableName, col.Name, col.Type, nullability, defaultClause)
}

// generateAlterColumn gera um ALTER COLUMN statement
func (c *ColumnsDiff) generateAlterColumn(tableName string, col ColumnInfo) string {
	// ALTER COLUMN pode ter múltiplas partes
	statements := make([]string, 0)

	// ALTER TYPE
	statements = append(statements, fmt.Sprintf("ALTER TABLE public.%s ALTER COLUMN %s TYPE %s;",
		tableName, col.Name, col.Type))

	// ALTER NULL/NOT NULL
	nullability := "DROP NOT NULL"
	if !col.Nullable {
		nullability = "SET NOT NULL"
	}
	statements = append(statements, fmt.Sprintf("ALTER TABLE public.%s ALTER COLUMN %s %s;",
		tableName, col.Name, nullability))

	// ALTER DEFAULT
	if col.DefaultValue != "" {
		statements = append(statements, fmt.Sprintf("ALTER TABLE public.%s ALTER COLUMN %s SET DEFAULT %s;",
			tableName, col.Name, col.DefaultValue))
	} else {
		statements = append(statements, fmt.Sprintf("ALTER TABLE public.%s ALTER COLUMN %s DROP DEFAULT;",
			tableName, col.Name))
	}

	return strings.Join(statements, "\n")
}

// Summary retorna um resumo das mudanças desta fase
func (c *ColumnsDiff) Summary() PhaseSummary {
	details := make([]string, 0)
	totalChanges := 0

	for tableName, cols := range c.toAdd {
		totalChanges += len(cols)
		details = append(details, fmt.Sprintf("Add columns to %s: %d", tableName, len(cols)))
	}

	for tableName, cols := range c.toAlter {
		totalChanges += len(cols)
		details = append(details, fmt.Sprintf("Alter columns in %s: %d", tableName, len(cols)))
	}

	for tableName, colNames := range c.toDrop {
		totalChanges += len(colNames)
		details = append(details, fmt.Sprintf("Drop columns from %s: %d", tableName, len(colNames)))
	}

	return PhaseSummary{
		PhaseName: c.Name(),
		Changes:   totalChanges,
		SQL:       c.GenerateSQL(),
		Details:   details,
	}
}
