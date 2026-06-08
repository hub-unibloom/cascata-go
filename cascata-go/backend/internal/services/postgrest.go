package services

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"cascata-backend/internal/utils"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgrestQuery struct {
	Text       string        `json:"text"`
	Values     []interface{} `json:"values"`
	Name       string        `json:"name,omitempty"`
	CountQuery string        `json:"count_query,omitempty"`
	CacheKey   string        `json:"cache_key,omitempty"`
	TTL        int           `json:"ttl,omitempty"`
}

type PostgrestService struct{}

// BuildQueryOptions holds optional parameters for resource embedding
type BuildQueryOptions struct {
	Ctx     context.Context
	Pool    *pgxpool.Pool
	Slug    string
	Schema  string
}

func (p *PostgrestService) BuildQuery(
	tableName string,
	method string,
	query map[string][]string,
	body map[string]interface{},
	headers http.Header,
	opts *BuildQueryOptions,
) (*PostgrestQuery, error) {
	// SANITIZATION
	re := regexp.MustCompile("^[a-zA-Z0-9_]+$")
	if !re.MatchString(tableName) {
		return nil, fmt.Errorf("Invalid table name identifier")
	}

	safeTable := utils.QuoteId(tableName)
	params := []interface{}{}
	sql := ""
	countQuery := ""

	// 1. Reserved Params
	selectParam := "*"
	if vals, ok := query["select"]; ok && len(vals) > 0 { selectParam = vals[0] }

	orderParam := ""
	if vals, ok := query["order"]; ok && len(vals) > 0 { orderParam = vals[0] }

	onConflictParam := ""
	if vals, ok := query["on_conflict"]; ok && len(vals) > 0 { onConflictParam = vals[0] }

	// 2. Build Filters
	filters := []string{}
	for key, values := range query {
		if isReserved(key) { continue }
		if !re.MatchString(key) { continue }

		for _, valStr := range values {
			clause, val := parseFilter(key, valStr, len(params)+1)
			if clause != "" {
				filters = append(filters, clause)
				if val != nil {
					params = append(params, val)
				}
			}
		}
	}
	whereClause := ""
	if len(filters) > 0 {
		whereClause = "WHERE " + strings.Join(filters, " AND ")
	}

	// 3. Handle Methods
	switch method {
	case "GET":
		// Check if resource embedding is needed (contains parentheses)
		needsEmbedding := strings.Contains(selectParam, "(") && strings.Contains(selectParam, ")")
		
		var columns string
		var joinClause string
		
		if needsEmbedding && opts != nil && opts.Ctx != nil && opts.Pool != nil {
			// Use resource embedding parser
			selectClause, joins, err := ParseSelectWithEmbedding(
				opts.Ctx, opts.Pool, opts.Slug, opts.Schema, tableName, selectParam,
			)
			if err != nil {
				return nil, fmt.Errorf("resource embedding error: %w", err)
			}
			columns = selectClause
			joinClause = joins
		} else {
			// Use legacy parser
			columns = parseSelect(selectParam)
		}
		
		orderBy := parseOrder(orderParam)
		limitClause := ""
		offsetClause := ""

		// Range Header
		if rangeH := headers.Get("Range"); rangeH != "" {
			reRange := regexp.MustCompile(`(\d+)-(\d+)?`)
			if match := reRange.FindStringSubmatch(rangeH); match != nil {
				start, _ := strconv.Atoi(match[1])
				offsetClause = fmt.Sprintf("OFFSET %d", start)
				if match[2] != "" {
					end, _ := strconv.Atoi(match[2])
					limitClause = fmt.Sprintf("LIMIT %d", end-start+1)
				}
			}
		}

		// Query Params Overrides
		if l := getFirst(query, "limit"); l != "" { limitClause = "LIMIT " + l }
		if o := getFirst(query, "offset"); o != "" { offsetClause = "OFFSET " + o }

		sql = fmt.Sprintf("SELECT %s FROM %s %s %s %s %s %s", columns, safeTable, joinClause, whereClause, orderBy, limitClause, offsetClause)

		if strings.Contains(headers.Get("Prefer"), "count=exact") {
			countQuery = fmt.Sprintf("SELECT COUNT(*) as total FROM %s %s", safeTable, whereClause)
		}

		// Semantic Cache
		if cacheH := headers.Get("X-Cascata-Cache-Control"); cacheH != "" {
			reCache := regexp.MustCompile(`max-age=(\d+)`)
			if match := reCache.FindStringSubmatch(cacheH); match != nil {
				ttl, _ := strconv.Atoi(match[1])
				if ttl > 0 && ttl <= 86400 {
					hash := sha256.Sum256([]byte(sql + fmt.Sprintf("%v", params)))
					cacheKey := fmt.Sprintf("qcache:%s:%s", tableName, hex.EncodeToString(hash[:])[:32])
					return &PostgrestQuery{Text: sql, Values: params, Name: generateStatementName(sql), CountQuery: countQuery, CacheKey: cacheKey, TTL: ttl}, nil
				}
			}
		}

	case "POST":
		var rows []map[string]interface{}
		if r, ok := body["_rows"].([]map[string]interface{}); ok {
			rows = r
		} else {
			rows = []map[string]interface{}{body}
		}

		if len(rows) == 0 { return nil, fmt.Errorf("No data to insert") }

		// SECURITY LOCK SANITIZER
		locks := parseJsonHeader(headers.Get("x-cascata-locked-columns"), tableName)
		// masks := parseJsonHeader(headers.Get("x-cascata-masked-columns"), tableName)

		keys := []string{}
		for k := range rows[0] {
			if locks[k] == "immutable" { continue }
			if !re.MatchString(k) { continue }
			keys = append(keys, k)
		}

		cols := []string{}
		for _, k := range keys { cols = append(cols, utils.QuoteId(k)) }

		valueGroups := []string{}
		paramIdx := 1
		for _, row := range rows {
			placeholders := []string{}
			for _, k := range keys {
				placeholders = append(placeholders, fmt.Sprintf("$%d", paramIdx))
				params = append(params, row[k])
				paramIdx++
			}
			valueGroups = append(valueGroups, "("+strings.Join(placeholders, ", ")+")")
		}

		upsertClause := ""
		prefer := headers.Get("Prefer")
		if strings.Contains(prefer, "resolution=merge-duplicates") {
			target := "id"
			if onConflictParam != "" { target = onConflictParam }
			
			updates := []string{}
			for _, k := range keys {
				updates = append(updates, fmt.Sprintf("%s = EXCLUDED.%s", utils.QuoteId(k), utils.QuoteId(k)))
			}
			upsertClause = fmt.Sprintf("ON CONFLICT (%s) DO UPDATE SET %s", utils.QuoteId(target), strings.Join(updates, ", "))
		} else if strings.Contains(prefer, "resolution=ignore-duplicates") {
			upsertClause = "ON CONFLICT DO NOTHING"
		}

		returning := ""
		if !strings.Contains(prefer, "return=minimal") { returning = "RETURNING *" }

		sql = fmt.Sprintf("INSERT INTO %s (%s) VALUES %s %s %s", safeTable, strings.Join(cols, ", "), strings.Join(valueGroups, ", "), upsertClause, returning)

	case "PATCH":
		locks := parseJsonHeader(headers.Get("x-cascata-locked-columns"), tableName)
		
		setClauses := []string{}
		for k, v := range body {
			if locks[k] == "immutable" || locks[k] == "insert_only" { continue }
			if !re.MatchString(k) { continue }
			
			setClauses = append(setClauses, fmt.Sprintf("%s = $%d", utils.QuoteId(k), len(params)+1))
			params = append(params, v)
		}

		if len(setClauses) == 0 { return nil, fmt.Errorf("No valid data to update") }
		if whereClause == "" { return nil, fmt.Errorf("PATCH requires a filter") }

		returning := ""
		if strings.Contains(headers.Get("Prefer"), "return=representation") { returning = "RETURNING *" }

		sql = fmt.Sprintf("UPDATE %s SET %s %s %s", safeTable, strings.Join(setClauses, ", "), whereClause, returning)

	case "DELETE":
		if whereClause == "" { return nil, fmt.Errorf("DELETE requires a filter") }
		returning := ""
		if strings.Contains(headers.Get("Prefer"), "return=representation") { returning = "RETURNING *" }
		sql = fmt.Sprintf("DELETE FROM %s %s %s", safeTable, whereClause, returning)
	}

	return &PostgrestQuery{Text: sql, Values: params, Name: generateStatementName(sql), CountQuery: countQuery}, nil
}

