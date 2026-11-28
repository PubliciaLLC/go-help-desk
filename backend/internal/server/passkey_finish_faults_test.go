package server_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The two passkey finish routes used to answer 400 no_challenge for a fault
// in the session store, telling the person to start over for something that
// was the server's problem, and read any size of body (#421).

var passkeyFinishRoutes = []string{
	"/api/v1/me/passkeys/register/finish",
	"/api/v1/auth/local/passkey/finish",
}

func signedInUser(t *testing.T, h *harness) *session {
	t.Helper()
	s := &session{h: h}
	res, body := s.send(t, http.MethodPost, "/api/v1/auth/local/login",
		map[string]any{"email": "user@test.local", "password": "password"})
	require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", body)
	return s
}

func TestPasskeyFinish_ASessionStoreFaultIsA500(t *testing.T) {
	for _, path := range passkeyFinishRoutes {
		t.Run(path, func(t *testing.T) {
			h, cleanup := newHarness(t)
			defer cleanup()
			s := signedInUser(t, h)
			h.srv.FailSessionWritesForTest()

			res, body := s.send(t, http.MethodPost, path, map[string]any{})
			require.Equal(t, http.StatusInternalServerError, res.StatusCode, "body: %s", body)
			require.Contains(t, string(body), "internal_error")
		})
	}
}

// Still a 400 when it is the person's doing: nothing was started.
func TestPasskeyFinish_NoChallengeStaysA400(t *testing.T) {
	for _, path := range passkeyFinishRoutes {
		t.Run(path, func(t *testing.T) {
			h, cleanup := newHarness(t)
			defer cleanup()
			s := signedInUser(t, h)

			res, body := s.send(t, http.MethodPost, path, map[string]any{})
			require.Equal(t, http.StatusBadRequest, res.StatusCode, "body: %s", body)
			require.Contains(t, string(body), "no_challenge")
		})
	}
}

func TestPasskeyFinish_BodyCapIs64KiB(t *testing.T) {
	const limit = 64 << 10
	for _, path := range passkeyFinishRoutes {
		t.Run(path, func(t *testing.T) {
			h, cleanup := newHarness(t)
			defer cleanup()
			s := signedInUser(t, h)

			// Exactly at the cap is read; it is refused later, for want of a
			// challenge, not for its size. `{"pad":""}` and the newline the
			// encoder adds are 11 bytes.
			res, body := s.send(t, http.MethodPost, path, map[string]any{"pad": strings.Repeat("a", limit-11)})
			require.NotEqual(t, http.StatusRequestEntityTooLarge, res.StatusCode, "body: %s", body)

			res, body = s.send(t, http.MethodPost, path, map[string]any{"pad": strings.Repeat("a", limit)})
			require.Equal(t, http.StatusRequestEntityTooLarge, res.StatusCode, "body: %s", body)
			require.Contains(t, string(body), "too_large")
		})
	}
}
