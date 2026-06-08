package services

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ForeignKey represents a foreign key relationship
type ForeignKey struct {
	TableSchema       string `json:"tableSchema"`
	TableName         string `json:"tableName"`
	ColumnName        string `json:"columnName"`
	ForeignTableSchema string `json:"foreignTableSchema"`
	ForeignTableName   string `json:"foreignTableName"`
	ForeignColumnName  string `json:"foreignColumnName"`
	ConstraintName     string `json:"constraintName"`
}

// ForeignKeyCache caches foreign key relationships
type ForeignKeyCache struct {
	l1Cache sync.Map // key: "{slug}:{schema}:{table}" → value: *fkCacheEntry
}

type fkCacheEntry struct {
	fks      []ForeignKey
	cachedAt time.Time
}

const (
	fkCacheTTL = 10 * time.Minute
)

// Global ForeignKeyCache instance
var GlobalForeignKeyCache = &ForeignKeyCache{}

// GetForeignKeys retrieves foreign keys for a table (with caching)
func (fc *ForeignKeyCache) GetForeignKeys(
	ctx context.Context,
	pool *pgxpool.Pool,
	slug, schema, tableName string,
) []ForeignKey {
	key := fmt.Sprintf("%s:fk:%s:%s.%s", slug, schema, schema, tableName)

	// === L1: In-Memory Cache ===
	if val, ok := fc.l1Cache.Load(key); ok {
		entry := val.(*fkCacheEntry)
		if time.Since(entry.cachedAt) < fkCacheTTL {
			return entry.fks
		}
	}

	// === L2: PostgreSQL (Cold Start) ===
	fks := fc.loadFromDatabase(ctx, pool, schema, tableName)

	// Store in L1
	fc.l1Cache.Store(key, &fkCacheEntry{fks: fks, cachedAt: time.Now()})

	return fks
}

// loadFromDatabase fetches foreign keys from PostgreSQL
func (fc *ForeignKeyCache) loadFromDatabase(
	ctx context.Context,
	pool *pgxpool.Pool,
	schema, tableName string,
) []ForeignKey {
	fks := []ForeignKey{}

	query := `
		SELECT 
			tc.table_schema,
			tc.table_name,
			kcu.column_name,
			ccu.table_schema AS foreign_table_schema,
			ccu.table_name AS foreign_table_name,
			ccu.column_name AS foreign_column_name,
			tc.constraint_name
		FROM information_schema.table_constraints AS tc 
		JOIN information_schema.key_column_usage AS kcu
			ON tc.constraint_name = kcu.constraint_name
			AND tc.table_schema = kcu.table_schema
		JOIN information_schema.constraint_column_usage AS ccu
			ON ccu.constraint_name = tc.constraint_name
			AND ccu.table_schema = tc.table_schema
		WHERE tc.constraint_type = 'FOREIGN KEY' 
			AND tc.table_schema = $1 
			AND tc.table_name = $2
		ORDER BY tc.constraint_name, kcu.ordinal_position
	`

	rows, err := pool.Query(ctx, query, schema, tableName)
	if err != nil {
		log.Printf("[FKDiscovery] Failed to fetch FKs for %s.%s: %v", schema, tableName, err)
		return fks
	}
	defer rows.Close()

	for rows.Next() {
		var fk ForeignKey
		if err := rows.Scan(
			&fk.TableSchema,
			&fk.TableName,
			&fk.ColumnName,
			&fk.ForeignTableSchema,
			&fk.ForeignTableName,
			&fk.ForeignColumnName,
			&fk.ConstraintName,
		); err != nil {
			log.Printf("[FKDiscovery] Scan error for FK: %v", err)
			continue
		}
		fks = append(fks, fk)
	}

	return fks
}

// InvalidateForeignKeys removes cached FKs for a specific table
func (fc *ForeignKeyCache) InvalidateForeignKeys(slug, schema, tableName string) {
	key := fmt.Sprintf("%s:fk:%s:%s.%s", slug, schema, schema, tableName)
	fc.l1Cache.Delete(key)
	log.Printf("[FKDiscovery] Invalidated FK cache for %s", key)
}

// InvalidateProjectForeignKeys removes ALL cached FKs for a project
func (fc *ForeignKeyCache) InvalidateProjectForeignKeys(slug string) {
	prefix := slug + ":fk:"

	fc.l1Cache.Range(func(k, v interface{}) bool {
		if key, ok := k.(string); ok {
			if len(key) > len(prefix) && key[:len(prefix)] == prefix {
				fc.l1Cache.Delete(k)
			}
		}
		return true
	})

	log.Printf("[FKDiscovery] Invalidated ALL FK caches for project %s", slug)
}

// FindForeignKeyByColumn finds a FK relationship for a specific column
func (fc *ForeignKeyCache) FindForeignKeyByColumn(
	ctx context.Context,
	pool *pgxpool.Pool,
	slug, schema, tableName, columnName string,
) *ForeignKey {
	fks := fc.GetForeignKeys(ctx, pool, slug, schema, tableName)
	for _, fk := range fks {
		if fk.ColumnName == columnName {
			return &fk
		}
	}
	return nil
}

// FindReverseForeignKey finds FKs that point to this table
func (fc *ForeignKeyCache) FindReverseForeignKey(
	ctx context.Context,
	pool *pgxpool.Pool,
	slug, schema, tableName string,
) []ForeignKey {
	// This requires a different query - find FKs where this table is the foreign table
	reverseFks := []ForeignKey{}

	query := `
		SELECT 
			tc.table_schema,
			tc.table_name,
			kcu.column_name,
			ccu.table_schema AS foreign_table_schema,
			ccu.table_name AS foreign_table_name,
			ccu.column_name AS foreign_column_name,
			tc.constraint_name
		FROM information_schema.table_constraints AS tc 
		JOIN information_schema.key_column_usage AS kcu
			ON tc.constraint_name = kcu.constraint_name
			AND tc.table_schema = kcu.table_schema
		JOIN information_schema.constraint_column_usage AS ccu
			ON ccu.constraint_name = tc.constraint_name
			AND ccu.table_schema = tc.table_schema
		WHERE tc.constraint_type = 'FOREIGN KEY' 
			AND ccu.table_schema = $1 
			AND ccu.table_name = $2
		ORDER BY tc.constraint_name, kcu.ordinal_position
	`

	rows, err := pool.Query(ctx, query, schema, tableName)
	if err != nil {
		log.Printf("[FKDiscovery] Failed to fetch reverse FKs for %s.%s: %v", schema, tableName, err)
		return reverseFks
	}
	defer rows.Close()

	for rows.Next() {
		var fk ForeignKey
		if err := rows.Scan(
			&fk.TableSchema,
			&fk.TableName,
			&fk.ColumnName,
			&fk.ForeignTableSchema,
			&fk.ForeignTableName,
			&fk.ForeignColumnName,
			&fk.ConstraintName,
		); err != nil {
			log.Printf("[FKDiscovery] Scan error for reverse FK: %v", err)
			continue
		}
		reverseFks = append(reverseFks, fk)
	}

	return reverseFks
}
