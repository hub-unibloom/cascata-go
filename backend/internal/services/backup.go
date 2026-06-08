package services

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"cascata-backend/internal/types"
	"github.com/jackc/pgx/v5"
)

type BackupService struct {
	CryptoSvc *CryptoService
}

type ProjectMetadata struct {
	ID           string                 `json:"id"`
	Name         string                 `json:"name"`
	Slug         string                 `json:"slug"`
	DbName       string                 `json:"db_name"`
	JwtSecret    string                 `json:"jwt_secret"`
	AnonKey      string                 `json:"anon_key"`
	ServiceKey   string                 `json:"service_key"`
	CustomDomain string                 `json:"custom_domain"`
	Metadata     types.ProjectMetadata  `json:"metadata"`
}

type TableDefinition struct {
	Schema string `json:"schema"`
	Name   string `json:"name"`
}

var tempUploadRoot = "/tmp/cascata_backups"

func init() {
	os.MkdirAll(tempUploadRoot, 0755)
}

func (s *BackupService) GenerateBackupToFile(ctx context.Context, project *ProjectMetadata) (string, error) {
	tempFilePath := filepath.Join(tempUploadRoot, fmt.Sprintf("backup_%s_%d.zip", project.Slug, time.Now().Unix()))
	output, err := os.Create(tempFilePath)
	if err != nil {
		return "", err
	}
	defer output.Close()

	archive := zip.NewWriter(output)
	defer archive.Close()

	connString := s.resolveConnectionString(project)

	// 1. MANIFEST
	manifest := map[string]interface{}{
		"version":     "2.0",
		"engine":      "Cascata-Architect-v7",
		"exported_at": time.Now().Format(time.RFC3339),
		"type":        "full_snapshot",
		"project": map[string]interface{}{
			"name":          project.Name,
			"slug":          project.Slug,
			"db_name":       project.DbName,
			"custom_domain": project.CustomDomain,
			"metadata":      project.Metadata,
		},
	}
	f, _ := archive.Create("manifest.json")
	json.NewEncoder(f).Encode(manifest)

	// 2. SECRETS
	secrets := map[string]string{
		"jwt_secret":  project.JwtSecret,
		"anon_key":    project.AnonKey,
		"service_key": project.ServiceKey,
	}
	f, _ = archive.Create("system/secrets.json")
	json.NewEncoder(f).Encode(secrets)

	// 3. VECTORS (Qdrant Snapshot)
	qdrantUrl := fmt.Sprintf("http://%s:%s", os.Getenv("QDRANT_HOST"), os.Getenv("QDRANT_PORT"))
	if os.Getenv("QDRANT_PORT") == "" { qdrantUrl = "http://qdrant:6333" }
	
	resp, err := http.Post(fmt.Sprintf("%s/collections/%s/snapshots", qdrantUrl, project.Slug), "application/json", nil)
	if err == nil {
		defer resp.Body.Close()
		var res struct { Result struct { Name string `json:"name"` } `json:"result"` }
		json.NewDecoder(resp.Body).Decode(&res)
		
		if res.Result.Name != "" {
			snapUrl := fmt.Sprintf("%s/collections/%s/snapshots/%s", qdrantUrl, project.Slug, res.Result.Name)
			snapResp, err := http.Get(snapUrl)
			if err == nil {
				defer snapResp.Body.Close()
				f, _ = archive.Create("vector/snapshot.qdrant")
				io.Copy(f, snapResp.Body)
			}
		}
	}

	// 4. SCHEMA (pg_dump)
	schemaF, _ := archive.Create("schema/structure.sql")
	err = s.getSchemaDumpStream(connString, schemaF)
	if err != nil {
		log.Printf("[BackupService] SCHEMA dump failed for %s: %v", project.Slug, err)
	}

	// 5. AUTH (System Data)
	authF, _ := archive.Create("system/auth_data.sql")
	_ = s.getDataDumpStream(connString, []string{"auth"}, authF)

	// 6. BUSINESS DATA (CSV Exports)
	tables, err := s.listTables(ctx, connString)
	if err == nil {
		for _, table := range tables {
			if table.Schema == "public" {
				f, _ = archive.Create(fmt.Sprintf("data/%s.%s.csv", table.Schema, table.Name))
				_ = s.getTableCsvStream(connString, table.Schema, table.Name, f)
			}
		}
	}

	// 7. STORAGE
	storageRoot := os.Getenv("STORAGE_ROOT")
	if storageRoot == "" { storageRoot = "../storage" }
	projectStoragePath := filepath.Join(storageRoot, project.Slug)
	
	if _, err := os.Stat(projectStoragePath); err == nil {
		err = filepath.Walk(projectStoragePath, func(path string, info os.FileInfo, err error) error {
			if err != nil { return err }
			if info.IsDir() { return nil }
			rel, _ := filepath.Rel(projectStoragePath, path)
			f, _ := archive.Create(filepath.Join("storage", rel))
			src, _ := os.Open(path)
			defer src.Close()
			io.Copy(f, src)
			return nil
		})
	}

	return tempFilePath, nil
}

func (s *BackupService) resolveConnectionString(project *ProjectMetadata) string {
	// For parity, same logic as BackupService.ts
	host := os.Getenv("DB_DIRECT_HOST")
	if host == "" { host = "db" }
	port := os.Getenv("DB_DIRECT_PORT")
	if port == "" { port = "5432" }
	user := os.Getenv("DB_USER")
	if user == "" { user = "cascata_admin" }
	pass := os.Getenv("DB_PASS")
	if pass == "" { pass = "secure_pass" }
	
	return fmt.Sprintf("postgresql://%s:%s@%s:%s/%s", user, pass, host, port, project.DbName)
}

func (s *BackupService) listTables(ctx context.Context, connString string) ([]TableDefinition, error) {
	conn, err := pgx.Connect(ctx, connString)
	if err != nil { return nil, err }
	defer conn.Close(ctx)

	rows, err := conn.Query(ctx, `
		SELECT table_schema, table_name 
		FROM information_schema.tables 
		WHERE table_schema IN ('public', 'auth') 
		AND table_type = 'BASE TABLE'
		AND table_name NOT LIKE '_deleted_%'
	`)
	if err != nil { return nil, err }
	defer rows.Close()

	var tables []TableDefinition
	for rows.Next() {
		var t TableDefinition
		if err := rows.Scan(&t.Schema, &t.Name); err == nil {
			tables = append(tables, t)
		}
	}
	return tables, nil
}

func (s *BackupService) getSchemaDumpStream(connString string, w io.Writer) error {
	// pg_dump --schema-only ...
	cmd := exec.Command("pg_dump", "--schema-only", "--no-owner", "--no-privileges", "--no-tablespaces", "-n", "public", "-n", "auth", connString)
	cmd.Stdout = w
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (s *BackupService) getDataDumpStream(connString string, schemas []string, w io.Writer) error {
	args := []string{"--data-only", "--no-owner", "--no-privileges", "--column-inserts", "--disable-triggers"}
	for _, sc := range schemas {
		args = append(args, "-n", sc)
	}
	args = append(args, connString)
	cmd := exec.Command("pg_dump", args...)
	cmd.Stdout = w
	return cmd.Run()
}

func (s *BackupService) getTableCsvStream(connString string, schema, tableName string, w io.Writer) error {
	// psql -c "COPY (...) TO STDOUT WITH CSV HEADER"
	query := fmt.Sprintf(`COPY (SELECT * FROM "%s"."%s") TO STDOUT WITH CSV HEADER`, schema, tableName)
	cmd := exec.Command("psql", "-c", query, connString)
	cmd.Stdout = w
	return cmd.Run()
}