func parseJsonHeader(h, tableName string) map[string]string {
	if h == "" { return nil }
	var full map[string]map[string]string
	json.Unmarshal([]byte(h), &full)
	return full[tableName]
}

func isReserved(k string) bool {
	res := []string{"select", "order", "limit", "offset", "on_conflict", "columns"}
	for _, r := range res {
		if k == r { return true }
	}
	return false
}

func parseFilter(key, value string, paramIndex int) (string, interface{}) {
	column := utils.QuoteId(key)
	dotIndex := strings.Index(value, ".")
	if dotIndex == -1 {
		return fmt.Sprintf("%s = $%d", column, paramIndex), value
	}

	op := value[:dotIndex]
	rawVal := value[dotIndex+1:]

	switch op {
	case "eq": return fmt.Sprintf("%s = $%d", column, paramIndex), rawVal
	case "neq": return fmt.Sprintf("%s != $%d", column, paramIndex), rawVal
	case "gt": return fmt.Sprintf("%s > $%d", column, paramIndex), rawVal
	case "gte": return fmt.Sprintf("%s >= $%d", column, paramIndex), rawVal
	case "lt": return fmt.Sprintf("%s < $%d", column, paramIndex), rawVal
	case "lte": return fmt.Sprintf("%s <= $%d", column, paramIndex), rawVal
	case "like": return fmt.Sprintf("%s LIKE $%d", column, paramIndex), strings.ReplaceAll(rawVal, "*", "%")
	case "ilike": return fmt.Sprintf("%s ILIKE $%d", column, paramIndex), strings.ReplaceAll(rawVal, "*", "%")
	case "is":
		if rawVal == "null" { return fmt.Sprintf("%s IS NULL", column), nil }
		if rawVal == "true" { return fmt.Sprintf("%s IS TRUE", column), nil }
		if rawVal == "false" { return fmt.Sprintf("%s IS FALSE", column), nil }
	case "in":
		cleanVal := strings.Trim(rawVal, "()")
		arr := strings.Split(cleanVal, ",")
		return fmt.Sprintf("%s = ANY($%d)", column, paramIndex), arr
	}
	return fmt.Sprintf("%s = $%d", column, paramIndex), value
}

