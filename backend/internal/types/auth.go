package types

import "time"

type AuthUser struct {
	ID           string                 `json:"id"`
	Sub          string                 `json:"sub"`
	Role         string                 `json:"role"`
	Aud          string                 `json:"aud"`
	Email        string                 `json:"email"`
	UserMetadata map[string]interface{} `json:"user_metadata"`
	AppMetadata  map[string]interface{} `json:"app_metadata"`
	CreatedAt    time.Time              `json:"created_at"`
	LastSignInAt *time.Time             `json:"last_sign_in_at"`
}

type AuthSession struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresIn    int       `json:"expires_in"`
	TokenType    string    `json:"token_type"`
	User         AuthUser  `json:"user"`
}

type DeviceInfo struct {
	IP          string `json:"ip"`
	UserAgent   string `json:"user_agent"`
	Fingerprint string `json:"fingerprint"`
}
