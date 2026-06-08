package services

import (
	"context"
	"crypto"
	"crypto/hmac"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
	"time"
)

type VaultReleasePolicy string

const (
	VaultPolicyExportable VaultReleasePolicy = "exportable"
	VaultPolicyRuntime    VaultReleasePolicy = "runtime"
	VaultPolicyVerifyOnly VaultReleasePolicy = "verify_only"
	VaultPolicySignOnly   VaultReleasePolicy = "sign_only"
)

type VaultAccessPurpose string

const (
	VaultPurposeUIReveal       VaultAccessPurpose = "ui_reveal"
	VaultPurposeAutomation     VaultAccessPurpose = "automation_runtime"
	VaultPurposeRPCRuntime     VaultAccessPurpose = "rpc_runtime"
	VaultPurposeWebhookVerify  VaultAccessPurpose = "webhook_verify"
	VaultPurposeInternalSystem VaultAccessPurpose = "internal_system"
)

var (
	ErrVaultSecretNotFound = errors.New("vault secret not found")
	ErrVaultPolicyDenied   = errors.New("vault release policy denied")
)

type VaultService struct {
	CryptoSvc *CryptoService
}

// GlobalVaultSvc is the singleton instance used across the system.
// Initialized in main.go to ensure dependencies are ready.
var GlobalVaultSvc *VaultService

type VaultSecretRecord struct {
	ID          string
	ProjectSlug string
	Name        string
	Type        string
	Description string
	Ciphertext  string
	Metadata    map[string]interface{}
	Policy      VaultReleasePolicy
}

func NewVaultService(cryptoSvc *CryptoService) *VaultService {
	return &VaultService{CryptoSvc: cryptoSvc}
}

func NormalizeVaultPolicy(raw interface{}) VaultReleasePolicy {
	policy, _ := raw.(string)
	switch VaultReleasePolicy(strings.TrimSpace(policy)) {
	case VaultPolicyExportable:
		return VaultPolicyExportable
	case VaultPolicyVerifyOnly:
		return VaultPolicyVerifyOnly
	case VaultPolicySignOnly:
		return VaultPolicySignOnly
	case VaultPolicyRuntime:
		return VaultPolicyRuntime
	default:
		return VaultPolicyRuntime
	}
}

func (s *VaultService) Fetch(ctx context.Context, projectSlug, identifier string) (*VaultSecretRecord, error) {
	if s == nil || s.CryptoSvc == nil {
		return nil, fmt.Errorf("vault service is not configured")
	}

	identifier = strings.TrimSpace(identifier)
	if strings.HasPrefix(identifier, "vault://") {
		refParts := strings.SplitN(strings.TrimPrefix(identifier, "vault://"), "/", 2)
		if len(refParts) == 2 {
			if refParts[0] != "" {
				projectSlug = refParts[0]
			}
			identifier = refParts[1]
		} else if len(refParts) == 1 {
			identifier = refParts[0]
		}
	}
	identifier = strings.TrimSpace(identifier)
	if identifier == "" {
		return nil, ErrVaultSecretNotFound
	}

	var rec VaultSecretRecord
	var metadataRaw []byte
	err := SystemPool.QueryRow(ctx, `
		SELECT id::text, project_slug, name, type, COALESCE(description, ''), COALESCE(secret_value, ''), metadata
		FROM system.project_secrets
		WHERE project_slug = $1
		  AND type <> 'folder'
		  AND (id::text = $2 OR name = $2)
	`, projectSlug, identifier).Scan(
		&rec.ID,
		&rec.ProjectSlug,
		&rec.Name,
		&rec.Type,
		&rec.Description,
		&rec.Ciphertext,
		&metadataRaw,
	)
	if err != nil {
		return nil, ErrVaultSecretNotFound
	}

	rec.Metadata = map[string]interface{}{}
	if len(metadataRaw) > 0 {
		_ = json.Unmarshal(metadataRaw, &rec.Metadata)
	}
	rec.Policy = NormalizeVaultPolicy(rec.Metadata["release_policy"])
	return &rec, nil
}

func (s *VaultService) Resolve(ctx context.Context, projectSlug, identifier string, purpose VaultAccessPurpose) (string, *VaultSecretRecord, error) {
	rec, err := s.Fetch(ctx, projectSlug, identifier)
	if err != nil {
		return "", nil, err
	}
	if !CanRevealVaultSecret(rec.Policy, purpose) {
		return "", rec, fmt.Errorf("%w: %s cannot be revealed for %s", ErrVaultPolicyDenied, rec.Policy, purpose)
	}

	plain, err := s.CryptoSvc.Decrypt(rec.Ciphertext)
	if err != nil {
		return "", rec, err
	}
	return plain, rec, nil
}

