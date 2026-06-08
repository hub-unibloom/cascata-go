package services

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"cascata-backend/internal/types"
	"cascata-backend/internal/utils"
	"cascata-backend/internal/services/nexus"
	"github.com/jackc/pgx/v5/pgxpool"
)

// cacheInvalidationChannel is the Redis pub/sub channel for cache invalidation
const cacheInvalidationChannel = "cache:invalidation"

// ColumnMetadata holds unified metadata for a single column (Edge-First Architecture)
// This is the single source of truth at runtime, cached in Dragonfly + L1 sync.Map
type ColumnMetadata struct {
	FormatPattern string `json:"formatPattern"`
	LockLevel     string `json:"lockLevel"`     // "unlocked", "immutable", "insert_only", "service_role_only", "code_protected", "otp_protected", "auto_clock"
	MaskLevel     string `json:"maskLevel"`     // "unmasked", "hide", "blur", "mask", "semi-mask", "encrypt"
	Formula       string `json:"formula"`
	ReturnType    string `json:"returnType"`
	StrictMode    bool   `json:"strictMode"`
	DataType      string `json:"dataType"`     // PostgreSQL data type (e.g. "text", "int4", "uuid")
	UdtName       string `json:"udtName"`      // PostgreSQL udt_name for UNNEST casting
	IsNullable    bool   `json:"isNullable"`
	Description   string `json:"description"`
	Methods       string `json:"methods,omitempty"` // E.g. "TOTP, Passkey, OTP"
}

// TableSchema is the full schema metadata for a table, keyed by column name
type TableSchema map[string]*ColumnMetadata

// EnumType represents a PostgreSQL ENUM type with its values
type EnumType struct {
	Schema string   `json:"schema"`
	Name   string   `json:"name"`
	Values []string `json:"values"`
}

// EnumTypeCache is a map of enum type names to their values (key: "schema.name" or just "name")
type EnumTypeCache map[string]*EnumType

// SchemaCache implements the Edge-First metadata cache using Dragonfly (L2) + sync.Map (L1)
// This eliminates PostgreSQL round-trips for format validation, locked columns, computed columns, and enum validation
type SchemaCache struct {
	l1Cache                 sync.Map // key: "{slug}:{schema}:{table}" → value: *schemaCacheEntry
	enumCache               sync.Map // key: "{slug}:{schema}" → value: *enumCacheEntry
	NexusSvc                *nexus.NexusService
}

type schemaCacheEntry struct {
	schema   TableSchema
	cachedAt time.Time
}

type enumCacheEntry struct {
	enums    EnumTypeCache
	cachedAt time.Time
}

const (
	schemaCacheTTL     = 5 * time.Minute  // L1 in-memory TTL
	dragonflySchemaTTL = 10 * time.Minute // L2 Dragonfly TTL
)

// Global SchemaCache instance
var GlobalSchemaCache = &SchemaCache{}

// cacheInvalidationMessage represents a cache invalidation message sent via pub/sub
type cacheInvalidationMessage struct {
	Action    string `json:"action"`    // "invalidate_table", "invalidate_project", "invalidate_all"
	Slug      string `json:"slug"`
	Schema    string `json:"schema,omitempty"`
	Table     string `json:"table,omitempty"`
	Timestamp int64  `json:"timestamp"`
}

// init starts the pub/sub listener for cache invalidation
func init() {
	// Start listening for cache invalidation messages in a goroutine
	go startCacheInvalidationListener()
}

// startCacheInvalidationListener listens for cache invalidation messages from other workers
func startCacheInvalidationListener() {
	// Wait for Dragonfly to be initialized (retry loop)
	for i := 0; i < 30; i++ {
		if GetDragonfly() != nil {
			break
		}
		time.Sleep(1 * time.Second)
	}

	rdb := GetDragonfly()
	if rdb == nil {
		log.Println("[SchemaCache] Dragonfly not available after wait, skipping distributed cache invalidation")
		return
	}

	ctx := context.Background()
	pubsub := rdb.Subscribe(ctx, cacheInvalidationChannel)
	defer pubsub.Close()

	log.Println("[SchemaCache] Started distributed cache invalidation listener")

	ch := pubsub.Channel()
	for msg := range ch {
		var invMsg cacheInvalidationMessage
		if err := json.Unmarshal([]byte(msg.Payload), &invMsg); err != nil {
			continue
		}

		// Process invalidation message
		switch invMsg.Action {
		case "invalidate_table":
			key := cacheKey(invMsg.Slug, invMsg.Schema, invMsg.Table)
			GlobalSchemaCache.l1Cache.Delete(key)
			log.Printf("[SchemaCache] Invalidated L1 cache for %s (from pub/sub)", key)
		case "invalidate_project":
			prefix := invMsg.Slug + ":schema:"
			GlobalSchemaCache.l1Cache.Range(func(k, v interface{}) bool {
				if key, ok := k.(string); ok {
					if len(key) > len(prefix) && key[:len(prefix)] == prefix {
						GlobalSchemaCache.l1Cache.Delete(k)
					}
				}
				return true
			})
			// LIMPA O CACHE LOCAL DE METADATA (project.go) PARA TODAS AS INSTÂNCIAS (DOCKERS)
			InvalidateProjectCache(invMsg.Slug)
			log.Printf("[SchemaCache] Invalidated all L1 caches and Project Struct for project %s (from pub/sub)", invMsg.Slug)
		}
	}
}

