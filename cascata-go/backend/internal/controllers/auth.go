package controllers

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"cascata-backend/internal/services"
	"cascata-backend/internal/types"
	"cascata-backend/internal/utils"
	"github.com/go-chi/chi/v5"
	"golang.org/x/crypto/bcrypt"
)

type AuthController struct {
	goTrue services.GoTrueService
}

func (c *AuthController) Signup(w http.ResponseWriter, r *http.Request) {
	log.Printf("[AUTH-SIGNUP] ===== INÍCIO DO SIGNUP =====")
	log.Printf("[AUTH-SIGNUP] Método: %s, Path: %s, Content-Type: %s", r.Method, r.URL.Path, r.Header.Get("Content-Type"))
	log.Printf("[AUTH-SIGNUP] Headers: apikey=%s, authorization=%v", 
		r.Header.Get("apikey"), 
		strings.HasPrefix(r.Header.Get("Authorization"), "Bearer"))

	val := r.Context().Value(types.CascataCtxKey)
	if val == nil {
		log.Printf("[AUTH-SIGNUP] ✗ ERRO: Contexto não encontrado no request")
		http.Error(w, `{"error":"Context Lost"}`, 500)
		return
	}
	ctx := val.(*types.CascataRequest)
	log.Printf("[AUTH-SIGNUP] ✓ Contexto obtido - Tenant: %+v", ctx.Project)

	if ctx.Project == nil {
		log.Printf("[AUTH-SIGNUP] ✗ ERRO: Projeto não resolvido no contexto")
		http.Error(w, `{"error":"Project Context Required"}`, 404)
		return
	}
	log.Printf("[AUTH-SIGNUP] ✓ Projeto resolvido: %s", ctx.Project.Name)

	var params map[string]interface{}
	bodyErr := json.NewDecoder(r.Body).Decode(&params)
	if bodyErr != nil {
		log.Printf("[AUTH-SIGNUP] ⚠ Erro ao decodificar body (pode ser normal): %v", bodyErr)
	} else {
		log.Printf("[AUTH-SIGNUP] ✓ Body decodificado - params: %v", params)
	}

	// Log dos parâmetros importantes (sem senha em claro)
	if email, ok := params["email"]; ok {
		log.Printf("[AUTH-SIGNUP]   → Email: %v", email)
	}
	if provider, ok := params["provider"]; ok {
		log.Printf("[AUTH-SIGNUP]   → Provider: %v", provider)
	}
	if strategies, ok := params["strategies"]; ok {
		log.Printf("[AUTH-SIGNUP]   → Strategies count: %d", len(strategies.([]interface{})))
	}

	deviceInfo := types.DeviceInfo{
		IP:        ctx.IP,
		UserAgent: r.Header.Get("User-Agent"),
	}
	log.Printf("[AUTH-SIGNUP] Device Info: IP=%s, UA=%s", deviceInfo.IP, deviceInfo.UserAgent)

	log.Printf("[AUTH-SIGNUP] Chamando goTrue.HandleSignup...")
	res, err := c.goTrue.HandleSignup(r.Context(), ctx.ProjectPool, params, ctx.Project.JWTSecret, deviceInfo)
	if err != nil {
		log.Printf("[AUTH-SIGNUP] ✗ ERRO no HandleSignup: %v", err)
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), 400)
		return
	}

	log.Printf("[AUTH-SIGNUP] ✓ SUCESSO - Resposta: %v", res)
	log.Printf("[AUTH-SIGNUP] ===== FIM DO SIGNUP =====")
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)
}

func (c *AuthController) Token(w http.ResponseWriter, r *http.Request) {
	val := r.Context().Value(types.CascataCtxKey)
	if val == nil {
		http.Error(w, `{"error":"Context Lost"}`, 500)
		return
	}
	ctx := val.(*types.CascataRequest)
	if ctx.Project == nil {
		http.Error(w, `{"error":"Project Context Required"}`, 404)
		return
	}

	var params services.GoTrueTokenParams
	
	// PRIORITY 1: Query parameters (alguns clientes enviam na URL)
	// Ex: /auth/v1/token?grant_type=refresh_token
	params.GrantType = r.URL.Query().Get("grant_type")
	params.RefreshToken = r.URL.Query().Get("refresh_token")
	
	// PRIORITY 2: Form body (form-urlencoded ou JSON sobrescreve query)
	contentType := r.Header.Get("Content-Type")
	if strings.Contains(contentType, "application/x-www-form-urlencoded") {
		// Parse form-urlencoded
		if err := r.ParseForm(); err == nil {
			if gt := r.FormValue("grant_type"); gt != "" {
				params.GrantType = gt
			}
			if rt := r.FormValue("refresh_token"); rt != "" {
				params.RefreshToken = rt
			}
			if email := r.FormValue("email"); email != "" {
				params.Email = email
			}
			if pass := r.FormValue("password"); pass != "" {
				params.Password = pass
			}
			if provider := r.FormValue("provider"); provider != "" {
				params.Provider = provider
			}
			if id := r.FormValue("identifier"); id != "" {
				params.Identifier = id
			}
			if totp := r.FormValue("totp_code"); totp != "" {
				params.TotpCode = totp
			}
			if idToken := r.FormValue("id_token"); idToken != "" {
				params.IdToken = idToken
			}
		}
	} else {
		// Parse JSON (padrão) - apenas sobrescreve se não estiver vazio no body
		var bodyParams services.GoTrueTokenParams
		if err := json.NewDecoder(r.Body).Decode(&bodyParams); err == nil {
			if bodyParams.GrantType != "" {
				params.GrantType = bodyParams.GrantType
			}
			if bodyParams.RefreshToken != "" {
				params.RefreshToken = bodyParams.RefreshToken
			}
			if bodyParams.Email != "" {
				params.Email = bodyParams.Email
			}
			if bodyParams.Password != "" {
				params.Password = bodyParams.Password
			}
			// ... outros campos
			params.Provider = bodyParams.Provider
			params.Identifier = bodyParams.Identifier
			params.TotpCode = bodyParams.TotpCode
			params.IdToken = bodyParams.IdToken
		}
	}

	deviceInfo := types.DeviceInfo{
		IP:        ctx.IP,
		UserAgent: r.Header.Get("User-Agent"),
	}

	res, err := c.goTrue.HandleToken(r.Context(), ctx.ProjectPool, params, ctx.Project.JWTSecret, deviceInfo)
	if err != nil {
		if strings.HasPrefix(err.Error(), "{") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(403)
			w.Write([]byte(err.Error()))
		} else {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), 400)
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)
}

func (c *AuthController) GetUser(w http.ResponseWriter, r *http.Request) {
	val := r.Context().Value(types.CascataCtxKey)
	if val == nil {
		http.Error(w, `{"error":"Context Lost"}`, 500)
		return
	}
	ctx := val.(*types.CascataRequest)
	if ctx.Project == nil {
		http.Error(w, `{"error":"Project Context Required"}`, 401)
		return
	}

	// 1. Identify User ID from JWT context (injected by Auth Middleware)
	var userID string
	if ctx.User != nil {
		if sub, ok := ctx.User["sub"].(string); ok {
			userID = sub
		}
	}

	if userID == "" {
		http.Error(w, `{"error":"Unauthorized (Token Required)"}`, 401)
		return
	}

	res, err := c.goTrue.HandleGetUser(r.Context(), ctx.ProjectPool, userID)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), 400)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)
}