func (s *VaultService) VerifyHMACSHA256(ctx context.Context, projectSlug, identifier string, payload []byte, providedSignature string) (bool, error) {
	rec, err := s.Fetch(ctx, projectSlug, identifier)
	if err != nil {
		return false, err
	}
	if !CanUseVaultSecretForVerification(rec.Policy) {
		return false, fmt.Errorf("%w: %s cannot be used for webhook verification", ErrVaultPolicyDenied, rec.Policy)
	}

	secret, err := s.CryptoSvc.Decrypt(rec.Ciphertext)
	if err != nil {
		return false, err
	}
	return VerifyHMACSHA256(secret, payload, providedSignature), nil
}

// VerifySecret checks if a provided plaintext matches the vault secret using timing-safe comparison.
// This is used for API Key validation where the secret itself is the verification token.
func (s *VaultService) VerifySecret(ctx context.Context, projectSlug, identifier, providedValue string) (bool, error) {
	rec, err := s.Fetch(ctx, projectSlug, identifier)
	if err != nil {
		return false, err
	}
	if !CanUseVaultSecretForVerification(rec.Policy) {
		return false, fmt.Errorf("%w: %s cannot be used for verification", ErrVaultPolicyDenied, rec.Policy)
	}

	secret, err := s.CryptoSvc.Decrypt(rec.Ciphertext)
	if err != nil {
		return false, err
	}

	// Timing-safe comparison to prevent side-channel attacks
	return hmac.Equal([]byte(secret), []byte(providedValue)), nil
}

func (s *VaultService) VerifyCiphertextHMACSHA256(ciphertext string, payload []byte, providedSignature string) (bool, error) {
	if s == nil || s.CryptoSvc == nil {
		return false, fmt.Errorf("vault service is not configured")
	}
	secret, err := s.CryptoSvc.Decrypt(ciphertext)
	if err != nil {
		return false, err
	}
	return VerifyHMACSHA256(secret, payload, providedSignature), nil
}

func CanRevealVaultSecret(policy VaultReleasePolicy, purpose VaultAccessPurpose) bool {
	switch policy {
	case VaultPolicyExportable:
		return true
	case VaultPolicyRuntime:
		return purpose == VaultPurposeAutomation ||
			purpose == VaultPurposeRPCRuntime ||
			purpose == VaultPurposeWebhookVerify ||
			purpose == VaultPurposeInternalSystem
	case VaultPolicyVerifyOnly:
		return false
	case VaultPolicySignOnly:
		return false
	default:
		return false
	}
}

func CanUseVaultSecretForVerification(policy VaultReleasePolicy) bool {
	return policy == VaultPolicyExportable ||
		policy == VaultPolicyRuntime ||
		policy == VaultPolicyVerifyOnly
}

func VerifyHMACSHA256(secret string, payload []byte, providedSignature string) bool {
	signature := normalizeHMACSHA256Signature(providedSignature)
	if signature == "" {
		return false
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	expected := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(signature), []byte(expected))
}

func normalizeHMACSHA256Signature(signature string) string {
	signature = strings.TrimSpace(signature)
	signature = strings.TrimPrefix(signature, "sha256=")
	signature = strings.ToLower(signature)
	if len(signature) != sha256.Size*2 {
		return ""
	}
	if _, err := hex.DecodeString(signature); err != nil {
		return ""
	}
	return signature
}

func (s *VaultService) VerifyRSASignature(ctx context.Context, projectSlug, identifier string, payload []byte, providedSignature string) (bool, error) {
	rec, err := s.Fetch(ctx, projectSlug, identifier)
	if err != nil {
		return false, err
	}
	if !CanUseVaultSecretForVerification(rec.Policy) {
		return false, fmt.Errorf("%w: %s cannot be used for webhook verification", ErrVaultPolicyDenied, rec.Policy)
	}

	publicKeyPEM, err := s.CryptoSvc.Decrypt(rec.Ciphertext)
	if err != nil {
		return false, err
	}
	return VerifyRSASignature(publicKeyPEM, payload, providedSignature)
}