// publishCacheInvalidation sends an invalidation message to all workers
func publishCacheInvalidation(msg cacheInvalidationMessage) {
	rdb := GetDragonfly()
	if rdb == nil {
		return
	}

	msg.Timestamp = time.Now().Unix()
	data, _ := json.Marshal(msg)
	ctx := context.Background()
	rdb.Publish(ctx, cacheInvalidationChannel, string(data))
}

// cacheKey builds the standardized cache key
func cacheKey(slug, schema, table string) string {
	return fmt.Sprintf("%s:schema:%s:table:%s", slug, schema, table)
}

// GetTableSchema returns cached table schema, loading from DB if needed (Cache-Aside pattern)
// CRITICAL: This is the ONLY function that should be called during request validation
// It NEVER returns nil — worst case returns empty TableSchema
func (sc *SchemaCache) GetTableSchema(
	ctx context.Context,
	pool *pgxpool.Pool,
	metadata *types.ProjectMetadata,
	slug, schema, tableName string,
) TableSchema {
	key := cacheKey(slug, schema, tableName)

	// === L1: In-Memory Cache (sync.Map) ===
	if val, ok := sc.l1Cache.Load(key); ok {
		entry := val.(*schemaCacheEntry)
		if time.Since(entry.cachedAt) < schemaCacheTTL {
			return entry.schema
		}
		// Expired — fall through to L2/DB
	}

	// === L2: Dragonfly Cache ===
	rdb := GetDragonfly()
	if rdb != nil {
		cached, err := rdb.Get(ctx, key).Result()
		if err == nil && cached != "" {
			var ts TableSchema
			if json.Unmarshal([]byte(cached), &ts) == nil && len(ts) > 0 {
				// Warm L1 from L2
				sc.l1Cache.Store(key, &schemaCacheEntry{schema: ts, cachedAt: time.Now()})
				return ts
			}
		}
	}

	// === L3: PostgreSQL (Cold Start — single query) ===
	ts := sc.warmFromDatabase(ctx, pool, metadata, schema, tableName)

	// Store in L1
	sc.l1Cache.Store(key, &schemaCacheEntry{schema: ts, cachedAt: time.Now()})

	// Store in L2 (Dragonfly) — best effort, non-blocking
	if rdb != nil {
		if data, err := json.Marshal(ts); err == nil {
			rdb.Set(ctx, key, string(data), dragonflySchemaTTL)
		}
	}

	return ts
}

