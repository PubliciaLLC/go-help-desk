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
	"github.com/open-help-desk/open-help-desk/backend/internal/domain/auth"
	"github.com/open-help-desk/open-help-desk/backend/internal/domain/user"
)

type contextKey string

const actorKey contextKey = "actor"

// Actor is attached to request context by the auth middleware.
type Actor struct {
	UserID    uuid.UUID
	Role      user.Role
	MFAPassed bool
	ClientID  string   // non-empty for OAuth2 bearer token requests
	Scopes    []string
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
			raw, ok := session.Values["session"]
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
			next.ServeHTTP(w, setActor(r, &Actor{
				UserID:    u.ID,
				Role:      u.Role,
				MFAPassed: true,
				Scopes:    key.Scopes,
			}))
		})
	}
}

// BearerAuth reads "Authorization: Bearer <jwt>", verifies it as an OAuth2
// client credentials token, and attaches a synthetic actor.
func BearerAuth(jwtSecret string) func(http.Handler) http.Handler {
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
			next.ServeHTTP(w, setActor(r, &Actor{
				Role:      user.RoleStaff, // OAuth clients act at staff level
				MFAPassed: true,
				ClientID:  claims.ClientID,
				Scopes:    claims.Scopes,
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
