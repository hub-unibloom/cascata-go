package middleware

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"

	"cascata-backend/internal/types"
)

// AppClientTableAccess controls table-level access based on App Client configuration
// This middleware enforces that App Clients can only access tables they are explicitly allowed to
func AppClientTableAccess(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Extract context
		val := r.Context().Value(types.CascataCtxKey)
		if val == nil {
			next.ServeHTTP(w, r)
			return
		}
		ctx := val.(*types.CascataRequest)

		// Skip if no project context or no App Client context
		// (Requests with regular anon_key bypass this restriction)
		if ctx.Project == nil || ctx.AppClient == nil {
			next.ServeHTTP(w, r)
			return
		}

		// Extract table name from various URL patterns
		tableName := extractTableNameFromPath(r.URL.Path, ctx.Project.Slug)
		if tableName == "" {
			// Not a table access request, allow through
			next.ServeHTTP(w, r)
			return
		}

		appClient := ctx.AppClient

		// Check blocked tables first (deny takes precedence)
		if len(appClient.BlockedTables) > 0 {
			for _, blocked := range appClient.BlockedTables {
				if strings.EqualFold(blocked, tableName) {
					log.Printf("[TableAccess] BLOCKED: AppClient %s attempted to access blocked table %s", appClient.ID, tableName)
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusForbidden)
					json.NewEncoder(w).Encode(map[string]interface{}{
						"error":   "Table access denied",
						"code":    "TABLE_ACCESS_DENIED",
						"table":   tableName,
						"app":     appClient.ID,
						"message": "This table is blocked for this App Client",
					})
					return
				}
			}
		}

		// Check allowed tables
		// If AllowedTables is empty, allow all tables (except blocked ones already checked above)
		if len(appClient.AllowedTables) > 0 {
			tableAllowed := false
			for _, allowed := range appClient.AllowedTables {
				if strings.EqualFold(allowed, tableName) {
					tableAllowed = true
					break
				}
				// Support wildcard patterns like "public.*" or "orders_*"
				if matchTablePattern(allowed, tableName) {
					tableAllowed = true
					break
				}
			}

			if !tableAllowed {
				log.Printf("[TableAccess] DENIED: AppClient %s attempted to access table %s (not in allowed list)", appClient.ID, tableName)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				json.NewEncoder(w).Encode(map[string]interface{}{
					"error":   "Table access denied",
					"code":    "TABLE_ACCESS_DENIED",
					"table":   tableName,
					"app":     appClient.ID,
					"message": "This table is not in the allowed list for this App Client",
				})
				return
			}
		}

		log.Printf("[TableAccess] ALLOWED: AppClient %s accessing table %s", appClient.ID, tableName)
		next.ServeHTTP(w, r)
	})
}

// extractTableNameFromPath extracts table name from various REST URL patterns
func extractTableNameFromPath(path, projectSlug string) string {
	path = strings.ToLower(path)

	// Pattern: /api/data/{slug}/{tableName}
	// Pattern: /api/data/{slug}/rest/v1/{tableName}
	// Pattern: /api/data/{slug}/tables/{tableName}

	// Remove prefix
	prefix := "/api/data/" + projectSlug + "/"
	if strings.HasPrefix(path, prefix) {
		remainder := strings.TrimPrefix(path, prefix)

		// Handle /rest/v1/{tableName}
		if strings.HasPrefix(remainder, "rest/v1/") {
			parts := strings.Split(remainder, "/")
			if len(parts) >= 3 {
				return parts[2]
			}
		}

		// Handle /tables/{tableName}
		if strings.HasPrefix(remainder, "tables/") {
			parts := strings.Split(remainder, "/")
			if len(parts) >= 2 {
				return parts[1]
			}
		}

		// Handle direct /{tableName}
		// Skip known non-table paths
		knownPaths := []string{"metadata", "logs", "logs/stats", "logs/export", "schemas", "stats", "tables", "functions", "query", "branch", "assets", "ai", "docs", "realtime"}
		parts := strings.Split(remainder, "/")
		if len(parts) >= 1 && parts[0] != "" {
			firstPart := parts[0]
			isKnown := false
			for _, known := range knownPaths {
				if firstPart == known {
					isKnown = true
					break
				}
			}
			if !isKnown {
				return firstPart
			}
		}
	}

	return ""
}

// matchTablePattern checks if table name matches a pattern with wildcards
// Patterns: "public.*", "orders_*", "*backup*"
func matchTablePattern(pattern, tableName string) bool {
	pattern = strings.ToLower(pattern)
	tableName = strings.ToLower(tableName)

	// Handle wildcard at end: "orders_*"
	if strings.HasSuffix(pattern, "*") {
		prefix := strings.TrimSuffix(pattern, "*")
		if strings.HasPrefix(tableName, prefix) {
			return true
		}
	}

	// Handle wildcard at start: "*backup"
	if strings.HasPrefix(pattern, "*") {
		suffix := strings.TrimPrefix(pattern, "*")
		if strings.HasSuffix(tableName, suffix) {
			return true
		}
	}

	// Handle wildcard in middle or multiple wildcards using simple approach
	if strings.Contains(pattern, "*") {
		// Convert to prefix/suffix matching
		parts := strings.Split(pattern, "*")
		if len(parts) == 2 {
			// Pattern like "abc*xyz"
			if strings.HasPrefix(tableName, parts[0]) && strings.HasSuffix(tableName, parts[1]) {
				return true
			}
		}
	}

	return pattern == tableName
}
