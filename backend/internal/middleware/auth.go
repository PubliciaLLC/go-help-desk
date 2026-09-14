// Package middleware provides HTTP middleware for authentication and
// authorization.
package middleware

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/sessions"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

type contextKey string

const actorKey contextKey = "actor"

// Actor is attached to request context by the auth middleware.
type Actor struct {
	UserID    uuid.UUID
	Role      user.Role
	MFAPassed bool
	ClientID  string // non-empty for OAuth2 bearer token requests
	Scopes    []string
	// Scoped marks a credential whose scopes are enforced: API keys and OAuth
	// clients. Without it, an empty Scopes slice is ambiguous — a session has
	// none because it is unscoped, a credential has none because it was
	// granted none, and those must resolve opposite ways.
	Scoped bool
}

// GetActor retrieves the Actor from the request context. Returns nil if not set.
func GetActor(r *http.Request) *Actor {
	v, _ := r.Context().Value(actorKey).(*Actor)
	return v
}

func setActor(r *http.Request, a *Actor) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), actorKey, a))
}

// SessionAuth reads the session cookie and attaches an Actor to the context.
// Requests without a valid session are passed through unchanged — use
// RequireRole downstream to gate specific endpoints.
func SessionAuth(store sessions.Store) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, cookieErr := r.Cookie(auth.SessionName)
			session, err := store.Get(r, auth.SessionName)
			slog.Debug("session auth",
				"path", r.URL.Path,
				"cookie_present", cookieErr == nil,
				"store_err", err,
				"is_new", session.IsNew,
			)
			if err != nil || session.IsNew {
				next.ServeHTTP(w, r)
				return
			}
			raw, ok := session.Values[auth.SessionDataKey]
			if !ok {
				slog.Debug("session auth: key not found in values")
				next.ServeHTTP(w, r)
				return
			}
			sd, ok := raw.(auth.SessionData)
			if !ok {
				slog.Debug("session auth: type assertion failed", "raw_type", fmt.Sprintf("%T", raw))
				next.ServeHTTP(w, r)
				return
			}
			next.ServeHTTP(w, setActor(r, &Actor{
				UserID:    sd.UserID,
				Role:      sd.Role,
				MFAPassed: sd.MFAPassed,
			}))
		})
	}
}

// APIKeyAuthFunc is the callback used to look up an API key by its hashed token.
// It returns the key and the owning user. Return a non-nil error to reject.
type APIKeyAuthFunc func(ctx context.Context, hashed string) (auth.APIKey, user.User, error)

// OAuthClientLookupFunc resolves an OAuth client by its client ID. BearerAuth
// calls it on every request so that deleting a client revokes its outstanding
// tokens; without it a token stays valid for its full hour no matter what the
// administrator does, which is not a revocation mechanism at all.
type OAuthClientLookupFunc func(ctx context.Context, clientID string) (auth.OAuthClient, error)

// APIKeyMarkUsedFunc is called asynchronously to update last_used_at.
type APIKeyMarkUsedFunc func(ctx context.Context, id uuid.UUID, at time.Time) error

// APIKeyAuth reads "Authorization: ApiKey <token>" and attaches the owning
// user as the actor. A nil lookup function means API key auth is disabled.
func APIKeyAuth(lookup APIKeyAuthFunc) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Skip if an actor is already set (session auth took precedence).
			if GetActor(r) != nil || lookup == nil {
				next.ServeHTTP(w, r)
				return
			}
			raw := r.Header.Get("Authorization")
			const prefix = "ApiKey "
			if !strings.HasPrefix(raw, prefix) {
				next.ServeHTTP(w, r)
				return
			}
			hashed := auth.HashToken(raw[len(prefix):])
			key, u, err := lookup(r.Context(), hashed)
			if err != nil {
				next.ServeHTTP(w, r)
				return
			}
			if key.ExpiresAt != nil && time.Now().After(*key.ExpiresAt) {
				next.ServeHTTP(w, r)
				return
			}
			// Disabling a user is the first thing an operator does when an
			// account is believed compromised, and it revokes their sessions.
			// An API key is a separate credential that no revocation path
			// touched, so without this the disable cut the browser off and
			// left the scriptable, longer-lived credential working.
			if !u.IsActive() {
				next.ServeHTTP(w, r)
				return
			}
			next.ServeHTTP(w, setActor(r, &Actor{
				UserID:    u.ID,
				Role:      u.Role,
				MFAPassed: true,
				Scopes:    key.Scopes,
				Scoped:    true,
			}))
		})
	}
}

