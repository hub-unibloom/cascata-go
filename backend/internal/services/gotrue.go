package services

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"cascata-backend/internal/types"
	"github.com/jackc/pgx/v5/pgxpool"
)

type GoTrueService struct{}

type GoTrueTokenParams struct {
	Email        string `json:"email"`
	Identifier   string `json:"identifier"`
	Password     string `json:"password"`
	RefreshToken string `json:"refresh_token"`
	IdToken      string `json:"id_token"`
	Provider     string `json:"provider"`
	GrantType    string `json:"grant_type"` // password, refresh_token, id_token, magic_link
	Token        string `json:"token"`
	Language     string `json:"language"`
	TotpCode     string `json:"totp_code"`
}

func (s *GoTrueService) HandleSignup(
	ctx context.Context,
	pool *pgxpool.Pool,
	params map[string]interface{},
	jwtSecret string,
	deviceInfo types.DeviceInfo,
) (interface{}, error) {
	log.Printf("[GOTRUE-SIGNUP] ===== INÍCIO HANDLE SIGNUP =====")

	var (
		userID       string
		session      *types.AuthSession
		err          error
		exists       bool
		passwordHash *string
		authSvc      = &AuthService{}
	)

	// Log dos parâmetros recebidos (sem senha)
	provider, _ := params["provider"].(string)
	if provider == "" { provider = "email" }
	log.Printf("[GOTRUE-SIGNUP] Provider: %s", provider)

	identifier, _ := params["identifier"].(string)
	if identifier == "" { identifier, _ = params["email"].(string) }
	log.Printf("[GOTRUE-SIGNUP] Identifier: %s", identifier)

	password, _ := params["password"].(string)
	hasPassword := password != ""
	log.Printf("[GOTRUE-SIGNUP] HasPassword: %v", hasPassword)

	data, _ := params["data"].(map[string]interface{})
	log.Printf("[GOTRUE-SIGNUP] Data fields: %v", len(data))

	if identifier == "" {
		log.Printf("[GOTRUE-SIGNUP] ✗ ERRO: Identifier é obrigatório")
		return nil, fmt.Errorf("Identifier is required")
	}

	// 1. Identity Check
	log.Printf("[GOTRUE-SIGNUP] ETAPA 1/4 - Verificando se identidade existe...")
	err = pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM auth.identities WHERE provider = $1 AND identifier = $2)", provider, identifier).Scan(&exists)
	if err != nil {
		log.Printf("[GOTRUE-SIGNUP] ✗ ERRO ao verificar identidade: %v", err)
		return nil, err
	}
	if exists {
		log.Printf("[GOTRUE-SIGNUP] ✗ ERRO: Usuário já existe (provider=%s, identifier=%s)", provider, identifier)
		return nil, fmt.Errorf("user_already_exists")
	}
	log.Printf("[GOTRUE-SIGNUP] ✓ Identidade disponível")

	// 2. User Creation
	log.Printf("[GOTRUE-SIGNUP] ETAPA 2/4 - Iniciando transação e criando usuário...")
	tx, err := pool.Begin(ctx)
	if err != nil {
		log.Printf("[GOTRUE-SIGNUP] ✗ ERRO ao iniciar transação: %v", err)
		return nil, err
	}
	defer tx.Rollback(ctx)
	log.Printf("[GOTRUE-SIGNUP] ✓ Transação iniciada")

	log.Printf("[GOTRUE-SIGNUP] Inserindo em auth.users...")
	err = tx.QueryRow(ctx, "INSERT INTO auth.users (raw_user_meta_data) VALUES ($1) RETURNING id", data).Scan(&userID)
	if err != nil {
		log.Printf("[GOTRUE-SIGNUP] ✗ ERRO ao inserir usuário: %v", err)
		return nil, err
	}
	log.Printf("[GOTRUE-SIGNUP] ✓ Usuário criado com ID: %s", userID)

	// 3. Identity Bind
	log.Printf("[GOTRUE-SIGNUP] ETAPA 3/4 - Criando identidade...")
	if password != "" {
		h, _ := authSvc.HashPassword(password)
		passwordHash = &h
		log.Printf("[GOTRUE-SIGNUP] ✓ Password hash gerado")
	}

	log.Printf("[GOTRUE-SIGNUP] Inserindo em auth.identities...")
	_, err = tx.Exec(ctx, `
		INSERT INTO auth.identities (user_id, provider, identifier, password_hash, identity_data, verified_at)
		VALUES ($1, $2, $3, $4, $5, now())`, userID, provider, identifier, passwordHash, data)

	if err != nil {
		log.Printf("[GOTRUE-SIGNUP] ✗ ERRO ao inserir identidade: %v", err)
		return nil, err
	}
	log.Printf("[GOTRUE-SIGNUP] ✓ Identidade criada")

	log.Printf("[GOTRUE-SIGNUP] Commit da transação...")
	if err := tx.Commit(ctx); err != nil {
		log.Printf("[GOTRUE-SIGNUP] ✗ ERRO no commit: %v", err)
		return nil, err
	}
	log.Printf("[GOTRUE-SIGNUP] ✓ Transação commitada")

	// 4. Create Session
	log.Printf("[GOTRUE-SIGNUP] ETAPA 4/4 - Criando sessão...")
	session, err = authSvc.CreateSession(ctx, userID, pool, jwtSecret, "1h", 30, provider, deviceInfo)
	if err != nil {
		log.Printf("[GOTRUE-SIGNUP] ✗ ERRO ao criar sessão: %v", err)
		return nil, err
	}
	log.Printf("[GOTRUE-SIGNUP] ✓ Sessão criada: AccessToken=%s...", session.AccessToken[:20])

	log.Printf("[GOTRUE-SIGNUP] ===== FIM HANDLE SIGNUP - SUCESSO =====")
	return s.FormatSessionResponse(ctx, pool, session), nil
}

