package services

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// ============================================================================
// HTTP NODE EXECUTOR - ENTERPRISE GRADE
// Suporte a: multipart/form-data, OAuth2, File Upload, Secrets Vault
// ============================================================================

type HTTPBodyType string

const (
	BodyTypeJSON       HTTPBodyType = "json"
	BodyTypeFormData   HTTPBodyType = "form_data"
	BodyTypeRaw        HTTPBodyType = "raw"
	BodyTypeBinary     HTTPBodyType = "binary"
	BodyTypeNone       HTTPBodyType = "none"
)

type HTTPAuthType string

const (
	AuthTypeNone       HTTPAuthType = "none"
	AuthTypeBearer     HTTPAuthType = "bearer"
	AuthTypeBasic      HTTPAuthType = "basic"
	AuthTypeApiKey     HTTPAuthType = "apikey"
	AuthTypeOAuth2     HTTPAuthType = "oauth2"
	AuthTypeVaultRef   HTTPAuthType = "vault_ref"
)

// HTTPFormField representa um campo de form-data
type HTTPFormField struct {
	Key       string `json:"key"`
	Value     string `json:"value,omitempty"`      // Para text fields
	Type      string `json:"type"`               // "text" ou "file"
	FileRef   string `json:"file_ref,omitempty"` // Referência a arquivo do storage
	FileData  []byte `json:"-"`                  // Dados do arquivo (runtime)
	FileName  string `json:"file_name,omitempty"` // Nome do arquivo
	ContentType string `json:"content_type,omitempty"`
}

// HTTPNodeConfig - Configuração completa do HTTP Node
type HTTPNodeConfig struct {
	URL             string            `json:"url"`
	Method          string            `json:"method"`
	BodyType        HTTPBodyType      `json:"body_type"`
	BodyJSON        interface{}       `json:"body_json,omitempty"`
	BodyRaw         string            `json:"body_raw,omitempty"`
	FormFields      []HTTPFormField   `json:"form_fields,omitempty"`
	Headers         map[string]string `json:"headers,omitempty"`
	QueryParams     map[string]string `json:"query_params,omitempty"`
	
	// Autenticação
	AuthType        HTTPAuthType      `json:"auth_type"`
	AuthConfig      HTTPAuthConfig    `json:"auth_config,omitempty"`
	
	// Configurações avançadas
	Timeout         int               `json:"timeout_ms,omitempty"`     // Default: 30000ms
	RetryCount      int               `json:"retry_count,omitempty"`    // Default: 3
	RetryDelay      int               `json:"retry_delay_ms,omitempty"` // Default: 1000ms
	RetryBackoff    string            `json:"retry_backoff,omitempty"`  // "linear" | "exponential"
	FollowRedirects bool              `json:"follow_redirects,omitempty"` // Default: true
	ValidateSSL     bool              `json:"validate_ssl,omitempty"`     // Default: true
	
	// Resposta
	ResponseFormat  string            `json:"response_format,omitempty"` // "auto" | "json" | "text" | "binary"

	// Mapeamento de campos da resposta para outputs amigáveis
	OutputMappings  []HTTPOutputMapping `json:"output_mappings,omitempty"`
}

type HTTPOutputMapping struct {
	SourcePath string `json:"sourcePath"`
	OutputName string `json:"outputName"`
	OutputType string `json:"outputType"`
}

// HTTPAuthConfig configuração de autenticação
type HTTPAuthConfig struct {
	// Bearer
	BearerToken     string `json:"bearer_token,omitempty"`
	TokenVaultRef   string `json:"token_vault_ref,omitempty"` // vault://project/secret
	
	// Basic
	BasicUser       string `json:"basic_user,omitempty"`
	BasicPass       string `json:"basic_pass,omitempty"`
	UserVaultRef    string `json:"user_vault_ref,omitempty"`
	PassVaultRef    string `json:"pass_vault_ref,omitempty"`
	
	// API Key
	APIKeyName      string `json:"api_key_name,omitempty"`
	APIKeyValue     string `json:"api_key_value,omitempty"`
	APIKeyInHeader  bool   `json:"api_key_in_header,omitempty"` // true=header, false=query
	APIKeyVaultRef  string `json:"api_key_vault_ref,omitempty"`
	
	// OAuth2
	OAuth2ClientID     string `json:"oauth2_client_id,omitempty"`
	OAuth2ClientSecret string `json:"oauth2_client_secret,omitempty"`
	OAuth2TokenURL     string `json:"oauth2_token_url,omitempty"`
	OAuth2Scopes       string `json:"oauth2_scopes,omitempty"`
	OAuth2GrantType    string `json:"oauth2_grant_type,omitempty"` // "client_credentials" | "password"
	ClientIDVaultRef   string `json:"client_id_vault_ref,omitempty"`
	ClientSecretVaultRef string `json:"client_secret_vault_ref,omitempty"`
}

