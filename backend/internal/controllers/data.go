package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"cascata-backend/internal/config"
	"cascata-backend/internal/services"
	"cascata-backend/internal/services/nexus"
	"cascata-backend/internal/types"
	"cascata-backend/internal/utils"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type DataController struct {
	CryptoSvc    *services.CryptoService
	ExtensionSvc *services.ExtensionService
	ComputedSvc  *services.ComputedService
	NexusSvc     *nexus.NexusService // Nexus Engine v0 — new automation system
}

// isInternalRequest detects requests coming from the internal TablePanel UI.
// When true, Nexus automations are bypassed to prevent sequestro of internal CRUD.
func isInternalRequest(r *http.Request) bool {
	return r.Header.Get("X-Cascata-Source") == "internal-panel"
}

func normalizeStepUpFactor(factor string) string {
	f := strings.ToLower(strings.TrimSpace(factor))
	switch f {
	case "totp/mfa", "mfa":
		return "totp"
	case "email_otp", "email":
		return "otp"
	case "biometria":
		return "passkey"
	default:
		return f
	}
}

func normalizeTableSecurityOperations(ops []string) map[string]bool {
	normalized := make(map[string]bool)
	for _, op := range ops {
		switch strings.ToLower(strings.TrimSpace(op)) {
		case "read", "select":
			normalized["SELECT"] = true
		case "create", "insert":
			normalized["INSERT"] = true
		case "update", "patch", "put":
			normalized["UPDATE"] = true
		case "delete", "remove":
			normalized["DELETE"] = true
		case "write", "mutation", "mutate":
			normalized["INSERT"] = true
			normalized["UPDATE"] = true
			normalized["DELETE"] = true
		case "crud", "all", "*":
			normalized["SELECT"] = true
			normalized["INSERT"] = true
			normalized["UPDATE"] = true
			normalized["DELETE"] = true
		}
	}
	return normalized
}

func normalizeTableSecurityFactors(factors []string) map[string]bool {
	normalized := make(map[string]bool)
	for _, factor := range factors {
		f := normalizeStepUpFactor(factor)
		if f != "" {
			normalized[f] = true
		}
	}
	if len(normalized) == 0 {
		normalized["totp"] = true
		normalized["otp"] = true
		normalized["passkey"] = true
	}
	return normalized
}

func tableSecurityFactorList(factors map[string]bool) []string {
	order := []string{"otp", "totp", "passkey"}
	out := make([]string, 0, len(factors))
	for _, factor := range order {
		if factors[factor] {
			out = append(out, factor)
		}
	}
	return out
}

func tableSecurityMethodList(operations []string) []string {
	ops := normalizeTableSecurityOperations(operations)
	out := make([]string, 0, 4)
	if ops["SELECT"] {
		out = append(out, "read")
	}
	if ops["INSERT"] && ops["UPDATE"] && ops["DELETE"] {
		out = append(out, "write")
		return out
	}
	if ops["INSERT"] {
		out = append(out, "create")
	}
	if ops["UPDATE"] {
		out = append(out, "update")
	}
	if ops["DELETE"] {
		out = append(out, "delete")
	}
	return out
}

func (d *DataController) enforceTableStepUp(w http.ResponseWriter, r *http.Request, ctx *types.CascataRequest, tableName, operation string) bool {
	if ctx == nil || ctx.Project == nil || ctx.IsSystemRequest {
		return true
	}
	rule, ok := ctx.Project.Metadata.TableSecurity[tableName]
	if !ok {
		return true
	}
	ops := normalizeTableSecurityOperations(rule.Operations)
	if !ops[operation] {
		return true
	}

	allowed := normalizeTableSecurityFactors(rule.AllowedFactors)
	for _, provider := range strings.Split(ctx.StepUpProviders, ",") {
		if allowed[normalizeStepUpFactor(provider)] {
			return true
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"error":            "MFA_REQUIRED",
		"message":          "Valid step-up authorization is required for this table operation.",
		"table":            tableName,
		"operation":        operation,
		"required_factors": tableSecurityFactorList(allowed),
	})
	return false
}

// --- ASSET SQL PARSING UTILITIES ---

// extractObjectName extrai o nome do objeto (função, trigger) do SQL DDL
func extractObjectName(sql string, assetType string) string {
	sql = strings.TrimSpace(sql)

	switch assetType {
	case "rpc":
		// CREATE [OR REPLACE] FUNCTION schema.name(...)
		re := regexp.MustCompile(`(?i)CREATE\s+(?:OR\s+REPLACE\s+)?FUNCTION\s+(?:(\w+)\.)?(\w+)`)
		if matches := re.FindStringSubmatch(sql); matches != nil {
			if len(matches) >= 3 && matches[2] != "" {
				return matches[2]
			}
		}
	case "trigger":
		// CREATE [OR REPLACE] TRIGGER name
		re := regexp.MustCompile(`(?i)CREATE\s+(?:OR\s+REPLACE\s+)?TRIGGER\s+(\w+)`)
		if matches := re.FindStringSubmatch(sql); matches != nil {
			if len(matches) >= 2 {
				return matches[1]
			}
		}
	case "cron":
		// Para cron, tenta extrair nome da função ou usa um padrão
		re := regexp.MustCompile(`(?i)CREATE\s+(?:OR\s+REPLACE\s+)?FUNCTION\s+(?:(\w+)\.)?(\w+)`)
		if matches := re.FindStringSubmatch(sql); matches != nil {
			if len(matches) >= 3 && matches[2] != "" {
				return matches[2]
			}
		}
	}

	return ""
}

// generateDropSQL gera o comando DROP apropriado para o tipo de objeto
func generateDropSQL(assetType string, objectName string) string {
	switch assetType {
	case "rpc":
		return fmt.Sprintf(`DROP FUNCTION IF EXISTS %s CASCADE`, objectName)
	case "trigger":
		// Para triggers precisamos da tabela, mas vamos tentar um genérico
		return fmt.Sprintf(`DROP TRIGGER IF EXISTS %s ON public.%%s`, objectName)
	case "cron":
		// Cron jobs são funções no PostgreSQL
		return fmt.Sprintf(`DROP FUNCTION IF EXISTS %s CASCADE`, objectName)
	}
	return ""
}

// extractTriggerInfo extrai o nome do trigger e da tabela do SQL CREATE TRIGGER
// Retorna: (triggerName, tableName)
func extractTriggerInfo(sql string) (string, string) {
	// Pattern: CREATE [OR REPLACE] TRIGGER name ... ON table_name
	// Ex: CREATE TRIGGER trg_categories_updated_at BEFORE UPDATE ON public.categories ...
	re := regexp.MustCompile(`(?i)CREATE\s+(?:OR\s+REPLACE\s+)?TRIGGER\s+(\w+)\s+.*ON\s+(?:public\.)?(\w+)`)
	matches := re.FindStringSubmatch(sql)
	if len(matches) >= 3 {
		return matches[1], matches[2]
	}
	return "", ""
}

// isManagedObjectType determina se o tipo é um objeto gerenciado (vs snippet)
func isManagedObjectType(assetType string) bool {
	return assetType == "rpc" || assetType == "trigger" || assetType == "cron"
}

// min retorna o menor valor entre a e b
func min(a, b int) int {
	if a < b { return a }
	return b
}

func dbExecutionMode(_ string) string {
	return "fast_lane"
}

// TransformCronSQL detecta chamadas pg_cron e transforma para usar schedule_in_database
// Retorna (isCronSQL bool, transformedSQL string)
// O pg_cron só existe no banco de sistema, então precisamos rotear as chamadas
func (d *DataController) TransformCronSQL(sql string, targetDbName string) (bool, string) {
	// Padrões para detectar chamadas pg_cron
	cronPatterns := []*regexp.Regexp{
		regexp.MustCompile(`(?i)SELECT\s+cron\.schedule\s*\(`),
		regexp.MustCompile(`(?i)SELECT\s+\*\s+FROM\s+cron\.schedule\s*\(`),
		regexp.MustCompile(`(?i)cron\.unschedule\s*\(`),
		regexp.MustCompile(`(?i)cron\.alter_job\s*\(`),
		regexp.MustCompile(`(?i)cron\.schedule_in_database\s*\(`),
	}

	// Verifica se é uma chamada pg_cron
	isCronSQL := false
	for _, pattern := range cronPatterns {
		if pattern.MatchString(sql) {
			isCronSQL = true
			break
		}
	}

	if !isCronSQL {
		return false, sql
	}

	// Se já é schedule_in_database, retorna como está
	// (assume que o database correto já foi informado)
	if regexp.MustCompile(`(?i)cron\.schedule_in_database\s*\(`).MatchString(sql) {
		log.Printf("[TransformCronSQL] Already using schedule_in_database, skipping transformation")
		return true, sql
	}

	// Transforma cron.schedule() em cron.schedule_in_database()
	// Lida com parâmetros que podem conter parênteses em strings dollar-quoted ($$...$$)
	transformed := transformCronSchedule(sql, targetDbName)

	log.Printf("[TransformCronSQL] Original SQL length: %d, Transformed SQL length: %d", len(sql), len(transformed))
	log.Printf("[TransformCronSQL] Target database: %s", targetDbName)

	return true, transformed
}

// transformCronSchedule transforma cron.schedule() em cron.schedule_in_database()
// Parser robusto que lida com todas as sintaxes PostgreSQL incluindo dollar quotes
// Garante que jobname seja único por tenant adicionando targetDbName como sufixo
func transformCronSchedule(sql string, targetDbName string) string {
	// Encontra a posição de "cron.schedule(" (case insensitive)
	cronRe := regexp.MustCompile(`(?i)cron\.schedule\s*\(`)
	cronIdx := cronRe.FindStringIndex(sql)
	if cronIdx == nil {
		log.Printf("[transformCronSchedule] Pattern 'cron.schedule(' not found")
		return sql
	}

	// Posição do '(' após "cron.schedule"
	openParenPos := cronIdx[1] - 1

	// Encontra o ')' correspondente usando parser robusto
	closeParenPos := findMatchingParenRobust(sql, openParenPos)
	if closeParenPos == -1 {
		log.Printf("[transformCronSchedule] Could not find matching closing parenthesis")
		return sql
	}

	// Extrai os parâmetros (dentro dos parênteses)
	params := sql[openParenPos+1 : closeParenPos]
	log.Printf("[transformCronSchedule] Params extracted: %q", truncateString(params, 80))

	// Modifica o jobname (primeiro parâmetro) para incluir o nome do banco
	// Isso garante unicidade por tenant: 'jobname' -> 'jobname-dbname'
	modifiedParams := modifyJobnameForUniqueness(params, targetDbName)

	// Detecta o prefixo antes de "cron.schedule" para reconstruir corretamente
	beforeCronSchedule := sql[:cronIdx[0]] // Tudo antes de "cron.schedule"
	afterCloseParen := sql[closeParenPos+1:] // Tudo depois do ')'

	// Constrói o SQL final: cron.schedule_in_database(jobname-dbname, schedule, command, targetDbName)
	result := beforeCronSchedule + "cron.schedule_in_database(" + modifiedParams + ", '" + targetDbName + "')" + afterCloseParen

	log.Printf("[transformCronSchedule] Transformed SQL: %q", truncateString(result, 150))

	return result
}

// modifyJobnameForUniqueness modifica o primeiro parâmetro (jobname) para incluir o nome do banco
// Garante que cada tenant tenha seus cron jobs isolados
func modifyJobnameForUniqueness(params string, targetDbName string) string {
	// Encontra o primeiro parâmetro (jobname) que deve ser uma string entre aspas
	// Padrão: 'jobname' ou "jobname" seguido de vírgula
	
	// Regex para capturar o jobname: string entre aspas simples no início
	jobnameRe := regexp.MustCompile(`^\s*('[^']*')`)
	matches := jobnameRe.FindStringSubmatchIndex(params)
	
	if matches == nil {
		log.Printf("[modifyJobnameForUniqueness] Could not find jobname in params: %q", truncateString(params, 50))
		return params
	}
	
	// Extrai o jobname original (incluindo as aspas)
	jobnameStart := matches[2]
	jobnameEnd := matches[3]
	originalJobname := params[jobnameStart:jobnameEnd]
	
	// Remove as aspas para o sufixo
	jobnameWithoutQuotes := originalJobname[1 : len(originalJobname)-1]
	
	// Verifica se já tem o sufixo do banco
	expectedSuffix := "-" + targetDbName
	if strings.HasSuffix(jobnameWithoutQuotes, expectedSuffix) {
		log.Printf("[modifyJobnameForUniqueness] Jobname already has suffix: %s", originalJobname)
		return params
	}
	
	// Constrói o novo jobname com sufixo
	newJobname := "'" + jobnameWithoutQuotes + expectedSuffix + "'"
	
	log.Printf("[modifyJobnameForUniqueness] Original: %s -> New: %s", originalJobname, newJobname)
	
	// Substitui o jobname original pelo novo nos parâmetros
	return params[:jobnameStart] + newJobname + params[jobnameEnd:]
}

// findMatchingParenRobust encontra o parêntese de fechamento correspondente
// Parser completo que entende todas as sintaxes de string do PostgreSQL
func findMatchingParenRobust(s string, openPos int) int {
	if openPos >= len(s) || s[openPos] != '(' {
		return -1
	}

	i := openPos + 1
	depth := 1

	for i < len(s) {
		ch := s[i]

		switch {
		// Dollar-quoted string: $tag$ ... $tag$ (where tag can be empty)
		case ch == '$':
			tagStart := parseDollarTag(s, i)
			if tagStart == -1 {
				i++
				continue
			}
			tag := s[i+1 : tagStart]
			// Pula para depois do $tag$ inicial
			i = tagStart + 1
			// Procura o $tag$ de fechamento
			closePos := findDollarQuoteEnd(s, i, tag)
			if closePos == -1 {
				return -1 // String dollar-quoted não fechada
			}
			i = closePos

		// String single-quoted: ' ... ' (com escape '' ou \')
		case ch == '\'':
			i++
			for i < len(s) {
				if s[i] == '\'' {
					// Verifica se é escape ''
					if i+1 < len(s) && s[i+1] == '\'' {
						i += 2 // Pula as duas aspas
						continue
					}
					// Fim da string
					break
				}
				// Escape \' - pula o \ e a '
				if s[i] == '\\' && i+1 < len(s) && s[i+1] == '\'' {
					i += 2
					continue
				}
				i++
			}
			i++

		// String double-quoted (identificadores): " ... "
		case ch == '"':
			i++
			for i < len(s) && s[i] != '"' {
				// Escaped quote: ""
				if s[i] == '"' && i+1 < len(s) && s[i+1] == '"' {
					i += 2
				} else {
					i++
				}
			}
			i++

		// Comentário -- (single line)
		case ch == '-' && i+1 < len(s) && s[i+1] == '-':
			i += 2
			for i < len(s) && s[i] != '\n' {
				i++
			}

		// Comentário /* ... */
		case ch == '/' && i+1 < len(s) && s[i+1] == '*':
			i += 2
			for i < len(s)-1 {
				if s[i] == '*' && s[i+1] == '/' {
					break
				}
				i++
			}
			i += 2

		// Parêntese abrindo
		case ch == '(':
			depth++
			i++

		// Parêntese fechando
		case ch == ')':
			depth--
			if depth == 0 {
				return i
			}
			i++

		default:
			i++
		}
	}

	return -1 // Não encontrou parêntese de fechamento
}

// parseDollarTag parseia uma tag de dollar quote a partir da posição do primeiro $
// Retorna a posição do segundo $ ou -1 se não for um dollar quote válido
func parseDollarTag(s string, dollarPos int) int {
	if s[dollarPos] != '$' {
		return -1
	}

	// Tag pode ser vazia (simples $$) ou conter identificadores
	i := dollarPos + 1
	for i < len(s) && isDollarTagChar(s[i]) {
		i++
	}

	if i >= len(s) || s[i] != '$' {
		return -1
	}

	return i
}

// isDollarTagChar verifica se um caractere é válido em uma tag de dollar quote
// Tags podem conter letras, dígitos e underscores, ou serem vazias
func isDollarTagChar(ch byte) bool {
	return (ch >= 'a' && ch <= 'z') ||
		(ch >= 'A' && ch <= 'Z') ||
		(ch >= '0' && ch <= '9') ||
		ch == '_'
}

// findDollarQuoteEnd procura o fechamento $tag$ de uma string dollar-quoted
// Retorna a posição logo após o fechamento, ou -1 se não encontrar
func findDollarQuoteEnd(s string, startPos int, tag string) int {
	endMarker := "$" + tag + "$"
	i := startPos

	for i < len(s) {
		if s[i] == '$' {
			// Verifica se é o fim
			if i+len(endMarker) <= len(s) && s[i:i+len(endMarker)] == endMarker {
				return i + len(endMarker)
			}
			// Pula dollar quotes aninhados
			nestedTagStart := parseDollarTag(s, i)
			if nestedTagStart != -1 {
				nestedTag := s[i+1 : nestedTagStart]
				nestedEnd := findDollarQuoteEnd(s, nestedTagStart+1, nestedTag)
				if nestedEnd != -1 {
					i = nestedEnd
					continue
				}
			}
		}
		i++
	}

	return -1
}

// truncateString trunca uma string para o tamanho máximo especificado
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// --- PRIVACY ENGINE (Sovereign Synergy) ---

// ApplyMaskingTier implements the real-time data masking and encryption/decryption
// logic, ensuring PII is never exposed to unauthorized roles.
func (d *DataController) ApplyMaskingTier(ctx *types.CascataRequest, data interface{}, tableName string) interface{} {
	metadata := ctx.Project.Metadata
	if metadata.MaskedColumns == nil {
		return data
	}

	masks := metadata.MaskedColumns[tableName]
	if masks == nil {
		return data
	}

	isAdmin := ctx.UserRole == types.RoleService || ctx.IsSystemRequest

	// Recursive applier
	var apply func(map[string]interface{}) map[string]interface{}
	apply = func(row map[string]interface{}) map[string]interface{} {
		newRow := make(map[string]interface{})
		for k, v := range row {
			maskType, hasMask := masks[k]
			if !hasMask {
				newRow[k] = v
				continue
			}

			// 1. ADMIN BYPASS & AUTO-DECRYPTION
			if isAdmin {
				if maskType == "encrypt" {
					if str, ok := v.(string); ok && strings.HasPrefix(str, "c:") {
						dec, _ := d.CryptoSvc.Decrypt(str)
						newRow[k] = dec
					} else {
						newRow[k] = v
					}
				} else {
					newRow[k] = v
				}
				continue
			}

			// 2. PRIVACY ENFORCEMENT (Anon/Authenticated)
			if v == nil {
				newRow[k] = nil
				continue
			}

			switch maskType {
			case "hide":
				// Stripped entirely
			case "mask":
				newRow[k] = "********"
			case "semi-mask":
				str := fmt.Sprintf("%v", v)
				visibleLen := len(str) / 4
				if visibleLen < 1 { visibleLen = 1 }
				newRow[k] = str[:visibleLen] + "****"
			case "encrypt":
				newRow[k] = "[ENCRYPTED]"
			case "blur":
				str := fmt.Sprintf("%v", v)
				if len(str) > 5 {
					newRow[k] = str[:3] + "..." + str[len(str)-2:]
				} else {
					newRow[k] = "***"
				}
			default:
				newRow[k] = v
			}
		}
		return newRow
	}

	// Handle slice vs single object
	switch v := data.(type) {
	case []map[string]interface{}:
		result := make([]map[string]interface{}, len(v))
		for i, row := range v { result[i] = apply(row) }
		return result
	case map[string]interface{}:
		return apply(v)
	default:
		return data
	}
}

// --- DATA OPERATIONS ---

// QueryRows (GET /tables/:tableName) - With RLS Enforcement
func (d *DataController) QueryRows(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context().Value(types.CascataCtxKey).(*types.CascataRequest)
	tableName := chi.URLParam(r, "tableName")
	schema := r.URL.Query().Get("schema")
	if schema == "" { schema = "public" }
	d.ExtractAndValidateStepUp(r, ctx, nil, nil)
	if !d.enforceTableStepUp(w, r, ctx, tableName, "SELECT") {
		return
	}

	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit == 0 || limit > 1000 { limit = 100 }
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))

	sortCol := r.URL.Query().Get("sortColumn")
	sortDir := "ASC"
	if strings.ToLower(r.URL.Query().Get("sortDirection")) == "desc" { sortDir = "DESC" }

	query := fmt.Sprintf("SELECT * FROM %s.%s", utils.QuoteId(schema), utils.QuoteId(tableName))
	if sortCol != "" {
		query += fmt.Sprintf(" ORDER BY %s %s", utils.QuoteId(sortCol), sortDir)
	}
	query += " LIMIT $1 OFFSET $2"

	// ============================================================
	// NEXUS ENGINE v0: PRE_PERSIST SEQUESTRO (SELECT)
	// ============================================================
	automationExecuted := false
	var responseData interface{}

	if d.NexusSvc != nil && !isInternalRequest(r) {
		userUUID := ""
		authSource := string(ctx.UserRole)
		if ctx.User != nil {
			if sub, ok := ctx.User["sub"].(string); ok { userUUID = sub }
		}
		safeHeaders := nexus.ExtractSafeHeaders(r.Header)

		payload := map[string]interface{}{
			"table":  tableName,
			"schema": schema,
			"limit":  limit,
			"offset": offset,
			"event":  "SELECT",
		}

		hookResult, err := d.NexusSvc.ResolvePrePersistForTable(
			r.Context(), ctx.Project.Slug, userUUID, string(ctx.UserRole), authSource,
			tableName, "SELECT", payload, safeHeaders,
		)
		if err == nil && hookResult.Intercepted {
			log.Printf("[QueryRows:Nexus] PRE_PERSIST sequestro ativo — trace=%s", hookResult.TraceID)
			responseData = hookResult.ResponseData
			automationExecuted = true
		}
	}

	if !automationExecuted {
		// ============================================================
		// RLS SECURITY: Execute with user role context
		// ============================================================
		userRole := string(ctx.UserRole)
		if userRole == "" {
			userRole = "anon"
		}
		user := ctx.User
		if user == nil {
			user = make(map[string]interface{})
		}

	result, err := d.ExecuteMultiStatementSQL(
		r.Context(),
		ctx.ProjectPool,
		query,
		[]interface{}{limit, offset},
		ctx.Project.DbName,
		userRole,
		user,
	)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, 500)
		return
	}

		// Apply Privacy Engine
		maskedData := d.ApplyMaskingTier(ctx, result, tableName)
		responseData = maskedData

		// ============================================================
		// NEXUS ENGINE v0: POST_PERSIST SEQUESTRO (SELECT)
		// ============================================================
		if d.NexusSvc != nil && !isInternalRequest(r) {
			userUUID := ""
			authSource := string(ctx.UserRole)
			if ctx.User != nil {
				if sub, ok := ctx.User["sub"].(string); ok { userUUID = sub }
			}
			safeHeaders := nexus.ExtractSafeHeaders(r.Header)

			hookResult, err := d.NexusSvc.ResolvePostPersistForTableSync(
				r.Context(), ctx.Project.Slug, userUUID, authSource,
				tableName, "SELECT", responseData, nil, safeHeaders,
			)
			if err == nil && hookResult.Intercepted {
				log.Printf("[QueryRows:Nexus] POST_PERSIST sequestro ativo — trace=%s", hookResult.TraceID)
				responseData = hookResult.ResponseData
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(responseData)
}

// InsertRows (POST /tables/:tableName/rows)
// SECURITY: ALL writes MUST pass through EnforcePrePersistenceSecurity before touching PostgreSQL
func (d *DataController) InsertRows(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context().Value(types.CascataCtxKey).(*types.CascataRequest)
	tableName := chi.URLParam(r, "tableName")
	schema := r.URL.Query().Get("schema")
	if schema == "" { schema = "public" }

	var body struct {
		Data interface{} `json:"data"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"Invalid payload"}`, 400)
		return
	}

	rows := []map[string]interface{}{}
	switch v := body.Data.(type) {
	case []interface{}:
		for _, item := range v {
			if m, ok := item.(map[string]interface{}); ok {
				rows = append(rows, m)
			}
		}
	case map[string]interface{}:
		rows = append(rows, v)
	}

	if len(rows) == 0 {
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte("[]"))
		return
	}
	d.ExtractAndValidateStepUp(r, ctx, nil, nil)
	for _, row := range rows {
		d.ExtractAndValidateStepUp(r, ctx, nil, row)
	}
	if !d.enforceTableStepUp(w, r, ctx, tableName, "INSERT") {
		return
	}

	// ============================================================
	// SECURITY GATEWAY — INVULNERABLE CHECKPOINT
	// ALL rows MUST pass through EnforcePrePersistenceSecurity
	// No data touches PostgreSQL without passing these layers:
	//   1. FORMAT PATTERN VALIDATION      → fail-fast, HTTP 400
	//   1.1. REQUEST AUTOMATION INTERCEPT → REQUEST_INTERCEPT trigger (enrich/block)
	//   1.2. ENUM TYPE VALIDATION         → fail-fast if invalid ENUM value
	//   2. LOCKED COLUMNS STRIPPING       → silent removal + audit
	//   2.1. AUTO CLOCK SPOOF DETECTION  → detect spoofing attempts
	//   2.2. AUTO CLOCK ENRICHMENT        → add NOW() to auto_clock columns
	//   3. COMPUTED COLUMNS               → formula evaluation
	// ============================================================
	gatewayResult, err := services.GlobalSchemaCache.EnforcePrePersistenceSecurityMulti(
		r.Context(),
		ctx.ProjectPool,
		&ctx.Project.Metadata,
		ctx.Project.Slug,
		schema,
		tableName,
		rows,
		"INSERT",
		d.ComputedSvc,
	)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), 400)
		return
	}
	if len(gatewayResult.StrippedCols) > 0 {
		log.Printf("[SecurityGateway:InsertRows] Stripped locked columns: %v", gatewayResult.StrippedCols)
	}

	// Build type map from cached schema — ZERO PG round-trips
	typeMap := services.GetTypeCastMap(gatewayResult.TableSchema)

	// Update body.Data with sanitized rows
	if len(rows) == 1 {
		body.Data = rows[0]
	} else {
		body.Data = rows
	}

	// ============================================================
	// NEXUS ENGINE v0: PRE_PERSIST SEQUESTRO
	// ============================================================
	var responseData interface{}
	automationExecuted := false

	if d.NexusSvc != nil && !isInternalRequest(r) {
		userUUID := ""
		authSource := string(ctx.UserRole)
		if ctx.User != nil {
			if sub, ok := ctx.User["sub"].(string); ok { userUUID = sub }
		}
		safeHeaders := nexus.ExtractSafeHeaders(r.Header)

		payload := make(map[string]interface{})
		if len(rows) == 1 {
			payload = rows[0]
		} else {
			payload["rows"] = rows
		}
		payload["table"] = tableName
		payload["schema"] = schema
		payload["event"] = "INSERT"

		hookResult, err := d.NexusSvc.ResolvePrePersistForTable(
			r.Context(), ctx.Project.Slug, userUUID, string(ctx.UserRole), authSource,
			tableName, "INSERT", payload, safeHeaders,
		)

		if err != nil {
			log.Printf("[InsertRows:Nexus] PRE_PERSIST error: %v", err)
			http.Error(w, fmt.Sprintf(`{"error":"Nexus automation failed: %s"}`, err.Error()), hookResult.ResponseCode)
			return
		}

		if hookResult.Intercepted {
			log.Printf("[InsertRows:Nexus] PRE_PERSIST sequestro ativo — trace=%s", hookResult.TraceID)
			responseData = hookResult.ResponseData
			automationExecuted = true
		}
	}

	// Default path if no PRE_PERSIST hijack
	if !automationExecuted {
		insertedData, err := utils.InsertRows(r.Context(), ctx.ProjectPool, tableName, body.Data, typeMap, schema)
		if err != nil {
			http.Error(w, `{"error":"`+err.Error()+`"}`, 500)
			return
		}
		responseData = insertedData

		// ============================================================
		// NEXUS ENGINE v0: POST_PERSIST SEQUESTRO (SYNC)
		// ============================================================
		if d.NexusSvc != nil && !isInternalRequest(r) && len(insertedData) > 0 {
			userUUID := ""
			authSource := string(ctx.UserRole)
			if ctx.User != nil {
				if sub, ok := ctx.User["sub"].(string); ok { userUUID = sub }
			}
			safeHeaders := nexus.ExtractSafeHeaders(r.Header)

			origBody := make(map[string]interface{})
			if len(rows) == 1 { origBody = rows[0] } else { origBody["rows"] = rows }

			hookResult, err := d.NexusSvc.ResolvePostPersistForTableSync(
				r.Context(), ctx.Project.Slug, userUUID, authSource,
				tableName, "INSERT", insertedData, origBody, safeHeaders,
			)
			if err == nil && hookResult.Intercepted {
				log.Printf("[InsertRows:Nexus] POST_PERSIST sequestro ativo — trace=%s", hookResult.TraceID)
				responseData = hookResult.ResponseData
			} else {
				// Fallback to ASYNC fire-and-forget for other post-persist automations
				d.NexusSvc.DispatchPostPersistAsync(r.Context(), ctx.Project.Slug, userUUID, string(ctx.UserRole), authSource, tableName, "INSERT", map[string]interface{}{"inserted": insertedData}, origBody)
			}
		}
	}

	// Apply Privacy Masking to final response
	maskedResponse := d.ApplyMaskingTier(ctx, responseData, tableName)

	w.WriteHeader(http.StatusCreated)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(maskedResponse)
}

