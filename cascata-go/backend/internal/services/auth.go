package services

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base32"
	"encoding/binary"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"cascata-backend/internal/types"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

// AuthService is the sovereign authentication engine
type AuthService struct{}

// --- TYPES ---

type UserProfile struct {
	Provider  string `json:"provider"`
	ID        string `json:"id"`
	Email     string `json:"email,omitempty"`
	Name      string `json:"name,omitempty"`
	AvatarURL string `json:"avatar_url,omitempty"`
}

type ProviderConfig struct {
	ClientID          string `json:"client_id"`
	ClientSecret      string `json:"client_secret"`
	RedirectURI       string `json:"redirect_uri"`
	AuthorizedClients string `json:"authorized_clients"` // Comma-separated list of additional client IDs
	AutoVerify        bool   `json:"auto_verify"`
}

type EmailConfig struct {
	FromEmail       string      `json:"from_email"`
	DeliveryMethods []string    `json:"delivery_methods"`
	ResendAPIKey    string      `json:"resend_api_key"`
	WebhookURL      string      `json:"webhook_url"`
	SMTP            *SMTPConfig `json:"smtp,omitempty"`
}

type SMTPConfig struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	User     string `json:"user"`
	Pass     string `json:"pass"`
	IsSecure bool   `json:"is_secure"`
}

// --- SOVEREIGN CRYPTO UTILS ---

func TimingSafeEqual(a, b string) bool {
	return hmac.Equal([]byte(a), []byte(b))
}

func GenerateRandomToken(length int) string {
	b := make([]byte, length)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return ""
	}
	return hex.EncodeToString(b)
}

// --- ADMIN TOKEN ---

// CreateAdminToken issues a JWT for dashboard admin access
func (s *AuthService) CreateAdminToken(adminID, sysSecret string) (string, error) {
	claims := jwt.MapClaims{
		"role": "admin",
		"sub":  adminID,
		"iat":  time.Now().Unix(),
		"exp":  time.Now().Add(12 * time.Hour).Unix(),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(sysSecret))
}

// --- IDENTITY PROVIDERS (OAUTH) ---

