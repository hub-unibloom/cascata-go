package services

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// GenerateOpenApiSpec generates OpenAPI 3.0.0 spec for a project
// Mirrors Modern Enterprise Standards with components/schemas and servers support
func GenerateOpenApiSpec(projectSlug, dbName, host string, projectPool *pgxpool.Pool) (map[string]interface{}, error) {
	// 1. Get tables from PROJECT database
	tables, err := getTables(projectPool)
	if err != nil {
		return nil, fmt.Errorf("failed to get tables: %w", err)
	}

	// 2. Get Edge Functions from system database
	edgeFunctions, err := getEdgeFunctions(projectSlug)
	if err != nil {
		fmt.Printf("[OpenAPI] Warning: failed to get edge functions: %v\n", err)
	}

	// Build OpenAPI 3.0.0 spec
	spec := map[string]interface{}{
		"openapi": "3.0.0",
		"info": map[string]interface{}{
			"description": "Cascata Sovereign API - Enterprise Grade Data Access",
			"title":       "Cascata API",
			"version":     "12.0.2",
			"contact": map[string]string{
				"name": "Cascata Support",
				"url": "https://cascata.io",
			},
		},
		"servers": []map[string]string{
			{
				"url":         fmt.Sprintf("/api/data/%s", projectSlug),
				"description": "Modern API (Sovereign Mode)",
			},
			{
				"url":         fmt.Sprintf("/api/data/%s/rest/v1", projectSlug),
				"description": "Legacy Compatibility (Supabase Style)",
			},
		},
		"paths": map[string]interface{}{
			"/": map[string]interface{}{
				"get": map[string]interface{}{
					"summary":    "OpenAPI Introspection",
					"tags":       []string{"Introspection"},
					"responses":  map[string]interface{}{"200": map[string]interface{}{"description": "OK"}},
				},
			},
		},
		"components": map[string]interface{}{
			"schemas":    make(map[string]interface{}),
			"parameters": map[string]interface{}{
				"select": map[string]interface{}{"name": "select", "in": "query", "schema": map[string]interface{}{"type": "string"}, "description": "Filtering columns"},
				"order":  map[string]interface{}{"name": "order", "in": "query", "schema": map[string]interface{}{"type": "string"}, "description": "Ordering"},
				"limit":  map[string]interface{}{"name": "limit", "in": "query", "schema": map[string]interface{}{"type": "integer"}, "description": "Pagination limit"},
				"offset": map[string]interface{}{"name": "offset", "in": "query", "schema": map[string]interface{}{"type": "integer"}, "description": "Pagination offset"},
			},
			"securitySchemes": map[string]interface{}{
				"ApiKeyAuth": map[string]string{
					"type": "apiKey",
					"in":   "header",
					"name": "apikey",
				},
				"BearerAuth": map[string]string{
					"type":   "http",
					"scheme": "bearer",
				},
			},
		},
		"security": []map[string]interface{}{
			{"ApiKeyAuth": []string{}},
			{"BearerAuth": []string{}},
		},
	}

	paths := spec["paths"].(map[string]interface{})
	components := spec["components"].(map[string]interface{})
	schemas := components["schemas"].(map[string]interface{})
	parameters := components["parameters"].(map[string]interface{})

	// 3. Build Table Schemas and Paths
	for _, table := range tables {
		columns, err := getColumns(projectPool, table)
		if err != nil {
			continue
		}

		properties := make(map[string]interface{})
		var required []string

		for _, col := range columns {
			typeMap := mapPgTypeToSwagger(col)
			prop := map[string]interface{}{
				"type":        typeMap["type"],
				"description": buildDescription(col),
			}
			if typeMap["format"] != "" {
				prop["format"] = typeMap["format"]
			}
			if col.ColumnDefault != nil && *col.ColumnDefault != "" {
				prop["default"] = *col.ColumnDefault
			}
			properties[col.Name] = prop

			if col.IsPrimaryKey || (col.IsNullable == "NO" && col.ColumnDefault == nil) {
				required = append(required, col.Name)
			}

			// Add filter parameter to components
			paramKey := fmt.Sprintf("%s.%s", table, col.Name)
			parameters[paramKey] = map[string]interface{}{
				"name": col.Name,
				"in":   "query",
				"schema": map[string]interface{}{"type": "string"},
				"description": fmt.Sprintf("Filter %s by %s", table, col.Name),
			}
		}

		schemas[table] = map[string]interface{}{
			"type":       "object",
			"properties": properties,
		}
		if len(required) > 0 {
			schemas[table].(map[string]interface{})["required"] = required
		}

		// Filter References for paths
		var rowFilters []map[string]interface{}
		for _, col := range columns {
			rowFilters = append(rowFilters, map[string]interface{}{
				"$ref": fmt.Sprintf("#/components/parameters/%s.%s", table, col.Name),
			})
		}

		// Route: /{table} - Clean Pattern
		paths["/"+table] = map[string]interface{}{
			"get": map[string]interface{}{
				"tags": []string{table},
				"parameters": append(rowFilters, []map[string]interface{}{
					{"$ref": "#/components/parameters/select"},
					{"$ref": "#/components/parameters/order"},
					{"$ref": "#/components/parameters/limit"},
					{"$ref": "#/components/parameters/offset"},
				}...),
				"responses": map[string]interface{}{
					"200": map[string]interface{}{
						"description": "OK",
						"content": map[string]interface{}{
							"application/json": map[string]interface{}{
								"schema": map[string]interface{}{
									"type": "array",
									"items": map[string]interface{}{"$ref": fmt.Sprintf("#/components/schemas/%s", table)},
								},
							},
						},
					},
				},
			},
			"post": map[string]interface{}{
				"tags": []string{table},
				"requestBody": map[string]interface{}{
					"content": map[string]interface{}{
						"application/json": map[string]interface{}{
							"schema": map[string]interface{}{"$ref": fmt.Sprintf("#/components/schemas/%s", table)},
						},
					},
				},
				"responses": map[string]interface{}{"201": map[string]interface{}{"description": "Created"}},
			},
			"patch": map[string]interface{}{
				"tags":       []string{table},
				"parameters": rowFilters,
				"requestBody": map[string]interface{}{
					"content": map[string]interface{}{
						"application/json": map[string]interface{}{
							"schema": map[string]interface{}{"$ref": fmt.Sprintf("#/components/schemas/%s", table)},
						},
					},
				},
				"responses": map[string]interface{}{"204": map[string]interface{}{"description": "No Content"}},
			},
			"delete": map[string]interface{}{
				"tags":       []string{table},
				"parameters": rowFilters,
				"responses":  map[string]interface{}{"204": map[string]interface{}{"description": "No Content"}},
			},
		}
	}

	// 4. Add Edge Functions
	for _, fn := range edgeFunctions {
		paths["/edge/"+fn.Name] = map[string]interface{}{
			"post": map[string]interface{}{
				"tags":    []string{"Edge Functions"},
				"summary": fmt.Sprintf("Execute %s", fn.Name),
				"requestBody": map[string]interface{}{
					"content": map[string]interface{}{
						"application/json": map[string]interface{}{
							"schema": map[string]interface{}{"type": "object"},
						},
					},
				},
				"responses": map[string]interface{}{
					"200": map[string]interface{}{
						"description": "OK",
						"content": map[string]interface{}{
							"application/json": map[string]interface{}{"schema": map[string]interface{}{"type": "object"}},
						},
					},
				},
			},
		}
	}

	return spec, nil
}