// UpdateRows (PUT/PATCH /tables/:tableName)
// SECURITY: ALL writes MUST pass through EnforcePrePersistenceSecurity before touching PostgreSQL
func (d *DataController) UpdateRows(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context().Value(types.CascataCtxKey).(*types.CascataRequest)
	tableName := chi.URLParam(r, "tableName")
	schema := r.URL.Query().Get("schema")
	if schema == "" { schema = "public" }

	var body map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid json", 400)
		return
	}

	// DEBUG LOGS
	log.Printf("[UpdateRows DEBUG] table=%s, body=%+v", tableName, body)

	// Extract data from nested "data" field (panel sends {"data": {...}, "pkColumn": "...", "pkValue": "..."})
	var data map[string]interface{}
	if rawData, ok := body["data"]; ok {
		if d, ok := rawData.(map[string]interface{}); ok {
			data = d
			log.Printf("[UpdateRows DEBUG] extracted data from body['data']: %+v", data)
		} else {
			log.Printf("[UpdateRows DEBUG] body['data'] is not map[string]interface{}, type=%T", rawData)
		}
	} else {
		log.Printf("[UpdateRows DEBUG] no body['data'] field found")
	}
	if data == nil {
		data = body // fallback: use whole body if no nested data
		log.Printf("[UpdateRows DEBUG] using whole body as data")
	}

	// Extract PK from body (but DON'T add to data yet — prevents Security Gateway from stripping it)
	var pkCol string
	var pkValue interface{}
	if pkColRaw, ok := body["pkColumn"].(string); ok {
		pkCol = pkColRaw
		if pkVal, ok := body["pkValue"]; ok {
			pkValue = pkVal
			log.Printf("[UpdateRows DEBUG] extracted pk %s=%v", pkCol, pkValue)
		} else {
			log.Printf("[UpdateRows DEBUG] pkColumn found but pkValue missing")
		}
	} else {
		log.Printf("[UpdateRows DEBUG] pkColumn not found or not string, type=%T", body["pkColumn"])
	}

	// Extract and validate Step-Up, clean security fields
	d.ExtractAndValidateStepUp(r, ctx, body, data)
	if !d.enforceTableStepUp(w, r, ctx, tableName, "UPDATE") {
		return
	}

	log.Printf("[UpdateRows DEBUG] final data map (before security): %+v", data)

	// ============================================================
	// SECURITY GATEWAY — INVULNERABLE CHECKPOINT
	// ALL updates MUST pass through EnforcePrePersistenceSecurity
	// No data touches PostgreSQL without passing these layers:
	//   1. FORMAT PATTERN VALIDATION      → fail-fast, HTTP 400
	//   1.1. REQUEST AUTOMATION INTERCEPT → REQUEST_INTERCEPT trigger (enrich/block)
	//   1.2. ENUM TYPE VALIDATION         → fail-fast if invalid ENUM value
	//   2. LOCKED COLUMNS STRIPPING       → silent removal + audit
	//   2.1. AUTO CLOCK SPOOF DETECTION  → detect spoofing attempts
	//   2.2. AUTO CLOCK ENRICHMENT        → add NOW() to auto_clock columns
	//   3. COMPUTED COLUMNS               → formula evaluation
	// ============================================================
	gatewayResult, err := services.GlobalSchemaCache.EnforcePrePersistenceSecurity(
		r.Context(),
		ctx.ProjectPool,
		&ctx.Project.Metadata,
		ctx.Project.Slug,
		schema,
		tableName,
		data,
		"UPDATE",
		d.ComputedSvc,
	)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), 400)
		return
	}
	if len(gatewayResult.StrippedCols) > 0 {
		log.Printf("[SecurityGateway:UpdateRows] Stripped locked columns: %v", gatewayResult.StrippedCols)
	}

	// Get PK column from cache if not provided in body — ZERO PG round-trips
	if pkCol == "" {
		pkCol = services.FindPrimaryKeyColumn(r.Context(), ctx.ProjectPool, ctx.Project.Slug, schema, tableName)
	}
	log.Printf("[UpdateRows DEBUG] PK column: %s", pkCol)

	// Restore PK to sanitized data AFTER security gateway (so it wasn't stripped if immutable)
	if pkCol != "" && pkValue != nil {
		gatewayResult.SanitizedData[pkCol] = pkValue
		log.Printf("[UpdateRows DEBUG] restored pk %s=%v to sanitized data", pkCol, pkValue)
	}

	// ============================================================
	// NEXUS ENGINE v0: PRE_PERSIST SEQUESTRO
	// ============================================================
	var responseData interface{}
	automationExecuted := false

	if d.NexusSvc != nil && !isInternalRequest(r) {
		userUUID := ""
		authSource := string(ctx.UserRole)
		if ctx.User != nil {
			if sub, ok := ctx.User["sub"].(string); ok { userUUID = sub }
		}
		safeHeaders := nexus.ExtractSafeHeaders(r.Header)

		payload := make(map[string]interface{})
		for k, v := range gatewayResult.SanitizedData {
			payload[k] = v
		}
		payload["table"] = tableName
		payload["schema"] = schema
		payload["event"] = "UPDATE"
		if pkCol != "" && pkValue != nil {
			payload["pk_column"] = pkCol
			payload["pk_value"] = pkValue
		}

		hookResult, err := d.NexusSvc.ResolvePrePersistForTable(
			r.Context(), ctx.Project.Slug, userUUID, string(ctx.UserRole), authSource,
			tableName, "UPDATE", payload, safeHeaders,
		)
		if err != nil {
			log.Printf("[UpdateRows:Nexus] PRE_PERSIST error: %v", err)
			http.Error(w, fmt.Sprintf(`{"error":"Nexus automation failed: %s"}`, err.Error()), hookResult.ResponseCode)
			return
		}
		if hookResult.Intercepted {
			log.Printf("[UpdateRows:Nexus] PRE_PERSIST sequestro ativo — trace=%s", hookResult.TraceID)
			responseData = hookResult.ResponseData
			automationExecuted = true
		}
	}

	// Default path if no PRE_PERSIST hijack
	if !automationExecuted {
		updatedData, err := utils.UpdateRows(r.Context(), ctx.ProjectPool, tableName, gatewayResult.SanitizedData, pkCol, schema)
		if err != nil {
			http.Error(w, `{"error":"`+err.Error()+`"}`, 500)
			return
		}
		responseData = updatedData

		// ============================================================
		// NEXUS ENGINE v0: POST_PERSIST SEQUESTRO (SYNC)
		// ============================================================
		if d.NexusSvc != nil && !isInternalRequest(r) && len(updatedData) > 0 {
			userUUID := ""
			authSource := string(ctx.UserRole)
			if ctx.User != nil {
				if sub, ok := ctx.User["sub"].(string); ok { userUUID = sub }
			}
			safeHeaders := nexus.ExtractSafeHeaders(r.Header)

			hookResult, err := d.NexusSvc.ResolvePostPersistForTableSync(
				r.Context(), ctx.Project.Slug, userUUID, authSource,
				tableName, "UPDATE", updatedData, gatewayResult.SanitizedData, safeHeaders,
			)
			if err == nil && hookResult.Intercepted {
				log.Printf("[UpdateRows:Nexus] POST_PERSIST sequestro ativo — trace=%s", hookResult.TraceID)
				responseData = hookResult.ResponseData
			} else {
				// Fallback to ASYNC
				d.NexusSvc.DispatchPostPersistAsync(r.Context(), ctx.Project.Slug, userUUID, string(ctx.UserRole), authSource, tableName, "UPDATE", map[string]interface{}{"updated": updatedData}, gatewayResult.SanitizedData)
			}
		}
	}

	masked := d.ApplyMaskingTier(ctx, responseData, tableName)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(masked)
}

// DeleteRows (DELETE /tables/:tableName/rows)
func (d *DataController) DeleteRows(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context().Value(types.CascataCtxKey).(*types.CascataRequest)
	tableName := chi.URLParam(r, "tableName")
	schema := r.URL.Query().Get("schema")
	if schema == "" { schema = "public" }

	var body map[string]interface{}
	json.NewDecoder(r.Body).Decode(&body)
	d.ExtractAndValidateStepUp(r, ctx, body, nil)

	idsRaw, _ := body["ids"].([]interface{})

	if len(idsRaw) == 0 {
		http.Error(w, `{"error":"IDs required"}`, 400)
		return
	}
	if !d.enforceTableStepUp(w, r, ctx, tableName, "DELETE") {
		return
	}

	// 1. Detect PK Column (Safety Requirement)
	pkQuery := `
		SELECT kcu.column_name 
		FROM information_schema.table_constraints tco
		JOIN information_schema.key_column_usage kcu 
		  ON kcu.constraint_name = tco.constraint_name AND kcu.constraint_schema = tco.constraint_schema
		WHERE tco.constraint_type = 'PRIMARY KEY' AND tco.table_schema = $1 AND tco.table_name = $2
	`
	var pkCol string
	err := ctx.ProjectPool.QueryRow(r.Context(), pkQuery, schema, tableName).Scan(&pkCol)
	if err != nil {
		http.Error(w, `{"error":"Table has no primary key. Use SQL Editor for manual deletes."}`, 400)
		return
	}

	// Convert []interface{} to []string for pgx compatibility
	idStrings := make([]string, len(idsRaw))
	for i, id := range idsRaw {
		idStrings[i] = fmt.Sprintf("%v", id)
	}

	// ============================================================
	// NEXUS ENGINE v0: PRE_PERSIST SEQUESTRO
	// ============================================================
	var responseData interface{}
	automationExecuted := false

	if d.NexusSvc != nil && !isInternalRequest(r) {
		userUUID := ""
		authSource := string(ctx.UserRole)
		if ctx.User != nil {
			if sub, ok := ctx.User["sub"].(string); ok { userUUID = sub }
		}
		safeHeaders := nexus.ExtractSafeHeaders(r.Header)

		payload := map[string]interface{}{
			"ids":       idStrings,
			"pk_column": pkCol,
			"table":     tableName,
			"schema":    schema,
			"event":     "DELETE",
		}

		hookResult, err := d.NexusSvc.ResolvePrePersistForTable(
			r.Context(), ctx.Project.Slug, userUUID, string(ctx.UserRole), authSource,
			tableName, "DELETE", payload, safeHeaders,
		)
		if err != nil {
			log.Printf("[DeleteRows:Nexus] PRE_PERSIST error: %v", err)
			http.Error(w, fmt.Sprintf(`{"error":"Nexus automation failed: %s"}`, err.Error()), hookResult.ResponseCode)
			return
		}
		if hookResult.Intercepted {
			log.Printf("[DeleteRows:Nexus] PRE_PERSIST sequestro ativo — trace=%s", hookResult.TraceID)
			responseData = hookResult.ResponseData
			automationExecuted = true
		}
	}

	// Default behavior if no sequestro
	if !automationExecuted {
		sql := fmt.Sprintf("DELETE FROM %s.%s WHERE %s = ANY($1) RETURNING *", utils.QuoteId(schema), utils.QuoteId(tableName), utils.QuoteId(pkCol))
		queryRows, err := ctx.ProjectPool.Query(r.Context(), sql, idStrings)
		if err != nil {
			http.Error(w, `{"error":"`+err.Error()+`"}`, 500)
			return
		}
		defer queryRows.Close()

		var result []map[string]interface{}
		fDesc := queryRows.FieldDescriptions()
		for queryRows.Next() {
			vals, _ := queryRows.Values()
			row := make(map[string]interface{})
			for i, fd := range fDesc {
				row[fd.Name] = utils.PurifyPgxValue(vals[i])
			}
			result = append(result, row)
		}

		responseData = result

		// ============================================================
		// NEXUS ENGINE v0: POST_PERSIST SEQUESTRO (SYNC)
		// ============================================================
		if d.NexusSvc != nil && !isInternalRequest(r) && len(result) > 0 {
			userUUID := ""
			authSource := string(ctx.UserRole)
			if ctx.User != nil {
				if sub, ok := ctx.User["sub"].(string); ok { userUUID = sub }
			}
			safeHeaders := nexus.ExtractSafeHeaders(r.Header)

			origBody := map[string]interface{}{"ids": idStrings, "pk_column": pkCol}

			hookResult, err := d.NexusSvc.ResolvePostPersistForTableSync(
				r.Context(), ctx.Project.Slug, userUUID, authSource,
				tableName, "DELETE", result, origBody, safeHeaders,
			)
			if err == nil && hookResult.Intercepted {
				log.Printf("[DeleteRows:Nexus] POST_PERSIST sequestro ativo — trace=%s", hookResult.TraceID)
				responseData = hookResult.ResponseData
			} else {
				// Fallback to ASYNC
				d.NexusSvc.DispatchPostPersistAsync(r.Context(), ctx.Project.Slug, userUUID, string(ctx.UserRole), authSource, tableName, "DELETE", map[string]interface{}{"deleted": result}, origBody)
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(responseData)
}

// HandlePostgrest (ALL /rest/v1/:tableName e /{tableName}) - EDGE-FIRST Architecture
// ALL security validations happen in Go memory via Dragonfly cache
// PostgreSQL only receives CLEAN, VALIDATED data
func (d *DataController) HandlePostgrest(w http.ResponseWriter, r *http.Request) {
	log.Printf("[HandlePostgrest] Method: %s, Path: %s, URL: %s", r.Method, r.URL.Path, r.URL.String())

	val := r.Context().Value(types.CascataCtxKey)
	if val == nil {
		log.Printf("[HandlePostgrest] ERROR: Context is nil")
		http.Error(w, `{"error":"Context Lost"}`, 500)
		return
	}
	cascataCtx := val.(*types.CascataRequest)
	if cascataCtx.Project == nil {
		http.Error(w, `{"error":"Project Required"}`, 400)
		return
	}

	// Extract table name
	tableName := chi.URLParam(r, "tableName")
	if tableName == "" {
		path := r.URL.Path
		parts := strings.Split(strings.Trim(path, "/"), "/")
		if len(parts) > 0 {
			tableName = parts[len(parts)-1]
		}
	}

	if tableName == "" || tableName == "rest" || tableName == "v1" {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error":"Table name required or invalid path"}`))
		return
	}

	// Get schema from query param or default to public
	schema := r.URL.Query().Get("schema")
	if schema == "" {
		schema = "public"
	}

	// Parse body for write methods
	var body map[string]interface{}
	if r.Method == "POST" || r.Method == "PATCH" || r.Method == "PUT" || r.Method == "DELETE" {
		json.NewDecoder(r.Body).Decode(&body)
	}
	d.ExtractAndValidateStepUp(r, cascataCtx, body, nil)

	// Determine operation type
	operation := "INSERT"
	if r.Method == "PATCH" || r.Method == "PUT" {
		operation = "UPDATE"
	} else if r.Method == "DELETE" {
		operation = "DELETE"
	} else if r.Method == "GET" {
		operation = "SELECT"
	}
	if !d.enforceTableStepUp(w, r, cascataCtx, tableName, operation) {
		return
	}

	// ============================================================
	// NEXUS ENGINE v0: PRE_PERSIST SEQUESTRO
	// ============================================================
	var responseData interface{}
	automationExecuted := false

	if d.NexusSvc != nil {
		userUUID := ""
		authSource := string(cascataCtx.UserRole)
		if cascataCtx.User != nil {
			if sub, ok := cascataCtx.User["sub"].(string); ok { userUUID = sub }
		}
		safeHeaders := nexus.ExtractSafeHeaders(r.Header)

		// Build payload
		payload := make(map[string]interface{})
		if body != nil {
			for k, v := range body { payload[k] = v }
		}
		payload["table"] = tableName
		payload["schema"] = schema
		payload["event"] = operation

		hookResult, err := d.NexusSvc.ResolvePrePersistForRoute(
			r.Context(), cascataCtx.Project.Slug, userUUID, string(cascataCtx.UserRole), authSource,
			fmt.Sprintf("/tables/%s", tableName), r.Method, payload, safeHeaders,
		)
		if err != nil {
			log.Printf("[HandlePostgrest:Nexus] PRE_PERSIST error: %v", err)
			http.Error(w, fmt.Sprintf(`{"error":"Nexus automation failed: %s"}`, err.Error()), 502)
			return
		}

		if hookResult.Intercepted {
			log.Printf("[HandlePostgrest:Nexus] PRE_PERSIST sequestro ativo — trace=%s", hookResult.TraceID)
			responseData = hookResult.ResponseData
			automationExecuted = true
		}
	}

	// ============================================================
	// If no Nexus automation intercepted, use DEFAULT behavior
	// ============================================================
	if !automationExecuted {
		// --- SECURITY GATEWAY ---
		if (r.Method == "POST" || r.Method == "PATCH" || r.Method == "PUT") && len(body) > 0 {
			gatewayResult, err := services.GlobalSchemaCache.EnforcePrePersistenceSecurity(
				r.Context(),
				cascataCtx.ProjectPool,
				&cascataCtx.Project.Metadata,
				cascataCtx.Project.Slug,
				schema,
				tableName,
				body,
				operation,
				d.ComputedSvc,
			)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), 400)
				return
			}
			body = gatewayResult.SanitizedData
			if len(gatewayResult.StrippedCols) > 0 {
				log.Printf("[SecurityGateway] Stripped locked columns: %v", gatewayResult.StrippedCols)
			}
		}

		// --- BUILD QUERY ---
		svc := &services.PostgrestService{}
		opts := &services.BuildQueryOptions{
			Ctx:    r.Context(),
			Pool:   cascataCtx.ProjectPool,
			Slug:   cascataCtx.Project.Slug,
			Schema: schema,
		}
		pgQuery, err := svc.BuildQuery(tableName, r.Method, r.URL.Query(), body, r.Header, opts)
		if err != nil {
			http.Error(w, `{"error":"`+err.Error()+`"}`, 400)
			return
		}

		// --- DATABASE EXECUTION ---
		userRole := string(cascataCtx.UserRole)
		if userRole == "" {
			userRole = "anon"
		}
		user := cascataCtx.User
		if user == nil {
			user = make(map[string]interface{})
		}

		result, err := d.ExecuteMultiStatementSQL(
			r.Context(),
			cascataCtx.ProjectPool,
			pgQuery.Text,
			pgQuery.Values,
			cascataCtx.Project.DbName,
			userRole,
			user,
		)
		if err != nil {
			http.Error(w, `{"error":"`+err.Error()+`"}`, 400)
			return
		}

		responseData = result

		// ============================================================
		// NEXUS ENGINE v0: POST_PERSIST SEQUESTRO (SYNC)
		// ============================================================
		if d.NexusSvc != nil {
			userUUID := ""
			authSource := string(cascataCtx.UserRole)
			if cascataCtx.User != nil {
				if sub, ok := cascataCtx.User["sub"].(string); ok { userUUID = sub }
			}
			safeHeaders := nexus.ExtractSafeHeaders(r.Header)

			origBody := body
			if origBody == nil { origBody = make(map[string]interface{}) }

			hookResult, err := d.NexusSvc.ResolvePostPersistForTableSync(
				r.Context(), cascataCtx.Project.Slug, userUUID, authSource,
				tableName, operation, responseData, origBody, safeHeaders,
			)
			if err == nil && hookResult.Intercepted {
				log.Printf("[HandlePostgrest:Nexus] POST_PERSIST sequestro ativo — trace=%s", hookResult.TraceID)
				responseData = hookResult.ResponseData
			} else {
				// Fallback to ASYNC if writing
				if r.Method == "POST" || r.Method == "PATCH" || r.Method == "PUT" {
					d.NexusSvc.DispatchPostPersistAsync(r.Context(), cascataCtx.Project.Slug, userUUID, string(cascataCtx.UserRole), authSource, tableName, operation, map[string]interface{}{"result": result}, origBody)
				}
			}
		}
	}

	log.Printf("[HandlePostgrest] END processing - returning response")

	// Apply Masking
	finalData := d.ApplyMaskingTier(cascataCtx, responseData, tableName)

	// Object JSON support
	if r.Header.Get("Accept") == "application/vnd.pgrst.object+json" {
		w.Header().Set("Content-Type", "application/json")
		if slice, ok := finalData.([]map[string]interface{}); ok && len(slice) > 0 {
			json.NewEncoder(w).Encode(slice[0])
		} else {
			w.Write([]byte("null"))
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(finalData)
}

// CreateTable (POST /tables)
func (d *DataController) CreateTable(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context().Value(types.CascataCtxKey).(*types.CascataRequest)
	if !ctx.IsSystemRequest { 
		http.Error(w, `{"error":"Only Dashboard can create tables."}`, 403)
		return 
	}

	var body struct {
		Name        string `json:"name"`
		Columns     []struct {
			Name          string      `json:"name"`
			Type          string      `json:"type"`
			PrimaryKey    bool        `json:"primaryKey"`
			Nullable      bool        `json:"nullable"`
			Default       string      `json:"default"`
			IsUnique      bool        `json:"isUnique"`
			ForeignKey    *struct {
				Table  string `json:"table"`
				Column string `json:"column"`
			} `json:"foreignKey"`
			Description   string      `json:"description"`
			FormatPattern string      `json:"formatPattern"`
		} `json:"columns"`
		Description string `json:"description"`
		RlsEnabled  bool   `json:"rls_enabled"`
	}
	json.NewDecoder(r.Body).Decode(&body)

	schema := r.URL.Query().Get("schema")
	if schema == "" { schema = "public" }

	// Build column definitions and collect comments for later application
	colDefs := []string{}
	hasCreatedAt := false
	hasUpdatedAt := false
	columnComments := []struct {
		colName string
		comment string
	}{}

	for _, c := range body.Columns {
		def := fmt.Sprintf("%s %s", utils.QuoteId(c.Name), c.Type)
		if c.PrimaryKey { def += " PRIMARY KEY" }
		if !c.Nullable && !c.PrimaryKey { def += " NOT NULL" }
		if c.Default != "" { def += " DEFAULT " + c.Default }
		if c.IsUnique { def += " UNIQUE" }
		if c.ForeignKey != nil {
			def += fmt.Sprintf(" REFERENCES %s(%s)", utils.QuoteId(c.ForeignKey.Table), utils.QuoteId(c.ForeignKey.Column))
		}
		colDefs = append(colDefs, def)

		// Build column comment (PostgreSQL requires separate COMMENT ON COLUMN)
		if c.FormatPattern != "" || c.Description != "" {
			comment := utils.BuildColumnComment(c.Description, c.FormatPattern)
			if comment != "" {
				columnComments = append(columnComments, struct {
					colName string
					comment string
				}{colName: c.Name, comment: comment})
			}
		}

		if c.Name == "created_at" { hasCreatedAt = true }
		if c.Name == "updated_at" { hasUpdatedAt = true }
	}

	sql := fmt.Sprintf("CREATE TABLE %s.%s (%s);", utils.QuoteId(schema), utils.QuoteId(body.Name), strings.Join(colDefs, ", "))
	
	tx, err := ctx.ProjectPool.Begin(r.Context())
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, 500)
		return
	}
	defer tx.Rollback(r.Context())

	if _, err := tx.Exec(r.Context(), sql); err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, 400)
		return
	}

	// TIER-3 PADLOCK: Temporal Integrity Triggers
	if hasCreatedAt || hasUpdatedAt {
		lockStatements := []string{}
		if hasCreatedAt { lockStatements = append(lockStatements, "NEW.created_at = OLD.created_at;") }
		if hasUpdatedAt { lockStatements = append(lockStatements, "NEW.updated_at = now();") }

		triggerFuncName := fmt.Sprintf("lock_temporal_state_%s", body.Name)
		triggerSql := fmt.Sprintf(`
			CREATE OR REPLACE FUNCTION %s.%s() RETURNS TRIGGER AS $$
			BEGIN %s RETURN NEW; END; $$ LANGUAGE plpgsql;
			CREATE TRIGGER ensure_temporal_integrity_%s BEFORE UPDATE ON %s.%s FOR EACH ROW EXECUTE FUNCTION %s.%s();
		`, utils.QuoteId(schema), utils.QuoteId(triggerFuncName), 
		   strings.Join(lockStatements, " "),
		   body.Name, utils.QuoteId(schema), utils.QuoteId(body.Name),
		   utils.QuoteId(schema), utils.QuoteId(triggerFuncName))
		
		tx.Exec(r.Context(), triggerSql)
	}

	// Security: Grant permissions to Cascata roles (REQUIRED for RLS to work)
	// These permissions allow anon/authenticated to access tables so RLS can then filter
	grantSQL := fmt.Sprintf(`
		GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE %s.%s TO anon, authenticated;
		GRANT ALL ON TABLE %s.%s TO service_role;
	`, utils.QuoteId(schema), utils.QuoteId(body.Name), utils.QuoteId(schema), utils.QuoteId(body.Name))
	tx.Exec(r.Context(), grantSQL)

	// Security: RLS Blindagem
	if body.RlsEnabled {
		tx.Exec(r.Context(), fmt.Sprintf("ALTER TABLE %s.%s ENABLE ROW LEVEL SECURITY", utils.QuoteId(schema), utils.QuoteId(body.Name)))
		tx.Exec(r.Context(), fmt.Sprintf("ALTER TABLE %s.%s FORCE ROW LEVEL SECURITY", utils.QuoteId(schema), utils.QuoteId(body.Name)))
		// Master Policy for God Mode - allows service_role to bypass RLS
		tx.Exec(r.Context(), fmt.Sprintf("CREATE POLICY master_system_policy ON %s.%s FOR ALL TO service_role USING (true) WITH CHECK (true)", utils.QuoteId(schema), utils.QuoteId(body.Name)))
	}

	// Apply column comments (PostgreSQL requires separate COMMENT ON COLUMN)
	for _, cc := range columnComments {
		commentSQL := fmt.Sprintf("COMMENT ON COLUMN %s.%s.%s IS %s",
			utils.QuoteId(schema),
			utils.QuoteId(body.Name),
			utils.QuoteId(cc.colName),
			utils.QuoteLiteral(cc.comment))
		if _, err := tx.Exec(r.Context(), commentSQL); err != nil {
			log.Printf("[CreateTable] Warning: failed to set comment for column %s: %v", cc.colName, err)
		}
	}

	if body.Description != "" {
		tx.Exec(r.Context(), fmt.Sprintf("COMMENT ON TABLE %s.%s IS $1", utils.QuoteId(schema), utils.QuoteId(body.Name)), body.Description)
	}

	tx.Commit(r.Context())

	// EDGE-FIRST: Invalidate schema cache for this table
	// The cache will be warmed on the first request to this table
	services.GlobalSchemaCache.InvalidateTable(ctx.Project.Slug, schema, body.Name)
	log.Printf("[CreateTable] Invalidated cache for %s.%s.%s", ctx.Project.Slug, schema, body.Name)

	w.WriteHeader(http.StatusCreated)
	w.Write([]byte(`{"success":true}`))
}