func (s *AuthService) VerifyGoogleToken(ctx context.Context, idToken string, conf ProviderConfig) (*UserProfile, error) {
	verifyURL := "https://oauth2.googleapis.com/tokeninfo?id_token=" + idToken
	resp, err := http.Get(verifyURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("invalid google id token")
	}

	var payload struct {
		Sub     string `json:"sub"`
		Email   string `json:"email"`
		Name    string `json:"name"`
		Picture string `json:"picture"`
		Aud     string `json:"aud"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}

	// Audience Validation
	allowed := strings.Split(conf.AuthorizedClients, ",")
	allowed = append(allowed, conf.ClientID)
	match := false
	for _, a := range allowed {
		if strings.TrimSpace(a) == payload.Aud {
			match = true
			break
		}
	}
	if !match && conf.ClientID != "" {
		return nil, fmt.Errorf("token audience mismatch")
	}

	return &UserProfile{
		Provider:  "google",
		ID:        payload.Sub,
		Email:     payload.Email,
		Name:      payload.Name,
		AvatarURL: payload.Picture,
	}, nil
}

// --- SESSION MANAGEMENT (SOVEREIGN) ---

// CreateSession generates access + refresh tokens for a user
func (s *AuthService) CreateSession(
	ctx context.Context,
	userID string,
	pool *pgxpool.Pool,
	jwtSecret string,
	accessTTL string,
	refreshDays int,
	loginProvider string,
	deviceInfo types.DeviceInfo,
) (*types.AuthSession, error) {
	// 1. Identify User
	var identifier string
	pool.QueryRow(ctx, "SELECT identifier FROM auth.identities WHERE user_id = $1 AND provider = $2 LIMIT 1", userID, loginProvider).Scan(&identifier)

	// 2. FINGERPRINTING
	fpBase := fmt.Sprintf("%s|%s|%s", deviceInfo.IP, deviceInfo.UserAgent, deviceInfo.Fingerprint)
	fpHash := sha256.Sum256([]byte(fpBase))
	fpHex := hex.EncodeToString(fpHash[:])

	// 3. Parse Access Token Duration
	ttlDuration, err := time.ParseDuration(accessTTL)
	if err != nil {
		ttlDuration = time.Hour
	}

	// 4. GENERATE ACCESS TOKEN (JWT)
	claims := jwt.MapClaims{
		"sub":          userID,
		"role":         "authenticated",
		"aud":          "authenticated",
		"identifier":   identifier,
		"provider":     loginProvider,
		"fpt":          fpHex[:16],
		"iat":          time.Now().Unix(),
		"exp":          time.Now().Add(ttlDuration).Unix(),
		"app_metadata": map[string]interface{}{"provider": loginProvider, "role": "authenticated"},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	accessToken, _ := token.SignedString([]byte(jwtSecret))

	// 5. GENERATE REFRESH TOKEN
	rawRT := GenerateRandomToken(40)
	rtHash := sha256.Sum256([]byte(rawRT))
	rtHex := hex.EncodeToString(rtHash[:])
	expiresRT := time.Now().AddDate(0, 0, refreshDays)

	_, err = pool.Exec(ctx,
		"INSERT INTO auth.refresh_tokens (token_hash, user_id, expires_at, ip_address, user_agent, fingerprint_hash) VALUES ($1, $2, $3, $4, $5, $6)",
		rtHex, userID, expiresRT, deviceInfo.IP, deviceInfo.UserAgent, fpHex)
	if err != nil {
		// Fallback for older schema
		pool.Exec(ctx, "INSERT INTO auth.refresh_tokens (token_hash, user_id, expires_at) VALUES ($1, $2, $3)", rtHex, userID, expiresRT)
	}

	return &types.AuthSession{
		AccessToken:  accessToken,
		RefreshToken: rawRT,
		ExpiresIn:    int(ttlDuration.Seconds()),
		TokenType:    "bearer",
		User: types.AuthUser{
			ID:    userID,
			Email: identifier,
			Role:  "authenticated",
		},
	}, nil
}

// RefreshSession performs secure token rotation
func (s *AuthService) RefreshSession(
	ctx context.Context,
	rawRT string,
	pool *pgxpool.Pool,
	jwtSecret string,
	accessTTL string,
	deviceInfo types.DeviceInfo,
) (*types.AuthSession, error) {
	oldHash := sha256.Sum256([]byte(rawRT))
	oldHex := hex.EncodeToString(oldHash[:])

	newRawRT := GenerateRandomToken(40)
	newHash := sha256.Sum256([]byte(newRawRT))
	newHex := hex.EncodeToString(newHash[:])

	// 1. PL/pgSQL Atomic Swap
	var status, userID string
	err := pool.QueryRow(ctx, "SELECT status, user_id FROM auth.refresh_session_v3($1, $2, $3, $4)",
		oldHex, newHex, deviceInfo.IP, deviceInfo.UserAgent).Scan(&status, &userID)

	if err != nil || status != "ok" {
		return nil, fmt.Errorf("invalid refresh operation: %s", status)
	}

	// 2. Sovereign Lock Check
	fpBase := fmt.Sprintf("%s|%s|%s", deviceInfo.IP, deviceInfo.UserAgent, deviceInfo.Fingerprint)
	currentFpHash := sha256.Sum256([]byte(fpBase))
	currentFpHex := hex.EncodeToString(currentFpHash[:])

	var storedFp string
	pool.QueryRow(ctx, "SELECT fingerprint_hash FROM auth.refresh_tokens WHERE token_hash = $1", newHex).Scan(&storedFp)

	if storedFp != "" && !TimingSafeEqual(storedFp, currentFpHex) {
		pool.Exec(ctx, "UPDATE auth.refresh_tokens SET revoked = true WHERE token_hash = $1", newHex)
		return nil, fmt.Errorf("security violation: session hijacking detected")
	}

	return s.CreateSession(ctx, userID, pool, jwtSecret, accessTTL, 30, "cascata", deviceInfo)
}

// --- OTP & MFA (TOTP) ---

func (s *AuthService) GenerateTOTPSecret(issuer, label string) (string, string) {
	secretBytes := make([]byte, 20)
	io.ReadFull(rand.Reader, secretBytes)
	secret := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(secretBytes)

	u := url.URL{
		Scheme: "otpauth",
		Host:   "totp",
		Path:   fmt.Sprintf("/%s:%s", url.QueryEscape(issuer), url.QueryEscape(label)),
	}
	q := u.Query()
	q.Set("secret", secret)
	q.Set("issuer", issuer)
	q.Set("algorithm", "SHA1")
	q.Set("digits", "6")
	q.Set("period", "30")
	u.RawQuery = q.Encode()

	return secret, u.String()
}

func (s *AuthService) VerifyTOTP(secret, code string) bool {
	if len(code) != 6 {
		return false
	}

	decoded, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(secret))
	if err != nil {
		return false
	}

	// HOTP Algorithm (RFC 4226)
	counter := time.Now().Unix() / 30

	check := func(c int64) bool {
		buf := make([]byte, 8)
		binary.BigEndian.PutUint64(buf, uint64(c))

		h := hmac.New(sha1.New, decoded)
		h.Write(buf)
		sum := h.Sum(nil)

		offset := sum[len(sum)-1] & 0xf
		binaryVal := binary.BigEndian.Uint32(sum[offset : offset+4])
		binaryVal &= 0x7fffffff

		otp := binaryVal % 1000000
		return fmt.Sprintf("%06d", otp) == code
	}

	// Clock drift tolerance (1 step)
	return check(counter) || check(counter-1) || check(counter+1)
}

// --- PASSWORDLESS ---

func (s *AuthService) InitiatePasswordless(ctx context.Context, pool *pgxpool.Pool, provider, identifier string) error {
	codeBytes := make([]byte, 3)
	io.ReadFull(rand.Reader, codeBytes)
	code := fmt.Sprintf("%06d", binary.BigEndian.Uint32(append([]byte{0}, codeBytes...))%1000000)

	_, err := pool.Exec(ctx, "INSERT INTO auth.otp_codes (provider, identifier, code, expires_at) VALUES ($1, $2, $3, NOW() + interval '15 minutes') ON CONFLICT (provider, identifier) DO UPDATE SET code = $3, expires_at = EXCLUDED.expires_at, attempts = 0",
		provider, identifier, code)
	if err != nil {
		return err
	}

	// Dispatch the OTP code via configured delivery methods
	var project *types.Project
	if val := ctx.Value(types.CascataCtxKey); val != nil {
		if req, ok := val.(*types.CascataRequest); ok {
			project = req.Project
		}
	}

	if project != nil {
		dispatcher := NewAuthDispatchService(SystemPool)
		if dispatchErr := dispatcher.DispatchOTP(ctx, project, provider, identifier, code, nil); dispatchErr != nil {
			log.Printf("[AuthService] Warning: failed to dispatch OTP code: %v", dispatchErr)
		}
	} else {
		log.Printf("[AuthService] Warning: Project context not found, skipped OTP dispatching for %s (%s)", identifier, provider)
	}

	return nil
}

// --- HELPER: Check if user is neutralized (Panic Signal) ---

func (s *AuthService) CheckUserNeutralized(ctx context.Context, slug, userID string) bool {
	if dragonfly == nil {
		return false
	}
	key := fmt.Sprintf("panic:neutralized:%s:%s", slug, userID)
	val, err := dragonfly.Get(ctx, key).Result()
	return err == nil && val == "1"
}

// --- PASSWORD HELPERS (Methods) ---

func (s *AuthService) HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 10)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

func (s *AuthService) ComparePassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// CompareAPIKeyHash validates an API key against its bcrypt hash (standalone, no AuthService instance required)
func CompareAPIKeyHash(hash, rawKey string) error {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(rawKey))
}

// --- OAUTH HELPERS (Standalone Functions for OAuth Providers) ---

// OAuthState holds the state parameter for OAuth flow
type OAuthState struct {
	RedirectTo string `json:"redirect_to"`
	Provider   string `json:"provider"`
	ClientID   string `json:"client_id"` // Identity-Aware Key Bridging
	Language   string `json:"language"`
}

// GenerateOAuthState creates a new state token
func GenerateOAuthState(redirectTo, provider, clientID, language string) string {
	state := OAuthState{
		RedirectTo: redirectTo,
		Provider:   provider,
		ClientID:   clientID,
		Language:   language,
	}
	data, _ := json.Marshal(state)
	return base64.URLEncoding.EncodeToString(data)
}

// ParseOAuthState decodes state token
func ParseOAuthState(state string) (*OAuthState, error) {
	data, err := base64.URLEncoding.DecodeString(state)
	if err != nil {
		return nil, err
	}
	var s OAuthState
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// GetProviderConfig extracts provider config from project metadata
func GetProviderConfig(metadata *types.ProjectMetadata, provider string) *ProviderConfig {
	if metadata == nil || metadata.Extra == nil {
		return nil
	}

	authConfig, ok := metadata.Extra["auth_config"].(map[string]interface{})
	if !ok {
		return nil
	}

	providers, ok := authConfig["providers"].(map[string]interface{})
	if !ok {
		return nil
	}

	providerData, ok := providers[provider].(map[string]interface{})
	if !ok {
		return nil
	}

	config := &ProviderConfig{
		ClientID:     getStringFromMap(providerData, "client_id"),
		ClientSecret: getStringFromMap(providerData, "client_secret"),
		RedirectURI:  getStringFromMap(providerData, "redirect_uri"),
		AutoVerify:   getBool(providerData, "auto_verify"),
	}

	// Handle authorized_clients (could be string or array)
	if ac, ok := providerData["authorized_clients"].(string); ok {
		config.AuthorizedClients = ac
	} else if acArr, ok := providerData["authorized_clients"].([]interface{}); ok {
		clients := []string{}
		for _, c := range acArr {
			if s, ok := c.(string); ok {
				clients = append(clients, s)
			}
		}
		config.AuthorizedClients = strings.Join(clients, ",")
	}

	return config
}

func getStringFromMap(m map[string]interface{}, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func getBool(m map[string]interface{}, key string) bool {
	if v, ok := m[key].(bool); ok {
		return v
	}
	return false
}

// GetAuthURL generates OAuth authorization URL
func GetAuthURL(provider string, config *ProviderConfig, state string) (string, error) {
	switch provider {
	case "google":
		return getGoogleAuthURL(config, state)
	case "github":
		return getGitHubAuthURL(config, state)
	default:
		return "", fmt.Errorf("provider %s not supported", provider)
	}
}

func getGoogleAuthURL(config *ProviderConfig, state string) (string, error) {
	if config.ClientID == "" {
		return "", fmt.Errorf("google client_id not configured")
	}

	nonce := generateNonce()

	authURL := "https://accounts.google.com/o/oauth2/v2/auth"
	params := url.Values{}
	params.Set("client_id", config.ClientID)
	params.Set("redirect_uri", config.RedirectURI)
	params.Set("response_type", "code")
	params.Set("scope", "openid email profile")
	params.Set("state", state)
	params.Set("nonce", nonce)
	params.Set("access_type", "offline")
	params.Set("prompt", "consent")

	return authURL + "?" + params.Encode(), nil
}

func getGitHubAuthURL(config *ProviderConfig, state string) (string, error) {
	if config.ClientID == "" {
		return "", fmt.Errorf("github client_id not configured")
	}

	authURL := "https://github.com/login/oauth/authorize"
	params := url.Values{}
	params.Set("client_id", config.ClientID)
	params.Set("redirect_uri", config.RedirectURI)
	params.Set("scope", "read:user user:email")
	params.Set("state", state)

	return authURL + "?" + params.Encode(), nil
}

func generateNonce() string {
	b := make([]byte, 16)
	rand.Read(b)
	return base64.URLEncoding.EncodeToString(b)
}

// ExchangeCode exchanges authorization code for user profile
func ExchangeCode(provider, code string, config *ProviderConfig) (*UserProfile, error) {
	switch provider {
	case "google":
		return exchangeGoogleCode(code, config)
	case "github":
		return exchangeGitHubCode(code, config)
	default:
		return nil, fmt.Errorf("provider %s not supported", provider)
	}
}

func exchangeGoogleCode(code string, config *ProviderConfig) (*UserProfile, error) {
	// Exchange code for tokens
	tokenURL := "https://oauth2.googleapis.com/token"
	data := url.Values{}
	data.Set("client_id", config.ClientID)
	data.Set("client_secret", config.ClientSecret)
	data.Set("code", code)
	data.Set("grant_type", "authorization_code")
	data.Set("redirect_uri", config.RedirectURI)

	resp, err := http.Post(tokenURL, "application/x-www-form-urlencoded", strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("failed to exchange code: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("token exchange failed: %s", string(body))
	}

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		IDToken     string `json:"id_token"`
		TokenType   string `json:"token_type"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("failed to parse token response: %w", err)
	}

	// If we have ID token, verify it
	if tokenResp.IDToken != "" {
		return verifyGoogleIDToken(tokenResp.IDToken, config)
	}

	// Otherwise fetch user info
	return fetchGoogleUserInfo(tokenResp.AccessToken)
}