func (c *AuthController) ListUsers(w http.ResponseWriter, r *http.Request) {
	val := r.Context().Value(types.CascataCtxKey)
	if val == nil {
		http.Error(w, `{"error":"Context Lost"}`, 500)
		return
	}
	ctx := val.(*types.CascataRequest)
	if ctx.ProjectPool == nil {
		http.Error(w, `{"error":"Project Pool Required"}`, 404)
		return
	}

	// Check if auth.users table exists
	var tableExists bool
	err := ctx.ProjectPool.QueryRow(r.Context(), 
		"SELECT EXISTS (SELECT FROM information_schema.tables WHERE table_schema = 'auth' AND table_name = 'users')").Scan(&tableExists)
	if err != nil || !tableExists {
		// Return empty array if table doesn't exist (frontend compatibility)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("[]"))
		return
	}

	// Rich User Query: Joins auth.users with auth.identities to expose provider information
	// Matches legacy TypeScript DataAuthController.listUsers synergy.
	query := `
		SELECT u.id, u.created_at, u.banned, u.last_sign_in_at, u.user_concatenation,
		       jsonb_agg(jsonb_build_object('id', i.id, 'provider', i.provider, 'identifier', i.identifier, 'verified_at', i.verified_at)) as identities 
		FROM auth.users u 
		LEFT JOIN auth.identities i ON u.id = i.user_id 
		GROUP BY u.id 
		ORDER BY u.created_at DESC`

	rows, err := ctx.ProjectPool.Query(r.Context(), query)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), 500)
		return
	}
	defer rows.Close()

	var users []map[string]interface{}
	for rows.Next() {
		var id string
		var banned bool
		var createdAt, lastSignIn *time.Time
		var identities json.RawMessage
		
		var userConcatenation []string
		err := rows.Scan(&id, &createdAt, &banned, &lastSignIn, &userConcatenation, &identities)
		if err != nil { continue }

		var identitiesParsed interface{}
		json.Unmarshal(identities, &identitiesParsed)

		users = append(users, map[string]interface{}{
			"id": id, 
			"created_at": createdAt, 
			"banned": banned,
			"last_sign_in_at": lastSignIn,
			"user_concatenation": userConcatenation,
			"identities":         identitiesParsed,
		})
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(users)
}

// CreateUser handles POST /auth/users - creates user with strategies (TypeScript parity)
func (c *AuthController) CreateUser(w http.ResponseWriter, r *http.Request) {
	val := r.Context().Value(types.CascataCtxKey)
	if val == nil {
		http.Error(w, `{"error":"Context Lost"}`, 500)
		return
	}
	ctx := val.(*types.CascataRequest)
	if ctx.ProjectPool == nil {
		http.Error(w, `{"error":"Project Pool Required"}`, 404)
		return
	}

	var body struct {
		Strategies  []struct {
			Provider   string `json:"provider"`
			Identifier string `json:"identifier"`
			Password   string `json:"password"`
		} `json:"strategies"`
		ProfileData map[string]interface{} `json:"profileData"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"Invalid JSON"}`, 400)
		return
	}

	// Start transaction
	tx, err := ctx.ProjectPool.Begin(r.Context())
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), 500)
		return
	}
	defer tx.Rollback(r.Context())

	// Create user with profile data
	var userID string
	err = tx.QueryRow(r.Context(), 
		"INSERT INTO auth.users (raw_user_meta_data) VALUES ($1) RETURNING id",
		body.ProfileData).Scan(&userID)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), 500)
		return
	}

	// Create identities for each strategy
	for _, s := range body.Strategies {
		var passwordHash *string
		if s.Password != "" {
			hash, err := bcrypt.GenerateFromPassword([]byte(s.Password), 10)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), 500)
				return
			}
			hashed := string(hash)
			passwordHash = &hashed
		}
		
		_, err = tx.Exec(r.Context(),
			"INSERT INTO auth.identities (user_id, provider, identifier, password_hash) VALUES ($1, $2, $3, $4)",
			userID, s.Provider, s.Identifier, passwordHash)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), 500)
			return
		}
	}

	if err := tx.Commit(r.Context()); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), 500)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"id":      userID,
	})
}

func (c *AuthController) ListPolicies(w http.ResponseWriter, r *http.Request) {
	val := r.Context().Value(types.CascataCtxKey)
	if val == nil {
		http.Error(w, `{"error":"Context Lost"}`, 500)
		return
	}
	ctx := val.(*types.CascataRequest)
	if ctx.ProjectPool == nil {
		http.Error(w, `{"error":"Project Pool Required"}`, 404)
		return
	}

	// Support for both RLS policies (pg_policies) and Orchestration policies (auth.policies)
	// If the path contains 'orchestration', we return business-level policies
	if strings.Contains(r.URL.Path, "orchestration") {
		// Check if auth.policies table exists
		var tableExists bool
		err := ctx.ProjectPool.QueryRow(r.Context(), 
			"SELECT EXISTS (SELECT FROM information_schema.tables WHERE table_schema = 'auth' AND table_name = 'policies')").Scan(&tableExists)
		if err != nil || !tableExists {
			// Return empty array if table doesn't exist (frontend compatibility)
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte("[]"))
			return
		}

		rows, err := ctx.ProjectPool.Query(r.Context(), "SELECT * FROM auth.policies ORDER BY priority DESC, created_at ASC")
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), 500)
			return
		}
		defer rows.Close()

		var policies []map[string]interface{}
		fDesc := rows.FieldDescriptions()
		for rows.Next() {
			vals, _ := rows.Values()
			row := make(map[string]interface{})
			for i, fd := range fDesc { row[fd.Name] = vals[i] }
			policies = append(policies, row)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(policies)
		return
	}

	// Fallback to Native RLS Policies
	rows, err := ctx.ProjectPool.Query(r.Context(), 
		"SELECT schemaname, tablename, policyname, roles, cmd, qual, with_check FROM pg_policies WHERE schemaname NOT IN ('pg_catalog', 'information_schema')")
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()

	var policies []map[string]interface{}
	for rows.Next() {
		var schema, table, name, command string
		var roles []string
		var qual, withCheck *string
		rows.Scan(&schema, &table, &name, &roles, &command, &qual, &withCheck)
		policies = append(policies, map[string]interface{}{
			"schema": schema, "table": table, "name": name, "roles": roles, "command": command, 
			"qual": qual, "with_check": withCheck,
		})
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(policies)
}

// deepMerge faz merge recursivo de dois maps (existing <- incoming)
// Preserva campos do existing que não existem no incoming
// Substitui campos que existem em ambos
func deepMerge(existing, incoming map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{})
	
	// Copiar existing para result
	for k, v := range existing {
		result[k] = v
	}
	
	// Fazer merge com incoming
	for k, incomingVal := range incoming {
		if existingVal, exists := result[k]; exists {
			// Ambos existem - fazer merge recursivo se são maps
			existingMap, existingIsMap := existingVal.(map[string]interface{})
			incomingMap, incomingIsMap := incomingVal.(map[string]interface{})
			
			if existingIsMap && incomingIsMap {
				// Merge recursivo de sub-maps (ex: providers.google)
				result[k] = deepMerge(existingMap, incomingMap)
			} else {
				// Substituição direta
				result[k] = incomingVal
			}
		} else {
			// Só existe no incoming - adicionar
			result[k] = incomingVal
		}
	}
	
	return result
}