// --- ASSET MANAGEMENT ---

// GetNativeAssetParentId retorna o parent_id de um asset nativo da tabela de organização
func GetNativeAssetParentId(ctx context.Context, pool *pgxpool.Pool, projectSlug string, nativeId string) *string {
	if pool == nil {
		pool = services.SystemPool
	}
	// CRITICAL: Criar project_assets PRIMEIRO (tabela referenciada pela FK)
	// antes de native_asset_organization (tabela com FK)
	_, _ = pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS system.project_assets (
			id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
			project_slug TEXT NOT NULL,
			name TEXT NOT NULL,
			type TEXT NOT NULL DEFAULT 'folder',
			parent_id UUID REFERENCES system.project_assets(id),
			metadata JSONB DEFAULT '{}',
			created_at TIMESTAMPTZ DEFAULT now(),
			updated_at TIMESTAMPTZ DEFAULT now()
		)
	`)
	
	// Criar índices para project_assets (idempotent)
	_, _ = pool.Exec(ctx, `
		CREATE INDEX IF NOT EXISTS idx_project_assets_slug ON system.project_assets(project_slug);
		CREATE INDEX IF NOT EXISTS idx_project_assets_parent ON system.project_assets(parent_id)
	`)
	
	// Agora criar native_asset_organization (que depende de project_assets)
	_, _ = pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS system.native_asset_organization (
			id SERIAL PRIMARY KEY,
			project_slug TEXT NOT NULL,
			native_id TEXT NOT NULL,
			asset_type TEXT NOT NULL,
			parent_id UUID NULL REFERENCES system.project_assets(id) ON DELETE SET NULL,
			created_at TIMESTAMPTZ DEFAULT NOW(),
			updated_at TIMESTAMPTZ DEFAULT NOW(),
			UNIQUE(project_slug, native_id)
		)
	`)

	var parentId *string
	pool.QueryRow(ctx, 
		"SELECT parent_id FROM system.native_asset_organization WHERE project_slug = $1 AND native_id = $2",
		projectSlug, nativeId).Scan(&parentId)
	return parentId
}

func (d *DataController) GetAssets(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context().Value(types.CascataCtxKey).(*types.CascataRequest)
	
	// Check if system.project_assets table exists
	var tableExists bool
	err := ctx.ProjectPool.QueryRow(r.Context(), 
		"SELECT EXISTS (SELECT FROM information_schema.tables WHERE table_schema = 'system' AND table_name = 'project_assets')").Scan(&tableExists)
	if err != nil || !tableExists {
		// Return empty array if table doesn't exist (frontend compatibility)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("[]"))
		return
	}
	
	// Assets are stored in system.project_assets (Architecture Parity)
	rows, err := ctx.ProjectPool.Query(r.Context(), 
		"SELECT id, project_slug, name, type, parent_id, metadata, created_at, updated_at FROM system.project_assets WHERE project_slug = $1 ORDER BY created_at DESC", 
		ctx.Project.Slug)
	
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), 500)
		return
	}
	defer rows.Close()

	// Initialize to empty slice to avoid 'null' in JSON (Sinergia Synergy)
	assets := []map[string]interface{}{}
	fDesc := rows.FieldDescriptions()
	for rows.Next() {
		vals, _ := rows.Values()
		row := make(map[string]interface{})
		for i, fd := range fDesc { row[fd.Name] = utils.PurifyPgxValue(vals[i]) }
		assets = append(assets, row)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(assets)
}

func (d *DataController) UpsertAsset(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context().Value(types.CascataCtxKey).(*types.CascataRequest)
	var body struct {
		ID       string                 `json:"id"`
		Name     string                 `json:"name"`
		Type     string                 `json:"type"`
		ParentID string                 `json:"parent_id"`
		Metadata map[string]interface{} `json:"metadata"`
	}
	json.NewDecoder(r.Body).Decode(&body)

	metadataJSON := "{}"
	if body.Metadata != nil {
		if bytes, err := json.Marshal(body.Metadata); err == nil {
			metadataJSON = string(bytes)
		}
	}

	parentId := body.ParentID
	if parentId == "root" || parentId == "" { parentId = "" }

	// Validate UUID format - if ID is not a valid UUID, treat as new asset
	isValidUUID := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`).MatchString(body.ID)

	// Detectar assets nativos (ex: native_cron_13, native_rpc_xxx, native_trig_xxx)
	// Assets nativos vêm do banco de dados (pg_cron, pg_proc, pg_trigger) e não devem ter SQL executado
	isNativeAsset := strings.HasPrefix(body.ID, "native_")

	// Extrair SQL do metadata
	sql := ""
	if body.Metadata != nil {
		if sqlVal, ok := body.Metadata["sql"].(string); ok {
			sql = sqlVal
		}
	}

	// Determinar se é objeto gerenciado (rpc, trigger, cron)
	isManaged := isManagedObjectType(body.Type)
	objectName := body.Name

	// Se for objeto gerenciado e tiver SQL, extrair nome do objeto e executar no banco
	// MAS: assets nativos (do sistema) não devem ter SQL executado - eles já existem
	if isManaged && sql != "" && !isNativeAsset {
		extractedName := extractObjectName(sql, body.Type)
		if extractedName != "" {
			objectName = extractedName
		}

		// Se for update e o nome mudou, dropar o objeto antigo primeiro
		if body.ID != "" && isValidUUID {
			var oldName string
			ctx.ProjectPool.QueryRow(r.Context(),
				"SELECT name FROM system.project_assets WHERE id=$1 AND project_slug=$2",
				body.ID, ctx.Project.Slug).Scan(&oldName)

			if oldName != "" && oldName != objectName {
				dropSQL := generateDropSQL(body.Type, oldName)
				if dropSQL != "" {
					ctx.ProjectPool.Exec(r.Context(), dropSQL)
					log.Printf("[UpsertAsset] Dropped old %s: %s", body.Type, oldName)
				}
			}
		}

		// Para triggers: PostgreSQL não suporta CREATE OR REPLACE TRIGGER
		// Precisamos fazer DROP TRIGGER IF EXISTS antes
		if body.Type == "trigger" {
			triggerName, tableName := extractTriggerInfo(sql)
			if triggerName != "" && tableName != "" {
				dropSQL := fmt.Sprintf("DROP TRIGGER IF EXISTS %s ON %s;", triggerName, tableName)
				_, _ = ctx.ProjectPool.Exec(r.Context(), dropSQL)
				log.Printf("[UpsertAsset] Dropped trigger (if existed): %s on %s", triggerName, tableName)
			}
		}

		// Executar SQL no banco do projeto
		_, err := d.ExecuteMultiStatementSQL(
			r.Context(),
			ctx.ProjectPool,
			sql,
			nil,
			ctx.Project.DbName,
			"postgres",
			nil,
		)
		if err != nil {
			services.LogAudit(ctx.Project.Slug, "ASSET_SQL_FAILURE", "/assets", 400, ctx.IP, "system",
				map[string]interface{}{"error": err.Error(), "sql_preview": sql[:min(len(sql), 200)]})
			http.Error(w, fmt.Sprintf(`{"error":"SQL execution failed: %s"}`, err.Error()), 400)
			return
		}

		log.Printf("[UpsertAsset] Executed SQL for managed object: %s (type: %s)", objectName, body.Type)
	}

	// Para snippets (all, edge_function, folder), usar o nome fornecido pelo usuário
	// Para managed objects, usar o nome extraído do SQL
	assetName := objectName
	if !isManaged {
		assetName = body.Name
	}

	var resultAsset map[string]interface{}
	var err error

	// ASSETS NATIVOS: Apenas salvar organização (parent_id), não inserir na tabela de assets
	// Assets nativos (ex: native_cron_13) já existem no banco de dados do sistema
	if isNativeAsset {
		// CRITICAL: Garantir que project_assets existe antes (tabela referenciada pela FK)
		_, _ = ctx.ProjectPool.Exec(r.Context(), `
			CREATE TABLE IF NOT EXISTS system.project_assets (
				id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
				project_slug TEXT NOT NULL,
				name TEXT NOT NULL,
				type TEXT NOT NULL DEFAULT 'folder',
				parent_id UUID REFERENCES system.project_assets(id),
				metadata JSONB DEFAULT '{}',
				created_at TIMESTAMPTZ DEFAULT now(),
				updated_at TIMESTAMPTZ DEFAULT now()
			)
		`)
		
		// Criar índices para project_assets
		_, _ = ctx.ProjectPool.Exec(r.Context(), `
			CREATE INDEX IF NOT EXISTS idx_project_assets_slug ON system.project_assets(project_slug);
			CREATE INDEX IF NOT EXISTS idx_project_assets_parent ON system.project_assets(parent_id)
		`)
		
		// Agora criar tabela de organização de assets nativos (depende de project_assets)
		_, _ = ctx.ProjectPool.Exec(r.Context(), `
			CREATE TABLE IF NOT EXISTS system.native_asset_organization (
				id SERIAL PRIMARY KEY,
				project_slug TEXT NOT NULL,
				native_id TEXT NOT NULL,  -- ex: native_cron_13
				asset_type TEXT NOT NULL, -- rpc, trigger, cron
				parent_id UUID NULL REFERENCES system.project_assets(id) ON DELETE SET NULL,
				created_at TIMESTAMPTZ DEFAULT NOW(),
				updated_at TIMESTAMPTZ DEFAULT NOW(),
				UNIQUE(project_slug, native_id)
			)
		`)

		// Upsert na tabela de organização
		_, err = ctx.ProjectPool.Exec(r.Context(), `
			INSERT INTO system.native_asset_organization (project_slug, native_id, asset_type, parent_id)
			VALUES ($1, $2, $3, NULLIF($4, '')::uuid)
			ON CONFLICT (project_slug, native_id) 
			DO UPDATE SET parent_id = NULLIF($4, '')::uuid, updated_at = NOW()
		`, ctx.Project.Slug, body.ID, body.Type, parentId)

		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"Failed to save native asset organization: %s"}`, err.Error()), 500)
			return
		}

		// Retornar o asset como ele foi enviado (não criamos um novo ID)
		resultAsset = map[string]interface{}{
			"id":          body.ID,
			"project_slug": ctx.Project.Slug,
			"name":        body.Name,
			"type":        body.Type,
			"parent_id":   parentId,
			"metadata":    body.Metadata,
			"is_native":   true,
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resultAsset)
		return
	}

	if body.ID != "" && isValidUUID {
		// UPDATE - preservar o type existente se não foi enviado
		existingType := body.Type
		if existingType == "" {
			var dbType string
			ctx.ProjectPool.QueryRow(r.Context(),
				"SELECT type FROM system.project_assets WHERE id=$1 AND project_slug=$2",
				body.ID, ctx.Project.Slug).Scan(&dbType)
			existingType = dbType
		}

		_, err = ctx.ProjectPool.Exec(r.Context(),
			"UPDATE system.project_assets SET name=$1, type=$2, metadata=$3, parent_id=NULLIF($4, '')::uuid, updated_at=now() WHERE id=$5 AND project_slug=$6",
			assetName, existingType, metadataJSON, parentId, body.ID, ctx.Project.Slug)

		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), 500)
			return
		}

		// Buscar asset atualizado para retornar
		row := ctx.ProjectPool.QueryRow(r.Context(),
			"SELECT id, project_slug, name, type, parent_id, metadata, created_at, updated_at FROM system.project_assets WHERE id=$1 AND project_slug=$2",
			body.ID, ctx.Project.Slug)

		var id, projSlug, name, assetType string
		var parentID *string
		var metadata map[string]interface{}
		var createdAt, updatedAt interface{}
		err = row.Scan(&id, &projSlug, &name, &assetType, &parentID, &metadata, &createdAt, &updatedAt)
		if err != nil {
			http.Error(w, `{"error":"Failed to fetch updated asset"}`, 500)
			return
		}
		resultAsset = map[string]interface{}{
			"id": id, "project_slug": projSlug, "name": name, "type": assetType,
			"parent_id": parentID, "metadata": metadata,
			"created_at": createdAt, "updated_at": updatedAt,
		}
	} else {
		// INSERT
		row := ctx.ProjectPool.QueryRow(r.Context(),
			`INSERT INTO system.project_assets (project_slug, name, type, parent_id, metadata)
			 VALUES ($1, $2, $3, NULLIF($4, '')::uuid, $5)
			 RETURNING id, project_slug, name, type, parent_id, metadata, created_at, updated_at`,
			ctx.Project.Slug, assetName, body.Type, parentId, metadataJSON)

		var id, projSlug, name, assetType string
		var parentID *string
		var metadata map[string]interface{}
		var createdAt, updatedAt interface{}
		err = row.Scan(&id, &projSlug, &name, &assetType, &parentID, &metadata, &createdAt, &updatedAt)
		if err != nil {
			services.LogAudit(ctx.Project.Slug, "ASSET_FAILURE", "/assets", 500, ctx.IP, "system", map[string]interface{}{"error": err.Error()})
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), 500)
			return
		}
		resultAsset = map[string]interface{}{
			"id": id, "project_slug": projSlug, "name": name, "type": assetType,
			"parent_id": parentID, "metadata": metadata,
			"created_at": createdAt, "updated_at": updatedAt,
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resultAsset)
}

// DeleteTable (DELETE /tables/:tableName) - Soft Delete (Recycle Bin) OR Permanent Delete
func (d *DataController) DeleteTable(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context().Value(types.CascataCtxKey).(*types.CascataRequest)
	// Require either system request OR project service role (admin panel user)
	if !ctx.IsSystemRequest && ctx.UserRole != types.RoleService {
		http.Error(w, `{"error":"Unauthorized - requires system access or service role"}`, 403)
		return 
	}
	tableName := chi.URLParam(r, "tableName")
	schema := r.URL.Query().Get("schema")
	if schema == "" { schema = "public" }

	// Check if table is already in recycle bin (starts with _deleted_)
	// If so, perform permanent deletion (DROP TABLE)
	// Otherwise, perform soft delete (RENAME TO _deleted_...)
	isInRecycleBin := strings.HasPrefix(tableName, "_deleted_")

	if isInRecycleBin {
		// Permanent deletion - check for CASCADE mode
		var body struct {
			Mode string `json:"mode"` // Can be "CASCADE" or empty
		}
		json.NewDecoder(r.Body).Decode(&body)

		// Extrair nome original da tabela (sem prefixo _deleted_timestamp_)
		originalTableName := tableName
		if strings.HasPrefix(tableName, "_deleted_") {
			parts := strings.SplitN(tableName, "_", 4)
			if len(parts) >= 4 {
				originalTableName = parts[3]
			}
		}

		// ╔══════════════════════════════════════════════════════════════╗
		// ║  LIMPEZA DE RESÍDUOS - Executar ANTES do DROP TABLE          ║
		// ╚══════════════════════════════════════════════════════════════╝
		cleanupTx, err := ctx.ProjectPool.Begin(r.Context())
		if err == nil {
			// 1. Remover funções de trigger específicas da tabela
			triggerFuncs := []string{
				fmt.Sprintf("lock_temporal_state_%s", originalTableName),
				fmt.Sprintf("enforce_rls_%s", originalTableName),
			}
			for _, funcName := range triggerFuncs {
				_, _ = cleanupTx.Exec(r.Context(), 
					fmt.Sprintf("DROP FUNCTION IF EXISTS %s.%s()", utils.QuoteId(schema), utils.QuoteId(funcName)))
			}

			// 2. Remover registros de security locks para esta tabela
			_, _ = cleanupTx.Exec(r.Context(), 
				"DELETE FROM system.security_locks WHERE table_name = $1", originalTableName)

			// 3. Remover triggers de automação que possam existir
			automationTriggers := []string{
				fmt.Sprintf("automation_trigger_%s", originalTableName),
				fmt.Sprintf("%s_changes", originalTableName),
			}
			for _, triggerName := range automationTriggers {
				_, _ = cleanupTx.Exec(r.Context(), 
					fmt.Sprintf("DROP TRIGGER IF EXISTS %s ON %s.%s", 
						utils.QuoteId(triggerName), utils.QuoteId(schema), utils.QuoteId(tableName)))
			}

			_ = cleanupTx.Commit(r.Context())
		}

		cascadeStr := ""
		if body.Mode == "CASCADE" {
			cascadeStr = " CASCADE"
		}

		sql := fmt.Sprintf("DROP TABLE %s.%s%s", utils.QuoteId(schema), utils.QuoteId(tableName), cascadeStr)
		// Limpar prepared statements para evitar erros de cache após DDL
		_, _ = ctx.ProjectPool.Exec(r.Context(), "DEALLOCATE ALL")
		if _, err := ctx.ProjectPool.Exec(r.Context(), sql); err != nil {
			http.Error(w, `{"error":"`+err.Error()+`"}`, 500)
			return
		}
		w.Write([]byte(`{"success":true,"permanent":true}`))
	} else {
		// Soft delete - rename to _deleted_...
		deletedName := fmt.Sprintf("_deleted_%d_%s", time.Now().Unix(), tableName)
		sql := fmt.Sprintf("ALTER TABLE %s.%s RENAME TO %s", utils.QuoteId(schema), utils.QuoteId(tableName), utils.QuoteId(deletedName))
		// Limpar prepared statements para evitar erros de cache após DDL
		_, _ = ctx.ProjectPool.Exec(r.Context(), "DEALLOCATE ALL")
		if _, err := ctx.ProjectPool.Exec(r.Context(), sql); err != nil {
			http.Error(w, `{"error":"`+err.Error()+`"}`, 500)
			return
		}
		w.Write([]byte(`{"success":true,"soft":true,"deletedName":"`+deletedName+`"}`))
	}
}

func (d *DataController) RestoreTable(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context().Value(types.CascataCtxKey).(*types.CascataRequest)
	tableName := chi.URLParam(r, "table")
	schema := r.URL.Query().Get("schema")
	if schema == "" { schema = "public" }

	// Extract original name from _deleted_timestamp_name
	parts := strings.SplitN(tableName, "_", 4)
	originalName := tableName
	if len(parts) >= 4 { originalName = parts[3] }

	// Check if a table with the original name already exists
	// If so, generate a unique name with suffix
	targetName := originalName
	var exists bool
	for i := 1; i <= 100; i++ {
		err := ctx.ProjectPool.QueryRow(r.Context(),
			"SELECT EXISTS(SELECT 1 FROM information_schema.tables WHERE table_schema = $1 AND table_name = $2)",
			schema, targetName).Scan(&exists)
		if err != nil {
			http.Error(w, `{"error":"Failed to check table existence: `+err.Error()+`"}`, 500)
			return
		}
		if !exists {
			break
		}
		// Generate new name with suffix
		targetName = fmt.Sprintf("%s_restored_%d", originalName, i)
	}

	// If still exists after 100 attempts, return error
	if exists {
		http.Error(w, `{"error":"Too many restored versions exist. Please rename or delete existing tables first."}`, 409)
		return
	}

	sql := fmt.Sprintf("ALTER TABLE %s.%s RENAME TO %s", utils.QuoteId(schema), utils.QuoteId(tableName), utils.QuoteId(targetName))
	// Limpar prepared statements para evitar erros de cache após DDL
	_, _ = ctx.ProjectPool.Exec(r.Context(), "DEALLOCATE ALL")
	if _, err := ctx.ProjectPool.Exec(r.Context(), sql); err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, 500)
		return
	}

	response := map[string]interface{}{
		"success":       true,
		"restoredName":  targetName,
		"originalName":  originalName,
		"renamed":       targetName != originalName,
	}
	json.NewEncoder(w).Encode(response)
}