// HTTPNodeExecutorV2 executa chamadas HTTP enterprise-grade
type HTTPNodeExecutorV2 struct {
	Config     HTTPNodeConfig
	OutputSlot int
	ErrorSlot  int
	cryptoSvc  *CryptoService
}

func (n *HTTPNodeExecutorV2) GetOutputSlot() int { return n.OutputSlot }
func (n *HTTPNodeExecutorV2) GetErrorSlot() int  { return n.ErrorSlot }

func (n *HTTPNodeExecutorV2) Execute(ctx *FlowContext) error {
	// Resolver URL com templates
	targetURL := n.resolveTemplate(n.Config.URL, ctx)
	
	// Validação de segurança
	if err := n.validateURL(targetURL); err != nil {
		n.setError(ctx, err)
		return nil
	}

	// Obter token OAuth2 se necessário
	if n.Config.AuthType == AuthTypeOAuth2 {
		if err := n.refreshOAuth2Token(ctx); err != nil {
			n.setError(ctx, fmt.Errorf("oauth2 failed: %w", err))
			return nil
		}
	}

	// Executar com retry
	result, err := n.executeWithRetry(targetURL, ctx)
	if err != nil {
		n.setError(ctx, err)
		return nil
	}

	// Result already comes in the correct format from executeWithRetry
	ctx.Vars[n.OutputSlot] = result
	return nil
}

func (n *HTTPNodeExecutorV2) executeWithRetry(targetURL string, ctx *FlowContext) (interface{}, error) {
	timeout := time.Duration(n.Config.Timeout) * time.Millisecond
	if timeout == 0 {
		timeout = 30 * time.Second
	}

	retryCount := n.Config.RetryCount
	if retryCount < 0 {
		retryCount = 0
	}
	if retryCount > 10 {
		retryCount = 10 // Cap máximo
	}

	retryDelay := time.Duration(n.Config.RetryDelay) * time.Millisecond
	if retryDelay == 0 {
		retryDelay = time.Second
	}

	client := n.createHTTPClient(timeout)

	var lastErr error
	for attempt := 0; attempt <= retryCount; attempt++ {
		if attempt > 0 {
			delay := n.calculateRetryDelay(attempt, retryDelay)
			time.Sleep(delay)
		}

		req, err := n.buildRequest(targetURL, ctx)
		if err != nil {
			return nil, err
		}

		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}

		bodyBytes, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		// Build response headers map
		headers := make(map[string]string)
		for k, v := range resp.Header {
			if len(v) > 0 {
				headers[k] = v[0]
			}
		}

		// Parse body
		parsedBody, parseErr := n.parseResponse(resp, bodyBytes)
		if parseErr != nil {
			parsedBody = string(bodyBytes) // Fallback to raw string
		}

		// Build complete result object
		result := map[string]interface{}{
			"status":     resp.StatusCode,
			"statusText": resp.Status,
			"headers":    headers,
			"body":       parsedBody,
		}

		// Apply output mappings: inject mapped fields into body for easy access
		if len(n.Config.OutputMappings) > 0 {
			if bodyMap, ok := parsedBody.(map[string]interface{}); ok {
				for _, mapping := range n.Config.OutputMappings {
					val := n.extractValueByPath(bodyMap, mapping.SourcePath)
					if val != nil {
						bodyMap[mapping.OutputName] = val
					}
				}
			}
		}

		// Sucesso
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return result, nil
		}

		// Erro 4xx - não retry
		if resp.StatusCode >= 400 && resp.StatusCode < 500 {
			return result, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(bodyBytes))
		}

		lastErr = fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(bodyBytes))
	}

	return nil, fmt.Errorf("failed after %d attempts: %w", retryCount+1, lastErr)
}

