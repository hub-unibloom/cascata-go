package nexus

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ============================================================================
// DATA COMPONENT — Operações de Banco de Dados (No-Code + Raw SQL)
// ============================================================================
// Suporta dois modos de operação:
//   - No-Code: Frontend envia operation/schema/table/mapping/filterRows
//     e o componente constrói SQL seguro com queries parametrizadas.
//   - Raw SQL (Code): Frontend envia query string com interpolação {{...}}.
//
// Todas as queries passam pelo RLSBridge para respeitar Row-Level Security.
// ============================================================================

type DataComponent struct {
	*BaseComponent
	systemPool          *pgxpool.Pool
	projectPoolResolver func(ctx context.Context, tenantID string) (*pgxpool.Pool, error)
}

func NewDataComponent(id string, pool *pgxpool.Pool, resolver func(ctx context.Context, tenantID string) (*pgxpool.Pool, error)) *DataComponent {
	return &DataComponent{
		BaseComponent: NewBaseComponent(id, TypeData,
			[]PortDefinition{{Name: "in", DataType: "object", Required: true}},
			[]PortDefinition{
				{Name: "out", DataType: "object", Required: true},
				{Name: "error", DataType: "error", Required: false},
			},
		),
		systemPool:          pool,
		projectPoolResolver: resolver,
	}
}

// safeIdentifierRegex valida nomes de schema/tabela/coluna contra SQL injection.
var safeIdentifierRegex = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

func validateIdentifier(name, label string) error {
	if name == "" {
		return fmt.Errorf("nexus[data]: %s is empty", label)
	}
	if !safeIdentifierRegex.MatchString(name) {
		return fmt.Errorf("nexus[data]: invalid %s %q (only alphanumeric and underscores)", label, name)
	}
	return nil
}

func quoteIdent(name string) string {
	return fmt.Sprintf(`"%s"`, strings.ReplaceAll(name, `"`, `""`))
}

// Process bifurca entre No-Code e Raw SQL baseado na presença de 'operation' ou 'query'.
func (c *DataComponent) Process(ctx context.Context, ip *InformationPacket, state *NexusState) (map[string][]*InformationPacket, error) {
	c.SetStatus(StatusProcessing)

	// Resolver o pool do tenant dinamicamente
	pool := c.systemPool
	if c.projectPoolResolver != nil && state.Security().TenantID != "" {
		if p, err := c.projectPoolResolver(ctx, state.Security().TenantID); err == nil && p != nil {
			pool = p
		} else {
			return nil, fmt.Errorf("nexus[data]: failed to get project pool for tenant %s: %v", state.Security().TenantID, err)
		}
	}
	rlsBridge := NewRLSBridge(pool)

	settings := c.config.Settings

	// No-Code mode: tem 'operation' + 'table'
	if operation, ok := settings["operation"].(string); ok && operation != "" {
		return c.processNoCode(ctx, ip, state, rlsBridge, strings.ToUpper(operation))
	}

	// Raw SQL mode: tem 'query'
	return c.processRawSQL(ctx, ip, state, rlsBridge)
}

// ============================================================================
// NO-CODE MODE — Constrói SQL a partir da configuração visual
// ============================================================================

type kvPair struct {
	Column string
	Value  interface{}
}

type filterCondition struct {
	Column   string
	Operator string
	Value    interface{}
}

func (c *DataComponent) processNoCode(ctx context.Context, ip *InformationPacket, state *NexusState, rlsBridge *RLSBridge, operation string) (map[string][]*InformationPacket, error) {
	settings := c.config.Settings

	schema := "public"
	if s, ok := settings["schema"].(string); ok && s != "" {
		schema = s
	}
	table, _ := settings["table"].(string)

	if err := validateIdentifier(schema, "schema"); err != nil {
		c.SetStatus(StatusError)
		return nil, err
	}
	if err := validateIdentifier(table, "table"); err != nil {
		c.SetStatus(StatusError)
		return nil, err
	}

	mappingPairs := c.extractMapping(settings, state)
	filterPairs := c.extractFilters(settings, state)
	qualifiedTable := fmt.Sprintf("%s.%s", quoteIdent(schema), quoteIdent(table))

	var query string
	var args []interface{}
	var err error

	switch operation {
	case "INSERT":
		query, args, err = buildInsertSQL(qualifiedTable, mappingPairs)
	case "UPDATE":
		query, args, err = buildUpdateSQL(qualifiedTable, mappingPairs, filterPairs)
	case "DELETE":
		query, args, err = buildDeleteSQL(qualifiedTable, filterPairs)
	case "SELECT":
		query, args, err = buildSelectSQL(qualifiedTable, filterPairs)
	default:
		c.SetStatus(StatusError)
		return nil, fmt.Errorf("nexus[data]: unsupported operation %q", operation)
	}
	if err != nil {
		c.SetStatus(StatusError)
		return nil, err
	}

	secCtx := state.GetSecurityContext()
	results, err := rlsBridge.ExecRLS(ctx, secCtx, query, args...)
	if err != nil {
		c.SetStatus(StatusError)
		return c.handleError(ip, fmt.Errorf("nexus[data]: %s on %s failed: %w", operation, table, err))
	}

	c.SetStatus(StatusSuccess)

	outIp := ip.Clone()
	outIp.Data["data_result"] = map[string]interface{}{
		"rows":      results,
		"count":     len(results),
		"operation": operation,
		"table":     table,
	}
	// Expõe primeira linha no nível raiz para fácil acesso por nós downstream
	if len(results) > 0 {
		for k, v := range results[0] {
			outIp.Data[k] = v
		}
	}

	return EmitSingle("out", outIp), nil
}

