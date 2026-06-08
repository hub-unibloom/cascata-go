package services

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
)

// CloudflareIntegration verifica e gerencia integração com Cloudflare (plano gratuito)
// Oferece: DDoS protection básica, CDN global, SSL/TLS gratuito, DNS rápido
type CloudflareIntegration struct {
	Domain    string                 `json:"domain"`
	IsProxied bool                   `json:"is_proxied"`        // Orange cloud ativado
	HasSSL    bool                   `json:"has_ssl"`           // SSL/TLS configurado
	DNSValid  bool                   `json:"dns_valid"`         // Aponta para Cascata
	Headers   map[string]string      `json:"headers,omitempty"` // CF-Ray, CF-Cache-Status, etc
	Settings  *CloudflareSettings    `json:"settings,omitempty"`
	CheckedAt time.Time              `json:"checked_at"`
}

type CloudflareSettings struct {
	SSLMode        string `json:"ssl_mode"`        // flexible, full, strict
	AlwaysHTTPS    bool   `json:"always_https"`    // Redirect HTTP→HTTPS
	AutoMinify     bool   `json:"auto_minify"`     // Minify CSS/JS/HTML
	Brotli         bool   `json:"brotli"`          // Compressão Brotli
	IPv6           bool   `json:"ipv6"`            // IPv6 ativado
	WebSockets     bool   `json:"websockets"`      // WebSockets suportados
	CacheLevel     string `json:"cache_level"`     // aggressive, standard, basic
}

// CheckCloudflareStatus detecta se domínio está usando Cloudflare proxy
// Faz requisição HEAD e analisa headers de resposta
func CheckCloudflareStatus(domain string, cascataIP string) (*CloudflareIntegration, error) {
	status := &CloudflareIntegration{
		Domain:    domain,
		Headers:   make(map[string]string),
		CheckedAt: time.Now(),
	}

	// Normalizar domínio
	domain = strings.TrimPrefix(domain, "http://")
	domain = strings.TrimPrefix(domain, "https://")
	domain = strings.TrimSuffix(domain, "/")

	// Fazer requisição HTTP para verificar headers CF
	client := &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse // Não seguir redirects
		},
	}

	resp, err := client.Head(fmt.Sprintf("http://%s", domain))
	if err != nil {
		log.Printf("[CloudflareCheck] Erro ao verificar %s: %v", domain, err)
		// Tentar HTTPS mesmo com erro HTTP
		resp, err = client.Head(fmt.Sprintf("https://%s", domain))
		if err != nil {
			return status, fmt.Errorf("domínio não responde: %w", err)
		}
	}
	defer resp.Body.Close()

	// Verificar headers Cloudflare
	for k, v := range resp.Header {
		headerValue := strings.ToLower(k)
		if strings.HasPrefix(headerValue, "cf-") || 
		   strings.HasPrefix(headerValue, "cdn-") {
			status.Headers[k] = strings.Join(v, ", ")
		}
	}

	// Detectar proxy Cloudflare (orange cloud)
	// CF-Ray header só existe quando passa pelo proxy
	if ray := resp.Header.Get("CF-Ray"); ray != "" {
		status.IsProxied = true
		status.Headers["CF-Ray"] = ray
	}

	// CF-Cache-Status indica cache ativo
	if cache := resp.Header.Get("CF-Cache-Status"); cache != "" {
		status.Headers["CF-Cache-Status"] = cache
	}

	// Verificar SSL (Server header pode indicar)
	if server := resp.Header.Get("Server"); strings.Contains(server, "cloudflare") {
		status.HasSSL = resp.TLS != nil
	}

	// Verificar se DNS aponta para Cascata
	// Compara IP real vs IP esperado
	status.DNSValid = checkDNSPointsToCascata(domain, cascataIP)

	// Detectar settings via headers adicionais
	status.Settings = detectCloudflareSettings(resp.Header)

	log.Printf("[CloudflareCheck] %s - Proxied: %v, SSL: %v, DNS: %v", 
		domain, status.IsProxied, status.HasSSL, status.DNSValid)

	return status, nil
}

// detectCloudflareSettings infere configurações CF via headers
func detectCloudflareSettings(headers http.Header) *CloudflareSettings {
	settings := &CloudflareSettings{
		SSLMode:    "unknown",
		CacheLevel: "standard",
	}

	// Strict-Transport-Security indica Always HTTPS
	if hsts := headers.Get("Strict-Transport-Security"); hsts != "" {
		settings.AlwaysHTTPS = true
	}

	// CF-Cache-Status indica nível de cache
	if cache := headers.Get("CF-Cache-Status"); cache != "" {
		switch cache {
		case "HIT", "MISS", "EXPIRED":
			settings.CacheLevel = "aggressive"
		case "DYNAMIC":
			settings.CacheLevel = "standard"
		case "BYPASS":
			settings.CacheLevel = "basic"
		}
	}

	// Accept-Encoding indica Brotli suporte
	if enc := headers.Get("Accept-Encoding"); strings.Contains(enc, "br") {
		settings.Brotli = true
	}

	return settings
}