func (n *HTTPNodeExecutorV2) extractValueByPath(data map[string]interface{}, path string) interface{} {
	parts := strings.Split(path, ".")
	current := interface{}(data)
	
	for _, part := range parts {
		if currentMap, ok := current.(map[string]interface{}); ok {
			current = currentMap[part]
		} else {
			return nil
		}
	}
	
	return current
}

func (n *HTTPNodeExecutorV2) buildRequest(targetURL string, ctx *FlowContext) (*http.Request, error) {
	method := strings.ToUpper(n.Config.Method)
	if method == "" {
		method = "GET"
	}

	var bodyReader io.Reader
	contentType := ""

	// Construir body baseado no tipo
	switch n.Config.BodyType {
	case BodyTypeJSON:
		if n.Config.BodyJSON != nil {
			resolved := n.resolveTemplateRecursive(n.Config.BodyJSON, ctx)
			bodyBytes, _ := json.Marshal(resolved)
			bodyReader = bytes.NewReader(bodyBytes)
			contentType = "application/json"
		}

	case BodyTypeFormData:
		bodyReader, contentType = n.buildMultipartForm(ctx)

	case BodyTypeRaw:
		if n.Config.BodyRaw != "" {
			resolved := n.resolveTemplate(n.Config.BodyRaw, ctx)
			bodyReader = strings.NewReader(resolved)
		}

	case BodyTypeBinary:
		// Para binary, o body deve vir de uma referência de arquivo
		// Usar slot 0 como padrão para binary body (pode ser ajustado via config)
		if len(ctx.Vars) > 0 {
			if data, ok := ctx.Vars[0].([]byte); ok {
				bodyReader = bytes.NewReader(data)
			}
		}
	}

	req, err := http.NewRequestWithContext(ctx.Context, method, targetURL, bodyReader)
	if err != nil {
		return nil, err
	}

	// Headers padrão
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("Accept", "application/json, */*")
	req.Header.Set("User-Agent", "Cascata-Automation/1.0")

	// Headers customizados
	for k, v := range n.Config.Headers {
		resolved := n.resolveTemplate(v, ctx)
		req.Header.Set(k, resolved)
	}

	// Query params
	if len(n.Config.QueryParams) > 0 {
		q := req.URL.Query()
		for k, v := range n.Config.QueryParams {
			resolved := n.resolveTemplate(v, ctx)
			q.Set(k, resolved)
		}
		req.URL.RawQuery = q.Encode()
	}

	// Aplicar autenticação
	if err := n.applyAuth(req, ctx); err != nil {
		return nil, err
	}

	return req, nil
}

func (n *HTTPNodeExecutorV2) buildMultipartForm(ctx *FlowContext) (io.Reader, string) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	for _, field := range n.Config.FormFields {
		key := n.resolveTemplate(field.Key, ctx)

		switch field.Type {
		case "text":
			value := n.resolveTemplate(field.Value, ctx)
			writer.WriteField(key, value)

		case "file":
			// Resolver referência de arquivo
			fileData := n.resolveFileRef(field.FileRef, ctx)
			if fileData != nil {
				filename := field.FileName
				if filename == "" {
					filename = "file"
				}
				filename = n.resolveTemplate(filename, ctx)

				part, _ := writer.CreateFormFile(key, filename)
				part.Write(fileData)
			}
		}
	}

	writer.Close()
	return &body, writer.FormDataContentType()
}

func (n *HTTPNodeExecutorV2) resolveFileRef(ref string, ctx *FlowContext) []byte {
	if ref == "" {
		return nil
	}

	// Suporta: storage://bucket/path, {{var}}, ou raw data
	resolved := n.resolveTemplate(ref, ctx)

	if strings.HasPrefix(resolved, "storage://") {
		// Buscar do storage
		path := strings.TrimPrefix(resolved, "storage://")
		parts := strings.SplitN(path, "/", 2)
		if len(parts) == 2 {
			// Aqui integrar com StorageService
			// Por enquanto retorna nil, implementar quando tiver StorageService
			return nil
		}
	}

	// Tentar buscar de variáveis do contexto usando NodeOutputs
	if slotIdx, ok := ctx.NodeOutputs[resolved]; ok && slotIdx < len(ctx.Vars) {
		if data, ok := ctx.Vars[slotIdx].([]byte); ok {
			return data
		}
	}

	return nil
}

