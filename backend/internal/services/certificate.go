package services

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"cascata-backend/internal/config"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/net/idna"
)

const (
	LetsEncryptBasePath = "/etc/letsencrypt/live"
	SystemCertPath      = "/etc/letsencrypt/live/system"
	WebrootPath         = "/var/www/html"
	NginxDynamicRoot    = "/etc/nginx/conf.d/dynamic"
	NginxControllerURL  = "http://nginx_controller:3001"
)

type CertProvider string

const (
	ProviderLetsEncrypt   CertProvider = "letsencrypt"
	ProviderCertbot       CertProvider = "certbot"
	ProviderManual        CertProvider = "manual"
	ProviderCloudflarePEM CertProvider = "cloudflare_pem"
)

type CertPaths struct {
	Fullchain string
	Privkey   string
}

// ToPunycode converte um domínio Unicode (ex: fidelixsoluções.com) para Punycode (xn--...)
// necessário para compatibilidade com DNS, certbot e sistemas de arquivo
func ToPunycode(domain string) (string, error) {
	// Preservar wildcard prefix se existir
	prefix := ""
	cleanDomain := domain
	if strings.HasPrefix(domain, "*.") {
		prefix = "*."
		cleanDomain = strings.TrimPrefix(domain, "*.")
	}

	// Converter para Punycode usando o perfil IDNA2008
	punycode, err := idna.Lookup.ToASCII(cleanDomain)
	if err != nil {
		return "", fmt.Errorf("falha ao converter domínio para punycode: %w", err)
	}

	return prefix + punycode, nil
}

// ToUnicode converte um domínio Punycode (xn--...) para Unicode legível
// usado para exibição e storage amigável
func ToUnicode(domain string) (string, error) {
	// Preservar wildcard prefix se existir
	prefix := ""
	cleanDomain := domain
	if strings.HasPrefix(domain, "*.") {
		prefix = "*."
		cleanDomain = strings.TrimPrefix(domain, "*.")
	}

	// Converter de Punycode para Unicode
	unicode, err := idna.Lookup.ToUnicode(cleanDomain)
	if err != nil {
		return "", fmt.Errorf("falha ao converter domínio para unicode: %w", err)
	}

	return prefix + unicode, nil
}

// NormalizeDomain converte domínio para forma Punycode (ASCII) se necessário
// retorna erro se o domínio for inválido
func NormalizeDomain(domain string) (string, error) {
	if domain == "" {
		return "", fmt.Errorf("domínio vazio")
	}

	// Já é ASCII? (inclui punycode xn--)
	if isASCII(domain) && !strings.Contains(domain, " ") {
		// Verificar se já está em formato válido
		return strings.ToLower(strings.TrimSpace(domain)), nil
	}

	// Converter para Punycode
	return ToPunycode(domain)
}

// isASCII verifica se string contém apenas caracteres ASCII
func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] > 127 {
			return false
		}
	}
	return true
}

// ReloadNginx notifies the controller to reload Nginx configuration
func ReloadNginx(ctx context.Context) error {
	req, _ := http.NewRequestWithContext(ctx, "POST", NginxControllerURL+"/reload", nil)
	req.Header.Set("x-internal-secret", config.InternalCtrlSecret)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to reload nginx: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("nginx controller returned status %d", resp.StatusCode)
	}
	return nil
}

// EnsureBootstrapCert creates a self-signed certificate if none exists
func EnsureBootstrapCert() {
	_ = os.MkdirAll(SystemCertPath, 0755)
	certFile := filepath.Join(SystemCertPath, "fullchain.pem")
	keyFile := filepath.Join(SystemCertPath, "privkey.pem")

	if _, err := os.Stat(certFile); os.IsNotExist(err) {
		log.Println("[CertService] Generating bootstrap self-signed certificate...")
		cmd := exec.Command("openssl", "req", "-x509", "-nodes", "-days", "3650", "-newkey", "rsa:2048",
			"-keyout", keyFile, "-out", certFile, "-subj", "/C=US/ST=State/L=City/O=Cascata/CN=localhost")
		if err := cmd.Run(); err != nil {
			log.Printf("[CertService] Bootstrap cert failed: %v", err)
		}
	}
}