func parseSelect(s string) string {
	if s == "" || s == "*" { return "*" }
	parts := strings.Split(s, ",")
	quoted := []string{}
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if strings.Contains(p, ":") && !strings.Contains(p, "::") {
			bits := strings.Split(p, ":")
			quoted = append(quoted, fmt.Sprintf("%s AS %s", utils.QuoteId(bits[0]), utils.QuoteId(bits[1])))
		} else {
			quoted = append(quoted, utils.QuoteId(p))
		}
	}
	return strings.Join(quoted, ", ")
}

// parseSelectLegacy is the original parseSelect (kept for backward compatibility)
func parseSelectLegacy(s string) string {
	return parseSelect(s)
}

func parseOrder(s string) string {
	if s == "" { return "" }
	parts := strings.Split(s, ",")
	orders := []string{}
	for _, p := range parts {
		bits := strings.Split(p, ".")
		col := utils.QuoteId(bits[0])
		dir := "ASC"
		if len(bits) > 1 && strings.ToLower(bits[1]) == "desc" { dir = "DESC" }
		orders = append(orders, fmt.Sprintf("%s %s", col, dir))
	}
	return "ORDER BY " + strings.Join(orders, ", ")
}

func generateStatementName(sql string) string {
	hash := sha256.Sum256([]byte(sql))
	return "ps_" + hex.EncodeToString(hash[:])[:16]
}


func getFirst(m map[string][]string, key string) string {
	if vals, ok := m[key]; ok && len(vals) > 0 { return vals[0] }
	return ""
}