// LinkAuthConfig handles POST /auth/link - saves OAuth provider configuration
func (c *AuthController) LinkAuthConfig(w http.ResponseWriter, r *http.Request) {
	val := r.Context().Value(types.CascataCtxKey)
	if val == nil {
		http.Error(w, `{"error":"Context Lost"}`, http.StatusInternalServerError)
		return
	}
	ctx := val.(*types.CascataRequest)
	if ctx.Project == nil || ctx.ProjectPool == nil {
		http.Error(w, `{"error":"Project Required"}`, http.StatusNotFound)
		return
	}

	// Only service role can configure auth
	if ctx.UserRole != types.RoleService {
		http.Error(w, `{"error":"Access Denied: Only Service Role can configure auth"}`, http.StatusForbidden)
		return
	}

	// Parse request body
	var body map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"Invalid JSON: %s"}`, err.Error()), http.StatusBadRequest)
		return
	}

	// Extract components from request body (Sinergia Parity)
	authConfigRaw, _ := body["authConfig"].(map[string]interface{})
	authStrategiesRaw, _ := body["authStrategies"].(map[string]interface{})
	linkedTablesRaw, _ := body["linked_tables"].([]interface{})

	var linkedTables []string
	for _, t := range linkedTablesRaw {
		if s, ok := t.(string); ok {
			linkedTables = append(linkedTables, s)
		}
	}

	var err error

	if ctx.TargetEnv != "" && ctx.TargetEnv != "live" {
		// --- BRAND-NEW BRANCH ISOLATED AUTH STRATEGIES SAVE ---
		var branchAuthRaw *string
		err = services.SystemPool.QueryRow(r.Context(),
			"SELECT auth_config_json FROM system.branches WHERE project_slug = $1 AND name = $2 AND status = 'active'",
			ctx.Project.Slug, ctx.TargetEnv).Scan(&branchAuthRaw)

		branchAuth := make(map[string]interface{})
		if err == nil && branchAuthRaw != nil && *branchAuthRaw != "" {
			json.Unmarshal([]byte(*branchAuthRaw), &branchAuth)
		}

		var authConfig map[string]interface{}
		if ac, ok := branchAuth["auth_config"].(map[string]interface{}); ok {
			authConfig = ac
		} else {
			authConfig = make(map[string]interface{})
		}

		if authConfigRaw != nil {
			authConfig = deepMerge(authConfig, authConfigRaw)
		}
		branchAuth["auth_config"] = authConfig

		if authStrategiesRaw != nil {
			branchAuth["auth_strategies"] = authStrategiesRaw
		}
		if linkedTablesRaw != nil {
			branchAuth["linked_tables"] = linkedTables
		}

		// Marshall and update branch row
		var authBytes []byte
		authBytes, err = json.Marshal(branchAuth)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"Failed to marshal branch auth: %s"}`, err.Error()), http.StatusInternalServerError)
			return
		}
		authStr := string(authBytes)

		_, err = services.SystemPool.Exec(r.Context(),
			"UPDATE system.branches SET auth_config_json = $1 WHERE project_slug = $2 AND name = $3 AND status = 'active'",
			authStr, ctx.Project.Slug, ctx.TargetEnv)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"Failed to save branch metadata: %s"}`, err.Error()), http.StatusInternalServerError)
			return
		}
		log.Printf("[LinkAuthConfig] Successfully updated branch-specific auth config for branch %s", ctx.TargetEnv)
	} else {
		// 1. Buscar metadata atual do banco (defesa contra race conditions)
		var currentMetadataRaw json.RawMessage
		err = services.SystemPool.QueryRow(r.Context(),
			"SELECT metadata FROM system.projects WHERE id = $1",
			ctx.Project.ID).Scan(&currentMetadataRaw)

		currentMetadata := make(map[string]interface{})
		if err == nil && len(currentMetadataRaw) > 0 {
			json.Unmarshal(currentMetadataRaw, &currentMetadata)
		}

		// Extrair o bloco 'extra' ou inicializar
		extra, ok := currentMetadata["extra"].(map[string]interface{})
		if !ok {
			extra = make(map[string]interface{})
			currentMetadata["extra"] = extra
		}

		// 2. MERGE INTELIGENTE (preserva providers, site_url, etc.)
		if authConfigRaw != nil {
			currentAuthConfig, _ := extra["auth_config"].(map[string]interface{})
			extra["auth_config"] = deepMerge(currentAuthConfig, authConfigRaw)
		}

		if authStrategiesRaw != nil {
			extra["auth_strategies"] = authStrategiesRaw
		}

		if linkedTablesRaw != nil {
			extra["linked_tables"] = linkedTables
		}

		// 3. Persistir metadata no banco do sistema
		_, err = services.SystemPool.Exec(r.Context(),
			"UPDATE system.projects SET metadata = $1 WHERE id = $2",
			currentMetadata, ctx.Project.ID)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"Failed to save metadata: %s"}`, err.Error()), http.StatusInternalServerError)
			return
		}
	}

	// 4. Executar DDL no banco do projeto (linked_tables logic)
	if len(linkedTables) > 0 {
		// Adicionar colunas e índices
		for _, table := range linkedTables {
			quotedTable := fmt.Sprintf("public.%s", utils.QuoteId(table))
			quotedIndex := utils.QuoteId("idx_" + table + "_user_id")

			// Adicionar coluna user_id
			_, err = ctx.ProjectPool.Exec(r.Context(),
				fmt.Sprintf("ALTER TABLE %s ADD COLUMN IF NOT EXISTS user_id UUID REFERENCES auth.users(id) ON DELETE SET NULL", quotedTable))
			if err != nil {
				log.Printf("[LinkAuthConfig] Warning: failed to add user_id to %s: %v", table, err)
			}

			// Criar índice
			_, err = ctx.ProjectPool.Exec(r.Context(),
				fmt.Sprintf("CREATE INDEX IF NOT EXISTS %s ON %s (user_id)", quotedIndex, quotedTable))
			if err != nil {
				log.Printf("[LinkAuthConfig] Warning: failed to create index on %s: %v", table, err)
			}
		}

		// SYNC ENUM: user_concatenation (Dual Schema: public & auth)
		// 1. Buscar valores atuais (do schema public como referência)
		var existingEnumValues []string
		rows, err := ctx.ProjectPool.Query(r.Context(), `
			SELECT enumlabel FROM pg_enum e
			JOIN pg_type t ON e.enumtypid = t.oid
			JOIN pg_namespace n ON n.oid = t.typnamespace
			WHERE t.typname = 'user_concatenation' AND n.nspname = 'public'
			ORDER BY enumsortorder`)
		if err == nil {
			for rows.Next() {
				var val string
				if err := rows.Scan(&val); err == nil {
					existingEnumValues = append(existingEnumValues, val)
				}
			}
			rows.Close()

			// 2. Determinar se precisamos apenas Adicionar ou se precisamos Recriar (Remoção)
			existingMap := make(map[string]bool)
			for _, v := range existingEnumValues { existingMap[v] = true }

			needsRecreate := false
			for _, v := range existingEnumValues {
				if v == "vazio" { continue }
				found := false
				for _, lt := range linkedTables {
					if lt == v { found = true; break }
				}
				if !found { needsRecreate = true; break }
			}

			if needsRecreate {
				// REMOÇÃO/RECRIAÇÃO (Heavy Operation)
				newEnumLabels := []string{"vazio"}
				newEnumLabels = append(newEnumLabels, linkedTables...)

				labelsLiteral := ""
				for i, l := range newEnumLabels {
					labelsLiteral += utils.QuoteLiteral(l)
					if i < len(newEnumLabels)-1 { labelsLiteral += ", " }
				}

				for _, schema := range []string{"public", "auth"} {
					steps := []string{
						fmt.Sprintf("ALTER TYPE %s.user_concatenation RENAME TO user_concatenation_old", schema),
						fmt.Sprintf("CREATE TYPE %s.user_concatenation AS ENUM (%s)", schema, labelsLiteral),
					}
					
					// Se for public, precisamos atualizar auth.users antes de dropar o antigo
					if schema == "public" {
						steps = append(steps, "ALTER TABLE auth.users ALTER COLUMN user_concatenation TYPE public.user_concatenation[] USING user_concatenation::text[]::public.user_concatenation[]")
					}
					steps = append(steps, fmt.Sprintf("DROP TYPE %s.user_concatenation_old", schema))

					for _, sql := range steps {
						ctx.ProjectPool.Exec(r.Context(), sql)
					}
				}
			} else {
				// APENAS ADIÇÃO (Light Operation)
				for _, table := range linkedTables {
					if !existingMap[table] {
						for _, schema := range []string{"public", "auth"} {
							ctx.ProjectPool.Exec(r.Context(),
								fmt.Sprintf("ALTER TYPE %s.user_concatenation ADD VALUE %s", schema, utils.QuoteLiteral(table)))
						}
					}
				}
			}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Auth configuration saved",
	})
}

