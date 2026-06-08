package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"cascata-backend/internal/services"
)

const (
	LetsEncryptBasePath = "/etc/letsencrypt/live"
	WebrootPath         = "/var/www/html"
	NginxControllerURL  = "http://nginx_controller:3001"
)

func main() {
	log.Println("[CertController] Starting certificate controller sidecar...")

	// Initialize Dragonfly/Redis client
	dragonflyHost := os.Getenv("DRAGONFLY_HOST")
	if dragonflyHost == "" {
		dragonflyHost = "dragonfly"
	}
	dragonflyPort := os.Getenv("DRAGONFLY_PORT")
	if dragonflyPort == "" {
		dragonflyPort = "6379"
	}

	redisAddr := fmt.Sprintf("%s:%s", dragonflyHost, dragonflyPort)
	queueSvc := services.NewCertQueueService(redisAddr)

	log.Println("[CertController] Connected to queue at", redisAddr)
	log.Println("[CertController] Waiting for certificate tasks...")

	// Main processing loop
	ctx := context.Background()

	for {
		// Requeue stale tasks (older than 10 minutes)
		if err := queueSvc.RequeueStaleTasks(ctx, 10*time.Minute); err != nil {
			log.Printf("[CertController] Failed to requeue stale tasks: %v", err)
		}

		// Dequeue next task (blocking with timeout)
		task, err := queueSvc.DequeueTask(ctx)
		if err != nil {
			log.Printf("[CertController] Error dequeuing task: %v", err)
			time.Sleep(5 * time.Second)
			continue
		}

		if task == nil {
			// No task available, sleep briefly
			time.Sleep(1 * time.Second)
			continue
		}

		// Process the task
		log.Printf("[CertController] Processing task %s: %s for domain %s", task.ID, task.Type, task.Domain)

		var processErr error
		switch task.Type {
		case services.TaskIssue:
			processErr = processIssue(ctx, task)
		case services.TaskRenew:
			processErr = processRenew(ctx, task)
		case services.TaskDelete:
			processErr = processDelete(ctx, task)
		case services.TaskReload:
			processErr = processReload(ctx, task)
		default:
			processErr = fmt.Errorf("unknown task type: %s", task.Type)
		}

		if processErr != nil {
			log.Printf("[CertController] Task %s failed: %v", task.ID, processErr)
			if err := queueSvc.FailTask(ctx, task.ID, processErr.Error()); err != nil {
				log.Printf("[CertController] Failed to mark task as failed: %v", err)
			}
		} else {
			log.Printf("[CertController] Task %s completed successfully", task.ID)
			if err := queueSvc.CompleteTask(ctx, task.ID, "Certificate operation completed"); err != nil {
				log.Printf("[CertController] Failed to mark task as completed: %v", err)
			}
		}
	}
}

func processIssue(ctx context.Context, task *services.CertTask) error {
	fsName := task.Domain
	if strings.HasPrefix(task.Domain, "*.") {
		fsName = "wildcard." + strings.TrimPrefix(task.Domain, "*.")
	}
	domainDir := filepath.Join(LetsEncryptBasePath, fsName)

	switch task.Provider {
	case "manual", "cloudflare_pem":
		return processManualCert(ctx, task, domainDir)
	case "letsencrypt", "certbot":
		return processLetsEncrypt(ctx, task, domainDir)
	default:
		return fmt.Errorf("unknown provider: %s", task.Provider)
	}
}

func processManualCert(ctx context.Context, task *services.CertTask, domainDir string) error {
	if task.Cert == "" || task.Key == "" {
		return fmt.Errorf("manual certificate and key are required")
	}

	// Create directory structure
	if err := os.MkdirAll(LetsEncryptBasePath, 0755); err != nil {
		return fmt.Errorf("failed to create base path: %w", err)
	}
	if err := os.RemoveAll(domainDir); err != nil {
		log.Printf("[CertController] Warning: failed to clean old dir: %v", err)
	}
	if err := os.MkdirAll(domainDir, 0755); err != nil {
		return fmt.Errorf("failed to create domain dir: %w", err)
	}

	// Write certificate files
	certPath := filepath.Join(domainDir, "fullchain.pem")
	keyPath := filepath.Join(domainDir, "privkey.pem")

	if err := os.WriteFile(certPath, []byte(strings.TrimSpace(task.Cert)), 0644); err != nil {
		return fmt.Errorf("failed to write certificate: %w", err)
	}
	if err := os.WriteFile(keyPath, []byte(strings.TrimSpace(task.Key)), 0600); err != nil {
		return fmt.Errorf("failed to write private key: %w", err)
	}

	log.Printf("[CertController] Manual certificate saved for %s", task.Domain)

	// Trigger nginx config rebuild via backend API before reloading
	if err := triggerConfigRebuild(ctx); err != nil {
		log.Printf("[CertController] Warning: config rebuild failed: %v", err)
		// Continue anyway - nginx reload might still work with existing configs
	}

	// Reload nginx
	return reloadNginx(ctx)
}

