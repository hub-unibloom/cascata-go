package services

import (
	"fmt"
	"log"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"cascata-backend/internal/utils"
)

// ============================================================================
// Data Node Executor - SELECT/INSERT/UPDATE/UPSERT/DELETE
// ============================================================================

// DataNodeExecutor executa operações CRUD com RLS
type DataNodeExecutor struct {
	Operation    string                 // select, insert, update, upsert, delete
	Table        string
	Filters      []DataFilter
	Body         interface{}            // template para insert/update
	ConflictCols string                 // para upsert
	ReturnCols   []string
	Limit        int
	OrderBy      string
	ReadOnly     bool                   // Force READ ONLY transaction
	TimeoutMs    int                    // Timeout configurável em ms (padrão: 8000)

	FieldExpressions    map[string]string
	FieldLogicPipelines map[string]InlineFieldLogicPipeline
	PayloadFieldPathMap map[string]string
		
	OutputSlot   int
	ErrorSlot    int
}

type DataFilter struct {
	Column string
	Op     string // eq, neq, gt, lt, gte, lte, like, ilike
	Value  string // referência para VarPool
}

func (n *DataNodeExecutor) GetOutputSlot() int { return n.OutputSlot }
func (n *DataNodeExecutor) GetErrorSlot() int  { return n.ErrorSlot }

func (n *DataNodeExecutor) Execute(ctx *FlowContext) error {
	log.Printf("[DataNodeExecutor] Starting execution - Operation=%s, Table=%s", n.Operation, n.Table)
	if n.Table == "" || n.Operation == "" {
		return fmt.Errorf("data node requires table and operation")
	}

	// Sanitize table name (básico)
	table := sanitizeIdentifier(n.Table)

	// Setup RLS
	role := ctx.UserRole
	if role == "" {
		role = "authenticated"
	}
	allowedRoles := map[string]bool{"anon": true, "authenticated": true, "service_role": true, "cascata_api_role": true}
	if !allowedRoles[role] {
		role = "authenticated"
	}

	claims := ctx.JWTClaims
	if claims == nil {
		claims = make(map[string]interface{})
	}

	quoteLocal := func(s interface{}) string {
		if s == nil {
			return "''"
		}
		str := fmt.Sprintf("%v", s)
		return "'" + strings.ReplaceAll(str, "'", "''") + "'"
	}

	// Get connection
	log.Printf("[DataNodeExecutor] Getting connection - ProjectPool type=%T, value=%v", ctx.ProjectPool, ctx.ProjectPool)
	pool, ok := ctx.ProjectPool.(*pgxpool.Pool)
	if !ok || pool == nil {
		log.Printf("[DataNodeExecutor] ERROR: ProjectPool is nil or wrong type - type=%T, ok=%v", ctx.ProjectPool, ok)
		return fmt.Errorf("no database pool available")
	}

	conn, err := pool.Acquire(ctx.Context)
	if err != nil {
		return fmt.Errorf("failed to acquire connection: %w", err)
	}
	defer conn.Release()

	tx, err := conn.Begin(ctx.Context)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback(ctx.Context)

	// Setup RLS
	timeoutMs := 8000
	if n.TimeoutMs > 0 {
		timeoutMs = n.TimeoutMs
	}
	
	// READ ONLY para operações de leitura ou quando forçado
	isReadOp := n.Operation == "select" || (n.ReadOnly && n.Operation != "insert" && n.Operation != "update" && n.Operation != "upsert" && n.Operation != "delete")
	
	// RLS Setup
	setupSQL := fmt.Sprintf(`
		SET LOCAL ROLE %s;
		SET LOCAL statement_timeout = '%dms';
		SET LOCAL "request.jwt.claim.sub" = %s;
		SET LOCAL "request.jwt.claim.role" = %s;
		SET LOCAL "request.jwt.claim.email" = %s;
	`, role, timeoutMs, quoteLocal(claims["sub"]), quoteLocal(claims["role"]), quoteLocal(claims["email"]))

	_, err = tx.Exec(ctx.Context, setupSQL)
	if err != nil {
		return fmt.Errorf("failed to setup RLS: %w", err)
	}

	// READ ONLY para operações de leitura
	if isReadOp {
		_, _ = tx.Exec(ctx.Context, "SET TRANSACTION READ ONLY")
	}

	// Executar operação
	var result interface{}
	switch n.Operation {
	case "select":
		result, err = n.executeSelect(ctx, tx, table)
	case "insert":
		result, err = n.executeInsert(ctx, tx, table)
	case "update":
		result, err = n.executeUpdate(ctx, tx, table)
	case "upsert":
		result, err = n.executeUpsert(ctx, tx, table)
	case "delete":
		result, err = n.executeDelete(ctx, tx, table)
	default:
		return fmt.Errorf("unknown operation: %s", n.Operation)
	}

	if err != nil {
		return err
	}

	if err := tx.Commit(ctx.Context); err != nil {
		return fmt.Errorf("commit failed: %w", err)
	}

	ctx.Vars[n.OutputSlot] = result
	return nil
}

