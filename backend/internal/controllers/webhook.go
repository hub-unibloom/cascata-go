package controllers

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"cascata-backend/internal/services"
	"cascata-backend/internal/services/nexus"
	"cascata-backend/internal/types"
	"github.com/go-chi/chi/v5"
)

// ─────────────────────────────────────────────────────────────
// Controller
// ─────────────────────────────────────────────────────────────

type WebhookController struct {
	NexusSvc  *nexus.NexusService
	CryptoSvc *services.CryptoService
	VaultSvc  *services.VaultService
}

// AuthPolicy defines a single authentication barrier in a webhook receiver.
type AuthPolicy struct {
	Method string                 `json:"method"`
	Config map[string]interface{} `json:"config"`
}

// ─────────────────────────────────────────────────────────────
// Management (Admin)
// ─────────────────────────────────────────────────────────────

func (c *WebhookController) SanitizeSlug(slug string) string {
	re := regexp.MustCompile(`[^a-z0-9-_]`)
	return strings.ToLower(re.ReplaceAllString(slug, "-"))
}

func (c *WebhookController) List(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context().Value(types.CascataCtxKey).(*types.CascataRequest)

	rows, err := services.SystemPool.Query(r.Context(),
		`SELECT id, name, path_slug, auth_method, target_type, target_id, is_active, created_at
		 FROM system.webhook_receivers
		 WHERE project_slug = $1
		 ORDER BY created_at DESC`,
		ctx.Project.Slug)
	if err != nil {
		log.Printf("[Webhook] Failed to list webhook receivers: %v", err)
		writeError(w, 500, "Failed to list webhook receivers")
		return
	}
	defer rows.Close()

	receivers := []map[string]interface{}{}
	for rows.Next() {
		var id, name, path, auth, targetType, targetId string
		var isActive bool
		var createdAt time.Time
		if err := rows.Scan(&id, &name, &path, &auth, &targetType, &targetId, &isActive, &createdAt); err != nil {
			log.Printf("[Webhook:List] scan error: %v", err)
			continue
		}
		receivers = append(receivers, map[string]interface{}{
			"id":          id,
			"name":        name,
			"path_slug":   path,
			"auth_method": auth,
			"target_type": targetType,
			"target_id":   targetId,
			"is_active":   isActive,
			"created_at":  createdAt,
		})
	}
	if err := rows.Err(); err != nil {
		log.Printf("[Webhook] Row iteration error: %v", err)
		writeError(w, 500, "Row iteration error")
		return
	}

	writeJSON(w, 200, receivers)
}

