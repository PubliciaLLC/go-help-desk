package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

var ticketsWrite = auth.Scope{Resource: auth.ResourceTickets, Action: auth.ActionWrite}
var ticketsRead = auth.Scope{Resource: auth.ResourceTickets, Action: auth.ActionRead}

func runWithActor(t *testing.T, a *Actor, required auth.Scope) *httptest.ResponseRecorder {
	t.Helper()
	reached := false
	h := RequireScope(required)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if a != nil {
		req = setActor(req, a)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code == http.StatusOK {
		require.True(t, reached, "a 200 must mean the handler actually ran")
	} else {
		require.False(t, reached, "the handler must not run when the scope is refused")
	}
	return rr
}

func TestRequireScope(t *testing.T) {
	cases := []struct {
		name     string
		actor    *Actor
		required auth.Scope
		want     int
	}{
		{
			// The defect this whole change exists to fix: a credential issued
			// before enforcement carries no scopes and could do everything.
			name:     "scoped credential with no scopes is denied",
			actor:    &Actor{UserID: uuid.New(), Role: user.RoleAdmin, Machine: true},
			required: ticketsRead, want: http.StatusForbidden,
		},
		{
			name: "scoped credential with the scope is allowed",
			actor: &Actor{UserID: uuid.New(), Role: user.RoleAdmin, Machine: true,
				Scopes: []string{"tickets:read"}},
			required: ticketsRead, want: http.StatusOK,
		},
		{
			name: "write satisfies read",
			actor: &Actor{UserID: uuid.New(), Role: user.RoleAdmin, Machine: true,
				Scopes: []string{"tickets:write"}},
			required: ticketsRead, want: http.StatusOK,
		},
		{
			name: "read does not satisfy write",
			actor: &Actor{UserID: uuid.New(), Role: user.RoleAdmin, Machine: true,
				Scopes: []string{"tickets:read"}},
			required: ticketsWrite, want: http.StatusForbidden,
		},
		{
			name: "a different resource does not satisfy it",
			actor: &Actor{UserID: uuid.New(), Role: user.RoleAdmin, Machine: true,
				Scopes: []string{"users:write"}},
			required: ticketsWrite, want: http.StatusForbidden,
		},
		{
			// A browser session is the user, with whatever their role allows.
			// There is nothing to narrow when the human is driving.
			name:     "an unscoped session passes through",
			actor:    &Actor{UserID: uuid.New(), Role: user.RoleAdmin},
			required: ticketsWrite, want: http.StatusOK,
		},
		{
			name:  "no actor is 401, not 403",
			actor: nil, required: ticketsRead, want: http.StatusUnauthorized,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rr := runWithActor(t, tc.actor, tc.required)
			require.Equal(t, tc.want, rr.Code)
		})
	}
}

// The refusal must say which scope was missing. An integration owner debugging
// a 403 otherwise has to guess, and guessing leads to granting everything.
func TestRequireScope_NamesTheMissingScope(t *testing.T) {
	rr := runWithActor(t,
		&Actor{UserID: uuid.New(), Role: user.RoleAdmin, Machine: true,
			Scopes: []string{"users:read"}},
		ticketsWrite)

	require.Equal(t, http.StatusForbidden, rr.Code)
	require.Contains(t, rr.Body.String(), "insufficient_scope")
	require.Contains(t, rr.Body.String(), "tickets:write")
}

// Scopes narrow; they never grant. This is why RequireScope runs after
// RequireRole rather than instead of it.
func TestRequireScope_CannotGrantBeyondTheRole(t *testing.T) {
	// A reporting user's key carrying users:write.
	actor := &Actor{UserID: uuid.New(), Role: user.RoleUser, Machine: true,
		Scopes: []string{"users:write"}}

	reached := false
	// The real ordering: role first, then scope.
	h := RequireRole(user.RoleAdmin)(
		RequireScope(auth.Scope{Resource: auth.ResourceUsers, Action: auth.ActionWrite})(
			http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { reached = true })))

	req := setActor(httptest.NewRequest(http.MethodGet, "/", nil), actor)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	require.Equal(t, http.StatusForbidden, rr.Code)
	require.False(t, reached)
	require.Contains(t, rr.Body.String(), "insufficient permissions",
		"the role must refuse first — a scope must never be what lets someone in")
}
