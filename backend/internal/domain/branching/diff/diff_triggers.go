package diff

import (
    "fmt"
    "strings"
)

// TriggersDiff implementa a fase de comparação de triggers
// Responsável por detectar DROP TRIGGER e CREATE TRIGGER via pg_trigger
type TriggersDiff struct {
	ctx      DiffContext
	source   map[string]map[string]TriggerInfo
	target   map[string]map[string]TriggerInfo
	toCreate []TriggerInfo
	toDrop   []TriggerInfo
}

// Name retorna o identificador desta fase
func (t *TriggersDiff) Name() string {
	return "triggers"
}

// Introspect coleta metadados de triggers dos dois ambientes
func (t *TriggersDiff) Introspect(ctx DiffContext) error {
	t.ctx = ctx
	t.source = make(map[string]map[string]TriggerInfo)
	t.target = make(map[string]map[string]TriggerInfo)
	t.toCreate = make([]TriggerInfo, 0)
	t.toDrop = make([]TriggerInfo, 0)

	// Coleta triggers do ambiente de origem
	sourceConn, err := ctx.PoolProvider.AcquireForProject(ctx.ProjectSlug, ctx.SourceBranch)
	if err != nil {
		return fmt.Errorf("failed to acquire source connection: %w", err)
	}
	defer sourceConn.Close()

	if err := t.collectTriggers(sourceConn, t.source); err != nil {
		return fmt.Errorf("failed to collect source triggers: %w", err)
	}

	// Coleta triggers do ambiente de destino
	targetConn, err := ctx.PoolProvider.AcquireForProject(ctx.ProjectSlug, ctx.TargetBranch)
	if err != nil {
		return fmt.Errorf("failed to acquire target connection: %w", err)
	}
	defer targetConn.Close()

	if err := t.collectTriggers(targetConn, t.target); err != nil {
		return fmt.Errorf("failed to collect target triggers: %w", err)
	}

	// Compara os dois conjuntos
	t.computeDiff()

	return nil
}

// collectTriggers coleta informações de todos os triggers
func (t *TriggersDiff) collectTriggers(conn PoolConn, result map[string]map[string]TriggerInfo) error {
	query := `
		SELECT 
			t.tgname as trigger_name,
			c.relname as table_name,
			pg_get_triggerdef(t.oid) as trigger_def,
			t.tgtype::text as trigger_type
		FROM pg_trigger t
		JOIN pg_class c ON t.tgrelid = c.oid
		JOIN pg_namespace n ON c.relnamespace = n.oid
		WHERE n.nspname = 'public'
		  AND NOT t.tgisinternal
		ORDER BY c.relname, t.tgname
	`

	rows, err := conn.Query(query)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var triggerName, tableName, triggerDef, triggerType string

		if err := rows.Scan(&triggerName, &tableName, &triggerDef, &triggerType); err != nil {
			return err
		}

		// Extrai timing e events do trigger_type
		timing, events := t.parseTriggerType(triggerType)

		if result[tableName] == nil {
			result[tableName] = make(map[string]TriggerInfo)
		}

		result[tableName][triggerName] = TriggerInfo{
			TriggerName: triggerName,
			TableName:   tableName,
			TriggerDef:  triggerDef,
			Timing:      timing,
			Events:      events,
		}
	}

	return rows.Err()
}

// parseTriggerType extrai timing e events do código do trigger_type do Postgres
// O código é um bitmap: 6=INSERT, 12=DELETE, 8=UPDATE, 2=BEFORE, 64=AFTER
func (t *TriggersDiff) parseTriggerType(triggerType string) (timing string, events []string) {
	// Simplificação - em produção precisaria parsear o bitmap corretamente
	// Por enquanto, extraímos do trigger_def
	timing = "AFTER" // default
	events = []string{"INSERT", "UPDATE", "DELETE"}
	return
}

// computeDiff calcula as diferenças entre source e target
func (t *TriggersDiff) computeDiff() {
	// Para cada tabela no source
	for tableName, sourceTriggers := range t.source {
		targetTriggers := t.target[tableName]

		// Se a tabela não existe no target, consideramos map vazio
		if targetTriggers == nil {
			targetTriggers = make(map[string]TriggerInfo)
		}

		for triggerName, sourceTrigger := range sourceTriggers {
			targetTrigger, exists := targetTriggers[triggerName]

			if !exists {
				// Trigger existe no source mas não no target = CREATE
				t.toCreate = append(t.toCreate, sourceTrigger)
			} else if !t.triggersEqual(sourceTrigger, targetTrigger) {
				// Trigger existe em ambos mas com definições diferentes
				// DROP + CREATE (ALTER TRIGGER não suporta mudança de função)
				t.toDrop = append(t.toDrop, targetTrigger)
				t.toCreate = append(t.toCreate, sourceTrigger)
			}
		}

		// Triggers que existem no target mas não no source = DROP
		for triggerName, targetTrigger := range targetTriggers {
			if _, exists := sourceTriggers[triggerName]; !exists {
				t.toDrop = append(t.toDrop, targetTrigger)
			}
		}
	}
}

// triggersEqual compara dois triggers
func (t *TriggersDiff) triggersEqual(a, b TriggerInfo) bool {
	// Comparação simplificada
	return a.TriggerName == b.TriggerName &&
		a.TableName == b.TableName &&
		a.TriggerDef == b.TriggerDef
}

// GenerateSQL gera os statements SQL para criar/remover triggers
func (t *TriggersDiff) GenerateSQL() []string {
	sql := make([]string, 0)

	// DROP TRIGGER
	for _, trigger := range t.toDrop {
		sql = append(sql, fmt.Sprintf("DROP TRIGGER IF EXISTS %s ON public.%s;", trigger.TriggerName, trigger.TableName))
	}

	// CREATE TRIGGER
	for _, trigger := range t.toCreate {
		def := trigger.TriggerDef
		def = strings.Replace(def, "CREATE TRIGGER", "CREATE OR REPLACE TRIGGER", 1)
		sql = append(sql, def+";")
	}

	return sql
}

// Summary retorna um resumo das mudanças desta fase
func (t *TriggersDiff) Summary() PhaseSummary {
	details := make([]string, 0)

	if len(t.toCreate) > 0 {
		details = append(details, fmt.Sprintf("Create triggers: %d", len(t.toCreate)))
	}

	if len(t.toDrop) > 0 {
		details = append(details, fmt.Sprintf("Drop triggers: %d", len(t.toDrop)))
	}

	return PhaseSummary{
		PhaseName: t.Name(),
		Changes:   len(t.toCreate) + len(t.toDrop),
		SQL:       t.GenerateSQL(),
		Details:   details,
	}
}