func (s *GoTrueService) HandleToken(
	ctx context.Context,
	pool *pgxpool.Pool,
	params GoTrueTokenParams,
	jwtSecret string,
	deviceInfo types.DeviceInfo,
) (interface{}, error) {
	var (
		err     error
		session *types.AuthSession
		authSvc = &AuthService{}
	)

	if params.GrantType == "password" {
		provider := params.Provider
		if provider == "" { provider = "email" }
		identifier := params.Identifier
		if identifier == "" { identifier = params.Email }

		var identity struct {
			UserId       string
			PasswordHash *string
			VerifiedAt   *time.Time
		}
		err = pool.QueryRow(ctx, "SELECT user_id, password_hash, verified_at FROM auth.identities WHERE provider = $1 AND identifier = $2", provider, identifier).
			Scan(&identity.UserId, &identity.PasswordHash, &identity.VerifiedAt)

		if err != nil { return nil, fmt.Errorf("Invalid login credentials") }
		if identity.PasswordHash == nil { return nil, fmt.Errorf("Invalid login credentials") }

		if !authSvc.ComparePassword(*identity.PasswordHash, params.Password) {
			return nil, fmt.Errorf("Invalid login credentials")
		}

		// MFA Check (Only verified TOTP secrets are checked)
		var totpSecret string
		err = pool.QueryRow(ctx, "SELECT identifier FROM auth.identities WHERE user_id = $1 AND provider = 'totp' AND verified_at IS NOT NULL LIMIT 1", identity.UserId).Scan(&totpSecret)
		if err == nil && totpSecret != "" {
			if params.TotpCode == "" { 
				// The Google Flow (Choice/Step-Up)
				return nil, fmt.Errorf(`{"error":"step_up_required","message":"This account requires additional verification to sign in.","supported_factors":["totp"]}`)
			}
			if !authSvc.VerifyTOTP(totpSecret, params.TotpCode) {
				return nil, fmt.Errorf("Invalid MFA Code")
			}
		}

		session, err = authSvc.CreateSession(ctx, identity.UserId, pool, jwtSecret, "1h", 30, provider, deviceInfo)
		if err != nil { return nil, err }

		return s.FormatSessionResponse(ctx, pool, session), nil
	}

	if params.GrantType == "refresh_token" {
		session, err = authSvc.RefreshSession(ctx, params.RefreshToken, pool, jwtSecret, "1h", deviceInfo)
		if err != nil { return nil, err }
		return s.FormatSessionResponse(ctx, pool, session), nil
	}

	if params.GrantType == "magic_link" {
		provider := params.Provider
		if provider == "" { provider = "email" }
		identifier := params.Identifier
		if identifier == "" { identifier = params.Email }

		var storedCode string
		err := pool.QueryRow(ctx, "SELECT code FROM auth.otp_codes WHERE provider = $1 AND identifier = $2 AND expires_at > NOW()", provider, identifier).Scan(&storedCode)
		if err != nil || storedCode != params.Token {
			return nil, fmt.Errorf("Invalid or expired magic link token")
		}

		pool.Exec(ctx, "DELETE FROM auth.otp_codes WHERE provider = $1 AND identifier = $2", provider, identifier)

		var userID string
		err = pool.QueryRow(ctx, "SELECT user_id FROM auth.identities WHERE provider = $1 AND identifier = $2", provider, identifier).Scan(&userID)
		if err != nil {
			return nil, fmt.Errorf("user_not_found")
		}

		// MFA Check (Only verified TOTP secrets are checked)
		var totpSecret string
		err = pool.QueryRow(ctx, "SELECT identifier FROM auth.identities WHERE user_id = $1 AND provider = 'totp' AND verified_at IS NOT NULL LIMIT 1", userID).Scan(&totpSecret)
		if err == nil && totpSecret != "" {
			if params.TotpCode == "" { 
				return nil, fmt.Errorf(`{"error":"step_up_required","message":"This account requires additional verification to sign in.","supported_factors":["totp"]}`)
			}
			if !authSvc.VerifyTOTP(totpSecret, params.TotpCode) {
				return nil, fmt.Errorf("Invalid MFA Code")
			}
		}

		session, err = authSvc.CreateSession(ctx, userID, pool, jwtSecret, "1h", 30, provider, deviceInfo)
		if err != nil { return nil, err }

		return s.FormatSessionResponse(ctx, pool, session), nil
	}

	return nil, fmt.Errorf("Unsupported grant_type")
}

