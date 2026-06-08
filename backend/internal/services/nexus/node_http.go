package nexus

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// HTTPComponent executa chamadas HTTP externas seguras.
// Inclui uma allowlist de domínios para prevenção de SSRF e vazamento de dados.
type HTTPComponent struct {
	*BaseComponent
	allowlist []string
	client    *http.Client
}

// NewHTTPComponent cria uma nova instância de HTTPComponent.
func NewHTTPComponent(id string, allowlist []string) *HTTPComponent {
	return &HTTPComponent{
		BaseComponent: NewBaseComponent(id, TypeHTTP,
			[]PortDefinition{{Name: "in", DataType: "object", Required: true}},
			[]PortDefinition{
				{Name: "out", DataType: "object", Required: true},
				{Name: "error", DataType: "error", Required: false},
			},
		),
		allowlist: allowlist,
		client: &http.Client{
			Timeout: 15 * time.Second, // Timeout base
		},
	}
}

// Process executa a chamada HTTP.
func (c *HTTPComponent) Process(ctx context.Context, ip *InformationPacket, state *NexusState) (map[string][]*InformationPacket, error) {
	c.SetStatus(StatusProcessing)

	// 1. Extração e interpolação da URL
	urlTpl, ok := c.config.Settings["url"].(string)
	if !ok || urlTpl == "" {
		return c.handleError(ip, fmt.Errorf("nexus[http]: missing 'url' setting"))
	}

	targetURL, err := state.InterpolateString(urlTpl, ip.Data)
	if err != nil {
		return c.handleError(ip, fmt.Errorf("interpolation error: %w", err))
	}

	// Adicionar Query Params se existirem
	if queryParams, ok := c.config.Settings["queryParams"].(map[string]interface{}); ok && len(queryParams) > 0 {
		parsedURL, err := url.Parse(targetURL)
		if err != nil {
			return c.handleError(ip, fmt.Errorf("failed to parse url for query params: %w", err))
		}
		q := parsedURL.Query()
		for k, v := range queryParams {
			if vStr, ok := v.(string); ok && vStr != "" {
				interpolatedVal, _ := state.InterpolateString(vStr, ip.Data)
				q.Set(k, interpolatedVal)
			}
		}
		parsedURL.RawQuery = q.Encode()
		targetURL = parsedURL.String()
	}

	// Validação de SSRF e Allowlist
	if err := c.validateURL(targetURL); err != nil {
		return c.handleError(ip, err)
	}

	// Método
	method, ok := c.config.Settings["method"].(string)
	if !ok {
		method = "GET"
	}
	method = strings.ToUpper(method)

	// 2. Body
	var reqBody io.Reader
	bodyType, _ := c.config.Settings["bodyType"].(string)
	
	// Fallback para comportamento legado se "body" estiver setado diretamente
	if bodyType == "" {
		if _, ok := c.config.Settings["body"]; ok {
			bodyType = "raw"
			c.config.Settings["bodyRaw"] = c.config.Settings["body"]
		}
	}

	contentTypeHeader := ""

	if bodyType != "none" && bodyType != "" {
		switch bodyType {
		case "json":
			var bodyData interface{}
			if bodyJSON, ok := c.config.Settings["bodyJSON"]; ok {
				if bodyJSONStr, ok := bodyJSON.(string); ok && bodyJSONStr != "" {
					var parsed interface{}
					if err := json.Unmarshal([]byte(bodyJSONStr), &parsed); err == nil {
						bodyData = parsed
					} else {
						bodyData = bodyJSONStr
					}
				} else {
					bodyData = bodyJSON
				}
			}

			if bodyData != nil {
				resolvedData := state.ResolveAny(bodyData)
				var bodyBytes []byte
				if strData, ok := resolvedData.(string); ok {
					interpolatedStr, _ := state.InterpolateString(strData, ip.Data)
					bodyBytes = []byte(interpolatedStr)
				} else {
					bodyBytes, _ = json.Marshal(resolvedData)
				}
				reqBody = bytes.NewBuffer(bodyBytes)
				contentTypeHeader = "application/json"
			}

		case "raw":
			if bodyRaw, ok := c.config.Settings["bodyRaw"].(string); ok && bodyRaw != "" {
				interpolatedBody, _ := state.InterpolateString(bodyRaw, ip.Data)
				reqBody = bytes.NewBufferString(interpolatedBody)
				contentTypeHeader = "text/plain"
			}

		case "form_data":
			if formData, ok := c.config.Settings["bodyFormData"].(map[string]interface{}); ok {
				valValues := url.Values{}
				for k, v := range formData {
					if vStr, ok := v.(string); ok {
						interpolatedVal, _ := state.InterpolateString(vStr, ip.Data)
						valValues.Set(k, interpolatedVal)
					}
				}
				reqBody = strings.NewReader(valValues.Encode())
				contentTypeHeader = "application/x-www-form-urlencoded"
			}
		}
	}

	// Criação do Request
	req, err := http.NewRequestWithContext(ctx, method, targetURL, reqBody)
	if err != nil {
		return c.handleError(ip, fmt.Errorf("failed to create request: %w", err))
	}

	if contentTypeHeader != "" {
		req.Header.Set("Content-Type", contentTypeHeader)
	}

	// 3. Autenticação
	authType, _ := c.config.Settings["authType"].(string)
	if authType != "" && authType != "none" {
		if authConfig, ok := c.config.Settings["authConfig"].(map[string]interface{}); ok {
			switch authType {
			case "bearer":
				if token, ok := authConfig["bearerToken"].(string); ok && token != "" {
					interpolatedToken, _ := state.InterpolateString(token, ip.Data)
					if !strings.HasPrefix(strings.ToLower(interpolatedToken), "bearer ") {
						interpolatedToken = "Bearer " + interpolatedToken
					}
					req.Header.Set("Authorization", interpolatedToken)
				}
			case "basic":
				user, _ := authConfig["basicUser"].(string)
				pass, _ := authConfig["basicPass"].(string)
				interpolatedUser, _ := state.InterpolateString(user, ip.Data)
				interpolatedPass, _ := state.InterpolateString(pass, ip.Data)
				req.SetBasicAuth(interpolatedUser, interpolatedPass)
			case "apikey":
				keyName, _ := authConfig["apiKeyName"].(string)
				keyValue, _ := authConfig["apiKeyValue"].(string)
				if keyName != "" && keyValue != "" {
					interpolatedKeyName, _ := state.InterpolateString(keyName, ip.Data)
					interpolatedKeyValue, _ := state.InterpolateString(keyValue, ip.Data)
					req.Header.Set(interpolatedKeyName, interpolatedKeyValue)
				}
			}
		}
	}

	// 4. Headers customizados
	if headers, ok := c.config.Settings["headers"].(map[string]interface{}); ok {
		for k, v := range headers {
			if vStr, ok := v.(string); ok {
				interpolatedVal, _ := state.InterpolateString(vStr, ip.Data)
				req.Header.Set(k, interpolatedVal)
			}
		}
	}

	// Execução
	resp, err := c.client.Do(req)
	if err != nil {
		return c.handleError(ip, fmt.Errorf("http request failed: %w", err))
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	// Tenta parsear JSON
	var respJSON interface{}
	if err := json.Unmarshal(respBody, &respJSON); err != nil {
		respJSON = string(respBody) // Fallback para string se não for JSON
	}

	c.SetStatus(StatusSuccess)

	outIp := ip.Clone()
	outIp.Data["http_response"] = map[string]interface{}{
		"status_code": resp.StatusCode,
		"headers":     resp.Header,
		"body":        respJSON,
	}

	// 5. Mapear outputs para o root do outIp.Data
	if mappings, ok := c.config.Settings["outputMappings"].([]interface{}); ok {
		for _, m := range mappings {
			if mMap, ok := m.(map[string]interface{}); ok {
				sourcePath, _ := mMap["sourcePath"].(string)
				outputName, _ := mMap["outputName"].(string)
				if sourcePath != "" && outputName != "" {
					if val, found := resolveNestedPath(respJSON, sourcePath); found {
						outIp.Data[outputName] = val
					}
				}
			}
		}
	}

	return EmitSingle("out", outIp), nil
}

// validateURL verifica contra a allowlist (prevenção de SSRF)
func (c *HTTPComponent) validateURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid url: %w", err)
	}

	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("invalid scheme: %s (only http/https allowed)", parsed.Scheme)
	}

	hostname := parsed.Hostname()
	
	// Prevenção de loopback / internal networks
	if hostname == "localhost" || hostname == "127.0.0.1" || strings.HasPrefix(hostname, "10.") || strings.HasPrefix(hostname, "192.168.") || strings.HasPrefix(hostname, "172.") {
		return fmt.Errorf("security violation: internal networks not allowed")
	}

	if len(c.allowlist) == 0 {
		return nil // Se a allowlist estiver vazia, aceita tudo (menos internos)
	}

	for _, allowed := range c.allowlist {
		if hostname == allowed || strings.HasSuffix(hostname, "."+allowed) {
			return nil
		}
	}

	return fmt.Errorf("security violation: domain %s not in allowlist", hostname)
}

func (c *HTTPComponent) handleError(ip *InformationPacket, err error) (map[string][]*InformationPacket, error) {
	c.SetStatus(StatusError)
	if c.config.ErrorStrategy == ErrorBypass {
		return EmitEmpty(), nil
	}
	errIp := ip.Clone()
	errIp.Data["error"] = err.Error()
	if c.config.ErrorStrategy == ErrorFallback {
		return EmitSingle("error", errIp), nil
	}
	return nil, err
}