// extractMapping lê pares coluna→valor de 'mappingRows' ou 'mapping'.
func (c *DataComponent) extractMapping(settings map[string]interface{}, state *NexusState) []kvPair {
	var pairs []kvPair

	// Prioridade 1: mappingRows (formato no-code UI)
	if rows, ok := settings["mappingRows"].([]interface{}); ok {
		for _, row := range rows {
			r, ok := row.(map[string]interface{})
			if !ok {
				continue
			}
			col, _ := r["column"].(string)
			val, _ := r["value"].(string)
			if col == "" {
				continue
			}
			resolved := state.Resolve(val)
			pairs = append(pairs, kvPair{Column: col, Value: resolved})
		}
	}

	// Prioridade 2: mapping objeto (formato compacto)
	if len(pairs) == 0 {
		if mapping, ok := settings["mapping"].(map[string]interface{}); ok {
			for col, val := range mapping {
				if col == "" {
					continue
				}
				valStr := fmt.Sprintf("%v", val)
				resolved := state.Resolve(valStr)
				pairs = append(pairs, kvPair{Column: col, Value: resolved})
			}
		}
	}

	return pairs
}

// extractFilters lê condições WHERE de 'filterRows'.
func (c *DataComponent) extractFilters(settings map[string]interface{}, state *NexusState) []filterCondition {
	var filters []filterCondition

	if rows, ok := settings["filterRows"].([]interface{}); ok {
		for _, row := range rows {
			r, ok := row.(map[string]interface{})
			if !ok {
				continue
			}
			col, _ := r["column"].(string)
			op, _ := r["operator"].(string)
			val, _ := r["value"].(string)
			if col == "" || op == "" {
				continue
			}
			resolved := state.Resolve(val)
			filters = append(filters, filterCondition{Column: col, Operator: op, Value: resolved})
		}
	}

	return filters
}

// ============================================================================
// SQL BUILDERS — Constroem queries parametrizadas seguras
// ============================================================================

func buildInsertSQL(table string, pairs []kvPair) (string, []interface{}, error) {
	if len(pairs) == 0 {
		return "", nil, fmt.Errorf("nexus[data]: INSERT requires at least one column mapping")
	}

	columns := make([]string, len(pairs))
	placeholders := make([]string, len(pairs))
	args := make([]interface{}, len(pairs))

	for i, p := range pairs {
		if err := validateIdentifier(p.Column, "column"); err != nil {
			return "", nil, err
		}
		columns[i] = quoteIdent(p.Column)
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args[i] = p.Value
	}

	query := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s) RETURNING *",
		table, strings.Join(columns, ", "), strings.Join(placeholders, ", "))
	return query, args, nil
}

func buildUpdateSQL(table string, pairs []kvPair, filters []filterCondition) (string, []interface{}, error) {
	if len(pairs) == 0 {
		return "", nil, fmt.Errorf("nexus[data]: UPDATE requires at least one column mapping")
	}

	setClauses := make([]string, len(pairs))
	args := make([]interface{}, 0, len(pairs)+len(filters))
	argIdx := 0

	for i, p := range pairs {
		if err := validateIdentifier(p.Column, "column"); err != nil {
			return "", nil, err
		}
		argIdx++
		setClauses[i] = fmt.Sprintf("%s = $%d", quoteIdent(p.Column), argIdx)
		args = append(args, p.Value)
	}

	whereClause, whereArgs, err := buildWhereClause(filters, argIdx)
	if err != nil {
		return "", nil, err
	}
	args = append(args, whereArgs...)

	query := fmt.Sprintf("UPDATE %s SET %s%s RETURNING *",
		table, strings.Join(setClauses, ", "), whereClause)
	return query, args, nil
}

