package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/smtp"
	"strings"
	"time"

	"cascata-backend/internal/types"
	"github.com/jackc/pgx/v5/pgxpool"
)

// AuthDispatchService handles sending OTP codes, verification links, and other notifications.
type AuthDispatchService struct {
	systemPool *pgxpool.Pool
}

func NewAuthDispatchService(systemPool *pgxpool.Pool) *AuthDispatchService {
	return &AuthDispatchService{systemPool: systemPool}
}

// SendEmailSMTP sends an email using standard SMTP.
func (s *AuthDispatchService) SendEmailSMTP(ctx context.Context, host string, port int, user, pass, from, to, subject, body string, secure bool) error {
	addr := fmt.Sprintf("%s:%d", host, port)
	auth := smtp.PlainAuth("", user, pass, host)

	// Build headers
	headers := make(map[string]string)
	headers["From"] = from
	headers["To"] = to
	headers["Subject"] = subject
	headers["MIME-Version"] = "1.0"
	headers["Content-Type"] = "text/html; charset=UTF-8"

	var msg bytes.Buffer
	for k, v := range headers {
		msg.WriteString(fmt.Sprintf("%s: %s\r\n", k, v))
	}
	msg.WriteString("\r\n")
	msg.WriteString(body)

	err := smtp.SendMail(addr, auth, from, []string{to}, msg.Bytes())
	if err != nil {
		log.Printf("[AuthDispatch] SMTP SendMail error: %v", err)
	}
	return err
}

// SendEmailResend sends an email using the Resend API.
func (s *AuthDispatchService) SendEmailResend(ctx context.Context, apiKey, from, to, subject, htmlBody string) error {
	if apiKey == "" {
		return fmt.Errorf("resend api key is missing")
	}

	payload := map[string]interface{}{
		"from":    from,
		"to":      []string{to},
		"subject": subject,
		"html":    htmlBody,
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.resend.com/emails", bytes.NewBuffer(bodyBytes))
	if err != nil {
		return err
	}

	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("[AuthDispatch] Resend API request error: %v", err)
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var errResp map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&errResp)
		log.Printf("[AuthDispatch] Resend API error response: %v", errResp)
		return fmt.Errorf("resend api error: status %d", resp.StatusCode)
	}

	return nil
}

// SendWebhook sends a JSON payload to a custom webhook URL.
func (s *AuthDispatchService) SendWebhook(ctx context.Context, url string, payload map[string]interface{}) error {
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(bodyBytes))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("[AuthDispatch] Webhook request error: %v", err)
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook error: status %d", resp.StatusCode)
	}

	return nil
}

// TriggerNexusAutomation executes a Nexus Automation workflow directly for OTP sending.
func (s *AuthDispatchService) TriggerNexusAutomation(ctx context.Context, tenantID, automationID string, payload map[string]interface{}) error {
	nexusSvc := GlobalSchemaCache.NexusSvc
	if nexusSvc == nil {
		return fmt.Errorf("NexusService is not initialized")
	}

	log.Printf("[AuthDispatch] Triggering Nexus Automation: ID=%s, Tenant=%s", automationID, tenantID)
	
	// Execute via ResolveWebhook (standard entry point for programmatic webhooks/custom calls)
	_, err := nexusSvc.ResolveWebhook(ctx, tenantID, string(types.RoleService), automationID, "/auth/otp", payload, map[string]string{})
	if err != nil {
		log.Printf("[AuthDispatch] Failed to execute Nexus automation %s: %v", automationID, err)
		return err
	}

	log.Printf("[AuthDispatch] Nexus Automation %s executed successfully", automationID)
	return nil
}

