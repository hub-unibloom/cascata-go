package services

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"cascata-backend/internal/utils"
)

// ============================================================================
// NODE EXECUTORS - Implementações compiladas de cada tipo de nó
// ============================================================================

// HTTPNodeExecutor executa chamadas HTTP com retry, auth e validação
type HTTPNodeExecutor struct {
	URL            string
	Method         string
	Headers        map[string]string
	BodyTemplate   interface{}
	QueryParams    map[string]string
	AuthMode       string
	AuthToken      string
	AuthUser       string
	AuthPass       string
	Timeout        time.Duration
	RetryCount     int
	FollowRedirect bool
	
	// Slots
	OutputSlot int
	ErrorSlot  int
}

func (n *HTTPNodeExecutor) GetOutputSlot() int { return n.OutputSlot }
func (n *HTTPNodeExecutor) GetErrorSlot() int  { return n.ErrorSlot }

func (n *HTTPNodeExecutor) Execute(ctx *FlowContext) error {
	targetURL := n.resolveURL(ctx)
	
	// Validação de segurança
	if err := n.validateTargetURL(targetURL); err != nil {
		ctx.Vars[n.OutputSlot] = map[string]interface{}{"__error": true, "message": err.Error()}
		return nil // HTTP node não retorna erro, coloca __error no output
	}

	method := n.Method
	if method == "" {
		method = "POST"
	}

	// Resolver body
	var bodyReader io.Reader
	if method != "GET" && n.BodyTemplate != nil {
		resolvedBody := resolveTemplateCtx(n.BodyTemplate, ctx)
		bodyBytes, _ := json.Marshal(resolvedBody)
		bodyReader = bytes.NewReader(bodyBytes)
	}

	// Criar request
	req, err := http.NewRequestWithContext(ctx.Context, method, targetURL, bodyReader)
	if err != nil {
		ctx.Vars[n.OutputSlot] = map[string]interface{}{"__error": true, "message": err.Error()}
		return nil
	}

	// Headers padrão + customizados
	req.Header.Set("Content-Type", "application/json")
	for k, v := range n.Headers {
		req.Header.Set(k, resolveStringCtx(v, ctx))
	}

	// Autenticação
	if err := n.applyAuth(req, ctx); err != nil {
		ctx.Vars[n.OutputSlot] = map[string]interface{}{"__error": true, "message": err.Error()}
		return nil
	}

	// Query params
	if len(n.QueryParams) > 0 {
		q := req.URL.Query()
		for k, v := range n.QueryParams {
			q.Set(k, resolveStringCtx(v, ctx))
		}
		req.URL.RawQuery = q.Encode()
	}

	// Executar com retry
	timeout := n.Timeout
	if timeout == 0 {
		timeout = 15 * time.Second
	}

	client := &http.Client{
		Timeout: timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if !n.FollowRedirect {
				return http.ErrUseLastResponse
			}
			if len(via) >= 10 {
				return fmt.Errorf("too many redirects")
			}
			return nil
		},
	}

	var lastErr error
	for attempt := 0; attempt <= n.RetryCount; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt) * time.Second)
		}

		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		defer resp.Body.Close()

		bodyBytes, _ := io.ReadAll(resp.Body)

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			// Sucesso - parsear response
			var result interface{}
			contentType := resp.Header.Get("Content-Type")
			if strings.Contains(contentType, "application/json") {
				json.Unmarshal(bodyBytes, &result)
			} else {
				result = string(bodyBytes)
			}
			ctx.Vars[n.OutputSlot] = result
			return nil
		}

		lastErr = fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(bodyBytes))
		if resp.StatusCode >= 400 && resp.StatusCode < 500 {
			// 4xx não faz retry
			break
		}
	}

	// Falhou após todos os retries
	ctx.Vars[n.OutputSlot] = map[string]interface{}{
		"__error":   true,
		"message":   lastErr.Error(),
		"attempts":  n.RetryCount + 1,
	}
	return nil
}

func (n *HTTPNodeExecutor) resolveURL(ctx *FlowContext) string {
	return resolveStringCtx(n.URL, ctx)
}

func (n *HTTPNodeExecutor) validateTargetURL(targetURL string) error {
	u, err := url.Parse(targetURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}

	hostname := strings.ToLower(u.Hostname())
	blockedHosts := []string{
		"localhost", "127.0.0.1", "::1", "0.0.0.0",
		"db", "postgres", "dragonfly", "redis", "internal",
	}

	for _, blocked := range blockedHosts {
		if hostname == blocked || strings.HasPrefix(hostname, blocked+".") {
			return fmt.Errorf("access to internal host %s is blocked", hostname)
		}
	}

	// Verificar IPs privados
	privateIPRegex := regexp.MustCompile(`^(10\.|172\.(1[6-9]|2[0-9]|3[01])\.|192\.168\.|127\.)`)
	if privateIPRegex.MatchString(hostname) {
		return fmt.Errorf("access to private IP range is blocked")
	}

	return nil
}