// checkDNSPointsToCascata verifica se DNS aponta para IPs do Cascata
func checkDNSPointsToCascata(domain, expectedIP string) bool {
	// Fazer lookup DNS
	// Nota: Em produção, usar net.LookupHost ou resolver customizado
	// Simplificado: verificar se IP real é diferente do IP do servidor
	// (indicando que passa por proxy/CDN)
	return true // Placeholder - implementar lookup real
}

// GetCloudflareRecommendations retorna recomendações de otimização
func (cf *CloudflareIntegration) GetRecommendations() []string {
	var recs []string

	if !cf.IsProxied {
		recs = append(recs, "Ativar 'Orange Cloud' no Cloudflare para proteção DDoS + CDN")
	}

	if !cf.HasSSL {
		recs = append(recs, "Configurar SSL/TLS no Cloudflare (Flexible ou Full)")
	}

	if cf.Settings != nil {
		if !cf.Settings.AlwaysHTTPS {
			recs = append(recs, "Ativar 'Always Use HTTPS' no Cloudflare")
		}
		if !cf.Settings.Brotli {
			recs = append(recs, "Ativar compressão Brotli para melhor performance")
		}
	}

	if !cf.DNSValid {
		recs = append(recs, "Verificar se DNS aponta para o servidor Cascata")
	}

	return recs
}

// ValidateCloudflareConfig verifica configuração completa para um projeto
func ValidateCloudflareConfig(ctx context.Context, domain, cascataIP string) (*CloudflareValidationResult, error) {
	status, err := CheckCloudflareStatus(domain, cascataIP)
	if err != nil {
		return nil, err
	}

	result := &CloudflareValidationResult{
		Integration:    status,
		IsOptimized:    status.IsProxied && status.HasSSL && status.DNSValid,
		Recommendations: status.GetRecommendations(),
		DDOSProtected:  status.IsProxied, // Proxy = proteção DDoS básica
	}

	// Score de 0-100
	score := 0
	if status.IsProxied {
		score += 40 // Proxy = DDoS + CDN
	}
	if status.HasSSL {
		score += 30 // SSL/TLS
	}
	if status.DNSValid {
		score += 20 // DNS correto
	}
	if status.Settings != nil && status.Settings.AlwaysHTTPS {
		score += 10 // HTTPS forçado
	}
	result.Score = score

	return result, nil
}

// CloudflareValidationResult resultado completo da validação
type CloudflareValidationResult struct {
	Integration     *CloudflareIntegration `json:"integration"`
	IsOptimized     bool                     `json:"is_optimized"`
	Score           int                      `json:"score"` // 0-100
	Recommendations []string               `json:"recommendations,omitempty"`
	DDOSProtected   bool                   `json:"ddos_protected"`
}

// Serve para JSON marshal
func (r *CloudflareValidationResult) ToJSON() ([]byte, error) {
	return json.Marshal(r)
}

// LogValidation imprime resultado no log
func (r *CloudflareValidationResult) LogValidation(projectSlug string) {
	log.Printf("[Cloudflare] Projeto: %s | Score: %d/100 | DDoS: %v | Optimized: %v",
		projectSlug, r.Score, r.DDOSProtected, r.IsOptimized)
	
	if len(r.Recommendations) > 0 {
		for _, rec := range r.Recommendations {
			log.Printf("[Cloudflare] Recomendação %s: %s", projectSlug, rec)
		}
	}
}

// CheckProjectCloudflare verifica configuração CF para um projeto específico
func CheckProjectCloudflare(projectSlug, customDomain, cascataIP string) (*CloudflareValidationResult, error) {
	if customDomain == "" {
		return nil, fmt.Errorf("projeto não tem custom domain configurado")
	}

	result, err := ValidateCloudflareConfig(context.Background(), customDomain, cascataIP)
	if err != nil {
		return nil, err
	}

	result.LogValidation(projectSlug)
	return result, nil
}

// IsCloudflareProtected verifica rapidamente se domínio tem proteção CF
func IsCloudflareProtected(domain string) bool {
	status, err := CheckCloudflareStatus(domain, "")
	if err != nil {
		return false
	}
	return status.IsProxied
}

// Cache purge (requer API token - opcional)
// Documentação: https://api.cloudflare.com/#zone-purge-all-files
func PurgeCloudflareCache(zoneID, apiToken string) error {
	// Implementação futura: purge via API quando usuário fornecer token
	log.Printf("[Cloudflare] Cache purge solicitado para zone %s", zoneID)
	return fmt.Errorf("funcionalidade requer Cloudflare API Token (opcional)")
}