func (c *WebhookController) Create(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context().Value(types.CascataCtxKey).(*types.CascataRequest)

	var body struct {
		Name       string `json:"name"`
		PathSlug   string `json:"path_slug"`
		AuthMethod string `json:"auth_method"`
		SecretKey  string `json:"secret_key"`
		TargetType string `json:"target_type"`
		TargetId   string `json:"target_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		log.Printf("[Webhook] Invalid request body: %v", err)
		writeError(w, 400, "Invalid request body")
		return
	}

	pathSlug := c.SanitizeSlug(body.PathSlug)
	if pathSlug == "" {
		writeError(w, 400, "Invalid path slug")
		return
	}

	encryptedSecret := ""
	if body.SecretKey != "" {
		enc, err := c.CryptoSvc.Encrypt("webhook_auth", body.SecretKey)
		if err != nil {
			log.Printf("[Webhook] Failed to encrypt secret key: %v", err)
			writeError(w, 500, "Failed to encrypt secret key")
			return
		}
		encryptedSecret = enc
	}

	var id string
	err := services.SystemPool.QueryRow(r.Context(),
		`INSERT INTO system.webhook_receivers (project_slug, name, path_slug, auth_method, secret_key, target_type, target_id)
		 VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`,
		ctx.Project.Slug, body.Name, pathSlug, body.AuthMethod, encryptedSecret, body.TargetType, body.TargetId,
	).Scan(&id)
	if err != nil {
		log.Printf("[Webhook] Failed to create webhook receiver: %v", err)
		writeError(w, 500, "Failed to create webhook receiver")
		return
	}

	writeJSON(w, 201, map[string]string{"id": id})
}

func (c *WebhookController) Delete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context().Value(types.CascataCtxKey).(*types.CascataRequest)
	id := chi.URLParam(r, "id")

	tag, err := services.SystemPool.Exec(r.Context(),
		"DELETE FROM system.webhook_receivers WHERE id = $1 AND project_slug = $2",
		id, ctx.Project.Slug)
	if err != nil {
		log.Printf("[Webhook] Failed to delete webhook receiver: %v", err)
		writeError(w, 500, "Failed to delete webhook receiver")
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, 404, "Webhook receiver not found")
		return
	}

	writeJSON(w, 200, map[string]bool{"success": true})
}

// ─────────────────────────────────────────────────────────────
// Execution (Public Gateway)
// ─────────────────────────────────────────────────────────────

// WebhookEnvelope is the rich context envelope delivered to the Nexus.
// Every field is intentional — Nexus automation nodes can reference any of
// them directly without extra HTTP calls or guesswork.
type WebhookEnvelope struct {
	// ── Identity ──────────────────────────────────────────────
	ReceiverID   string `json:"receiver_id"`
	ProjectSlug  string `json:"project_slug"`
	PathSlug     string `json:"path_slug"`
	BranchName   string `json:"branch_name"`

	// ── Timing ────────────────────────────────────────────────
	ReceivedAt   time.Time `json:"received_at"`
	ReceivedUnix int64     `json:"received_unix"`

	// ── Request Metadata ──────────────────────────────────────
	Method       string `json:"method"`
	URL          string `json:"url"`
	Path         string `json:"path"`
	RawQuery     string `json:"raw_query"`
	QueryParams  map[string]string `json:"query_params"`
	Protocol     string `json:"protocol"`

	// ── Network / Origin ──────────────────────────────────────
	ClientIP     string `json:"client_ip"`
	UserAgent    string `json:"user_agent"`
	Referer      string `json:"referer"`
	ForwardedFor string `json:"forwarded_for,omitempty"`
	CloudflareIP string `json:"cf_connecting_ip,omitempty"`

	// ── Cloudflare Enrichment (if behind CF proxy) ─────────────
	CFCountry    string `json:"cf_country,omitempty"`
	CFRay        string `json:"cf_ray,omitempty"`
	CFVisitor    string `json:"cf_visitor,omitempty"` // {"scheme":"https"}

	// ── Headers (sanitized) ───────────────────────────────────
	Headers      map[string]string `json:"headers"`

	// ── Body ──────────────────────────────────────────────────
	ContentType  string      `json:"content_type"`
	BodyFormat   string      `json:"body_format"` // "json" | "form" | "multipart" | "xml" | "text" | "binary" | "empty"
	Body         interface{} `json:"body"`
	RawBody      string      `json:"raw_body,omitempty"` // populated for non-JSON formats
	BodySize     int         `json:"body_size"`

	// ── Form Data (when Content-Type is form-urlencoded or multipart) ─
	FormFields   map[string]string `json:"form_fields,omitempty"`
	FileNames    []string          `json:"file_names,omitempty"` // multipart file names only (no content)

	// ── Auth Context ──────────────────────────────────────────
	AuthMethod   string `json:"auth_method"` // which policy passed (last one wins if multiple)
	UserRole     string `json:"user_role"`
	UserID       string `json:"user_id,omitempty"`
	AppClientID  string `json:"app_client_id,omitempty"`

	// ── Source Signals ────────────────────────────────────────
	// Well-known delivery identifiers from common providers.
	// Populated when headers match known patterns — avoids magic in automation nodes.
	SourceHints  WebhookSourceHints `json:"source_hints"`
}

// WebhookSourceHints carries provider-specific delivery metadata
// extracted from headers. All fields are optional; empty string = not present.
type WebhookSourceHints struct {
	// Generic
	DeliveryID   string `json:"delivery_id,omitempty"`   // X-Webhook-ID, X-Delivery-Id, etc.
	EventType    string `json:"event_type,omitempty"`    // X-Event-Type, X-GitHub-Event, etc.
	Timestamp    string `json:"timestamp,omitempty"`     // X-Timestamp, X-Webhook-Timestamp, etc.
	Idempotency  string `json:"idempotency_key,omitempty"`

	// Provider-specific
	GitHubEvent  string `json:"github_event,omitempty"`   // X-GitHub-Event
	GitHubHookID string `json:"github_hook_id,omitempty"` // X-GitHub-Hook-ID
	StripeEvent  string `json:"stripe_event,omitempty"`   // stripe-signature present → event type from body
	HotmartEvent string `json:"hotmart_event,omitempty"`
	PagarMeEvent string `json:"pagarme_event,omitempty"`
	MercadoPago  string `json:"mercadopago_action,omitempty"`
	AsaasEvent   string `json:"asaas_event,omitempty"`
	InterEvent   string `json:"inter_event,omitempty"`    // banco inter
	C6Event      string `json:"c6_event,omitempty"`
	IFoodEvent   string `json:"ifood_event,omitempty"`
	ShopifyEvent string `json:"shopify_event,omitempty"`  // X-Shopify-Topic
	SlackEvent   string `json:"slack_event,omitempty"`    // Slack event_callback type from body
	TwilioEvent  string `json:"twilio_event,omitempty"`
	SendGridEvent string `json:"sendgrid_event,omitempty"`
}

func (c *WebhookController) HandleIncoming(w http.ResponseWriter, r *http.Request) {
	receivedAt := time.Now().UTC()

	projectSlug := chi.URLParam(r, "projectSlug")
	pathSlug := chi.URLParam(r, "pathSlug")

	cascataReq, _ := r.Context().Value(types.CascataCtxKey).(*types.CascataRequest)
	if projectSlug == "" && cascataReq != nil && cascataReq.Project != nil {
		projectSlug = cascataReq.Project.Slug
	}
	if projectSlug == "" {
		writeError(w, 400, "Project slug missing")
		return
	}

	// ── 0. Rate Limiting ──────────────────────────────────────
	ip := nexus.ExtractClientIP(r)
	rateCheck := services.CheckRateLimit(r.Context(), services.SystemPool, projectSlug, "/webhook/"+pathSlug, "POST", "anon", ip, "")
	if rateCheck.Blocked {
		w.Header().Set("Retry-After", fmt.Sprintf("%d", rateCheck.RetryAfter))
		writeError(w, 429, rateCheck.CustomMessage)
		return
	}

	// ── 1. Read Body (hard limit 10 MB) ───────────────────────
	r.Body = http.MaxBytesReader(w, r.Body, 10*1024*1024)
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		log.Printf("[Webhook] Payload too large or unreadable: %v", err)
		writeError(w, 413, "Payload too large or unreadable")
		return
	}

	// ── 2. Fetch Receiver from Nexus Automations ──────────────
	var receiver struct {
		ID            string
		AuthPolicies  string
		Method        string
		AsyncResponse *string
	}
	branchName := types.GetBranchName(r.Context())

	err = services.SystemPool.QueryRow(r.Context(),
		`SELECT id,
		        COALESCE(graph_json->'nodes'->0->'config'->>'auth_policies', '[]'),
		        COALESCE(graph_json->'nodes'->0->'config'->>'method', 'POST'),
		        graph_json->'nodes'->0->'config'->>'async_response'
		 FROM system.nexus_automations
		 WHERE tenant_id   = $1
		   AND branch_name = $2
		   AND hook_type   = 'WEBHOOK'
		   AND graph_json->'nodes'->0->'config'->>'path_slug' = $3
		   AND is_active   = true`,
		projectSlug, branchName, pathSlug,
	).Scan(&receiver.ID, &receiver.AuthPolicies, &receiver.Method, &receiver.AsyncResponse)
	if err != nil {
		writeError(w, 404, "Webhook not found or inactive")
		return
	}

	// ── 3. Method Validation ──────────────────────────────────
	incomingMethod := strings.ToUpper(r.Method)
	allowedMethod := strings.ToUpper(receiver.Method)
	if allowedMethod != "ANY" && allowedMethod != "" && allowedMethod != incomingMethod {
		w.Header().Set("Allow", allowedMethod)
		writeError(w, 405, fmt.Sprintf("Method %s not allowed; expected %s", incomingMethod, allowedMethod))
		return
	}

	// ── 4. Auth Policy Enforcement (Multi-Barrier) ────────────
	var policies []AuthPolicy
	if err := json.Unmarshal([]byte(receiver.AuthPolicies), &policies); err != nil {
		log.Printf("[Webhook:Auth] Failed to parse auth_policies: %v", err)
	}

	resolveVaultRef := c.buildVaultResolver(r, projectSlug)

	lastAuthMethod := "none"
	for _, policy := range policies {
		if err := c.enforcePolicy(w, r, policy, cascataReq, projectSlug, bodyBytes, resolveVaultRef); err != nil {
			// enforcePolicy already wrote the HTTP error response
			return
		}
		lastAuthMethod = policy.Method
	}

	// ── 5. Build Rich Envelope ────────────────────────────────
	contentType := r.Header.Get("Content-Type")
	bodyFormat, parsedBody, formFields, fileNames := parseBody(contentType, bodyBytes)

	queryParams := map[string]string{}
	for k, v := range r.URL.Query() {
		if len(v) > 0 {
			queryParams[k] = v[0]
		}
	}

	fullURL := r.URL.String()
	if r.Host != "" {
		scheme := "https"
		if r.TLS == nil && r.Header.Get("X-Forwarded-Proto") != "https" {
			scheme = "http"
		}
		fullURL = scheme + "://" + r.Host + r.RequestURI
	}

	appClientID := ""
	userRole := "anon"
	if cascataReq != nil {
		userRole = string(cascataReq.UserRole)
		if cascataReq.AppClient != nil {
			appClientID = cascataReq.AppClient.ID
		}
		// UserID available if your types.CascataRequest exposes it
		// userID = cascataReq.UserID
	}

	envelope := WebhookEnvelope{
		ReceiverID:   receiver.ID,
		ProjectSlug:  projectSlug,
		PathSlug:     pathSlug,
		BranchName:   branchName,
		ReceivedAt:   receivedAt,
		ReceivedUnix: receivedAt.UnixMilli(),

		Method:      incomingMethod,
		URL:         fullURL,
		Path:        r.URL.Path,
		RawQuery:    r.URL.RawQuery,
		QueryParams: queryParams,
		Protocol:    r.Proto,

		ClientIP:     ip,
		UserAgent:    r.Header.Get("User-Agent"),
		Referer:      r.Header.Get("Referer"),
		ForwardedFor: r.Header.Get("X-Forwarded-For"),
		CloudflareIP: r.Header.Get("CF-Connecting-IP"),
		CFCountry:    r.Header.Get("CF-IPCountry"),
		CFRay:        r.Header.Get("CF-Ray"),
		CFVisitor:    r.Header.Get("CF-Visitor"),

		Headers: nexus.ExtractSafeHeaders(r.Header),

		ContentType: contentType,
		BodyFormat:  bodyFormat,
		Body:        parsedBody,
		RawBody:     rawBodyString(bodyFormat, bodyBytes),
		BodySize:    len(bodyBytes),

		FormFields: formFields,
		FileNames:  fileNames,

		AuthMethod:  lastAuthMethod,
		UserRole:    userRole,
		AppClientID: appClientID,

		SourceHints: extractSourceHints(r, parsedBody),
	}

	// ── 6. Dispatch to Nexus ──────────────────────────────────
	envelopeMap := structToMap(envelope)

	hr, err := c.NexusSvc.ResolveWebhook(
		r.Context(), projectSlug, userRole,
		receiver.ID, pathSlug,
		envelopeMap,
		nexus.ExtractSafeHeaders(r.Header),
	)
	if err != nil {
		log.Printf("[Webhook:Execution] Nexus error project=%s path=%s: %v", projectSlug, pathSlug, err)
		writeError(w, 500, "Internal automation error")
		return
	}

	// ── 7. Response ───────────────────────────────────────────
	if hr != nil && hr.TraceID != "" {
		w.Header().Set("X-Cascata-Trace", hr.TraceID)
	}

	if hr != nil && hr.Intercepted {
		status := 200
		if hr.ResponseCode > 0 {
			status = hr.ResponseCode
		}

		log.Printf("[webhook.go] Intercepted response. ResponseData keys: %v", getMapKeys(hr.ResponseData))

		// Verifica o tipo de resposta (json, text, html, xml)
		responseType := "json"
		if rt, ok := hr.ResponseData["response_type"].(string); ok {
			responseType = rt
		}
		log.Printf("[webhook.go] responseType: %s", responseType)

		// Para tipos não-JSON, retorna o body como texto puro.
		if responseType == "text" || responseType == "html" || responseType == "xml" {
			bodyStr := resolveNonJSONBody(hr.ResponseData, envelopeMap)
			log.Printf("[webhook.go] non-JSON body resolved: %q (len=%d)", truncateLog(bodyStr, 200), len(bodyStr))

			switch responseType {
			case "text":
				w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			case "html":
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
			case "xml":
				w.Header().Set("Content-Type", "application/xml; charset=utf-8")
			}
			w.WriteHeader(status)
			if bodyStr != "" {
				w.Write([]byte(bodyStr))
			}
			return
		}

		// Para JSON, usa o método padrão
		writeJSON(w, status, hr.ResponseData)
		return
	}

	// Async / passthrough
	if receiver.AsyncResponse != nil && *receiver.AsyncResponse != "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(202)
		w.Write([]byte(*receiver.AsyncResponse))
		return
	}

	traceID := ""
	if hr != nil {
		traceID = hr.TraceID
	}
	writeJSON(w, 202, map[string]interface{}{
		"success":    true,
		"trace_id":   traceID,
		"queued_at":  receivedAt.Format(time.RFC3339Nano),
	})
}

// ─────────────────────────────────────────────────────────────
// Auth Policy Enforcement — extracted for clarity
// ─────────────────────────────────────────────────────────────

func (c *WebhookController) enforcePolicy(
	w http.ResponseWriter,
	r *http.Request,
	policy AuthPolicy,
	cascataReq *types.CascataRequest,
	projectSlug string,
	bodyBytes []byte,
	resolveVaultRef func(string) string,
) error {
	// Returns non-nil error only to signal "access denied — response already written".
	fail := func() error { return fmt.Errorf("auth failed") }

	switch policy.Method {

	case "none":
		// Open endpoint — no checks.

	case "anonymous":
		apiKey := r.Header.Get("apikey")
		if apiKey == "" {
			if ah := r.Header.Get("Authorization"); strings.HasPrefix(ah, "Bearer ") {
				apiKey = strings.TrimPrefix(ah, "Bearer ")
			}
		}
		if apiKey == "" {
			http.Error(w, `{"error":"App Client Key (anon_key) Required"}`, 401)
			return fail()
		}
		isValid := false
		allowProjectKey, _ := policy.Config["allow_project_key"].(bool)
		if allowProjectKey && cascataReq != nil && cascataReq.Project != nil {
			isValid = subtle.ConstantTimeCompare([]byte(apiKey), []byte(cascataReq.Project.AnonKey)) == 1
		}
		if !isValid {
			allowedIDs := extractStringSlice(policy.Config["app_client_ids"])
			if cascataReq != nil && cascataReq.AppClient != nil {
				if len(allowedIDs) == 0 {
					isValid = true
				} else {
					for _, id := range allowedIDs {
						if cascataReq.AppClient.ID == id {
							isValid = true
							break
						}
					}
				}
			}
		}
		if !isValid {
			http.Error(w, `{"error":"Invalid or unauthorized App Client Key"}`, 401)
			return fail()
		}

	case "api_key":
		apiKey := r.Header.Get("apikey")
		if apiKey == "" {
			if ah := r.Header.Get("Authorization"); strings.HasPrefix(ah, "Bearer ") {
				apiKey = strings.TrimPrefix(ah, "Bearer ")
			}
		}
		if apiKey == "" || !strings.HasPrefix(apiKey, "sk_live_") {
			http.Error(w, `{"error":"API Key (sk_live_*) Required"}`, 401)
			return fail()
		}
		lookupIndex := apiKey
		if parts := strings.SplitN(apiKey[8:], "_", 2); len(parts) > 0 {
			lookupIndex = "sk_live_" + parts[0]
		}
		var keyGroupID *string
		var keyHash string
		var isActive bool
		err := services.SystemPool.QueryRow(r.Context(),
			`SELECT key_hash, group_id, is_active FROM system.api_keys
			 WHERE project_slug = $1 AND lookup_index = $2`,
			projectSlug, lookupIndex).Scan(&keyHash, &keyGroupID, &isActive)
		if err != nil || !isActive {
			http.Error(w, `{"error":"Invalid or expired API Key"}`, 401)
			return fail()
		}
		if err := services.CompareAPIKeyHash(keyHash, apiKey); err != nil {
			http.Error(w, `{"error":"Invalid API Key"}`, 401)
			return fail()
		}
		allowedGroupIDs := extractStringSlice(policy.Config["group_ids"])
		if len(allowedGroupIDs) > 0 {
			if keyGroupID == nil {
				http.Error(w, `{"error":"API Key does not belong to an authorized Key Group"}`, 403)
				return fail()
			}
			found := false
			for _, gid := range allowedGroupIDs {
				if *keyGroupID == gid {
					found = true
					break
				}
			}
			if !found {
				http.Error(w, `{"error":"API Key group not authorized for this webhook"}`, 403)
				return fail()
			}
		}

	case "identity":
		if cascataReq == nil || cascataReq.UserRole == types.RoleAnon {
			http.Error(w, `{"error":"Authentication Required: Identity Verification Failed"}`, 401)
			return fail()
		}
		minRole := "authenticated"
		if mr, ok := policy.Config["min_role"].(string); ok && mr != "" {
			minRole = mr
		}
		hierarchy := map[string]int{"anon": 0, "authenticated": 1, "admin": 2, "service": 3}
		if hierarchy[string(cascataReq.UserRole)] < hierarchy[minRole] {
			http.Error(w, fmt.Sprintf(`{"error":"Insufficient role: requires %s, current is %s"}`, minRole, cascataReq.UserRole), 403)
			return fail()
		}

	case "bearer", "hmac_sha256", "rsa_signature":
		vaultRef, _ := policy.Config["vault_ref"].(string)
		if vaultRef == "" {
			http.Error(w, `{"error":"Secret not configured"}`, 500)
			return fail()
		}
		secret := resolveVaultRef(vaultRef)
		if secret == "" {
			http.Error(w, `{"error":"Secret resolution failed"}`, 500)
			return fail()
		}
		credential := ""
		switch policy.Method {
		case "bearer":
			if ah := r.Header.Get("Authorization"); strings.HasPrefix(ah, "Bearer ") {
				credential = strings.TrimPrefix(ah, "Bearer ")
			}
		default:
			// Check multiple well-known signature headers in priority order
			for _, h := range []string{
				"X-Cascata-Signature",
				"X-Hub-Signature-256",
				"X-Signature-SHA256",
				"X-Webhook-Signature",
			} {
				if v := r.Header.Get(h); v != "" {
					credential = v
					break
				}
			}
		}
		if credential == "" {
			http.Error(w, fmt.Sprintf(`{"error":"Missing security credential for %s"}`, policy.Method), 401)
			return fail()
		}
		var ok bool
		switch policy.Method {
		case "hmac_sha256":
			ok = services.VerifyHMACSHA256(secret, bodyBytes, credential)
		case "rsa_signature":
			valid, err := services.VerifyRSASignature(secret, bodyBytes, credential)
			ok = err == nil && valid
		case "bearer":
			ok = subtle.ConstantTimeCompare([]byte(credential), []byte(secret)) == 1
		}
		if !ok {
			http.Error(w, fmt.Sprintf(`{"error":"Invalid credential for %s"}`, policy.Method), 401)
			return fail()
		}

	case "basic_auth":
		username, password, authOK := r.BasicAuth()
		if !authOK {
			w.Header().Set("WWW-Authenticate", `Basic realm="Restricted"`)
			http.Error(w, `{"error":"Basic Auth Required"}`, 401)
			return fail()
		}
		vaultRef, _ := policy.Config["vault_ref"].(string)
		if vaultRef == "" {
			http.Error(w, `{"error":"Basic Auth Secret not configured"}`, 500)
			return fail()
		}
		secretJSON := resolveVaultRef(vaultRef)
		var creds struct {
			ClientID     string `json:"client_id"`
			ClientSecret string `json:"client_secret"`
		}
		if err := json.Unmarshal([]byte(secretJSON), &creds); err != nil {
			http.Error(w, `{"error":"Invalid Basic Auth secret format"}`, 500)
			return fail()
		}
		if subtle.ConstantTimeCompare([]byte(username), []byte(creds.ClientID)) == 0 ||
			subtle.ConstantTimeCompare([]byte(password), []byte(creds.ClientSecret)) == 0 {
			w.Header().Set("WWW-Authenticate", `Basic realm="Restricted"`)
			http.Error(w, `{"error":"Invalid Client ID or Client Secret"}`, 401)
			return fail()
		}

	default:
		http.Error(w, fmt.Sprintf(`{"error":"Unsupported authentication policy: %s"}`, policy.Method), 403)
		return fail()
	}

	return nil
}

// ─────────────────────────────────────────────────────────────
// Body Parsing
// ─────────────────────────────────────────────────────────────

// parseBody inspects Content-Type and returns a structured representation.
// Returns: format, parsed body, form fields, and multipart file names.
func parseBody(contentType string, body []byte) (
	format string,
	parsed interface{},
	formFields map[string]string,
	fileNames []string,
) {
	if len(body) == 0 {
		return "empty", nil, nil, nil
	}

	mediaType, params, _ := mime.ParseMediaType(contentType)

	switch {
	case mediaType == "application/json" || strings.HasSuffix(mediaType, "+json"):
		var v interface{}
		if err := json.Unmarshal(body, &v); err == nil {
			return "json", v, nil, nil
		}
		// Malformed JSON — fall through to text
		return "text", nil, nil, nil

	case mediaType == "application/x-www-form-urlencoded":
		values, err := url.ParseQuery(string(body))
		if err != nil {
			return "form", nil, nil, nil
		}
		fields := map[string]string{}
		for k, v := range values {
			if len(v) > 0 {
				fields[k] = v[0]
			}
		}
		// Also expose as nested JSON body for convenience
		asJSON := map[string]interface{}{}
		for k, v := range fields {
			asJSON[k] = v
		}
		return "form", asJSON, fields, nil

	case strings.HasPrefix(mediaType, "multipart/"):
		boundary := params["boundary"]
		if boundary == "" {
			return "multipart", nil, nil, nil
		}
		fields := map[string]string{}
		var names []string
		mr := multipart.NewReader(bytes.NewReader(body), boundary)
		for {
			part, err := mr.NextPart()
			if err != nil {
				break
			}
			if part.FileName() != "" {
				names = append(names, part.FileName())
				part.Close()
				continue
			}
			partBody, err := io.ReadAll(io.LimitReader(part, 64*1024)) // cap field at 64 KB
			if err == nil {
				fields[part.FormName()] = string(partBody)
			}
			part.Close()
		}
		return "multipart", nil, fields, names

	case mediaType == "application/xml" || mediaType == "text/xml" || strings.HasSuffix(mediaType, "+xml"):
		return "xml", nil, nil, nil

	case strings.HasPrefix(mediaType, "text/"):
		return "text", nil, nil, nil

	default:
		// Unknown binary or unset Content-Type — try JSON sniff
		trimmed := bytes.TrimSpace(body)
		if len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[') {
			var v interface{}
			if err := json.Unmarshal(trimmed, &v); err == nil {
				return "json", v, nil, nil
			}
		}
		return "binary", nil, nil, nil
	}
}

// rawBodyString returns the raw body as a string for non-binary, non-JSON formats.
func rawBodyString(format string, body []byte) string {
	switch format {
	case "json", "binary", "empty":
		return ""
	default:
		if len(body) > 65536 { // cap at 64 KB to avoid bloating the envelope
			return string(body[:65536]) + "…[truncated]"
		}
		return string(body)
	}
}

// ─────────────────────────────────────────────────────────────
// Source Hints
// ─────────────────────────────────────────────────────────────

// extractSourceHints reads well-known vendor headers and body fields to
// surface provider-specific metadata without requiring custom nodes.
func extractSourceHints(r *http.Request, body interface{}) WebhookSourceHints {
	h := r.Header
	hints := WebhookSourceHints{}

	// Generic delivery ID (first match wins)
	for _, hdr := range []string{
		"X-Webhook-ID", "X-Delivery-Id", "X-Request-ID",
		"X-Amzn-RequestId", "X-Correlation-ID",
	} {
		if v := h.Get(hdr); v != "" {
			hints.DeliveryID = v
			break
		}
	}

	// Generic event type
	for _, hdr := range []string{
		"X-Event-Type", "X-Webhook-Event", "X-Event-Name",
	} {
		if v := h.Get(hdr); v != "" {
			hints.EventType = v
			break
		}
	}

	// Generic timestamp
	for _, hdr := range []string{"X-Timestamp", "X-Webhook-Timestamp", "X-Delivery-Timestamp"} {
		if v := h.Get(hdr); v != "" {
			hints.Timestamp = v
			break
		}
	}

	// Idempotency
	for _, hdr := range []string{"Idempotency-Key", "X-Idempotency-Key"} {
		if v := h.Get(hdr); v != "" {
			hints.Idempotency = v
			break
		}
	}

	// GitHub
	hints.GitHubEvent = h.Get("X-GitHub-Event")
	hints.GitHubHookID = h.Get("X-GitHub-Hook-ID")
	if hints.DeliveryID == "" {
		hints.DeliveryID = h.Get("X-GitHub-Delivery")
	}

	// Shopify
	hints.ShopifyEvent = h.Get("X-Shopify-Topic")

	// Stripe — delivery id from header; event type from body
	if sid := h.Get("Stripe-Signature"); sid != "" {
		if bmap, ok := body.(map[string]interface{}); ok {
			if t, ok := bmap["type"].(string); ok {
				hints.StripeEvent = t
			}
			if id, ok := bmap["id"].(string); ok && hints.DeliveryID == "" {
				hints.DeliveryID = id
			}
		}
	}

	// Hotmart
	hints.HotmartEvent = h.Get("X-Hotmart-Event")

	// Pagar.me
	hints.PagarMeEvent = h.Get("X-PagarMe-Event")

	// Mercado Pago — action in body
	if h.Get("X-Signature") != "" {
		if bmap, ok := body.(map[string]interface{}); ok {
			if action, ok := bmap["action"].(string); ok {
				hints.MercadoPago = action
			}
		}
	}

	// Asaas — event type from body
	if bmap, ok := body.(map[string]interface{}); ok {
		if event, ok := bmap["event"].(string); ok {
			// Asaas events start with "PAYMENT_", "SUBSCRIPTION_", etc.
			if strings.Contains(event, "_") && hints.StripeEvent == "" {
				hints.AsaasEvent = event
			}
		}
	}

	// Banco Inter
	hints.InterEvent = h.Get("X-Inter-Event")

	// C6 Bank
	hints.C6Event = h.Get("X-C6-Event")

	// iFood
	hints.IFoodEvent = h.Get("X-Ifood-Event-Type")

	// Slack
	if bmap, ok := body.(map[string]interface{}); ok {
		if bmap["type"] == "event_callback" {
			if eventObj, ok := bmap["event"].(map[string]interface{}); ok {
				if t, ok := eventObj["type"].(string); ok {
					hints.SlackEvent = t
				}
			}
		}
	}

	// Twilio
	hints.TwilioEvent = h.Get("X-Twilio-Signature")

	// SendGrid
	hints.SendGridEvent = h.Get("X-Twilio-Email-Event-Webhook-Signature")

	return hints
}


// ─────────────────────────────────────────────────────────────
// Non-JSON Body Resolution
// ─────────────────────────────────────────────────────────────

// resolveNonJSONBody extracts the text body to return for text/html/xml responses.
//
// Resolution chain (first non-empty string wins):
//  1. hr.ResponseData["body"] — explicit body set by the Response node
//  2. hr.ResponseData["output"] — some nodes surface their result here
//  3. Any scalar string value in ResponseData that is not a control field
//  4. envelope query_params["hub.challenge"] — Facebook/WhatsApp webhook verification
//  5. Empty string (caller writes no body)
func resolveNonJSONBody(responseData map[string]interface{}, envelope map[string]interface{}) string {
	controlKeys := map[string]bool{
		"status_code": true, "response_type": true, "headers": true,
		"body": true, // checked explicitly first
	}

	// 1. Explicit body field (non-nil, non-empty)
	if v, ok := responseData["body"]; ok && v != nil {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
		if v != nil {
			return fmt.Sprintf("%v", v)
		}
	}

	// 2. "output" field
	if v, ok := responseData["output"]; ok && v != nil {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
	}

	// 3. First non-nil scalar string in ResponseData that isn't a control key
	for k, v := range responseData {
		if controlKeys[k] || v == nil {
			continue
		}
		if s, ok := v.(string); ok && s != "" {
			log.Printf("[webhook.go] resolveNonJSONBody: using responseData[%q] = %q", k, truncateLog(s, 80))
			return s
		}
	}

	// 4. Envelope query_params — handles Facebook/WhatsApp/Meta hub.challenge verification
	if qp, ok := envelope["query_params"].(map[string]interface{}); ok {
		// hub.challenge takes priority (Meta webhook verification)
		if ch, ok := qp["hub.challenge"].(string); ok && ch != "" {
			log.Printf("[webhook.go] resolveNonJSONBody: Facebook hub.challenge=%q", ch)
			return ch
		}
		// Generic challenge fallback
		for _, k := range []string{"challenge", "verify_token"} {
			if v, ok := qp[k].(string); ok && v != "" {
				return v
			}
		}
	}

	return ""
}

// truncateLog truncates a string for safe log output.
func truncateLog(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

// getMapKeys retorna as chaves de um map[string]interface{}
func getMapKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// ─────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────

func (c *WebhookController) buildVaultResolver(r *http.Request, projectSlug string) func(string) string {
	return func(vaultRef string) string {
		if !strings.HasPrefix(vaultRef, "{{$vault.") {
			dec, err := c.CryptoSvc.Decrypt(vaultRef)
			if err == nil {
				return dec
			}
			return vaultRef
		}
		identifier := strings.TrimPrefix(vaultRef, "{{$vault.")
		identifier = strings.TrimSuffix(identifier, ".value}}")
		identifier = strings.TrimSuffix(identifier, "}}")

		vaultSvc := c.VaultSvc
		if vaultSvc == nil {
			vaultSvc = services.NewVaultService(c.CryptoSvc)
		}
		secretRec, err := vaultSvc.Fetch(r.Context(), projectSlug, identifier)
		if err != nil || secretRec == nil {
			return ""
		}
		val, err := c.CryptoSvc.Decrypt(secretRec.Ciphertext)
		if err != nil {
			return ""
		}
		return val
	}
}

func extractStringSlice(v interface{}) []string {
	raw, ok := v.([]interface{})
	if !ok {
		return nil
	}
	result := make([]string, 0, len(raw))
	for _, item := range raw {
		if s, ok := item.(string); ok {
			result = append(result, s)
		}
	}
	return result
}

// structToMap marshals a struct to map[string]interface{} via JSON round-trip.
// This ensures Nexus always receives a consistent, serialisable map regardless
// of the envelope struct layout.
func structToMap(v interface{}) map[string]interface{} {
	b, err := json.Marshal(v)
	if err != nil {
		return map[string]interface{}{}
	}
	var m map[string]interface{}
	if err := json.Unmarshal(b, &m); err != nil {
		return map[string]interface{}{}
	}
	return m
}