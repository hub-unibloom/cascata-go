package utils

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"strings"

	"cascata-backend/internal/types"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ConvertToUUIDString converts a byte array to UUID string format if applicable
// This fixes the issue where []byte UUIDs are sent to PostgreSQL as byte arrays
func ConvertToUUIDString(val interface{}) interface{} {
	if val == nil {
		return nil
	}
	
	switch v := val.(type) {
	case []byte:
		// If it's 16 bytes, it's likely a UUID - convert to string
		if len(v) == 16 {
			return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
				v[0:4], v[4:6], v[6:8], v[8:10], v[10:16])
		}
		return v
	}
	
	return val
}

// QuoteId safely quotes a PostgreSQL identifier (table, column, schema)
func QuoteId(id string) string {
	return `"` + strings.ReplaceAll(id, `"`, `""`) + `"`
}

// QuoteLiteral safely quotes a PostgreSQL string literal
func QuoteLiteral(val string) string {
	return `'` + strings.ReplaceAll(val, `'`, `''`) + `'`
}

// ParseColumnFormat extracts description and format pattern from a column comment
// Supports TWO formats:
//   1. NEW: "description||FORMAT:regex" (used by TableCreatorDrawer)
//   2. LEGACY: "description [pattern:regex]" (old format)
func ParseColumnFormat(comment string) (string, string) {
	if comment == "" { return "", "" }
	
	// Try NEW format first: "||FORMAT:pattern"
	if idx := strings.Index(comment, "||FORMAT:"); idx >= 0 {
		description := strings.TrimSpace(comment[:idx])
		pattern := comment[idx+9:] // len("||FORMAT:") = 9
		return description, pattern
	}
	
	// Fallback to LEGACY format: "[pattern:regex]"
	patternRegex := `\[pattern:(.*?)\]`
	re := regexp.MustCompile(patternRegex)
	match := re.FindStringSubmatch(comment)
	
	if len(match) > 1 {
		pattern := match[1]
		description := strings.TrimSpace(strings.ReplaceAll(comment, match[0], ""))
		return description, pattern
	}
	
	// No pattern found - return whole comment as description
	return comment, ""
}

// MapPgTypeToCast converts Postgres types to the correct cast for UNNEST
func MapPgTypeToCast(dataType, udtName string) string {
	udt := strings.ToLower(udtName)
	dt := strings.ToLower(dataType)

	switch {
	case udt == "timestamptz" || dt == "timestamp with time zone": return "timestamptz"
	case udt == "timestamp" || dt == "timestamp without time zone": return "timestamp"
	case udt == "bool" || dt == "boolean": return "boolean"
	case udt == "int4" || dt == "integer": return "integer"
	case udt == "int8" || dt == "bigint": return "bigint"
	case udt == "uuid": return "uuid"
	case udt == "jsonb": return "jsonb"
	case dt == "character varying" || udt == "varchar" || udt == "text": return "text"
	}
	return "text"
}