func (n *DataNodeExecutor) executeSelect(ctx *FlowContext, tx pgx.Tx, table string) (interface{}, error) {
	query := fmt.Sprintf("SELECT * FROM %s", table)
	
	// WHERE
	params := []interface{}{}
	if len(n.Filters) > 0 {
		whereClauses := []string{}
		for i, f := range n.Filters {
			op := f.Op
			if op == "eq" {
				op = "="
			} else if op == "neq" {
				op = "!="
			}
			whereClauses = append(whereClauses, fmt.Sprintf("%s %s $%d", sanitizeIdentifier(f.Column), op, len(params)+1))

			fieldPath := fmt.Sprintf("filters.%d.value", i)
			baseValue := resolveFilterBaseValue(f.Value, ctx)
			value := applyFieldPipelineValue(fieldPath, baseValue, n.FieldExpressions, n.FieldLogicPipelines, ctx)
			params = append(params, value)
		}
		query += " WHERE " + strings.Join(whereClauses, " AND ")
	}

	// ORDER BY
	if n.OrderBy != "" {
		query += " ORDER BY " + sanitizeIdentifier(n.OrderBy)
	}

	// LIMIT
	if n.Limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", n.Limit)
	}

	rows, err := tx.Query(ctx.Context, query, params...)
	if err != nil {
		return nil, fmt.Errorf("select failed: %w", err)
	}
	defer rows.Close()

	return scanRows(rows)
}

func (n *DataNodeExecutor) executeInsert(ctx *FlowContext, tx pgx.Tx, table string) (interface{}, error) {
	resolvedBody := resolveTemplateCtx(n.Body, ctx)
	
	bodyMap, ok := resolvedBody.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("insert body must be an object")
	}
	bodyMap = applyFieldPipelinesToBodyMap(bodyMap, n.PayloadFieldPathMap, n.FieldExpressions, n.FieldLogicPipelines, ctx)

	keys := make([]string, 0, len(bodyMap))
	values := make([]interface{}, 0, len(bodyMap))
	for k, v := range bodyMap {
		keys = append(keys, sanitizeIdentifier(k))
		values = append(values, v)
	}

	placeholders := make([]string, len(values))
	for i := range values {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
	}

	query := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s) RETURNING *",
		table,
		strings.Join(keys, ", "),
		strings.Join(placeholders, ", "))

	log.Printf("[DataNodeExecutor.executeInsert] EXECUTING INSERT - table=%s, keys=%v, values=%v", table, keys, values)

	rows, err := tx.Query(ctx.Context, query, values...)
	if err != nil {
		log.Printf("[DataNodeExecutor.executeInsert] INSERT FAILED: %v", err)
		return nil, fmt.Errorf("insert failed: %w", err)
	}
	defer rows.Close()

	result, err := scanRows(rows)
	if err != nil {
		log.Printf("[DataNodeExecutor.executeInsert] scanRows failed: %v", err)
		return nil, err
	}
	
	log.Printf("[DataNodeExecutor.executeInsert] INSERT SUCCESS - table=%s, result rows=%d", table, len(result))
	return result, nil
}

