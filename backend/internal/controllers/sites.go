package controllers

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"cascata-backend/internal/services"

	"github.com/go-chi/chi/v5"
)

type SitesController struct{}

func NewSitesController() *SitesController {
	return &SitesController{}
}

// DeploySite handles POST /api/sites/deploy
func (c *SitesController) DeploySite(w http.ResponseWriter, r *http.Request) {
	// Parse Multipart Form
	err := r.ParseMultipartForm(100 << 20) // 100MB max upload
	if err != nil {
		http.Error(w, `{"error":"Failed to parse multipart form"}`, 400)
		return
	}

	// Try reading project_slug from URL parameter, fall back to Form
	projectSlug := chi.URLParam(r, "slug")
	if projectSlug == "" {
		projectSlug = r.FormValue("project_slug")
	}
	siteName := r.FormValue("name")
	customDomain := r.FormValue("domain")

	if projectSlug == "" || siteName == "" {
		http.Error(w, `{"error":"project_slug and name are required"}`, 400)
		return
	}

	// Check if project exists
	var exists bool
	err = services.SystemPool.QueryRow(r.Context(), "SELECT EXISTS(SELECT 1 FROM system.projects WHERE slug = $1)", projectSlug).Scan(&exists)
	if err != nil || !exists {
		http.Error(w, `{"error":"Project not found"}`, 404)
		return
	}

	// Slugify site name for directory path
	siteSlug := strings.ToLower(siteName)
	siteSlug = regexp.MustCompile(`[^a-z0-9\-]+`).ReplaceAllString(siteSlug, "-")
	siteSlug = strings.Trim(siteSlug, "-")
	if siteSlug == "" {
		http.Error(w, `{"error":"Invalid site name"}`, 400)
		return
	}

	// Auto-generate domain if not provided
	if customDomain == "" {
		var sysDomain string
		_ = services.SystemPool.QueryRow(r.Context(), "SELECT settings->>'domain' FROM system.ui_settings WHERE project_slug = '_system_root_' AND table_name = 'system_config'").Scan(&sysDomain)
		if sysDomain != "" {
			customDomain = siteSlug + "." + sysDomain
		} else {
			customDomain = siteSlug + ".localhost"
		}
	}

	// Normalize domain
	normalizedDomain, err := services.NormalizeDomain(customDomain)
	if err == nil {
		customDomain = normalizedDomain
	}

	// Read uploaded ZIP file
	file, _, err := r.FormFile("file")
	if err != nil {
		http.Error(w, `{"error":"ZIP file is required (form-data: file)"}`, 400)
		return
	}
	defer file.Close()

	// Save upload to temp file
	tempFile, err := os.CreateTemp("", "site-upload-*.zip")
	if err != nil {
		http.Error(w, `{"error":"Failed to create temporary file"}`, 500)
		return
	}
	defer os.Remove(tempFile.Name())
	defer tempFile.Close()

	if _, err := io.Copy(tempFile, file); err != nil {
		http.Error(w, `{"error":"Failed to save uploaded ZIP file"}`, 500)
		return
	}

	// Define path on local storage volume
	storageRoot := os.Getenv("STORAGE_ROOT")
	if storageRoot == "" {
		storageRoot = "./storage"
	}
	storagePath := filepath.Join(storageRoot, projectSlug, "_sites", siteSlug)

	// Ensure parent directories are world-traversable so nginx can read them
	_ = os.MkdirAll(storageRoot, 0755)
	_ = os.Chmod(storageRoot, 0755)
	projectStoragePath := filepath.Join(storageRoot, projectSlug)
	_ = os.MkdirAll(projectStoragePath, 0755)
	_ = os.Chmod(projectStoragePath, 0755)
	sitesBasePath := filepath.Join(storageRoot, projectSlug, "_sites")
	_ = os.MkdirAll(sitesBasePath, 0755)
	_ = os.Chmod(sitesBasePath, 0755)

	// Clean and recreate site directory
	_ = os.RemoveAll(storagePath)
	if err := os.MkdirAll(storagePath, 0755); err != nil {
		http.Error(w, `{"error":"Failed to create site storage directory"}`, 500)
		return
	}

	// Extract ZIP archive
	zipReader, err := zip.OpenReader(tempFile.Name())
	if err != nil {
		http.Error(w, `{"error":"Uploaded file is not a valid ZIP archive"}`, 400)
		return
	}
	defer zipReader.Close()

	for _, f := range zipReader.File {
		// Clean and verify extraction path to avoid zip slip vulnerability
		filePath := filepath.Join(storagePath, f.Name)
		if !strings.HasPrefix(filePath, filepath.Clean(storagePath)+string(os.PathSeparator)) && filePath != storagePath {
			continue // Skip dangerous path traversal
		}

		if f.FileInfo().IsDir() {
			// Force 0755 on directories — ignore ZIP's original mode
			os.MkdirAll(filePath, 0755)
			continue
		}

		if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
			continue
		}

		// Force 0644 on files — never trust ZIP's original mode
		dstFile, err := os.OpenFile(filePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
		if err != nil {
			continue
		}

		fileInZip, err := f.Open()
		if err != nil {
			dstFile.Close()
			continue
		}

		_, _ = io.Copy(dstFile, fileInZip)
		fileInZip.Close()
		dstFile.Close()
	}

	// Post-extraction: walk and normalize all permissions so Nginx (different user) can read,
	// and index every extracted file into system.storage_objects so the storage browser
	// can list the _sites bucket correctly (fixes {"items":null} response).
	_ = filepath.Walk(storagePath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip unreadable entries, don't abort
		}
		if info.IsDir() {
			if err := os.Chmod(path, 0755); err != nil {
				log.Printf("[SitesController] Failed to chmod directory %s: %v", path, err)
			}
		} else {
			if err := os.Chmod(path, 0644); err != nil {
				log.Printf("[SitesController] Failed to chmod file %s: %v", path, err)
			}
		}

		// Index each file/folder into system.storage_objects under the _sites bucket.
		// relPath is relative to storagePath so it starts at the site slug level.
		relPath := strings.TrimPrefix(path, storagePath)
		relPath = strings.TrimPrefix(relPath, string(os.PathSeparator))
		relPath = strings.ReplaceAll(relPath, "\\", "/")
		if relPath == "" {
			// Skip the root directory itself — it is already represented by the bucket
			return nil
		}
		isDir := info.IsDir()
		indexInfo := map[string]interface{}{
			"size":     info.Size(),
			"mimeType": "application/octet-stream",
			"isFolder": isDir,
			"provider": "local",
		}
		if isDir {
			indexInfo["mimeType"] = "application/directory"
		}
		// relPath inside the _sites bucket is: <siteSlug>/<rest>
		fullKey := filepath.Join(siteSlug, relPath)
		fullKey = strings.ReplaceAll(fullKey, "\\", "/")
		if indexErr := services.IndexObject(r.Context(), services.SystemPool, projectSlug, "_sites", fullKey, indexInfo); indexErr != nil {
			log.Printf("[SitesController] Warning: failed to index extracted path %s: %v", fullKey, indexErr)
		}
		return nil
	})

	// Ensure the _sites bucket root folder itself is indexed (visible in bucket list)
	if indexErr := services.IndexObject(r.Context(), services.SystemPool, projectSlug, "_sites", siteSlug, map[string]interface{}{
		"size":     int64(0),
		"mimeType": "application/directory",
		"isFolder": true,
		"provider": "local",
	}); indexErr != nil {
		log.Printf("[SitesController] Warning: failed to index site folder entry: %v", indexErr)
	}

	// Get project's ssl_certificate_source to inherit certificate
	var projectSSLCertSource string
	_ = services.SystemPool.QueryRow(r.Context(), `
		SELECT ssl_certificate_source FROM system.projects WHERE slug = $1
	`, projectSlug).Scan(&projectSSLCertSource)

	// Auto-detect certificate source for the site domain
	siteSSLCertSource := projectSSLCertSource
	if siteSSLCertSource == "" && customDomain != "" {
		siteSSLCertSource = services.DetectWildcardSource(customDomain)
	}

	// ── AUTO-DETECT ACTIVE FOLDER ──────────────────────────────────────────────
	// After extraction, inspect the top-level entries of storagePath.
	// If there is exactly one directory at the root (common pattern: the build
	// tool emits dist/ or <project-name>/) we treat that as the active_folder.
	// If there are files directly at root, active_folder stays empty (root itself
	// serves as the entry point — no subfolder needed).
	detectedFolder := ""
	if rootEntries, readErr := os.ReadDir(storagePath); readErr == nil {
		var topDirs []string
		topHasFiles := false
		for _, entry := range rootEntries {
			if entry.IsDir() {
				topDirs = append(topDirs, entry.Name())
			} else {
				topHasFiles = true
			}
		}
		// Only treat as a "subfolder build" when there are NO files at root and
		// exactly ONE directory — that directory is the extracted project folder.
		if !topHasFiles && len(topDirs) == 1 {
			candidate := topDirs[0]

			// Resolve name conflicts: if another site in this _sites bucket already
			// has a folder with the same name, rename the physical directory and
			// use the suffixed name so Nginx always points to a unique path.
			sitesBasePath := filepath.Join(storageRoot, projectSlug, "_sites")
			finalName := candidate
			for counter := 1; ; counter++ {
				conflictPath := filepath.Join(sitesBasePath, finalName)
				// A conflict exists only if there's a DIFFERENT site slug occupying that name
				if finalName == siteSlug {
					// Same site folder — no conflict, it's ours
					break
				}
				if _, statErr := os.Stat(conflictPath); os.IsNotExist(statErr) {
					break // Name is free
				}
				// Conflict: try candidate + counter
				finalName = fmt.Sprintf("%s%d", candidate, counter)
			}

			// If the extracted subfolder name differs from finalName (suffix was added),
			// rename the physical directory so the path is consistent.
			extractedSubPath := filepath.Join(storagePath, candidate)
			if candidate != finalName {
				targetSubPath := filepath.Join(storagePath, finalName)
				if renErr := os.Rename(extractedSubPath, targetSubPath); renErr != nil {
					log.Printf("[SitesController] Warning: could not rename extracted folder %s → %s: %v", candidate, finalName, renErr)
					finalName = candidate // Keep original if rename failed
				}
			}

			detectedFolder = finalName
			log.Printf("[SitesController] Auto-detected active_folder: %s (original: %s)", finalName, candidate)
		}
	}
	// ─────────────────────────────────────────────────────────────────────────

	// Update database record
	var siteID string
	err = services.SystemPool.QueryRow(r.Context(), `
		SELECT id::text FROM system.sites WHERE project_slug = $1 AND name = $2
	`, projectSlug, siteName).Scan(&siteID)

	if err != nil {
		// Site does not exist, insert it
		err = services.SystemPool.QueryRow(r.Context(), `
			INSERT INTO system.sites (project_slug, name, domain, storage_path, ssl_certificate_source, active_folder, status)
			VALUES ($1, $2, $3, $4, $5, $6, 'active')
			RETURNING id::text
		`, projectSlug, siteName, customDomain, storagePath, siteSSLCertSource, detectedFolder).Scan(&siteID)
	} else {
		// Update existing site — always refresh active_folder when re-deploying
		_, err = services.SystemPool.Exec(r.Context(), `
			UPDATE system.sites
			SET domain = $1, storage_path = $2, ssl_certificate_source = $3, active_folder = $4, status = 'active', updated_at = NOW()
			WHERE id = $5
		`, customDomain, storagePath, siteSSLCertSource, detectedFolder, siteID)
	}

	if err != nil {
		if strings.Contains(err.Error(), "duplicate key value violates unique constraint") {
			http.Error(w, `{"error":"Domain is already in use by another site or project"}`, 409)
			return
		}
		http.Error(w, fmt.Sprintf(`{"error":"Failed to save site metadata: %s"}`, err.Error()), 500)
		return
	}

	// Only request Let's Encrypt certificate if no certificate source is available
	// This respects the project's certificate configuration (Cloudflare, manual, etc.)
	if customDomain != "" && !strings.HasSuffix(customDomain, ".localhost") && siteSSLCertSource == "" {
		certSvc := services.CertificateService{CryptoSvc: &services.CryptoService{}}
		err = certSvc.RequestCertificate(r.Context(), services.SystemPool, customDomain, "admin@cascata.io", services.ProviderLetsEncrypt, "", "")
		if err != nil {
			log.Printf("[SitesController] Warning: Failed to request SSL certificate: %v", err)
		}
	}

	// Rebuild Nginx configs to serve the new site
	if err := services.RebuildNginxConfigs(r.Context(), services.SystemPool); err != nil {
		log.Printf("[SitesController] Warning: Failed to rebuild Nginx configurations: %v", err)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":         true,
		"id":              siteID,
		"name":            siteName,
		"domain":          customDomain,
		"storage_path":    storagePath,
		"active_folder":   detectedFolder,
		"status":          "active",
	})
}

