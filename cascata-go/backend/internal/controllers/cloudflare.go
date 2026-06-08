package controllers

import (
	"encoding/json"
	"net/http"
	"os"

	"cascata-backend/internal/services"
	"cascata-backend/internal/types"
)

// CloudflareController gerencia integração com Cloudflare (plano gratuito)
type CloudflareController struct{}

// CheckDomain verifica se custom domain está configurado com Cloudflare proxy
// GET /api/admin/projects/{slug}/cloudflare/check
func (c *CloudflareController) CheckDomain(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context().Value(types.CascataCtxKey).(*types.CascataRequest)

	// Verificar permissão (apenas admin ou owner)
	if !ctx.IsDashboardAuth {
		http.Error(w, `{"error":"Unauthorized"}`, http.StatusForbidden)
		return
	}

	project := ctx.Project
	if project == nil {
		http.Error(w, `{"error":"Project not found"}`, http.StatusNotFound)
		return
	}

	if project.CustomDomain == "" {
		http.Error(w, `{"error":"Project has no custom domain configured"}`, http.StatusBadRequest)
		return
	}

	// Pegar IP do Cascata das env vars ou usar default
	cascataIP := os.Getenv("CASCATA_PUBLIC_IP")
	if cascataIP == "" {
		cascataIP = r.Host // Fallback
	}

	// Verificar configuração Cloudflare
	result, err := services.CheckProjectCloudflare(
		project.Slug,
		project.CustomDomain,
		cascataIP,
	)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// GetRecommendations retorna recomendações específicas para o domínio
// GET /api/admin/projects/{slug}/cloudflare/recommendations
func (c *CloudflareController) GetRecommendations(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context().Value(types.CascataCtxKey).(*types.CascataRequest)

	if !ctx.IsDashboardAuth {
		http.Error(w, `{"error":"Unauthorized"}`, http.StatusForbidden)
		return
	}

	project := ctx.Project
	if project == nil || project.CustomDomain == "" {
		http.Error(w, `{"error":"No custom domain configured"}`, http.StatusBadRequest)
		return
	}

	// Verificar status atual
	cascataIP := os.Getenv("CASCATA_PUBLIC_IP")
	if cascataIP == "" {
		cascataIP = r.Host
	}

	result, err := services.ValidateCloudflareConfig(r.Context(), project.CustomDomain, cascataIP)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}

	response := map[string]interface{}{
		"domain":            project.CustomDomain,
		"score":             result.Score,
		"is_optimized":      result.IsOptimized,
		"ddos_protected":    result.DDOSProtected,
		"recommendations":   result.Recommendations,
		"quick_wins":        getQuickWins(result),
		"next_steps":        getNextSteps(result),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// HealthCheck verifica rápidamente se domínio está protegido
// GET /api/data/{slug}/cloudflare/health
// Público - pode ser chamado pelo próprio usuário
func (c *CloudflareController) HealthCheck(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context().Value(types.CascataCtxKey).(*types.CascataRequest)
	
	project := ctx.Project
	if project == nil || project.CustomDomain == "" {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"protected": false,
			"reason":    "no_custom_domain",
			"message":   "Projeto não tem domínio customizado configurado",
		})
		return
	}

	// Check rápido
	isProtected := services.IsCloudflareProtected(project.CustomDomain)

	response := map[string]interface{}{
		"protected":    isProtected,
		"domain":     project.CustomDomain,
		"message":      getProtectionMessage(isProtected),
	}

	if !isProtected {
		response["action_required"] = "Ativar Cloudflare proxy no domínio para proteção DDoS gratuita"
		response["help_url"] = "https://developers.cloudflare.com/dns/manage-dns-records/reference/proxied-dns-records/"
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// ValidateAllProjects verifica Cloudflare em todos os projetos com custom domain
// GET /api/admin/cloudflare/validate-all (admin only)
func (c *CloudflareController) ValidateAllProjects(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context().Value(types.CascataCtxKey).(*types.CascataRequest)

	// Apenas system admin
	if !ctx.IsSystemRequest {
		http.Error(w, `{"error":"System admin required"}`, http.StatusForbidden)
		return
	}

	// Query todos projetos com custom_domain
	rows, err := services.SystemPool.Query(r.Context(),
		"SELECT slug, custom_domain FROM system.projects WHERE custom_domain IS NOT NULL AND custom_domain != ''")
	if err != nil {
		http.Error(w, `{"error":"Database error"}`, http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	cascataIP := os.Getenv("CASCATA_PUBLIC_IP")
	if cascataIP == "" {
		cascataIP = r.Host
	}

	var results []map[string]interface{}
	protectedCount := 0
	totalCount := 0

	for rows.Next() {
		var slug, domain string
		rows.Scan(&slug, &domain)
		totalCount++

		result, err := services.ValidateCloudflareConfig(r.Context(), domain, cascataIP)
		if err != nil {
			results = append(results, map[string]interface{}{
				"slug":   slug,
				"domain": domain,
				"error":  err.Error(),
			})
			continue
		}

		if result.DDOSProtected {
			protectedCount++
		}

		results = append(results, map[string]interface{}{
			"slug":         slug,
			"domain":       domain,
			"score":        result.Score,
			"protected":    result.DDOSProtected,
			"optimized":    result.IsOptimized,
			"recommendations_count": len(result.Recommendations),
		})
	}

	summary := map[string]interface{}{
		"total_projects":      totalCount,
		"protected_projects":  protectedCount,
		"protection_rate":     float64(protectedCount) / float64(totalCount) * 100,
		"unprotected_count":   totalCount - protectedCount,
		"projects":            results,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(summary)
}

// Helpers

func getQuickWins(result *services.CloudflareValidationResult) []string {
	var wins []string
	
	if !result.IsOptimized && result.Integration != nil {
		if !result.Integration.IsProxied {
			wins = append(wins, "Ativar 'Orange Cloud' no Cloudflare (proteção DDoS instantânea)")
		}
		if !result.Integration.HasSSL {
			wins = append(wins, "Configurar SSL/TLS gratuito no Cloudflare")
		}
	}
	
	return wins
}

func getNextSteps(result *services.CloudflareValidationResult) []string {
	var steps []string
	
	if result.Integration != nil && result.Integration.Settings != nil {
		if !result.Integration.Settings.AlwaysHTTPS {
			steps = append(steps, "Forçar HTTPS sempre (Always Use HTTPS)")
		}
		if !result.Integration.Settings.Brotli {
			steps = append(steps, "Ativar compressão Brotli para performance")
		}
	}
	
	return steps
}

func getProtectionMessage(protected bool) string {
	if protected {
		return "Domínio protegido com Cloudflare (DDoS + CDN ativos)"
	}
	return "Domínio não está usando Cloudflare proxy - vulnerável a DDoS"
}
