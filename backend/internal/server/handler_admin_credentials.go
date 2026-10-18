package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/publiciallc/go-help-desk/backend/internal/safehttp"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/publiciallc/go-help-desk/backend/internal/database/authstore"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/audit"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	authmw "github.com/publiciallc/go-help-desk/backend/internal/middleware"
	"github.com/publiciallc/go-help-desk/backend/internal/server/notify"
)

// Admin machine credentials: API keys, OAuth clients and webhooks.
//
// Split out of handler_admin.go, which had grown to 1,332 lines across twelve
// unrelated resources. Moved verbatim: no handler logic changed.

// ── API Keys ─────────────────────────────────────────────────────────────────

// Who may see and revoke which keys (Erik, 2026-10-13, see #466, and narrowed
// the same day): an administrator signed in through the browser sees and
// revokes every key, with its owner shown. An API key sees and revokes only
// itself: not its owner's other keys, and not anyone else's. The distinction
// is the credential, not the role: a key held by an administrator is still a
// key, and a leaked one must not be able to revoke the owner's other keys,
// which are what would let them recover.
func (s *Server) handleListAPIKeys(w http.ResponseWriter, r *http.Request) {
	a := authmw.GetActor(r)
	var only *uuid.UUID
	if a.Machine {
		only = &a.APIKeyID
	}
	keys, err := s.authStore.ListWithOwner(r.Context(), only)
	if err != nil {
		handleError(w, err)
		return
	}
	// Marked so an administrator can find the keys worth rotating (#450).
	years := s.adminSvc.APIKeyMaxLifetimeYears(r.Context())
	now := time.Now()
	type view struct {
		auth.APIKeyWithOwner
		BeyondLimit bool `json:"beyond_limit"`
	}
	out := make([]view, len(keys))
	for i, k := range keys {
		out[i] = view{k, beyondLimit(years, k.ExpiresAt, now)}
	}
	JSON(w, http.StatusOK, out)
}