// ListSites handles GET /api/sites
func (c *SitesController) ListSites(w http.ResponseWriter, r *http.Request) {
	// Try reading project_slug from URL parameter or query parameter
	projectSlug := chi.URLParam(r, "slug")
	if projectSlug == "" {
		projectSlug = r.URL.Query().Get("project_slug")
	}

	if projectSlug == "" {
		http.Error(w, `{"error":"project_slug is required"}`, 400)
		return
	}

	rows, err := services.SystemPool.Query(r.Context(), `
		SELECT id::text, name, domain, storage_path, status, created_at, updated_at, active_folder
		FROM system.sites
		WHERE project_slug = $1
		ORDER BY created_at DESC
	`, projectSlug)

	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"Failed to query sites: %s"}`, err.Error()), 500)
		return
	}
	defer rows.Close()

	sites := []map[string]interface{}{}
	for rows.Next() {
		var id, name, domain, storagePath, status, activeFolder string
		var createdAt, updatedAt interface{}
		if err := rows.Scan(&id, &name, &domain, &storagePath, &status, &createdAt, &updatedAt, &activeFolder); err != nil {
			continue
		}
		sites = append(sites, map[string]interface{}{
			"id":            id,
			"name":          name,
			"domain":        domain,
			"storage_path":  storagePath,
			"status":        status,
			"created_at":    createdAt,
			"updated_at":    updatedAt,
			"active_folder": activeFolder,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(sites)
}

// DeleteSite handles DELETE /api/sites/{id}
func (c *SitesController) DeleteSite(w http.ResponseWriter, r *http.Request) {
	siteID := chi.URLParam(r, "id")
	if siteID == "" {
		http.Error(w, `{"error":"Site ID is required"}`, 400)
		return
	}

	// Fetch site to find storage path and project ownership
	var storagePath, siteProjectSlug string
	err := services.SystemPool.QueryRow(r.Context(), `
		SELECT storage_path, project_slug FROM system.sites WHERE id = $1
	`, siteID).Scan(&storagePath, &siteProjectSlug)

	if err != nil {
		http.Error(w, `{"error":"Site not found"}`, 404)
		return
	}

	// Delete physical directory
	if storagePath != "" {
		_ = os.RemoveAll(storagePath)
	}

	// Unindex all storage objects for this site from the _sites bucket.
	// storagePath ends in <projectSlug>/_sites/<siteSlug>; extract the siteSlug.
	siteSlugForIndex := filepath.Base(storagePath)
	if siteProjectSlug != "" && siteSlugForIndex != "" && siteSlugForIndex != "." {
		if err := services.UnindexObject(r.Context(), services.SystemPool, siteProjectSlug, "_sites", siteSlugForIndex); err != nil {
			log.Printf("[SitesController] Warning: failed to unindex storage objects for site %s: %v", siteSlugForIndex, err)
		}
	}

	// Delete database record
	_, err = services.SystemPool.Exec(r.Context(), "DELETE FROM system.sites WHERE id = $1", siteID)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"Failed to delete site from registry: %s"}`, err.Error()), 500)
		return
	}

	// Rebuild Nginx configs to clean up the configuration file
	if err := services.RebuildNginxConfigs(r.Context(), services.SystemPool); err != nil {
		log.Printf("[SitesController] Warning: Failed to rebuild Nginx configurations after deletion: %v", err)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Site deleted successfully",
	})
}