// RebuildNginxConfigs regenerates all dynamic vhost files
func RebuildNginxConfigs(ctx context.Context, pool *pgxpool.Pool) error {
	_ = os.MkdirAll(NginxDynamicRoot, 0755)

	// Clean old configs
	files, _ := os.ReadDir(NginxDynamicRoot)
	for _, f := range files {
		if strings.HasSuffix(f.Name(), ".conf") {
			_ = os.Remove(filepath.Join(NginxDynamicRoot, f.Name()))
		}
	}

	// Get System Domain
	var sysDomain string
	_ = pool.QueryRow(ctx, "SELECT settings->>'domain' FROM system.ui_settings WHERE project_slug = '_system_root_' AND table_name = 'system_config'").Scan(&sysDomain)

	if sysDomain != "" && validateDomain(sysDomain) {
		certs := resolveCertPath(sysDomain)
		if certs == nil {
			certs = &CertPaths{Fullchain: filepath.Join(SystemCertPath, "fullchain.pem"), Privkey: filepath.Join(SystemCertPath, "privkey.pem")}
		}
		conf := generateNginxBlock(sysDomain, *certs, "frontend", "http://backend_control:3000")
		_ = os.WriteFile(filepath.Join(NginxDynamicRoot, "00_system_dashboard.conf"), []byte(conf), 0644)
	}

	// Get Project Domains
	rows, err := pool.Query(ctx, "SELECT slug, custom_domain, ssl_certificate_source FROM system.projects WHERE custom_domain IS NOT NULL")
	if err != nil {
		log.Printf("[RebuildNginx] ERROR querying projects: %v", err)
	} else {
		count := 0
		for rows.Next() {
			var slug, domain string
			var sslSource *string
			if err := rows.Scan(&slug, &domain, &sslSource); err != nil { 
				log.Printf("[RebuildNginx] ERROR scanning project: %v", err)
				continue 
			}
			if domain == "" || domain == sysDomain { continue }

			log.Printf("[RebuildNginx] Processing project %s with domain %s", slug, domain)
			
			certs := resolveCertPath(domain)
			if certs == nil && sslSource != nil {
				log.Printf("[RebuildNginx] Trying sslSource %s for domain %s", *sslSource, domain)
				certs = resolveCertPath(*sslSource)
			}

			if certs != nil {
				conf := generateNginxBlock(domain, *certs, "backend_data", "")
				configPath := filepath.Join(NginxDynamicRoot, "10_proj_"+slug+".conf")
				if err := os.WriteFile(configPath, []byte(conf), 0644); err != nil {
					log.Printf("[RebuildNginx] ERROR writing config for %s: %v", domain, err)
				} else {
					log.Printf("[RebuildNginx] SUCCESS - Generated config for %s at %s", domain, configPath)
					count++
				}
			} else {
				log.Printf("[RebuildNginx] WARNING - No certificates found for domain %s", domain)
			}
		}
		rows.Close()
		log.Printf("[RebuildNginx] Generated %d project configs", count)
	}

	// Get Static Sites
	siteRows, err := pool.Query(ctx, "SELECT id::text, project_slug, domain, storage_path, ssl_certificate_source, active_folder FROM system.sites WHERE status = 'active' AND domain IS NOT NULL")
	if err != nil {
		log.Printf("[RebuildNginx] ERROR querying static sites: %v", err)
	} else {
		siteCount := 0
		for siteRows.Next() {
			var id, projectSlug, domain, storagePath, activeFolder string
			var sslCertSource *string
			if err := siteRows.Scan(&id, &projectSlug, &domain, &storagePath, &sslCertSource, &activeFolder); err != nil {
				log.Printf("[RebuildNginx] ERROR scanning static site: %v", err)
				continue
			}
			if domain == "" || domain == sysDomain {
				continue
			}

			var sslSourceVal string
			if sslCertSource != nil {
				sslSourceVal = *sslCertSource
			}

			log.Printf("[RebuildNginx] Processing static site %s with domain %s (ssl_source: %s, active_folder: %s)", id, domain, sslSourceVal, activeFolder)

			// Append active_folder to storage_path if set (for versioning support)
			finalStoragePath := storagePath
			if activeFolder != "" {
				finalStoragePath = filepath.Join(storagePath, activeFolder)
			}

			// Use site's ssl_certificate_source first, then fallback to domain, then wildcard
			certs := resolveCertPath(sslSourceVal)
			if certs == nil {
				certs = resolveCertPath(domain)
			}
			if certs == nil {
				// Try wildcard fallback if any
				certs = resolveCertPath(DetectWildcardSource(domain))
			}

			if certs != nil {
				conf := generateNginxStaticSiteBlock(domain, *certs, finalStoragePath)
				configPath := filepath.Join(NginxDynamicRoot, "20_site_"+id+".conf")
				if err := os.WriteFile(configPath, []byte(conf), 0644); err != nil {
					log.Printf("[RebuildNginx] ERROR writing config for static site %s: %v", domain, err)
				} else {
					log.Printf("[RebuildNginx] SUCCESS - Generated config for static site %s at %s", domain, configPath)
					siteCount++
				}
			} else {
				log.Printf("[RebuildNginx] WARNING - No certificates found for static site domain %s (ssl_source: %s)", domain, sslSourceVal)
			}
		}
		siteRows.Close()
		log.Printf("[RebuildNginx] Generated %d static site configs", siteCount)
	}

	return ReloadNginx(ctx)
}

