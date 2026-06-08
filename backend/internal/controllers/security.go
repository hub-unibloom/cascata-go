package controllers

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"cascata-backend/internal/middleware"
	"cascata-backend/internal/services"
	"cascata-backend/internal/types"
	"cascata-backend/internal/utils"
	"github.com/go-chi/chi/v5"
	"golang.org/x/crypto/bcrypt"
)

// SecurityController handles security-related operations (RLS policies, rate limits, API keys, etc.)
// TypeScript parity: backend/src/controllers/SecurityController.ts
type SecurityController struct {
	VaultSvc  *services.VaultService
	CryptoSvc *services.CryptoService
}

// ListPolicies returns RLS policies for the project (TypeScript parity)
func (s *SecurityController) ListPolicies(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context().Value(types.CascataCtxKey).(*types.CascataRequest)

	// Query native PostgreSQL RLS policies
	rows, err := ctx.ProjectPool.Query(r.Context(), `
		SELECT schemaname, tablename, policyname, roles, cmd, qual, with_check 
		FROM pg_policies 
		WHERE schemaname NOT IN ('pg_catalog', 'information_schema')
		ORDER BY tablename, policyname
	`)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, 500)
		return
	}
	defer rows.Close()

	// Return empty array if no policies (frontend compatibility)
	policies := []map[string]interface{}{}
	for rows.Next() {
		var schema, table, name, command string
		var roles []string
		var qual, withCheck *string
		rows.Scan(&schema, &table, &name, &roles, &command, &qual, &withCheck)
		policies = append(policies, map[string]interface{}{
			"schema":     schema,
			"table":      table,
			"name":       name,
			"roles":      roles,
			"command":    command,
			"qual":       qual,
			"with_check": withCheck,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(policies)
}

// GetStatus returns current security status (RPS, panic mode) from Dragonfly (in-memory)
// This provides real-time edge defense status without hitting PostgreSQL
func (s *SecurityController) GetStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context().Value(types.CascataCtxKey).(*types.CascataRequest)
	
	// Read from Dragonfly (edge cache) - not PostgreSQL for speed
	panicMode := services.CheckPanic(ctx.Project.Slug)
	currentRps := services.GetCurrentRPS(ctx.Project.Slug)
	
	// Se panic mode ativo, retorna info sobre quem está whitelisted
	response := map[string]interface{}{
		"current_rps": currentRps,
		"panic_mode":  panicMode,
	}
	
	if panicMode {
		adminWhitelisted := services.GetPanicAdmin(ctx.Project.Slug)
		response["whitelisted_admin"] = adminWhitelisted
		
		// Verifica se o usuário atual é o whitelisted
		currentUser := ""
		if ctx.User != nil {
			if sub, ok := ctx.User["sub"].(string); ok {
				currentUser = sub
			}
		}
		response["you_are_whitelisted"] = (adminWhitelisted != "" && adminWhitelisted == currentUser)
	}
	
	json.NewEncoder(w).Encode(response)
}

// TogglePanic enables/disables panic mode via Dragonfly (immediate edge effect)
// Saves the admin's IP/UserID to whitelist their session during lockdown
func (s *SecurityController) TogglePanic(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context().Value(types.CascataCtxKey).(*types.CascataRequest)
	var body struct{ Enabled bool `json:"enabled"` }
	json.NewDecoder(r.Body).Decode(&body)

	// Captura identificador do admin (IP ou UserID do JWT) para whitelisting
	adminIdentifier := getClientIP(r)
	adminEmail := ""
	// Tenta obter UserID do JWT (claim "sub") se disponível
	if ctx.User != nil {
		if sub, ok := ctx.User["sub"].(string); ok && sub != "" {
			adminIdentifier = sub // Prefere UserID do JWT (mais confiável que IP)
		}
		if email, ok := ctx.User["email"].(string); ok {
			adminEmail = email
		}
	}

	// 1. Set in Dragonfly (immediate effect - edge defense)
	// Salva o identificador do admin que ativou para permitir acesso durante lockdown
	if err := services.SetPanic(ctx.Project.Slug, body.Enabled, adminIdentifier); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), 500)
		return
	}

	// 2. Persist to PostgreSQL (durability across restarts)
	// Non-blocking: even if DB fails, Dragonfly state is active
	go func() {
		services.SystemPool.Exec(r.Context(),
			"UPDATE system.projects SET metadata = jsonb_set(COALESCE(metadata, '{}'::jsonb), '{security,panic_mode}', $1::jsonb) WHERE slug = $2",
			fmt.Sprintf("%t", body.Enabled), ctx.Project.Slug)
	}()

	// 3. Audit Trail - Log this critical security action
	actionDesc := fmt.Sprintf("Panic mode %s for project %s", 
		map[bool]string{true: "ACTIVATED", false: "DEACTIVATED"}[body.Enabled], 
		ctx.Project.Slug)
	go services.LogSecurityAction(r.Context(), 
		services.ActionSecurityPanicToggle,
		adminIdentifier,
		getClientIP(r),
		ctx.Project.Slug,
		actionDesc,
		map[string]interface{}{
			"admin_email":      adminEmail,
			"previous_state":   !body.Enabled,
			"new_state":        body.Enabled,
			"whitelisted_id":   adminIdentifier,
			"triggered_by":     "dashboard",
		})

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":     true,
		"panic_mode":  body.Enabled,
		"edge_synced": true,
		"whitelisted": adminIdentifier,
	})
}

