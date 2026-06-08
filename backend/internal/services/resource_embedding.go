package services

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"strings"

	"cascata-backend/internal/utils"
	"github.com/jackc/pgx/v5/pgxpool"
)

// SelectField represents a single field in a SELECT clause
type SelectField struct {
	// For simple columns: "id" -> Table: "", Column: "id"
	// For embedded resources: "product_catalog(name)" -> Table: "product_catalog", Column: "name"
	// For nested: "product_catalog(category(name))" -> Table: "product_catalog", Column: "category(name)"
	Table      string       `json:"table"`
	Column     string       `json:"column"`
	Alias      string       `json:"alias,omitempty"`
	IsEmbedded bool         `json:"isEmbedded"`
	Children   []SelectField `json:"children,omitempty"` // For nested embedding
}

// JoinClause represents a JOIN clause in SQL
type JoinClause struct {
	JoinType       string `json:"joinType"` // "LEFT JOIN", "INNER JOIN", etc.
	TableAlias     string `json:"tableAlias"`
	TableName      string `json:"tableName"`
	OnClause       string `json:"onClause"`
	ParentTable    string `json:"parentTable"`
	ParentColumn   string `json:"parentColumn"`
	ForeignTable   string `json:"foreignTable"`
	ForeignColumn  string `json:"foreignColumn"`
}

// ResourceEmbeddingParser parses PostgREST-style resource embedding syntax
type ResourceEmbeddingParser struct {
	fkCache *ForeignKeyCache
}

// NewResourceEmbeddingParser creates a new parser instance
func NewResourceEmbeddingParser(fkCache *ForeignKeyCache) *ResourceEmbeddingParser {
	return &ResourceEmbeddingParser{
		fkCache: fkCache,
	}
}

// ParseSelect parses a PostgREST-style select parameter into SelectField structs
// Examples:
//   "id,name" -> [{Table: "", Column: "id"}, {Table: "", Column: "name"}]
//   "id,product_catalog(name)" -> [{Table: "", Column: "id"}, {Table: "product_catalog", Column: "name", IsEmbedded: true}]
//   "product_catalog(category(name))" -> [{Table: "product_catalog", Column: "category(name)", IsEmbedded: true, Children: [...]}]
func (p *ResourceEmbeddingParser) ParseSelect(selectParam string) ([]SelectField, error) {
	if selectParam == "" || selectParam == "*" {
		return []SelectField{{Table: "", Column: "*", IsEmbedded: false}}, nil
	}

	fields := []SelectField{}
	parts := strings.Split(selectParam, ",")

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		field, err := p.parseField(part)
		if err != nil {
			return nil, fmt.Errorf("error parsing field '%s': %w", part, err)
		}
		fields = append(fields, field)
	}

	return fields, nil
}

// parseField parses a single field (can be simple, embedded, or nested)
func (p *ResourceEmbeddingParser) parseField(field string) (SelectField, error) {
	// Check for alias syntax: "column:alias"
	if strings.Contains(field, ":") && !strings.Contains(field, "::") {
		bits := strings.SplitN(field, ":", 2)
		baseField := strings.TrimSpace(bits[0])
		alias := strings.TrimSpace(bits[1])

		parsed, err := p.parseField(baseField)
		if err != nil {
			return SelectField{}, err
		}
		parsed.Alias = alias
		return parsed, nil
	}

	// Check for embedding syntax: "table(column)" or "table(column1,column2)"
	openParen := strings.Index(field, "(")
	closeParen := strings.LastIndex(field, ")")

	if openParen > 0 && closeParen > openParen {
		// This is an embedded resource
		tableName := strings.TrimSpace(field[:openParen])
		innerContent := strings.TrimSpace(field[openParen+1 : closeParen])

		// Validate table name
		if !isValidIdentifier(tableName) {
			return SelectField{}, fmt.Errorf("invalid table name '%s'", tableName)
		}

		// Parse inner content (can be nested)
		innerFields, err := p.ParseSelect(innerContent)
		if err != nil {
			return SelectField{}, fmt.Errorf("error parsing embedded fields for '%s': %w", tableName, err)
		}

		// Check if any inner field is itself embedded (nested)
		hasNested := false
		children := []SelectField{}
		for _, inner := range innerFields {
			if inner.IsEmbedded {
				hasNested = true
				children = append(children, inner)
			}
		}

		// If we have nested embedding, the column is the parent of the nested
		// Otherwise, the column is the embedded table itself
		if hasNested {
			// Find the non-embedded fields
			simpleColumns := []string{}
			for _, inner := range innerFields {
				if !inner.IsEmbedded {
					simpleColumns = append(simpleColumns, inner.Column)
				}
			}

			return SelectField{
				Table:      tableName,
				Column:     strings.Join(simpleColumns, ","),
				IsEmbedded: true,
				Children:   children,
			}, nil
		}

		// Simple embedding: table(column1,column2)
		return SelectField{
			Table:      tableName,
			Column:     innerContent,
			IsEmbedded: true,
		}, nil
	}

	// Simple column
	if !isValidIdentifier(field) {
		return SelectField{}, fmt.Errorf("invalid column name '%s'", field)
	}

	return SelectField{
		Table:      "",
		Column:     field,
		IsEmbedded: false,
	}, nil
}