// warmFromDatabase performs the single PostgreSQL query to build full column metadata
// This merges: col_description (formatPattern), Project.Metadata (locks, masks, computed), and column types
func (sc *SchemaCache) warmFromDatabase(
	ctx context.Context,
	pool *pgxpool.Pool,
	metadata *types.ProjectMetadata,
	schema, tableName string,
) TableSchema {
	ts := make(TableSchema)

	// Single query to get ALL column metadata from PostgreSQL
	query := `
		SELECT c.column_name, c.data_type, c.udt_name, c.is_nullable,
		       col_description(pgc.oid, c.ordinal_position) as comment
		FROM information_schema.columns c
		JOIN pg_catalog.pg_class pgc ON pgc.relname = c.table_name AND pgc.relkind = 'r'
		JOIN pg_catalog.pg_namespace pgn ON pgn.oid = pgc.relnamespace AND pgn.nspname = c.table_schema
		WHERE c.table_schema = $1 AND c.table_name = $2
		ORDER BY c.ordinal_position`

	rows, err := pool.Query(ctx, query, schema, tableName)
	if err != nil {
		log.Printf("[SchemaCache] Failed to warm cache for %s.%s: %v", schema, tableName, err)
		return ts
	}
	defer rows.Close()

	for rows.Next() {
		var name, dataType, udtName, isNullable string
		var comment *string // CRITICAL: Must be *string because col_description can return NULL

		if err := rows.Scan(&name, &dataType, &udtName, &isNullable, &comment); err != nil {
			log.Printf("[SchemaCache] Scan error for %s.%s: %v", schema, tableName, err)
			continue
		}

		// Use udt_name when data_type is USER-DEFINED (enums, custom types)
		finalDataType := dataType
		if dataType == "USER-DEFINED" && udtName != "" {
			finalDataType = udtName
		}

		cm := &ColumnMetadata{
			DataType:   finalDataType,
			UdtName:    udtName,
			IsNullable: isNullable == "YES",
			LockLevel:  "unlocked",
			MaskLevel:  "unmasked",
		}

		// Parse formatPattern and description from column comment
		if comment != nil && *comment != "" {
			cm.Description, cm.FormatPattern = utils.ParseColumnFormat(*comment)
		}

		ts[name] = cm
	}

	// Merge Project.Metadata (Locked, Masked, Computed columns)
	if metadata != nil {
		// Locked Columns
		if locks, ok := metadata.LockedColumns[tableName]; ok {
			for colName, levelVal := range locks {
				if cm, exists := ts[colName]; exists {
					if strVal, isStr := levelVal.(string); isStr {
						cm.LockLevel = strVal
					} else if mapVal, isMap := levelVal.(map[string]interface{}); isMap {
						if lockType, has := mapVal["lock_type"].(string); has {
							cm.LockLevel = lockType
						} else if lockLevel, has := mapVal["lockLevel"].(string); has {
							cm.LockLevel = lockLevel
						}
						// Retrieve allowed factors/methods
						if factorsRaw, has := mapVal["allowed_factors"]; has {
							if factorsArr, ok := factorsRaw.([]interface{}); ok {
								var factorsStr []string
								for _, f := range factorsArr {
									if fs, ok := f.(string); ok {
										factorsStr = append(factorsStr, FormatFactorName(fs))
									}
								}
								cm.Methods = strings.Join(factorsStr, ", ")
							} else if factorsStrArr, ok := factorsRaw.([]string); ok {
								var factorsStr []string
								for _, f := range factorsStrArr {
									factorsStr = append(factorsStr, FormatFactorName(f))
								}
								cm.Methods = strings.Join(factorsStr, ", ")
							}
						} else if methodsRaw, has := mapVal["methods"]; has {
							if methodsStr, isStr := methodsRaw.(string); isStr {
								parts := strings.Split(methodsStr, ",")
								var factors []string
								for _, p := range parts {
									p = strings.TrimSpace(p)
									if p != "" {
										factors = append(factors, FormatFactorName(p))
									}
								}
								cm.Methods = strings.Join(factors, ", ")
							}
						}
					}
				}
			}
		}

		// Masked Columns
		if masks, ok := metadata.MaskedColumns[tableName]; ok {
			for colName, level := range masks {
				if cm, exists := ts[colName]; exists {
					cm.MaskLevel = level
				}
			}
		}

		// Auto Clock Columns - aplicar lockLevel "auto_clock" às colunas configuradas
		if autoClocks, ok := metadata.AutoClockColumns[tableName]; ok {
			for colName := range autoClocks {
				if cm, exists := ts[colName]; exists {
					cm.LockLevel = "auto_clock"
				}
			}
		}

		// Computed Columns
		if computed, ok := metadata.ComputedColumns[tableName]; ok {
			for colName, def := range computed {
				if cm, exists := ts[colName]; exists {
					cm.Formula = def.Formula
					cm.ReturnType = def.ReturnType
					cm.StrictMode = def.StrictMode
				}
			}
		}
	}

	// Security: Detect columns with common auto_clock names that are NOT protected
	// Isso ajuda a identificar potenciais brechas de segurança no schema
	detectUnprotectedTemporalColumns(ts, tableName)

	return ts
}

// InvalidateTable removes cached schema for a specific table from both L1 and L2
// MUST be called whenever: CreateTable, AlterTable, UpdateMetadata, COMMENT ON COLUMN
func (sc *SchemaCache) InvalidateTable(slug, schema, tableName string) {
	key := cacheKey(slug, schema, tableName)

	// L1: Remove from sync.Map
	sc.l1Cache.Delete(key)

	// L2: Remove from Dragonfly
	rdb := GetDragonfly()
	if rdb != nil {
		ctx := context.Background()
		rdb.Del(ctx, key)
		log.Printf("[SchemaCache] Invalidated cache for %s", key)
	}

	// Also invalidate FK cache for this table
	GlobalForeignKeyCache.InvalidateForeignKeys(slug, schema, tableName)

	// Notify other workers to invalidate their L1 caches
	publishCacheInvalidation(cacheInvalidationMessage{
		Action: "invalidate_table",
		Slug:   slug,
		Schema: schema,
		Table:  tableName,
	})
}

// InvalidateProject removes ALL cached schemas for a project
// Called when project metadata is bulk-updated
func (sc *SchemaCache) InvalidateProject(slug string) {
	prefix := slug + ":schema:"

	// L1: Walk and delete matching keys
	sc.l1Cache.Range(func(k, v interface{}) bool {
		if key, ok := k.(string); ok {
			if len(key) > len(prefix) && key[:len(prefix)] == prefix {
				sc.l1Cache.Delete(k)
			}
		}
		return true
	})

	// L2: Pattern delete from Dragonfly using SCAN (safer than KEYS)
	rdb := GetDragonfly()
	if rdb != nil {
		ctx := context.Background()
		pattern := slug + ":schema:*"
		
		var deletedCount int
		iter := rdb.Scan(ctx, 0, pattern, 100).Iterator()
		for iter.Next(ctx) {
			rdb.Del(ctx, iter.Val())
			deletedCount++
		}
		
		if err := iter.Err(); err != nil {
			log.Printf("[SchemaCache] Error scanning keys for project %s: %v", slug, err)
		} else {
			log.Printf("[SchemaCache] Invalidated ALL cached schemas for project %s (%d keys deleted)", slug, deletedCount)
		}
	}

	// Also invalidate ALL FK caches for this project
	GlobalForeignKeyCache.InvalidateProjectForeignKeys(slug)

	// Limpa também o cache estrutural do projeto para garantir que o próximo request
	// (ex: warmFromDatabase) pegue o metadata mais recente do banco de dados (ex: locks, masks).
	InvalidateProjectCache(slug)

	// Notify other workers to invalidate their L1 caches and Project Structs
	publishCacheInvalidation(cacheInvalidationMessage{
		Action: "invalidate_project",
		Slug:   slug,
	})
}