// HandleAuth is the compatibility bridge for Cascata SDK / GoTrue-style requests (/auth/v1/*)
func (c *AuthController) HandleAuth(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	
	// Routing logic translated from legacy Express schema
	if strings.HasSuffix(path, "/signup") {
		c.Signup(w, r)
		return
	}
	if strings.HasSuffix(path, "/token") {
		c.Token(w, r)
		return
	}
	if strings.HasSuffix(path, "/user") {
		if r.Method == "PUT" {
			c.UpdateUser(w, r)
		} else {
			c.GetUser(w, r)
		}
		return
	}
	if strings.HasSuffix(path, "/users") {
		if r.Method == "POST" {
			c.CreateUser(w, r)
		} else {
			c.ListUsers(w, r)
		}
		return
	}
	if strings.HasSuffix(path, "/link") {
		c.LinkAuthConfig(w, r)
		return
	}
	if strings.HasSuffix(path, "/strategies") {
		c.GetStrategies(w, r)
		return
	}
	if strings.HasSuffix(path, "/orchestration/policies") {
		c.ListPolicies(w, r)
		return
	}
	// Handle /auth/users/:id/identities - Link identity
	if strings.Contains(path, "/users/") && strings.HasSuffix(path, "/identities") {
		if r.Method == "POST" {
			c.LinkUserIdentity(w, r)
		} else {
			http.Error(w, `{"error":"Method not allowed"}`, 405)
		}
		return
	}
	// Handle /auth/users/:id/strategies/:identityId - Unlink identity
	if strings.Contains(path, "/users/") && strings.Contains(path, "/strategies/") {
		if r.Method == "DELETE" {
			c.UnlinkUserIdentity(w, r)
		} else {
			http.Error(w, `{"error":"Method not allowed"}`, 405)
		}
		return
	}
	// Handle /auth/users/:id/sessions - List sessions for a user
	if strings.Contains(path, "/users/") && strings.HasSuffix(path, "/sessions") {
		c.GetUserSessions(w, r)
		return
	}

	if strings.HasSuffix(path, "/authorize") {
		c.Authorize(w, r)
		return
	}
	if strings.HasSuffix(path, "/callback") {
		c.Callback(w, r)
		return
	}
	if strings.HasSuffix(path, "/mfa/enroll") {
		c.EnrollMFA(w, r)
		return
	}
	if strings.HasSuffix(path, "/mfa/verify") {
		c.VerifyMFA(w, r)
		return
	}
	if strings.HasSuffix(path, "/challenge") {
		c.Challenge(w, r)
		return
	}
	if strings.HasSuffix(path, "/verify-challenge") {
		c.VerifyChallenge(w, r)
		return
	}
	
	// WEBAUTHN / PASSKEYS ROUTES
	if strings.HasSuffix(path, "/webauthn/enroll") {
		webAuthnCtrl := NewWebAuthnController()
		webAuthnCtrl.EnrollStart(w, r)
		return
	}
	if strings.HasSuffix(path, "/webauthn/enroll/finish") {
		webAuthnCtrl := NewWebAuthnController()
		webAuthnCtrl.EnrollFinish(w, r)
		return
	}
	if strings.HasSuffix(path, "/webauthn/verify") {
		webAuthnCtrl := NewWebAuthnController()
		webAuthnCtrl.VerifyStart(w, r)
		return
	}
	if strings.HasSuffix(path, "/webauthn/verify/finish") {
		webAuthnCtrl := NewWebAuthnController()
		webAuthnCtrl.VerifyFinish(w, r)
		return
	}

	http.Error(w, `{"error":"Endpoint not implemented in Sovereign Auth Engine"}`, 501)
}

// Authorize handles OAuth authorization redirect
func (c *AuthController) Authorize(w http.ResponseWriter, r *http.Request) {
	val := r.Context().Value(types.CascataCtxKey)
	if val == nil {
		http.Error(w, `{"error":"Context Lost"}`, http.StatusInternalServerError)
		return
	}
	ctx := val.(*types.CascataRequest)
	if ctx.Project == nil {
		http.Error(w, `{"error":"Project Required"}`, http.StatusNotFound)
		return
	}

	// Get provider from query
	providerName := r.URL.Query().Get("provider")
	if providerName == "" {
		http.Error(w, `{"error":"Provider required"}`, http.StatusBadRequest)
		return
	}

	// Get provider config
	config := services.GetProviderConfig(&ctx.Project.Metadata, providerName)
	if config == nil || config.ClientID == "" {
		http.Error(w, `{"error":"Provider not configured"}`, http.StatusBadRequest)
		return
	}

	// Determine callback URL
	host := r.Host
	var callbackURL string
	if ctx.Project.CustomDomain != "" && host == ctx.Project.CustomDomain {
		callbackURL = fmt.Sprintf("https://%s/auth/v1/callback", host)
	} else {
		callbackURL = fmt.Sprintf("https://%s/api/data/%s/auth/v1/callback", host, ctx.Project.Slug)
	}
	config.RedirectURI = callbackURL

	// Get redirect target and language
	redirectTo := r.URL.Query().Get("redirect_to")
	language := r.URL.Query().Get("language")
	if language == "" {
		language = "en-US"
	}

	// Get app client ID for Identity-Aware Key Bridging
	// Priority 1: From context (if resolved by anon_key header)
	// Priority 2: From query parameter (for OAuth flows where header cannot be sent)
	clientID := ""
	if ctx.AppClient != nil {
		clientID = ctx.AppClient.ID
		log.Printf("[Authorize] App Client resolved from context: ID=%s, SiteURL=%s", ctx.AppClient.ID, ctx.AppClient.SiteURL)
	} else {
		// Check for app_client_id in query params (e.g., ?app_client_id=terra-79b7f0)
		appClientIDFromQuery := r.URL.Query().Get("app_client_id")
		if appClientIDFromQuery != "" {
			// Validate that this app client exists
			for i := range ctx.Project.Metadata.AppClients {
				if ctx.Project.Metadata.AppClients[i].ID == appClientIDFromQuery && ctx.Project.Metadata.AppClients[i].Active {
					clientID = appClientIDFromQuery
					ctx.AppClient = &ctx.Project.Metadata.AppClients[i]
					log.Printf("[Authorize] App Client resolved from query param: ID=%s, SiteURL=%s", clientID, ctx.AppClient.SiteURL)
					break
				}
			}
			if clientID == "" {
				log.Printf("[Authorize] Warning: app_client_id '%s' not found or inactive", appClientIDFromQuery)
			}
		}
	}

	// Fallback: If no clientID yet, try to auto-select an App Client
	if clientID == "" {
		// Priority 1: If exactly one active app client, use it
		if len(ctx.Project.Metadata.AppClients) == 1 && ctx.Project.Metadata.AppClients[0].Active {
			clientID = ctx.Project.Metadata.AppClients[0].ID
			ctx.AppClient = &ctx.Project.Metadata.AppClients[0]
			log.Printf("[Authorize] Auto-selected single App Client: ID=%s, SiteURL=%s", clientID, ctx.AppClient.SiteURL)
		} else if len(ctx.Project.Metadata.AppClients) > 0 {
			// Priority 2: Use first active app client with SiteURL configured
			for i := range ctx.Project.Metadata.AppClients {
				if ctx.Project.Metadata.AppClients[i].Active && ctx.Project.Metadata.AppClients[i].SiteURL != "" {
					clientID = ctx.Project.Metadata.AppClients[i].ID
					ctx.AppClient = &ctx.Project.Metadata.AppClients[i]
					log.Printf("[Authorize] Auto-selected first App Client with SiteURL: ID=%s, SiteURL=%s", clientID, ctx.AppClient.SiteURL)
					break
				}
			}
		}
	}

	// Validate redirect_to against Allowed Origins (CORS security)
	if redirectTo != "" {
		allowedOrigins := ctx.Project.Metadata.AllowedOrigins
		// If App Client exists, use its specific AllowedOrigins
		if ctx.AppClient != nil && len(ctx.AppClient.AllowedOrigins) > 0 {
			allowedOrigins = ctx.AppClient.AllowedOrigins
			log.Printf("[Authorize] Using App Client AllowedOrigins: %v", allowedOrigins)
		}

		// Only validate if there are configured origins (empty = dev mode, allow all)
		if len(allowedOrigins) > 0 {
			isAllowed := false
			for _, origin := range allowedOrigins {
				if origin == "*" || strings.HasPrefix(redirectTo, origin) {
					isAllowed = true
					break
				}
			}
			if !isAllowed {
				http.Error(w, `{"error":"Redirect URL not in allowed origins"}`, http.StatusBadRequest)
				return
			}
		}
	}

	// Generate state
	state := services.GenerateOAuthState(redirectTo, providerName, clientID, language)
	log.Printf("[Authorize] Generated state with client_id='%s', redirect_to='%s', provider='%s'", clientID, redirectTo, providerName)

	// Generate auth URL
	authURL, err := services.GetAuthURL(providerName, config, state)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusInternalServerError)
		return
	}

	// Redirect to provider
	http.Redirect(w, r, authURL, http.StatusFound)
}

