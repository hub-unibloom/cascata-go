package services

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"

	"cascata-backend/internal/types"
)

const AppAnonKeyPrefix = "anon_"

// GenerateRandomNonce generates a cryptographically secure random nonce
func GenerateRandomNonce(length int) (string, error) {
	bytes := make([]byte, length/2) // hex encoding doubles the length
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

// GenerateAppAnonKey creates a new app-specific anonymous key
// Format: anon_<app_id>_<base64url(HMAC-SHA256(app_id + ":" + nonce, secret))>
func GenerateAppAnonKey(appID, nonce, jwtSecret string) string {
	message := appID + ":" + nonce
	h := hmac.New(sha256.New, []byte(jwtSecret))
	h.Write([]byte(message))
	signature := base64.RawURLEncoding.EncodeToString(h.Sum(nil))
	return AppAnonKeyPrefix + appID + "_" + signature
}

// ValidateAppAnonKey validates an app-specific anonymous key against the project app clients
// Returns the matching AppClient if valid, nil otherwise
func ValidateAppAnonKey(key string, appClients []types.AppClient, jwtSecret string) *types.AppClient {
	// Check prefix
	if !strings.HasPrefix(key, AppAnonKeyPrefix) {
		return nil
	}

	// Parse key: anon_<app_id>_<signature>
	withoutPrefix := strings.TrimPrefix(key, AppAnonKeyPrefix)
	parts := strings.SplitN(withoutPrefix, "_", 2)
	if len(parts) != 2 {
		return nil
	}

	appID := parts[0]
	_ = parts[1] // signature extracted but we compare full keys for constant-time verification

	// Find app client by ID with O(n) search
	// In production, this should use a pre-built map for O(1) lookup
	for _, appClient := range appClients {
		if appClient.ID != appID {
			continue
		}

		// Check if app is active
		if !appClient.Active {
			return nil
		}

		// Recalculate HMAC with stored nonce
		expectedKey := GenerateAppAnonKey(appID, appClient.Nonce, jwtSecret)
		
		// Constant-time comparison to prevent timing attacks
		if hmac.Equal([]byte(key), []byte(expectedKey)) {
			return &appClient
		}

		return nil
	}

	return nil
}

// BuildAppClientIndex creates an O(1) lookup index from app_clients
// Key format includes the full anon_key for direct validation
func BuildAppClientIndex(appClients []types.AppClient, jwtSecret string) map[string]*types.AppClient {
	index := make(map[string]*types.AppClient, len(appClients))
	
	for i := range appClients {
		appClient := &appClients[i]
		if !appClient.Active {
			continue
		}
		
		// Index by the full anon_key
		anonKey := GenerateAppAnonKey(appClient.ID, appClient.Nonce, jwtSecret)
		index[anonKey] = appClient
	}
	
	return index
}

// GenerateAppClientID creates a URL-safe unique ID from app name
// Example: "Driver Mobile App" -> "driver-mobile-app-a1b2c3"
func GenerateAppClientID(name string) string {
	// Simple slugification
	slug := strings.ToLower(name)
	slug = strings.ReplaceAll(slug, " ", "-")
	slug = strings.ReplaceAll(slug, "_", "-")
	
	// Add short random suffix to ensure uniqueness
	suffix, _ := GenerateRandomNonce(6)
	
	return slug + "-" + suffix[:6]
}

// CreateAppClient creates a new AppClient with generated nonce and anon_key
func CreateAppClient(name, siteURL string, allowedOrigins []string, allowedTables []string, blockedTables []string, jwtSecret string) (*types.AppClient, string, error) {
	// Generate unique ID
	appID := GenerateAppClientID(name)
	
	// Generate random nonce
	nonce, err := GenerateRandomNonce(32)
	if err != nil {
		return nil, "", fmt.Errorf("failed to generate nonce: %w", err)
	}
	
	// Create app client
	appClient := &types.AppClient{
		ID:             appID,
		Name:           name,
		Nonce:          nonce,
		SiteURL:        siteURL,
		AllowedOrigins: allowedOrigins,
		AllowedTables:  allowedTables,
		BlockedTables:  blockedTables,
		Active:         true,
	}
	
	// Generate the anonymous key
	anonKey := GenerateAppAnonKey(appID, nonce, jwtSecret)
	
	return appClient, anonKey, nil
}

// RotateAppClientNonce generates a new nonce for an existing app client
// This invalidates the old anon_key and generates a new one
func RotateAppClientNonce(appClient *types.AppClient, jwtSecret string) (string, error) {
	newNonce, err := GenerateRandomNonce(32)
	if err != nil {
		return "", fmt.Errorf("failed to generate new nonce: %w", err)
	}
	
	appClient.Nonce = newNonce
	newAnonKey := GenerateAppAnonKey(appClient.ID, newNonce, jwtSecret)
	
	return newAnonKey, nil
}