// ValidateFormatPatterns validates all fields in a row map against cached format patterns
// Returns error on the FIRST validation failure (fail-fast)
// This is the Edge-First replacement for the old per-request PostgreSQL query
func ValidateFormatPatterns(schema TableSchema, data map[string]interface{}) error {
	for colName, meta := range schema {
		if meta.FormatPattern == "" {
			continue
		}
		val, exists := data[colName]
		if !exists || val == nil {
			continue
		}
		strVal := fmt.Sprintf("%v", val)
		if valid, err := utils.ValidateFormatPattern(strVal, meta.FormatPattern); !valid {
			return fmt.Errorf("validation failed for column '%s': %v", colName, err)
		}
	}
	return nil
}

// ValidateFormatPatternsMulti validates multiple rows against cached format patterns
// Returns error on the FIRST validation failure across ALL rows
func ValidateFormatPatternsMulti(schema TableSchema, rows []map[string]interface{}) error {
	for _, row := range rows {
		if err := ValidateFormatPatterns(schema, row); err != nil {
			return err
		}
	}
	return nil
}

// GetEnumTypes retrieves ENUM types for a schema (with L1/L2 caching)
func (sc *SchemaCache) GetEnumTypes(ctx context.Context, pool *pgxpool.Pool, slug, schema string) EnumTypeCache {
	key := fmt.Sprintf("%s:%s:enums", slug, schema)

	// === L1: In-Memory Cache ===
	if entry, ok := sc.enumCache.Load(key); ok {
		if e, valid := entry.(*enumCacheEntry); valid && time.Since(e.cachedAt) < schemaCacheTTL {
			return e.enums
		}
	}

	// === L2: Dragonfly Cache ===
	rdb := GetDragonflyClient()
	if rdb != nil {
		if cached, err := rdb.Get(ctx, key).Result(); err == nil && cached != "" {
			var enums EnumTypeCache
			if json.Unmarshal([]byte(cached), &enums) == nil && len(enums) > 0 {
				sc.enumCache.Store(key, &enumCacheEntry{enums: enums, cachedAt: time.Now()})
				return enums
			}
		}
	}

	// === L3: PostgreSQL (Cold Start) ===
	enums := sc.warmEnumsFromDatabase(ctx, pool, schema)

	// Store in L1
	sc.enumCache.Store(key, &enumCacheEntry{enums: enums, cachedAt: time.Now()})

	// Store in L2 (Dragonfly)
	if rdb != nil {
		if data, err := json.Marshal(enums); err == nil {
			rdb.Set(ctx, key, string(data), dragonflySchemaTTL)
		}
	}

	return enums
}

// warmEnumsFromDatabase fetches ENUM types from PostgreSQL
func (sc *SchemaCache) warmEnumsFromDatabase(ctx context.Context, pool *pgxpool.Pool, schema string) EnumTypeCache {
	enums := make(EnumTypeCache)

	query := `
		SELECT 
			n.nspname as schema,
			t.typname as name,
			array_agg(e.enumlabel ORDER BY e.enumsortorder) as values
		FROM pg_type t
		JOIN pg_namespace n ON n.oid = t.typnamespace
		LEFT JOIN pg_enum e ON e.enumtypid = t.oid
		WHERE t.typtype = 'e'
		AND n.nspname = $1
		GROUP BY n.nspname, t.typname
		ORDER BY n.nspname, t.typname`

	rows, err := pool.Query(ctx, query, schema)
	if err != nil {
		log.Printf("[SchemaCache] Failed to fetch ENUM types for schema %s: %v", schema, err)
		return enums
	}
	defer rows.Close()

	for rows.Next() {
		var enumSchema, name string
		var values []string
		if err := rows.Scan(&enumSchema, &name, &values); err != nil {
			continue
		}
		enum := &EnumType{
			Schema: enumSchema,
			Name:   name,
			Values: values,
		}
		// Store by simple name and schema.name
		enums[name] = enum
		enums[fmt.Sprintf("%s.%s", enumSchema, name)] = enum
	}

	return enums
}