// Callback handles OAuth callback and creates session
func (c *AuthController) Callback(w http.ResponseWriter, r *http.Request) {
	val := r.Context().Value(types.CascataCtxKey)
	if val == nil {
		http.Error(w, `{"error":"Context Lost"}`, http.StatusInternalServerError)
		return
	}
	ctx := val.(*types.CascataRequest)
	if ctx.Project == nil || ctx.ProjectPool == nil {
		http.Error(w, `{"error":"Project Required"}`, http.StatusNotFound)
		return
	}

	// Get code and state
	code := r.URL.Query().Get("code")
	stateStr := r.URL.Query().Get("state")

	if code == "" {
		http.Error(w, `{"error":"Authorization code required"}`, http.StatusBadRequest)
		return
	}

	// Parse state
	state, err := services.ParseOAuthState(stateStr)
	if err != nil {
		state = &services.OAuthState{Provider: "google"}
	}

	log.Printf("[Callback] Received state: redirect_to='%s', provider='%s', client_id='%s'", 
		state.RedirectTo, state.Provider, state.ClientID)
	log.Printf("[Callback] Project has %d app clients", len(ctx.Project.Metadata.AppClients))

	// Get provider config
	providerName := state.Provider
	if providerName == "" {
		providerName = "google"
	}

	config := services.GetProviderConfig(&ctx.Project.Metadata, providerName)
	if config == nil || config.ClientID == "" {
		http.Error(w, `{"error":"Provider not configured"}`, http.StatusBadRequest)
		return
	}

	// Determine callback URL (must match authorize)
	host := r.Host
	if ctx.Project.CustomDomain != "" && host == ctx.Project.CustomDomain {
		config.RedirectURI = fmt.Sprintf("https://%s/auth/v1/callback", host)
	} else {
		config.RedirectURI = fmt.Sprintf("https://%s/api/data/%s/auth/v1/callback", host, ctx.Project.Slug)
	}

	// Exchange code for user profile
	profile, err := services.ExchangeCode(providerName, code, config)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"OAuth failed: %s"}`, err.Error()), http.StatusBadRequest)
		return
	}

	// Get auth config for auto_verify
	var authConfig map[string]interface{}
	if ac, ok := ctx.Project.Metadata.Extra["auth_config"].(map[string]interface{}); ok {
		authConfig = ac
	}

	// Upsert user
	userID, err := services.UpsertUser(ctx.ProjectPool, profile, authConfig)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"User creation failed: %s"}`, err.Error()), http.StatusInternalServerError)
		return
	}

	// Get device info
	deviceInfo := getDeviceInfo(r)

	// Create session
	session, err := services.CreateSession(
		userID,
		ctx.ProjectPool,
		ctx.Project.JWTSecret,
		"1h",
		30,
		providerName,
		deviceInfo,
	)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"Session creation failed: %s"}`, err.Error()), http.StatusInternalServerError)
		return
	}

	// Set auth cookies
	setAuthCookies(w, session)

	// Build hash fragment
	hash := fmt.Sprintf("access_token=%s&refresh_token=%s&expires_in=%d&token_type=bearer&type=recovery",
		session.AccessToken, session.RefreshToken, session.ExpiresIn)

	// Determine redirect target with Identity-Aware Key Bridging
	fallbackURL := ""
	if authConfig != nil {
		if siteURL, ok := authConfig["site_url"].(string); ok {
			fallbackURL = siteURL
		}
	}

	// Variable to hold the resolved app client for CORS validation
	resolvedAppClient := ctx.AppClient

	// Check for app client in state - PRIORITY 1: Use already resolved AppClient from context
	if ctx.AppClient != nil && ctx.AppClient.SiteURL != "" {
		// App Client was already resolved by middleware (via anon_key)
		log.Printf("[Callback] App Client resolved from context: ID=%s, SiteURL=%s", ctx.AppClient.ID, ctx.AppClient.SiteURL)
		fallbackURL = ctx.AppClient.SiteURL
	} else if state.ClientID != "" {
		// PRIORITY 2: Look up by ClientID from OAuth state
		// Check in the typed AppClients array
		for i := range ctx.Project.Metadata.AppClients {
			if ctx.Project.Metadata.AppClients[i].ID == state.ClientID {
				if ctx.Project.Metadata.AppClients[i].SiteURL != "" {
					fallbackURL = ctx.Project.Metadata.AppClients[i].SiteURL
					resolvedAppClient = &ctx.Project.Metadata.AppClients[i] // Store for CORS validation
					log.Printf("[Callback] App Client resolved from state: ID=%s, SiteURL=%s", state.ClientID, fallbackURL)
					break
				}
			}
		}
	}

	// PRIORITY 3: Fallback - auto-select first active App Client with SiteURL
	if fallbackURL == "" && len(ctx.Project.Metadata.AppClients) > 0 {
		for i := range ctx.Project.Metadata.AppClients {
			if ctx.Project.Metadata.AppClients[i].Active && ctx.Project.Metadata.AppClients[i].SiteURL != "" {
				fallbackURL = ctx.Project.Metadata.AppClients[i].SiteURL
				resolvedAppClient = &ctx.Project.Metadata.AppClients[i]
				log.Printf("[Callback] Auto-selected App Client (fallback): ID=%s, SiteURL=%s", ctx.Project.Metadata.AppClients[i].ID, fallbackURL)
				break
			}
		}
	}

	// Determine final redirect target
	target := state.RedirectTo
	if target == "" {
		target = fallbackURL
	}
	
	log.Printf("[Callback] Final redirect target: %s (redirect_to=%s, fallback=%s)", target, state.RedirectTo, fallbackURL)

	// Validate redirect_to against Allowed Origins (CORS security) - double-check on callback
	if state.RedirectTo != "" {
		allowedOrigins := ctx.Project.Metadata.AllowedOrigins
		// If App Client exists (from context or resolved from state), use its specific AllowedOrigins
		if resolvedAppClient != nil && len(resolvedAppClient.AllowedOrigins) > 0 {
			allowedOrigins = resolvedAppClient.AllowedOrigins
			log.Printf("[Callback] Using App Client AllowedOrigins: %v", allowedOrigins)
		}

		// Only validate if there are configured origins (empty = dev mode, allow all)
		if len(allowedOrigins) > 0 {
			isAllowed := false
			for _, origin := range allowedOrigins {
				if origin == "*" || strings.HasPrefix(state.RedirectTo, origin) {
					isAllowed = true
					break
				}
			}
			if !isAllowed {
				http.Error(w, `{"error":"Redirect URL not in allowed origins"}`, http.StatusBadRequest)
				return
			}
		}
	}

	if target != "" {
		// Remove trailing slash
		if strings.HasSuffix(target, "/") {
			target = target[:len(target)-1]
		}
		// Ensure target is an absolute URL
		if !strings.HasPrefix(target, "http://") && !strings.HasPrefix(target, "https://") {
			target = "https://" + target
		}
		http.Redirect(w, r, target+"#"+hash, http.StatusFound)
	} else {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(session)
	}
}

// Helper functions
func getDeviceInfo(r *http.Request) map[string]string {
	return map[string]string{
		"ip":         r.RemoteAddr,
		"userAgent":  r.UserAgent(),
		"fingerprint": r.Header.Get("X-Device-Fingerprint"),
	}
}

func setAuthCookies(w http.ResponseWriter, session *services.Session) {
	// Set refresh token cookie (httpOnly, secure)
	http.SetCookie(w, &http.Cookie{
		Name:     "refresh_token",
		Value:    session.RefreshToken,
		Path:     "/",
		MaxAge:   30 * 24 * 60 * 60, // 30 days
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
}

// GetUserSessions handles GET /auth/users/:id/sessions - Lists active refresh tokens for a user
func (c *AuthController) GetUserSessions(w http.ResponseWriter, r *http.Request) {
	val := r.Context().Value(types.CascataCtxKey)
	if val == nil {
		http.Error(w, `{"error":"Context Lost"}`, 500)
		return
	}
	ctx := val.(*types.CascataRequest)
	if ctx.ProjectPool == nil {
		http.Error(w, `{"error":"Project Pool Required"}`, 404)
		return
	}

	// Require service_role for session queries
	if ctx.UserRole != types.RoleService {
		http.Error(w, `{"error":"Access Denied: Only Service Role can query sessions directly."}`, 403)
		return
	}

	userID := chi.URLParam(r, "id")
	if userID == "" {
		http.Error(w, `{"error":"User ID required"}`, 400)
		return
	}

	// Check if auth.refresh_tokens table exists
	var tableExists bool
	err := ctx.ProjectPool.QueryRow(r.Context(),
		"SELECT EXISTS (SELECT FROM information_schema.tables WHERE table_schema = 'auth' AND table_name = 'refresh_tokens')").Scan(&tableExists)
	if err != nil || !tableExists {
		// Return empty array if table doesn't exist (frontend compatibility)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("[]"))
		return
	}

	query := `
		SELECT id, user_agent, ip_address, created_at, expires_at 
		FROM auth.refresh_tokens 
		WHERE user_id = $1 AND revoked = false
		ORDER BY created_at DESC
	`
	rows, err := ctx.ProjectPool.Query(r.Context(), query, userID)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), 500)
		return
	}
	defer rows.Close()

	type Session struct {
		ID        string     `json:"id"`
		UserAgent *string    `json:"user_agent"`
		IPAddress *string    `json:"ip_address"`
		CreatedAt time.Time  `json:"created_at"`
		ExpiresAt *time.Time `json:"expires_at"`
	}

	var sessions []Session
	for rows.Next() {
		var s Session
		err := rows.Scan(&s.ID, &s.UserAgent, &s.IPAddress, &s.CreatedAt, &s.ExpiresAt)
		if err != nil {
			continue
		}
		sessions = append(sessions, s)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(sessions)
}

// RevokeOtherSessions handles DELETE /auth/users/:id/sessions - Revokes all sessions except current
func (c *AuthController) RevokeOtherSessions(w http.ResponseWriter, r *http.Request) {
	val := r.Context().Value(types.CascataCtxKey)
	if val == nil {
		http.Error(w, `{"error":"Context Lost"}`, 500)
		return
	}
	ctx := val.(*types.CascataRequest)
	if ctx.ProjectPool == nil {
		http.Error(w, `{"error":"Project Pool Required"}`, 404)
		return
	}

	// Require service_role for session revocation
	if ctx.UserRole != types.RoleService {
		http.Error(w, `{"error":"Access Denied: Only Service Role can revoke sessions."}`, 403)
		return
	}

	userID := chi.URLParam(r, "id")
	if userID == "" {
		http.Error(w, `{"error":"User ID required"}`, 400)
		return
	}

	// Check if auth.refresh_tokens table exists
	var tableExists bool
	err := ctx.ProjectPool.QueryRow(r.Context(),
		"SELECT EXISTS (SELECT FROM information_schema.tables WHERE table_schema = 'auth' AND table_name = 'refresh_tokens')").Scan(&tableExists)
	if err != nil || !tableExists {
		// Return success if table doesn't exist (nothing to revoke)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"message": "No sessions to revoke.",
		})
		return
	}

	// Parse optional body for current_session_id
	var body struct {
		CurrentSessionID string `json:"current_session_id"`
	}
	json.NewDecoder(r.Body).Decode(&body)

	currentSessionID := body.CurrentSessionID
	if currentSessionID == "" {
		currentSessionID = "00000000-0000-0000-0000-000000000000"
	}

	query := `
		UPDATE auth.refresh_tokens 
		SET revoked = true 
		WHERE user_id = $1 AND id != $2 AND revoked = false
	`
	_, err = ctx.ProjectPool.Exec(r.Context(), query, userID, currentSessionID)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), 500)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Other sessions revoked successfully.",
	})
}

// RevokeSession handles DELETE /auth/users/:id/sessions/:sessionId - Revokes a specific session
func (c *AuthController) RevokeSession(w http.ResponseWriter, r *http.Request) {
	val := r.Context().Value(types.CascataCtxKey)
	if val == nil {
		http.Error(w, `{"error":"Context Lost"}`, 500)
		return
	}
	ctx := val.(*types.CascataRequest)
	if ctx.ProjectPool == nil {
		http.Error(w, `{"error":"Project Pool Required"}`, 404)
		return
	}

	// Require service_role for session revocation
	if ctx.UserRole != types.RoleService {
		http.Error(w, `{"error":"Access Denied: Only Service Role can revoke sessions."}`, 403)
		return
	}

	userID := chi.URLParam(r, "id")
	sessionID := chi.URLParam(r, "sessionId")
	if userID == "" || sessionID == "" {
		http.Error(w, `{"error":"User ID and Session ID required"}`, 400)
		return
	}

	// Check if auth.refresh_tokens table exists
	var tableExists bool
	err := ctx.ProjectPool.QueryRow(r.Context(),
		"SELECT EXISTS (SELECT FROM information_schema.tables WHERE table_schema = 'auth' AND table_name = 'refresh_tokens')").Scan(&tableExists)
	if err != nil || !tableExists {
		// Return success if table doesn't exist (nothing to revoke)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"message": "No session to revoke.",
		})
		return
	}

	query := `UPDATE auth.refresh_tokens SET revoked = true WHERE id = $1 AND user_id = $2`
	_, err = ctx.ProjectPool.Exec(r.Context(), query, sessionID, userID)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), 500)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Session revoked.",
	})
}

// DeleteUser handles DELETE /auth/users/:id - Deletes a user from auth.users
func (c *AuthController) DeleteUser(w http.ResponseWriter, r *http.Request) {
	val := r.Context().Value(types.CascataCtxKey)
	if val == nil {
		http.Error(w, `{"error":"Context Lost"}`, 500)
		return
	}
	ctx := val.(*types.CascataRequest)
	if ctx.ProjectPool == nil {
		http.Error(w, `{"error":"Project Pool Required"}`, 404)
		return
	}

	// Require service_role for user deletion
	if ctx.UserRole != types.RoleService {
		http.Error(w, `{"error":"Access Denied: Only Service Role can delete users."}`, 403)
		return
	}

	userID := chi.URLParam(r, "id")
	if userID == "" {
		http.Error(w, `{"error":"User ID required"}`, 400)
		return
	}

	// Delete user (cascades to identities via FK)
	_, err := ctx.ProjectPool.Exec(r.Context(), "DELETE FROM auth.users WHERE id = $1", userID)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), 500)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
	})
}

// GetStrategies handles GET /auth/strategies - Returns active authentication strategies (branch-aware)
func (c *AuthController) GetStrategies(w http.ResponseWriter, r *http.Request) {
	val := r.Context().Value(types.CascataCtxKey)
	if val == nil {
		http.Error(w, `{"error":"Context Lost"}`, 500)
		return
	}
	ctx := val.(*types.CascataRequest)
	if ctx.Project == nil {
		http.Error(w, `{"error":"Project Context Required"}`, 404)
		return
	}

	var strategies interface{}

	// 1. Check if auth_strategies was already overridden in the Metadata.Extra (set by ProjectResolver if in a branch environment)
	if ctx.Project.Metadata.Extra != nil {
		if s, ok := ctx.Project.Metadata.Extra["auth_strategies"]; ok {
			strategies = s
		}
	}

	// 2. Fallback to Project Metadata root or nested extra properties (useful for live/main environments)
	if strategies == nil {
		var metadataMap map[string]interface{}
		metadataBytes, err := json.Marshal(ctx.Project.Metadata)
		if err == nil {
			json.Unmarshal(metadataBytes, &metadataMap)
			if s, ok := metadataMap["auth_strategies"]; ok {
				strategies = s
			} else if extraMap, ok := metadataMap["extra"].(map[string]interface{}); ok {
				if s, ok := extraMap["auth_strategies"]; ok {
					strategies = s
				}
			}
		}
	}

	// 3. Fallback to basic email configuration with sub-methods if nothing is found
	if strategies == nil {
		strategies = map[string]interface{}{
			"email": map[string]interface{}{
				"enabled":               true,
				"jwt_expiration":        "24h",
				"refresh_validity_days": 30,
				"rules":                 []interface{}{},
				"password_enabled":      true,
				"otp_enabled":           true,
				"biometria_enabled":     false,
			},
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(strategies)
}

// UpdateUser handles PUT /auth/user - Updates user profile details, email/password/metadata
func (c *AuthController) UpdateUser(w http.ResponseWriter, r *http.Request) {
	val := r.Context().Value(types.CascataCtxKey)
	if val == nil {
		http.Error(w, `{"error":"Context Lost"}`, 500)
		return
	}
	ctx := val.(*types.CascataRequest)
	if ctx.Project == nil || ctx.ProjectPool == nil {
		http.Error(w, `{"error":"Project Context Required"}`, 401)
		return
	}

	// 1. Identify User ID from JWT context (injected by Auth Middleware)
	var userID string
	var currentProvider string
	if ctx.User != nil {
		if sub, ok := ctx.User["sub"].(string); ok {
			userID = sub
		}
		if prov, ok := ctx.User["provider"].(string); ok {
			currentProvider = prov
		}
	}

	if userID == "" {
		http.Error(w, `{"error":"Unauthorized (Token Required)"}`, 401)
		return
	}

	// Parse parameters
	var params struct {
		Email    string                 `json:"email"`
		Password string                 `json:"password"`
		Data     map[string]interface{} `json:"data"`
	}
	if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"Invalid request: %s"}`, err.Error()), 400)
		return
	}

	tx, err := ctx.ProjectPool.Begin(r.Context())
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), 500)
		return
	}
	defer tx.Rollback(r.Context())

	// 2. Fetch existing user metadata to merge
	var existingMetadataRaw []byte
	err = tx.QueryRow(r.Context(), "SELECT raw_user_meta_data FROM auth.users WHERE id = $1 FOR UPDATE", userID).Scan(&existingMetadataRaw)
	if err != nil {
		http.Error(w, `{"error":"User not found"}`, 404)
		return
	}

	var metadata map[string]interface{}
	if len(existingMetadataRaw) > 0 {
		json.Unmarshal(existingMetadataRaw, &metadata)
	}
	if metadata == nil {
		metadata = make(map[string]interface{})
	}

	// Merge new metadata
	if params.Data != nil {
		for k, v := range params.Data {
			metadata[k] = v
		}
	}

	// 3. Handle password change
	if params.Password != "" {
		hash, err := bcrypt.GenerateFromPassword([]byte(params.Password), 10)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"Password hash failed: %s"}`, err.Error()), 500)
			return
		}
		hashedPassword := string(hash)

		if currentProvider == "" {
			currentProvider = "email"
		}
		// Update password hash of the identity that signed in (or the email identity if active)
		_, err = tx.Exec(r.Context(),
			"UPDATE auth.identities SET password_hash = $1, updated_at = NOW() WHERE user_id = $2 AND provider = $3",
			hashedPassword, userID, currentProvider)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"Failed to update password: %s"}`, err.Error()), 500)
			return
		}
	}

	// 4. Handle email update / linking
	if params.Email != "" {
		emailClean := strings.TrimSpace(strings.ToLower(params.Email))
		metadata["email"] = emailClean

		// Check if this identity is already taken by another user
		var identityUserId string
		err = tx.QueryRow(r.Context(),
			"SELECT user_id FROM auth.identities WHERE provider = 'email' AND identifier = $1",
			emailClean).Scan(&identityUserId)

		if err == nil {
			// Identity exists!
			if identityUserId != userID {
				http.Error(w, `{"error":"email_already_exists"}`, 400)
				return
			}
		} else {
			// Identity does not exist! Link it to this user
			var passwordHash *string
			if params.Password != "" {
				hash, _ := bcrypt.GenerateFromPassword([]byte(params.Password), 10)
				hp := string(hash)
				passwordHash = &hp
			} else {
				// Copy existing password hash if available
				var existingHash *string
				tx.QueryRow(r.Context(), "SELECT password_hash FROM auth.identities WHERE user_id = $1 AND password_hash IS NOT NULL LIMIT 1", userID).Scan(&existingHash)
				passwordHash = existingHash
			}

			// Read confirmation policy
			emailConfirmVal := false
			if ctx.Project.Metadata.Extra != nil {
				if authConfig, ok := ctx.Project.Metadata.Extra["auth_config"].(map[string]interface{}); ok {
					if ec, ok := authConfig["email_confirmation"].(bool); ok {
						emailConfirmVal = ec
					}
				}
			}

			var verifiedAt interface{} = nil
			if !emailConfirmVal {
				verifiedAt = time.Now()
			}

			_, err = tx.Exec(r.Context(),
				`INSERT INTO auth.identities (user_id, provider, identifier, password_hash, verified_at)
				 VALUES ($1, 'email', $2, $3, $4)`,
				userID, emailClean, passwordHash, verifiedAt)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"Failed to link email identity: %s"}`, err.Error()), 500)
				return
			}

			if emailConfirmVal {
				log.Printf("[UpdateUser] Email confirmation required for %s", emailClean)
			}
		}
	}

	// Update user metadata in auth.users
	_, err = tx.Exec(r.Context(),
		"UPDATE auth.users SET raw_user_meta_data = $1, updated_at = NOW() WHERE id = $2",
		metadata, userID)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"Failed to update user: %s"}`, err.Error()), 500)
		return
	}

	if err := tx.Commit(r.Context()); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), 500)
		return
	}

	// Fetch updated user object to return
	res, err := c.goTrue.HandleGetUser(r.Context(), ctx.ProjectPool, userID)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), 400)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)
}

