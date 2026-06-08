package services

import (
	"context"
	"fmt"
	"log"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ============================================================
// Phantom Injection Architecture — Go Implementation
// Core mission: Share heavy extensions across tenants without bloat
// ============================================================

// PhantomSource maps extensions to their Docker image source
// Only extensions NOT native to postgres:18-alpine are listed here
type PhantomSource struct {
	Image       string   // Docker image to extract from
	Provides    []string // List of extensions provided by this image
	EstimateMB  int      // Estimated size impact in MB
	Description string   // Human-readable description
}

// ExtensionStatus tracks the real-time state of extension operations
type ExtensionStatus struct {
	Status    string // "available", "injecting", "ready", "installed", "error"
	Message   string
	StartedAt int64
}

// ExtensionInfo represents enriched extension data for API responses
type ExtensionInfo struct {
	Name             string  `json:"name"`
	Category         string  `json:"category"`
	Description      string  `json:"description"`
	Featured         bool    `json:"featured"`
	Origin           string  `json:"origin"` // "native", "preloaded", "phantom"
	Status           string  `json:"status"` // "available", "ready", "installed", "error"
	InstalledVersion *string `json:"installed_version"`
	DefaultVersion   string  `json:"default_version"`
	SourceImage      *string `json:"source_image"`
	EstimateMB       int     `json:"estimate_mb"`
	Tier             int     `json:"tier"` // 0=native/preloaded, 1=phantom-light, 2=phantom-geo, 3=phantom-heavy
}

// Volume paths (inside the backend container, mounted for Docker operations)
const (
	ExtensionsVolume = "cascata_extension_payloads"
)

// ============================================================
// Extension Registry — Static Catalog + Dynamic Injection State
// ============================================================

var (
	// PHANTOM_SOURCES: Extensions that need Docker extraction (not native to Alpine)
	phantomSources = []PhantomSource{
		{
			Image:       "pgvector/pgvector:0.8.0-pg18",
			Provides:    []string{"vector"},
			EstimateMB:  5,
			Description: "pgvector — AI/RAG vector embeddings",
		},
		{
			Image:       "postgis/postgis:18-3.6-alpine",
			Provides:    []string{"postgis", "postgis_tiger_geocoder", "postgis_topology", "address_standardizer", "address_standardizer_data_us"},
			EstimateMB:  80,
			Description: "PostGIS — Geospatial functions",
		},
		{
			Image:       "timescale/timescaledb-ha:pg18",
			Provides:    []string{"timescaledb"},
			EstimateMB:  35,
			Description: "TimescaleDB — Time-series data",
		},
	}

	// NATIVE_EXTENSIONS: Always available in postgres:18-alpine
	nativeExtensions = map[string]bool{
		"plpgsql": true, "pgcrypto": true, "uuid-ossp": true, "pg_trgm": true,
		"citext": true, "hstore": true, "ltree": true, "btree_gin": true,
		"btree_gist": true, "fuzzystrmatch": true, "unaccent": true,
		"intarray": true, "earthdistance": true, "cube": true, "seg": true,
		"isn": true, "dict_int": true, "dict_xsyn": true,
		"postgres_fdw": true, "dblink": true, "amcheck": true,
		"pageinspect": true, "pg_buffercache": true, "pg_freespacemap": true,
		"pg_visibility": true, "pg_walinspect": true, "moddatetime": true,
		"autoinc": true, "insert_username": true, "pgaudit": true, "plpython3u": true,
	}

	// PRELOADED_EXTENSIONS: Compiled/installed via Dockerfile
	preloadedExtensions = map[string]bool{
		"pg_cron":            true,
		"pg_stat_statements": true,
	}

	// Build reverse lookup: extension name -> phantom source
	phantomLookup = buildPhantomLookup()

	// In-memory injection status tracking (per-extension)
	injectionStatus   = make(map[string]*ExtensionStatus)
	injectionStatusMu sync.RWMutex
)

// buildPhantomLookup creates the reverse mapping from extension name to source
func buildPhantomLookup() map[string]*PhantomSource {
	lookup := make(map[string]*PhantomSource)
	for i := range phantomSources {
		source := &phantomSources[i]
		for _, ext := range source.Provides {
			lookup[ext] = source
		}
	}
	return lookup
}

// ============================================================
// ExtensionService — Core Phantom Injection Logic
// ============================================================

type ExtensionService struct{}

// NewExtensionService creates a new ExtensionService instance
func NewExtensionService() *ExtensionService {
	return &ExtensionService{}
}

// IsPhantomExtension checks if an extension requires Docker injection
func (s *ExtensionService) IsPhantomExtension(name string) bool {
	_, exists := phantomLookup[name]
	return exists
}

// GetPhantomSource returns the PhantomSource for an extension, or nil
func (s *ExtensionService) GetPhantomSource(name string) *PhantomSource {
	if source, exists := phantomLookup[name]; exists {
		return source
	}
	return nil
}

// GetExtensionOrigin determines the origin type (native, preloaded, phantom)
func (s *ExtensionService) GetExtensionOrigin(name string) string {
	if preloadedExtensions[name] {
		return "preloaded"
	}
	if s.IsPhantomExtension(name) {
		return "phantom"
	}
	return "native"
}

// GetExtensionTier returns the tier classification (0-3)
func (s *ExtensionService) GetExtensionTier(name string) int {
	source := s.GetPhantomSource(name)
	if source == nil {
		return 0 // native/preloaded
	}
	// Tier 1: vector (light)
	if contains(source.Provides, "vector") {
		return 1
	}
	// Tier 2: postgis (geo - heavy)
	if contains(source.Provides, "postgis") {
		return 2
	}
	// Tier 3: everything else
	return 3
}

// GetInjectionStatus returns the current injection status for an extension
func (s *ExtensionService) GetInjectionStatus(name string) *ExtensionStatus {
	injectionStatusMu.RLock()
	defer injectionStatusMu.RUnlock()

	if status, exists := injectionStatus[name]; exists {
		return status
	}
	return &ExtensionStatus{
		Status:  "available",
		Message: "No operation in progress.",
	}
}

// setInjectionStatus updates the injection status (thread-safe)
func (s *ExtensionService) setInjectionStatus(name, status, message string) {
	injectionStatusMu.Lock()
	defer injectionStatusMu.Unlock()

	injectionStatus[name] = &ExtensionStatus{
		Status:    status,
		Message:   message,
		StartedAt: time.Now().UnixMilli(),
	}
}

// IsExtensionReady checks if an extension is ready (exists in pg_available_extensions)
func (s *ExtensionService) IsExtensionReady(ctx context.Context, pool *pgxpool.Pool, name string) (bool, error) {
	var exists bool
	err := pool.QueryRow(ctx,
		"SELECT EXISTS(SELECT 1 FROM pg_available_extensions WHERE name = $1)",
		name).Scan(&exists)
	return exists, err
}

// ============================================================
// Phantom Injection — Docker Image Extraction
// ============================================================

// PhantomInject performs the Docker extraction for phantom extensions
// This is the core of the True Phantom Injection architecture
func (s *ExtensionService) PhantomInject(ctx context.Context, name string) error {
	source := s.GetPhantomSource(name)
	if source == nil {
		return fmt.Errorf("extension %s is not a phantom extension", name)
	}

	log.Printf("[ExtensionService] Phantom Injection starting for %q from %s", name, source.Image)

	s.setInjectionStatus(name, "injecting", fmt.Sprintf("Downloading %s...", source.Image))

	// Check if image is already pulled (avoid re-downloading)
	imagePulled := false
	checkCmd := exec.CommandContext(ctx, "docker", "image", "inspect", source.Image)
	if err := checkCmd.Run(); err == nil {
		imagePulled = true
		log.Printf("[ExtensionService] Image %s already cached locally", source.Image)
	}

	if imagePulled {
		s.setInjectionStatus(name, "injecting", "Extracting extension files...")
	} else {
		s.setInjectionStatus(name, "injecting", "Pulling Docker image (first time only)...")
	}

	// Build the extraction command
	// The magic: extract files from official image to shared volume
	extractCmd := buildExtractionCommand(source.Image)

	log.Printf("[ExtensionService] Executing: %s...", extractCmd[:min(120, len(extractCmd))])

	// Execute with timeout (5 minutes for initial pull)
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", extractCmd)
	output, err := cmd.CombinedOutput()
	if err != nil {
		s.setInjectionStatus(name, "error", fmt.Sprintf("Injection failed: %v", err))
		return fmt.Errorf("phantom injection failed: %w (output: %s)", err, string(output))
	}

	if !strings.Contains(string(output), "PHANTOM_INJECT_OK") {
		s.setInjectionStatus(name, "error", "Extraction may have failed")
		return fmt.Errorf("phantom injection may have failed: %s", string(output))
	}

	log.Printf("[ExtensionService] Phantom Injection complete for %s", strings.Join(source.Provides, ", "))

	// Record all provided extensions in the registry
	for _, ext := range source.Provides {
		s.setInjectionStatus(ext, "ready", "Extension files injected. Waiting for Phantom Linker...")
		s.recordInRegistry(ctx, ext, source.Image, source.EstimateMB)
	}

	// Wait for the Phantom Linker to detect and symlink the files
	return s.waitForLinker(ctx, name, 30*time.Second)
}

// buildExtractionCommand creates the Docker command to extract extension files
func buildExtractionCommand(image string) string {
	// Extract from three locations inside the official image:
	// 1. /usr/local/lib/postgresql/ → .so files (compiled extensions)
	// 2. /usr/local/share/postgresql/extension/ → .control + .sql files
	// 3. /usr/lib/ → OS native libraries (libgeos, libproj, etc)
	return fmt.Sprintf(`docker run --rm -v %s:/cascata_extensions --entrypoint sh %s -c "
		mkdir -p /cascata_extensions/lib /cascata_extensions/share /cascata_extensions/os_lib && \
		cp -rn /usr/local/lib/postgresql/*.so /cascata_extensions/lib/ 2>/dev/null || true && \
		cp -rn /usr/local/lib/postgresql/*.so.* /cascata_extensions/lib/ 2>/dev/null || true && \
		cp -rn /usr/local/share/postgresql/extension/* /cascata_extensions/share/ 2>/dev/null || true && \
		cp -n /usr/lib/*.so* /cascata_extensions/os_lib/ 2>/dev/null || true && \
		echo PHANTOM_INJECT_OK
	"`, ExtensionsVolume, image)
}

// waitForLinker polls pg_available_extensions until the extension appears
func (s *ExtensionService) waitForLinker(ctx context.Context, name string, timeout time.Duration) error {
	start := time.Now()
	pollInterval := 2 * time.Second

	log.Printf("[ExtensionService] Waiting for Phantom Linker to detect %q...", name)

	for time.Since(start) < timeout {
		// Check if extension is now available in PostgreSQL
		var exists bool
		err := SystemPool.QueryRow(ctx,
			"SELECT EXISTS(SELECT 1 FROM pg_available_extensions WHERE name = $1)",
			name).Scan(&exists)

		if err == nil && exists {
			log.Printf("[ExtensionService] %q detected by PostgreSQL. Ready to use.", name)
			s.setInjectionStatus(name, "ready", "Extension ready to install.")
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pollInterval):
			// Continue polling
		}
	}

	// Timeout reached - extension might still work, linker might be slow
	log.Printf("[ExtensionService] Timeout waiting for linker. Extension may still be available.")
	s.setInjectionStatus(name, "ready", "Extension may be available. Try installing.")
	return nil
}