func (n *HTTPNodeExecutor) applyAuth(req *http.Request, ctx *FlowContext) error {
	switch n.AuthMode {
	case "bearer":
		token := resolveStringCtx(n.AuthToken, ctx)
		if strings.HasPrefix(token, "vault://") {
			resolved, err := resolveCompiledVaultRef(ctx, token)
			if err != nil {
				return err
			}
			token = resolved
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}

	case "apikey", "basic":
		user := resolveStringCtx(n.AuthUser, ctx)
		pass := resolveStringCtx(n.AuthPass, ctx)
		if strings.HasPrefix(user, "vault://") {
			resolved, err := resolveCompiledVaultRef(ctx, user)
			if err != nil {
				return err
			}
			user = resolved
		}
		if strings.HasPrefix(pass, "vault://") {
			resolved, err := resolveCompiledVaultRef(ctx, pass)
			if err != nil {
				return err
			}
			pass = resolved
		}
		if user != "" || pass != "" {
			req.Header.Set("Authorization", "Basic "+basicAuth(user, pass))
		}
	}
	return nil
}

func resolveCompiledVaultRef(ctx *FlowContext, ref string) (string, error) {
	if ctx.CryptoSvc == nil {
		return "", fmt.Errorf("crypto service unavailable for vault ref")
	}

	parts := strings.Split(strings.TrimPrefix(ref, "vault://"), "/")
	if len(parts) >= 2 {
		returned, _, err := NewVaultService(ctx.CryptoSvc).Resolve(ctx.Context, parts[0], parts[1], VaultPurposeAutomation)
		return returned, err
	}

	secretName := strings.TrimPrefix(ref, "vault://")
	returned, _, err := NewVaultService(ctx.CryptoSvc).Resolve(ctx.Context, ctx.ProjectSlug, secretName, VaultPurposeAutomation)
	return returned, err
}

func basicAuth(user, pass string) string {
	return base64Encode(user + ":" + pass)
}

func base64Encode(s string) string {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	var result strings.Builder
	input := []byte(s)
	
	for i := 0; i < len(input); i += 3 {
		b := []int{0, 0, 0}
		for j := 0; j < 3 && i+j < len(input); j++ {
			b[j] = int(input[i+j])
		}
		
		n := (b[0] << 16) | (b[1] << 8) | b[2]
		
		for j := 0; j < 4; j++ {
			if i*8+j*6 < len(input)*8 {
				result.WriteByte(alphabet[(n>>uint(18-j*6))&0x3F])
			} else {
				result.WriteByte('=')
			}
		}
	}
	
	return result.String()
}

// BuildHTTPNode cria um HTTPNodeExecutor a partir da configuração
func BuildHTTPNode(config map[string]interface{}, compiler *Compiler) (NodeExecutor, error) {
	node := &HTTPNodeExecutor{
		OutputSlot: compiler.allocSlot(),
		ErrorSlot:  -1,
	}

	if v, ok := config["url"].(string); ok {
		node.URL = v
	}
	if v, ok := config["method"].(string); ok {
		node.Method = strings.ToUpper(v)
	}
	if v, ok := config["timeout"].(float64); ok {
		node.Timeout = time.Duration(v) * time.Millisecond
	}
	if v, ok := config["retries"].(float64); ok {
		node.RetryCount = int(v)
	}
	if v, ok := config["follow_redirects"].(bool); ok {
		node.FollowRedirect = v
	} else {
		node.FollowRedirect = true
	}
	if v, ok := config["auth"].(string); ok {
		node.AuthMode = v
	}
	if v, ok := config["auth_token"].(string); ok {
		node.AuthToken = v
	}
	if v, ok := config["auth_user"].(string); ok {
		node.AuthUser = v
	}
	if v, ok := config["auth_pass"].(string); ok {
		node.AuthPass = v
	}
	if v, ok := config["body"]; ok {
		node.BodyTemplate = v
	}
	if v, ok := config["query_params"].(map[string]interface{}); ok {
		node.QueryParams = make(map[string]string)
		for k, val := range v {
			node.QueryParams[k] = fmt.Sprintf("%v", val)
		}
	}
	if v, ok := config["headers"].(map[string]interface{}); ok {
		node.Headers = make(map[string]string)
		for k, val := range v {
			node.Headers[k] = fmt.Sprintf("%v", val)
		}
	}

	return node, nil
}

// ============================================================================
// SQL Node Executor
// ============================================================================

// SQLNodeExecutor executa queries SQL com segurança RLS
type SQLNodeExecutor struct {
	SQL           string
	Params        []string // refs para VarPool slots
	ReadOnly      bool
	TimeoutMs     int      // Timeout configurável em ms (padrão: 8000)
	OutputSlot    int
	ErrorSlot     int
}

func (n *SQLNodeExecutor) GetOutputSlot() int { return n.OutputSlot }
func (n *SQLNodeExecutor) GetErrorSlot() int  { return n.ErrorSlot }

var sqlForbiddenPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i);\s*-{2,}`),
	regexp.MustCompile(`(?i)COPY\s+`),
	regexp.MustCompile(`(?i)pg_read_file\s*\(`),
	regexp.MustCompile(`(?i)pg_write_file\s*\(`),
	regexp.MustCompile(`(?i)pg_ls_dir\s*\(`),
	regexp.MustCompile(`(?i)pg_terminate_backend\s*\(`),
	regexp.MustCompile(`(?i)pg_cancel_backend\s*\(`),
	regexp.MustCompile(`(?i)pg_reload_conf\s*\(`),
	regexp.MustCompile(`(?i)ALTER\s+SYSTEM\s+`),
	regexp.MustCompile(`(?i)CREATE\s+OR\s+REPLACE\s+FUNCTION`),
	regexp.MustCompile(`(?i)\bDO\s+\$\$`),
	regexp.MustCompile(`(?i)PERFORM\s+dblink`),
}

func (n *SQLNodeExecutor) Execute(ctx *FlowContext) error {
	// Verificar patterns proibidos
	for _, pattern := range sqlForbiddenPatterns {
		if pattern.MatchString(n.SQL) {
			return fmt.Errorf("security violation: SQL pattern blocked")
		}
	}

	// Verificar se é SELECT para READ ONLY
	isSelect := regexp.MustCompile(`^\s*SELECT\s+`).MatchString(n.SQL)
	readOnly := n.ReadOnly || isSelect

	// Configurar timeout do nó (padrão: 8000ms)
	sqlTimeout := 8 * time.Second
	if n.TimeoutMs > 0 {
		sqlTimeout = time.Duration(n.TimeoutMs) * time.Millisecond
	}

	// Preparar parâmetros
	params := make([]interface{}, len(n.Params))
	for i, p := range n.Params {
		params[i] = resolveVarCtx(p, ctx)
	}

	// Setup RLS
	role := ctx.UserRole
	if role == "" {
		role = "authenticated"
	}
	
	// Whitelist de roles permitidos
	allowedRoles := map[string]bool{
		"anon": true, "authenticated": true, 
		"service_role": true, "cascata_api_role": true,
	}
	if !allowedRoles[role] {
		role = "authenticated"
	}

	claims := ctx.JWTClaims
	if claims == nil {
		claims = make(map[string]interface{})
	}

	quoteLocal := func(s interface{}) string {
		if s == nil {
			return "''"
		}
		str := fmt.Sprintf("%v", s)
		return "'" + strings.ReplaceAll(str, "'", "''") + "'"
	}

	// Executar no project pool
	pool, ok := ctx.ProjectPool.(*pgxpool.Pool)
	if !ok || pool == nil {
		return fmt.Errorf("no database pool available")
	}

	conn, err := pool.Acquire(ctx.Context)
	if err != nil {
		return fmt.Errorf("failed to acquire connection: %w", err)
	}
	defer conn.Release()

	// Begin transaction
	tx, err := conn.Begin(ctx.Context)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback(ctx.Context)

	// READ ONLY se for SELECT
	if readOnly {
		_, _ = tx.Exec(ctx.Context, "SET TRANSACTION READ ONLY")
	}

	// Setup RLS
	setupSQL := fmt.Sprintf(`
		SET LOCAL ROLE %s;
		SET LOCAL statement_timeout = '%dms';
		SET LOCAL "request.jwt.claim.sub" = %s;
		SET LOCAL "request.jwt.claim.role" = %s;
		SET LOCAL "request.jwt.claim.email" = %s;
	`, role, sqlTimeout.Milliseconds(), 
		quoteLocal(claims["sub"]),
		quoteLocal(claims["role"]),
		quoteLocal(claims["email"]))

	_, err = tx.Exec(ctx.Context, setupSQL)
	if err != nil {
		return fmt.Errorf("failed to setup RLS: %w", err)
	}

	// Executar query
	rows, err := tx.Query(ctx.Context, n.SQL, params...)
	if err != nil {
		return fmt.Errorf("query failed: %w", err)
	}
	defer rows.Close()

	// Ler resultados
	var results []map[string]interface{}
	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			continue
		}
		row := make(map[string]interface{})
		for i, fd := range rows.FieldDescriptions() {
			row[string(fd.Name)] = utils.PurifyPgxValue(values[i])
		}
		results = append(results, row)
	}

	// Commit
	if err := tx.Commit(ctx.Context); err != nil {
		return fmt.Errorf("commit failed: %w", err)
	}

	ctx.Vars[n.OutputSlot] = results
	return nil
}

// BuildSQLNode cria um SQLNodeExecutor
func BuildSQLNode(config map[string]interface{}, compiler *Compiler) (NodeExecutor, error) {
	node := &SQLNodeExecutor{
		OutputSlot: compiler.allocSlot(),
		ErrorSlot:  -1,
		Params:     []string{},
		TimeoutMs:  8000, // Default: 8000ms
	}

	if v, ok := config["sql"].(string); ok {
		node.SQL = v
	}
	if v, ok := config["readonly"].(bool); ok {
		node.ReadOnly = v
	}
	if v, ok := config["timeout_ms"].(float64); ok {
		node.TimeoutMs = int(v)
	}
	if params, ok := config["params"].([]interface{}); ok {
		for _, p := range params {
			node.Params = append(node.Params, fmt.Sprintf("%v", p))
		}
	}

	return node, nil
}
