package diff

import (
	"fmt"
	"strings"
)

// IndexesDiff implementa a fase de comparação de índices
// Responsável por detectar CREATE INDEX e DROP INDEX via pg_indexes
type IndexesDiff struct {
	ctx      DiffContext
	source   map[string]map[string]IndexInfo
	target   map[string]map[string]IndexInfo
	toCreate []IndexInfo
	toDrop   []IndexInfo
}

// Name retorna o identificador desta fase
func (i *IndexesDiff) Name() string {
	return "indexes"
}

// Introspect coleta metadados de índices dos dois ambientes
func (i *IndexesDiff) Introspect(ctx DiffContext) error {
	i.ctx = ctx
	i.source = make(map[string]map[string]IndexInfo)
	i.target = make(map[string]map[string]IndexInfo)
	i.toCreate = make([]IndexInfo, 0)
	i.toDrop = make([]IndexInfo, 0)

	// Coleta índices do ambiente de origem
	sourceConn, err := ctx.PoolProvider.AcquireForProject(ctx.ProjectSlug, ctx.SourceBranch)
	if err != nil {
		return fmt.Errorf("failed to acquire source connection: %w", err)
	}
	defer sourceConn.Close()

	if err := i.collectIndexes(sourceConn, i.source); err != nil {
		return fmt.Errorf("failed to collect source indexes: %w", err)
	}

	// Coleta índices do ambiente de destino
	targetConn, err := ctx.PoolProvider.AcquireForProject(ctx.ProjectSlug, ctx.TargetBranch)
	if err != nil {
		return fmt.Errorf("failed to acquire target connection: %w", err)
	}
	defer targetConn.Close()

	if err := i.collectIndexes(targetConn, i.target); err != nil {
		return fmt.Errorf("failed to collect target indexes: %w", err)
	}

	// Compara os dois conjuntos
	i.computeDiff()

	return nil
}

// collectIndexes coleta informações de todos os índices não-primários
func (i *IndexesDiff) collectIndexes(conn PoolConn, result map[string]map[string]IndexInfo) error {
	query := `
		SELECT 
			i.relname as indexname,
			pg_get_indexdef(i.oid) as indexdef,
			idx.indisunique,
			idx.indisprimary
		FROM pg_index idx
		JOIN pg_class i ON i.oid = idx.indexrelid
		JOIN pg_class t ON t.oid = idx.indrelid
		JOIN pg_namespace n ON n.oid = i.relnamespace
		WHERE n.nspname = 'public'
			AND NOT idx.indisprimary
		ORDER BY t.relname, i.relname
	`

	rows, err := conn.Query(query)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var indexName, indexDef string
		var isUnique, isPrimary bool

		if err := rows.Scan(&indexName, &indexDef, &isUnique, &isPrimary); err != nil {
			// Fallback: tentamos sem as colunas extras se o scan falhar
			if err := rows.Scan(&indexName, &indexDef); err != nil {
				return err
			}
			isUnique = false
			isPrimary = false
		}

		// Extrai o nome da tabela do indexdef
		tableName := i.extractTableNameFromIndexDef(indexDef)
		if tableName == "" {
			continue
		}

		if result[tableName] == nil {
			result[tableName] = make(map[string]IndexInfo)
		}

		result[tableName][indexName] = IndexInfo{
			IndexName: indexName,
			TableName: tableName,
			IsUnique:  isUnique,
			IsPrimary: isPrimary,
			IndexDef:  indexDef,
		}
	}

	return rows.Err()
}

// extractTableNameFromIndexDef extrai o nome da tabela de um CREATE INDEX statement
func (i *IndexesDiff) extractTableNameFromIndexDef(indexDef string) string {
	// Pattern: CREATE [UNIQUE] INDEX name ON table (...)
	parts := strings.Fields(indexDef)
	for i, part := range parts {
		if strings.ToUpper(part) == "ON" && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return ""
}

// computeDiff calcula as diferenças entre source e target
func (i *IndexesDiff) computeDiff() {
	// Para cada tabela no source
	for tableName, sourceIndexes := range i.source {
		targetIndexes := i.target[tableName]

		// Se a tabela não existe no target, consideramos map vazio
		if targetIndexes == nil {
			targetIndexes = make(map[string]IndexInfo)
		}

		for indexName, sourceIdx := range sourceIndexes {
			targetIdx, exists := targetIndexes[indexName]

			if !exists {
				// Índice existe no source mas não no target = CREATE
				i.toCreate = append(i.toCreate, sourceIdx)
			} else if !i.indexesEqual(sourceIdx, targetIdx) {
				// Índice existe em ambos mas com definições diferentes
				// DROP + CREATE (ALTER INDEX não suporta mudança de colunas)
				i.toDrop = append(i.toDrop, targetIdx)
				i.toCreate = append(i.toCreate, sourceIdx)
			}
		}

		// Índices que existem no target mas não no source = DROP
		for indexName, targetIdx := range targetIndexes {
			if _, exists := sourceIndexes[indexName]; !exists {
				i.toDrop = append(i.toDrop, targetIdx)
			}
		}
	}
}

// indexesEqual compara dois índices
func (i *IndexesDiff) indexesEqual(a, b IndexInfo) bool {
	// Comparação simplificada - em produção pode normalizar o indexdef
	return a.IndexName == b.IndexName &&
		a.IsUnique == b.IsUnique &&
		a.IndexDef == b.IndexDef
}

// GenerateSQL gera os statements SQL para criar/remover índices
func (i *IndexesDiff) GenerateSQL() []string {
	sql := make([]string, 0)

	// DROP INDEX
	for _, idx := range i.toDrop {
		sql = append(sql, fmt.Sprintf("DROP INDEX IF EXISTS public.%s;", idx.IndexName))
	}

	// CREATE INDEX
	for _, idx := range i.toCreate {
		def := idx.IndexDef
		def = strings.Replace(def, "CREATE INDEX", "CREATE INDEX IF NOT EXISTS", 1)
		def = strings.Replace(def, "CREATE UNIQUE INDEX", "CREATE UNIQUE INDEX IF NOT EXISTS", 1)
		sql = append(sql, def+";")
	}

	return sql
}

// Summary retorna um resumo das mudanças desta fase
func (i *IndexesDiff) Summary() PhaseSummary {
	details := make([]string, 0)

	if len(i.toCreate) > 0 {
		details = append(details, fmt.Sprintf("Create indexes: %d", len(i.toCreate)))
	}

	if len(i.toDrop) > 0 {
		details = append(details, fmt.Sprintf("Drop indexes: %d", len(i.toDrop)))
	}

	return PhaseSummary{
		PhaseName: i.Name(),
		Changes:   len(i.toCreate) + len(i.toDrop),
		SQL:       i.GenerateSQL(),
		Details:   details,
	}
}