func (n *DataNodeExecutor) executeUpdate(ctx *FlowContext, tx pgx.Tx, table string) (interface{}, error) {
	resolvedBody := resolveTemplateCtx(n.Body, ctx)

	bodyMap, ok := resolvedBody.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("update body must be an object")
	}
	bodyMap = applyFieldPipelinesToBodyMap(bodyMap, n.PayloadFieldPathMap, n.FieldExpressions, n.FieldLogicPipelines, ctx)

	setClauses := []string{}
	params := []interface{}{}
	for k, v := range bodyMap {
		setClauses = append(setClauses, fmt.Sprintf("%s = $%d", sanitizeIdentifier(k), len(params)+1))
		params = append(params, v)
	}

	query := fmt.Sprintf("UPDATE %s SET %s", table, strings.Join(setClauses, ", "))

	// WHERE
	if len(n.Filters) > 0 {
		whereClauses := []string{}
		for i, f := range n.Filters {
			op := f.Op
			if op == "eq" {
				op = "="
			}
			whereClauses = append(whereClauses, fmt.Sprintf("%s %s $%d", sanitizeIdentifier(f.Column), op, len(params)+1))

			fieldPath := fmt.Sprintf("filters.%d.value", i)
			baseValue := resolveFilterBaseValue(f.Value, ctx)
			value := applyFieldPipelineValue(fieldPath, baseValue, n.FieldExpressions, n.FieldLogicPipelines, ctx)
			params = append(params, value)
		}
		query += " WHERE " + strings.Join(whereClauses, " AND ")
	}

	query += " RETURNING *"

	rows, err := tx.Query(ctx.Context, query, params...)
	if err != nil {
		return nil, fmt.Errorf("update failed: %w", err)
	}
	defer rows.Close()

	return scanRows(rows)
}

func (n *DataNodeExecutor) executeUpsert(ctx *FlowContext, tx pgx.Tx, table string) (interface{}, error) {
	resolvedBody := resolveTemplateCtx(n.Body, ctx)

	bodyMap, ok := resolvedBody.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("upsert body must be an object")
	}
	bodyMap = applyFieldPipelinesToBodyMap(bodyMap, n.PayloadFieldPathMap, n.FieldExpressions, n.FieldLogicPipelines, ctx)

	keys := make([]string, 0, len(bodyMap))
	values := make([]interface{}, 0, len(bodyMap))
	for k, v := range bodyMap {
		keys = append(keys, sanitizeIdentifier(k))
		values = append(values, v)
	}

	placeholders := make([]string, len(values))
	for i := range values {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
	}

	// Colunas de conflito
	conflictCols := n.ConflictCols
	if conflictCols == "" {
		conflictCols = "id"
	}

	// UPDATE SET
	updateCols := make([]string, len(keys))
	for i, k := range keys {
		updateCols[i] = fmt.Sprintf("%s = EXCLUDED.%s", k, k)
	}

	query := fmt.Sprintf(
		"INSERT INTO %s (%s) VALUES (%s) ON CONFLICT (%s) DO UPDATE SET %s RETURNING *",
		table,
		strings.Join(keys, ", "),
		strings.Join(placeholders, ", "),
		sanitizeIdentifier(conflictCols),
		strings.Join(updateCols, ", "))

	rows, err := tx.Query(ctx.Context, query, values...)
	if err != nil {
		return nil, fmt.Errorf("upsert failed: %w", err)
	}
	defer rows.Close()

	return scanRows(rows)
}

func (n *DataNodeExecutor) executeDelete(ctx *FlowContext, tx pgx.Tx, table string) (interface{}, error) {
	query := fmt.Sprintf("DELETE FROM %s", table)
	
	params := []interface{}{}
	if len(n.Filters) > 0 {
		whereClauses := []string{}
		for i, f := range n.Filters {
			op := "="
			if f.Op == "neq" {
				op = "!="
			}
			whereClauses = append(whereClauses, fmt.Sprintf("%s %s $%d", sanitizeIdentifier(f.Column), op, len(params)+1))

			fieldPath := fmt.Sprintf("filters.%d.value", i)
			baseValue := resolveFilterBaseValue(f.Value, ctx)
			value := applyFieldPipelineValue(fieldPath, baseValue, n.FieldExpressions, n.FieldLogicPipelines, ctx)
			params = append(params, value)
		}
		query += " WHERE " + strings.Join(whereClauses, " AND ")
	}

	query += " RETURNING *"

	rows, err := tx.Query(ctx.Context, query, params...)
	if err != nil {
		return nil, fmt.Errorf("delete failed: %w", err)
	}
	defer rows.Close()

	return scanRows(rows)
}