func verifyGoogleIDToken(idToken string, config *ProviderConfig) (*UserProfile, error) {
	url := fmt.Sprintf("https://oauth2.googleapis.com/tokeninfo?id_token=%s", idToken)
	resp, err := http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to verify id_token: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("id_token verification failed: %s", string(body))
	}

	var payload struct {
		Sub     string `json:"sub"`
		Email   string `json:"email"`
		Name    string `json:"name"`
		Picture string `json:"picture"`
		Aud     string `json:"aud"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("failed to parse id_token: %w", err)
	}

	// Verify audience
	mainClientID := config.ClientID
	extraClientIDs := []string{}
	if config.AuthorizedClients != "" {
		for _, id := range strings.Split(config.AuthorizedClients, ",") {
			id = strings.TrimSpace(id)
			if id != "" {
				extraClientIDs = append(extraClientIDs, id)
			}
		}
	}
	allowedAudiences := append([]string{mainClientID}, extraClientIDs...)

	audienceValid := false
	for _, aud := range allowedAudiences {
		if aud == payload.Aud {
			audienceValid = true
			break
		}
	}
	if !audienceValid && len(allowedAudiences) > 0 {
		return nil, fmt.Errorf("token audience mismatch")
	}

	return &UserProfile{
		Provider:  "google",
		ID:        payload.Sub,
		Email:     payload.Email,
		Name:      payload.Name,
		AvatarURL: payload.Picture,
	}, nil
}

func fetchGoogleUserInfo(accessToken string) (*UserProfile, error) {
	url := "https://www.googleapis.com/oauth2/v2/userinfo"
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch user info: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("user info fetch failed: %s", string(body))
	}

	var profile struct {
		ID      string `json:"id"`
		Email   string `json:"email"`
		Name    string `json:"name"`
		Picture string `json:"picture"`
	}
	if err := json.Unmarshal(body, &profile); err != nil {
		return nil, fmt.Errorf("failed to parse user info: %w", err)
	}

	return &UserProfile{
		Provider:  "google",
		ID:        profile.ID,
		Email:     profile.Email,
		Name:      profile.Name,
		AvatarURL: profile.Picture,
	}, nil
}

func exchangeGitHubCode(code string, config *ProviderConfig) (*UserProfile, error) {
	// Exchange code for access token
	tokenURL := "https://github.com/login/oauth/access_token"
	data := map[string]string{
		"client_id":     config.ClientID,
		"client_secret": config.ClientSecret,
		"code":          code,
		"redirect_uri":  config.RedirectURI,
	}
	jsonData, _ := json.Marshal(data)

	req, _ := http.NewRequest("POST", tokenURL, strings.NewReader(string(jsonData)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to exchange code: %w", err)
	}
	defer resp.Body.Close()

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		Error       string `json:"error"`
		ErrorDesc   string `json:"error_description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return nil, fmt.Errorf("failed to parse token response: %w", err)
	}

	if tokenResp.Error != "" {
		return nil, fmt.Errorf("github token error: %s", tokenResp.ErrorDesc)
	}

	// Fetch user info
	return fetchGitHubUserInfo(tokenResp.AccessToken)
}