// DispatchOTP dispatches an OTP code using the project's strategy configuration.
func (s *AuthDispatchService) DispatchOTP(ctx context.Context, project *types.Project, strategyName string, identifier string, code string, metadata map[string]interface{}) error {
	if project == nil {
		return fmt.Errorf("project context is nil")
	}

	log.Printf("[AuthDispatch] Dispatching OTP code for project %s, strategy %s, identifier %s", project.Slug, strategyName, identifier)

	// Fetch strategy configurations from Project Metadata
	authStrategies, ok := project.Metadata.Extra["auth_strategies"].(map[string]interface{})
	if !ok || authStrategies == nil {
		return fmt.Errorf("auth_strategies config not found in project metadata")
	}

	strategyRaw, exists := authStrategies[strategyName]
	if !exists {
		return fmt.Errorf("strategy %s not configured", strategyName)
	}

	strategyMap, ok := strategyRaw.(map[string]interface{})
	if !ok {
		return fmt.Errorf("invalid strategy configuration format")
	}

	webhookURL, _ := strategyMap["webhook_url"].(string)

	// 1. Check if the Dispatch method is a Nexus Automation (prefix nexus://)
	if strings.HasPrefix(webhookURL, "nexus://") {
		automationID := strings.TrimPrefix(webhookURL, "nexus://")
		payload := map[string]interface{}{
			"code":       code,
			"identifier": identifier,
			"strategy":   strategyName,
			"project":    project.Slug,
			"metadata":   metadata,
			"timestamp":  time.Now().Format(time.RFC3339),
		}
		return s.TriggerNexusAutomation(ctx, project.Slug, automationID, payload)
	}

	// 2. Check if SMTP Server
	if webhookURL == "smtp://" {
		emailGateway, _ := project.Metadata.Extra["email"].(map[string]interface{})
		if emailGateway == nil {
			emailGateway, _ = authStrategies["email"].(map[string]interface{})
		}
		if emailGateway == nil {
			return fmt.Errorf("SMTP dispatch requested but email configuration is missing")
		}
		fromEmail, _ := emailGateway["from_email"].(string)
		if fromEmail == "" {
			fromEmail = "noreply@cascata.io"
		}
		subject := "Your Security Verification Code"
		bodyHTML := fmt.Sprintf("<h2>Security Code</h2><p>Your verification code is: <strong>%s</strong></p><p>This code expires in 15 minutes.</p>", code)

		if bindings, ok := strategyMap["template_bindings"].(map[string]interface{}); ok {
			if tplID, ok := bindings["otp_challenge"].(string); ok && tplID != "" {
				if templates, ok := project.Metadata.Extra["messaging_templates"].(map[string]interface{}); ok {
					if tpl, ok := templates[tplID].(map[string]interface{}); ok {
						if defLang, _ := tpl["default_language"].(string); defLang != "" {
							if variants, ok := tpl["variants"].(map[string]interface{}); ok {
								if variant, ok := variants[defLang].(map[string]interface{}); ok {
									if tplSub, _ := variant["subject"].(string); tplSub != "" {
										subject = tplSub
									}
									if tplBody, _ := variant["body"].(string); tplBody != "" {
										bodyHTML = tplBody
									}
								}
							}
						}
					}
				}
			}
		}

		bodyHTML = strings.ReplaceAll(bodyHTML, "{{.Token}}", code)
		bodyHTML = strings.ReplaceAll(bodyHTML, "{{.Code}}", code)

		host, _ := emailGateway["smtp_host"].(string)
		portVal, _ := emailGateway["smtp_port"].(float64)
		port := int(portVal)
		if port == 0 {
			port = 587
		}
		user, _ := emailGateway["smtp_user"].(string)
		pass, _ := emailGateway["smtp_pass"].(string)
		secure, _ := emailGateway["smtp_secure"].(bool)

		return s.SendEmailSMTP(ctx, host, port, user, pass, fromEmail, identifier, subject, bodyHTML, secure)
	}

	// 3. Check if Resend API
	if webhookURL == "resend://" {
		emailGateway, _ := project.Metadata.Extra["email"].(map[string]interface{})
		if emailGateway == nil {
			emailGateway, _ = authStrategies["email"].(map[string]interface{})
		}
		if emailGateway == nil {
			return fmt.Errorf("Resend dispatch requested but email configuration is missing")
		}
		fromEmail, _ := emailGateway["from_email"].(string)
		if fromEmail == "" {
			fromEmail = "noreply@cascata.io"
		}
		subject := "Your Security Verification Code"
		bodyHTML := fmt.Sprintf("<h2>Security Code</h2><p>Your verification code is: <strong>%s</strong></p><p>This code expires in 15 minutes.</p>", code)

		if bindings, ok := strategyMap["template_bindings"].(map[string]interface{}); ok {
			if tplID, ok := bindings["otp_challenge"].(string); ok && tplID != "" {
				if templates, ok := project.Metadata.Extra["messaging_templates"].(map[string]interface{}); ok {
					if tpl, ok := templates[tplID].(map[string]interface{}); ok {
						if defLang, _ := tpl["default_language"].(string); defLang != "" {
							if variants, ok := tpl["variants"].(map[string]interface{}); ok {
								if variant, ok := variants[defLang].(map[string]interface{}); ok {
									if tplSub, _ := variant["subject"].(string); tplSub != "" {
										subject = tplSub
									}
									if tplBody, _ := variant["body"].(string); tplBody != "" {
										bodyHTML = tplBody
									}
								}
							}
						}
					}
				}
			}
		}

		bodyHTML = strings.ReplaceAll(bodyHTML, "{{.Token}}", code)
		bodyHTML = strings.ReplaceAll(bodyHTML, "{{.Code}}", code)

		apiKey, _ := emailGateway["resend_api_key"].(string)
		return s.SendEmailResend(ctx, apiKey, fromEmail, identifier, subject, bodyHTML)
	}

	// 4. Check if standard Webhook URL is configured
	if webhookURL != "" {
		payload := map[string]interface{}{
			"code":       code,
			"identifier": identifier,
			"strategy":   strategyName,
			"project":    project.Slug,
			"metadata":   metadata,
			"timestamp":  time.Now().Format(time.RFC3339),
		}
		return s.SendWebhook(ctx, webhookURL, payload)
	}

	// 3. Fallback to Email Gateway config (SMTP/Resend) for default 'email' or email-based strategies
	emailGateway, ok := project.Metadata.Extra["email"].(map[string]interface{})
	if !ok {
		// Fallback to auth_strategies.email
		emailGateway, _ = authStrategies["email"].(map[string]interface{})
	}

	if emailGateway == nil {
		return fmt.Errorf("no dispatch mechanism configured (no webhook, no email gateway)")
	}

	deliveryMethodsRaw, _ := emailGateway["delivery_methods"].([]interface{})
	deliveryMethods := []string{}
	for _, m := range deliveryMethodsRaw {
		if s, ok := m.(string); ok {
			deliveryMethods = append(deliveryMethods, s)
		}
	}

	// If empty delivery methods, check legacy single delivery_method
	if len(deliveryMethods) == 0 {
		if dm, ok := emailGateway["delivery_method"].(string); ok && dm != "" {
			deliveryMethods = append(deliveryMethods, dm)
		}
	}

	if len(deliveryMethods) == 0 {
		return fmt.Errorf("no email delivery methods enabled")
	}

	// Render default OTP email template
	subject := "Your Security Verification Code"
	bodyHTML := fmt.Sprintf("<h2>Security Code</h2><p>Your verification code is: <strong>%s</strong></p><p>This code expires in 15 minutes.</p>", code)

	// Fetch customized template if available
	authConfig, _ := project.Metadata.Extra["auth_config"].(map[string]interface{})
	if authConfig != nil {
		if templates, ok := authConfig["email_templates"].(map[string]interface{}); ok {
			if magicLinkTpl, ok := templates["magic_link"].(map[string]interface{}); ok {
				if tplSub, _ := magicLinkTpl["subject"].(string); tplSub != "" {
					subject = tplSub
				}
				if tplBody, _ := magicLinkTpl["body"].(string); tplBody != "" {
					bodyHTML = tplBody
				}
			}
		}
	}

	// Replace templates variables
	bodyHTML = strings.ReplaceAll(bodyHTML, "{{ .ConfirmationURL }}", "")
	bodyHTML = strings.ReplaceAll(bodyHTML, "{{ .Token }}", code)
	bodyHTML = strings.ReplaceAll(bodyHTML, "{{.Token}}", code)

	fromEmail, _ := emailGateway["from_email"].(string)
	if fromEmail == "" {
		fromEmail = "noreply@cascata.io"
	}

	// Send using the first configured method (or iterate/send on all)
	var sendErr error
	for _, method := range deliveryMethods {
		switch method {
		case "resend":
			apiKey, _ := emailGateway["resend_api_key"].(string)
			sendErr = s.SendEmailResend(ctx, apiKey, fromEmail, identifier, subject, bodyHTML)
		case "smtp":
			host, _ := emailGateway["smtp_host"].(string)
			portVal, _ := emailGateway["smtp_port"].(float64)
			port := int(portVal)
			if port == 0 {
				port = 587
			}
			user, _ := emailGateway["smtp_user"].(string)
			pass, _ := emailGateway["smtp_pass"].(string)
			secure, _ := emailGateway["smtp_secure"].(bool)

			sendErr = s.SendEmailSMTP(ctx, host, port, user, pass, fromEmail, identifier, subject, bodyHTML, secure)
		case "webhook":
			webhook, _ := emailGateway["webhook_url"].(string)
			if webhook != "" {
				payload := map[string]interface{}{
					"code":       code,
					"identifier": identifier,
					"strategy":   strategyName,
					"project":    project.Slug,
					"timestamp":  time.Now().Format(time.RFC3339),
				}
				sendErr = s.SendWebhook(ctx, webhook, payload)
			}
		}

		if sendErr == nil {
			return nil // Success!
		}
	}

	return fmt.Errorf("failed to send OTP via enabled delivery methods: %v", sendErr)
}