// RequireScope refuses a request whose credential does not carry the scope the
// route needs.
//
// It runs AFTER RequireRole, and that order is the whole design: a scope
// narrows, it never grants. An API key acts at its owner's role and an OAuth
// client acts as staff, so `users:write` on a key owned by a reporting user
// still reaches nothing — RequireRole has already refused.
//
// Session-authenticated requests carry no scopes and are not scoped. A browser
// session IS the user, with whatever their role allows; scopes exist to give a
// machine credential less than its owner, and there is nothing to narrow when
// the human is driving.
func RequireScope(required auth.Scope) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			a := GetActor(r)
			if a == nil {
				// No actor at all is RequireRole's to answer, not ours; saying
				// "insufficient scope" to an anonymous caller would tell them
				// the route exists and misname why they were refused.
				http.Error(w, `{"error":{"code":"unauthorized","message":"authentication required"}}`, http.StatusUnauthorized)
				return
			}
			if !a.Scoped {
				next.ServeHTTP(w, r)
				return
			}
			if !auth.Allows(a.Scopes, required) {
				// The scope name is from a fixed vocabulary with no quotes or
				// backslashes, so it cannot break the JSON literal.
				http.Error(w, fmt.Sprintf(
					`{"error":{"code":"insufficient_scope","message":"this credential does not carry the %s scope"}}`,
					required), http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// BearerAuth reads "Authorization: Bearer <jwt>", verifies it as an OAuth2
// client credentials token, and attaches a synthetic actor.
func BearerAuth(jwtSecret string, lookup OAuthClientLookupFunc) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if GetActor(r) != nil {
				next.ServeHTTP(w, r)
				return
			}
			raw := r.Header.Get("Authorization")
			const prefix = "Bearer "
			if !strings.HasPrefix(raw, prefix) {
				next.ServeHTTP(w, r)
				return
			}
			claims, err := auth.VerifyAccessToken(raw[len(prefix):], jwtSecret)
			if err != nil {
				next.ServeHTTP(w, r)
				return
			}
			// A valid signature only proves this token was issued, not that the
			// client it names still exists. Deleting a client is the only
			// revocation this system offers, and until this lookup it revoked
			// nothing: the token kept working until it expired on its own.
			if lookup == nil {
				next.ServeHTTP(w, r)
				return
			}
			if _, err := lookup(r.Context(), claims.ClientID); err != nil {
				next.ServeHTTP(w, r)
				return
			}
			next.ServeHTTP(w, setActor(r, &Actor{
				Role:      user.RoleStaff, // OAuth clients act at staff level
				MFAPassed: true,
				ClientID:  claims.ClientID,
				Scopes:    claims.Scopes,
				Scoped:    true,
			}))
		})
	}
}

// RequireRole rejects requests where the actor's role is not in the allowed set.
// Returns 401 when no actor is present, 403 when the role is insufficient.
func RequireRole(roles ...user.Role) func(http.Handler) http.Handler {
	allowed := make(map[user.Role]struct{}, len(roles))
	for _, r := range roles {
		allowed[r] = struct{}{}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			a := GetActor(r)
			if a == nil {
				http.Error(w, `{"error":{"code":"unauthorized","message":"authentication required"}}`, http.StatusUnauthorized)
				return
			}
			if _, ok := allowed[a.Role]; !ok {
				http.Error(w, `{"error":{"code":"forbidden","message":"insufficient permissions"}}`, http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RequireMFA rejects requests where the actor has not yet passed a TOTP challenge.
func RequireMFA(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a := GetActor(r)
		if a == nil || !a.MFAPassed {
			http.Error(w, `{"error":{"code":"mfa_required","message":"MFA verification required"}}`, http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}