func fetchGitHubUserInfo(accessToken string) (*UserProfile, error) {
	url := "https://api.github.com/user"
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch user info: %w", err)
	}
	defer resp.Body.Close()

	var profile struct {
		ID        int    `json:"id"`
		Login     string `json:"login"`
		Name      string `json:"name"`
		Email     string `json:"email"`
		AvatarURL string `json:"avatar_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&profile); err != nil {
		return nil, fmt.Errorf("failed to parse user info: %w", err)
	}

	// If no email, fetch from emails endpoint
	email := profile.Email
	if email == "" {
		email = fetchGitHubEmail(accessToken)
	}

	return &UserProfile{
		Provider:  "github",
		ID:        fmt.Sprintf("%d", profile.ID),
		Email:     email,
		Name:      profile.Name,
		AvatarURL: profile.AvatarURL,
	}, nil
}

func fetchGitHubEmail(accessToken string) string {
	url := "https://api.github.com/user/emails"
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	var emails []struct {
		Email    string `json:"email"`
		Primary  bool   `json:"primary"`
		Verified bool   `json:"verified"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&emails); err != nil {
		return ""
	}

	for _, e := range emails {
		if e.Primary && e.Verified {
			return e.Email
		}
	}
	return ""
}

// --- STANDALONE SESSION FUNCTIONS (Called by Controllers) ---