func (n *HTTPNodeExecutorV2) applyAuth(req *http.Request, ctx *FlowContext) error {
	switch n.Config.AuthType {
	case AuthTypeBearer:
		token := n.resolveSecret(n.Config.AuthConfig.BearerToken, 
			n.Config.AuthConfig.TokenVaultRef, ctx)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}

	case AuthTypeBasic:
		user := n.resolveSecret(n.Config.AuthConfig.BasicUser,
			n.Config.AuthConfig.UserVaultRef, ctx)
		pass := n.resolveSecret(n.Config.AuthConfig.BasicPass,
			n.Config.AuthConfig.PassVaultRef, ctx)
		if user != "" || pass != "" {
			auth := base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))
			req.Header.Set("Authorization", "Basic "+auth)
		}

	case AuthTypeApiKey:
		keyValue := n.resolveSecret(n.Config.AuthConfig.APIKeyValue,
			n.Config.AuthConfig.APIKeyVaultRef, ctx)
		keyName := n.Config.AuthConfig.APIKeyName
		
		if n.Config.AuthConfig.APIKeyInHeader {
			req.Header.Set(keyName, keyValue)
		} else {
			q := req.URL.Query()
			q.Set(keyName, keyValue)
			req.URL.RawQuery = q.Encode()
		}

	case AuthTypeOAuth2:
		// Token já foi obtido em refreshOAuth2Token
		token := n.resolveSecret("", n.Config.AuthConfig.TokenVaultRef, ctx)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
	}

	return nil
}

func (n *HTTPNodeExecutorV2) resolveSecret(directValue, vaultRef string, ctx *FlowContext) string {
	// Prioridade: vault ref > direct value
	if vaultRef != "" {
		return n.resolveFromVault(vaultRef, ctx)
	}
	
	if directValue != "" {
		return n.resolveTemplate(directValue, ctx)
	}
	
	return ""
}

func (n *HTTPNodeExecutorV2) resolveFromVault(ref string, ctx *FlowContext) string {
	// Formato: vault://project/secret_name
	if !strings.HasPrefix(ref, "vault://") {
		return ""
	}

	parts := strings.Split(strings.TrimPrefix(ref, "vault://"), "/")
	if len(parts) < 2 {
		return ""
	}

	projectSlug := parts[0]
	secretName := parts[1]
	cryptoSvc := n.cryptoSvc
	if cryptoSvc == nil {
		cryptoSvc = ctx.CryptoSvc
	}

	if cryptoSvc != nil {
		secret, _, err := NewVaultService(cryptoSvc).Resolve(ctx.Context, projectSlug, secretName, VaultPurposeAutomation)
		if err == nil {
			return secret
		}
	}

	return ""
}

func (n *HTTPNodeExecutorV2) refreshOAuth2Token(ctx *FlowContext) error {
	config := n.Config.AuthConfig
	
	clientID := n.resolveSecret(config.OAuth2ClientID, config.ClientIDVaultRef, ctx)
	clientSecret := n.resolveSecret(config.OAuth2ClientSecret, config.ClientSecretVaultRef, ctx)

	if clientID == "" || clientSecret == "" || config.OAuth2TokenURL == "" {
		return fmt.Errorf("oauth2 configuration incomplete")
	}

	grantType := config.OAuth2GrantType
	if grantType == "" {
		grantType = "client_credentials"
	}

	data := url.Values{}
	data.Set("grant_type", grantType)
	data.Set("client_id", clientID)
	data.Set("client_secret", clientSecret)
	
	if config.OAuth2Scopes != "" {
		data.Set("scope", config.OAuth2Scopes)
	}

	req, err := http.NewRequest("POST", config.OAuth2TokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return fmt.Errorf("oauth2 token request failed: %s", string(body))
	}

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return err
	}

	// Armazenar token no contexto usando slot 0 temporariamente
	// Em produção, deveria usar slot dedicado
	n.Config.AuthConfig.TokenVaultRef = "memory://oauth2_token"
	if len(ctx.Vars) > 0 {
		ctx.Vars[0] = tokenResp.AccessToken
	}

	return nil
}