func scanRows(rows pgx.Rows) ([]map[string]interface{}, error) {
	var results []map[string]interface{}
	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			continue
		}
		row := make(map[string]interface{})
		for i, fd := range rows.FieldDescriptions() {
			row[string(fd.Name)] = utils.PurifyPgxValue(values[i])
		}
		results = append(results, row)
	}
	return results, nil
}

func sanitizeIdentifier(name string) string {
	// Remove caracteres perigosos, mantém apenas alfanumérico e underscore
	var result strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			result.WriteRune(r)
		}
	}
	return result.String()
}

// BuildDataNode cria um DataNodeExecutor
func BuildDataNode(config map[string]interface{}, compiler *Compiler) (NodeExecutor, error) {
	node := &DataNodeExecutor{
		OutputSlot: compiler.allocSlot(),
		ErrorSlot:  -1,
		Filters:    []DataFilter{},
		TimeoutMs:  8000, // Default: 8000ms
		FieldExpressions:    map[string]string{},
		FieldLogicPipelines: map[string]InlineFieldLogicPipeline{},
		PayloadFieldPathMap: map[string]string{},
	}

	if v, ok := config["operation"].(string); ok {
		node.Operation = strings.ToLower(v)
	}
	if v, ok := config["table"].(string); ok {
		node.Table = v
	}
	if v, ok := config["body"]; ok {
		node.Body = v
	}
	if v, ok := config["conflict_cols"].(string); ok {
		node.ConflictCols = v
	}
	if v, ok := config["limit"].(float64); ok {
		node.Limit = int(v)
	}
	if v, ok := config["order_by"].(string); ok {
		node.OrderBy = v
	}
	if v, ok := config["readonly"].(bool); ok {
		node.ReadOnly = v
	}
	if v, ok := config["timeout_ms"].(float64); ok {
		node.TimeoutMs = int(v)
	}

	node.FieldExpressions = parseFieldExpressions(config)
	node.FieldLogicPipelines = parseFieldLogicPipelines(config)
	node.PayloadFieldPathMap = buildIndexedFieldPathMap(config, "_payload", "column", false)

	// Parse filters
	if filters, ok := config["filters"].([]interface{}); ok {
		for _, f := range filters {
			if fMap, ok := f.(map[string]interface{}); ok {
				filter := DataFilter{}
				if c, ok := fMap["column"].(string); ok {
					filter.Column = c
				}
				if o, ok := fMap["op"].(string); ok {
					filter.Op = o
				}
				if v, ok := fMap["value"]; ok {
					filter.Value = fmt.Sprintf("%v", v)
				}
				node.Filters = append(node.Filters, filter)
			}
		}
	}

	return node, nil
}

// ============================================================================
// RPC Node Executor - Chamada de funções PostgreSQL
// ============================================================================

// RPCNodeExecutor executa funções PostgreSQL com RLS
type RPCNodeExecutor struct {
	Function string
	Args     []string // referências para VarPool
	
	OutputSlot int
	ErrorSlot  int
	
	// Configurações avançadas
	TimeoutMs int    // Timeout em milissegundos (padrão: 8000)
	ReadOnly  bool   // Executar em transação READ ONLY
}

func (n *RPCNodeExecutor) GetOutputSlot() int { return n.OutputSlot }
func (n *RPCNodeExecutor) GetErrorSlot() int  { return n.ErrorSlot }