// UpsertUser creates or updates a user from OAuth profile
func UpsertUser(projectPool *pgxpool.Pool, profile *UserProfile, authConfig map[string]interface{}) (string, error) {
	// Check for provider auto_verify setting
	providerAutoVerify := false
	if authConfig != nil {
		if providers, ok := authConfig["providers"].(map[string]interface{}); ok {
			if providerData, ok := providers[profile.Provider].(map[string]interface{}); ok {
				if av, ok := providerData["auto_verify"].(bool); ok {
					providerAutoVerify = av
				}
			}
		}
	}

	// Call the database function
	var userID string
	profileJSON, _ := json.Marshal(profile)
	err := projectPool.QueryRow(
		context.Background(),
		"SELECT auth.upsert_user_v2($1::jsonb, $2::boolean) as user_id",
		string(profileJSON), providerAutoVerify,
	).Scan(&userID)

	if err != nil {
		return "", fmt.Errorf("upsert user failed: %w", err)
	}

	return userID, nil
}

// Session represents the authentication session
type Session struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	User         User   `json:"user"`
}

// User represents the user in the session
type User struct {
	ID            string                 `json:"id"`
	Email         string                 `json:"email"`
	Identifier    string                 `json:"identifier"`
	Provider      string                 `json:"provider"`
	UserMetadata  map[string]interface{} `json:"user_metadata"`
	AppMetadata   map[string]interface{} `json:"app_metadata"`
}