// BuildSelectWithJoins builds the SELECT clause and JOIN clauses from parsed fields
// Returns: (selectClause, joinClauses, error)
func (p *ResourceEmbeddingParser) BuildSelectWithJoins(
	ctx context.Context,
	pool *pgxpool.Pool,
	slug, schema, baseTable string,
	fields []SelectField,
) (string, []JoinClause, error) {
	selectParts := []string{}
	joinClauses := []JoinClause{}
	tableAliasCounter := 1

	// Process each field
	for _, field := range fields {
		if field.Column == "*" {
			selectParts = append(selectParts, "*")
			continue
		}

		if !field.IsEmbedded {
			// Simple column
			if field.Alias != "" {
				selectParts = append(selectParts, fmt.Sprintf("%s AS %s", utils.QuoteId(field.Column), utils.QuoteId(field.Alias)))
			} else {
				selectParts = append(selectParts, utils.QuoteId(field.Column))
			}
		} else {
			// Embedded resource - need to build JOIN
			joins, embeddedSelect, err := p.buildEmbeddedResource(
				ctx, pool, slug, schema, baseTable, field, &tableAliasCounter,
			)
			if err != nil {
				return "", nil, err
			}
			joinClauses = append(joinClauses, joins...)
			selectParts = append(selectParts, embeddedSelect...)
		}
	}

	selectClause := strings.Join(selectParts, ", ")
	return selectClause, joinClauses, nil
}

// buildEmbeddedResource builds JOIN clauses for an embedded resource
func (p *ResourceEmbeddingParser) buildEmbeddedResource(
	ctx context.Context,
	pool *pgxpool.Pool,
	slug, schema, parentTable string,
	field SelectField,
	aliasCounter *int,
) ([]JoinClause, []string, error) {
	// Find FK relationship from parentTable to field.Table
	fks := p.fkCache.GetForeignKeys(ctx, pool, slug, schema, parentTable)

	// Look for FK that points to the embedded table
	var matchingFK *ForeignKey
	for _, fk := range fks {
		if fk.ForeignTableName == field.Table {
			matchingFK = &fk
			break
		}
	}

	if matchingFK == nil {
		// Try to find reverse FK (embedded table has FK to parent)
		reverseFKs := p.fkCache.FindReverseForeignKey(ctx, pool, slug, schema, field.Table)
		for _, fk := range reverseFKs {
			if fk.ForeignTableName == parentTable {
				matchingFK = &fk
				break
			}
		}
	}

	if matchingFK == nil {
		return nil, nil, fmt.Errorf("no foreign key relationship found between '%s' and '%s'", parentTable, field.Table)
	}

	// Generate table alias
	tableAlias := fmt.Sprintf("t%d", *aliasCounter)
	*aliasCounter++

	// Build JOIN clause
	joinClause := JoinClause{
		JoinType:      "LEFT JOIN",
		TableAlias:    tableAlias,
		TableName:     fmt.Sprintf("%s.%s", utils.QuoteId(matchingFK.ForeignTableSchema), utils.QuoteId(matchingFK.ForeignTableName)),
		OnClause:      fmt.Sprintf("%s.%s = %s.%s", utils.QuoteId(parentTable), utils.QuoteId(matchingFK.ColumnName), tableAlias, utils.QuoteId(matchingFK.ForeignColumnName)),
		ParentTable:   parentTable,
		ParentColumn:  matchingFK.ColumnName,
		ForeignTable:  matchingFK.ForeignTableName,
		ForeignColumn: matchingFK.ForeignColumnName,
	}

	joins := []JoinClause{joinClause}

	// Parse the columns to select from the embedded table
	columnParts := strings.Split(field.Column, ",")
	selectParts := []string{}

	for _, colPart := range columnParts {
		colPart = strings.TrimSpace(colPart)
		if colPart == "" {
			continue
		}

		// Check if this is a nested embedding
		if strings.Contains(colPart, "(") {
			// Parse nested field
			nestedField, err := p.parseField(colPart)
			if err != nil {
				return nil, nil, fmt.Errorf("error parsing nested field '%s': %w", colPart, err)
			}

			// Build nested joins
			nestedJoins, nestedSelect, err := p.buildEmbeddedResource(
				ctx, pool, slug, schema, tableAlias, nestedField, aliasCounter,
			)
			if err != nil {
				return nil, nil, err
			}
			joins = append(joins, nestedJoins...)
			selectParts = append(selectParts, nestedSelect...)
		} else {
			// Simple column from embedded table
			if field.Alias != "" {
				selectParts = append(selectParts, fmt.Sprintf("%s.%s AS %s", tableAlias, utils.QuoteId(colPart), utils.QuoteId(field.Alias)))
			} else {
				selectParts = append(selectParts, fmt.Sprintf("%s.%s", tableAlias, utils.QuoteId(colPart)))
			}
		}
	}

	return joins, selectParts, nil
}

// isValidIdentifier checks if a string is a valid SQL identifier
func isValidIdentifier(s string) bool {
	if s == "" {
		return false
	}
	// Allow alphanumeric and underscore
	re := regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)
	return re.MatchString(s)
}


// FormatJoinClauses formats join clauses into SQL
func FormatJoinClauses(joins []JoinClause) string {
	if len(joins) == 0 {
		return ""
	}

	parts := []string{}
	for _, join := range joins {
		parts = append(parts, fmt.Sprintf("%s %s AS %s ON %s", join.JoinType, join.TableName, join.TableAlias, join.OnClause))
	}

	return strings.Join(parts, " ")
}

// ParseSelectWithEmbedding is a convenience function that parses select and builds SQL
func ParseSelectWithEmbedding(
	ctx context.Context,
	pool *pgxpool.Pool,
	slug, schema, baseTable, selectParam string,
) (string, string, error) {
	parser := NewResourceEmbeddingParser(GlobalForeignKeyCache)
	fields, err := parser.ParseSelect(selectParam)
	if err != nil {
		return "", "", err
	}

	selectClause, joins, err := parser.BuildSelectWithJoins(ctx, pool, slug, schema, baseTable, fields)
	if err != nil {
		return "", "", err
	}

	joinClause := FormatJoinClauses(joins)
	return selectClause, joinClause, nil
}
