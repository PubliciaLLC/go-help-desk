package server

import (
	"net/http"

	"github.com/gorilla/sessions"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	authmw "github.com/publiciallc/go-help-desk/backend/internal/middleware"
)

// POST /api/v1/auth/local/login
func (s *Server) handleLocalLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}

	u, err := s.users.VerifyPassword(r.Context(), body.Email, body.Password)
	if err != nil {
		Error(w, http.StatusUnauthorized, "invalid_credentials", "invalid email or password")
		return
	}

	if !user.IsLocalAuthAllowed(u, s.adminSvc.SAMLEnabled(r.Context())) {
		Error(w, http.StatusForbidden, "saml_required", "local login is disabled; use SAML")
		return
	}

	mfaPassed := !s.adminSvc.MFAEnabled(r.Context()) || !u.MFAEnabled
	if err := s.writeSession(w, r, auth.SessionData{
		UserID:    u.ID,
		Role:      u.Role,
		MFAPassed: mfaPassed,
	}); err != nil {
		handleError(w, err)
		return
	}

	JSON(w, http.StatusOK, map[string]any{
		"user":       u,
		"mfa_needed": !mfaPassed,
	})
}

// POST /api/v1/auth/local/logout
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	session, _ := s.sessions.Get(r, auth.SessionName)
	session.Options = &sessions.Options{MaxAge: -1}
	_ = session.Save(r, w)
	w.WriteHeader(http.StatusNoContent)
}

// POST /api/v1/auth/local/mfa/verify
func (s *Server) handleMFAVerify(w http.ResponseWriter, r *http.Request) {
	a := authmw.GetActor(r)
	if a == nil {
		Error(w, http.StatusUnauthorized, "unauthorized", "not logged in")
		return
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	if err := s.users.VerifyMFACode(r.Context(), a.UserID, body.Code); err != nil {
		Error(w, http.StatusUnauthorized, "invalid_mfa_code", "invalid TOTP code")
		return
	}
	if err := s.writeSession(w, r, auth.SessionData{
		UserID:    a.UserID,
		Role:      a.Role,
		MFAPassed: true,
	}); err != nil {
		handleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// POST /api/v1/auth/oauth/token
func (s *Server) handleOAuthToken(w http.ResponseWriter, r *http.Request) {
	var body struct {
		GrantType    string `json:"grant_type"`
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	if body.GrantType != "client_credentials" {
		Error(w, http.StatusBadRequest, "unsupported_grant_type", "only client_credentials is supported")
		return
	}

	client, err := s.oauthClientStore.GetByClientID(r.Context(), body.ClientID)
	if err != nil {
		Error(w, http.StatusUnauthorized, "invalid_client", "invalid client credentials")
		return
	}
	if auth.HashToken(body.ClientSecret) != client.HashedSecret {
		Error(w, http.StatusUnauthorized, "invalid_client", "invalid client credentials")
		return
	}

	token, err := auth.IssueAccessToken(client, s.cfg.JWTSecret)
	if err != nil {
		handleError(w, err)
		return
	}
	JSON(w, http.StatusOK, map[string]any{
		"access_token": token,
		"token_type":   "Bearer",
		"expires_in":   3600,
	})
}

// GET /api/v1/auth/saml/login — initiates the IdP redirect.
func (s *Server) handleSAMLLogin(w http.ResponseWriter, r *http.Request) {
	h := s.samlHTTP()
	if h == nil {
		Error(w, http.StatusServiceUnavailable, "saml_not_configured", "SAML is not configured")
		return
	}
	h.ServeHTTP(w, r)
}

// POST /api/v1/auth/saml/acs — assertion consumer service.
func (s *Server) handleSAMLACS(w http.ResponseWriter, r *http.Request) {
	h := s.samlHTTP()
	if h == nil {
		Error(w, http.StatusServiceUnavailable, "saml_not_configured", "SAML is not configured")
		return
	}
	h.ServeHTTP(w, r)
}

// GET /api/v1/auth/saml/metadata — SP metadata XML for IdP registration.
func (s *Server) handleSAMLMetadata(w http.ResponseWriter, r *http.Request) {
	h := s.samlHTTP()
	if h == nil {
		Error(w, http.StatusServiceUnavailable, "saml_not_configured", "SAML is not configured")
		return
	}
	h.ServeHTTP(w, r)
}

// writeSession persists session data to the cookie store.
func (s *Server) writeSession(w http.ResponseWriter, r *http.Request, sd auth.SessionData) error {
	// Ignore the decode error: CookieStore.Get always returns a usable session
	// even when an existing cookie can't be decoded (e.g. after a module rename
	// changes gob type paths). We're overwriting the session anyway.
	session, _ := s.sessions.Get(r, auth.SessionName)
	session.Values["session"] = sd
	return session.Save(r, w)
}