func (s *VaultService) VerifyCiphertextRSASignature(ciphertext string, payload []byte, providedSignature string) (bool, error) {
	if s == nil || s.CryptoSvc == nil {
		return false, fmt.Errorf("vault service is not configured")
	}
	publicKeyPEM, err := s.CryptoSvc.Decrypt(ciphertext)
	if err != nil {
		return false, err
	}
	return VerifyRSASignature(publicKeyPEM, payload, providedSignature)
}

func VerifyRSASignature(publicKeyPEM string, payload []byte, providedSignature string) (bool, error) {
	block, _ := pem.Decode([]byte(publicKeyPEM))
	if block == nil {
		return false, fmt.Errorf("failed to parse PEM block containing the public key")
	}

	var pub interface{}
	var err error
	pub, err = x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		pub, err = x509.ParsePKCS1PublicKey(block.Bytes)
		if err != nil {
			return false, fmt.Errorf("failed to parse RSA public key: %v", err)
		}
	}

	rsaPub, ok := pub.(*rsa.PublicKey)
	if !ok {
		return false, fmt.Errorf("not an RSA public key")
	}

	// Decode signature (usually base64 or hex)
	sigBytes, err := base64.StdEncoding.DecodeString(providedSignature)
	if err != nil {
		sigBytes, err = hex.DecodeString(providedSignature)
		if err != nil {
			return false, fmt.Errorf("failed to decode signature (tried base64 and hex): %v", err)
		}
	}

	hashed := sha256.Sum256(payload)
	err = rsa.VerifyPKCS1v15(rsaPub, crypto.SHA256, hashed[:], sigBytes)
	if err != nil {
		return false, nil // Verification failed
	}

	return true, nil
}

// ResolveAllRuntimeSecrets fetches and decrypts all project secrets that have release policies
// "runtime" or "exportable". It uses DragonflyDB cache if available, with a 45s TTL.
func (s *VaultService) ResolveAllRuntimeSecrets(ctx context.Context, projectSlug string) (map[string]string, error) {
	if s == nil || s.CryptoSvc == nil {
		return nil, fmt.Errorf("vault service is not configured")
	}

	cacheKey := fmt.Sprintf("vault:runtime:%s", projectSlug)
	rdb := GetDragonfly()

	// 1. Try reading from cache
	if rdb != nil {
		cached, err := rdb.Get(ctx, cacheKey).Result()
		if err == nil && cached != "" {
			var cachedSecrets map[string]string
			if err := json.Unmarshal([]byte(cached), &cachedSecrets); err == nil {
				return cachedSecrets, nil
			}
		}
	}

	// 2. Fetch from Postgres system.project_secrets
	rows, err := SystemPool.Query(ctx, `
		SELECT id::text, name, type, COALESCE(secret_value, ''), metadata
		FROM system.project_secrets
		WHERE project_slug = $1
		  AND type <> 'folder'
	`, projectSlug)
	if err != nil {
		return nil, fmt.Errorf("failed to query vault secrets: %w", err)
	}
	defer rows.Close()

	secrets := make(map[string]string)
	for rows.Next() {
		var id, name, secretType, ciphertext string
		var metadataRaw []byte

		if err := rows.Scan(&id, &name, &secretType, &ciphertext, &metadataRaw); err != nil {
			return nil, fmt.Errorf("failed to scan vault secret row: %w", err)
		}

		metadata := make(map[string]interface{})
		if len(metadataRaw) > 0 {
			_ = json.Unmarshal(metadataRaw, &metadata)
		}

		policy := NormalizeVaultPolicy(metadata["release_policy"])
		if policy == VaultPolicyRuntime || policy == VaultPolicyExportable {
			if ciphertext != "" {
				plain, err := s.CryptoSvc.Decrypt(ciphertext)
				if err != nil {
					return nil, fmt.Errorf("failed to decrypt vault secret %s: %w", name, err)
				}
				secrets[name] = plain
			}
		}
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error reading database rows: %w", err)
	}

	// 3. Write to DragonflyDB cache (TTL 45s)
	if rdb != nil {
		if data, err := json.Marshal(secrets); err == nil {
			_ = rdb.Set(ctx, cacheKey, string(data), 45*time.Second).Err()
		}
	}

	return secrets, nil
}

// InvalidateVaultRuntimeCache deletes the cached runtime secrets for the given project.
func InvalidateVaultRuntimeCache(ctx context.Context, projectSlug string) {
	cacheKey := fmt.Sprintf("vault:runtime:%s", projectSlug)
	rdb := GetDragonfly()
	if rdb != nil {
		_ = rdb.Del(ctx, cacheKey).Err()
	}
}