// GenerateSwagger2Spec generates Swagger 2.0 spec (OpenAPI 2.0) for PostgREST/Supabase/FlutterFlow compatibility
// This format uses 'definitions' instead of 'components/schemas' and 'host'/'basePath' instead of 'servers'
func GenerateSwagger2Spec(projectSlug, dbName, host, basePath string, projectPool *pgxpool.Pool) (map[string]interface{}, error) {
	// 1. Get tables from PROJECT database
	tables, err := getTables(projectPool)
	if err != nil {
		return nil, fmt.Errorf("failed to get tables: %w", err)
	}

	// 2. Get Edge Functions from system database
	edgeFunctions, err := getEdgeFunctions(projectSlug)
	if err != nil {
		fmt.Printf("[Swagger2] Warning: failed to get edge functions: %v\n", err)
	}

	// Build Swagger 2.0 spec structure
	spec := map[string]interface{}{
		"swagger": "2.0",
		"info": map[string]interface{}{
			"description": "Cascata API - PostgREST/Supabase Compatible (Swagger 2.0)",
			"title":       "Cascata API",
			"version":     "12.0.2",
			"contact": map[string]string{
				"name": "Cascata Support",
				"url":  "https://cascata.io",
			},
		},
		"host":     host,
		"basePath": basePath,
		"schemes":  []string{"http", "https"},
		"consumes": []string{"application/json", "application/vnd.pgrst.object+json"},
		"produces": []string{"application/json", "application/vnd.pgrst.object+json", "text/csv"},
		"paths": map[string]interface{}{
			"/": map[string]interface{}{
				"get": map[string]interface{}{
					"summary":  "OpenAPI Introspection",
					"tags":     []string{"Introspection"},
					"produces": []string{"application/openapi+json", "application/json"},
					"responses": map[string]interface{}{
						"200": map[string]interface{}{"description": "OK"},
					},
				},
			},
		},
		"definitions": make(map[string]interface{}),
		"parameters": map[string]interface{}{
			"select": map[string]interface{}{
				"name":        "select",
				"in":          "query",
				"type":        "string",
				"description": "Filtering Columns",
			},
			"order": map[string]interface{}{
				"name":        "order",
				"in":          "query",
				"type":        "string",
				"description": "Ordering",
			},
			"limit": map[string]interface{}{
				"name":        "limit",
				"in":          "query",
				"type":        "integer",
				"description": "Limiting and Pagination",
			},
			"offset": map[string]interface{}{
				"name":        "offset",
				"in":          "query",
				"type":        "integer",
				"description": "Pagination offset",
			},
			"preferPost": map[string]interface{}{
				"name":        "Prefer",
				"in":          "header",
				"type":        "string",
				"enum":        []string{"return=representation", "return=minimal"},
				"description": "Preference",
			},
		},
	}

	paths := spec["paths"].(map[string]interface{})
	definitions := spec["definitions"].(map[string]interface{})
	parameters := spec["parameters"].(map[string]interface{})

	// 3. Build Table Definitions and Paths
	for _, table := range tables {
		columns, err := getColumns(projectPool, table)
		if err != nil {
			continue
		}

		properties := make(map[string]interface{})
		var required []string

		for _, col := range columns {
			typeMap := mapPgTypeToSwagger(col)
			prop := map[string]interface{}{
				"type":        typeMap["type"],
				"description": buildDescription(col),
			}
			if typeMap["format"] != "" {
				prop["format"] = typeMap["format"]
			}
			if col.ColumnDefault != nil && *col.ColumnDefault != "" {
				prop["default"] = *col.ColumnDefault
			}
			properties[col.Name] = prop

			if col.IsPrimaryKey || (col.IsNullable == "NO" && col.ColumnDefault == nil) {
				required = append(required, col.Name)
			}

			// Add row filter parameter to shared parameters
			paramKey := fmt.Sprintf("rowFilter.%s.%s", table, col.Name)
			parameters[paramKey] = map[string]interface{}{
				"name":        col.Name,
				"in":          "query",
				"type":        "string",
				"description": fmt.Sprintf("Filter %s by %s", table, col.Name),
			}
		}

		// Table definition
		def := map[string]interface{}{
			"type":       "object",
			"properties": properties,
		}
		if len(required) > 0 {
			def["required"] = required
		}
		definitions[table] = def

		// Body parameter for this table
		bodyParamKey := fmt.Sprintf("body.%s", table)
		parameters[bodyParamKey] = map[string]interface{}{
			"name":        table,
			"in":          "body",
			"description": table,
			"schema": map[string]interface{}{
				"$ref": fmt.Sprintf("#/definitions/%s", table),
			},
		}

		// Build row filter references for this table
		var rowFilters []map[string]interface{}
		for _, col := range columns {
			rowFilters = append(rowFilters, map[string]interface{}{
				"$ref": fmt.Sprintf("#/parameters/rowFilter.%s.%s", table, col.Name),
			})
		}

		// Table path - GET/POST/PATCH/DELETE
		paths["/"+table] = map[string]interface{}{
			"get": map[string]interface{}{
				"tags":       []string{table},
				"parameters": append(rowFilters, []map[string]interface{}{
					{"$ref": "#/parameters/select"},
					{"$ref": "#/parameters/order"},
					{"$ref": "#/parameters/limit"},
					{"$ref": "#/parameters/offset"},
				}...),
				"responses": map[string]interface{}{
					"200": map[string]interface{}{
						"description": "OK",
						"schema": map[string]interface{}{
							"type": "array",
							"items": map[string]interface{}{
								"$ref": fmt.Sprintf("#/definitions/%s", table),
							},
						},
					},
				},
			},
			"post": map[string]interface{}{
				"tags": []string{table},
				"parameters": []map[string]interface{}{
					{"$ref": fmt.Sprintf("#/parameters/body.%s", table)},
					{"$ref": "#/parameters/preferPost"},
				},
				"responses": map[string]interface{}{
					"201": map[string]interface{}{"description": "Created"},
				},
			},
			"patch": map[string]interface{}{
				"tags":       []string{table},
				"parameters": append(rowFilters, []map[string]interface{}{
					{"$ref": fmt.Sprintf("#/parameters/body.%s", table)},
				}...),
				"responses": map[string]interface{}{
					"204": map[string]interface{}{"description": "No Content"},
				},
			},
			"delete": map[string]interface{}{
				"tags":       []string{table},
				"parameters": rowFilters,
				"responses": map[string]interface{}{
					"204": map[string]interface{}{"description": "No Content"},
				},
			},
		}
	}

	// 4. Add Edge Functions
	for _, fn := range edgeFunctions {
		paths["/rpc/"+fn.Name] = map[string]interface{}{
			"post": map[string]interface{}{
				"tags":        []string{"Edge Functions"},
				"summary":     fmt.Sprintf("Execute %s", fn.Name),
				"description": fn.Notes,
				"consumes":    []string{"application/json"},
				"produces":    []string{"application/json"},
				"parameters": []map[string]interface{}{
					{
						"name":     "payload",
						"in":       "body",
						"required": false,
						"schema": map[string]interface{}{
							"type":    "object",
							"example": map[string]string{"key": "value"},
						},
					},
				},
				"responses": map[string]interface{}{
					"200": map[string]interface{}{
						"description": "Successful Execution",
						"schema":      map[string]interface{}{"type": "object"},
					},
				},
			},
		}
	}

	return spec, nil
}