// --- EXTENSIONS (Phantom Injection) ---

// getExtensionCategory returns the category for an extension based on its name
func getExtensionCategory(name string) string {
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

// isFeaturedExtension checks if an extension should be highlighted as featured
func isFeaturedExtension(name string) bool {
	featured := map[string]bool{
		"vector": true, "postgis": true, "pgcrypto": true,
		"pg_trgm": true, "uuid-ossp": true, "pg_cron": true,
		"timescaledb": true,
	}
	return featured[name]
}

func (d *DataController) ListExtensions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context().Value(types.CascataCtxKey).(*types.CascataRequest)

	exts, err := d.ExtensionSvc.ListAvailableEnriched(r.Context(), ctx.ProjectPool, ctx.Project.Slug)
	if err != nil {
		log.Printf("[DataController] Failed to list extensions: %v", err)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]services.ExtensionInfo{})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(exts)
}

func (d *DataController) InstallExtension(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context().Value(types.CascataCtxKey).(*types.CascataRequest)
	var body struct {
		Name   string `json:"name"`
		Schema string `json:"schema"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	if body.Schema == "" {
		body.Schema = "public"
	}

	// Use ExtensionService for full phantom injection support
	result, err := d.ExtensionSvc.InstallExtension(r.Context(), ctx.ProjectPool, ctx.Project.Slug, body.Name, body.Schema)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), 400)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func (d *DataController) UninstallExtension(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context().Value(types.CascataCtxKey).(*types.CascataRequest)
	var body struct {
		Name    string `json:"name"`
		Cascade bool   `json:"cascade"`
	}
	json.NewDecoder(r.Body).Decode(&body)

	// Use ExtensionService for proper uninstall handling
	result, err := d.ExtensionSvc.UninstallExtension(r.Context(), ctx.ProjectPool, ctx.Project.Slug, body.Name, body.Cascade)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), 400)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// --- ENUM TYPES MANAGEMENT (PostgreSQL Native) ---

// ListEnumTypes - GET /api/data/{slug}/enum-types
func (d *DataController) ListEnumTypes(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context().Value(types.CascataCtxKey).(*types.CascataRequest)
	schemaFilter := r.URL.Query().Get("schema")

	pool := ctx.ProjectPool
	query := `
		SELECT 
			n.nspname as schema,
			t.typname as name,
			array_agg(e.enumlabel ORDER BY e.enumsortorder) as values
		FROM pg_type t
		JOIN pg_namespace n ON n.oid = t.typnamespace
		LEFT JOIN pg_enum e ON e.enumtypid = t.oid
		WHERE t.typtype = 'e'
		AND n.nspname NOT IN ('information_schema', 'pg_catalog')
		GROUP BY n.nspname, t.typname
		ORDER BY n.nspname, t.typname`

	if schemaFilter != "" {
		query = `
			SELECT 
				n.nspname as schema,
				t.typname as name,
				array_agg(e.enumlabel ORDER BY e.enumsortorder) as values
			FROM pg_type t
			JOIN pg_namespace n ON n.oid = t.typnamespace
			LEFT JOIN pg_enum e ON e.enumtypid = t.oid
			WHERE t.typtype = 'e'
			AND n.nspname = $1
			GROUP BY n.nspname, t.typname
			ORDER BY n.nspname, t.typname`
	}

	var rows pgx.Rows
	var err error
	if schemaFilter != "" {
		rows, err = pool.Query(r.Context(), query, schemaFilter)
	} else {
		rows, err = pool.Query(r.Context(), query)
	}
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"Failed to list ENUM types: %s"}`, err.Error()), 500)
		return
	}
	defer rows.Close()

	var enumTypes []map[string]interface{}
	for rows.Next() {
		var schema, name string
		var values []string
		if err := rows.Scan(&schema, &name, &values); err != nil {
			continue
		}
		enumTypes = append(enumTypes, map[string]interface{}{
			"schema": schema,
			"name":   name,
			"values": values,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(enumTypes)
}

// CreateEnumType - POST /api/data/{slug}/enum-types
func (d *DataController) CreateEnumType(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context().Value(types.CascataCtxKey).(*types.CascataRequest)
	
	var body struct {
		Name   string   `json:"name"`
		Schema string   `json:"schema"`
		Values []string `json:"values"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"Invalid request body"}`, 400)
		return
	}

	// Validate identifier
	validIdent := regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)
	if !validIdent.MatchString(body.Name) {
		http.Error(w, `{"error":"Invalid ENUM type name. Must be a valid PostgreSQL identifier."}`, 400)
		return
	}
	if body.Schema == "" {
		body.Schema = "public"
	}
	if !validIdent.MatchString(body.Schema) {
		http.Error(w, `{"error":"Invalid schema name"}`, 400)
		return
	}
	if len(body.Values) == 0 {
		http.Error(w, `{"error":"At least one ENUM value is required"}`, 400)
		return
	}

	pool := ctx.ProjectPool
	quotedSchema := fmt.Sprintf(`"%s"`, body.Schema)
	quotedName := fmt.Sprintf(`"%s"`, body.Name)

	// Build ENUM values
	var enumValues []string
	for _, v := range body.Values {
		escaped := strings.ReplaceAll(v, "'", "''")
		enumValues = append(enumValues, fmt.Sprintf("'%s'", escaped))
	}

	createQuery := fmt.Sprintf(`CREATE TYPE %s.%s AS ENUM (%s)`, quotedSchema, quotedName, strings.Join(enumValues, ", "))
	if _, err := pool.Exec(r.Context(), createQuery); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"Failed to create ENUM type: %s"}`, err.Error()), 400)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": fmt.Sprintf(`ENUM type "%s.%s" created successfully`, body.Schema, body.Name),
		"schema":  body.Schema,
		"name":    body.Name,
		"values":  body.Values,
	})
}

// UpdateEnumType - PATCH /api/data/{slug}/enum-types/{name}
func (d *DataController) UpdateEnumType(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context().Value(types.CascataCtxKey).(*types.CascataRequest)
	name := chi.URLParam(r, "name")
	
	var body struct {
		Schema string   `json:"schema"`
		Values []string `json:"values"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"Invalid request body"}`, 400)
		return
	}

	// Validate
	validIdent := regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)
	if !validIdent.MatchString(name) {
		http.Error(w, `{"error":"Invalid ENUM type name"}`, 400)
		return
	}
	if body.Schema == "" {
		body.Schema = "public"
	}
	if !validIdent.MatchString(body.Schema) {
		http.Error(w, `{"error":"Invalid schema name"}`, 400)
		return
	}

	pool := ctx.ProjectPool
	quotedSchema := fmt.Sprintf(`"%s"`, body.Schema)
	quotedName := fmt.Sprintf(`"%s"`, name)

	// Get current values
	var currentValues []string
	rows, err := pool.Query(r.Context(), `
		SELECT enumlabel 
		FROM pg_type t
		JOIN pg_namespace n ON n.oid = t.typnamespace
		JOIN pg_enum e ON e.enumtypid = t.oid
		WHERE t.typname = $1 AND n.nspname = $2
		ORDER BY e.enumsortorder`, name, body.Schema)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"Failed to get current ENUM values: %s"}`, err.Error()), 500)
		return
	}
	for rows.Next() {
		var val string
		if err := rows.Scan(&val); err == nil {
			currentValues = append(currentValues, val)
		}
	}
	rows.Close()

	// Find new values to add (PostgreSQL only allows adding, not removing)
	currentSet := make(map[string]bool)
	for _, v := range currentValues {
		currentSet[v] = true
	}
	var addedValues []string
	for _, v := range body.Values {
		if !currentSet[v] {
			addedValues = append(addedValues, v)
		}
	}

	// Add each new value
	for _, value := range addedValues {
		escapedValue := strings.ReplaceAll(value, "'", "''")
		var alterQuery string
		if len(currentValues) > 0 {
			lastValue := strings.ReplaceAll(currentValues[len(currentValues)-1], "'", "''")
			alterQuery = fmt.Sprintf(`ALTER TYPE %s.%s ADD VALUE '%s' AFTER '%s'`, quotedSchema, quotedName, escapedValue, lastValue)
		} else {
			alterQuery = fmt.Sprintf(`ALTER TYPE %s.%s ADD VALUE '%s'`, quotedSchema, quotedName, escapedValue)
		}
		if _, err := pool.Exec(r.Context(), alterQuery); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"Failed to add ENUM value '%s': %s"}`, value, err.Error()), 400)
			return
		}
		currentValues = append(currentValues, value)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": fmt.Sprintf(`ENUM type "%s.%s" updated successfully`, body.Schema, name),
		"schema":  body.Schema,
		"name":    name,
		"values":  currentValues,
		"added":   addedValues,
	})
}

// DeleteEnumType - DELETE /api/data/{slug}/enum-types/{name}?schema=public
func (d *DataController) DeleteEnumType(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context().Value(types.CascataCtxKey).(*types.CascataRequest)
	name := chi.URLParam(r, "name")
	schema := r.URL.Query().Get("schema")

	if schema == "" {
		schema = "public"
	}

	// Validate
	validIdent := regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)
	if !validIdent.MatchString(name) {
		http.Error(w, `{"error":"Invalid ENUM type name"}`, 400)
		return
	}
	if !validIdent.MatchString(schema) {
		http.Error(w, `{"error":"Invalid schema name"}`, 400)
		return
	}

	pool := ctx.ProjectPool
	quotedSchema := fmt.Sprintf(`"%s"`, schema)
	quotedName := fmt.Sprintf(`"%s"`, name)

	dropQuery := fmt.Sprintf(`DROP TYPE IF EXISTS %s.%s`, quotedSchema, quotedName)
	if _, err := pool.Exec(r.Context(), dropQuery); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"Failed to delete ENUM type: %s"}`, err.Error()), 400)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": fmt.Sprintf(`ENUM type "%s.%s" deleted successfully`, schema, name),
		"schema":  schema,
		"name":    name,
	})
}

// --- TELEMETRY & STATS ---

func (d *DataController) GetStats(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context().Value(types.CascataCtxKey).(*types.CascataRequest)
	
	var tablesCount, usersCount int
	dbSize := "0 KB"

	// 1. Table Count
	ctx.ProjectPool.QueryRow(r.Context(), "SELECT count(*) FROM information_schema.tables WHERE table_schema = 'public' AND table_name NOT LIKE '_deleted_%'").Scan(&tablesCount)
	// 2. User Count (Legacy Auth Support)
	ctx.ProjectPool.QueryRow(r.Context(), "SELECT count(*) FROM auth.users").Scan(&usersCount)
	// 3. DB Physical Size
	ctx.ProjectPool.QueryRow(r.Context(), "SELECT pg_size_pretty(pg_database_size(current_database()))").Scan(&dbSize)

	res := map[string]interface{}{
		"tables": tablesCount,
		"users": usersCount,
		"size": dbSize,
		"throughput": []interface{}{}, // Initialized to empty array
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)
}

// ExecuteRpc (POST /rpc/:name)
func (d *DataController) ExecuteRpc(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context().Value(types.CascataCtxKey).(*types.CascataRequest)
	name := chi.URLParam(r, "name")
	schema := r.URL.Query().Get("schema")
	if schema == "" { schema = "public" }

	var params map[string]interface{}
	json.NewDecoder(r.Body).Decode(&params)

	placeholders := []string{}
	values := []interface{}{}
	i := 1
	for k, v := range params {
		placeholders = append(placeholders, fmt.Sprintf("%s => $%d", utils.QuoteId(k), i))
		values = append(values, v)
		i++
	}

	sql := fmt.Sprintf("SELECT * FROM %s.%s(%s)", utils.QuoteId(schema), utils.QuoteId(name), strings.Join(placeholders, ", "))
	
	rows, err := ctx.ProjectPool.Query(r.Context(), sql, values...)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, 500)
		return
	}
	defer rows.Close()

	var result []map[string]interface{}
	fDesc := rows.FieldDescriptions()
	for rows.Next() {
		vals, _ := rows.Values()
		row := make(map[string]interface{})
		for i, fd := range fDesc { row[fd.Name] = utils.PurifyPgxValue(vals[i]) }
		result = append(result, row)
	}
	json.NewEncoder(w).Encode(result)
}

// ListFunctions (GET /functions) - Lista funções nativas do PostgreSQL com DDL
func (d *DataController) ListFunctions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context().Value(types.CascataCtxKey).(*types.CascataRequest)
	schema := r.URL.Query().Get("schema")
	if schema == "" { schema = "public" }

	// Return name + DDL using pg_get_functiondef
	rows, err := ctx.ProjectPool.Query(r.Context(), `
		SELECT 
			p.proname as name,
			pg_get_functiondef(p.oid) as ddl
		FROM pg_proc p
		JOIN pg_namespace n ON p.pronamespace = n.oid
		WHERE n.nspname = $1
		  AND p.proname NOT LIKE 'uuid_%'
		  AND p.proname NOT LIKE 'pgp_%'
		  AND p.proname NOT LIKE 'pg_%'
		ORDER BY p.proname
	`, schema)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, 500)
		return
	}
	defer rows.Close()

	functions := []map[string]interface{}{}
	for rows.Next() {
		var name, ddl string
		if err := rows.Scan(&name, &ddl); err == nil {
			// Build native_id and get parent_id from organization table
			nativeId := fmt.Sprintf("native_rpc_%s", name)
			parentId := GetNativeAssetParentId(r.Context(), ctx.ProjectPool, ctx.Project.Slug, nativeId)
			
			functions = append(functions, map[string]interface{}{
				"name":      name,
				"ddl":       ddl,
				"parent_id": parentId,
			})
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(functions)
}

// GetFunctionDefinition (GET /rpc/:name/definition) - Retorna definição da função
func (d *DataController) GetFunctionDefinition(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context().Value(types.CascataCtxKey).(*types.CascataRequest)
	name := chi.URLParam(r, "name")
	schema := r.URL.Query().Get("schema")
	if schema == "" { schema = "public" }

	// Buscar definição da função
	var definition string
	err := ctx.ProjectPool.QueryRow(r.Context(), `
		SELECT pg_get_functiondef(p.oid) 
		FROM pg_proc p 
		JOIN pg_namespace n ON p.pronamespace = n.oid 
		WHERE p.proname = $1 AND n.nspname = $2
	`, name, schema).Scan(&definition)
	if err != nil {
		http.Error(w, `{"error":"Function not found"}`, 404)
		return
	}

	// Buscar argumentos da função
	argRows, err := ctx.ProjectPool.Query(r.Context(), `
		SELECT parameter_name as name, data_type as type, parameter_mode as mode 
		FROM information_schema.parameters 
		WHERE specific_name = (
			SELECT specific_name 
			FROM information_schema.routines 
			WHERE routine_name = $1 AND routine_schema = $2 
			LIMIT 1
		) 
		ORDER BY ordinal_position ASC
	`, name, schema)
	if err != nil {
		argRows = nil
	}

	var args []map[string]string
	if argRows != nil {
		for argRows.Next() {
			var argName, argType, argMode string
			if err := argRows.Scan(&argName, &argType, &argMode); err == nil {
				args = append(args, map[string]string{
					"name": argName,
					"type": argType,
					"mode": argMode,
				})
			}
		}
		argRows.Close()
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"definition": definition,
		"args":       args,
	})
}

