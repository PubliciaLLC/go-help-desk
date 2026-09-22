package middleware_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	authmw "github.com/publiciallc/go-help-desk/backend/internal/middleware"
)

var errNope = errors.New("nope")

func guestHandler(t *testing.T, resolve authmw.GuestTokenResolver) http.Handler {
	t.Helper()
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := authmw.GuestTicketID(r)
		require.True(t, ok, "a request that reaches the handler must carry a ticket")
		_, _ = w.Write([]byte(id))
	})
	return authmw.GuestAuth(resolve)(next)
}

func alwaysResolves(id string) authmw.GuestTokenResolver {
	return func(context.Context, string) (string, error) { return id, nil }
}

// The middleware admits a request whose token resolves, and hands the handler
// the ticket rather than the token.
func TestGuestAuth_AdmitsAResolvableToken(t *testing.T) {
	var seen string
	h := guestHandler(t, func(_ context.Context, raw string) (string, error) {
		seen = raw
		return "the-ticket", nil
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Guest abc123")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "abc123", seen, "the raw token reaches the resolver and nothing else")
	require.Equal(t, "the-ticket", rec.Body.String())
}

// Every refusal is the same refusal. A status or body that varied would tell
// whoever is guessing which of their guesses was once real.
func TestGuestAuth_EveryRefusalIsIdentical(t *testing.T) {
	cases := []struct {
		name    string
		header  string
		resolve authmw.GuestTokenResolver
	}{
		{"no header", "", alwaysResolves("t")},
		{"wrong scheme", "Bearer abc123", alwaysResolves("t")},
		{"bearer is not guest", "Bearer ", alwaysResolves("t")},
		{"empty token", "Guest ", alwaysResolves("t")},
		{"whitespace token", "Guest    ", alwaysResolves("t")},
		{"token does not resolve", "Guest abc123", func(context.Context, string) (string, error) {
			return "", errNope
		}},
	}

	var bodies []string
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}
			rec := httptest.NewRecorder()
			guestHandler(t, tc.resolve).ServeHTTP(rec, req)

			require.Equal(t, http.StatusNotFound, rec.Code,
				"not 401, which invites a retry, and not 403, which admits the token was real")
			b, _ := io.ReadAll(rec.Result().Body)
			bodies = append(bodies, string(b))
		})
	}
	for i := 1; i < len(bodies); i++ {
		require.Equal(t, bodies[0], bodies[i], "case %d differs", i)
	}
	require.NotContains(t, bodies[0], "abc123", "a refusal must not echo the token back")
}

// A guest is not an actor. Nothing that reads GetActor may see one, or a guest
// token becomes a session by accident.
func TestGuestAuth_DoesNotProduceAnActor(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Nil(t, authmw.GetActor(r), "a guest must never appear as a signed-in actor")
		w.WriteHeader(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Guest abc123")
	rec := httptest.NewRecorder()
	authmw.GuestAuth(alwaysResolves("the-ticket"))(next).ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

// A request that never went through the middleware carries no guest authority,
// so a handler mounted elsewhere cannot be tricked into finding one.
func TestGuestAuth_TicketIsAbsentWithoutTheMiddleware(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Guest abc123")
	_, ok := authmw.GuestTicketID(req)
	require.False(t, ok)
}