// getClientIP extrai o IP real do cliente considerando proxies
func getClientIP(r *http.Request) string {
	if ip := r.Header.Get("X-Real-Ip"); ip != "" {
		return ip
	}
	if ip := r.Header.Get("X-Forwarded-For"); ip != "" {
		return strings.Split(ip, ",")[0]
	}
	host, _, _ := strings.Cut(r.RemoteAddr, ":")
	return host
}

// ListRateLimits returns configured rate limits
func (s *SecurityController) ListRateLimits(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context().Value(types.CascataCtxKey).(*types.CascataRequest)
	rows, err := services.SystemPool.Query(r.Context(),
		"SELECT * FROM system.rate_limits WHERE project_slug = $1 ORDER BY created_at DESC", ctx.Project.Slug)
	if err != nil {
		json.NewEncoder(w).Encode([]map[string]interface{}{})
		return
	}
	defer rows.Close()
	result := []map[string]interface{}{}
	fDesc := rows.FieldDescriptions()
	for rows.Next() {
		vals, _ := rows.Values()
		row := make(map[string]interface{})
		for i, fd := range fDesc { row[fd.Name] = utils.PurifyPgxValue(vals[i]) }
		
		// Reconstruct metadata for frontend compatibility
		metadata := map[string]interface{}{
			"is_cumulative":     row["is_cumulative"],
			"operation_weights": row["operation_weights"],
			"time_windows":      row["time_windows"],
			"crud_limits":       row["crud_limits"],
			"group_limits":      row["group_limits"],
		}
		row["metadata"] = metadata

		result = append(result, row)
	}
	json.NewEncoder(w).Encode(result)
}