// ExecuteWithRLS executes a SQL query with Row Level Security context.
// It sets up the PostgreSQL role and JWT claims before executing the query,
// ensuring RLS policies are properly evaluated.
// CRITICAL: This is the ONLY way user-facing queries should be executed.
func ExecuteWithRLS(
	ctx context.Context,
	pool *pgxpool.Pool,
	query string,
	params []interface{},
	userRole string,
	user map[string]interface{},
) ([]map[string]interface{}, error) {
	if userRole == "" || userRole == "postgres" {
		// No RLS needed (admin/internal use)
		return executeDirect(ctx, pool, query, params)
	}

	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()

	tx, err := conn.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	// Whitelist of allowed roles to prevent injection
	allowedRoles := map[string]bool{
		"anon": true, "authenticated": true,
		"service_role": true, "cascata_api_role": true,
	}

	role := userRole
	if !allowedRoles[role] {
		role = "authenticated" // Default fallback
	}

	// Extract context variables if available
	stepUpProviders := ""
	projectSlug := ""
	if val := ctx.Value(types.CascataCtxKey); val != nil {
		if cascataReq, ok := val.(*types.CascataRequest); ok {
			stepUpProviders = cascataReq.StepUpProviders
			if cascataReq.Project != nil {
				projectSlug = cascataReq.Project.Slug
			}
		}
	}

	// Sanitize values for SET LOCAL
	quoteLocal := func(s interface{}) string {
		if s == nil {
			return "''"
		}
		str := fmt.Sprintf("%v", s)
		return "'" + strings.ReplaceAll(str, "'", "''") + "'"
	}

	// Build and execute RLS setup SQL
	setupSQL := fmt.Sprintf(`
		SET LOCAL ROLE %s;
		SET LOCAL statement_timeout = '30000';
		SET LOCAL "request.jwt.claim.sub" = %s;
		SET LOCAL "request.jwt.claim.role" = %s;
		SET LOCAL "request.jwt.claim.email" = %s;
		SET LOCAL "request.stepup.verified_providers" = %s;
		SET LOCAL "request.jwt.claim.project_slug" = %s;
		SET LOCAL "app.current_project_slug" = %s;
	`,
		role,
		quoteLocal(user["sub"]),
		quoteLocal(role),
		quoteLocal(user["email"]),
		quoteLocal(stepUpProviders),
		quoteLocal(projectSlug),
		quoteLocal(projectSlug),
	)

	_, err = tx.Exec(ctx, setupSQL)
	if err != nil {
		return nil, fmt.Errorf("RLS setup failed: %w", err)
	}

	// Execute the actual query
	rows, err := tx.Query(ctx, query, params...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// Parse results
	fieldDesc := rows.FieldDescriptions()
	result := []map[string]interface{}{}
	if len(fieldDesc) > 0 {
		for rows.Next() {
			vals, err := rows.Values()
			if err != nil {
				continue
			}
			row := make(map[string]interface{})
			for j, fd := range fieldDesc {
				row[string(fd.Name)] = PurifyPgxValue(vals[j])
			}
			result = append(result, row)
		}
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	return result, nil
}

// executeDirect executes a query without RLS context (for internal/admin use)
func executeDirect(
	ctx context.Context,
	pool *pgxpool.Pool,
	query string,
	params []interface{},
) ([]map[string]interface{}, error) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()

	rows, err := conn.Query(ctx, query, params...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	fieldDesc := rows.FieldDescriptions()
	result := []map[string]interface{}{}
	if len(fieldDesc) > 0 {
		for rows.Next() {
			vals, err := rows.Values()
			if err != nil {
				continue
			}
			row := make(map[string]interface{})
			for j, fd := range fieldDesc {
				row[string(fd.Name)] = PurifyPgxValue(vals[j])
			}
			result = append(result, row)
		}
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return result, nil
}

// InsertRows handles high-performance batch insertion using UNNEST technique (Professional Synergy)
// SECURITY: This function MUST only be called AFTER EnforcePrePersistenceSecurity has validated the data.
// The typeMap parameter comes from the SecurityGateway cache — NO PostgreSQL round-trips for metadata.
func InsertRows(ctx context.Context, pool *pgxpool.Pool, tableName string, data interface{}, typeMap map[string]string, schema string) ([]map[string]interface{}, error) {
	if schema == "" { schema = "public" }
	
	log.Printf("[InsertRows] CALLED - table=%s.%s", schema, tableName)

	var rows []map[string]interface{}
	switch v := data.(type) {
	case []interface{}:
		for _, item := range v {
			if m, ok := item.(map[string]interface{}); ok {
				rows = append(rows, m)
			}
		}
	case map[string]interface{}:
		rows = append(rows, v)
	case []map[string]interface{}:
		rows = v
	default:
		return nil, fmt.Errorf("unsupported data format for InsertRows")
	}

	if len(rows) == 0 {
		return []map[string]interface{}{}, nil
	}

	// NOTE: Format validation, locked columns stripping, and computed columns
	// are already handled by EnforcePrePersistenceSecurity BEFORE this function is called.
	// This function ONLY builds and executes the SQL — data is already CLEAN.

	// Dynamic Query Construction using VALUES (compatível com QueryExecModeExec)
	allKeys := make(map[string]bool)
	for _, row := range rows {
		for k := range row { allKeys[k] = true }
	}

	keysArray := make([]string, 0, len(allKeys))
	for k := range allKeys { keysArray = append(keysArray, k) }

	columns := make([]string, len(keysArray))
	for i, key := range keysArray {
		columns[i] = QuoteId(key)
	}

	// Construir VALUES para cada linha: ($1, $2, ...), ($3, $4, ...)
	var valueClauses []string
	var flatValues []interface{}
	paramIdx := 1

	for _, row := range rows {
		rowParams := make([]string, len(keysArray))
		for i, key := range keysArray {
			if val, exists := row[key]; exists {
				// Convert UUID bytes to string to prevent byte array storage
				flatValues = append(flatValues, ConvertToUUIDString(val))
			} else {
				flatValues = append(flatValues, nil)
			}
			rowParams[i] = fmt.Sprintf("$%d", paramIdx)
			paramIdx++
		}
		valueClauses = append(valueClauses, "("+strings.Join(rowParams, ", ")+")")
	}

	sql := fmt.Sprintf("INSERT INTO %s.%s (%s) VALUES %s RETURNING *",
		QuoteId(schema), QuoteId(tableName),
		strings.Join(columns, ", "),
		strings.Join(valueClauses, ", "))

	// Extract user info from context
	userRole := "authenticated"
	var user map[string]interface{}
	
	if val := ctx.Value(types.CascataCtxKey); val != nil {
		if cascataReq, ok := val.(*types.CascataRequest); ok {
			userRole = string(cascataReq.UserRole)
			user = cascataReq.User
		}
	}

	insertedData, err := ExecuteWithRLS(ctx, pool, sql, flatValues, userRole, user)
	if err != nil { return nil, err }

	log.Printf("[InsertRows] SUCCESS - table=%s.%s, inserted %d rows", schema, tableName, len(insertedData))
	return insertedData, nil
}

// UpdateRows implements flexible record updating with column locking security
// SECURITY: This function MUST only be called AFTER EnforcePrePersistenceSecurity has validated the data.
// The pkCol parameter comes from the SecurityGateway cache — NO PostgreSQL round-trips for metadata.
func UpdateRows(ctx context.Context, pool *pgxpool.Pool, tableName string, data map[string]interface{}, pkCol string, schema string) (map[string]interface{}, error) {
	if schema == "" { schema = "public" }

	// NOTE: Format validation, locked columns stripping, and computed columns
	// are already handled by EnforcePrePersistenceSecurity BEFORE this function is called.
	// This function ONLY builds and executes the SQL — data is already CLEAN.

	// Extract PK value
	pkValue, pkExists := data[pkCol]

	// DEBUG LOG
	fmt.Printf("[UpdateRows DEBUG] table=%s, pkCol=%s, pkExists=%v, data keys=%v\n", tableName, pkCol, pkExists, getMapKeys(data))

	if !pkExists { return nil, fmt.Errorf("missing primary key value in update payload: expected key '%s' in data", pkCol) }

	// 3. Build Update Query
	var sets []string
	var args []interface{}
	i := 1
	for k, v := range data {
		if k == pkCol { continue }
		sets = append(sets, fmt.Sprintf("%s = $%d", QuoteId(k), i))
		args = append(args, v)
		i++
	}

	if len(sets) == 0 { return data, nil } // Nothing to update besides PK

	args = append(args, pkValue)
	whereIdx := len(args)
	sql := fmt.Sprintf("UPDATE %s.%s SET %s WHERE %s = $%d RETURNING *", QuoteId(schema), QuoteId(tableName), strings.Join(sets, ", "), QuoteId(pkCol), whereIdx)

	// Extract user info from context
	userRole := "authenticated"
	var user map[string]interface{}
	
	if val := ctx.Value(types.CascataCtxKey); val != nil {
		if cascataReq, ok := val.(*types.CascataRequest); ok {
			userRole = string(cascataReq.UserRole)
			user = cascataReq.User
		}
	}

	rows, err := ExecuteWithRLS(ctx, pool, sql, args, userRole, user)
	if err != nil { return nil, err }

	if len(rows) == 0 {
		return nil, fmt.Errorf("no row found with specified primary key")
	}

	return rows[0], nil
}

// Helper function for debug logging
func getMapKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