// RunRawQuery (POST /api/data/{slug}/query) - Administrative God Mode SQL Editor
func (d *DataController) RunRawQuery(w http.ResponseWriter, r *http.Request) {
	ctxVal := r.Context().Value(types.CascataCtxKey)
	if ctxVal == nil {
		log.Printf("[RunRawQuery] ERROR: CascataCtxKey not found in context")
		http.Error(w, `{"error":"Authentication context not found"}`, 401)
		return
	}
	ctx := ctxVal.(*types.CascataRequest)
	
	// DEBUG: Log detalhado para rastrear falhas de autorização
	log.Printf("[RunRawQuery] Auth Debug - Project: %s, UserRole: %s, IsSystemRequest: %v, IsDashboardAuth: %v", 
		ctx.Project.Slug, ctx.UserRole, ctx.IsSystemRequest, ctx.IsDashboardAuth)
	
	// Permitir acesso se tem Service Role ou é System Request ou é Dashboard Auth
	if ctx.UserRole != types.RoleService && !ctx.IsSystemRequest && !ctx.IsDashboardAuth {
		log.Printf("[RunRawQuery] ACCESS DENIED - UserRole: %s (needs RoleService), IsSystemRequest: %v, IsDashboardAuth: %v", 
			ctx.UserRole, ctx.IsSystemRequest, ctx.IsDashboardAuth)
		http.Error(w, `{"error":"Only Service Role can execute raw SQL"}`, 403)
		return 
	}
	
	log.Printf("[RunRawQuery] ACCESS GRANTED for project %s", ctx.Project.Slug)

	var body struct { SQL string `json:"sql"`; Params []interface{} `json:"params"` }
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"Invalid JSON"}`, 400)
		return
	}

	start := time.Now()

	// ============================================================
	// PG_CRON PROXY V2: Split statements first, then route individually
	// Statements with cron.* go to SystemPool, others to ProjectPool
	// ============================================================
	statements := splitSQLStatements(body.SQL)
	
	// Categorize statements
	var regularStatements []string
	var cronStatements []string
	hasCron := false
	
	for _, stmt := range statements {
		trimmed := strings.TrimSpace(stmt)
		if trimmed == "" {
			continue
		}
		
		// Check if this statement contains cron functions
		isCronStmt, transformedStmt := d.TransformCronSQL(trimmed, ctx.Project.DbName)
		if isCronStmt {
			hasCron = true
			cronStatements = append(cronStatements, transformedStmt)
		} else {
			regularStatements = append(regularStatements, trimmed)
		}
	}

	// AUDIT: Log do SQL executado no painel
	auditData := map[string]interface{}{
		"sql_preview":       body.SQL[:min(len(body.SQL), 200)],
		"sql_length":        len(body.SQL),
		"params_count":      len(body.Params),
		"source":            "dashboard_sql_editor",
		"statement_count":   len(statements),
		"regular_count":     len(regularStatements),
		"cron_count":        len(cronStatements),
	}
	if hasCron {
		auditData["pg_cron_proxy"] = true
		auditData["target_database"] = ctx.Project.DbName
	}
	services.LogSecurityEvent(
		ctx.Project.Slug,
		"raw_query",
		"",
		"EXECUTE",
		"ADMIN_SQL_QUERY",
		ctx.IP,
		string(ctx.UserRole),
		"info",
		auditData,
	)

	var lastResult []map[string]interface{}
	var lastErr error

	// Execute regular statements on ProjectPool (tenant database)
	if len(regularStatements) > 0 {
		regularSQL := strings.Join(regularStatements, ";\n")
		result, err := d.ExecuteMultiStatementSQL(
			r.Context(),
			ctx.ProjectPool,
			regularSQL,
			body.Params,
			ctx.Project.DbName,
			"postgres",
			nil,
		)
		if err != nil {
			lastErr = err
		} else {
			lastResult = result
			
			// Invalidate schema cache on DDL / Comment changes to avoid stale cache
			for _, stmt := range regularStatements {
				upper := strings.ToUpper(strings.TrimSpace(stmt))
				if strings.HasPrefix(upper, "COMMENT") || 
				   strings.HasPrefix(upper, "ALTER TABLE") || 
				   strings.HasPrefix(upper, "CREATE TABLE") || 
				   strings.HasPrefix(upper, "DROP TABLE") {
					log.Printf("[SchemaCache] DDL or Comment query detected, invalidating cache for project %s", ctx.Project.Slug)
					services.GlobalSchemaCache.InvalidateProject(ctx.Project.Slug)
					break
				}
			}
		}
	}

	// Execute cron statements on SystemPool (cascata_system)
	if lastErr == nil && len(cronStatements) > 0 {
		log.Printf("[RunRawQuery] PG_CRON PROXY: Executing %d cron statement(s) on system database for database '%s'", 
			len(cronStatements), ctx.Project.DbName)
		
		cronSQL := strings.Join(cronStatements, ";\n")
		result, err := d.ExecuteMultiStatementSQL(
			r.Context(),
			services.SystemPool,
			cronSQL,
			body.Params,
			config.SystemDatabaseName,
			"postgres",
			nil,
		)
		if err != nil {
			lastErr = err
		} else {
			// Cron result takes precedence if exists
			lastResult = result
		}
	}

	if lastErr != nil {
		http.Error(w, `{"error":"`+lastErr.Error()+`"}`, 400)
		return
	}

	resp := map[string]interface{}{
		"rows":     lastResult,
		"duration": time.Since(start).Milliseconds(),
	}
	json.NewEncoder(w).Encode(resp)
}

// GetColumns (GET /tables/:tableName/columns)
// EDGE-FIRST: Populates Dragonfly cache so subsequent validations are zero DB round-trip
func (d *DataController) GetColumns(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context().Value(types.CascataCtxKey).(*types.CascataRequest)
	tableName := chi.URLParam(r, "tableName")
	schema := r.URL.Query().Get("schema")
	if schema == "" { schema = "public" }

	query := `
		SELECT 
			c.column_name as name,
			CASE 
				WHEN c.data_type = 'USER-DEFINED' THEN c.udt_name
				WHEN c.data_type = 'ARRAY' THEN REPLACE(c.udt_name, '_', '') || '[]'
				ELSE c.data_type
			END as type,
			c.is_nullable, c.column_default,
			EXISTS (
				SELECT 1 FROM information_schema.key_column_usage kcu 
				WHERE kcu.table_name = $1 AND kcu.table_schema = $2 AND kcu.column_name = c.column_name
			) as is_primary_key,
			col_description(pgc.oid, c.ordinal_position) as comment
		FROM information_schema.columns c 
		JOIN pg_catalog.pg_class pgc ON pgc.relname = c.table_name AND pgc.relkind = 'r'
		JOIN pg_catalog.pg_namespace pgn ON pgn.oid = pgc.relnamespace AND pgn.nspname = c.table_schema
		WHERE c.table_schema = $2 AND c.table_name = $1
		ORDER BY c.ordinal_position`

	rows, err := ctx.ProjectPool.Query(r.Context(), query, tableName, schema)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, 500)
		return
	}
	defer rows.Close()

	// Universal Padlock Metadata
	locks := ctx.Project.Metadata.LockedColumns[tableName]
	masks := ctx.Project.Metadata.MaskedColumns[tableName]
	computed := ctx.Project.Metadata.ComputedColumns[tableName]

	// EDGE-FIRST: Get table schema from cache (auto-warms if cache miss)
	sc := services.GlobalSchemaCache
	tableSchema := sc.GetTableSchema(r.Context(), ctx.ProjectPool, &ctx.Project.Metadata, ctx.Project.Slug, schema, tableName)

	// CRITICAL: Initialize to empty slice to avoid 'null' in JSON
	cols := []map[string]interface{}{}
	for rows.Next() {
		var name, cType, nullable string
		var def *string
		var isPk bool
		var comment *string
		rows.Scan(&name, &cType, &nullable, &def, &isPk, &comment)
		
		// PRIORITY: Use lockLevel from cached schema (includes auto_clock from warmFromDatabase)
		// Fallback to LockedColumns from metadata for backward compatibility
		lock := "unlocked"
		methods := ""
		if meta, ok := tableSchema[name]; ok && meta != nil {
			if meta.LockLevel != "" {
				lock = meta.LockLevel
			}
			methods = meta.Methods
		}
		
		if lock == "unlocked" && locks != nil {
			if l, ok := locks[name]; ok && l != nil {
				if s, isStr := l.(string); isStr {
					lock = s
				} else if m, isMap := l.(map[string]interface{}); isMap {
					if lt, exists := m["lock_type"].(string); exists {
						lock = lt
					}
				}
			}
		}

		if methods == "" && locks != nil {
			if lVal, ok := locks[name]; ok && lVal != nil {
				if mapVal, isMap := lVal.(map[string]interface{}); isMap {
					if factorsRaw, has := mapVal["allowed_factors"]; has {
						var factors []string
						if arr, ok := factorsRaw.([]interface{}); ok {
							for _, item := range arr {
								if s, ok := item.(string); ok {
									factors = append(factors, services.FormatFactorName(s))
								}
							}
						} else if strArr, ok := factorsRaw.([]string); ok {
							for _, s := range strArr {
								factors = append(factors, services.FormatFactorName(s))
							}
						}
						methods = strings.Join(factors, ", ")
					}
				}
			}
		}
		
		mask := masks[name]
		if mask == "" { mask = "unmasked" }
		
		// Extract formula from ComputedColumnDef struct
		formula := ""
		returnType := ""
		strictMode := false
		if compDef, ok := computed[name]; ok {
			formula = compDef.Formula
			returnType = compDef.ReturnType
			strictMode = compDef.StrictMode
		}

		// Get cached metadata or parse from comment if available
		description := ""
		formatPattern := ""
		if meta, ok := tableSchema[name]; ok && meta != nil {
			description = meta.Description
			formatPattern = meta.FormatPattern
		}
		
		// Fallback or override if comment has values and cache is stale/empty
		if comment != nil && *comment != "" {
			descFromComment, patFromComment := utils.ParseColumnFormat(*comment)
			if description == "" {
				description = descFromComment
			}
			if formatPattern == "" {
				formatPattern = patFromComment
			}
		}

		cols = append(cols, map[string]interface{}{
			"name": name, "type": cType, "is_nullable": nullable == "YES", "defaultValue": def, "isPrimaryKey": isPk,
			"lockLevel": lock, "maskLevel": mask, "formula": formula, "returnType": returnType,
			"strictMode": strictMode,
			"description": description,
			"formatPattern": formatPattern,
			"methods": methods,
		})
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(cols)
}
// --- UI SETTINGS ---

func (d *DataController) GetUiSettings(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	table := chi.URLParam(r, "table")
	var settings map[string]interface{}
	err := services.SystemPool.QueryRow(r.Context(), "SELECT settings FROM system.ui_settings WHERE project_slug = $1 AND table_name = $2", slug, table).Scan(&settings)
	if err != nil { settings = map[string]interface{}{} }
	json.NewEncoder(w).Encode(settings)
}

func (d *DataController) SaveUiSettings(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	table := chi.URLParam(r, "table")
	var body struct { Settings map[string]interface{} `json:"settings"` }
	json.NewDecoder(r.Body).Decode(&body)
	
	_, err := services.SystemPool.Exec(r.Context(), 
		"INSERT INTO system.ui_settings (project_slug, table_name, settings) VALUES ($1, $2, $3) ON CONFLICT (project_slug, table_name) DO UPDATE SET settings = $3",
		slug, table, body.Settings)
	
	if err != nil { http.Error(w, `{"error":"`+err.Error()+`"}`, 500); return }
	w.Write([]byte(`{"success":true}`))
}

// --- METADATA ---

func (d *DataController) GetMetadata(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context().Value(types.CascataCtxKey).(*types.CascataRequest)
	
	metadata := map[string]interface{}{
		"ai_governance": map[string]interface{}{
			"mcp_enabled": ctx.Project.Metadata.AiGovernance.McpEnabled,
		},
		"locked_columns":   ctx.Project.Metadata.LockedColumns,
		"masked_columns":   ctx.Project.Metadata.MaskedColumns,
		"computed_columns": ctx.Project.Metadata.ComputedColumns,
		"table_security":   ctx.Project.Metadata.TableSecurity,
	}
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"metadata": metadata})
}

func (d *DataController) ListAutomations(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context().Value(types.CascataCtxKey).(*types.CascataRequest)
	
	log.Printf("[ListAutomations] ENTRY - Project: %s, IsSystemRequest: %v, IsDashboardAuth: %v, UserRole: %s", 
		ctx.Project.Slug, ctx.IsSystemRequest, ctx.IsDashboardAuth, ctx.UserRole)
	
	branchName := types.GetBranchName(r.Context())

	// Nexus v0: Busca na tabela canônica nexus_automations, mas mantém a assinatura legada para a UI
	query := `
		SELECT 
			id, 
			name, 
			description, 
			hook_type as trigger_type, 
			json_build_object('table', table_name, 'event', event_type) as trigger_config,
			COALESCE(graph_json->'nodes', '[]'::jsonb) as nodes, 
			COALESCE(graph_json->'edges', '[]'::jsonb) as edges,
			is_active, 
			status,
			COALESCE(graph_json->>'execution_mode', 'linear') as execution_mode,
			tenant_id as project_slug,
			created_at, 
			updated_at 
		FROM system.nexus_automations 
		WHERE tenant_id = $1 AND branch_name = $2
		ORDER BY created_at DESC
	`
	rows, err := services.SystemPool.Query(r.Context(), query, ctx.Project.Slug, branchName)
	
	if err != nil {
		log.Printf("[ListAutomations] QUERY FAILED - Project: %s, Error: %v", ctx.Project.Slug, err)
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), 500)
		return
	}
	
	defer rows.Close()
	
	aut := []map[string]interface{}{}
	fDesc := rows.FieldDescriptions()
	for rows.Next() { 
		vals, _ := rows.Values()
		row := make(map[string]interface{})
		for i, fd := range fDesc { row[fd.Name] = utils.PurifyPgxValue(vals[i]) }
		aut = append(aut, row)
	}
	
	log.Printf("[ListAutomations] SUCCESS - Project: %s, Count: %d", ctx.Project.Slug, len(aut))
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(aut)
}

func (d *DataController) ListTables(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context().Value(types.CascataCtxKey).(*types.CascataRequest)
	schema := r.URL.Query().Get("schema")
	if schema == "" { schema = "public" }

	query := `
		SELECT table_name as name, table_schema as schema 
		FROM information_schema.tables 
		WHERE table_schema = $1 
		AND table_type = 'BASE TABLE' 
		AND table_name NOT LIKE '\_deleted\_%'
		ORDER BY table_name
	`
	rows, err := ctx.ProjectPool.Query(r.Context(), query, schema)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, 500)
		return
	}
	defer rows.Close()

	// CRITICAL: Initialize to empty slice to avoid 'null' in frontend filter/map (Zero Regression)
	tables := []map[string]interface{}{}
	for rows.Next() {
		var name, sch string
		rows.Scan(&name, &sch)
		item := map[string]interface{}{"name": name, "schema": sch}
		if rule, ok := ctx.Project.Metadata.TableSecurity[name]; ok {
			item["methods"] = tableSecurityMethodList(rule.Operations)
			item["type"] = tableSecurityFactorList(normalizeTableSecurityFactors(rule.AllowedFactors))
		}
		tables = append(tables, item)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(tables)
}

func (d *DataController) GetSchemas(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context().Value(types.CascataCtxKey).(*types.CascataRequest)

	// TypeScript parity: Query all schemas including auth, extensions, etc.
	// Only exclude system schemas (information_schema, pg_catalog, pg_toast)
	rows, err := ctx.ProjectPool.Query(r.Context(), `
		SELECT schema_name as name 
		FROM information_schema.schemata 
		WHERE schema_name NOT IN ('information_schema', 'pg_catalog', 'pg_toast')
		  AND schema_name NOT LIKE 'pg_temp_%'
		  AND schema_name NOT LIKE 'pg_toast_temp_%'
		ORDER BY schema_name
	`)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, 500)
		return
	}
	defer rows.Close()

	schemas := []map[string]string{}
	for rows.Next() {
		var name string
		rows.Scan(&name)
		schemas = append(schemas, map[string]string{"name": name})
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(schemas)
}

func (d *DataController) ListTriggers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context().Value(types.CascataCtxKey).(*types.CascataRequest)
	
	// Return name + DDL using pg_get_triggerdef
	query := `
		SELECT 
			t.tgname as name,
			pg_get_triggerdef(t.oid, true) as ddl,
			c.relname as table_name,
			p.proname as function_name
		FROM pg_trigger t
		JOIN pg_class c ON c.oid = t.tgrelid
		JOIN pg_proc p ON p.oid = t.tgfoid
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname NOT IN ('pg_catalog', 'information_schema')
		  AND t.tgisinternal = false
		ORDER BY t.tgname
	`
	rows, err := ctx.ProjectPool.Query(r.Context(), query)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, 500)
		return
	}
	defer rows.Close()

	triggers := []map[string]interface{}{}
	for rows.Next() {
		var name, ddl, tableName, funcName string
		if err := rows.Scan(&name, &ddl, &tableName, &funcName); err == nil {
			// Build native_id and get parent_id from organization table
			nativeId := fmt.Sprintf("native_trig_%s", name)
			parentId := GetNativeAssetParentId(r.Context(), ctx.ProjectPool, ctx.Project.Slug, nativeId)
			
			triggers = append(triggers, map[string]interface{}{
				"name":          name,
				"ddl":           ddl,
				"table_name":    tableName,
				"function_name": funcName,
				"parent_id":     parentId,
			})
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(triggers)
}

// ListCronJobs (GET /cron-jobs) - Lista cron jobs do pg_cron para o tenant atual
// Busca do cascata_system onde database = tenant_db ou jobname termina com -tenant_db
func (d *DataController) ListCronJobs(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context().Value(types.CascataCtxKey).(*types.CascataRequest)
	targetDbName := ctx.Project.DbName
	
	// Query the cron.job table in cascata_system
	// Filter by database column OR jobname ending with -targetDbName
	query := `
		SELECT 
			jobid,
			jobname,
			schedule,
			command,
			database,
			active
		FROM cron.job
		WHERE database = $1
		   OR jobname LIKE '%-' || $1
		ORDER BY jobid
	`
	
	// Use SystemPool to query system database
	rows, err := services.SystemPool.Query(r.Context(), query, targetDbName)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, 500)
		return
	}
	defer rows.Close()

	cronJobs := []map[string]interface{}{}
	for rows.Next() {
		var jobid int
		var jobname, schedule, command, database string
		var active bool
		if err := rows.Scan(&jobid, &jobname, &schedule, &command, &database, &active); err == nil {
			// Build native_id and get parent_id from organization table
			nativeId := fmt.Sprintf("native_cron_%d", jobid)
			parentId := GetNativeAssetParentId(r.Context(), ctx.ProjectPool, ctx.Project.Slug, nativeId)
			
			cronJobs = append(cronJobs, map[string]interface{}{
				"jobid":     jobid,
				"jobname":   jobname,
				"schedule":  schedule,
				"command":   command,
				"database":  database,
				"active":    active,
				"parent_id": parentId,
			})
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(cronJobs)
}

func (d *DataController) GetTriggerDefinition(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context().Value(types.CascataCtxKey).(*types.CascataRequest)
	name := chi.URLParam(r, "name")
	
	var def string
	query := "SELECT pg_get_triggerdef(oid) FROM pg_trigger WHERE tgname = $1"
	err := ctx.ProjectPool.QueryRow(r.Context(), query, name).Scan(&def)
	if err != nil {
		http.Error(w, "Trigger not found", 404)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"definition": def})
}

func (d *DataController) ListRecycleBin(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context().Value(types.CascataCtxKey).(*types.CascataRequest)
	schema := r.URL.Query().Get("schema")
	if schema == "" { schema = "public" }

	query := `
		SELECT table_name as name 
		FROM information_schema.tables 
		WHERE table_schema = $1 
		AND table_name LIKE '\_deleted\_%'
		ORDER BY table_name
	`
	rows, err := ctx.ProjectPool.Query(r.Context(), query, schema)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, 500)
		return
	}
	defer rows.Close()

	tables := []map[string]string{}
	for rows.Next() {
		var name string
		rows.Scan(&name)
		tables = append(tables, map[string]string{"name": name})
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(tables)
}

func (d *DataController) HandleRealtime(w http.ResponseWriter, r *http.Request) {
	val := r.Context().Value(types.CascataCtxKey)
	if val == nil {
		http.Error(w, "Context Lost", 500)
		return
	}
	ctx := val.(*types.CascataRequest)
	
	env := r.URL.Query().Get("env")
	if env == "" { env = "live" }
	
	services.HandleRealtimeConnection(w, r, ctx.Project, env)
}

// ExecuteMultiStatementSQL splits and executes multiple SQL statements with RLS context.
// It returns results from the last SELECT statement, or empty if no SELECT.
// CRITICAL: This function now enforces RLS by setting the user role and JWT claims before execution.
func (d *DataController) ExecuteMultiStatementSQL(
	ctx context.Context, 
	pool *pgxpool.Pool, 
	sql string, 
	params []interface{}, 
	dbName string,
	userRole string,
	user map[string]interface{},
) ([]map[string]interface{}, error) {
	statements := splitSQLStatements(sql)
	
	if len(statements) == 0 {
		return []map[string]interface{}{}, nil
	}
	
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()
	
	tx, err := conn.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	
	// ============================================================
	// RLS SECURITY SETUP: Configure PostgreSQL role and JWT claims
	// This is CRITICAL for Row Level Security to work correctly.
	// ============================================================
	if userRole != "" && userRole != "postgres" {
		// Whitelist of allowed roles to prevent injection
		allowedRoles := map[string]bool{
			"anon": true, "authenticated": true, 
			"service_role": true, "cascata_api_role": true,
		}
		
		role := userRole
		if !allowedRoles[role] {
			role = "authenticated" // Default fallback
		}
		
		// Sanitize values for SET LOCAL
		quoteLocal := func(s interface{}) string {
			if s == nil {
				return "''"
			}
			str := fmt.Sprintf("%v", s)
			return "'" + strings.ReplaceAll(str, "'", "''") + "'"
		}
		
		// Extract StepUpProviders from context if available
		stepUpProviders := ""
		projectSlug := ""
		if val := ctx.Value(types.CascataCtxKey); val != nil {
			if cascataReq, ok := val.(*types.CascataRequest); ok {
				stepUpProviders = cascataReq.StepUpProviders
				if cascataReq.Project != nil {
					projectSlug = cascataReq.Project.Slug
				}
			}
		}

		// Build and execute RLS setup SQL
		setupSQL := fmt.Sprintf(`
			SET LOCAL ROLE %s;
			SET LOCAL statement_timeout = '30000';
			SET LOCAL "request.jwt.claim.sub" = %s;
			SET LOCAL "request.jwt.claim.role" = %s;
			SET LOCAL "request.jwt.claim.email" = %s;
			SET LOCAL "request.stepup.verified_providers" = %s;
			SET LOCAL "request.jwt.claim.project_slug" = %s;
			SET LOCAL "app.current_project_slug" = %s;
		`, 
			role,
			quoteLocal(user["sub"]),
			quoteLocal(role),
			quoteLocal(user["email"]),
			quoteLocal(stepUpProviders),
			quoteLocal(projectSlug),
			quoteLocal(projectSlug),
		)
		
		_, err = tx.Exec(ctx, setupSQL)
		if err != nil {
			return nil, fmt.Errorf("RLS setup failed: %w", err)
		}
	}
	
	var lastResult []map[string]interface{}
	
	// Detectar se há operações DDL e limpar prepared statements do PgBouncer
	// Isso evita erros como "cached plan must not change result type" e 
	// "prepared statement name is already in use" quando o schema muda
	upperSQL := strings.ToUpper(sql)
	isDDL := strings.Contains(upperSQL, "CREATE TABLE") || 
	         strings.Contains(upperSQL, "ALTER TABLE") || 
	         strings.Contains(upperSQL, "DROP TABLE") ||
	         strings.Contains(upperSQL, "DROP COLUMN") ||
	         strings.Contains(upperSQL, "ADD COLUMN") ||
	         strings.Contains(upperSQL, "RENAME COLUMN") ||
	         strings.Contains(upperSQL, "CREATE TRIGGER") ||
	         strings.Contains(upperSQL, "DROP TRIGGER") ||
	         strings.Contains(upperSQL, "CREATE FUNCTION") ||
	         strings.Contains(upperSQL, "DROP FUNCTION") ||
	         strings.Contains(upperSQL, "CREATE OR REPLACE FUNCTION")
	
	if isDDL {
		_, _ = tx.Exec(ctx, "DEALLOCATE ALL")
	}
	
	for i, stmt := range statements {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		
		upperStmt := strings.ToUpper(stmt)
		isStmtDDL := strings.Contains(upperStmt, "CREATE ") || 
		            strings.Contains(upperStmt, "ALTER ") || 
		            strings.Contains(upperStmt, "DROP ") ||
		            strings.Contains(upperStmt, "GRANT ") ||
		            strings.Contains(upperStmt, "REVOKE ")
		// Check if statement has positional parameters ($1, $2, etc)
		hasParams := strings.Contains(stmt, "$1") || 
		             (i == 0 && len(params) > 0)
		
		// DDL commands (CREATE, ALTER, DROP, etc) use Exec() - no params allowed
		// DML with params (INSERT $1, UPDATE $1) must use Query() with params
		// SELECT always uses Query()
		if isStmtDDL && !hasParams {
			_, err = tx.Exec(ctx, stmt)
			if err != nil {
				return nil, fmt.Errorf("statement %d failed: %w", i+1, err)
			}
			lastResult = []map[string]interface{}{}
		} else {
			var rows pgx.Rows
			if i == 0 && len(params) > 0 {
				rows, err = tx.Query(ctx, stmt, params...)
			} else {
				rows, err = tx.Query(ctx, stmt)
			}
			
			if err != nil {
				return nil, fmt.Errorf("statement %d failed: %w", i+1, err)
			}
			
			fieldDesc := rows.FieldDescriptions()
			if len(fieldDesc) > 0 {
				result := []map[string]interface{}{}
				for rows.Next() {
					vals, err := rows.Values()
					if err != nil {
						rows.Close()
						return nil, err
					}
					row := make(map[string]interface{})
					for j, fd := range fieldDesc {
						row[fd.Name] = utils.PurifyPgxValue(vals[j])
					}
					result = append(result, row)
				}
				
				if err := rows.Err(); err != nil {
					rows.Close()
					return nil, err
				}
				
				lastResult = result
			} else {
				lastResult = []map[string]interface{}{}
			}
			
			rows.Close()
		}
	}
	
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	
	// Check if any DDL was executed and trigger auto-grant AFTER commit
	// upperSQL já foi definido anteriormente para detectar DDL
	if strings.Contains(upperSQL, "CREATE") || strings.Contains(upperSQL, "ALTER") || strings.Contains(upperSQL, "DROP") {
		go d.triggerAutoGrant(ctx, pool, dbName)
	}
	
	return lastResult, nil
}

// splitSQLStatements splits SQL by semicolons, respecting string literals, comments, and dollar-quoted strings
func splitSQLStatements(sql string) []string {
	var statements []string
	var current strings.Builder
	inString := false
	stringChar := rune(0)
	inComment := false
	inDollarQuote := false
	dollarTag := ""
	skipCount := 0  // Skip characters already processed in end tag detection
	
	// Convert to runes once to avoid byte/rune index mismatch issues
	runes := []rune(sql)
	
	for i, ch := range runes {
		if skipCount > 0 {
			skipCount--
			continue
		}
		if inComment {
			if ch == '\n' {
				inComment = false
			}
			current.WriteRune(ch)
			continue
		}
		
		if inDollarQuote {
			current.WriteRune(ch)
			// Check for end of dollar-quoted string
			if ch == '$' {
				// Check for $$ (empty tag)
				if dollarTag == "" {
					if i+1 < len(runes) && runes[i+1] == '$' {
						inDollarQuote = false
						dollarTag = ""
						// Write the second $ and skip it in main loop
						current.WriteRune('$')
						skipCount = 1
					}
				} else {
					// Check for $tag$ - need to match the full end tag
					endTag := "$" + dollarTag + "$"
					if i+len(endTag) <= len(runes) {
						match := true
						for j := 0; j < len(endTag); j++ {
							if runes[i+j] != rune(endTag[j]) {
								match = false
								break
							}
						}
						if match {
							inDollarQuote = false
							dollarTag = ""
							// Write the rest of the end tag
							for j := 1; j < len(endTag); j++ {
								current.WriteRune(rune(endTag[j]))
							}
							// Skip the remaining characters of end tag in main loop
							skipCount = len(endTag) - 1
						}
					}
				}
			}
			continue
		}
		
		if inString {
			current.WriteRune(ch)
			if ch == stringChar {
				if i+1 < len(runes) && runes[i+1] == stringChar {
					continue
				}
				inString = false
			}
			continue
		}
		
		if ch == '-' && i+1 < len(runes) && runes[i+1] == '-' {
			inComment = true
			current.WriteRune(ch)
			continue
		}
		
		if ch == '\'' || ch == '"' {
			inString = true
			stringChar = ch
			current.WriteRune(ch)
			continue
		}
		
		// Check for dollar-quoted string start ($$ or $tag$)
		if ch == '$' {
			remaining := string(runes[i:])
			if strings.HasPrefix(remaining, "$$") {
				inDollarQuote = true
				dollarTag = ""
				// Write both $ characters and skip the second one in main loop
				current.WriteString("$$")
				skipCount = 1
				continue
			}
			// Check for $tag$ pattern
			if i+1 < len(runes) {
				nextChars := runes[i+1:]
				j := 0
				for j < len(nextChars) && nextChars[j] != '$' {
					j++
				}
				if j > 0 && j < len(nextChars) && nextChars[j] == '$' {
					inDollarQuote = true
					dollarTag = string(nextChars[:j])
					// Write the complete delimiter: $ + tag + $
					current.WriteRune('$')
					current.WriteString(dollarTag)
					current.WriteRune('$')
					// Skip the tag characters + closing $ in main loop
					skipCount = j + 1
					continue
				}
			}
		}
		
		if ch == ';' {
			stmt := strings.TrimSpace(current.String())
			if stmt != "" {
				statements = append(statements, stmt)
			}
			current.Reset()
			continue
		}
		
		current.WriteRune(ch)
	}
	
	stmt := strings.TrimSpace(current.String())
	if stmt != "" {
		statements = append(statements, stmt)
	}
	
	return statements
}

// triggerAutoGrant runs the auto-grant pipeline for DDL operations
func (d *DataController) triggerAutoGrant(ctx context.Context, pool *pgxpool.Pool, dbName string) {
	grantSql := `
		DO $$ 
		DECLARE s TEXT; t TEXT; 
		BEGIN
			FOR s IN SELECT schema_name FROM information_schema.schemata
				WHERE schema_name NOT IN ('information_schema', 'pg_catalog', 'pg_toast')
				AND schema_name NOT LIKE 'pg_temp_%' AND schema_name NOT LIKE 'pg_toast_temp_%'
			LOOP
				EXECUTE format('GRANT USAGE ON SCHEMA %I TO anon, authenticated, service_role, cascata_api_role', s);
				EXECUTE format('GRANT ALL ON ALL TABLES IN SCHEMA %I TO service_role, cascata_api_role', s);
				EXECUTE format('GRANT ALL ON ALL SEQUENCES IN SCHEMA %I TO service_role, cascata_api_role', s);
				EXECUTE format('GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA %I TO anon, authenticated', s);
				EXECUTE format('GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA %I TO anon, authenticated', s);
				
				FOR t IN SELECT tablename FROM pg_tables WHERE schemaname = s LOOP
					DECLARE is_rls_active BOOLEAN;
					BEGIN
						SELECT relrowsecurity INTO is_rls_active FROM pg_class 
						JOIN pg_namespace ON pg_namespace.oid = pg_class.relnamespace 
						WHERE nspname = s AND relname = t;

						IF is_rls_active THEN
							EXECUTE format('ALTER TABLE %I.%I FORCE ROW LEVEL SECURITY', s, t);
							IF NOT EXISTS (SELECT 1 FROM pg_policies WHERE schemaname = s AND tablename = t AND policyname = 'master_system_policy') THEN
								EXECUTE format('CREATE POLICY master_system_policy ON %I.%I FOR ALL TO service_role, current_user USING (true) WITH CHECK (true)', s, t);
							END IF;
						END IF;
					END;
				END LOOP;
			END LOOP;
		END $$;
	`
	pool.Exec(context.Background(), grantSql)
	
	// Reload pool to clear cached plans after DDL
	if dbName != "" {
		services.Reload(dbName)
	}
}

// DeleteAsset removes an asset
// Para objetos gerenciados (rpc, trigger, cron), faz DROP no banco primeiro
func (d *DataController) DeleteAsset(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context().Value(types.CascataCtxKey).(*types.CascataRequest)
	id := chi.URLParam(r, "id")

	// Validate UUID format
	if !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`).MatchString(id) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]bool{"success": true})
		return
	}

	// Buscar informações do asset antes de deletar
	var assetType, assetName string
	err := ctx.ProjectPool.QueryRow(r.Context(),
		"SELECT type, name FROM system.project_assets WHERE id=$1 AND project_slug=$2",
		id, ctx.Project.Slug).Scan(&assetType, &assetName)

	if err != nil {
		// Asset não encontrado ou erro - tenta deletar mesmo assim
		_, _ = ctx.ProjectPool.Exec(r.Context(),
			"DELETE FROM system.project_assets WHERE id=$1 AND project_slug=$2", id, ctx.Project.Slug)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]bool{"success": true})
		return
	}

	// Se for objeto gerenciado, fazer DROP no banco primeiro
	if isManagedObjectType(assetType) {
		dropSQL := generateDropSQL(assetType, assetName)
		if dropSQL != "" {
			// Tentar dropar - não falhar se não existir
			_, dropErr := ctx.ProjectPool.Exec(r.Context(), dropSQL)
			if dropErr != nil {
				log.Printf("[DeleteAsset] Warning: Failed to DROP %s %s: %v", assetType, assetName, dropErr)
			} else {
				log.Printf("[DeleteAsset] Dropped %s: %s", assetType, assetName)
			}
		}
	}

	// Deletar o asset
	_, err = ctx.ProjectPool.Exec(r.Context(),
		"DELETE FROM system.project_assets WHERE id=$1 AND project_slug=$2", id, ctx.Project.Slug)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), 500)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// GetAssetHistory returns history of asset changes