// LinkUserIdentity handles POST /auth/users/:id/identities - Links a new strategy identity to a user
func (c *AuthController) LinkUserIdentity(w http.ResponseWriter, r *http.Request) {
	val := r.Context().Value(types.CascataCtxKey)
	if val == nil {
		http.Error(w, `{"error":"Context Lost"}`, 500)
		return
	}
	ctx := val.(*types.CascataRequest)
	if ctx.Project == nil || ctx.ProjectPool == nil {
		http.Error(w, `{"error":"Project Context Required"}`, 401)
		return
	}

	// Extract userId from URL path
	pathParts := strings.Split(r.URL.Path, "/")
	var userID string
	for i, part := range pathParts {
		if part == "users" && i+1 < len(pathParts) {
			userID = pathParts[i+1]
			break
		}
	}

	if userID == "" {
		http.Error(w, `{"error":"User ID Required"}`, 400)
		return
	}

	var params struct {
		Provider   string `json:"provider"`
		Identifier string `json:"identifier"`
		Password   string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"Invalid request: %s"}`, err.Error()), 400)
		return
	}

	if params.Provider == "" || params.Identifier == "" {
		http.Error(w, `{"error":"Provider and Identifier are required"}`, 400)
		return
	}

	providerClean := strings.TrimSpace(strings.ToLower(params.Provider))
	identifierClean := strings.TrimSpace(params.Identifier)
	if providerClean == "email" {
		identifierClean = strings.ToLower(identifierClean)
	}

	tx, err := ctx.ProjectPool.Begin(r.Context())
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), 500)
		return
	}
	defer tx.Rollback(r.Context())

	// Check if user exists
	var userExists bool
	err = tx.QueryRow(r.Context(), "SELECT EXISTS(SELECT 1 FROM auth.users WHERE id = $1)", userID).Scan(&userExists)
	if err != nil || !userExists {
		http.Error(w, `{"error":"User not found"}`, 404)
		return
	}

	// Check if this identity is already taken
	var existingUser string
	err = tx.QueryRow(r.Context(),
		"SELECT user_id FROM auth.identities WHERE provider = $1 AND identifier = $2",
		providerClean, identifierClean).Scan(&existingUser)
	if err == nil {
		if existingUser == userID {
			http.Error(w, `{"error":"Identity already linked to this user"}`, 400)
			return
		}
		http.Error(w, `{"error":"Identity already linked to another user"}`, 400)
		return
	}

	var passwordHash *string
	if params.Password != "" {
		hash, err := bcrypt.GenerateFromPassword([]byte(params.Password), 10)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"Password hash failed: %s"}`, err.Error()), 500)
			return
		}
		hp := string(hash)
		passwordHash = &hp
	}

	// Link identity (verified immediately since this is an administrative action from Dashboard)
	_, err = tx.Exec(r.Context(),
		`INSERT INTO auth.identities (user_id, provider, identifier, password_hash, verified_at)
		 VALUES ($1, $2, $3, $4, NOW())`,
		userID, providerClean, identifierClean, passwordHash)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"Failed to link identity: %s"}`, err.Error()), 500)
		return
	}

	if err := tx.Commit(r.Context()); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), 500)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Identity linked successfully",
	})
}

// UnlinkUserIdentity handles DELETE /auth/users/:id/strategies/:identityId - Unlinks a strategy identity
func (c *AuthController) UnlinkUserIdentity(w http.ResponseWriter, r *http.Request) {
	val := r.Context().Value(types.CascataCtxKey)
	if val == nil {
		http.Error(w, `{"error":"Context Lost"}`, 500)
		return
	}
	ctx := val.(*types.CascataRequest)
	if ctx.Project == nil || ctx.ProjectPool == nil {
		http.Error(w, `{"error":"Project Context Required"}`, 401)
		return
	}

	pathParts := strings.Split(r.URL.Path, "/")
	var userID, identityID string
	for i, part := range pathParts {
		if part == "users" && i+1 < len(pathParts) {
			userID = pathParts[i+1]
		}
		if part == "strategies" && i+1 < len(pathParts) {
			identityID = pathParts[i+1]
		}
	}

	if userID == "" || identityID == "" {
		http.Error(w, `{"error":"User ID and Identity ID are required"}`, 400)
		return
	}

	tx, err := ctx.ProjectPool.Begin(r.Context())
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), 500)
		return
	}
	defer tx.Rollback(r.Context())

	// Security check: cannot delete the last identity of the user
	var identityCount int
	err = tx.QueryRow(r.Context(), "SELECT COUNT(*) FROM auth.identities WHERE user_id = $1", userID).Scan(&identityCount)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), 500)
		return
	}
	if identityCount <= 1 {
		http.Error(w, `{"error":"Cannot unlink the only identity of this user"}`, 400)
		return
	}

	// Delete identity
	res, err := tx.Exec(r.Context(),
		"DELETE FROM auth.identities WHERE id = $1 AND user_id = $2",
		identityID, userID)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"Failed to delete identity: %s"}`, err.Error()), 500)
		return
	}

	rowsAffected := res.RowsAffected()
	if rowsAffected == 0 {
		http.Error(w, `{"error":"Identity not found"}`, 404)
		return
	}

	if err := tx.Commit(r.Context()); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), 500)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Identity unlinked successfully",
	})
}