// ColumnInfo represents a PostgreSQL column
type ColumnInfo struct {
	Name          string
	UdtName       string
	IsNullable    string
	ColumnDefault *string
	IsPrimaryKey  bool
}

// EdgeFunctionInfo represents an Edge Function
type EdgeFunctionInfo struct {
	Name  string
	Notes string
}

func getTables(pool *pgxpool.Pool) ([]string, error) {
	rows, err := pool.Query(context.Background(), `
		SELECT table_name 
		FROM information_schema.tables 
		WHERE table_schema = 'public' 
		AND table_type = 'BASE TABLE'
		AND table_name NOT LIKE '_deleted_%'
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tables []string
	for rows.Next() {
		var tableName string
		if err := rows.Scan(&tableName); err == nil {
			tables = append(tables, tableName)
		}
	}
	return tables, rows.Err()
}

func getColumns(pool *pgxpool.Pool, table string) ([]ColumnInfo, error) {
	rows, err := pool.Query(context.Background(), `
		SELECT 
			a.attname as name,
			t.typname as udt_name,
			CASE WHEN a.attnotnull THEN 'NO' ELSE 'YES' END as is_nullable,
			pg_get_expr(d.adbin, d.adrelid) as column_default,
			COALESCE(i.indisprimary, false) as is_primary_key
		FROM pg_attribute a
		JOIN pg_class c ON c.oid = a.attrelid
		JOIN pg_namespace n ON n.oid = c.relnamespace
		JOIN pg_type t ON a.atttypid = t.oid
		LEFT JOIN pg_attrdef d ON d.adrelid = c.oid AND d.adnum = a.attnum
		LEFT JOIN pg_index i ON i.indrelid = c.oid AND i.indisprimary AND a.attnum = ANY(i.indkey)
		WHERE n.nspname = 'public' AND c.relname = $1 AND a.attnum > 0 AND NOT a.attisdropped
		ORDER BY a.attnum
	`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var columns []ColumnInfo
	for rows.Next() {
		var col ColumnInfo
		var colDefault *string
		err := rows.Scan(&col.Name, &col.UdtName, &col.IsNullable, &colDefault, &col.IsPrimaryKey)
		if err != nil {
			continue
		}
		col.ColumnDefault = colDefault
		columns = append(columns, col)
	}
	return columns, rows.Err()
}

func getEdgeFunctions(slug string) ([]EdgeFunctionInfo, error) {
	rows, err := SystemPool.Query(context.Background(),
		"SELECT name, metadata->>'notes' as notes FROM system.edge_functions WHERE project_slug = $1 AND status = 'active'",
		slug,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var functions []EdgeFunctionInfo
	for rows.Next() {
		var fn EdgeFunctionInfo
		var notes *string
		if err := rows.Scan(&fn.Name, &notes); err == nil {
			if notes != nil {
				fn.Notes = *notes
			} else {
				fn.Notes = "Serverless Function"
			}
			functions = append(functions, fn)
		}
	}
	return functions, rows.Err()
}

func buildDescription(col ColumnInfo) string {
	if col.IsPrimaryKey {
		return "Note:\nThis is a Primary Key.<pk/>"
	}
	return ""
}

func mapPgTypeToSwagger(col ColumnInfo) map[string]string {
	udt := col.UdtName
	switch udt {
	case "int2", "smallint":
		return map[string]string{"type": "integer", "format": "smallint"}
	case "int4", "integer", "serial", "serial4":
		return map[string]string{"type": "integer", "format": "integer"}
	case "int8", "bigint", "bigserial", "serial8":
		return map[string]string{"type": "integer", "format": "bigint"}
	case "numeric", "decimal", "real", "float4", "double precision", "float8", "money":
		return map[string]string{"type": "number", "format": "numeric"}
	case "bool", "boolean":
		return map[string]string{"type": "boolean", "format": "boolean"}
	case "uuid":
		return map[string]string{"type": "string", "format": "uuid"}
	case "json", "jsonb":
		return map[string]string{"type": "object", "format": "json"}
	case "timestamp", "timestamptz", "date", "time":
		return map[string]string{"type": "string", "format": "date-time"}
	case "text", "varchar", "char", "bpchar":
		return map[string]string{"type": "string", "format": "text"}
	}

	// Handle arrays (start with _)
	if len(udt) > 0 && udt[0] == '_' {
		innerUdt := udt[1:]
		innerMap := mapPgTypeToSwagger(ColumnInfo{UdtName: innerUdt})
		return map[string]string{"type": "array", "format": udt, "items.type": innerMap["type"], "items.format": innerMap["format"]}
	}

	return map[string]string{"type": "string", "format": "text"}
}