func validateDomain(domain string) bool {
	if domain == "" || strings.Contains(domain, " ") || !strings.Contains(domain, ".") {
		return domain == "localhost"
	}

	// Normalizar domínio (converter IDN para Punycode se necessário)
	normalized, err := NormalizeDomain(domain)
	if err != nil {
		return false
	}

	// Suporta wildcards como *.unibloom.com.br ou unibloom.com.br
	re := regexp.MustCompile(`^(\*\.)?[a-zA-Z0-9][a-zA-Z0-9-._]{0,61}[a-zA-Z0-9](?:\.[a-zA-Z]{2,})+$`)
	return re.MatchString(normalized)
}

func resolveCertPath(domain string) *CertPaths {
	dir := filepath.Join(LetsEncryptBasePath, domain)
	if _, err := os.Stat(filepath.Join(dir, "fullchain.pem")); err == nil {
		return &CertPaths{Fullchain: filepath.Join(dir, "fullchain.pem"), Privkey: filepath.Join(dir, "privkey.pem")}
	}

	// Wildcard fallback
	parts := strings.Split(domain, ".")
	if len(parts) >= 2 {
		root := strings.Join(parts[1:], ".")
		candidates := []string{"wildcard." + root, "*." + root, root}
		for _, c := range candidates {
			dir := filepath.Join(LetsEncryptBasePath, c)
			if _, err := os.Stat(filepath.Join(dir, "fullchain.pem")); err == nil {
				return &CertPaths{Fullchain: filepath.Join(dir, "fullchain.pem"), Privkey: filepath.Join(dir, "privkey.pem")}
			}
		}
	}
	return nil
}

// DetectWildcardSource verifica se existe um certificado wildcard que cobre o domínio
// Retorna o nome do domínio de origem do certificado (ex: *.unibloom.com.br) ou vazio
func DetectWildcardSource(domain string) string {
	parts := strings.Split(domain, ".")
	if len(parts) < 2 {
		return ""
	}
	
	root := strings.Join(parts[1:], ".")
	
	// Verificar candidates em ordem de prioridade
	candidates := []string{"wildcard." + root, "*." + root, root}
	for _, c := range candidates {
		dir := filepath.Join(LetsEncryptBasePath, c)
		if _, err := os.Stat(filepath.Join(dir, "fullchain.pem")); err == nil {
			// Retorna o formato wildcard esperado pelo sistema
			if strings.HasPrefix(c, "wildcard.") {
				return "*." + strings.TrimPrefix(c, "wildcard.")
			}
			return c
		}
	}
	
	return ""
}