// CreateSession creates a new authentication session
func CreateSession(
	userID string,
	projectPool *pgxpool.Pool,
	jwtSecret string,
	expiresIn string,
	refreshTokenExpiresInDays int,
	loginProvider string,
	deviceInfo map[string]string,
) (*Session, error) {
	// Get primary identifier for this user
	var primaryIdentifier string
	err := projectPool.QueryRow(
		context.Background(),
		"SELECT identifier FROM auth.identities WHERE user_id = $1 AND provider = $2 LIMIT 1",
		userID, loginProvider,
	).Scan(&primaryIdentifier)
	if err != nil {
		// Not fatal, continue without identifier
		primaryIdentifier = ""
	}

	// Create fingerprint hash
	fingerprintBase := fmt.Sprintf("%s|%s|%s", deviceInfo["ip"], deviceInfo["userAgent"], deviceInfo["fingerprint"])
	fingerprintHash := sha256.Sum256([]byte(fingerprintBase))
	fingerprintHex := hex.EncodeToString(fingerprintHash[:])[:16]

	// Create JWT
	now := time.Now()
	expiresAt := now.Add(time.Hour) // Default 1 hour

	claims := jwt.MapClaims{
		"sub":        userID,
		"role":       "authenticated",
		"aud":        "authenticated",
		"identifier": primaryIdentifier,
		"provider":   loginProvider,
		"fpt":        fingerprintHex,
		"app_metadata": map[string]interface{}{
			"provider": loginProvider,
			"role":     "authenticated",
		},
		"iat": now.Unix(),
		"exp": expiresAt.Unix(),
	}

	// Parse expiresIn duration
	if expiresIn != "" {
		dur, err := time.ParseDuration(expiresIn)
		if err == nil {
			claims["exp"] = now.Add(dur).Unix()
			expiresAt = now.Add(dur)
		}
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	accessToken, err := token.SignedString([]byte(jwtSecret))
	if err != nil {
		return nil, fmt.Errorf("failed to sign token: %w", err)
	}

	// Generate refresh token
	refreshTokenBytes := make([]byte, 40)
	rand.Read(refreshTokenBytes)
	rawRefreshToken := hex.EncodeToString(refreshTokenBytes)
	tokenHash := sha256.Sum256([]byte(rawRefreshToken))
	tokenHashHex := hex.EncodeToString(tokenHash[:])

	refreshExpiresAt := time.Now().AddDate(0, 0, refreshTokenExpiresInDays)

	// Insert refresh token with fingerprint
	_, err = projectPool.Exec(
		context.Background(),
		`INSERT INTO auth.refresh_tokens (token_hash, user_id, expires_at, ip_address, user_agent, fingerprint_hash) 
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		tokenHashHex, userID, refreshExpiresAt, deviceInfo["ip"], deviceInfo["userAgent"], fingerprintHex,
	)
	if err != nil {
		// Try without fingerprint columns (backward compat)
		_, err = projectPool.Exec(
			context.Background(),
			`INSERT INTO auth.refresh_tokens (token_hash, user_id, expires_at) VALUES ($1, $2, $3)`,
			tokenHashHex, userID, refreshExpiresAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to store refresh token: %w", err)
		}
	}

	// Get user metadata
	var userMetadata map[string]interface{}
	err = projectPool.QueryRow(
		context.Background(),
		"SELECT raw_user_meta_data FROM auth.users WHERE id = $1",
		userID,
	).Scan(&userMetadata)
	if err != nil {
		userMetadata = map[string]interface{}{}
	}

	return &Session{
		AccessToken:  accessToken,
		RefreshToken: rawRefreshToken,
		ExpiresIn:    int(expiresAt.Sub(now).Seconds()),
		User: User{
			ID:           userID,
			Email:        "", // Will be filled by caller if needed
			Identifier:   primaryIdentifier,
			Provider:     loginProvider,
			UserMetadata: userMetadata,
			AppMetadata: map[string]interface{}{
				"provider": loginProvider,
				"role":     "authenticated",
			},
		},
	}, nil
}
