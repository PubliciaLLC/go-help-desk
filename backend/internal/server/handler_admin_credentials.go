package server

import (
	"fmt"
	"github.com/publiciallc/go-help-desk/backend/internal/safehttp"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/publiciallc/go-help-desk/backend/internal/database/authstore"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
	authmw "github.com/publiciallc/go-help-desk/backend/internal/middleware"
	"github.com/publiciallc/go-help-desk/backend/internal/server/notify"
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
	// Required, not defaulted. With deny-by-default an empty list produces a
	// credential that can do nothing, and the caller would not find out until
	// the integration started returning 403. Omitting the field used to reach
	// the database as NULL and come back as an opaque 500 — on the exact call
	// the upgrade notes tell every operator to make.
	if len(body.Scopes) == 0 {
		Error(w, http.StatusBadRequest, "scopes_required",
			"scopes is required: a credential with no scopes is refused on every route. "+
				"See GET /api/v1/admin/scopes for the available scopes.")
		return
	}
	// Reject unknown scopes at creation. Unrecognised entries are ignored at
	// enforcement time, so without this a typo produces a credential that
	// looks restricted, is accepted, and quietly grants less than intended.
	if err := auth.ValidateScopes(body.Scopes); err != nil {
		Error(w, http.StatusBadRequest, "invalid_scope", err.Error())
		return
	}
	// A machine credential cannot mint one broader than itself. Without this,
	// credentials:write is every scope: hold only that, issue a key with
	// users:write, use it. Escalation by one extra request is not a boundary.
	if isMachine(r) {
		if over, ok := auth.Subset(authmw.GetActor(r).Scopes, body.Scopes); !ok {
			Error(w, http.StatusForbidden, "insufficient_scope",
				"this credential cannot grant "+over+", which it does not hold itself")
			return
		}
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

// handleListScopes returns the scope catalogue.
//
// Served rather than duplicated in the frontend so the picker cannot drift from
// what the server enforces: a scope the UI offers but the server rejects would
// produce a credential that fails at creation, and one the server knows but the
// UI omits would be silently unreachable.
func (s *Server) handleListScopes(w http.ResponseWriter, r *http.Request) {
	type scopeInfo struct {
		Scope    string `json:"scope"`
		Resource string `json:"resource"`
		Action   string `json:"action"`
	}
	all := auth.All()
	out := make([]scopeInfo, len(all))
	for i, sc := range all {
		out[i] = scopeInfo{Scope: sc.String(), Resource: sc.Resource, Action: string(sc.Action)}
	}
	JSON(w, http.StatusOK, out)
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
	// Required for the same reason as on API keys: with deny-by-default an
	// empty list creates a client that can do nothing, and omitting the field
	// reached the database as NULL and returned an opaque 500.
	if len(body.Scopes) == 0 {
		Error(w, http.StatusBadRequest, "scopes_required",
			"scopes is required: a credential with no scopes is refused on every route. "+
				"See GET /api/v1/admin/scopes for the available scopes.")
		return
	}
	if err := auth.ValidateScopes(body.Scopes); err != nil {
		Error(w, http.StatusBadRequest, "invalid_scope", err.Error())
		return
	}
	// A machine credential cannot mint one broader than itself. Without this,
	// credentials:write is every scope: hold only that, issue a key with
	// users:write, use it. Escalation by one extra request is not a boundary.
	if isMachine(r) {
		if over, ok := auth.Subset(authmw.GetActor(r).Scopes, body.Scopes); !ok {
			Error(w, http.StatusForbidden, "insufficient_scope",
				"this credential cannot grant "+over+", which it does not hold itself")
			return
		}
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

// validPayloadFormat reports whether v is empty (defaults to raw) or one of
// notify.Formats. Checked here, before the row is written, so a typo is a
// 400 at the moment it's made rather than a constraint violation surfaced as
// a 500 — the CHECK on webhook_configs.payload_format is the last line of
// defense, not the first.
func validPayloadFormat(v string) bool {
	if v == "" {
		return true
	}
	return notify.Format(v).IsValid()
}

// checkEvents validates that events is not empty and every entry is a valid event.
// Empty list: 400 `missing_events`, with the existing message unchanged.
// Otherwise, the first invalid entry gets 400 `invalid_event_name`.
// Returns false after writing the error.
func checkEvents(w http.ResponseWriter, events []string) bool {
	if len(events) == 0 {
		Error(w, http.StatusBadRequest, "missing_events",
			"events is required: a webhook with no events is refused. "+
				"Provide at least one event type.")
		return false
	}
	for _, e := range events {
		if !notify.IsWebhookEvent(e) {
			eventList := make([]string, len(notify.WebhookEvents))
			for i, evt := range notify.WebhookEvents {
				eventList[i] = string(evt)
			}
			msg := fmt.Sprintf("unknown event %q: events must be \"*\" or any of: %s",
				e, strings.Join(eventList, ", "))
			Error(w, http.StatusBadRequest, "invalid_event_name", msg)
			return false
		}
	}
	return true
}

func (s *Server) handleCreateWebhook(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URL           string   `json:"url"`
		Events        []string `json:"events"`
		Secret        string   `json:"secret"`
		PayloadFormat string   `json:"payload_format"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	// A webhook subscription with zero events would silently never fire.
	// Require events to be present and non-empty.
	if !checkEvents(w, body.Events) {
		return
	}
	// Checked here so a bad target is reported when the form is saved. The
	// real boundary is the guarded dialer in notify — a name that passes now
	// can resolve somewhere else by delivery time.
	if err := safehttp.ValidateURL(body.URL); err != nil {
		Error(w, http.StatusBadRequest, "invalid_url", err.Error())
		return
	}
	if !validPayloadFormat(body.PayloadFormat) {
		Error(w, http.StatusBadRequest, "invalid_payload_format",
			"payload_format must be one of: raw, slack, teams, discord, jira")
		return
	}
	payloadFormat := body.PayloadFormat
	if payloadFormat == "" {
		payloadFormat = string(notify.FormatRaw)
	}
	wh := authstore.WebhookConfig{
		ID:            uuid.New(),
		URL:           body.URL,
		Events:        body.Events,
		Secret:        body.Secret,
		Enabled:       true,
		CreatedAt:     time.Now(),
		PayloadFormat: payloadFormat,
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
		URL           *string  `json:"url"`
		Events        []string `json:"events"`
		Secret        *string  `json:"secret"`
		Enabled       *bool    `json:"enabled"`
		PayloadFormat *string  `json:"payload_format"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	if body.URL != nil {
		// Checked here for the same reason as on create, and it was missing:
		// an existing hook could be repointed at a private address and accepted
		// with 200. The guarded dialer still refused it at delivery, so nothing
		// was ever fetched — but the hook was then stored, shown as enabled,
		// and silently never delivered, which is indistinguishable from a
		// working one because delivery failures are not recorded anywhere.
		if err := safehttp.ValidateURL(*body.URL); err != nil {
			Error(w, http.StatusBadRequest, "invalid_url", err.Error())
			return
		}
		existing.URL = *body.URL
	}
	if body.Events != nil {
		// Validated only when sent, like url above: a PATCH of {"enabled":
		// false} must not be gated on a field it does not touch.
		if !checkEvents(w, body.Events) {
			return
		}
		existing.Events = body.Events
	}
	if body.Secret != nil {
		existing.Secret = *body.Secret
	}
	if body.Enabled != nil {
		existing.Enabled = *body.Enabled
	}
	if body.PayloadFormat != nil {
		// Validated only when sent, like url above: a PATCH of {"enabled":
		// false} must not be gated on a field it does not touch.
		if !validPayloadFormat(*body.PayloadFormat) {
			Error(w, http.StatusBadRequest, "invalid_payload_format",
				"payload_format must be one of: raw, slack, teams, discord, jira")
			return
		}
		existing.PayloadFormat = *body.PayloadFormat
		if existing.PayloadFormat == "" {
			existing.PayloadFormat = string(notify.FormatRaw)
		}
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