func generateNginxBlock(domain string, certs CertPaths, target string, apiControlUpstream string) string {
	var locations string
	if target == "frontend" {
		locations = fmt.Sprintf(`
    location / {
        proxy_pass http://frontend;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
    location /api/control/ {
        proxy_pass %s;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
    location ~ ^/(api/data/|rpc/|auth/|storage/|edge/|tables/|rest/|vector/) {
        proxy_pass http://backend_data_sockets;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_buffering off;
    }`, apiControlUpstream)
	} else {
		locations = `
    location / {
        proxy_pass http://backend_data_sockets;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }`
	}

	return fmt.Sprintf(`
server {
    listen 443 ssl;
    server_name %s;
    ssl_certificate %s;
    ssl_certificate_key %s;
    ssl_protocols TLSv1.2 TLSv1.3;
    client_max_body_size 100M;
    %s
}
server {
    listen 80;
    server_name %s;
    location /.well-known/acme-challenge/ {
        root /var/www/html;
        allow all;
    }
    location / { return 301 https://$host$request_uri; }
}
`, domain, certs.Fullchain, certs.Privkey, locations, domain)
}

func generateNginxStaticSiteBlock(domain string, certs CertPaths, staticRoot string) string {
	// Normalize storage path for nginx container
	// Backend saves absolute host path (e.g., /cascata-storage/...) but nginx needs container path (/cascata/storage/...)
	nginxRoot := staticRoot
	if strings.HasPrefix(staticRoot, "/cascata-storage") {
		nginxRoot = strings.Replace(staticRoot, "/cascata-storage", "/cascata/storage", 1)
	} else if strings.HasPrefix(staticRoot, "./storage") {
		nginxRoot = strings.Replace(staticRoot, "./storage", "/cascata/storage", 1)
	} else if !strings.HasPrefix(staticRoot, "/cascata/storage") {
		// If it's a relative path or other format, assume it's relative to /cascata/storage
		nginxRoot = "/cascata/storage/" + strings.TrimPrefix(staticRoot, "./")
	}

	return fmt.Sprintf(`
server {
    listen 443 ssl;
    server_name %s;
    ssl_certificate %s;
    ssl_certificate_key %s;
    ssl_protocols TLSv1.2 TLSv1.3;
    client_max_body_size 100M;

    root %s;
    index index.html;
    autoindex off;

    # Try exact file → directory with trailing slash → SPA fallback (index.html).
    # Using =404 as final fallback prevents the internal redirect loop that occurs
    # when index.html itself does not exist at the root of the extracted ZIP.
    location / {
        try_files $uri $uri/ /index.html =404;
    }

    # Guard: if /index.html is missing, return a clean 404 instead of looping.
    location = /index.html {
        try_files $uri =404;
    }
}
server {
    listen 80;
    server_name %s;
    location /.well-known/acme-challenge/ {
        root /var/www/html;
        allow all;
    }
    location / { return 301 https://$host$request_uri; }
}
`, domain, certs.Fullchain, certs.Privkey, nginxRoot, domain)
}

type CertificateService struct {
	CryptoSvc *CryptoService
}

// RequestCertificate enqueues a certificate issuance task
// The actual certificate operations are handled by the cert-controller sidecar
func (s *CertificateService) RequestCertificate(ctx context.Context, pool *pgxpool.Pool, domain, email string, provider CertProvider, manualCert, manualKey string) error {
	// Initialize queue service
	queueSvc := NewCertQueueService(GetDragonfly().Options().Addr)

	// Create task
	task := &CertTask{
		Type:     TaskIssue,
		Domain:   domain,
		Email:    email,
		Provider: string(provider),
		Cert:     manualCert,
		Key:      manualKey,
	}

	// Enqueue task
	if err := queueSvc.EnqueueTask(ctx, task); err != nil {
		return fmt.Errorf("failed to enqueue certificate task: %w", err)
	}

	log.Printf("[CertificateService] Certificate task enqueued for domain: %s (provider: %s)", domain, provider)

	// For manual/cloudflare certs, also trigger async nginx rebuild
	if provider == ProviderManual || provider == ProviderCloudflarePEM {
		// Queue nginx reload task
		reloadTask := &CertTask{
			Type:   TaskReload,
			Domain: "system",
		}
		if err := queueSvc.EnqueueTask(ctx, reloadTask); err != nil {
			log.Printf("[CertificateService] Warning: failed to queue nginx reload: %v", err)
		}
	}

	return nil
}