func (d *DataController) GetAssetHistory(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context().Value(types.CascataCtxKey).(*types.CascataRequest)
	id := chi.URLParam(r, "id")

	rows, err := ctx.ProjectPool.Query(r.Context(), `
		SELECT h.id, h.created_at, h.created_by, h.metadata 
		FROM system.asset_history h
		INNER JOIN system.project_assets a ON a.id = h.asset_id
		WHERE h.asset_id = $1 AND a.project_slug = $2
		ORDER BY h.created_at DESC LIMIT 50`, id, ctx.Project.Slug)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]map[string]interface{}{})
		return
	}
	defer rows.Close()

	history := []map[string]interface{}{}
	fDesc := rows.FieldDescriptions()
	for rows.Next() {
		vals, _ := rows.Values()
		row := make(map[string]interface{})
		for i, fd := range fDesc { row[fd.Name] = utils.PurifyPgxValue(vals[i]) }
		history = append(history, row)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(history)
}

// UpsertAutomation creates or updates an automation
func (d *DataController) UpsertAutomation(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context().Value(types.CascataCtxKey).(*types.CascataRequest)
	
	log.Printf("[UpsertAutomation] ENTRY - Project: %s, IsSystemRequest: %v, IsDashboardAuth: %v, UserRole: %s", 
		ctx.Project.Slug, ctx.IsSystemRequest, ctx.IsDashboardAuth, ctx.UserRole)
	
	if !ctx.IsSystemRequest && !ctx.IsDashboardAuth && ctx.UserRole != types.RoleService {
		log.Printf("[UpsertAutomation] ACCESS DENIED - Project: %s, IsSystemRequest: %v, IsDashboardAuth: %v, UserRole: %s", 
			ctx.Project.Slug, ctx.IsSystemRequest, ctx.IsDashboardAuth, ctx.UserRole)
		http.Error(w, `{"error":"Unauthorized"}`, 403)
		return
	}
	
	log.Printf("[UpsertAutomation] ACCESS GRANTED - Project: %s, Role: %s", ctx.Project.Slug, ctx.UserRole)
	
	var body struct {
		ID            string          `json:"id"`
		Name          string          `json:"name"`
		Description   string          `json:"description"`
		TriggerType   string          `json:"trigger_type"`
		TriggerConfig json.RawMessage `json:"trigger_config"`
		Nodes         json.RawMessage `json:"nodes"`
		Edges         json.RawMessage `json:"edges"`
		IsActive      *bool           `json:"is_active"`
		Status        string          `json:"status"`
		ExecutionMode string          `json:"execution_mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		log.Printf("[UpsertAutomation] ERROR parsing body - Project: %s, Error: %v", ctx.Project.Slug, err)
		http.Error(w, `{"error":"Invalid JSON body"}`, 400)
		return
	}
	
	// Normalizar trigger_config: aceita objeto ou string JSON
	var triggerConfig interface{}
	if len(body.TriggerConfig) > 0 {
		// Tentar parse como objeto primeiro
		var objConfig map[string]interface{}
		if err := json.Unmarshal(body.TriggerConfig, &objConfig); err == nil {
			triggerConfig = objConfig
		} else {
			// Tentar como string JSON (frontend enviou stringified)
			var strConfig string
			if err := json.Unmarshal(body.TriggerConfig, &strConfig); err == nil {
				// strConfig contém o JSON stringified, fazer unmarshal novamente
				if err := json.Unmarshal([]byte(strConfig), &objConfig); err == nil {
					triggerConfig = objConfig
				} else {
					triggerConfig = map[string]interface{}{}
				}
			} else {
				triggerConfig = map[string]interface{}{}
			}
		}
	} else {
		triggerConfig = map[string]interface{}{}
	}
	
	// Normalizar nodes: aceita array ou string JSON
	var nodes interface{}
	if len(body.Nodes) > 0 {
		// Tentar parse como array primeiro
		var arrNodes []interface{}
		if err := json.Unmarshal(body.Nodes, &arrNodes); err == nil {
			nodes = arrNodes
		} else {
			// Tentar como string JSON
			var strNodes string
			if err := json.Unmarshal(body.Nodes, &strNodes); err == nil {
				if err := json.Unmarshal([]byte(strNodes), &arrNodes); err == nil {
					nodes = arrNodes
				} else {
					nodes = []interface{}{}
				}
			} else {
				nodes = []interface{}{}
			}
		}
	} else {
		nodes = []interface{}{}
	}

	var edges interface{} = []interface{}{}
	if len(body.Edges) > 0 {
		var arrEdges []interface{}
		if err := json.Unmarshal(body.Edges, &arrEdges); err == nil {
			edges = arrEdges
		}
	}
	
	log.Printf("[UpsertAutomation] BODY - Project: %s, ID: %s, Name: %s, TriggerType: %s, HasNodes: %v",
		ctx.Project.Slug, body.ID, body.Name, body.TriggerType, len(body.Nodes) > 0)
	log.Printf("[UpsertAutomation] BODY DETAILS - trigger_config: %s, nodes: %s", string(body.TriggerConfig), string(body.Nodes))
	
	branchName := types.GetBranchName(r.Context())

	status := body.Status
	if status == "" {
		if body.ID != "" {
			_ = services.SystemPool.QueryRow(r.Context(),
				`SELECT status FROM system.nexus_automations WHERE id=$1 AND tenant_id=$2 AND branch_name=$3`,
				body.ID, ctx.Project.Slug, branchName).Scan(&status)
			if status == "" {
				status = "draft"
			}
		} else {
			status = "draft"
		}
	}

	// LIFECYCLE SYNC: se frontend enviou is_active=true, promove status para 'active'.
	// Se enviou is_active=false, rebaixa para 'draft'.
	if body.IsActive != nil {
		if *body.IsActive {
			status = "active"
		} else if status == "active" {
			status = "draft"
		}
	}
	isActive := status == "active"

	// Gerar UUID estável ANTES de construir graphObj para que graph_json.id nunca seja vazio
	var preGeneratedID string
	if body.ID != "" {
		preGeneratedID = body.ID
	} else {
		preGeneratedID = uuid.New().String()
	}

	var id string
	var createdAt, updatedAt time.Time
	
	// Extração de metadados para o novo schema Nexus v0
	tableName := "*"
	eventType := "ANY"
	routePattern := "*"
	httpMethod := "ANY"
	hookType := "PRE_PERSIST" // Default para sequestro
	
	if tc, ok := triggerConfig.(map[string]interface{}); ok {
		if t, _ := tc["table"].(string); t != "" { tableName = t }
		if e, _ := tc["event"].(string); e != "" { 
			eventType = strings.ToUpper(strings.TrimSpace(e))
			if eventType == "*" {
				eventType = "ANY"
			}
		}
	}

	if nodeList, ok := nodes.([]interface{}); ok {
		for _, rawNode := range nodeList {
			nodeMap, ok := rawNode.(map[string]interface{})
			if !ok {
				continue
			}
			nodeID, _ := nodeMap["node_id"].(string)
			if nodeID == "" {
				nodeID, _ = nodeMap["nodeId"].(string)
			}
			if nodeID != "pre_event_trigger" && nodeID != "post_event_trigger" && nodeID != "webhook_trigger" && nodeID != "cron_trigger" {
				continue
			}
			if cfg, ok := nodeMap["config"].(map[string]interface{}); ok {
				if t, _ := cfg["table"].(string); t != "" {
					tableName = t
				}
				if e, _ := cfg["event"].(string); e != "" {
					eventType = strings.ToUpper(strings.TrimSpace(e))
					if eventType == "*" {
						eventType = "ANY"
					}
				}
				// PADRONIZAÇÃO DE SEQUESTRO (Sinergia)
				// Extração do path_slug para Webhooks para alimentar a coluna route_pattern
				if ps, ok := cfg["path_slug"].(string); ok && ps != "" {
					routePattern = ps
				}
				if m, ok := cfg["method"].(string); ok && m != "" {
					httpMethod = strings.ToUpper(m)
				}
			}
			switch nodeID {
			case "pre_event_trigger":
				hookType = "PRE_PERSIST"
				body.TriggerType = "API_INTERCEPT"
			case "post_event_trigger":
				hookType = "POST_PERSIST"
				body.TriggerType = "POST_PERSIST"
			case "webhook_trigger":
				hookType = "WEBHOOK"
				body.TriggerType = "WEBHOOK"
			case "cron_trigger":
				hookType = "CRON"
				body.TriggerType = "CRON"
			}
			break
		}
	}
	
	// Mapeamento de TriggerType legada para HookType Nexus
	switch body.TriggerType {
	case "API_INTERCEPT": hookType = "PRE_PERSIST"
	case "POST_PERSIST": hookType = "POST_PERSIST"
	case "WEBHOOK":      hookType = "WEBHOOK"
	case "CRON":         hookType = "CRON"
	}

	// Construir GraphJSON completo — usa preGeneratedID garantindo ID não-vazio
	graphObj := map[string]interface{}{
		"id":             preGeneratedID,
		"nodes":          nodes,
		"edges":          edges,
		"version":        1,
		"mode":           "fast_lane",
		"execution_mode": body.ExecutionMode,
	}
	graphJSON, _ := json.Marshal(graphObj)

	if body.ID != "" {
		// Update
		log.Printf("[UpsertAutomation] UPDATE Nexus mode - Project: %s, ID: %s", ctx.Project.Slug, body.ID)
		err := services.SystemPool.QueryRow(r.Context(), `
			UPDATE system.nexus_automations 
			SET name=$1, description=$2, hook_type=$3, table_name=$4, event_type=$5, graph_json=$6, is_active=$7, status=$8, execution_mode=$9, route_pattern=$10, method=$11, updated_at=NOW()
			WHERE id=$12 AND tenant_id=$13 AND branch_name=$14
			RETURNING id, created_at, updated_at`,
			body.Name, body.Description, hookType, tableName, eventType, graphJSON, isActive, status, dbExecutionMode(body.ExecutionMode), routePattern, httpMethod, body.ID, ctx.Project.Slug, branchName).Scan(&id, &createdAt, &updatedAt)
		if err != nil {
			log.Printf("[UpsertAutomation] UPDATE FAILED - Project: %s, ID: %s, Error: %v", ctx.Project.Slug, body.ID, err)
			http.Error(w, `{"error":"Automation not found in nexus_automations"}`, 404)
			return
		}
		log.Printf("[UpsertAutomation] UPDATE SUCCESS - Project: %s, ID: %s", ctx.Project.Slug, body.ID)
		id = body.ID
	} else {
		// Insert — usa o preGeneratedID para garantir consistência com graph_json.id
		log.Printf("[UpsertAutomation] INSERT Nexus mode - Project: %s, Name: %s, PreID: %s", ctx.Project.Slug, body.Name, preGeneratedID)
		err := services.SystemPool.QueryRow(r.Context(), `
			INSERT INTO system.nexus_automations (id, tenant_id, branch_name, name, description, hook_type, table_name, event_type, graph_json, is_active, status, execution_mode, route_pattern, method)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
			RETURNING id, created_at, updated_at`,
			preGeneratedID, ctx.Project.Slug, branchName, body.Name, body.Description, hookType, tableName, eventType, graphJSON, isActive, status, dbExecutionMode(body.ExecutionMode), routePattern, httpMethod).Scan(&id, &createdAt, &updatedAt)
		if err != nil {
			log.Printf("[UpsertAutomation] INSERT FAILED - Project: %s, Name: %s, Error: %v", ctx.Project.Slug, body.Name, err)
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), 500)
			return
		}
		log.Printf("[UpsertAutomation] INSERT SUCCESS - Project: %s, Name: %s, ID: %s", ctx.Project.Slug, body.Name, id)
	}

	// INVALIDAR CACHE da automação para refletir mudanças imediatamente
	if d.NexusSvc != nil {
		log.Printf("[UpsertAutomation] INVALIDATING NEXUS CACHE - Project: %s", ctx.Project.Slug)
		d.NexusSvc.InvalidateCache(r.Context(), ctx.Project.Slug)
	}

	// Construir resposta com os dados enviados + IDs/timestamps
	result := map[string]interface{}{
		"id":              id,
		"project_slug":    ctx.Project.Slug,
		"name":            body.Name,
		"description":     body.Description,
		"trigger_type":    body.TriggerType,
		"trigger_config":  triggerConfig,
		"nodes":           nodes,
		"is_active":       isActive,
		"status":          status,
		"execution_mode":  body.ExecutionMode,
		"created_at":      createdAt,
		"updated_at":      updatedAt,
	}
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// DeleteAutomation removes an automation
func (d *DataController) DeleteAutomation(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context().Value(types.CascataCtxKey).(*types.CascataRequest)
	
	log.Printf("[DeleteAutomation] ENTRY - Project: %s, IsSystemRequest: %v, IsDashboardAuth: %v, UserRole: %s", 
		ctx.Project.Slug, ctx.IsSystemRequest, ctx.IsDashboardAuth, ctx.UserRole)
	
	if !ctx.IsSystemRequest && !ctx.IsDashboardAuth && ctx.UserRole != types.RoleService {
		log.Printf("[DeleteAutomation] ACCESS DENIED - Project: %s, IsSystemRequest: %v, IsDashboardAuth: %v, UserRole: %s", 
			ctx.Project.Slug, ctx.IsSystemRequest, ctx.IsDashboardAuth, ctx.UserRole)
		http.Error(w, `{"error":"Unauthorized"}`, 403)
		return
	}
	
	log.Printf("[DeleteAutomation] ACCESS GRANTED - Project: %s, Role: %s", ctx.Project.Slug, ctx.UserRole)
	
	id := chi.URLParam(r, "id")
	log.Printf("[DeleteAutomation] DELETE - Project: %s, ID: %s", ctx.Project.Slug, id)
	
	branchName := types.GetBranchName(r.Context())

	// Nexus v0: Deletar da tabela canônica
	result, err := services.SystemPool.Exec(r.Context(),
		"DELETE FROM system.nexus_automations WHERE id=$1 AND tenant_id=$2 AND branch_name=$3", id, ctx.Project.Slug, branchName)
	if err != nil {
		log.Printf("[DeleteAutomation] DELETE FAILED - Project: %s, ID: %s, Error: %v", ctx.Project.Slug, id, err)
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), 500)
		return
	}
	
	rowsAffected := result.RowsAffected()
	
	// Segunda tentativa: se não achou, pode ser que tenha project_slug diferente (migração antiga)
	if rowsAffected == 0 {
		// Verificar se existe com qualquer project_slug
		var existingSlug string
		errCheck := services.SystemPool.QueryRow(r.Context(),
			"SELECT project_slug FROM system.automations WHERE id=$1", id).Scan(&existingSlug)
		
		if errCheck == nil {
			// Encontrou com outro project_slug, deletar mesmo assim
			log.Printf("[DeleteAutomation] FOUND with different slug - Project: %s, ID: %s, ActualSlug: %s", 
				ctx.Project.Slug, id, existingSlug)
			result, err = services.SystemPool.Exec(r.Context(),
				"DELETE FROM system.automations WHERE id=$1", id)
			if err != nil {
				log.Printf("[DeleteAutomation] DELETE FAILED (fallback) - Project: %s, ID: %s, Error: %v", ctx.Project.Slug, id, err)
				http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), 500)
				return
			}
			rowsAffected = result.RowsAffected()
		} else {
			log.Printf("[DeleteAutomation] DELETE FAILED - Project: %s, ID: %s, Error: Automation not found", ctx.Project.Slug, id)
			http.Error(w, `{"error":"Automation not found"}`, 404)
			return
		}
	}
	log.Printf("[DeleteAutomation] DELETE SUCCESS - Project: %s, ID: %s, RowsAffected: %d", ctx.Project.Slug, id, rowsAffected)
	
	// Invalida o cache para remover a automação deletada do motor Nexus
	if d.NexusSvc != nil {
		d.NexusSvc.InvalidateCache(r.Context(), ctx.Project.Slug)
		
		// Limpa resíduos específicos da automação (contadores de falha, auto-disable, cache de planos)
		d.NexusSvc.CleanupAutomationResidues(r.Context(), id)
	}
	
	// Limpa webhook receivers que apontam para esta automação
	_, err = services.SystemPool.Exec(r.Context(),
		"DELETE FROM system.webhook_receivers WHERE target_type = 'AUTOMATION' AND target_id = $1 AND project_slug = $2",
		id, ctx.Project.Slug)
	if err != nil {
		log.Printf("[DeleteAutomation] WARNING - Failed to cleanup webhook receivers: %v", err)
		// Não falha a operação por este erro, apenas log
	} else {
		log.Printf("[DeleteAutomation] Cleaned up webhook receivers for automation: %s", id)
	}
	
	// Limpa automação deletada de snapshots de branches (automations_json)
	// Isso remove a automação do JSONB de todas as branches do projeto
	_, err = services.SystemPool.Exec(r.Context(),
		"UPDATE system.branches SET automations_json = automations_json - $1::text WHERE project_slug = $2 AND automations_json IS NOT NULL",
		id, ctx.Project.Slug)
	if err != nil {
		log.Printf("[DeleteAutomation] WARNING - Failed to cleanup branches automations_json: %v", err)
		// Não falha a operação por este erro, apenas log
	} else {
		log.Printf("[DeleteAutomation] Cleaned up branches automations_json for automation: %s", id)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// GetAutomationStats returns stats for automations — retorna stats PER automation_id
func (d *DataController) GetAutomationStats(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context().Value(types.CascataCtxKey).(*types.CascataRequest)
	
	log.Printf("[GetAutomationStats] ENTRY - Project: %s", ctx.Project.Slug)
	
	// Busca stats agrupados por automation_id (para o dashboard mostrar por automação)
	rows, err := services.SystemPool.Query(r.Context(),
		`SELECT 
			automation_id::text,
			COUNT(*) as total_runs,
			COUNT(*) FILTER (WHERE status = 'success') as success_count,
			COUNT(*) FILTER (WHERE status IN ('error', 'failed', 'timeout')) as failed_count,
			COALESCE(AVG(duration_ms), 0) as avg_ms,
			MAX(created_at) as last_run_at
		 FROM system.nexus_execution_log 
		 WHERE tenant_id = $1
		 GROUP BY automation_id`,
		ctx.Project.Slug)
	
	if err != nil {
		log.Printf("[GetAutomationStats] QUERY ERROR - Project: %s, Error: %v", ctx.Project.Slug, err)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{})
		return
	}
	defer rows.Close()

	stats := make(map[string]interface{})
	for rows.Next() {
		var automationID string
		var totalRuns, successCount, failedCount int64
		var avgMs float64
		var lastRunAt *time.Time

		if err := rows.Scan(&automationID, &totalRuns, &successCount, &failedCount, &avgMs, &lastRunAt); err != nil {
			continue
		}

		entry := map[string]interface{}{
			"total_runs":    totalRuns,
			"success_count": successCount,
			"failed_count":  failedCount,
			"avg_ms":        avgMs,
			"last_run_at":   nil,
		}
		if lastRunAt != nil {
			entry["last_run_at"] = lastRunAt.Format(time.RFC3339)
		}
		stats[automationID] = entry
	}

	log.Printf("[GetAutomationStats] SUCCESS - Project: %s, AutomationCount: %d", ctx.Project.Slug, len(stats))
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}

// GetAutomationRuns returns execution runs for automations
func (d *DataController) GetAutomationRuns(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context().Value(types.CascataCtxKey).(*types.CascataRequest)
	
	automationID := r.URL.Query().Get("automation_id")
	log.Printf("[GetAutomationRuns] ENTRY - Project: %s, AutomationID: %s", ctx.Project.Slug, automationID)
	
	var query string
	var args []interface{}
	
	if automationID != "" {
		query = `SELECT trace_id AS id, automation_id::text AS automation_id, status, duration_ms AS execution_time_ms, error_message, 
				 trigger_data AS trigger_payload, response_data AS final_output, created_at 
			 FROM system.nexus_execution_log 
			 WHERE tenant_id = $1 AND automation_id = $2::uuid 
			 ORDER BY created_at DESC LIMIT 100`
		args = []interface{}{ctx.Project.Slug, automationID}
	} else {
		query = `SELECT trace_id AS id, automation_id::text AS automation_id, status, duration_ms AS execution_time_ms, error_message, 
				 trigger_data AS trigger_payload, response_data AS final_output, created_at 
			 FROM system.nexus_execution_log 
			 WHERE tenant_id = $1 
			 ORDER BY created_at DESC LIMIT 100`
		args = []interface{}{ctx.Project.Slug}
	}
	
	rows, err := services.SystemPool.Query(r.Context(), query, args...)
	if err != nil {
		log.Printf("[GetAutomationRuns] QUERY FAILED - Project: %s, AutomationID: %s, Error: %v", ctx.Project.Slug, automationID, err)
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), 500)
		return
	}
	defer rows.Close()
	
	runs := []map[string]interface{}{}
	fDesc := rows.FieldDescriptions()
	for rows.Next() {
		vals, _ := rows.Values()
		row := make(map[string]interface{})
		for i, fd := range fDesc {
			row[fd.Name] = utils.PurifyPgxValue(vals[i])
		}
		runs = append(runs, row)
	}
	
	log.Printf("[GetAutomationRuns] SUCCESS - Project: %s, Count: %d", ctx.Project.Slug, len(runs))
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(runs)
}

// GetLogs retrieves API audit logs for a project (Observability Hub)
// Supports filtering, pagination, and comprehensive querying
func (d *DataController) GetLogs(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context().Value(types.CascataCtxKey).(*types.CascataRequest)
	if ctx.Project == nil {
		http.Error(w, `{"error":"Project Required"}`, 400)
		return
	}

	// Parse pagination params
	limit := 100
	if l, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && l > 0 && l <= 10000 {
		limit = l
	}

	offset := 0
	if o, err := strconv.Atoi(r.URL.Query().Get("offset")); err == nil && o >= 0 {
		offset = o
	}

	// Parse filter params
	method := r.URL.Query().Get("method")
	path := r.URL.Query().Get("path")
	clientIP := r.URL.Query().Get("client_ip")
	clientIPMode := r.URL.Query().Get("client_ip_mode") // "include" (default) ou "exclude"
	if clientIPMode != "exclude" {
		clientIPMode = "include"
	}
	userRole := r.URL.Query().Get("user_role")

	var statusCode *int
	if sc, err := strconv.Atoi(r.URL.Query().Get("status_code")); err == nil {
		statusCode = &sc
	}

	var minDuration, maxDuration *int64
	if d, err := strconv.ParseInt(r.URL.Query().Get("min_duration_ms"), 10, 64); err == nil {
		minDuration = &d
	}
	if d, err := strconv.ParseInt(r.URL.Query().Get("max_duration_ms"), 10, 64); err == nil {
		maxDuration = &d
	}

	// Date range filters
	var startDate, endDate *time.Time
	if sd, err := time.Parse(time.RFC3339, r.URL.Query().Get("start_date")); err == nil {
		startDate = &sd
	}
	if ed, err := time.Parse(time.RFC3339, r.URL.Query().Get("end_date")); err == nil {
		endDate = &ed
	}

	// Build dynamic query with filters
	whereClauses := []string{"project_slug = $1"}
	args := []interface{}{ctx.Project.Slug}
	argIdx := 1

	if method != "" {
		argIdx++
		whereClauses = append(whereClauses, fmt.Sprintf("method = $%d", argIdx))
		args = append(args, method)
	}
	if path != "" {
		argIdx++
		whereClauses = append(whereClauses, fmt.Sprintf("path ILIKE $%d", argIdx))
		args = append(args, "%"+path+"%")
	}
	// Client IP filter - suporta múltiplos IPs separados por vírgula
	if clientIP != "" {
		// Parse lista de IPs (separados por vírgula)
		ipList := strings.Split(clientIP, ",")
		var cleanIPs []string
		for _, ip := range ipList {
			ip = strings.TrimSpace(ip)
			if ip != "" {
				cleanIPs = append(cleanIPs, ip)
			}
		}

		if len(cleanIPs) > 0 {
			if len(cleanIPs) == 1 {
				// Single IP - exact match
				argIdx++
				if clientIPMode == "exclude" {
					whereClauses = append(whereClauses, fmt.Sprintf("client_ip != $%d", argIdx))
				} else {
					whereClauses = append(whereClauses, fmt.Sprintf("client_ip = $%d", argIdx))
				}
				args = append(args, cleanIPs[0])
			} else {
				// Multiple IPs - IN clause
				placeholders := make([]string, len(cleanIPs))
				for i := range cleanIPs {
					argIdx++
					placeholders[i] = fmt.Sprintf("$%d", argIdx)
					args = append(args, cleanIPs[i])
				}
				if clientIPMode == "exclude" {
					whereClauses = append(whereClauses, fmt.Sprintf("client_ip NOT IN (%s)", strings.Join(placeholders, ", ")))
				} else {
					whereClauses = append(whereClauses, fmt.Sprintf("client_ip IN (%s)", strings.Join(placeholders, ", ")))
				}
			}
		}
	}
	if userRole != "" {
		argIdx++
		whereClauses = append(whereClauses, fmt.Sprintf("user_role = $%d", argIdx))
		args = append(args, userRole)
	}
	if statusCode != nil {
		argIdx++
		whereClauses = append(whereClauses, fmt.Sprintf("status_code = $%d", argIdx))
		args = append(args, *statusCode)
	}
	if minDuration != nil {
		argIdx++
		whereClauses = append(whereClauses, fmt.Sprintf("duration_ms >= $%d", argIdx))
		args = append(args, *minDuration)
	}
	if maxDuration != nil {
		argIdx++
		whereClauses = append(whereClauses, fmt.Sprintf("duration_ms <= $%d", argIdx))
		args = append(args, *maxDuration)
	}
	if startDate != nil {
		argIdx++
		whereClauses = append(whereClauses, fmt.Sprintf("created_at >= $%d", argIdx))
		args = append(args, *startDate)
	}
	if endDate != nil {
		argIdx++
		whereClauses = append(whereClauses, fmt.Sprintf("created_at <= $%d", argIdx))
		args = append(args, *endDate)
	}

	whereSQL := strings.Join(whereClauses, " AND ")

	// Get total count for pagination metadata
	var totalCount int
	countQuery := fmt.Sprintf("SELECT COUNT(*) FROM system.api_logs WHERE %s", whereSQL)
	err := services.SystemPool.QueryRow(r.Context(), countQuery, args...).Scan(&totalCount)
	if err != nil {
		// If error is about missing column, fallback to simple query
		log.Printf("[GetLogs] Count query failed (possibly missing column): %v", err)
		totalCount = -1 // Unknown
	}

	// Build data query with pagination
	argIdx++
	limitIdx := argIdx
	args = append(args, limit)

	argIdx++
	offsetIdx := argIdx
	args = append(args, offset)

	dataQuery := fmt.Sprintf(`
		SELECT id, method, path, status_code, client_ip, duration_ms, user_role,
		       payload, headers, geo_info, response_size, created_at
		FROM system.api_logs
		WHERE %s
		ORDER BY created_at DESC
		LIMIT $%d OFFSET $%d`, whereSQL, limitIdx, offsetIdx)

	rows, err := services.SystemPool.Query(r.Context(), dataQuery, args...)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"Query failed: %s"}`, err.Error()), 500)
		return
	}
	defer rows.Close()

	logs := []map[string]interface{}{}
	fDesc := rows.FieldDescriptions()
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			continue
		}
		row := make(map[string]interface{})
		for i, fd := range fDesc {
			if i < len(vals) {
				row[fd.Name] = utils.PurifyPgxValue(vals[i])
			}
		}
		logs = append(logs, row)
	}

	// Build response with metadata
	response := map[string]interface{}{
		"data":       logs,
		"pagination": map[string]interface{}{
			"limit":       limit,
			"offset":      offset,
			"total_count": totalCount,
			"has_more":    totalCount < 0 || (offset+len(logs)) < totalCount,
			"returned":    len(logs),
		},
		"filters": map[string]interface{}{
			"method":         method,
			"path":           path,
			"client_ip":      clientIP,
			"client_ip_mode": clientIPMode,
			"user_role":      userRole,
			"status_code":    statusCode,
			"start_date":     startDate,
			"end_date":       endDate,
		},
		"project_slug": ctx.Project.Slug,
		"queried_at":   time.Now().UTC().Format(time.RFC3339),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// GetLogsStats returns aggregated statistics for API logs (Observability Hub Dashboard)
func (d *DataController) GetLogsStats(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context().Value(types.CascataCtxKey).(*types.CascataRequest)
	if ctx.Project == nil {
		http.Error(w, `{"error":"Project Required"}`, 400)
		return
	}

	// Parse time range - suporta date range personalizado ou hours
	var startTime, endTime time.Time
	hours := 24
	endTime = time.Now().UTC()
	
	// Verifica se tem start/end personalizado
	if startStr := r.URL.Query().Get("start"); startStr != "" {
		if parsed, err := time.Parse(time.RFC3339, startStr); err == nil {
			startTime = parsed.UTC()
		} else {
			// Tenta parse como datetime-local (YYYY-MM-DDTHH:mm)
			if parsed, err := time.Parse("2006-01-02T15:04", startStr); err == nil {
				startTime = parsed.UTC()
			}
		}
	}
	if endStr := r.URL.Query().Get("end"); endStr != "" {
		if parsed, err := time.Parse(time.RFC3339, endStr); err == nil {
			endTime = parsed.UTC()
		} else {
			// Tenta parse como datetime-local
			if parsed, err := time.Parse("2006-01-02T15:04", endStr); err == nil {
				endTime = parsed.UTC()
			}
		}
	}
	
	// Se não tem start personalizado, usa hours
	if startTime.IsZero() {
		if h, err := strconv.Atoi(r.URL.Query().Get("hours")); err == nil && h > 0 && h <= 720 {
			hours = h
		}
		startTime = endTime.Add(-time.Duration(hours) * time.Hour)
	}

	// Parse interval (30min ou 1hour)
	interval := r.URL.Query().Get("interval")
	if interval != "30" && interval != "30min" {
		interval = "60" // default 1 hora
	}
	use30Min := interval == "30" || interval == "30min"
	intervalMinutes := 30
	if !use30Min {
		intervalMinutes = 60
	}

	// Query aggregated stats
	hoursCalculated := int(endTime.Sub(startTime).Hours())
	if hoursCalculated < 1 {
		hoursCalculated = 1
	}
	stats := map[string]interface{}{
		"time_range_hours": hoursCalculated,
		"project_slug":     ctx.Project.Slug,
		"generated_at":     time.Now().UTC().Format(time.RFC3339),
		"interval_minutes": intervalMinutes,
		"start_time":       startTime.Format(time.RFC3339),
		"end_time":         endTime.Format(time.RFC3339),
	}

	// Total requests (com end time)
	var totalRequests int
	_ = services.SystemPool.QueryRow(r.Context(),
		`SELECT COUNT(*) FROM system.api_logs WHERE project_slug = $1 AND created_at >= $2 AND created_at <= $3`,
		ctx.Project.Slug, startTime, endTime).Scan(&totalRequests)
	stats["total_requests"] = totalRequests

	// Requests by method (com end time)
	methodStats := []map[string]interface{}{}
	rows, err := services.SystemPool.Query(r.Context(),
		`SELECT method, COUNT(*) as count FROM system.api_logs
		 WHERE project_slug = $1 AND created_at >= $2 AND created_at <= $3 AND method IS NOT NULL
		 GROUP BY method ORDER BY count DESC`,
		ctx.Project.Slug, startTime, endTime)
	if err == nil {
		for rows.Next() {
			var method string
			var count int
			rows.Scan(&method, &count)
			methodStats = append(methodStats, map[string]interface{}{"method": method, "count": count})
		}
		rows.Close()
	}
	stats["requests_by_method"] = methodStats

	// Status code distribution (com end time)
	statusStats := []map[string]interface{}{}
	rows, err = services.SystemPool.Query(r.Context(),
		`SELECT status_code, COUNT(*) as count FROM system.api_logs
		 WHERE project_slug = $1 AND created_at >= $2 AND created_at <= $3 AND status_code IS NOT NULL
		 GROUP BY status_code ORDER BY count DESC LIMIT 10`,
		ctx.Project.Slug, startTime, endTime)
	if err == nil {
		for rows.Next() {
			var statusCode int
			var count int
			rows.Scan(&statusCode, &count)
			statusStats = append(statusStats, map[string]interface{}{"status_code": statusCode, "count": count})
		}
		rows.Close()
	}
	stats["status_distribution"] = statusStats

	// Top paths (com end time)
	topPaths := []map[string]interface{}{}
	rows, err = services.SystemPool.Query(r.Context(),
		`SELECT path, COUNT(*) as count, AVG(duration_ms) as avg_duration
		 FROM system.api_logs
		 WHERE project_slug = $1 AND created_at >= $2 AND created_at <= $3 AND path IS NOT NULL
		 GROUP BY path ORDER BY count DESC LIMIT 10`,
		ctx.Project.Slug, startTime, endTime)
	if err == nil {
		for rows.Next() {
			var path string
			var count int
			var avgDuration float64
			rows.Scan(&path, &count, &avgDuration)
			topPaths = append(topPaths, map[string]interface{}{
				"path":         path,
				"count":        count,
				"avg_duration": avgDuration,
			})
		}
		rows.Close()
	}
	stats["top_paths"] = topPaths

	// Error rate (4xx + 5xx / total) (com end time)
	var errorCount int
	_ = services.SystemPool.QueryRow(r.Context(),
		`SELECT COUNT(*) FROM system.api_logs
		 WHERE project_slug = $1 AND created_at >= $2 AND created_at <= $3 AND status_code >= 400`,
		ctx.Project.Slug, startTime, endTime).Scan(&errorCount)

	errorRate := 0.0
	if totalRequests > 0 {
		errorRate = float64(errorCount) / float64(totalRequests) * 100
	}
	stats["error_count"] = errorCount
	stats["error_rate_percent"] = errorRate

	// Average response time (com end time)
	var avgResponseTime float64
	_ = services.SystemPool.QueryRow(r.Context(),
		`SELECT COALESCE(AVG(duration_ms), 0) FROM system.api_logs
		 WHERE project_slug = $1 AND created_at >= $2 AND created_at <= $3`,
		ctx.Project.Slug, startTime, endTime).Scan(&avgResponseTime)
	stats["avg_response_time_ms"] = avgResponseTime

	// Peak RPS (requests per second in any single second) (com end time)
	var peakRPS float64
	_ = services.SystemPool.QueryRow(r.Context(),
		`SELECT COALESCE(MAX(req_count), 0) FROM (
			SELECT COUNT(*) as req_count FROM system.api_logs
			WHERE project_slug = $1 AND created_at >= $2 AND created_at <= $3
			GROUP BY DATE_TRUNC('second', created_at)
		) as rps`,
		ctx.Project.Slug, startTime, endTime).Scan(&peakRPS)
	stats["peak_rps"] = peakRPS

	// THROUGHPUT for Traffic Pulse Chart - suporta 30min ou 1h intervalos
	// Format: [{ name: "14:00", requests: 120, success: 115, error: 5 }, ...]
	throughputStats := []map[string]interface{}{}
	
	// Helper para arredondar hora para slot de 30min (00 ou 30) em UTC
	roundTo30Min := func(t time.Time) time.Time {
		utcTime := t.UTC()
		minute := utcTime.Minute()
		if minute >= 30 {
			return time.Date(utcTime.Year(), utcTime.Month(), utcTime.Day(), utcTime.Hour(), 30, 0, 0, time.UTC)
		}
		return time.Date(utcTime.Year(), utcTime.Month(), utcTime.Day(), utcTime.Hour(), 0, 0, 0, time.UTC)
	}

	// Helper para arredondar para hora cheia em UTC
	roundToHour := func(t time.Time) time.Time {
		utcTime := t.UTC()
		return time.Date(utcTime.Year(), utcTime.Month(), utcTime.Day(), utcTime.Hour(), 0, 0, 0, time.UTC)
	}
	
	var intervalQuery string
	var queryArgs []interface{}
	var startSlot, endSlot time.Time
	
	// Calculate duration to decide label format
	duration := endTime.Sub(startTime)
	useDateInLabel := duration > 24*time.Hour
	
	if use30Min {
		// 30 minutos: query compatível usando TO_TIMESTAMP para criar slots
		startSlot = roundTo30Min(startTime)
		endSlot = roundTo30Min(endTime)
		
		// Format label based on duration - show date when period > 24h
		labelFormat := "HH24:MI"
		if useDateInLabel {
			labelFormat = "DD/MM HH24:MI"
		}
		
		intervalQuery = fmt.Sprintf(`
			SELECT 
				TO_CHAR(slot, '%s') as time_label,
				COALESCE(SUM(total_requests), 0) as total_requests,
				COALESCE(SUM(success_count), 0) as success_count,
				COALESCE(SUM(error_count), 0) as error_count
			FROM generate_series($4::timestamptz, $5::timestamptz, INTERVAL '30 minutes') as slot
			LEFT JOIN (
				SELECT 
					TO_TIMESTAMP(
						EXTRACT(EPOCH FROM DATE_TRUNC('hour', created_at)) + 
						CASE WHEN EXTRACT(MINUTE FROM created_at) >= 30 THEN 1800 ELSE 0 END
					)::timestamptz as time_slot,
					COUNT(*) as total_requests,
					COUNT(*) FILTER (WHERE status_code < 400) as success_count,
					COUNT(*) FILTER (WHERE status_code >= 400) as error_count
				FROM system.api_logs
				WHERE project_slug = $1 
					AND created_at >= $2 
					AND created_at <= $3
				GROUP BY 
					EXTRACT(EPOCH FROM DATE_TRUNC('hour', created_at)) + 
					CASE WHEN EXTRACT(MINUTE FROM created_at) >= 30 THEN 1800 ELSE 0 END
			) stats ON slot = stats.time_slot
			GROUP BY slot
			ORDER BY slot ASC`, labelFormat)
		queryArgs = []interface{}{ctx.Project.Slug, startTime, endTime, startSlot, endSlot}
	} else {
		// 1 hora: query simplificada usando EPOCH
		startSlot = roundToHour(startTime)
		endSlot = roundToHour(endTime)
		
		// Format label based on duration - show date when period > 24h
		labelFormat := "HH24:00"
		if useDateInLabel {
			labelFormat = "DD/MM HH24:00"
		}
		
		intervalQuery = fmt.Sprintf(`
			SELECT 
				TO_CHAR(slot, '%s') as time_label,
				COALESCE(SUM(total_requests), 0) as total_requests,
				COALESCE(SUM(success_count), 0) as success_count,
				COALESCE(SUM(error_count), 0) as error_count
			FROM generate_series($4::timestamptz, $5::timestamptz, INTERVAL '1 hour') as slot
			LEFT JOIN (
				SELECT 
					DATE_TRUNC('hour', created_at)::timestamptz as time_slot,
					COUNT(*) as total_requests,
					COUNT(*) FILTER (WHERE status_code < 400) as success_count,
					COUNT(*) FILTER (WHERE status_code >= 400) as error_count
				FROM system.api_logs
				WHERE project_slug = $1 
					AND created_at >= $2 
					AND created_at <= $3
				GROUP BY DATE_TRUNC('hour', created_at)
			) stats ON slot = stats.time_slot
			GROUP BY slot
			ORDER BY slot ASC`, labelFormat)
		queryArgs = []interface{}{ctx.Project.Slug, startTime, endTime, startSlot, endSlot}
	}
	
	rows, err = services.SystemPool.Query(r.Context(), intervalQuery, queryArgs...)
	if err != nil {
		// Log do erro para debugging
		log.Printf("[GetLogsStats] ERRO na query de throughput: %v", err)
		log.Printf("[GetLogsStats] Query params: project=%s, start=%v, end=%v, slotStart=%v, slotEnd=%v", 
			ctx.Project.Slug, startTime, endTime, startSlot, endSlot)
	} else {
		for rows.Next() {
			var timeLabel string
			var total, success, errorCount int
			rows.Scan(&timeLabel, &total, &success, &errorCount)
			throughputStats = append(throughputStats, map[string]interface{}{
				"name":     timeLabel, // "14:00" ou "14:30"
				"requests": total,
				"success":  success,
				"error":    errorCount,
			})
		}
		rows.Close()
		log.Printf("[GetLogsStats] Throughput retornado: %d registros", len(throughputStats))
	}
	stats["throughput"] = throughputStats

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}

// GetLogsExport exports logs for compliance reporting (JSON or CSV format)
func (d *DataController) GetLogsExport(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context().Value(types.CascataCtxKey).(*types.CascataRequest)
	if ctx.Project == nil {
		http.Error(w, `{"error":"Project Required"}`, 400)
		return
	}

	format := r.URL.Query().Get("format")
	if format != "csv" {
		format = "json" // Default to JSON
	}

	// Parse date range (default: last 7 days)
	days := 7
	if d, err := strconv.Atoi(r.URL.Query().Get("days")); err == nil && d > 0 && d <= 90 {
		days = d
	}
	startTime := time.Now().UTC().Add(-time.Duration(days) * 24 * time.Hour)

	// Query logs
	rows, err := services.SystemPool.Query(r.Context(),
		`SELECT id, method, path, status_code, client_ip, duration_ms, user_role,
		        payload, headers, geo_info, response_size, created_at
		 FROM system.api_logs
		 WHERE project_slug = $1 AND created_at >= $2
		 ORDER BY created_at DESC`,
		ctx.Project.Slug, startTime)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"Export failed: %s"}`, err.Error()), 500)
		return
	}
	defer rows.Close()

	// Collect logs
	logs := []map[string]interface{}{}
	fDesc := rows.FieldDescriptions()
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			continue
		}
		row := make(map[string]interface{})
		for i, fd := range fDesc {
			if i < len(vals) {
				row[fd.Name] = vals[i]
			}
		}
		logs = append(logs, row)
	}

	// Set filename
	timestamp := time.Now().UTC().Format("20060102_150405")
	filename := fmt.Sprintf("audit_logs_%s_%s_%dd", ctx.Project.Slug, timestamp, days)

	if format == "csv" {
		w.Header().Set("Content-Type", "text/csv")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s.csv", filename))

		// Write CSV header
		fmt.Fprintln(w, "id,method,path,status_code,client_ip,duration_ms,user_role,response_size,created_at")

		// Write data rows
		for _, log := range logs {
			fmt.Fprintf(w, "%v,%v,%v,%v,%v,%v,%v,%v,%v\n",
				log["id"], log["method"], log["path"], log["status_code"],
				log["client_ip"], log["duration_ms"], log["user_role"],
				log["response_size"], log["created_at"])
		}
	} else {
		// JSON format
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s.json", filename))

		response := map[string]interface{}{
			"export_metadata": map[string]interface{}{
				"project_slug":    ctx.Project.Slug,
				"exported_at":     time.Now().UTC().Format(time.RFC3339),
				"time_range_days": days,
				"record_count":    len(logs),
				"format":          "json",
				"compliance":      "ISO 27001 / SOC2",
			},
			"logs": logs,
		}
		json.NewEncoder(w).Encode(response)
	}
}

// ClearLogs - DELETE /api/data/{slug}/logs?days=X - Remove logs antigos (manual purge)
// Usa system.purge_old_logs() que é a ÚNICA forma autorizada de deletar logs (immutability)
func (d *DataController) ClearLogs(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context().Value(types.CascataCtxKey).(*types.CascataRequest)
	if ctx.Project == nil {
		http.Error(w, `{"error":"Project Required"}`, 400)
		return
	}

	// Parse days param
	daysStr := r.URL.Query().Get("days")
	days, err := strconv.Atoi(daysStr)
	if err != nil || days < 1 {
		days = 30 // default
	}
	cutoffDate := time.Now().UTC().AddDate(0, 0, -days)

	// Audit logging
	userID := ""
	if ctx.User != nil {
		if u, ok := ctx.User["id"].(string); ok {
			userID = u
		}
	}

	// Use the authorized SQL function (handles maintenance_mode flag internally)
	var deletedCount int
	err = services.SystemPool.QueryRow(r.Context(),
		`SELECT system.purge_old_logs($1, $2, FALSE)`,
		ctx.Project.Slug, days).Scan(&deletedCount)

	if err != nil {
		log.Printf("[ClearLogs] Purge error: %v", err)
		http.Error(w, fmt.Sprintf(`{"error": "Failed to clear logs: %s"}`, err.Error()), http.StatusInternalServerError)
		return
	}

	// Log admin action
	services.LogAdminAction(r.Context(), services.AdminAuditEntry{
		ActionType:        services.ActionProjectUpdate,
		ActorType:         services.ActorUser,
		ActorID:           userID,
		TargetType:        "project",
		TargetID:          ctx.Project.ID,
		ActionDescription: fmt.Sprintf("Manually purged %d logs older than %d days", deletedCount, days),
		Status:            services.StatusSuccess,
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":        true,
		"deleted_count":  deletedCount,
		"days_threshold": days,
		"cutoff_date":    cutoffDate.Format(time.RFC3339),
		"project_slug":   ctx.Project.Slug,
	})
}

// UpdatePurgeSchedule - PATCH /api/data/{slug}/logs/schedule - Configura cron de purge
func (d *DataController) UpdatePurgeSchedule(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context().Value(types.CascataCtxKey).(*types.CascataRequest)
	if ctx.Project == nil {
		http.Error(w, `{"error":"Project Required"}`, 400)
		return
	}

	var req struct {
		Cron      string `json:"cron"`
		Timezone  string `json:"timezone"`  // Stored in metadata->>'timezone'
		Enabled   bool   `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error": "Invalid request"}`, http.StatusBadRequest)
		return
	}

	// Validate cron (basic check)
	if req.Cron == "" {
		req.Cron = "0 4 * * *" // default 4 AM
	}
	if req.Timezone == "" {
		req.Timezone = "UTC"
	}

	// Update database - timezone goes to metadata, not separate column
	_, err := services.SystemPool.Exec(r.Context(), `
		UPDATE system.projects 
		SET purge_cron_expression = $1, 
		    purge_enabled = $2,
		    metadata = COALESCE(metadata, '{}'::jsonb) || jsonb_build_object('timezone', $3),
		    updated_at = NOW()
		WHERE id = $4`,
		req.Cron, req.Enabled, req.Timezone, ctx.Project.ID)

	if err != nil {
		log.Printf("[UpdatePurgeSchedule] DB error: %v", err)
		http.Error(w, `{"error": "Failed to update schedule"}`, http.StatusInternalServerError)
		return
	}

	// Update PurgeScheduler in real-time
	scheduler := services.GetPurgeScheduler(services.SystemPool)
	if req.Enabled {
		scheduler.ScheduleProjectPurge(ctx.Project.Slug, req.Cron, req.Timezone, 
			ctx.Project.LogRetentionDays, ctx.Project.ArchiveLogs)
	} else {
		scheduler.RemoveProjectPurge(ctx.Project.Slug)
	}

	// Audit logging
	userID := ""
	if ctx.User != nil {
		if u, ok := ctx.User["id"].(string); ok {
			userID = u
		}
	}
	services.LogAdminAction(r.Context(), services.AdminAuditEntry{
		ActionType:        services.ActionProjectUpdate,
		ActorType:         services.ActorUser,
		ActorID:           userID,
		TargetType:        "project",
		TargetID:          ctx.Project.ID,
		ActionDescription: fmt.Sprintf("Purge schedule updated: cron=%s, tz=%s, enabled=%v", req.Cron, req.Timezone, req.Enabled),
		Status:            services.StatusSuccess,
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":       true,
		"cron":          req.Cron,
		"timezone":      req.Timezone,
		"enabled":       req.Enabled,
		"project_slug":  ctx.Project.Slug,
		"note":          "Schedule updated and applied immediately",
	})
}