func (n *HTTPNodeExecutorV2) createHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if !n.Config.FollowRedirects {
				return http.ErrUseLastResponse
			}
			if len(via) >= 10 {
				return fmt.Errorf("too many redirects")
			}
			return nil
		},
	}
}

func (n *HTTPNodeExecutorV2) parseResponse(resp *http.Response, body []byte) (interface{}, error) {
	format := n.Config.ResponseFormat
	if format == "" || format == "auto" {
		// Auto-detect
		contentType := resp.Header.Get("Content-Type")
		if strings.Contains(contentType, "application/json") {
			format = "json"
		} else if strings.Contains(contentType, "image/") || strings.Contains(contentType, "application/octet") {
			format = "binary"
		} else {
			format = "text"
		}
	}

	switch format {
	case "json":
		var result interface{}
		if err := json.Unmarshal(body, &result); err != nil {
			return string(body), nil // Fallback to text
		}
		return result, nil
	
	case "binary":
		return map[string]interface{}{
			"__binary":    true,
			"data":        base64.StdEncoding.EncodeToString(body),
			"content_type": resp.Header.Get("Content-Type"),
			"size":        len(body),
		}, nil
	
	case "text":
		return string(body), nil
	
	default:
		return string(body), nil
	}
}

func (n *HTTPNodeExecutorV2) validateURL(targetURL string) error {
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

	// IPs privados
	privateIPRegex := regexp.MustCompile(`^(10\.|172\.(1[6-9]|2[0-9]|3[01])\.|192\.168\.|127\.)`)
	if privateIPRegex.MatchString(hostname) {
		return fmt.Errorf("access to private IP range is blocked")
	}

	// Validar SSL se configurado
	if n.Config.ValidateSSL && u.Scheme != "https" {
		// Apenas warning para HTTP em produção
		// return fmt.Errorf("HTTPS required when validate_ssl is true")
	}

	return nil
}

func (n *HTTPNodeExecutorV2) calculateRetryDelay(attempt int, baseDelay time.Duration) time.Duration {
	if n.Config.RetryBackoff == "exponential" {
		// 2^attempt * base: 1s, 2s, 4s, 8s...
		delay := time.Duration(1 << uint(attempt)) * baseDelay
		if delay > 60*time.Second {
			delay = 60 * time.Second
		}
		return delay
	}
	// Linear
	return time.Duration(attempt) * baseDelay
}

// Template resolution functions
func (n *HTTPNodeExecutorV2) resolveTemplate(s string, ctx *FlowContext) string {
	return resolveStringCtx(s, ctx)
}

func (n *HTTPNodeExecutorV2) resolveTemplateRecursive(v interface{}, ctx *FlowContext) interface{} {
	switch val := v.(type) {
	case string:
		return n.resolveTemplate(val, ctx)
	case map[string]interface{}:
		result := make(map[string]interface{})
		for k, v := range val {
			result[k] = n.resolveTemplateRecursive(v, ctx)
		}
		return result
	case []interface{}:
		result := make([]interface{}, len(val))
		for i, v := range val {
			result[i] = n.resolveTemplateRecursive(v, ctx)
		}
		return result
	default:
		return v
	}
}

func (n *HTTPNodeExecutorV2) setError(ctx *FlowContext, err error) {
	ctx.Vars[n.OutputSlot] = map[string]interface{}{
		"__error": true,
		"message": err.Error(),
		"timestamp": time.Now().Unix(),
	}
}

// BuildHTTPNodeV2 cria executor a partir da configuração
func BuildHTTPNodeV2(config map[string]interface{}, compiler *Compiler, cryptoSvc *CryptoService) (NodeExecutor, error) {
	// Parse config
	configBytes, _ := json.Marshal(config)
	var nodeConfig HTTPNodeConfig
	if err := json.Unmarshal(configBytes, &nodeConfig); err != nil {
		return nil, fmt.Errorf("invalid http config: %w", err)
	}

	node := &HTTPNodeExecutorV2{
		Config:     nodeConfig,
		OutputSlot: compiler.allocSlot(),
		ErrorSlot:  -1,
		cryptoSvc:  cryptoSvc,
	}

	return node, nil
}