// ListAvailableCerts returns a list of domains that have certificates in the vault
func (s *CertificateService) ListAvailableCerts() []string {
	if _, err := os.Stat(LetsEncryptBasePath); os.IsNotExist(err) {
		return []string{}
	}

	dirs, err := os.ReadDir(LetsEncryptBasePath)
	if err != nil {
		return []string{}
	}

	var domains []string
	for _, d := range dirs {
		if d.IsDir() && d.Name() != "system" && d.Name() != "README" {
			name := d.Name()
			if strings.HasPrefix(name, "wildcard.") {
				name = "*." + strings.TrimPrefix(name, "wildcard.")
			}
			domains = append(domains, name)
		}
	}
	return domains
}

// DeleteCertificate enqueues a certificate deletion task
// The actual deletion is handled by the cert-controller sidecar
func (s *CertificateService) DeleteCertificate(ctx context.Context, pool *pgxpool.Pool, domain string) error {
	// Professional safety check: Is this certificate in use?
	inUse, reasons, err := s.IsCertificateInUse(ctx, pool, domain)
	if err != nil {
		return fmt.Errorf("failed to check certificate usage: %w", err)
	}
	if inUse {
		return fmt.Errorf("cannot delete certificate for %s: it is currently in use by %s", domain, strings.Join(reasons, ", "))
	}

	// Initialize queue service
	queueSvc := NewCertQueueService(GetDragonfly().Options().Addr)

	// Create delete task
	task := &CertTask{
		Type:   TaskDelete,
		Domain: domain,
	}

	// Enqueue task
	if err := queueSvc.EnqueueTask(ctx, task); err != nil {
		return fmt.Errorf("failed to enqueue delete task: %w", err)
	}

	// Also queue nginx reload
	reloadTask := &CertTask{
		Type:   TaskReload,
		Domain: "system",
	}
	if err := queueSvc.EnqueueTask(ctx, reloadTask); err != nil {
		log.Printf("[CertificateService] Warning: failed to queue nginx reload: %v", err)
	}

	log.Printf("[CertificateService] Delete task enqueued for domain: %s", domain)
	return nil
}

// IsCertificateInUse checks if a certificate is referenced by any project or system configuration
func (s *CertificateService) IsCertificateInUse(ctx context.Context, pool *pgxpool.Pool, domain string) (bool, []string, error) {
	var reasons []string
	
	// 1. Check if it's the system domain
	var sysDomain string
	_ = pool.QueryRow(ctx, "SELECT settings->>'domain' FROM system.ui_settings WHERE project_slug = '_system_root_' AND table_name = 'system_config'").Scan(&sysDomain)
	
	if sysDomain != "" {
		if sysDomain == domain || domain == DetectWildcardSource(sysDomain) {
			reasons = append(reasons, "System Dashboard ("+sysDomain+")")
		}
	}

	// 2. Check projects using this exact domain or as ssl_certificate_source
	rows, err := pool.Query(ctx, `
		SELECT slug, custom_domain 
		FROM system.projects 
		WHERE custom_domain = $1 OR ssl_certificate_source = $1
	`, domain)
	if err != nil {
		return false, nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var slug, customDomain string
		if err := rows.Scan(&slug, &customDomain); err == nil {
			reasons = append(reasons, fmt.Sprintf("Project %s (%s)", slug, customDomain))
		}
	}

	// 3. If it's a wildcard, check projects covered by it
	if strings.HasPrefix(domain, "*.") {
		root := strings.TrimPrefix(domain, "*.")
		rows, err := pool.Query(ctx, `
			SELECT slug, custom_domain 
			FROM system.projects 
			WHERE custom_domain LIKE '%.' || $1
		`, root)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var slug, customDomain string
				if err := rows.Scan(&slug, &customDomain); err == nil {
					// Verificação extra: o domínio do projeto realmente usa este wildcard?
					if DetectWildcardSource(customDomain) == domain {
						reasons = append(reasons, fmt.Sprintf("Project %s (covered by wildcard %s)", slug, customDomain))
					}
				}
			}
		}
	}

	return len(reasons) > 0, reasons, nil
}