// GetLogExportConfig returns the log export configuration for a project
func (d *DataController) GetLogExportConfig(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context().Value(types.CascataCtxKey).(*types.CascataRequest)
	if ctx.Project == nil {
		http.Error(w, `{"error":"Project Required"}`, 400)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"config":       ctx.Project.Metadata.LogExport,
		"project_slug": ctx.Project.Slug,
	})
}

// UpdateLogExportConfig updates the log export configuration for a project
func (d *DataController) UpdateLogExportConfig(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context().Value(types.CascataCtxKey).(*types.CascataRequest)
	if ctx.Project == nil {
		http.Error(w, `{"error":"Project Required"}`, 400)
		return
	}

	var req types.LogExportConfig
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"Invalid request: `+err.Error()+`"}`, 400)
		return
	}

	// Validate configuration
	otlpSvc := services.NewOTLPService()
	if err := otlpSvc.ValidateLogExportConfig(req); err != nil {
		http.Error(w, `{"error":"Validation failed: `+err.Error()+`"}`, 400)
		return
	}

	// Generate API key if not provided
	if req.APIKey == "" {
		req.APIKey = otlpSvc.GenerateAPIKey()
	}

	// Merge with existing config to preserve exporter IDs
	existingConfig := ctx.Project.Metadata.LogExport
	if len(req.Exporters) > 0 && len(existingConfig.Exporters) > 0 {
		for i, newExp := range req.Exporters {
			if newExp.ID == "" {
				// Try to find matching exporter by provider
				for _, oldExp := range existingConfig.Exporters {
					if oldExp.Provider == newExp.Provider && oldExp.Name == newExp.Name {
						req.Exporters[i].ID = oldExp.ID
						break
					}
				}
				if req.Exporters[i].ID == "" {
					req.Exporters[i].ID = generateExporterID()
				}
			}
		}
	}

	// Update metadata in database
	metadataJSON, err := json.Marshal(map[string]interface{}{
		"log_export": req,
	})
	if err != nil {
		http.Error(w, `{"error":"Failed to marshal metadata"}`, 500)
		return
	}

	_, err = services.SystemPool.Exec(r.Context(), `
		UPDATE system.projects 
		SET metadata = COALESCE(metadata, '{}'::jsonb) || $1::jsonb,
		    updated_at = NOW()
		WHERE id = $2`,
		metadataJSON, ctx.Project.ID)

	if err != nil {
		log.Printf("[UpdateLogExportConfig] DB error: %v", err)
		http.Error(w, `{"error":"Failed to update configuration"}`, 500)
		return
	}

	// Audit logging
	userID := ""
	if ctx.User != nil {
		if u, ok := ctx.User["id"].(string); ok {
			userID = u
		}
	}
	services.LogAdminAction(r.Context(), services.AdminAuditEntry{
		ActionType:        services.ActionProjectUpdate,
		ActorType:         services.ActorUser,
		ActorID:           userID,
		TargetType:        "project",
		TargetID:          ctx.Project.ID,
		ActionDescription: fmt.Sprintf("Log export config updated: mode=%s, enabled=%v, exporters=%d", req.Mode, req.Enabled, len(req.Exporters)),
		Status:            services.StatusSuccess,
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":       true,
		"config":        req,
		"project_slug":  ctx.Project.Slug,
		"note":          "Log export configuration updated successfully",
	})
}

// TestLogExportConnection tests the connection to a log export endpoint
func (d *DataController) TestLogExportConnection(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context().Value(types.CascataCtxKey).(*types.CascataRequest)
	if ctx.Project == nil {
		http.Error(w, `{"error":"Project Required"}`, 400)
		return
	}

	var req struct {
		Exporter types.LogExportExporterConfig `json:"exporter"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"Invalid request"}`, 400)
		return
	}

	otlpSvc := services.NewOTLPService()

	// Create test log entry
	testEntries := []services.AuditLogEntry{
		{
			ProjectSlug:  ctx.Project.Slug,
			Method:       "TEST",
			Path:         "/test/connection",
			StatusCode:   200,
			ClientIP:     "127.0.0.1",
			DurationMs:   1,
			UserRole:     "test",
			CreatedAt:    time.Now().UTC(),
		},
	}

	var err error
	if req.Exporter.Endpoint != "" {
		err = otlpSvc.ExportLogsNative(r.Context(), req.Exporter.Endpoint, req.Exporter.APIKey, req.Exporter.Headers, testEntries)
	} else {
		// Test sidecar connection
		config := ctx.Project.Metadata.LogExport
		err = otlpSvc.SendLogsToCollector(r.Context(), ctx.Project.Slug, config.APIKey, testEntries)
	}

	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Connection test successful",
	})
}

// GenerateLogExportAPIKey generates a new API key for log export
func (d *DataController) GenerateLogExportAPIKey(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context().Value(types.CascataCtxKey).(*types.CascataRequest)
	if ctx.Project == nil {
		http.Error(w, `{"error":"Project Required"}`, 400)
		return
	}

	otlpSvc := services.NewOTLPService()
	newKey := otlpSvc.GenerateAPIKey()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"api_key":      newKey,
		"project_slug": ctx.Project.Slug,
	})
}

func generateExporterID() string {
	return fmt.Sprintf("exp_%d", time.Now().UnixNano())
}

// ActivateAutomation ativa uma automação com validação de conflitos
func (d *DataController) ActivateAutomation(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context().Value(types.CascataCtxKey).(*types.CascataRequest)

	if !ctx.IsSystemRequest && !ctx.IsDashboardAuth && ctx.UserRole != types.RoleService {
		http.Error(w, `{"error":"Unauthorized"}`, 403)
		return
	}

	id := chi.URLParam(r, "id")
	log.Printf("[ActivateAutomation] ENTRY - Project: %s, ID: %s", ctx.Project.Slug, id)

	branchName := types.GetBranchName(r.Context())

	// 1. Buscar automação que será ativada
	var targetHookType string
	var targetTable, targetEvent *string
	var targetRoutePattern, targetMethod *string
	err := services.SystemPool.QueryRow(r.Context(),
		`SELECT hook_type, table_name, event_type, route_pattern, method FROM system.nexus_automations WHERE id=$1 AND tenant_id=$2 AND branch_name=$3`,
		id, ctx.Project.Slug, branchName).Scan(&targetHookType, &targetTable, &targetEvent, &targetRoutePattern, &targetMethod)
	if err != nil {
		log.Printf("[ActivateAutomation] NOT FOUND - Project: %s, ID: %s", ctx.Project.Slug, id)
		http.Error(w, `{"error":"Automation not found"}`, 404)
		return
	}

	// 2. Se for CRON ou MANUAL, não há conflitos possíveis (múltiplas instâncias podem coexistir)
	if targetHookType != "CRON" && targetHookType != "MANUAL" {
		// Tratar nulos como strings vazias para lógica de conflito
		tTable := ""
		if targetTable != nil { tTable = *targetTable }
		tEvent := ""
		if targetEvent != nil { tEvent = *targetEvent }
		tRoutePattern := "*"
		if targetRoutePattern != nil { tRoutePattern = *targetRoutePattern }
		tMethod := "ANY"
		if targetMethod != nil { tMethod = *targetMethod }

		// 3. Buscar automações ativas que podem conflitar
		conflictHookSQL := "hook_type=$3"
		conflictArgs := []interface{}{ctx.Project.Slug, id, targetHookType, branchName}
		if targetHookType == "PRE_PERSIST" || targetHookType == "POST_PERSIST" {
			conflictHookSQL = "hook_type IN ('PRE_PERSIST', 'POST_PERSIST')"
			conflictArgs = []interface{}{ctx.Project.Slug, id, branchName}
		}
		rows, err := services.SystemPool.Query(r.Context(),
			fmt.Sprintf(`SELECT id, name, table_name, event_type, route_pattern, method FROM system.nexus_automations 
			 WHERE tenant_id=$1 AND status='active' AND is_active=true AND id!=$2 AND branch_name=$%d 
			 AND %s`, len(conflictArgs), conflictHookSQL),
			conflictArgs...)
		if err != nil {
			log.Printf("[ActivateAutomation] DB ERROR - Project: %s, Error: %v", ctx.Project.Slug, err)
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), 500)
			return
		}
		defer rows.Close()

		for rows.Next() {
			var conflictID, conflictName string
			var conflictTable, conflictEvent *string
			var conflictRoutePattern, conflictMethod *string
			if err := rows.Scan(&conflictID, &conflictName, &conflictTable, &conflictEvent, &conflictRoutePattern, &conflictMethod); err != nil {
				continue
			}

			cTable := ""
			if conflictTable != nil { cTable = *conflictTable }
			cEvent := ""
			if conflictEvent != nil { cEvent = *conflictEvent }
			cRoutePattern := "*"
			if conflictRoutePattern != nil { cRoutePattern = *conflictRoutePattern }
			cMethod := "ANY"
			if conflictMethod != nil { cMethod = *conflictMethod }

			if targetHookType == "WEBHOOK" {
				// Webhooks conflitam apenas se tiverem o mesmo route_pattern AND métodos sobrepostos
				methodsOverlap := cMethod == "ANY" || tMethod == "ANY" || cMethod == tMethod
				if cRoutePattern == tRoutePattern && methodsOverlap {
					log.Printf("[ActivateAutomation] CONFLICT - Project: %s, ID: %s conflicts with %s (webhook route/method match)", ctx.Project.Slug, id, conflictID)
					http.Error(w, fmt.Sprintf(`{"error":"Conflict with active webhook workflow '%s' (%s) on route '%s' and method '%s'","conflicting_id":"%s","conflicting_name":"%s"}`, conflictName, conflictID, cRoutePattern, cMethod, conflictID, conflictName), 409)
					return
				}
			} else {
				// Se mesma tabela e evento (ou wildcard), é conflito
				if (cTable == tTable || cTable == "" || cTable == "*") &&
					(cEvent == tEvent || cEvent == "" || cEvent == "*" || cEvent == "ANY" || tEvent == "ANY") {
					log.Printf("[ActivateAutomation] CONFLICT - Project: %s, ID: %s conflicts with %s", ctx.Project.Slug, id, conflictID)
					http.Error(w, fmt.Sprintf(`{"error":"Conflict with active workflow '%s' (%s)","conflicting_id":"%s","conflicting_name":"%s"}`, conflictName, conflictID, conflictID, conflictName), 409)
					return
				}
			}
		}
	}

	// 5. Atualizar status para active
	_, err = services.SystemPool.Exec(r.Context(),
		`UPDATE system.nexus_automations SET status='active', is_active=true, updated_at=NOW() WHERE id=$1 AND tenant_id=$2 AND branch_name=$3`,
		id, ctx.Project.Slug, branchName)
	if err != nil {
		log.Printf("[ActivateAutomation] UPDATE FAILED - Project: %s, ID: %s, Error: %v", ctx.Project.Slug, id, err)
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), 500)
		return
	}

	if d.NexusSvc != nil {
		d.NexusSvc.InvalidateCache(r.Context(), ctx.Project.Slug)
	}

	log.Printf("[ActivateAutomation] SUCCESS - Project: %s, ID: %s", ctx.Project.Slug, id)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "status": "active"})
}

// DeactivateAutomation desativa uma automação (muda para draft)
func (d *DataController) DeactivateAutomation(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context().Value(types.CascataCtxKey).(*types.CascataRequest)

	if !ctx.IsSystemRequest && !ctx.IsDashboardAuth && ctx.UserRole != types.RoleService {
		http.Error(w, `{"error":"Unauthorized"}`, 403)
		return
	}

	id := chi.URLParam(r, "id")
	log.Printf("[DeactivateAutomation] ENTRY - Project: %s, ID: %s", ctx.Project.Slug, id)

	branchName := types.GetBranchName(r.Context())

	// Atualizar status para draft
	result, err := services.SystemPool.Exec(r.Context(),
		`UPDATE system.nexus_automations SET status='draft', is_active=false, updated_at=NOW() WHERE id=$1 AND tenant_id=$2 AND branch_name=$3`,
		id, ctx.Project.Slug, branchName)
	if err != nil {
		log.Printf("[DeactivateAutomation] UPDATE FAILED - Project: %s, ID: %s, Error: %v", ctx.Project.Slug, id, err)
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), 500)
		return
	}

	rowsAffected := result.RowsAffected()
	if rowsAffected == 0 {
		log.Printf("[DeactivateAutomation] NOT FOUND - Project: %s, ID: %s", ctx.Project.Slug, id)
		http.Error(w, `{"error":"Automation not found"}`, 404)
		return
	}

	if d.NexusSvc != nil {
		d.NexusSvc.InvalidateCache(r.Context(), ctx.Project.Slug)
	}

	log.Printf("[DeactivateAutomation] SUCCESS - Project: %s, ID: %s", ctx.Project.Slug, id)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "status": "draft"})
}

// TestAutomation executa uma automação em modo de teste com payload customizado
// POST /api/data/{project}/automations/{id}/test
func (d *DataController) TestAutomation(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context().Value(types.CascataCtxKey).(*types.CascataRequest)

	id := chi.URLParam(r, "id")
	log.Printf("[TestAutomation] ENTRY - Project: %s, ID: %s", ctx.Project.Slug, id)

	branchName := types.GetBranchName(r.Context())

	// 1. Buscar automação
	var automation struct {
		ID            string
		Name          string
		GraphJSON     string
		ExecutionMode string
		Status        string
	}
	err := services.SystemPool.QueryRow(r.Context(),
		`SELECT id, name, graph_json, execution_mode, status FROM system.nexus_automations WHERE id=$1 AND tenant_id=$2 AND branch_name=$3`,
		id, ctx.Project.Slug, branchName).Scan(&automation.ID, &automation.Name, &automation.GraphJSON, &automation.ExecutionMode, &automation.Status)
	if err != nil {
		log.Printf("[TestAutomation] NOT FOUND - Project: %s, ID: %s", ctx.Project.Slug, id)
		http.Error(w, `{"error":"Automation not found"}`, 404)
		return
	}

	// 2. Parse do payload de teste (ou usa default)
	var testPayload map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&testPayload); err != nil {
		testPayload = map[string]interface{}{
			"test": true,
			"timestamp": time.Now().Unix(),
			"source": "nexus_test_mode",
		}
	}

	traceID := fmt.Sprintf("nxs-test-%d", time.Now().UnixNano())
	triggerData := map[string]interface{}{
		"payload": testPayload,
		"headers": map[string]string{"X-Test-Mode": "true"},
		"is_test": true,
	}

	// 3. Executar síncrono (sempre para teste)
	startTime := time.Now()
	result, err := d.NexusSvc.HookResolver.Engine.Execute(r.Context(), ctx.Project.Slug, string(ctx.UserRole), []byte(automation.GraphJSON), testPayload, map[string]string{"X-Test-Mode": "true"}, "fast_lane")
	duration := time.Since(startTime).Milliseconds()

	if err != nil {
		log.Printf("[TestAutomation] EXECUTION FAILED - Project: %s, ID: %s, Error: %v", ctx.Project.Slug, id, err)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"execution_id": traceID,
			"status":       "error",
			"error":        err.Error(),
			"duration_ms":  duration,
		})
		return
	}

	// 4. Registrar execução
	nexus.RecordExecution(
		r.Context(),
		services.SystemPool,
		d.NexusSvc.HookResolver.Logger,
		result,
		automation.ID,
		ctx.Project.Slug,
		triggerData,
	)

	log.Printf("[TestAutomation] SUCCESS - Project: %s, ID: %s, TraceID: %s, Duration: %dms", ctx.Project.Slug, id, traceID, duration)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"execution_id":  traceID,
		"status":        "success",
		"duration_ms":   duration,
		"trigger_payload": testPayload,
		"final_output":  result.ResponseData,
	})
}

// GetAutomationStepLogs retorna logs detalhados de uma execução (n8n-style)
// GET /api/data/{project}/automations/step-logs?execution_id=xxx
func (d *DataController) GetAutomationStepLogs(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context().Value(types.CascataCtxKey).(*types.CascataRequest)

	executionID := r.URL.Query().Get("execution_id")
	if executionID == "" {
		http.Error(w, `{"error":"execution_id is required"}`, 400)
		return
	}

	log.Printf("[GetAutomationStepLogs] ENTRY - Project: %s, ExecutionID: %s", ctx.Project.Slug, executionID)

	// Buscar logs detalhados da execução
	rows, err := services.SystemPool.Query(r.Context(),
		`SELECT step_id, node_id, node_type, node_name, level, message,
			 input_data, output_data, error_details, duration_ms, metadata, created_at
		 FROM system.automation_step_logs
		 WHERE execution_id = $1 AND project_slug = $2
		 ORDER BY created_at ASC`,
		executionID, ctx.Project.Slug)
	if err != nil {
		log.Printf("[GetAutomationStepLogs] QUERY FAILED - Project: %s, Error: %v", ctx.Project.Slug, err)
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), 500)
		return
	}
	defer rows.Close()

	logs := []map[string]interface{}{}
	for rows.Next() {
		var stepID, nodeID, nodeType, nodeName, level, message string
		var errorDetails string
		var durationMs int64
		var inputData, outputData, metadata []byte
		var createdAt time.Time

		err := rows.Scan(&stepID, &nodeID, &nodeType, &nodeName, &level, &message,
			&inputData, &outputData, &errorDetails, &durationMs, &metadata, &createdAt)
		if err != nil {
			continue
		}

		var inputJSON, outputJSON, metaJSON interface{}
		json.Unmarshal(inputData, &inputJSON)
		json.Unmarshal(outputData, &outputJSON)
		json.Unmarshal(metadata, &metaJSON)

		logs = append(logs, map[string]interface{}{
			"step_id":       stepID,
			"node_id":       nodeID,
			"node_type":     nodeType,
			"node_name":     nodeName,
			"level":         level,
			"message":       message,
			"input_data":    inputJSON,
			"output_data":   outputJSON,
			"error_details": errorDetails,
			"duration_ms":   durationMs,
			"metadata":      metaJSON,
			"created_at":    createdAt.Format(time.RFC3339),
		})
	}

	log.Printf("[GetAutomationStepLogs] SUCCESS - Project: %s, ExecutionID: %s, Count: %d", ctx.Project.Slug, executionID, len(logs))
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"execution_id": executionID,
		"logs":         logs,
		"count":        len(logs),
	})
}

// GetAutomationRunLogs retorna logs em formato direto para a tela de runs.
// GET /api/data/{project}/automations/runs/{executionId}/logs
func (d *DataController) GetAutomationRunLogs(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context().Value(types.CascataCtxKey).(*types.CascataRequest)
	executionID := chi.URLParam(r, "executionId")
	if executionID == "" {
		http.Error(w, `{"error":"execution id is required"}`, 400)
		return
	}

	rows, err := services.SystemPool.Query(r.Context(),
		`SELECT step_id, node_id, node_type, node_name, level, message,
			 input_data, output_data, error_details, duration_ms, metadata, created_at
		 FROM system.automation_step_logs
		 WHERE execution_id = $1 AND project_slug = $2
		 ORDER BY created_at ASC`,
		executionID, ctx.Project.Slug)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), 500)
		return
	}
	defer rows.Close()

	logs := []map[string]interface{}{}
	for rows.Next() {
		var stepID, nodeID, nodeType, nodeName, level, message string
		var errorDetails string
		var durationMs int64
		var inputData, outputData, metadata []byte
		var createdAt time.Time
		if err := rows.Scan(&stepID, &nodeID, &nodeType, &nodeName, &level, &message,
			&inputData, &outputData, &errorDetails, &durationMs, &metadata, &createdAt); err != nil {
			continue
		}
		var inputJSON, outputJSON, metaJSON interface{}
		_ = json.Unmarshal(inputData, &inputJSON)
		_ = json.Unmarshal(outputData, &outputJSON)
		_ = json.Unmarshal(metadata, &metaJSON)
		logs = append(logs, map[string]interface{}{
			"step_id":       stepID,
			"node_id":       nodeID,
			"node_type":     nodeType,
			"node_name":     nodeName,
			"level":         level,
			"message":       message,
			"input_data":    inputJSON,
			"output_data":   outputJSON,
			"error_details": errorDetails,
			"duration_ms":   durationMs,
			"metadata":      metaJSON,
			"created_at":    createdAt.Format(time.RFC3339),
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(logs)
}

// GetAutomationExecutionList retorna lista de execuções com resumo
// GET /api/data/{project}/automations/executions?automation_id=xxx&limit=50
func (d *DataController) GetAutomationExecutionList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context().Value(types.CascataCtxKey).(*types.CascataRequest)

	automationID := r.URL.Query().Get("automation_id")
	limit := 50
	if l, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && l > 0 && l <= 200 {
		limit = l
	}

	log.Printf("[GetAutomationExecutionList] ENTRY - Project: %s, AutomationID: %s", ctx.Project.Slug, automationID)

	// Query para buscar execuções únicas com resumo
	query := `
		SELECT 
			execution_id,
			MIN(created_at) as started_at,
			MAX(created_at) as finished_at,
			COUNT(*) as step_count,
			MAX(CASE WHEN level = 'error' THEN 1 ELSE 0 END) as has_error,
			MAX(duration_ms) as total_duration_ms
		FROM system.automation_step_logs
		WHERE project_slug = $1
	`
	args := []interface{}{ctx.Project.Slug}
	argCount := 1

	if automationID != "" {
		argCount++
		query += fmt.Sprintf(" AND automation_id = $%d", argCount)
		args = append(args, automationID)
	}

	query += `
		GROUP BY execution_id
		ORDER BY MIN(created_at) DESC
		LIMIT $` + fmt.Sprintf("%d", argCount+1)
	args = append(args, limit)

	rows, err := services.SystemPool.Query(r.Context(), query, args...)
	if err != nil {
		log.Printf("[GetAutomationExecutionList] QUERY FAILED - Project: %s, Error: %v", ctx.Project.Slug, err)
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), 500)
		return
	}
	defer rows.Close()

	executions := []map[string]interface{}{}
	for rows.Next() {
		var executionID string
		var startedAt, finishedAt time.Time
		var stepCount int
		var hasError bool
		var totalDurationMs int64

		err := rows.Scan(&executionID, &startedAt, &finishedAt, &stepCount, &hasError, &totalDurationMs)
		if err != nil {
			continue
		}

		status := "success"
		if hasError {
			status = "error"
		}

		executions = append(executions, map[string]interface{}{
			"execution_id":      executionID,
			"started_at":        startedAt.Format(time.RFC3339),
			"finished_at":       finishedAt.Format(time.RFC3339),
			"step_count":        stepCount,
			"status":            status,
			"total_duration_ms": totalDurationMs,
		})
	}

	log.Printf("[GetAutomationExecutionList] SUCCESS - Project: %s, Count: %d", ctx.Project.Slug, len(executions))
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"executions": executions,
		"count":      len(executions),
	})
}

func (d *DataController) ExtractAndValidateStepUp(r *http.Request, ctx *types.CascataRequest, body map[string]interface{}, data map[string]interface{}) {
	var stepUpProvider, stepUpCode string

	// 1. Extract from Headers
	if p := r.Header.Get("X-Cascata-Stepup-Provider"); p != "" {
		stepUpProvider = p
	}
	if c := r.Header.Get("X-Cascata-Stepup-Code"); c != "" {
		stepUpCode = c
	}

	// Step-up keys that need to be parsed and stripped to prevent PostgreSQL write errors
	keysToCheck := []string{
		"totp_code", "otp_code", "passkey_code", "biometria_code",
		"totp", "otp", "passkey", "biometria",
		"provider", "code",
	}

	// Local helper function to extract and strip keys from a map
	extractFromMap := func(m map[string]interface{}) {
		if m == nil {
			return
		}
		for _, key := range keysToCheck {
			val, exists := m[key]
			if !exists {
				continue
			}

			// Resolve value to string safely
			var valStr string
			if s, ok := val.(string); ok {
				valStr = s
			} else if f, ok := val.(float64); ok {
				valStr = fmt.Sprintf("%.0f", f)
			}

			if valStr != "" {
				if key == "provider" {
					if stepUpProvider == "" {
						stepUpProvider = valStr
					}
				} else if key == "code" {
					if stepUpCode == "" {
						stepUpCode = valStr
					}
				} else if strings.HasSuffix(key, "_code") {
					prov := strings.TrimSuffix(key, "_code")
					if stepUpProvider == "" {
						stepUpProvider = prov
					}
					if stepUpCode == "" {
						stepUpCode = valStr
					}
				} else {
					// Fallback for raw keys like "totp": "value"
					if stepUpProvider == "" {
						stepUpProvider = key
					}
					if stepUpCode == "" {
						stepUpCode = valStr
					}
				}
			}
			delete(m, key)
		}
	}

	// Extract and clean from both maps (handles standard payloads and panel data payloads)
	extractFromMap(body)
	extractFromMap(data)

	query := r.URL.Query()
	queryChanged := false
	for _, key := range keysToCheck {
		valStr := query.Get(key)
		if valStr == "" {
			continue
		}
		if key == "provider" {
			if stepUpProvider == "" {
				stepUpProvider = valStr
			}
		} else if key == "code" {
			if stepUpCode == "" {
				stepUpCode = valStr
			}
		} else if strings.HasSuffix(key, "_code") {
			prov := strings.TrimSuffix(key, "_code")
			if stepUpProvider == "" {
				stepUpProvider = prov
			}
			if stepUpCode == "" {
				stepUpCode = valStr
			}
		} else {
			if stepUpProvider == "" {
				stepUpProvider = key
			}
			if stepUpCode == "" {
				stepUpCode = valStr
			}
		}
		query.Del(key)
		queryChanged = true
	}
	if queryChanged {
		r.URL.RawQuery = query.Encode()
	}

	if stepUpProvider == "" || stepUpCode == "" {
		return
	}
	if ctx.User == nil {
		return
	}
	sub, ok := ctx.User["sub"].(string)
	if !ok || sub == "" {
		return
	}

	authSvc := &services.AuthService{}
	verified := false
	provSlug := strings.ToLower(stepUpProvider)
	
	// Normalize for StepUpProviders output to match FormatFactorName precisely
	dbProvider := provSlug
	if provSlug == "passkey" || provSlug == "biometria" {
		dbProvider = "passkey"
		provSlug = "passkey"
	} else if provSlug == "totp/mfa" || provSlug == "mfa" || provSlug == "totp" {
		dbProvider = "totp"
		provSlug = "totp"
	} else if provSlug == "email_otp" || provSlug == "otp" || provSlug == "email" {
		dbProvider = "email" // For legacy/standard auth.identities
		provSlug = "otp"
	}

	if provSlug == "totp" {
		var secret string
		err := ctx.ProjectPool.QueryRow(r.Context(), "SELECT identifier FROM auth.identities WHERE user_id = $1 AND provider = 'totp' AND verified_at IS NOT NULL LIMIT 1", sub).Scan(&secret)
		if err == nil && authSvc.VerifyTOTP(secret, stepUpCode) {
			verified = true
			ctx.StepUpProviders = "totp"
		}
	} else {
		// Agnostic OTP Validation
		var identifier string
		err := ctx.ProjectPool.QueryRow(r.Context(), "SELECT identifier FROM auth.identities WHERE user_id = $1 AND provider = $2 LIMIT 1", sub, dbProvider).Scan(&identifier)
		if err == nil && identifier != "" {
			var storedCode string
			err := ctx.ProjectPool.QueryRow(r.Context(), "SELECT code FROM auth.otp_codes WHERE provider = $1 AND identifier = $2 AND expires_at > NOW()", dbProvider, identifier).Scan(&storedCode)
			if err == nil && storedCode == stepUpCode {
				ctx.ProjectPool.Exec(r.Context(), "DELETE FROM auth.otp_codes WHERE provider = $1 AND identifier = $2", dbProvider, identifier)
				verified = true
				ctx.StepUpProviders = provSlug
			}
		}
	}

	if verified {
		log.Printf("[ExtractAndValidateStepUp] Step-Up MFA verified for user %s (Provider: %s)", sub, provSlug)
	} else {
		log.Printf("[ExtractAndValidateStepUp] Step-Up MFA verification FAILED for user %s (Provider: %s)", sub, provSlug)
	}
}