func (s *GoTrueService) HandleGetUser(ctx context.Context, pool *pgxpool.Pool, userID string) (interface{}, error) {
	var user struct {
		ID                string
		RawUserMetadata   map[string]interface{}
		CreatedAt         time.Time
		LastSignInAt      *time.Time
		UserConcatenation []string
	}
	err := pool.QueryRow(ctx, "SELECT id, raw_user_meta_data, created_at, last_sign_in_at, user_concatenation FROM auth.users WHERE id = $1", userID).
		Scan(&user.ID, &user.RawUserMetadata, &user.CreatedAt, &user.LastSignInAt, &user.UserConcatenation)
	if err != nil {
		return nil, err
	}

	rows, _ := pool.Query(ctx, "SELECT id, user_id, identity_data, provider, last_sign_in_at, created_at, verified_at FROM auth.identities WHERE user_id = $1", userID)
	var identities []map[string]interface{}
	for rows.Next() {
		var id, uid, provider string
		var data map[string]interface{}
		var last, created, verified *time.Time
		rows.Scan(&id, &uid, &data, &provider, &last, &created, &verified)
		identities = append(identities, map[string]interface{}{
			"id": id, "user_id": uid, "identity_data": data, "provider": provider, "last_sign_in_at": last, "created_at": created, "verified_at": verified,
		})
	}

	return s.FormatUserObject(user.ID, user.RawUserMetadata, user.CreatedAt, user.LastSignInAt, identities, user.UserConcatenation), nil
}

func (s *GoTrueService) FormatSessionResponse(ctx context.Context, pool *pgxpool.Pool, session *types.AuthSession) map[string]interface{} {
	expiresAt := time.Now().Unix() + int64(session.ExpiresIn)

	// Query identities for the user
	identities := []map[string]interface{}{}
	if pool != nil {
		rows, err := pool.Query(ctx, "SELECT id, user_id, identity_data, provider, last_sign_in_at, created_at, verified_at FROM auth.identities WHERE user_id = $1", session.User.ID)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var id, uid, provider string
				var dataRaw []byte
				var last, created, verified *time.Time
				if err := rows.Scan(&id, &uid, &dataRaw, &provider, &last, &created, &verified); err == nil {
					var data map[string]interface{}
					if len(dataRaw) > 0 {
						json.Unmarshal(dataRaw, &data)
					}
					identities = append(identities, map[string]interface{}{
						"id": id, "user_id": uid, "identity_data": data, "provider": provider, "last_sign_in_at": last, "created_at": created, "verified_at": verified,
					})
				}
			}
		}
	}

	// Query user_concatenation
	userConcatenation := []string{"vazio"}
	if pool != nil {
		var uc []string
		err := pool.QueryRow(ctx, "SELECT user_concatenation FROM auth.users WHERE id = $1", session.User.ID).Scan(&uc)
		if err == nil && len(uc) > 0 {
			userConcatenation = uc
		}
	}

	return map[string]interface{}{
		"access_token":  session.AccessToken,
		"token_type":    "bearer",
		"expires_in":    session.ExpiresIn,
		"expires_at":    expiresAt,
		"refresh_token": session.RefreshToken,
		"user":          s.FormatUserObject(session.User.ID, session.User.UserMetadata, session.User.CreatedAt, session.User.LastSignInAt, identities, userConcatenation),
	}
}

func (s *GoTrueService) FormatUserObject(id string, metadata map[string]interface{}, created time.Time, lastLogin *time.Time, identities []map[string]interface{}, userConcatenation []string) map[string]interface{} {
	providers := []string{}
	var confirmedAt interface{} = nil
	for _, i := range identities {
		providers = append(providers, i["provider"].(string))
		if i["verified_at"] != nil {
			confirmedAt = i["verified_at"]
		}
	}

	return map[string]interface{}{
		"id":                 id,
		"aud":                "authenticated",
		"role":               "authenticated",
		"email":              metadata["email"],
		"email_confirmed_at": confirmedAt,
		"confirmed_at":       confirmedAt,
		"last_sign_in_at":    lastLogin,
		"app_metadata":       map[string]interface{}{"provider": "cascata", "providers": providers},
		"user_metadata":      metadata,
		"identities":         identities,
		"user_concatenation": userConcatenation,
		"created_at":         created,
		"updated_at":         created,
	}
}
