package server

import (
	"github.com/google/uuid"
	"net/http"
	"strings"

	"golang.org/x/oauth2"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
)

func (s *Server) handleOIDCLogin(w http.ResponseWriter, r *http.Request) {

	if s.oidcProvider == nil {
		Error(w,
			http.StatusServiceUnavailable,
			"oidc_not_configured",
			"OIDC is not configured")
		return
	}

	state := uuid.New().String()

	if err := s.writeSession(
		w,
		r,
		auth.SessionData{
			OIDCState: state,
		},
	); err != nil {
		handleError(w, err)
		return
	}

	url := s.oidcProvider.Config.AuthCodeURL(
		state,
		oauth2.AccessTypeOffline,
	)

	http.Redirect(w, r, url, http.StatusFound)
}

func (s *Server) handleOIDCCallback(w http.ResponseWriter, r *http.Request) {

	if s.oidcProvider == nil {
		Error(w,
			http.StatusServiceUnavailable,
			"oidc_not_configured",
			"OIDC is not configured")
		return
	}

	session, err := s.sessions.Get(r, auth.SessionName)
	if err != nil {
		handleError(w, err)
		return
	}

	sd, ok := session.Values["session"].(auth.SessionData)

	if !ok {
		Error(w,
			http.StatusUnauthorized,
			"invalid_session",
			"OIDC session state missing")
		return
	}

	if sd.OIDCState == "" || r.URL.Query().Get("state") != sd.OIDCState {
		Error(w,
			http.StatusUnauthorized,
			"invalid_state",
			"OIDC state validation failed")
		return
	}

	state := r.URL.Query().Get("state")

	if state == "" {
		Error(w, http.StatusBadRequest, "missing_state", "OIDC state missing")
		return
	}

	code := r.URL.Query().Get("code")

	if code == "" {
		Error(w,
			http.StatusBadRequest,
			"missing_code",
			"OIDC authorization code missing")
		return
	}

	token, err := s.oidcProvider.Config.Exchange(
		r.Context(),
		code,
	)

	if err != nil {
		handleError(w, err)
		return
	}

	rawIDToken, ok := token.Extra("id_token").(string)

	if !ok {
		Error(w,
			http.StatusUnauthorized,
			"missing_id_token",
			"OIDC provider did not return id_token")
		return
	}

	idToken, err := s.oidcProvider.Verifier.Verify(
		r.Context(),
		rawIDToken,
	)

	if err != nil {

		Error(w,
			http.StatusUnauthorized,
			"invalid_id_token",
			"OIDC token validation failed")
		return
	}

	var claims auth.OIDCClaims

	if err := idToken.Claims(&claims); err != nil {
		handleError(w, err)
		return
	}

	email := strings.TrimSpace(claims.Email)

	name := claims.Name

	if name == "" {
		name = claims.PreferredUsername
	}

	u, err := s.users.UpsertOIDCUser(
		r.Context(),
		claims.Subject,
		email,
		name,
	)

	if err != nil {
		handleError(w, err)
		return
	}

	if err := s.writeSession(
		w,
		r,
		auth.SessionData{
			UserID:    u.ID,
			Role:      u.Role,
			MFAPassed: true,
			OIDCState: "",
		},
	); err != nil {
		handleError(w, err)
		return
	}

	http.Redirect(w, r, "/", http.StatusSeeOther)
}