func (s *Server) handleCreateAPIKey(w http.ResponseWriter, r *http.Request) {
	a := authmw.GetActor(r)
	var body struct {
		Name      string     `json:"name"`
		Scopes    []string   `json:"scopes"`
		ExpiresAt *string    `json:"expires_at"`
		UserID    *uuid.UUID `json:"user_id"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}

	// The key's owner is the caller unless an administrator names someone else
	// (#450). Only a signed-in person reaches this route (see routes.go), so
	// there is no machine credential to guard against here.
	owner := ownerOfKey{id: a.UserID, role: a.Role}
	if body.UserID != nil && *body.UserID != a.UserID {
		target, err := s.users.GetByIDAdmin(r.Context(), *body.UserID)
		if err != nil {
			handleError(w, err)
			return
		}
		if target.DeletedAt != nil {
			handleError(w, fmt.Errorf("user %s: %w", target.ID, user.ErrNotFound))
			return
		}
		// Who may own a key (Erik, 2026-10-16): staff, and the administrator
		// themselves. Judged before the scopes, so the refusal says the real
		// reason. A service account is an OAuth client's identity and has its
		// own credential; a requester never holds a key; and issuing for
		// another administrator is a second administrator's credential minted
		// by the first.
		switch {
		case target.ServiceAccount:
			Error(w, http.StatusBadRequest, "service_account_cannot_own_key",
				"a service account (an OAuth client's identity) cannot own an API key")
			return
		case target.Role == user.RoleUser:
			Error(w, http.StatusBadRequest, "requester_cannot_own_key", "a requester cannot own an API key")
			return
		case target.Role == user.RoleAdmin:
			Error(w, http.StatusBadRequest, "cannot_issue_for_administrator",
				"a key can be issued on behalf of a staff member, or for yourself, but not for another administrator")
			return
		}
		// A disabled account's keys are refused on every request, so the key
		// would be issued dead. Better said now than discovered by the person
		// it was issued for.
		if target.Disabled {
			Error(w, http.StatusBadRequest, "user_disabled", "that account is disabled; enable it before issuing it a key")
			return
		}
		owner = ownerOfKey{id: target.ID, role: target.Role}
	}

	if !checkKeyScopes(w, body.Scopes, owner.role) {
		return
	}
	expiresAt, ok := s.checkKeyExpiry(w, r.Context(), body.ExpiresAt)
	if !ok {
		return
	}
	s.issueAPIKey(w, r, owner, body.Name, body.Scopes, expiresAt)
}

// ownerOfKey is who a key will act as. The role is what limits its scopes.
type ownerOfKey struct {
	id   uuid.UUID
	role user.Role
}

// checkKeyScopes validates the scopes both key-creating routes accept, writing
// the refusal and returning false when they are not acceptable.
func checkKeyScopes(w http.ResponseWriter, scopes []string, ownerRole user.Role) bool {
	// Required, not defaulted. With deny-by-default an empty list produces a
	// credential that can do nothing, and the caller would not find out until
	// the integration started returning 403. Omitting the field used to reach
	// the database as NULL and come back as an opaque 500 — on the exact call
	// the upgrade notes tell every operator to make.
	if len(scopes) == 0 {
		Error(w, http.StatusBadRequest, "scopes_required",
			"scopes is required: a credential with no scopes is refused on every route. "+
				"See GET /api/v1/admin/scopes for the available scopes.")
		return false
	}
	// Reject unknown scopes at creation. Unrecognised entries are ignored at
	// enforcement time, so without this a typo produces a credential that
	// looks restricted, is accepted, and quietly grants less than intended.
	if err := auth.ValidateScopes(scopes); err != nil {
		Error(w, http.StatusBadRequest, "invalid_scope", err.Error())
		return false
	}
	// A key acts at its owner's role, so a scope that role has no route for
	// can only mislead: it looks like access the key does not have (#450).
	// Checked against the owner, not the caller: an administrator issuing a
	// key for a requester is limited by what a requester can do.
	if err := auth.WithinRole(string(ownerRole), scopes); err != nil {
		Error(w, http.StatusBadRequest, "invalid_scope", err.Error())
		return false
	}
	return true
}

// checkKeyExpiry validates the expiry both routes accept: readable, in the
// future, present when a limit applies, and within it. nil means no expiry.
func (s *Server) checkKeyExpiry(w http.ResponseWriter, ctx context.Context, expiresAt *string) (*time.Time, bool) {
	// The expiry was decoded and then dropped on the floor: the field was
	// read off the request, never copied onto the key, and the key never
	// expired. The middleware honours ExpiresAt, the client type declares it,
	// and the admin page has an "Expires" column that could therefore never
	// be filled. Create a key with a date in 2020 and it worked — which is
	// precisely the person this bites, somebody putting an expiry on a
	// script's credential.
	//
	// Refused rather than ignored when it cannot be read, and refused when it
	// is already past: a credential that expires before it is issued is a
	// request nobody means, and silently issuing a working key for it is how
	// this defect looked from outside.
	//
	// And capped (Erik, 2026-10-16): every key lives at most
	// api_key_max_lifetime_years (2 unless an administrator says otherwise),
	// and with a cap the expiry is required. Checked here, when the key is
	// made, and never when it is used: lowering the limit does not break a key
	// that exists.
	years := s.adminSvc.APIKeyMaxLifetimeYears(ctx)
	if expiresAt == nil || strings.TrimSpace(*expiresAt) == "" {
		if years != admin.APIKeyNoLimit {
			Error(w, http.StatusBadRequest, "expires_at_required",
				"expires_at is required: an API key can live at most "+lifetimeWords(years)+". Use an RFC 3339 timestamp, for example 2027-01-31T00:00:00Z")
			return nil, false
		}
		return nil, true
	}
	at, err := time.Parse(time.RFC3339, *expiresAt)
	if err != nil {
		Error(w, http.StatusBadRequest, "invalid_expires_at",
			"expires_at must be an RFC 3339 timestamp, for example 2027-01-31T00:00:00Z")
		return nil, false
	}
	if !at.After(time.Now()) {
		Error(w, http.StatusBadRequest, "invalid_expires_at",
			"expires_at is in the past, so this credential would be refused on every request")
		return nil, false
	}
	if years != admin.APIKeyNoLimit && at.After(time.Now().AddDate(years, 0, 0)) {
		Error(w, http.StatusBadRequest, "expires_at_too_far",
			"expires_at is more than the longest an API key may live, "+lifetimeWords(years)+" from now")
		return nil, false
	}
	return &at, true
}

// lifetimeWords is "1 year" or "N years".
func lifetimeWords(years int) string {
	if years == 1 {
		return "1 year"
	}
	return strconv.Itoa(years) + " years"
}

// beyondLimit reports whether a key outlives the current maximum: no expiry at
// all, or one further off than the limit allows from now. Used only to mark
// keys in the lists so they can be rotated; the limit is not applied to a key
// that already exists.
func beyondLimit(years int, expiresAt *time.Time, now time.Time) bool {
	if years == admin.APIKeyNoLimit {
		return false
	}
	return expiresAt == nil || expiresAt.After(now.AddDate(years, 0, 0))
}

// issueAPIKey stores a validated key for owner, audits it, and answers 201 with
// the raw token, which is shown once and never again.
func (s *Server) issueAPIKey(w http.ResponseWriter, r *http.Request, owner ownerOfKey, name string, scopes []string, expiresAt *time.Time) {
	a := authmw.GetActor(r)
	raw, _, err := auth.GenerateToken()
	if err != nil {
		handleError(w, err)
		return
	}
	raw = "GHD_" + raw
	key := auth.APIKey{
		ID:          uuid.New(),
		Name:        name,
		HashedToken: auth.HashToken(raw),
		UserID:      owner.id,
		Scopes:      scopes,
		ExpiresAt:   expiresAt,
		CreatedAt:   time.Now(),
		CreatedBy:   &a.UserID,
	}
	if err := s.authStore.CreateAPIKey(r.Context(), key); err != nil {
		handleError(w, err)
		return
	}
	s.auditAPIKey(r.Context(), a.UserID, "api_key_issued", key.ID, owner.id, scopes, expiresAt)
	// Return the raw token once — it will never be shown again.
	JSON(w, http.StatusCreated, map[string]any{
		"id":         key.ID,
		"token":      raw, // shown once
		"name":       key.Name,
		"expires_at": key.ExpiresAt,
		"user_id":    key.UserID,
		"created_by": key.CreatedBy,
	})
}

// auditAPIKey records a key being issued or revoked (#450), for the caller's
// own keys and for other people's alike. on_behalf is the part worth reading
// later: an administrator acting on somebody else's account.
//
// Best effort, like user.Service.writeAuditEntry and for the same reason: the
// entry does not gate anything, and a key that was issued must not be reported
// as failed because the log table had a bad moment. It is detached from the
// request, though, so hanging up right after the key is written cannot be a
// way to leave no trace of having issued it. The name is left out: it is free
// text, and the audit view shows only what cannot carry personal data.
func (s *Server) auditAPIKey(reqCtx context.Context, actorID uuid.UUID, action string, keyID, ownerID uuid.UUID, scopes []string, expiresAt *time.Time) {
	if s.auditStore == nil {
		return
	}
	after := map[string]any{
		"owner_user_id": ownerID.String(),
		"on_behalf":     ownerID != actorID,
		"scopes":        strings.Join(scopes, ","),
	}
	if expiresAt != nil {
		after["expires_at"] = expiresAt.UTC().Format(time.RFC3339)
	}
	s.writeAudit(reqCtx, audit.Entry{
		ID: uuid.New(), ActorID: &actorID, EntityType: "api_key", EntityID: keyID,
		Action: action, After: after, CreatedAt: time.Now(),
	})
}

// writeAudit is the best-effort, detached audit write the key paths share.
// reqCtx is only the parent: context.WithoutCancel keeps its values and drops
// its cancellation, exactly as user.Service.ResetMFA does. Without it a caller
// who disconnects right after the change commits (pgx then reports "context
// canceled") would suppress the entry, which is the one thing the actor
// issuing a credential might want.
func (s *Server) writeAudit(reqCtx context.Context, e audit.Entry) {
	ctx := context.WithoutCancel(reqCtx)
	if err := s.auditStore.Create(ctx, e); err != nil {
		slog.ErrorContext(ctx, "writing audit entry failed", "entity_type", e.EntityType, "entity_id", e.EntityID, "action", e.Action, "error", err)
	}
}

func (s *Server) handleDeleteAPIKey(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid ID")
		return
	}
	// Not found rather than forbidden for a key that is someone else's: the
	// answer must not say which ids exist.
	if a := authmw.GetActor(r); a.Machine && id != a.APIKeyID {
		handleError(w, fmt.Errorf("API key %s: %w", id, auth.ErrNotFound))
		return
	}
	// The owner is read first so the audit entry can say whose key it was.
	rows, err := s.authStore.ListWithOwner(r.Context(), &id)
	if err != nil {
		handleError(w, err)
		return
	}
	if len(rows) == 0 {
		handleError(w, fmt.Errorf("API key %s: %w", id, auth.ErrNotFound))
		return
	}
	if err := s.authStore.Delete(r.Context(), id); err != nil {
		handleError(w, err)
		return
	}
	s.auditAPIKey(r.Context(), authmw.GetActor(r).UserID, "api_key_revoked", id, rows[0].UserID, rows[0].Scopes, rows[0].ExpiresAt)
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
	// ?role= narrows the list to what a key owned by that role may carry, for
	// the picker when issuing on someone's behalf (#450). Only the roles that
	// can own a key.
	if role := r.URL.Query().Get("role"); role != "" {
		if role != string(user.RoleAdmin) && role != string(user.RoleStaff) {
			Error(w, http.StatusBadRequest, "bad_request", "role must be admin or staff: no other role can own an API key")
			return
		}
		all = auth.ScopesForRole(role)
	}
	out := make([]scopeInfo, len(all))
	for i, sc := range all {
		out[i] = scopeInfo{Scope: sc.String(), Resource: sc.Resource, Action: string(sc.Action)}
	}
	JSON(w, http.StatusOK, out)
}

// ── OAuth Clients ────────────────────────────────────────────────────────────

// oauthClientView is a client as the admin page lists it: the client, the id of
// the service user it acts as (inside OAuthClient), and that user's groups,
// which are the client's ticket scope.
type oauthClientView struct {
	auth.OAuthClient
	Groups []auth.ClientGroup `json:"groups"`
}

func (s *Server) handleListOAuthClients(w http.ResponseWriter, r *http.Request) {
	clients, err := s.authStore.ListOAuthClients(r.Context())
	if err != nil {
		handleError(w, err)
		return
	}
	// One query for every client's groups, not one per row.
	groups, err := s.authStore.ListOAuthClientGroups(r.Context())
	if err != nil {
		handleError(w, err)
		return
	}
	out := make([]oauthClientView, len(clients))
	for i, c := range clients {
		out[i] = oauthClientView{OAuthClient: c, Groups: groups[c.ID]}
		if out[i].Groups == nil {
			out[i].Groups = []auth.ClientGroup{} // [] and not null: "no groups" is an answer
		}
	}
	JSON(w, http.StatusOK, out)
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
	// Only a signed-in person reaches this route (routes.go): keys can't make
	// credentials, so there is no machine credential to compare scopes with.
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
		UserID:       uuid.New(), // its service user, created in the same statement
	}
	if err := s.authStore.CreateOAuthClient(r.Context(), client); err != nil {
		handleError(w, err)
		return
	}
	JSON(w, http.StatusCreated, map[string]any{
		"id":            client.ID,
		"client_id":     client.ClientID,
		"client_secret": raw,
		"name":          client.Name,
		// The service user made with it (#420): the identity the client acts
		// as, listed on Admin -> Users, where it can be given groups.
		"user_id": client.UserID,
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
	webhooks, err := s.authStore.ListWebhooks(r.Context())
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

// checkFilter decodes and validates a webhook's ticket filter (#413). It
// writes the refusal and returns false when the filter is not acceptable. The
// checks run in this order and the first failure wins, so a refusal names one
// problem. Empty or absent lists are no condition, so only the ids that are
// present are looked up.
func (s *Server) checkFilter(w http.ResponseWriter, ctx context.Context, raw json.RawMessage) (notification.WebhookFilter, bool) {
	var f notification.WebhookFilter
	dec := json.NewDecoder(bytes.NewReader(raw))
	// An unknown key would be ignored, and an ignored key widens the hook: a
	// filter that names a type the server does not know would fire for every
	// ticket. Refusing it is the only honest answer.
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		Error(w, http.StatusBadRequest, "invalid_filter",
			"filter must be an object with category_ids, priorities, group_ids and invert")
		return notification.WebhookFilter{}, false
	}
	for _, p := range f.Priorities {
		if !ticket.Priority(p).Valid() {
			Error(w, http.StatusBadRequest, "invalid_filter_priority",
				fmt.Sprintf("unknown priority %q: priorities must be any of critical, high, medium, low", p))
			return notification.WebhookFilter{}, false
		}
	}
	// Inactive categories are accepted: a ticket in one still exists, and the
	// hook may be meant for it (#413).
	cats, err := s.categories.ListCategories(ctx, false)
	if err != nil {
		handleError(w, err)
		return notification.WebhookFilter{}, false
	}
	knownCats := make(map[uuid.UUID]bool, len(cats))
	for _, c := range cats {
		knownCats[c.ID] = true
	}
	for _, id := range f.CategoryIDs {
		if !knownCats[id] {
			Error(w, http.StatusBadRequest, "unknown_filter_category", "no category "+id.String())
			return notification.WebhookFilter{}, false
		}
	}
	groups, err := s.groups.List(ctx)
	if err != nil {
		handleError(w, err)
		return notification.WebhookFilter{}, false
	}
	knownGroups := make(map[uuid.UUID]bool, len(groups))
	for _, g := range groups {
		knownGroups[g.ID] = true
	}
	for _, id := range f.GroupIDs {
		if !knownGroups[id] {
			Error(w, http.StatusBadRequest, "unknown_filter_group", "no group "+id.String())
			return notification.WebhookFilter{}, false
		}
	}
	// Inverted with nothing to invert matches no ticket: a hook that can never
	// fire is a mistake to refuse, not to store.
	if f.Invert && f.IsEmpty() {
		Error(w, http.StatusBadRequest, "empty_inverted_filter",
			"invert needs at least one condition: an inverted empty filter matches no ticket")
		return notification.WebhookFilter{}, false
	}
	return f, true
}

func (s *Server) handleCreateWebhook(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URL           string          `json:"url"`
		Events        []string        `json:"events"`
		Secret        string          `json:"secret"`
		PayloadFormat string          `json:"payload_format"`
		Enabled       *bool           `json:"enabled"`
		Filter        json.RawMessage `json:"filter"`
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
	// Absent, null and {} all mean no filter. Checked only when sent.
	var filter notification.WebhookFilter
	if len(body.Filter) > 0 {
		f, ok := s.checkFilter(w, r.Context(), body.Filter)
		if !ok {
			return
		}
		filter = f
	}
	// Omitted means enabled, which is what create always did. An explicit
	// false lets an operator save a hook before its receiver is ready.
	enabled := true
	if body.Enabled != nil {
		enabled = *body.Enabled
	}
	wh := authstore.WebhookConfig{
		APIKeyID:      callingKey(r),
		ID:            uuid.New(),
		URL:           body.URL,
		Events:        body.Events,
		Secret:        body.Secret,
		Enabled:       enabled,
		CreatedAt:     time.Now(),
		PayloadFormat: payloadFormat,
		Filter:        filter,
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
		URL           *string         `json:"url"`
		Events        []string        `json:"events"`
		Secret        *string         `json:"secret"`
		Enabled       *bool           `json:"enabled"`
		PayloadFormat *string         `json:"payload_format"`
		Filter        json.RawMessage `json:"filter"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	// Who last pointed the hook (#450): a key that changes the URL owns the
	// destination from then on, and the hook dies with the key; a signed-in
	// administrator who changes it takes it over. Anything else leaves the link
	// as it was, so a key editing the events of a session-made hook does not
	// tie it.
	repointed := body.URL != nil && *body.URL != existing.URL
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
	if repointed {
		existing.APIKeyID = callingKey(r)
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
	if len(body.Filter) > 0 {
		// Validated only when sent, like the fields above. An object replaces
		// the whole filter; null or {} clears it; absent keeps the current one.
		f, ok := s.checkFilter(w, r.Context(), body.Filter)
		if !ok {
			return
		}
		existing.Filter = f
	}
	if err := s.authStore.UpdateWebhook(r.Context(), existing); err != nil {
		handleError(w, err)
		return
	}
	// Re-read rather than echo the edited copy: a changed URL clears the last
	// delivery in the database, and the copy still carries the old receiver's
	// result.
	saved, err := s.authStore.GetWebhook(r.Context(), id)
	if err != nil {
		handleError(w, err)
		return
	}
	JSON(w, http.StatusOK, saved)
}

// callingKey is the API key making this request, or nil for a session. An
// OAuth client is not a key and never reaches the webhook routes.
func callingKey(r *http.Request) *uuid.UUID {
	if a := authmw.GetActor(r); a != nil && a.APIKeyID != uuid.Nil {
		id := a.APIKeyID
		return &id
	}
	return nil
}

// handleReapproveWebhook lets a signed-in administrator vouch for a hook that
// a key made or re-pointed: it clears the link, so the hook no longer depends
// on that key. Keys are refused at the route.
func (s *Server) handleReapproveWebhook(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid ID")
		return
	}
	if err := s.authStore.ReapproveWebhook(r.Context(), id); err != nil {
		handleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