func processLetsEncrypt(ctx context.Context, task *services.CertTask, domainDir string) error {
	if !strings.Contains(task.Email, "@") {
		return fmt.Errorf("invalid email for certbot: %s", task.Email)
	}

	// Setup ACME challenge directory
	acmeDir := filepath.Join(WebrootPath, ".well-known", "acme-challenge")
	if err := os.MkdirAll(acmeDir, 0755); err != nil {
		return fmt.Errorf("failed to create acme dir: %w", err)
	}

	// Ensure webroot is readable
	if err := exec.Command("chmod", "-R", "755", WebrootPath).Run(); err != nil {
		log.Printf("[CertController] Warning: chmod failed: %v", err)
	}

	// Run certbot
	cmd := exec.CommandContext(ctx, "certbot", "certonly", "--webroot",
		"-w", WebrootPath,
		"-d", task.Domain,
		"--email", task.Email,
		"--agree-tos",
		"--non-interactive")

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("certbot failed: %v, output: %s", err, string(output))
	}

	log.Printf("[CertController] Let's Encrypt certificate issued for %s", task.Domain)

	// Trigger nginx config rebuild via backend API before reloading
	if err := triggerConfigRebuild(ctx); err != nil {
		log.Printf("[CertController] Warning: config rebuild failed: %v", err)
		// Continue anyway - nginx reload might still work with existing configs
	}

	// Reload nginx
	return reloadNginx(ctx)
}

func processRenew(ctx context.Context, task *services.CertTask) error {
	// Renew all certificates
	cmd := exec.CommandContext(ctx, "certbot", "renew", "--non-interactive", "--quiet")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("certbot renew failed: %v, output: %s", err, string(output))
	}

	log.Printf("[CertController] Certificates renewed")

	// Trigger nginx config rebuild via backend API before reloading
	if err := triggerConfigRebuild(ctx); err != nil {
		log.Printf("[CertController] Warning: config rebuild failed: %v", err)
		// Continue anyway - nginx reload might still work with existing configs
	}

	// Reload nginx
	return reloadNginx(ctx)
}

func processDelete(ctx context.Context, task *services.CertTask) error {
	// Professional Deletion: Only delete the EXACT domain requested.
	// The previous "helpful" logic was deleting wildcards and root domains
	// which caused shared certificates to be lost.
	
	targetDir := filepath.Join(LetsEncryptBasePath, task.Domain)
	
	// Handle wildcard prefix in filesystem (some systems use wildcard.domain instead of *.domain)
	altDir := ""
	if strings.HasPrefix(task.Domain, "*.") {
		altDir = filepath.Join(LetsEncryptBasePath, "wildcard."+strings.TrimPrefix(task.Domain, "*."))
	}

	removed := false
	
	// Delete primary target
	if _, err := os.Stat(targetDir); err == nil {
		if err := os.RemoveAll(targetDir); err != nil {
			log.Printf("[CertController] Warning: failed to remove %s: %v", targetDir, err)
		} else {
			log.Printf("[CertController] Removed certificate directory: %s", targetDir)
			removed = true
		}
	}

	// Delete alternate wildcard name if applicable
	if altDir != "" {
		if _, err := os.Stat(altDir); err == nil {
			if err := os.RemoveAll(altDir); err != nil {
				log.Printf("[CertController] Warning: failed to remove %s: %v", altDir, err)
			} else {
				log.Printf("[CertController] Removed alternate certificate directory: %s", altDir)
				removed = true
			}
		}
	}

	if !removed {
		return fmt.Errorf("certificate not found for domain: %s (target: %s)", task.Domain, targetDir)
	}

	// Trigger nginx config rebuild via backend API before reloading
	if err := triggerConfigRebuild(ctx); err != nil {
		log.Printf("[CertController] Warning: config rebuild failed: %v", err)
	}

	return reloadNginx(ctx)
}

func processReload(ctx context.Context, task *services.CertTask) error {
	// Trigger nginx config rebuild via backend API before reloading
	if err := triggerConfigRebuild(ctx); err != nil {
		log.Printf("[CertController] Warning: config rebuild failed: %v", err)
		// Continue anyway - nginx reload might still work with existing configs
	}
	return reloadNginx(ctx)
}

func triggerConfigRebuild(ctx context.Context) error {
	backendURL := os.Getenv("BACKEND_CONTROL_URL")
	if backendURL == "" {
		backendURL = "http://backend_control:3000"
	}

	req, err := http.NewRequestWithContext(ctx, "POST", backendURL+"/api/control/system/rebuild-nginx", nil)
	if err != nil {
		return err
	}

	req.Header.Set("Authorization", "Bearer "+os.Getenv("INTERNAL_CTRL_SECRET"))
	req.Header.Set("X-Cascata-Internal-Key", os.Getenv("INTERNAL_CTRL_SECRET"))

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("backend returned %d: %s", resp.StatusCode, string(body))
	}

	log.Println("[CertController] Nginx configs rebuilt via backend")
	return nil
}

func reloadNginx(ctx context.Context) error {
	// Try nginx controller first
	if NginxControllerURL != "" {
		client := NewNginxControllerClient(NginxControllerURL)
		if err := client.Reload(ctx); err == nil {
			log.Println("[CertController] Nginx reloaded via controller")
			return nil
		} else {
			log.Printf("[CertController] Controller reload failed, trying direct: %v", err)
		}
	}

	// Fallback: direct nginx reload
	cmd := exec.CommandContext(ctx, "nginx", "-s", "reload")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("nginx reload failed: %v, output: %s", err, string(output))
	}

	log.Println("[CertController] Nginx reloaded directly")
	return nil
}

// NginxControllerClient interacts with the nginx controller service
type NginxControllerClient struct {
	baseURL string
	secret  string
}

func NewNginxControllerClient(baseURL string) *NginxControllerClient {
	return &NginxControllerClient{
		baseURL: baseURL,
		secret:  os.Getenv("INTERNAL_CTRL_SECRET"),
	}
}

func (c *NginxControllerClient) Reload(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/reload", nil)
	if err != nil {
		return err
	}
	
	req.Header.Set("X-Internal-Secret", c.secret)
	
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("nginx controller returned %d: %s", resp.StatusCode, string(body))
	}
	
	return nil
}