// UpdateSite handles PATCH /api/sites/{id}
func (c *SitesController) UpdateSite(w http.ResponseWriter, r *http.Request) {
	projectSlug := chi.URLParam(r, "slug")
	siteID := chi.URLParam(r, "id")

	if projectSlug == "" || siteID == "" {
		http.Error(w, `{"error":"project_slug and site ID are required"}`, 400)
		return
	}

	var body struct {
		Name         *string `json:"name"`
		Domain       *string `json:"domain"`
		Status       *string `json:"status"`
		ActiveFolder *string `json:"active_folder"`
	}

	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"Invalid request body"}`, 400)
		return
	}

	// Fetch current site
	var currentName, currentDomain, currentPath, currentStatus, currentActiveFolder string
	err := services.SystemPool.QueryRow(r.Context(), `
		SELECT name, domain, storage_path, status, active_folder FROM system.sites WHERE id = $1 AND project_slug = $2
	`, siteID, projectSlug).Scan(&currentName, &currentDomain, &currentPath, &currentStatus, &currentActiveFolder)

	if err != nil {
		http.Error(w, `{"error":"Site not found"}`, 404)
		return
	}

	// Update fields if provided
	newName := currentName
	if body.Name != nil && *body.Name != "" {
		newName = *body.Name
	}

	newDomain := currentDomain
	domainChanged := false
	if body.Domain != nil {
		normalized, err := services.NormalizeDomain(*body.Domain)
		if err == nil {
			newDomain = normalized
		} else {
			newDomain = *body.Domain
		}
		if newDomain != currentDomain {
			domainChanged = true
		}
	}

	newStatus := currentStatus
	if body.Status != nil && *body.Status != "" {
		newStatus = *body.Status
	}

	newActiveFolder := currentActiveFolder
	if body.ActiveFolder != nil {
		newActiveFolder = *body.ActiveFolder
	}

	// If name changed, rename the storage folder as well for consistency
	newStoragePath := currentPath
	if newName != currentName {
		siteSlug := strings.ToLower(newName)
		siteSlug = regexp.MustCompile(`[^a-z0-9\-]+`).ReplaceAllString(siteSlug, "-")
		siteSlug = strings.Trim(siteSlug, "-")
		if siteSlug != "" {
			storageRoot := os.Getenv("STORAGE_ROOT")
			if storageRoot == "" {
				storageRoot = "./storage"
			}
			newStoragePath = filepath.Join(storageRoot, projectSlug, "_sites", siteSlug)
			// Rename physical directory
			if _, err := os.Stat(currentPath); err == nil {
				if err := os.Rename(currentPath, newStoragePath); err != nil {
					log.Printf("[SitesController] Warning: failed to rename directory: %v", err)
					newStoragePath = currentPath // Keep old path if rename failed
				}
			}
		}
	}

	newSSLCertSource := ""
	if domainChanged && newDomain != "" {
		// Fetch project to see if it has configured manual or specific ssl sources
		var projectSSLCertSource string
		_ = services.SystemPool.QueryRow(r.Context(), `
			SELECT ssl_certificate_source FROM system.projects WHERE slug = $1
		`, projectSlug).Scan(&projectSSLCertSource)

		newSSLCertSource = projectSSLCertSource
		if newSSLCertSource == "" {
			newSSLCertSource = services.DetectWildcardSource(newDomain)
		}
	}

	if domainChanged {
		_, err = services.SystemPool.Exec(r.Context(), `
			UPDATE system.sites
			SET name = $1, domain = $2, storage_path = $3, status = $4, ssl_certificate_source = $5, active_folder = $6, updated_at = NOW()
			WHERE id = $7
		`, newName, newDomain, newStoragePath, newStatus, newSSLCertSource, newActiveFolder, siteID)
	} else {
		_, err = services.SystemPool.Exec(r.Context(), `
			UPDATE system.sites
			SET name = $1, domain = $2, storage_path = $3, status = $4, active_folder = $5, updated_at = NOW()
			WHERE id = $6
		`, newName, newDomain, newStoragePath, newStatus, newActiveFolder, siteID)
	}

	if err != nil {
		if strings.Contains(err.Error(), "duplicate key value violates unique constraint") {
			http.Error(w, `{"error":"Domain is already in use by another site or project"}`, 409)
			return
		}
		http.Error(w, fmt.Sprintf(`{"error":"Failed to update site metadata: %s"}`, err.Error()), 500)
		return
	}

	// Request certificate asynchronously for the new domain
	if domainChanged && newDomain != "" && !strings.HasSuffix(newDomain, ".localhost") {
		certSvc := services.CertificateService{CryptoSvc: &services.CryptoService{}}
		err = certSvc.RequestCertificate(r.Context(), services.SystemPool, newDomain, "admin@cascata.io", services.ProviderLetsEncrypt, "", "")
		if err != nil {
			log.Printf("[SitesController] Warning: Failed to request SSL certificate: %v", err)
		}
	}

	// Rebuild Nginx configs
	if err := services.RebuildNginxConfigs(r.Context(), services.SystemPool); err != nil {
		log.Printf("[SitesController] Warning: Failed to rebuild Nginx configurations: %v", err)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":       true,
		"id":            siteID,
		"name":          newName,
		"domain":        newDomain,
		"storage_path":  newStoragePath,
		"active_folder": newActiveFolder,
		"status":       newStatus,
	})
}