// CreateRateLimit creates a rate limit rule
func (s *SecurityController) CreateRateLimit(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context().Value(types.CascataCtxKey).(*types.CascataRequest)
	var body struct {
		RoutePattern      string                 `json:"route_pattern"`
		Method            string                 `json:"method"`
		WindowSeconds     *int                   `json:"window_seconds"`
		WindowSecondsAnon *int                   `json:"window_seconds_anon"`
		WindowSecondsAuth *int                   `json:"window_seconds_auth"`
		RateLimitAnon     *int                   `json:"rate_limit_anon"`
		BurstLimitAnon    *int                   `json:"burst_limit_anon"`
		RateLimitAuth     *int                   `json:"rate_limit_auth"`
		BurstLimitAuth    *int                   `json:"burst_limit_auth"`
		RateLimit         *int                   `json:"rate_limit"`
		BurstLimit        *int                   `json:"burst_limit"`
		MessageAnon       *string                `json:"message_anon"`
		MessageAuth       *string                `json:"message_auth"`
		Metadata          map[string]interface{} `json:"metadata"`
	}
	json.NewDecoder(r.Body).Decode(&body)

	// Fallback logic using pointers to avoid overriding explicit values (like 0)
	var rLimit, bLimit, rLimitAnon, bLimitAnon, rLimitAuth, bLimitAuth int
	var wSecs, wSecsAnon, wSecsAuth int

	// Global / root limits logic (falling back to anon/auth if root is not specified)
	if body.RateLimit != nil {
		rLimit = *body.RateLimit
	} else if body.RateLimitAnon != nil {
		rLimit = *body.RateLimitAnon
	} else if body.RateLimitAuth != nil {
		rLimit = *body.RateLimitAuth / 2
		if rLimit == 0 { rLimit = 1 }
	} else {
		rLimit = 10
	}

	if body.BurstLimit != nil {
		bLimit = *body.BurstLimit
	} else if body.BurstLimitAnon != nil {
		bLimit = *body.BurstLimitAnon
	} else if body.BurstLimitAuth != nil {
		bLimit = *body.BurstLimitAuth / 2
		if bLimit == 0 { bLimit = 1 }
	} else {
		bLimit = 5
	}

	if body.WindowSeconds != nil {
		wSecs = *body.WindowSeconds
	} else if body.WindowSecondsAnon != nil {
		wSecs = *body.WindowSecondsAnon
	} else if body.WindowSecondsAuth != nil {
		wSecs = *body.WindowSecondsAuth
	} else {
		wSecs = 1
	}

	// Granular limits logic, falling back to global if not provided
	if body.RateLimitAnon != nil { rLimitAnon = *body.RateLimitAnon } else { rLimitAnon = rLimit }
	if body.BurstLimitAnon != nil { bLimitAnon = *body.BurstLimitAnon } else { bLimitAnon = bLimit }
	if body.WindowSecondsAnon != nil { wSecsAnon = *body.WindowSecondsAnon } else { wSecsAnon = wSecs }

	if body.RateLimitAuth != nil { rLimitAuth = *body.RateLimitAuth } else { rLimitAuth = rLimit * 2 }
	if body.BurstLimitAuth != nil { bLimitAuth = *body.BurstLimitAuth } else { bLimitAuth = bLimit * 2 }
	if body.WindowSecondsAuth != nil { wSecsAuth = *body.WindowSecondsAuth } else { wSecsAuth = wSecs }

	var crudLimits interface{}
	var groupLimits interface{}
	var timeWindows interface{}
	var operationWeights interface{}
	isCumulative := false

	if body.Metadata != nil {
		if cl, ok := body.Metadata["crud_limits"]; ok {
			crudLimits = cl
		}
		if gl, ok := body.Metadata["group_limits"]; ok {
			groupLimits = gl
		}
		if tw, ok := body.Metadata["time_windows"]; ok {
			timeWindows = tw
		}
		if ow, ok := body.Metadata["operation_weights"]; ok {
			operationWeights = ow
		}
		if ic, ok := body.Metadata["is_cumulative"]; ok {
			if b, ok := ic.(bool); ok {
				isCumulative = b
			} else if icMap, ok := ic.(map[string]interface{}); ok {
				for _, v := range icMap {
					if b, ok := v.(bool); ok && b {
						isCumulative = true
						break
					}
				}
			}
		}
	}

	var crudLimitsJSON, groupLimitsJSON, timeWindowsJSON, operationWeightsJSON []byte
	var err error

	if crudLimits != nil {
		if crudLimitsJSON, err = json.Marshal(crudLimits); err != nil {
			log.Printf("[CreateRateLimit] Error marshaling crudLimits: %v", err)
		}
	}
	if groupLimits != nil {
		if groupLimitsJSON, err = json.Marshal(groupLimits); err != nil {
			log.Printf("[CreateRateLimit] Error marshaling groupLimits: %v", err)
		}
	}
	if timeWindows != nil {
		if timeWindowsJSON, err = json.Marshal(timeWindows); err != nil {
			log.Printf("[CreateRateLimit] Error marshaling timeWindows: %v", err)
		}
	}
	if operationWeights != nil {
		if operationWeightsJSON, err = json.Marshal(operationWeights); err != nil {
			log.Printf("[CreateRateLimit] Error marshaling operationWeights: %v", err)
		}
	}

	var crudParam interface{} = crudLimitsJSON
	if crudLimits == nil { crudParam = nil }

	var groupParam interface{} = groupLimitsJSON
	if groupLimits == nil { groupParam = nil }

	var timeWindowsParam interface{} = timeWindowsJSON
	if timeWindows == nil { timeWindowsParam = nil }

	var weightsParam interface{} = operationWeightsJSON
	if operationWeights == nil { weightsParam = nil }

	sql := `
		INSERT INTO system.rate_limits (
			project_slug, route_pattern, method, rate_limit, burst_limit, 
			rate_limit_anon, burst_limit_anon, rate_limit_auth, burst_limit_auth, 
			window_seconds, window_seconds_anon, window_seconds_auth,
			message_anon, message_auth, crud_limits, group_limits, 
			time_windows, operation_weights, is_cumulative
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19)
		ON CONFLICT (project_slug, route_pattern, method) 
		DO UPDATE SET 
			rate_limit = EXCLUDED.rate_limit,
			burst_limit = EXCLUDED.burst_limit,
			rate_limit_anon = EXCLUDED.rate_limit_anon, 
			burst_limit_anon = EXCLUDED.burst_limit_anon,
			rate_limit_auth = EXCLUDED.rate_limit_auth, 
			burst_limit_auth = EXCLUDED.burst_limit_auth,
			window_seconds = EXCLUDED.window_seconds,
			window_seconds_anon = EXCLUDED.window_seconds_anon,
			window_seconds_auth = EXCLUDED.window_seconds_auth,
			message_anon = EXCLUDED.message_anon,
			message_auth = EXCLUDED.message_auth,
			crud_limits = EXCLUDED.crud_limits,
			group_limits = EXCLUDED.group_limits,
			time_windows = EXCLUDED.time_windows,
			operation_weights = EXCLUDED.operation_weights,
			is_cumulative = EXCLUDED.is_cumulative`

	_, execErr := services.SystemPool.Exec(r.Context(), sql,
		ctx.Project.Slug,
		body.RoutePattern,
		body.Method,
		rLimit,
		bLimit,
		rLimitAnon,
		bLimitAnon,
		rLimitAuth,
		bLimitAuth,
		wSecs,
		wSecsAnon,
		wSecsAuth,
		body.MessageAnon,
		body.MessageAuth,
		crudParam,
		groupParam,
		timeWindowsParam,
		weightsParam,
		isCumulative,
	)
	if execErr != nil {
		log.Printf("[CreateRateLimit] Error inserting/updating rate limit: %v", execErr)
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, execErr.Error()), 500)
		return
	}
	
	// Atualiza cache no Dragonfly para o edge limiter
	go middleware.RefreshRateLimitCache(r.Context(), ctx.Project.Slug)
	
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// DeleteRateLimit deletes a rate limit rule
func (s *SecurityController) DeleteRateLimit(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context().Value(types.CascataCtxKey).(*types.CascataRequest)
	id := chi.URLParam(r, "id")

	// Busca o route_pattern antes de deletar (para logging)
	var routePattern string
	services.SystemPool.QueryRow(r.Context(),
		"SELECT route_pattern FROM system.rate_limits WHERE id = $1 AND project_slug = $2",
		id, ctx.Project.Slug).Scan(&routePattern)

	services.SystemPool.Exec(r.Context(), "DELETE FROM system.rate_limits WHERE id = $1 AND project_slug = $2", id, ctx.Project.Slug)

	// Invalida cache de rate limit no Dragonfly (força recarregamento)
	go func() {
		// Pequeno delay para garantir que a transação do banco commitou
		time.Sleep(100 * time.Millisecond)
		if err := middleware.InvalidateRateLimitCache(r.Context(), ctx.Project.Slug); err != nil {
			log.Printf("[DeleteRateLimit] Warning: Failed to invalidate cache: %v", err)
		}
		// Recarrega o cache com dados atualizados
		middleware.RefreshRateLimitCache(r.Context(), ctx.Project.Slug)
	}()

	log.Printf("[DeleteRateLimit] Rate limit deleted for project=%s, route=%s", ctx.Project.Slug, routePattern)
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// --- KEY GROUPS ---

// ListKeyGroups returns all API key groups
func (s *SecurityController) ListKeyGroups(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context().Value(types.CascataCtxKey).(*types.CascataRequest)
	rows, err := services.SystemPool.Query(r.Context(),
		"SELECT * FROM system.api_key_groups WHERE project_slug = $1 ORDER BY name ASC", ctx.Project.Slug)
	if err != nil {
		json.NewEncoder(w).Encode([]map[string]interface{}{})
		return
	}
	defer rows.Close()
	result := []map[string]interface{}{}
	fDesc := rows.FieldDescriptions()
	for rows.Next() {
		vals, _ := rows.Values()
		row := make(map[string]interface{})
		for i, fd := range fDesc { row[fd.Name] = utils.PurifyPgxValue(vals[i]) }
		result = append(result, row)
	}
	json.NewEncoder(w).Encode(result)
}

// CreateKeyGroup creates a new API key group
func (s *SecurityController) CreateKeyGroup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context().Value(types.CascataCtxKey).(*types.CascataRequest)
	var body struct {
		Name          string                 `json:"name"`
		RateLimit     int                    `json:"rate_limit"`
		BurstLimit    int                    `json:"burst_limit"`
		WindowSeconds int                    `json:"window_seconds"`
		CrudLimits    map[string]interface{} `json:"crud_limits"`
		Scopes        []string               `json:"scopes"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	var result map[string]interface{}
	services.SystemPool.QueryRow(r.Context(), `
		INSERT INTO system.api_key_groups (project_slug, name, rate_limit, burst_limit, window_seconds, crud_limits, scopes)
		VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING *`,
		ctx.Project.Slug, body.Name, body.RateLimit, body.BurstLimit, body.WindowSeconds, body.CrudLimits, body.Scopes).Scan(&result)
	json.NewEncoder(w).Encode(result)
}

// UpdateKeyGroup updates an API key group
func (s *SecurityController) UpdateKeyGroup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context().Value(types.CascataCtxKey).(*types.CascataRequest)
	id := chi.URLParam(r, "id")
	var body struct {
		Name       string `json:"name"`
		RateLimit  int    `json:"rate_limit"`
		BurstLimit int    `json:"burst_limit"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	services.SystemPool.Exec(r.Context(),
		"UPDATE system.api_key_groups SET name = $1, rate_limit = $2, burst_limit = $3 WHERE id = $4 AND project_slug = $5",
		body.Name, body.RateLimit, body.BurstLimit, id, ctx.Project.Slug)
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// DeleteKeyGroup removes an API key group
func (s *SecurityController) DeleteKeyGroup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context().Value(types.CascataCtxKey).(*types.CascataRequest)
	id := chi.URLParam(r, "id")
	var count int
	services.SystemPool.QueryRow(r.Context(), "SELECT COUNT(*) FROM system.api_keys WHERE group_id = $1", id).Scan(&count)
	if count > 0 {
		http.Error(w, `{"error":"Cannot delete group: it has active keys."}`, 400)
		return
	}
	services.SystemPool.Exec(r.Context(), "DELETE FROM system.api_key_groups WHERE id = $1 AND project_slug = $2", id, ctx.Project.Slug)
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// CreatePolicy creates a new RLS policy
func (s *SecurityController) CreatePolicy(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context().Value(types.CascataCtxKey).(*types.CascataRequest)
	if !ctx.IsSystemRequest {
		http.Error(w, `{"error":"Unauthorized: Dashboard only."}`, 403)
		return
	}
	var body struct {
		Name      string `json:"name"`
		Table     string `json:"table"`
		Command   string `json:"command"`
		Role      string `json:"role"`
		Using     string `json:"using"`
		WithCheck string `json:"withCheck"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	sql := fmt.Sprintf("CREATE POLICY %s ON public.%s FOR %s TO %s USING (%s)",
		utils.QuoteId(body.Name), utils.QuoteId(body.Table), body.Command, body.Role, body.Using)
	if body.WithCheck != "" {
		sql += fmt.Sprintf(" WITH CHECK (%s)", body.WithCheck)
	}
	_, err := ctx.ProjectPool.Exec(r.Context(), sql)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), 500)
		return
	}
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// DeletePolicy removes an RLS policy
func (s *SecurityController) DeletePolicy(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context().Value(types.CascataCtxKey).(*types.CascataRequest)
	table := chi.URLParam(r, "table")
	name := chi.URLParam(r, "name")
	sql := fmt.Sprintf("DROP POLICY %s ON public.%s", utils.QuoteId(name), utils.QuoteId(table))
	_, err := ctx.ProjectPool.Exec(r.Context(), sql)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), 500)
		return
	}
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// --- API KEYS ---

// ListApiKeys returns all API keys for the project
func (s *SecurityController) ListApiKeys(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context().Value(types.CascataCtxKey).(*types.CascataRequest)
	rows, err := services.SystemPool.Query(r.Context(), `
		SELECT k.id, k.name, k.prefix, k.scopes, k.rate_limit, k.burst_limit, 
		       k.expires_at, k.last_used_at, k.is_active, k.created_at, k.group_id, 
		       k.vault_item_id, g.name as group_name
		FROM system.api_keys k
		LEFT JOIN system.api_key_groups g ON k.group_id = g.id
		WHERE k.project_slug = $1 ORDER BY k.created_at DESC`, ctx.Project.Slug)
	if err != nil {
		json.NewEncoder(w).Encode([]map[string]interface{}{})
		return
	}
	defer rows.Close()
	result := []map[string]interface{}{}
	fDesc := rows.FieldDescriptions()
	for rows.Next() {
		vals, _ := rows.Values()
		row := make(map[string]interface{})
		for i, fd := range fDesc { row[fd.Name] = utils.PurifyPgxValue(vals[i]) }
		result = append(result, row)
	}
	json.NewEncoder(w).Encode(result)
}

// CreateApiKey generates a new API key with secure hashing
func (s *SecurityController) CreateApiKey(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context().Value(types.CascataCtxKey).(*types.CascataRequest)
	var body struct {
		Name          string   `json:"name"`
		Scopes        []string `json:"scopes"`
		RateLimit     int      `json:"rate_limit"`
		BurstLimit    int      `json:"burst_limit"`
		ExpiresInDays int      `json:"expires_in_days"`
		GroupID       *string  `json:"group_id"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	if body.Name == "" {
		http.Error(w, `{"error":"Name is required"}`, 400)
		return
	}
	// Generate secure key: sk_live_UUID_RANDOM
	uuidBytes := make([]byte, 16)
	rand.Read(uuidBytes)
	uuid := hex.EncodeToString(uuidBytes)
	randomBytes := make([]byte, 12)
	rand.Read(randomBytes)
	random := hex.EncodeToString(randomBytes)
	rawKey := fmt.Sprintf("sk_live_%s_%s", uuid, random)
	lookupIndex := fmt.Sprintf("sk_live_%s", uuid)
	// Hash the key (Legacy compatibility - we keep bcrypt for now but move to Vault)
	hashedKey, _ := bcrypt.GenerateFromPassword([]byte(rawKey), 10)

	// --- GLORY UPGRADE: Vault Integration ---
	var vaultID *string
	if s.VaultSvc != nil && s.CryptoSvc != nil {
		cipher, err := s.CryptoSvc.Encrypt(ctx.Project.Slug, rawKey)
		if err == nil {
			metaObj := map[string]interface{}{
				"release_policy": string(services.VaultPolicyVerifyOnly),
				"created_by":     "system_security_ctrl",
			}
			meta, _ := json.Marshal(metaObj)

			err = services.SystemPool.QueryRow(r.Context(), `
				INSERT INTO system.project_secrets (project_slug, name, type, description, secret_value, metadata)
				VALUES ($1, $2, 'key', $3, $4, $5)
				RETURNING id::text`,
				ctx.Project.Slug, 
				fmt.Sprintf("API Key: %s", body.Name),
				fmt.Sprintf("Vault-protected credential for '%s'", body.Name),
				cipher, 
				meta).Scan(&vaultID)
			
			if err != nil {
				log.Printf("[CreateApiKey] Warning: Vault storage failed: %v", err)
			}
		}
	}

	var expiresAt *time.Time
	if body.ExpiresInDays > 0 {
		t := time.Now().AddDate(0, 0, body.ExpiresInDays)
		expiresAt = &t
	}
	var result map[string]interface{}
	services.SystemPool.QueryRow(r.Context(), `
		INSERT INTO system.api_keys (project_slug, name, key_hash, lookup_index, prefix, scopes, rate_limit, burst_limit, expires_at, group_id, vault_item_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		RETURNING id, name, prefix, scopes, rate_limit, burst_limit, expires_at, created_at, group_id`,
		ctx.Project.Slug, body.Name, string(hashedKey), lookupIndex, "sk_live_",
		body.Scopes, body.RateLimit, body.BurstLimit, expiresAt, body.GroupID, vaultID).Scan(&result)
	result["secret"] = rawKey
	json.NewEncoder(w).Encode(result)
}

// UpdateApiKey updates API key properties
func (s *SecurityController) UpdateApiKey(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context().Value(types.CascataCtxKey).(*types.CascataRequest)
	id := chi.URLParam(r, "id")
	var body struct {
		ExpiresAt *time.Time `json:"expires_at"`
		IsActive  *bool      `json:"is_active"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	updates := []string{}
	values := []interface{}{}
	idx := 1
	if body.ExpiresAt != nil {
		updates = append(updates, fmt.Sprintf("expires_at = $%d", idx))
		values = append(values, *body.ExpiresAt)
		idx++
	}
	if body.IsActive != nil {
		updates = append(updates, fmt.Sprintf("is_active = $%d", idx))
		values = append(values, *body.IsActive)
		idx++
	}
	if len(updates) == 0 {
		json.NewEncoder(w).Encode(map[string]bool{"success": true})
		return
	}
	values = append(values, id, ctx.Project.Slug)
	query := fmt.Sprintf("UPDATE system.api_keys SET %s WHERE id = $%d AND project_slug = $%d",
		strings.Join(updates, ", "), idx, idx+1)
	services.SystemPool.Exec(r.Context(), query, values...)
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// DeleteApiKey removes an API key
func (s *SecurityController) DeleteApiKey(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context().Value(types.CascataCtxKey).(*types.CascataRequest)
	id := chi.URLParam(r, "id")
	services.SystemPool.Exec(r.Context(), "DELETE FROM system.api_keys WHERE id = $1 AND project_slug = $2", id, ctx.Project.Slug)
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}