func buildDeleteSQL(table string, filters []filterCondition) (string, []interface{}, error) {
	if len(filters) == 0 {
		return "", nil, fmt.Errorf("nexus[data]: DELETE requires at least one filter (safety guard)")
	}

	whereClause, args, err := buildWhereClause(filters, 0)
	if err != nil {
		return "", nil, err
	}

	query := fmt.Sprintf("DELETE FROM %s%s RETURNING *", table, whereClause)
	return query, args, nil
}

func buildSelectSQL(table string, filters []filterCondition) (string, []interface{}, error) {
	whereClause, args, err := buildWhereClause(filters, 0)
	if err != nil {
		return "", nil, err
	}

	query := fmt.Sprintf("SELECT * FROM %s%s LIMIT 1000", table, whereClause)
	return query, args, nil
}

func buildWhereClause(filters []filterCondition, startArgIdx int) (string, []interface{}, error) {
	if len(filters) == 0 {
		return "", nil, nil
	}

	clauses := make([]string, 0, len(filters))
	args := make([]interface{}, 0, len(filters))
	argIdx := startArgIdx

	for _, f := range filters {
		if err := validateIdentifier(f.Column, "filter column"); err != nil {
			return "", nil, err
		}

		var sqlOp string
		value := f.Value
		switch f.Operator {
		case "==", "eq", "equals", "=":
			sqlOp = "="
		case "!=", "neq", "not_equals":
			sqlOp = "!="
		case ">", "gt":
			sqlOp = ">"
		case ">=", "gte":
			sqlOp = ">="
		case "<", "lt":
			sqlOp = "<"
		case "<=", "lte":
			sqlOp = "<="
		case "contains":
			sqlOp = "ILIKE"
			value = fmt.Sprintf("%%%v%%", value)
		case "starts_with":
			sqlOp = "ILIKE"
			value = fmt.Sprintf("%v%%", value)
		case "ends_with":
			sqlOp = "ILIKE"
			value = fmt.Sprintf("%%%v", value)
		default:
			sqlOp = "="
		}

		argIdx++
		clauses = append(clauses, fmt.Sprintf("%s %s $%d", quoteIdent(f.Column), sqlOp, argIdx))
		args = append(args, value)
	}

	return " WHERE " + strings.Join(clauses, " AND "), args, nil
}

// ============================================================================
// RAW SQL MODE — Para modo Code (SQL avançado)
// ============================================================================

func (c *DataComponent) processRawSQL(ctx context.Context, ip *InformationPacket, state *NexusState, rlsBridge *RLSBridge) (map[string][]*InformationPacket, error) {
	queryTpl, ok := c.config.Settings["query"].(string)
	if !ok || queryTpl == "" {
		c.SetStatus(StatusError)
		return nil, fmt.Errorf("nexus[data]: node %s requires either 'operation' (no-code) or 'query' (code) in settings", c.ID())
	}

	query, err := state.InterpolateString(queryTpl, ip.Data)
	if err != nil {
		c.SetStatus(StatusError)
		return c.handleError(ip, fmt.Errorf("interpolation error: %w", err))
	}

	var args []interface{}
	if argsRaw, ok := c.config.Settings["args"].([]interface{}); ok {
		args = argsRaw
	}

	secCtx := state.GetSecurityContext()
	results, err := rlsBridge.ExecRLS(ctx, secCtx, query, args...)
	if err != nil {
		c.SetStatus(StatusError)
		return c.handleError(ip, err)
	}

	c.SetStatus(StatusSuccess)

	outIp := ip.Clone()
	outIp.Data["data_result"] = map[string]interface{}{
		"rows":  results,
		"count": len(results),
	}

	return EmitSingle("out", outIp), nil
}

// handleError aplica ErrorStrategy configurada.
func (c *DataComponent) handleError(ip *InformationPacket, err error) (map[string][]*InformationPacket, error) {
	if c.config.ErrorStrategy == ErrorBypass {
		return EmitEmpty(), nil
	}

	errIp := ip.Clone()
	errIp.Data["error"] = err.Error()

	if c.config.ErrorStrategy == ErrorFallback {
		return EmitSingle("error", errIp), nil
	}

	return nil, err
}