// recordInRegistry records the injected extension in system.extension_registry
func (s *ExtensionService) recordInRegistry(ctx context.Context, name, sourceImage string, estimateMB int) {
	sql := `
		INSERT INTO system.extension_registry (extension_name, source_image, status, file_size_bytes)
		VALUES ($1, $2, 'injected', $3)
		ON CONFLICT (extension_name) DO UPDATE 
		SET status = 'injected', injected_at = NOW(), source_image = $2, file_size_bytes = $3
	`
	_, err := SystemPool.Exec(ctx, sql, name, sourceImage, estimateMB*1024*1024)
	if err != nil {
		// Registry table may not exist yet — non-fatal
		log.Printf("[ExtensionService:Warn] Failed to record %s in registry: %v", name, err)
	}
}

// ============================================================
// Extension Installation/Uninstallation
// ============================================================

// InstallExtension installs an extension, triggering phantom injection if needed
func (s *ExtensionService) InstallExtension(ctx context.Context, projectPool *pgxpool.Pool, projectSlug, name, targetSchema string) (map[string]interface{}, error) {
	// Validate extension name (security)
	if !regexp.MustCompile(`^[a-zA-Z0-9_-]+$`).MatchString(name) {
		return nil, fmt.Errorf("invalid extension name: only alphanumeric, underscore and hyphen allowed")
	}

	// Check if it's a phantom extension that needs injection
	if s.IsPhantomExtension(name) {
		isReady, err := s.IsExtensionReady(ctx, SystemPool, name)
		if err != nil {
			return nil, fmt.Errorf("failed to check extension readiness: %w", err)
		}

		if !isReady {
			// Trigger Phantom Injection
			log.Printf("[ExtensionService] Extension %s not ready, triggering phantom injection", name)
			if err := s.PhantomInject(ctx, name); err != nil {
				return nil, fmt.Errorf("phantom injection failed: %w", err)
			}
		}
	}

	// Special handling for pg_cron (runs on system database)
	if name == "pg_cron" {
		_, err := SystemPool.Exec(ctx, `CREATE EXTENSION IF NOT EXISTS "pg_cron" SCHEMA public CASCADE`)
		if err != nil {
			return nil, fmt.Errorf("failed to create pg_cron: %w", err)
		}
	} else {
		// Create the extension in the project's schema
		safeSchema := targetSchema
		if safeSchema != "public" {
			safeSchema = fmt.Sprintf("\"%s\"", targetSchema)
		}

		// Create schema if it doesn't exist
		if targetSchema != "public" {
			_, err := projectPool.Exec(ctx, fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", safeSchema))
			if err != nil {
				return nil, fmt.Errorf("failed to create schema: %w", err)
			}

			// Set search_path for dependent extensions (e.g., postgis_tiger_geocoder needs PostGIS types)
			_, err = projectPool.Exec(ctx, fmt.Sprintf("SET search_path TO \"$user\", public, %s", safeSchema))
			if err != nil {
				log.Printf("[ExtensionService:Warn] Failed to set search_path: %v", err)
			}
		}

		// Create the extension
		sql := fmt.Sprintf(`CREATE EXTENSION IF NOT EXISTS "%s" SCHEMA %s CASCADE`, name, safeSchema)
		_, err := projectPool.Exec(ctx, sql)
		if err != nil {
			return nil, fmt.Errorf("failed to create extension %s: %w", name, err)
		}

		// Add schema to search path and grant permissions (Supabase-style)
		if targetSchema != "public" {
			// Alter database search path
			_, _ = projectPool.Exec(ctx, fmt.Sprintf(`
				DO $$
				BEGIN
					EXECUTE format('ALTER DATABASE %%I SET search_path TO "\$user", public, %%I', current_database(), '%s');
				END
				$$;
			`, targetSchema))

			// Grant permissions to Cascata roles
			grantSQL := fmt.Sprintf(`
				GRANT USAGE ON SCHEMA %s TO anon, authenticated, service_role, cascata_api_role;
				GRANT SELECT ON ALL TABLES IN SCHEMA %s TO anon, authenticated, service_role, cascata_api_role;
				GRANT EXECUTE ON ALL FUNCTIONS IN SCHEMA %s TO anon, authenticated, service_role, cascata_api_role;
				GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA %s TO anon, authenticated, service_role, cascata_api_role;
				ALTER DEFAULT PRIVILEGES IN SCHEMA %s
					GRANT SELECT ON TABLES TO anon, authenticated, service_role, cascata_api_role;
				ALTER DEFAULT PRIVILEGES IN SCHEMA %s
					GRANT EXECUTE ON FUNCTIONS TO anon, authenticated, service_role, cascata_api_role;
			`, safeSchema, safeSchema, safeSchema, safeSchema, safeSchema, safeSchema)
			_, _ = projectPool.Exec(ctx, grantSQL)
		}
	}

	// Record in project_extensions registry
	s.recordProjectExtension(ctx, projectSlug, name)

	// Clear injection status
	s.setInjectionStatus(name, "installed", "Extension installed successfully.")

	return map[string]interface{}{
		"success":         true,
		"message":         fmt.Sprintf("Extension %q installed successfully.", name),
		"requiresPhantom": s.IsPhantomExtension(name),
	}, nil
}

// UninstallExtension removes an extension from a project
func (s *ExtensionService) UninstallExtension(ctx context.Context, projectPool *pgxpool.Pool, projectSlug, name string, cascade bool) (map[string]interface{}, error) {
	// Validate extension name
	if !regexp.MustCompile(`^[a-zA-Z0-9_-]+$`).MatchString(name) {
		return nil, fmt.Errorf("invalid extension name")
	}

	// Protect critical system extensions
	if name == "plpgsql" {
		return nil, fmt.Errorf("cannot drop protected system extension: %s", name)
	}

	cascadeStr := ""
	if cascade {
		cascadeStr = "CASCADE"
	}

	// Drop the extension
	if name == "pg_cron" {
		_, err := SystemPool.Exec(ctx, fmt.Sprintf(`DROP EXTENSION IF EXISTS "%s" %s`, name, cascadeStr))
		if err != nil {
			return nil, fmt.Errorf("failed to drop extension: %w", err)
		}
	} else {
		_, err := projectPool.Exec(ctx, fmt.Sprintf(`DROP EXTENSION IF EXISTS "%s" %s`, name, cascadeStr))
		if err != nil {
			return nil, fmt.Errorf("failed to drop extension: %w", err)
		}
	}

	// Remove from project registry
	_, _ = SystemPool.Exec(ctx,
		"DELETE FROM system.project_extensions WHERE project_slug = $1 AND extension_name = $2",
		projectSlug, name)

	return map[string]interface{}{
		"success": true,
		"message": fmt.Sprintf("Extension %q removed.", name),
	}, nil
}

// recordProjectExtension records the installation in project_extensions
func (s *ExtensionService) recordProjectExtension(ctx context.Context, projectSlug, name string) {
	// Get installed version
	var version *string
	err := SystemPool.QueryRow(ctx,
		"SELECT installed_version FROM pg_available_extensions WHERE name = $1",
		name).Scan(&version)
	if err != nil {
		version = nil
	}

	sql := `
		INSERT INTO system.project_extensions (project_slug, extension_name, installed_version)
		VALUES ($1, $2, $3)
		ON CONFLICT (project_slug, extension_name) DO UPDATE 
		SET installed_version = $3, installed_at = NOW()
	`
	_, _ = SystemPool.Exec(ctx, sql, projectSlug, name, version)
}

// ============================================================
// ListAvailableEnriched — Full Extension Catalog
// ============================================================

// ListAvailableEnriched returns all extensions with enriched metadata
// This is the main endpoint for the frontend extension panel
func (s *ExtensionService) ListAvailableEnriched(ctx context.Context, projectPool *pgxpool.Pool, projectSlug string) ([]ExtensionInfo, error) {
	// 1. Query what PostgreSQL actually has available (USE PROJECT POOL - NOT SYSTEM POOL)
	// CRITICAL: pg_available_extensions shows installed_version PER DATABASE
	pgRows, err := projectPool.Query(ctx, `
		SELECT name, default_version, installed_version, comment 
		FROM pg_available_extensions 
		ORDER BY name ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to query available extensions: %w", err)
	}
	defer pgRows.Close()

	pgMap := make(map[string]struct {
		defaultVersion   string
		installedVersion *string
		comment          string
	})

	for pgRows.Next() {
		var name, defV, comment string
		var instV *string
		if err := pgRows.Scan(&name, &defV, &instV, &comment); err == nil {
			pgMap[name] = struct {
				defaultVersion   string
				installedVersion *string
				comment          string
			}{defV, instV, comment}
		}
	}

	// 1b. Query SYSTEM extensions (pg_cron, etc) from SystemPool
	// These are system-level extensions installed in cascata_system
	systemRows, err := SystemPool.Query(ctx, `
		SELECT name, default_version, installed_version, comment 
		FROM pg_available_extensions 
		WHERE name IN ('pg_cron', 'pg_stat_statements')
		ORDER BY name ASC
	`)
	if err == nil {
		defer systemRows.Close()
		for systemRows.Next() {
			var name, defV, comment string
			var instV *string
			if err := systemRows.Scan(&name, &defV, &instV, &comment); err == nil {
				// Merge into pgMap - system extensions override project ones
				pgMap[name] = struct {
					defaultVersion   string
					installedVersion *string
					comment          string
				}{defV, instV, comment}
			}
		}
	}

	// 2. Query injection registry from system database
	injectedMap := make(map[string]struct {
		sourceImage string
		status      string
	})
	regRows, err := SystemPool.Query(ctx,
		"SELECT extension_name, source_image, status FROM system.extension_registry")
	if err == nil {
		defer regRows.Close()
		for regRows.Next() {
			var name, sourceImage, status string
			if err := regRows.Scan(&name, &sourceImage, &status); err == nil {
				injectedMap[name] = struct {
					sourceImage string
					status      string
				}{sourceImage, status}
			}
		}
	}
	// Non-fatal if table doesn't exist

	// 3. Build enriched catalog
	var enriched []ExtensionInfo
	processed := make(map[string]bool)

	// Process all known extensions (native + phantom)
	allKnown := make(map[string]bool)
	for ext := range nativeExtensions {
		allKnown[ext] = true
	}
	for ext := range preloadedExtensions {
		allKnown[ext] = true
	}
	for ext := range phantomLookup {
		allKnown[ext] = true
	}

	for ext := range allKnown {
		processed[ext] = true
		pgInfo := pgMap[ext]
		injectedInfo := injectedMap[ext]
		source := s.GetPhantomSource(ext)

		origin := s.GetExtensionOrigin(ext)
		tier := s.GetExtensionTier(ext)

		var sourceImage *string
		var estimateMB int
		if source != nil {
			sourceImage = &source.Image
			estimateMB = source.EstimateMB
		}

		// Determine effective status
		status := "available"
		if pgInfo.installedVersion != nil {
			status = "installed"
		} else if _, exists := pgMap[ext]; exists {
			status = "ready"
		} else if injectedInfo.status == "injected" {
			status = "ready"
		} else {
			// Check in-memory status
			memStatus := s.GetInjectionStatus(ext)
			if memStatus.Status != "available" {
				status = memStatus.Status
			}
		}

		enriched = append(enriched, ExtensionInfo{
			Name:             ext,
			Category:         getCategoryForExtension(ext),
			Description:      getDescriptionForExtension(ext, pgInfo.comment),
			Featured:         isFeaturedExtension(ext),
			Origin:           origin,
			Status:           status,
			InstalledVersion: pgInfo.installedVersion,
			DefaultVersion:   pgInfo.defaultVersion,
			SourceImage:      sourceImage,
			EstimateMB:       estimateMB,
			Tier:             tier,
		})
	}

	// Also include any extensions from pg_available_extensions that we didn't know about
	// (system extensions that Alpine ships but we didn't list)
	brokenAlpineRegex := regexp.MustCompile(`^(plperl|plperlu|bool_plperl|bool_plperlu|hstore_plperl|hstore_plperlu|jsonb_plperlu|plpython3.*|pltcl|pltclu|pgaudit)$`)

	for name, info := range pgMap {
		if !processed[name] && !brokenAlpineRegex.MatchString(name) {
			enriched = append(enriched, ExtensionInfo{
				Name:             name,
				Category:         "Util",
				Description:      info.comment,
				Featured:         false,
				Origin:           "native",
				Status:           map[bool]string{true: "installed", false: "ready"}[info.installedVersion != nil],
				InstalledVersion: info.installedVersion,
				DefaultVersion:   info.defaultVersion,
				SourceImage:      nil,
				EstimateMB:       0,
				Tier:             0,
			})
		}
	}

	// Sort: installed first, then featured, then by tier, then alphabetical
	sortExtensions(enriched)

	return enriched, nil
}

// ============================================================
// Helper Functions
// ============================================================

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func getCategoryForExtension(name string) string {
	categories := map[string]string{
		"vector": "AI",
		"postgis": "Geo", "postgis_tiger_geocoder": "Geo",
		"postgis_topology": "Geo", "address_standardizer": "Geo",
		"address_standardizer_data_us": "Geo", "earthdistance": "Geo",
		"pgcrypto": "Crypto",
		"pg_trgm": "Search", "fuzzystrmatch": "Search",
		"unaccent": "Search", "dict_int": "Search", "dict_xsyn": "Search",
		"btree_gin": "Index", "btree_gist": "Index",
		"uuid-ossp": "DataType", "hstore": "DataType", "citext": "DataType",
		"ltree": "DataType", "isn": "DataType", "cube": "DataType",
		"seg": "DataType", "intarray": "DataType",
		"pg_cron": "Util", "pg_stat_statements": "Audit",
		"pgaudit": "Audit", "timescaledb": "Time",
		"postgres_fdw": "Admin", "dblink": "Admin",
		"amcheck": "Admin", "pageinspect": "Admin",
		"pg_buffercache": "Admin", "pg_freespacemap": "Admin",
		"pg_visibility": "Admin", "pg_walinspect": "Admin",
		"moddatetime": "Util", "autoinc": "Util",
		"insert_username": "Util", "plpgsql": "Lang",
		"plpython3u": "Lang",
	}
	if cat, ok := categories[name]; ok {
		return cat
	}
	return "Util"
}

func getDescriptionForExtension(name, pgComment string) string {
	if pgComment != "" {
		return pgComment
	}

	descriptions := map[string]string{
		"vector":                     "Store and query vector embeddings. Essential for AI/RAG applications.",
		"postgis":                    "Spatial and geographic objects for PostgreSQL.",
		"postgis_tiger_geocoder":     "Tiger Geocoder for PostGIS.",
		"postgis_topology":           "Topology spatial types and functions.",
		"address_standardizer":       "Parse addresses into elements. Useful for geocoding normalization.",
		"address_standardizer_data_us": "US dataset for address standardizer.",
		"earthdistance":              "Calculate great circle distances on the surface of the Earth.",
		"pgcrypto":                   "Cryptographic functions (hashing, encryption, UUID generation).",
		"pg_trgm":                    "Text similarity measurement and index searching based on trigrams.",
		"fuzzystrmatch":              "Determine similarities and distances between strings.",
		"unaccent":                   "Text search dictionary that removes accents.",
		"uuid-ossp":                  "Functions to generate universally unique identifiers (UUIDs).",
		"hstore":                     "Data type for storing sets of (key, value) pairs.",
		"citext":                     "Case-insensitive character string type.",
		"ltree":                      "Hierarchical tree-like data structure.",
		"pg_cron":                    "Job scheduler for PostgreSQL (run SQL on a schedule).",
		"pg_stat_statements":         "Track execution statistics of all SQL statements executed.",
		"timescaledb":                "Scalable inserts and complex queries for time-series data.",
	}
	if desc, ok := descriptions[name]; ok {
		return desc
	}
	return "PostgreSQL extension."
}

func isFeaturedExtension(name string) bool {
	featured := map[string]bool{
		"vector": true, "postgis": true, "pgcrypto": true,
		"pg_trgm": true, "uuid-ossp": true, "pg_cron": true,
		"timescaledb": true,
	}
	return featured[name]
}

func sortExtensions(exts []ExtensionInfo) {
	// Simple bubble sort for the specific requirements
	// installed > featured > tier > alphabetical
	n := len(exts)
	for i := 0; i < n; i++ {
		for j := 0; j < n-i-1; j++ {
			shouldSwap := false

			// Installed first
			if exts[j].Status != "installed" && exts[j+1].Status == "installed" {
				shouldSwap = true
			} else if exts[j].Status == "installed" && exts[j+1].Status != "installed" {
				shouldSwap = false
			} else {
				// Featured second
				if !exts[j].Featured && exts[j+1].Featured {
					shouldSwap = true
				} else if exts[j].Featured && !exts[j+1].Featured {
					shouldSwap = false
				} else {
					// Tier third
					if exts[j].Tier > exts[j+1].Tier {
						shouldSwap = true
					} else if exts[j].Tier < exts[j+1].Tier {
						shouldSwap = false
					} else {
						// Alphabetical last
						if exts[j].Name > exts[j+1].Name {
							shouldSwap = true
						}
					}
				}
			}

			if shouldSwap {
				exts[j], exts[j+1] = exts[j+1], exts[j]
			}
		}
	}
}