func (c *AuthController) EnrollMFA(w http.ResponseWriter, r *http.Request) {
	val := r.Context().Value(types.CascataCtxKey)
	if val == nil {
		http.Error(w, `{"error":"Context Lost"}`, 500)
		return
	}
	ctx := val.(*types.CascataRequest)
	
	var userID string
	if ctx.User != nil {
		if sub, ok := ctx.User["sub"].(string); ok {
			userID = sub
		}
	}
	if userID == "" {
		http.Error(w, `{"error":"Unauthorized"}`, 401)
		return
	}
	
	authSvc := &services.AuthService{}
	
	var email string
	ctx.ProjectPool.QueryRow(r.Context(), "SELECT raw_user_meta_data->>'email' FROM auth.users WHERE id = $1", userID).Scan(&email)
	if email == "" { email = "user-" + userID[:8] }
	
	issuer := ctx.Project.Name
	if issuer == "" { issuer = "CascataApp" }
	
	secret, u := authSvc.GenerateTOTPSecret(issuer, email)
	
	// Clean up any existing TOTP identities for this user to avoid clutter
	ctx.ProjectPool.Exec(r.Context(), "DELETE FROM auth.identities WHERE user_id = $1 AND provider = 'totp'", userID)
	
	// Persist the secret as an unverified TOTP factor in the database (verified_at = NULL)
	_, err := ctx.ProjectPool.Exec(r.Context(), `
		INSERT INTO auth.identities (user_id, provider, identifier, verified_at)
		VALUES ($1, 'totp', $2, NULL)
	`, userID, secret)
	if err != nil {
		http.Error(w, `{"error":"Failed to initiate MFA"}`, 500)
		return
	}
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"secret": secret,
		"qr_code_url": u,
	})
}