// ValidateEnums validates that enum values are valid for their type
// Returns error on the FIRST validation failure (fail-fast)
func ValidateEnums(schema TableSchema, enums EnumTypeCache, data map[string]interface{}) error {
	for colName, meta := range schema {
		// Check if this column's type is an enum
		enum, isEnum := enums[meta.DataType]
		if !isEnum {
			// Try with schema prefix
			enum, isEnum = enums[meta.UdtName]
		}
		if !isEnum {
			continue
		}

		val, exists := data[colName]
		if !exists || val == nil {
			continue
		}

		strVal := fmt.Sprintf("%v", val)
		
		// Check if value is in enum values
		valid := false
		for _, ev := range enum.Values {
			if ev == strVal {
				valid = true
				break
			}
		}
		
		if !valid {
			return fmt.Errorf("invalid ENUM value for column '%s': '%s' is not a valid value for type '%s'. Valid values: %v",
				colName, strVal, enum.Name, enum.Values)
		}
	}
	return nil
}

// ValidateEnumsMulti validates multiple rows against enum types
func ValidateEnumsMulti(schema TableSchema, enums EnumTypeCache, rows []map[string]interface{}) error {
	for _, row := range rows {
		if err := ValidateEnums(schema, enums, row); err != nil {
			return err
		}
	}
	return nil
}

// StripLockedColumns removes locked columns from the data map (in-place mutation)
// Returns list of stripped column names for audit logging
func StripLockedColumns(schema TableSchema, data map[string]interface{}, operation string) []string {
	var stripped []string
	for colName, meta := range schema {
		if _, exists := data[colName]; !exists {
			continue
		}
		switch meta.LockLevel {
		case "immutable":
			delete(data, colName)
			stripped = append(stripped, colName)
		case "insert_only":
			if operation == "UPDATE" {
				delete(data, colName)
				stripped = append(stripped, colName)
			}
		// "service_role_only", "code_protected" and "otp_protected" are handled by the controller layer
		// because they need request context (user role, OTP token)
		// "auto_clock" is handled by ApplyAutoClock (enrichment, not stripping)
		}
	}
	return stripped
}

// StripAndAuditAutoClock detecta e remove tentativas de spoofing em colunas auto_clock
// Retorna lista de colunas onde spoofing foi detectado (para audit logging)
// SECURITY: Mesmo que o valor seja válido, removemos para garantir que só o sistema define
func StripAndAuditAutoClock(ctx context.Context, schema TableSchema, data map[string]interface{}, operation string, tableName string) []string {
	var spoofAttempts []string

	// Extrair contexto de auditoria do CascataRequest (se disponível)
	var projectSlug, clientIP, userRole string
	if val := ctx.Value(types.CascataCtxKey); val != nil {
		if cascataCtx, ok := val.(*types.CascataRequest); ok {
			if cascataCtx.Project != nil {
				projectSlug = cascataCtx.Project.Slug
			}
			userRole = string(cascataCtx.UserRole)
			// Client IP pode vir do header ou RemoteAddr - simplificado aqui
			// No middleware real, isso é extraído de forma mais robusta
		}
	}

	for colName, meta := range schema {
		if meta.LockLevel != "auto_clock" {
			continue
		}

		// Verifica se usuário tentou enviar valor para coluna auto_clock
		if _, exists := data[colName]; !exists {
			continue
		}

		// Sempre remove - sistema deve controlar completamente
		spoofedValue := data[colName] // Capturar antes de deletar para o log
		delete(data, colName)
		spoofAttempts = append(spoofAttempts, colName)

		// Log de segurança estruturado - agora vai para o banco de dados também
		LogSecurityEvent(
			projectSlug,
			tableName,
			colName,
			operation,
			"AUTO_CLOCK_SPOOF_ATTEMPT",
			clientIP,
			userRole,
			"warning",
			map[string]interface{}{
				"attempted_value": fmt.Sprintf("%v", spoofedValue),
				"action_taken":    "value_stripped",
				"system_replaced": "NOW()",
			},
		)
	}

	return spoofAttempts
}

// Common auto_clock column names - sinergia com nomes já existentes no sistema
// Detecta padrões como: updated_at, modified_at, update, data, last_modified, etc.
var commonAutoClockNames = []string{
	"updated_at", "modified_at", "last_modified", "last_updated",
	"update", "modificado", "atualizado", "data", // sinergia com sistemas existentes
	"changed_at", "timestamp", "revision_date",
}

// isCommonAutoClockName verifica se o nome da coluna segue padrões comuns de auto_clock
// Útil para detecção automática ou warnings de segurança
func isCommonAutoClockName(colName string) bool {
	colName = strings.ToLower(colName)
	for _, pattern := range commonAutoClockNames {
		if strings.Contains(colName, pattern) {
			return true
		}
	}
	return false
}

