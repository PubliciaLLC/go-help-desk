package server

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/publiciallc/go-help-desk/backend/internal/database/authstore"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
	authmw "github.com/publiciallc/go-help-desk/backend/internal/middleware"
)

// Admin machine credentials: API keys, OAuth clients and webhooks.
//
// Split out of handler_admin.go, which had grown to 1,332 lines across twelve
// unrelated resources. Moved verbatim: no handler logic changed.

// ── API Keys ─────────────────────────────────────────────────────────────────

func (s *Server) handleListAPIKeys(w http.ResponseWriter, r *http.Request) {
	a := authmw.GetActor(r)
	keys, err := s.authStore.ListByUser(r.Context(), a.UserID)
	if err != nil {
		handleError(w, err)
		return
	}
	JSON(w, http.StatusOK, keys)
}

func (s *Server) handleCreateAPIKey(w http.ResponseWriter, r *http.Request) {
	a := authmw.GetActor(r)
	var body struct {
		Name      string   `json:"name"`
		Scopes    []string `json:"scopes"`
		ExpiresAt *string  `json:"expires_at"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	raw, _, err := auth.GenerateToken()
	if err != nil {
		handleError(w, err)
		return
	}
	raw = "GHD_" + raw
	hashed := auth.HashToken(raw)
	key := auth.APIKey{
		ID:          uuid.New(),
		Name:        body.Name,
		HashedToken: hashed,
		UserID:      a.UserID,
		Scopes:      body.Scopes,
		CreatedAt:   time.Now(),
	}
	if err := s.authStore.CreateAPIKey(r.Context(), key); err != nil {
		handleError(w, err)
		return
	}
	// Return the raw token once — it will never be shown again.
	JSON(w, http.StatusCreated, map[string]any{
		"id":    key.ID,
		"token": raw, // shown once
		"name":  key.Name,
	})
}

func (s *Server) handleDeleteAPIKey(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid ID")
		return
	}
	if err := s.authStore.Delete(r.Context(), id); err != nil {
		handleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── OAuth Clients ────────────────────────────────────────────────────────────

func (s *Server) handleListOAuthClients(w http.ResponseWriter, r *http.Request) {
	clients, err := s.authStore.ListOAuthClients(r.Context())
	if err != nil {
		handleError(w, err)
		return
	}
	JSON(w, http.StatusOK, clients)
}

func (s *Server) handleCreateOAuthClient(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name   string   `json:"name"`
		Scopes []string `json:"scopes"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	raw, hashed, err := auth.GenerateToken()
	if err != nil {
		handleError(w, err)
		return
	}
	clientIDRaw, _, err2 := auth.GenerateToken()
	if err2 != nil {
		handleError(w, err2)
		return
	}
	client := auth.OAuthClient{
		ID:           uuid.New(),
		ClientID:     clientIDRaw[:16],
		HashedSecret: hashed,
		Name:         body.Name,
		Scopes:       body.Scopes,
		CreatedAt:    time.Now(),
	}
	if err := s.authStore.CreateOAuthClient(r.Context(), client); err != nil {
		handleError(w, err)
		return
	}
	JSON(w, http.StatusCreated, map[string]any{
		"client_id":     client.ClientID,
		"client_secret": raw,
		"name":          client.Name,
	})
}

func (s *Server) handleDeleteOAuthClient(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid ID")
		return
	}
	if err := s.authStore.DeleteOAuthClient(r.Context(), id); err != nil {
		handleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── Webhooks ─────────────────────────────────────────────────────────────────

func (s *Server) handleListWebhooks(w http.ResponseWriter, r *http.Request) {
	webhooks, err := s.authStore.ListEnabledWebhooks(r.Context())
	if err != nil {
		handleError(w, err)
		return
	}
	JSON(w, http.StatusOK, webhooks)
}

func (s *Server) handleCreateWebhook(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URL    string   `json:"url"`
		Events []string `json:"events"`
		Secret string   `json:"secret"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	wh := authstore.WebhookConfig{
		ID:        uuid.New(),
		URL:       body.URL,
		Events:    body.Events,
		Secret:    body.Secret,
		Enabled:   true,
		CreatedAt: time.Now(),
	}
	if err := s.authStore.CreateWebhook(r.Context(), wh); err != nil {
		handleError(w, err)
		return
	}
	JSON(w, http.StatusCreated, wh)
}

func (s *Server) handleUpdateWebhook(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid ID")
		return
	}
	existing, err := s.authStore.GetWebhook(r.Context(), id)
	if err != nil {
		handleError(w, err)
		return
	}
	var body struct {
		URL     *string  `json:"url"`
		Events  []string `json:"events"`
		Secret  *string  `json:"secret"`
		Enabled *bool    `json:"enabled"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	if body.URL != nil {
		existing.URL = *body.URL
	}
	if body.Events != nil {
		existing.Events = body.Events
	}
	if body.Secret != nil {
		existing.Secret = *body.Secret
	}
	if body.Enabled != nil {
		existing.Enabled = *body.Enabled
	}
	if err := s.authStore.UpdateWebhook(r.Context(), existing); err != nil {
		handleError(w, err)
		return
	}
	JSON(w, http.StatusOK, existing)
}

func (s *Server) handleDeleteWebhook(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid ID")
		return
	}
	if err := s.authStore.DeleteWebhook(r.Context(), id); err != nil {
		handleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