func (n *RPCNodeExecutor) Execute(ctx *FlowContext) error {
	if n.Function == "" {
		return fmt.Errorf("rpc node requires function name")
	}

	// Timeout configurável (padrão: 8000ms)
	timeoutMs := n.TimeoutMs
	if timeoutMs <= 0 {
		timeoutMs = 8000
	}
	if timeoutMs > 60000 {
		timeoutMs = 60000 // Máximo: 60s
	}

	// Sanitize function name
	fnName := sanitizeIdentifier(n.Function)

	// Setup RLS
	role := ctx.UserRole
	if role == "" {
		role = "authenticated"
	}
	allowedRoles := map[string]bool{"anon": true, "authenticated": true, "service_role": true, "cascata_api_role": true}
	if !allowedRoles[role] {
		role = "authenticated"
	}

	claims := ctx.JWTClaims
	if claims == nil {
		claims = make(map[string]interface{})
	}

	quoteLocal := func(s interface{}) string {
		if s == nil {
			return "''"
		}
		str := fmt.Sprintf("%v", s)
		return "'" + strings.ReplaceAll(str, "'", "''") + "'"
	}

	// Get connection
	pool, ok := ctx.ProjectPool.(*pgxpool.Pool)
	if !ok || pool == nil {
		return fmt.Errorf("no database pool available")
	}

	conn, err := pool.Acquire(ctx.Context)
	if err != nil {
		return fmt.Errorf("failed to acquire connection: %w", err)
	}
	defer conn.Release()

	// Transação com READ ONLY opcional
	var tx pgx.Tx
	if n.ReadOnly {
		tx, err = conn.BeginTx(ctx.Context, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	} else {
		tx, err = conn.Begin(ctx.Context)
	}
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback(ctx.Context)

	// RLS Setup com timeout configurável
	setupSQL := fmt.Sprintf(`
		SET LOCAL ROLE %s;
		SET LOCAL statement_timeout = '%dms';
		SET LOCAL "request.jwt.claim.sub" = %s;
		SET LOCAL "request.jwt.claim.role" = %s;
		SET LOCAL "request.jwt.claim.email" = %s;
	`, role, timeoutMs, quoteLocal(claims["sub"]), quoteLocal(claims["role"]), quoteLocal(claims["email"]))

	_, err = tx.Exec(ctx.Context, setupSQL)
	if err != nil {
		return fmt.Errorf("failed to setup RLS: %w", err)
	}

	// Prepare args
	args := make([]interface{}, len(n.Args))
	for i, argRef := range n.Args {
		args[i] = resolveVarCtx(argRef, ctx)
	}

	placeholders := make([]string, len(args))
	for i := range args {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
	}

	query := fmt.Sprintf("SELECT * FROM %s(%s)", fnName, strings.Join(placeholders, ", "))

	rows, err := tx.Query(ctx.Context, query, args...)
	if err != nil {
		return fmt.Errorf("rpc failed: %w", err)
	}
	defer rows.Close()

	result, err := scanRows(rows)
	if err != nil {
		return err
	}

	if err := tx.Commit(ctx.Context); err != nil {
		return fmt.Errorf("commit failed: %w", err)
	}

	ctx.Vars[n.OutputSlot] = result
	return nil
}

// BuildRPCNode cria um RPCNodeExecutor
func BuildRPCNode(config map[string]interface{}, compiler *Compiler) (NodeExecutor, error) {
	node := &RPCNodeExecutor{
		OutputSlot: compiler.allocSlot(),
		ErrorSlot:  -1,
		Args:       []string{},
		TimeoutMs:  8000, // Padrão: 8000ms
		ReadOnly:   false,
	}

	if v, ok := config["function"].(string); ok {
		node.Function = v
	}
	if args, ok := config["args"].([]interface{}); ok {
		for _, a := range args {
			node.Args = append(node.Args, fmt.Sprintf("%v", a))
		}
	}
	
	// Parse timeout_ms
	if timeout, ok := config["timeout_ms"].(float64); ok {
		node.TimeoutMs = int(timeout)
	}
	if timeout, ok := config["timeout_ms"].(int); ok {
		node.TimeoutMs = timeout
	}
	
	// Parse readonly flag
	if readonly, ok := config["readonly"].(bool); ok {
		node.ReadOnly = readonly
	}

	return node, nil
}