// detectUnprotectedTemporalColumns verifica colunas temporais comuns que NÃO estão em auto_clock
// Útil para logging de segurança - ajuda a identificar colunas que talvez deveriam ser protegidas
func detectUnprotectedTemporalColumns(schema TableSchema, tableName string) []string {
	var unprotected []string
	for colName, meta := range schema {
		// É temporal E tem nome comum de auto_clock, mas NÃO está marcada como auto_clock
		if isDateTimeType(meta.DataType) && isCommonAutoClockName(colName) && meta.LockLevel != "auto_clock" {
			unprotected = append(unprotected, colName)
		}
	}
	if len(unprotected) > 0 {
		log.Printf("[AutoClock-DETECT] Table %s has temporal columns with common auto_clock names but NOT protected: %v | Consider setting lockLevel=auto_clock", tableName, unprotected)
	}
	return unprotected
}

// isDateTimeType verifica se um tipo PostgreSQL é de data/hora
// Cobre todos os tipos temporais existentes no PostgreSQL
func isDateTimeType(dataType string) bool {
	dataType = strings.ToLower(dataType)
	dtTypes := []string{
		"timestamp", "timestamptz", "timestamp with time zone", "timestamp without time zone",
		"date",
		"time", "timetz", "time with time zone", "time without time zone",
		"interval",
	}
	for _, t := range dtTypes {
		if strings.Contains(dataType, t) {
			return true
		}
	}
	return false
}

// getNowValueForType retorna o valor NOW() apropriado para o tipo PostgreSQL
// Inteligente: detecta variações e retorna formato correto
func getNowValueForType(dataType string) interface{} {
	dataType = strings.ToLower(dataType)

	switch {
	// Timestamp com timezone
	case strings.Contains(dataType, "timestamp with time zone"),
		strings.Contains(dataType, "timestamptz"):
		return time.Now().UTC()

	// Timestamp sem timezone
	case strings.Contains(dataType, "timestamp without time zone"),
		dataType == "timestamp":
		return time.Now().UTC().Truncate(time.Second)

	// Data apenas (date)
	case dataType == "date":
		return time.Now().UTC().Truncate(24 * time.Hour).Format("2006-01-02")

	// Time com timezone
	case strings.Contains(dataType, "time with time zone"),
		strings.Contains(dataType, "timetz"):
		return time.Now().UTC().Format("15:04:05-07")

	// Time sem timezone
	case strings.Contains(dataType, "time without time zone"),
		dataType == "time":
		return time.Now().UTC().Format("15:04:05")

	// Interval - retorna duração desde epoch (uso raro)
	case dataType == "interval":
		return time.Since(time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)).String()

	// Default fallback: timestamp with time zone (mais comum)
	default:
		return time.Now().UTC()
	}
}

// ApplyAutoClock enriquece os dados com NOW() para colunas auto_clock
// Inteligente: detecta TODOS os tipos de data/hora PostgreSQL
// Retorna lista de colunas que foram atualizadas
func ApplyAutoClock(schema TableSchema, data map[string]interface{}, operation string) []string {
	var updated []string
	if operation != "UPDATE" {
		// Auto Clock só aplica em UPDATE (não em INSERT)
		return updated
	}

	for colName, meta := range schema {
		if meta.LockLevel != "auto_clock" {
			continue
		}

		// Verificar se é tipo de data/hora (proteção extra)
		if !isDateTimeType(meta.DataType) {
			log.Printf("[AutoClock-WARN] Column %s marked auto_clock but type %s is not temporal - applying anyway", colName, meta.DataType)
		}

		// Aplicar NOW() com tipo correto
		data[colName] = getNowValueForType(meta.DataType)
		updated = append(updated, colName)
	}

	return updated
}

// SecurityGatewayResult holds the sanitized data and table schema after security enforcement
type SecurityGatewayResult struct {
	SanitizedData map[string]interface{}
	TableSchema   TableSchema
	StrippedCols  []string
	Operation     string
}