func (c *AuthController) VerifyMFA(w http.ResponseWriter, r *http.Request) {
	val := r.Context().Value(types.CascataCtxKey)
	if val == nil {
		http.Error(w, `{"error":"Context Lost"}`, 500)
		return
	}
	ctx := val.(*types.CascataRequest)
	
	var userID string
	if ctx.User != nil {
		if sub, ok := ctx.User["sub"].(string); ok {
			userID = sub
		}
	}
	if userID == "" {
		http.Error(w, `{"error":"Unauthorized"}`, 401)
		return
	}
	
	var params struct {
		Secret string `json:"secret"`
		Code   string `json:"code"`
	}
	json.NewDecoder(r.Body).Decode(&params)
	
	// Support standard verification (without secret in payload) by loading the pending TOTP secret from the DB
	if params.Secret == "" {
		err := ctx.ProjectPool.QueryRow(r.Context(), `
			SELECT identifier FROM auth.identities 
			WHERE user_id = $1 AND provider = 'totp' AND verified_at IS NULL
			ORDER BY created_at DESC LIMIT 1
		`, userID).Scan(&params.Secret)
		if err != nil || params.Secret == "" {
			http.Error(w, `{"error":"No pending MFA enrollment found"}`, 400)
			return
		}
	}
	
	authSvc := &services.AuthService{}
	if !authSvc.VerifyTOTP(params.Secret, params.Code) {
		http.Error(w, `{"error":"Invalid TOTP Code"}`, 400)
		return
	}
	
	_, err := ctx.ProjectPool.Exec(r.Context(), `
		INSERT INTO auth.identities (user_id, provider, identifier, verified_at)
		VALUES ($1, 'totp', $2, now())
		ON CONFLICT (provider, identifier) DO UPDATE SET verified_at = now()
	`, userID, params.Secret)
	
	if err != nil {
		http.Error(w, `{"error":"Failed to link MFA"}`, 500)
		return
	}
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "MFA activated successfully",
	})
}

func (c *AuthController) Challenge(w http.ResponseWriter, r *http.Request) {
	val := r.Context().Value(types.CascataCtxKey)
	if val == nil {
		http.Error(w, `{"error":"Context Lost"}`, 500)
		return
	}
	ctx := val.(*types.CascataRequest)
	
	var params struct {
		Provider   string `json:"provider"`
		Identifier string `json:"identifier"`
	}
	json.NewDecoder(r.Body).Decode(&params)
	
	if params.Provider == "" || params.Identifier == "" {
		http.Error(w, `{"error":"Provider and identifier required"}`, 400)
		return
	}
	
	authSvc := &services.AuthService{}
	err := authSvc.InitiatePasswordless(r.Context(), ctx.ProjectPool, params.Provider, params.Identifier)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), 500)
		return
	}
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Challenge initiated. Check your identifier.",
	})
}

func (c *AuthController) VerifyChallenge(w http.ResponseWriter, r *http.Request) {
	val := r.Context().Value(types.CascataCtxKey)
	if val == nil {
		http.Error(w, `{"error":"Context Lost"}`, 500)
		return
	}
	ctx := val.(*types.CascataRequest)
	
	var params struct {
		Provider   string `json:"provider"`
		Identifier string `json:"identifier"`
		Code       string `json:"code"`
	}
	json.NewDecoder(r.Body).Decode(&params)
	
	if params.Provider == "" || params.Identifier == "" || params.Code == "" {
		http.Error(w, `{"error":"Provider, identifier, and code required"}`, 400)
		return
	}
	
	var storedCode string
	err := ctx.ProjectPool.QueryRow(r.Context(), "SELECT code FROM auth.otp_codes WHERE provider = $1 AND identifier = $2 AND expires_at > NOW()", params.Provider, params.Identifier).Scan(&storedCode)
	if err != nil || storedCode != params.Code {
		http.Error(w, `{"error":"Invalid or expired code"}`, 400)
		return
	}
	
	ctx.ProjectPool.Exec(r.Context(), "DELETE FROM auth.otp_codes WHERE provider = $1 AND identifier = $2", params.Provider, params.Identifier)
	
	var userID string
	err = ctx.ProjectPool.QueryRow(r.Context(), "SELECT user_id FROM auth.identities WHERE provider = $1 AND identifier = $2", params.Provider, params.Identifier).Scan(&userID)
	if err != nil {
		http.Error(w, `{"error":"User not found. Please sign up first."}`, 404)
		return
	}
	
	authSvc := &services.AuthService{}
	deviceInfo := types.DeviceInfo{
		IP:        ctx.IP,
		UserAgent: r.Header.Get("User-Agent"),
	}
	session, err := authSvc.CreateSession(r.Context(), userID, ctx.ProjectPool, ctx.Project.JWTSecret, "1h", 30, params.Provider, deviceInfo)
	if err != nil {
		http.Error(w, `{"error":"Failed to create session"}`, 500)
		return
	}
	
	goTrueSvc := &services.GoTrueService{}
	res := goTrueSvc.FormatSessionResponse(r.Context(), ctx.ProjectPool, session)
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)
}
