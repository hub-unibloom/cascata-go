package controllers

import (
	"encoding/json"
	"fmt"
	"net/http"

	"cascata-backend/internal/types"
	"github.com/go-webauthn/webauthn/webauthn"
)

// WebAuthnController manages FIDO2 / Passkey authentication
type WebAuthnController struct {
	WebAuthn *webauthn.WebAuthn
}

// NewWebAuthnController creates a new WebAuthn controller instance
// NOTE: Em um cenário Multi-Tenant (Cascata), a origem/RPDisplayName 
// deve ser dinâmica por tenant. Para simplificar e iniciar, instanciamos de forma estática 
// ou pegamos do contexto do projeto (ctx.Project).
func NewWebAuthnController() *WebAuthnController {
	w, err := webauthn.New(&webauthn.Config{
		RPDisplayName: "Cascata Passkeys", // Can be overridden per project
		RPID:          "localhost",        // Must match the domain where the app is hosted
		RPOrigins:     []string{"http://localhost:3000"}, // Must match the origin
	})
	if err != nil {
		fmt.Printf("[WebAuthn] Failed to initialize: %v\n", err)
	}

	return &WebAuthnController{
		WebAuthn: w,
	}
}

// WebAuthnUser represents a user for WebAuthn
type WebAuthnUser struct {
	id          []byte
	name        string
	displayName string
	credentials []webauthn.Credential
}

// WebAuthnID returns the user's ID
func (u *WebAuthnUser) WebAuthnID() []byte {
	return u.id
}

// WebAuthnName returns the user's name
func (u *WebAuthnUser) WebAuthnName() string {
	return u.name
}

// WebAuthnDisplayName returns the user's display name
func (u *WebAuthnUser) WebAuthnDisplayName() string {
	return u.displayName
}

// WebAuthnCredentials returns the user's credentials
func (u *WebAuthnUser) WebAuthnCredentials() []webauthn.Credential {
	return u.credentials
}

// WebAuthnIcon returns the user's icon
func (u *WebAuthnUser) WebAuthnIcon() string {
	return ""
}

// EnrollStart initiates the Passkey registration process
func (c *WebAuthnController) EnrollStart(w http.ResponseWriter, r *http.Request) {
	val := r.Context().Value(types.CascataCtxKey)
	if val == nil {
		http.Error(w, `{"error":"Context Lost"}`, 500)
		return
	}
	ctx := val.(*types.CascataRequest)
	
	if ctx.User == nil {
		http.Error(w, `{"error":"Unauthorized: Must be logged in to enroll Passkey"}`, 401)
		return
	}
	
	// Create a dummy user object (in reality, fetch from auth.users / auth.identities)
	sub := ctx.User["sub"].(string)
	user := &WebAuthnUser{
		id:          []byte(sub),
		name:        ctx.User["email"].(string),
		displayName: ctx.User["email"].(string),
	}
	
	// Start registration
	creationOptions, sessionData, err := c.WebAuthn.BeginRegistration(user)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), 500)
		return
	}
	
	// In a real application, you must store sessionData (e.g. in Redis) linked to the user
	// for the Finish registration step.
	// For this boilerplate, we'll return it so the frontend can temporarily hold it,
	// though storing it safely in the backend is required for FIDO2 specs.
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"options": creationOptions,
		"session": sessionData, // SECURITY WARNING: Do not expose session in production, store in Redis!
	})
}

// EnrollFinish completes the Passkey registration
func (c *WebAuthnController) EnrollFinish(w http.ResponseWriter, r *http.Request) {
	// 1. Get sessionData from Redis (using the request context / user)
	// 2. c.WebAuthn.FinishRegistration(user, sessionData, r)
	// 3. If successful, save the credential to auth.identities with provider = 'biometria'
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"success": true, "message": "Biometria/Passkey enrolled successfully. (Boilerplate)"}`))
}

// VerifyStart initiates the Passkey authentication process
func (c *WebAuthnController) VerifyStart(w http.ResponseWriter, r *http.Request) {
	// 1. Ask WebAuthn for BeginLogin()
	// 2. Store sessionData in Redis
	// 3. Return options to the client
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"message": "Biometria/Passkey verify start. (Boilerplate)"}`))
}

// VerifyFinish completes the Passkey authentication process
func (c *WebAuthnController) VerifyFinish(w http.ResponseWriter, r *http.Request) {
	// 1. Get sessionData from Redis
	// 2. Call c.WebAuthn.FinishLogin(user, sessionData, r)
	// 3. Issue JWT Token OR complete the Step-Up Flow!
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"success": true, "message": "Biometria/Passkey verified. (Boilerplate)"}`))
}