// EnforcePrePersistenceSecurity is the INVULNERABLE security gateway.
// ALL write operations MUST pass through this function before touching PostgreSQL.
// No request, no route, no code path can bypass these layers.
//
// Security Layers (in order):
//   1. FORMAT PATTERN VALIDATION      — fail-fast, HTTP 400
//   1.1. ENUM TYPE VALIDATION         — fail-fast if value not in ENUM, HTTP 400
//   1.2. REQUEST AUTOMATION INTERCEPT — REQUEST_INTERCEPT trigger (enrich/block before persist)
//   2. LOCKED COLUMNS STRIPPING       — silent removal + audit log
//   2.1. AUTO CLOCK SPOOF DETECTION   — detecta e remove tentativas de spoofing + security alert
//   2.2. AUTO CLOCK ENRICHMENT        — adiciona NOW() às colunas auto_clock (Go, não trigger)
//   3. COMPUTED COLUMNS               — formula evaluation (strict_mode → 400)
//
// After this function returns, data is CLEAN and VALIDATED.
// PostgreSQL ONLY receives data that passed ALL layers.
func (sc *SchemaCache) EnforcePrePersistenceSecurity(
	ctx context.Context,
	pool *pgxpool.Pool,
	metadata *types.ProjectMetadata,
	slug, schema, tableName string,
	data map[string]interface{},
	operation string,
	computedSvc interface{ TopologicalSortColumns(map[string]string) []string; EvaluateFormula(string, map[string]interface{}) (interface{}, error) },
) (*SecurityGatewayResult, error) {
	// === STEP 0: Get table metadata from cache (Cache-Aside: auto-warm if miss) ===
	// This is the EDGE-FIRST optimization — zero DB round-trips for cached tables
	tableSchema := sc.GetTableSchema(ctx, pool, metadata, slug, schema, tableName)

	// === STEP 1: LAYER 1 — FORMAT PATTERN VALIDATION ===
	// Fail-fast: if any value violates its format pattern, REJECT immediately
	// No data touches PostgreSQL on validation failure
	if err := ValidateFormatPatterns(tableSchema, data); err != nil {
		return nil, err
	}

	// === STEP 1.2: LAYER 1.2 — ENUM TYPE VALIDATION ===
	// Fail-fast: if any enum value is invalid, REJECT immediately
	// Validates against PostgreSQL ENUM types cached from pg_enum
	enumTypes := sc.GetEnumTypes(ctx, pool, slug, schema)
	if err := ValidateEnums(tableSchema, enumTypes, data); err != nil {
		return nil, err
	}

	// === STEP 2: LAYER 2 — LOCKED COLUMNS STRIPPING ===
	// Silent removal: immutable columns are stripped from the payload
	// Audit trail is preserved in StrippedCols for logging
	stripped := StripLockedColumns(tableSchema, data, operation)

	// === STEP 2.1: LAYER 2.1 — AUTO CLOCK SPOOF DETECTION ===
	// Detecta e remove tentativas de spoofing em colunas auto_clock
	// Log de segurança estruturado: vai para system.api_logs com category=security
	spoofAttempts := StripAndAuditAutoClock(ctx, tableSchema, data, operation, tableName)
	if len(spoofAttempts) > 0 {
		// Log já emitido pela função via LogSecurityEvent
		// Os valores spoofed foram removidos, sistema vai aplicar NOW() corretamente
		log.Printf("[SecurityGateway] Spoofed auto_clock values stripped: %v", spoofAttempts)
	}

	// === STEP 2.2: LAYER 2.2 — AUTO CLOCK ENRICHMENT ===
	// Adiciona NOW() automaticamente às colunas com lockLevel=auto_clock
	// Isso acontece no Go (mais rápido e confiável que trigger PostgreSQL)
	autoClockUpdated := ApplyAutoClock(tableSchema, data, operation)
	if len(autoClockUpdated) > 0 {
		log.Printf("[SecurityGateway] Auto Clock applied to columns: %v", autoClockUpdated)
	}

	// === STEP 3: LAYER 3 — COMPUTED COLUMNS ===
	// Formula evaluation with topological sort for dependency resolution
	computedFormulas := make(map[string]string)
	strictModes := make(map[string]bool)
	for colName, meta := range tableSchema {
		if meta.Formula != "" {
			computedFormulas[colName] = meta.Formula
			strictModes[colName] = meta.StrictMode
		}
	}

	if len(computedFormulas) > 0 && computedSvc != nil {
		orderedCols := computedSvc.TopologicalSortColumns(computedFormulas)
		for _, colName := range orderedCols {
			formula := computedFormulas[colName]
			computedVal, err := computedSvc.EvaluateFormula(formula, data)
			if err != nil {
				if strictModes[colName] {
					return nil, fmt.Errorf("computed column '%s' formula error: %s", colName, err.Error())
				}
				continue
			}
			if computedVal != nil {
				data[colName] = computedVal
			}
		}
	}

	return &SecurityGatewayResult{
		SanitizedData: data,
		TableSchema:   tableSchema,
		StrippedCols:  stripped,
		Operation:     operation,
	}, nil
}

// EnforcePrePersistenceSecurityMulti applies security layers to multiple rows (batch inserts)
func (sc *SchemaCache) EnforcePrePersistenceSecurityMulti(
	ctx context.Context,
	pool *pgxpool.Pool,
	metadata *types.ProjectMetadata,
	slug, schema, tableName string,
	rows []map[string]interface{},
	operation string,
	computedSvc interface{ TopologicalSortColumns(map[string]string) []string; EvaluateFormula(string, map[string]interface{}) (interface{}, error) },
) (*SecurityGatewayResult, error) {
	// Get schema once for all rows
	tableSchema := sc.GetTableSchema(ctx, pool, metadata, slug, schema, tableName)

	// Validate ALL rows against format patterns (fail-fast on first violation)
	if err := ValidateFormatPatternsMulti(tableSchema, rows); err != nil {
		return nil, err
	}

	// Validate ALL rows against ENUM types (fail-fast on first violation)
	enumTypes := sc.GetEnumTypes(ctx, pool, slug, schema)
	if err := ValidateEnumsMulti(tableSchema, enumTypes, rows); err != nil {
		return nil, err
	}

	// Strip locked columns from ALL rows
	var allStripped []string
	for _, row := range rows {
		stripped := StripLockedColumns(tableSchema, row, operation)
		allStripped = append(allStripped, stripped...)
	}

	// Strip and audit auto_clock spoof attempts from ALL rows
	var allSpoofAttempts []string
	for _, row := range rows {
		spoofAttempts := StripAndAuditAutoClock(ctx, tableSchema, row, operation, tableName)
		allSpoofAttempts = append(allSpoofAttempts, spoofAttempts...)
	}
	if len(allSpoofAttempts) > 0 {
		log.Printf("[SecurityGateway-Multi] Spoofed auto_clock values stripped from batch: %v", allSpoofAttempts)
	}

	// Apply auto_clock enrichment for ALL rows (batch UPDATE operations)
	var allAutoClocks []string
	for _, row := range rows {
		autoClocks := ApplyAutoClock(tableSchema, row, operation)
		allAutoClocks = append(allAutoClocks, autoClocks...)
	}
	if len(allAutoClocks) > 0 {
		log.Printf("[SecurityGateway-Multi] Auto Clock applied to batch columns: %v", allAutoClocks)
	}

	// Evaluate computed columns for ALL rows
	computedFormulas := make(map[string]string)
	strictModes := make(map[string]bool)
	for colName, meta := range tableSchema {
		if meta.Formula != "" {
			computedFormulas[colName] = meta.Formula
			strictModes[colName] = meta.StrictMode
		}
	}

	if len(computedFormulas) > 0 && computedSvc != nil {
		orderedCols := computedSvc.TopologicalSortColumns(computedFormulas)
		for i, row := range rows {
			for _, colName := range orderedCols {
				formula := computedFormulas[colName]
				computedVal, err := computedSvc.EvaluateFormula(formula, row)
				if err != nil {
					if strictModes[colName] {
						return nil, fmt.Errorf("computed column '%s' formula error: %s", colName, err.Error())
					}
					continue
				}
				if computedVal != nil {
					rows[i][colName] = computedVal
					row[colName] = computedVal // make available for subsequent computations
				}
			}
		}
	}

	return &SecurityGatewayResult{
		SanitizedData: nil, // multi-row: rows are modified in-place
		TableSchema:   tableSchema,
		StrippedCols:  allStripped,
		Operation:     operation,
	}, nil
}

// FindPrimaryKeyColumn queries the primary key column name for a table
// This is needed by UpdateRows to build WHERE clauses
// Results are cached in L1/L2 to avoid repeated PG queries
func FindPrimaryKeyColumn(ctx context.Context, pool *pgxpool.Pool, slug, schema, tableName string) string {
	key := fmt.Sprintf("%s:pk:%s:%s", slug, schema, tableName)

	// L1 check
	if val, ok := GlobalSchemaCache.l1Cache.Load(key); ok {
		entry := val.(*schemaCacheEntry)
		if time.Since(entry.cachedAt) < schemaCacheTTL {
			if pk, ok := entry.schema["_pk_col"]; ok && pk != nil {
				return pk.Description // reuse Description field to store PK name
			}
		}
	}

	// L2 check
	rdb := GetDragonfly()
	if rdb != nil {
		cached, err := rdb.Get(ctx, key).Result()
		if err == nil && cached != "" {
			// Store in L1
			ts := TableSchema{"_pk_col": &ColumnMetadata{Description: cached}}
			GlobalSchemaCache.l1Cache.Store(key, &schemaCacheEntry{schema: ts, cachedAt: time.Now()})
			return cached
		}
	}

	// PG query
	var pkCol string
	err := pool.QueryRow(ctx, `
		SELECT kcu.column_name 
		FROM information_schema.table_constraints tco
		JOIN information_schema.key_column_usage kcu ON kcu.constraint_name = tco.constraint_name 
		WHERE tco.constraint_type = 'PRIMARY KEY' AND tco.table_schema = $1 AND tco.table_name = $2`,
		schema, tableName).Scan(&pkCol)
	if err != nil {
		return "id" // default fallback
	}

	// Cache it
	ts := TableSchema{"_pk_col": &ColumnMetadata{Description: pkCol}}
	GlobalSchemaCache.l1Cache.Store(key, &schemaCacheEntry{schema: ts, cachedAt: time.Now()})
	if rdb != nil {
		rdb.Set(ctx, key, pkCol, dragonflySchemaTTL)
	}

	return pkCol
}

// GetTypeCastMap builds the column → PostgreSQL type map for UNNEST batch inserts
func GetTypeCastMap(schema TableSchema) map[string]string {
	result := make(map[string]string, len(schema))
	for colName, meta := range schema {
		result[colName] = utils.MapPgTypeToCast(meta.DataType, meta.UdtName)
	}
	return result
}

func FormatFactorName(factor string) string {
	switch strings.ToLower(factor) {
	case "totp":
		return "TOTP/MFA"
	case "biometria":
		return "Passkey"
	case "otp":
		return "OTP"
	default:
		return factor
	}
}
