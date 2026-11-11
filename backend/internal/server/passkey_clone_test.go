package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// #616(b): a passkey sign-in whose counter did not advance is refused, ends the
// account's sessions, and is audited.
//
// The harness relying party is testPasskeys' (https://help.test.local).
func cloneHarness(t *testing.T, storedCount int64) (*harness, *session, *testutil.SoftKey) {
	t.Helper()
	h, cleanup := newHarness(t)
	t.Cleanup(cleanup)
	mfaOptionalForStaff(t, h)

	key := testutil.NewSoftKey(t, "help.test.local", "https://help.test.local")
	cred := passkeyFor(h.staffID, "unused")
	cred.CredentialID = key.CredentialID
	cred.PublicKey = key.PublicKey(t)
	cred.SignCount = storedCount
	require.NoError(t, h.passkeyStore.Create(context.Background(), cred))

	return h, staffPasswordLogin(t, h), key
}

// signInWithKey runs start and finish with the key reporting counter.
func signInWithKey(t *testing.T, h *harness, s *session, key *testutil.SoftKey, counter uint32) (*http.Response, []byte) {
	t.Helper()
	res, body := s.send(t, http.MethodPost, "/api/v1/auth/local/passkey/start", nil)
	require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", body)
	var opts struct {
		PublicKey struct {
			Challenge string `json:"challenge"`
		} `json:"publicKey"`
	}
	require.NoError(t, json.Unmarshal(body, &opts))

	handle := [16]byte(h.staffID)
	return s.send(t, http.MethodPost, "/api/v1/auth/local/passkey/finish",
		json.RawMessage(key.Assert(t, opts.PublicKey.Challenge, handle[:], counter)))
}

// keepLastCookies does what a browser does with two Set-Cookie headers for one
// name: the last wins. The passkey finish route writes the session twice (once
// to take the challenge, once to sign in) and the harness jar keeps both.
func keepLastCookies(s *session) {
	last := map[string]*http.Cookie{}
	for _, c := range s.jar {
		last[c.Name] = c
	}
	s.jar = s.jar[:0]
	for _, c := range last {
		s.jar = append(s.jar, c)
	}
}

func sessionCount(t *testing.T, h *harness) int {
	t.Helper()
	var n int
	require.NoError(t, h.tx.QueryRow(`SELECT count(*) FROM sessions WHERE user_id = $1`, h.staffID).Scan(&n))
	return n
}

func TestPasskeySignIn_CounterThatDidNotAdvanceIsRefusedAndAudited(t *testing.T) {
	h, s, key := cloneHarness(t, 5)
	other := staffPasswordLogin(t, h) // a second session, which must go too
	require.Equal(t, 2, sessionCount(t, h), "precondition: two sessions")

	res, body := signInWithKey(t, h, s, key, 5)
	require.Equal(t, http.StatusUnauthorized, res.StatusCode, "body: %s", body)
	require.Contains(t, string(body), "passkey_suspect")

	require.Zero(t, sessionCount(t, h), "a refused clone left a session alive")
	res, _ = other.send(t, http.MethodGet, "/api/v1/me", nil)
	require.Equal(t, http.StatusUnauthorized, res.StatusCode)

	entries, err := h.auditStore.ListByEntity(context.Background(), "user", h.staffID, 10, 0)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, "passkey_clone_suspected", entries[0].Action)
	creds, err := h.passkeyStore.ListByUser(context.Background(), h.staffID)
	require.NoError(t, err)
	require.Equal(t, creds[0].ID.String(), entries[0].After["passkey_id"], "the entry does not say which key")
	require.EqualValues(t, 5, creds[0].SignCount, "the refused assertion moved the stored counter")
}

// The same key, honestly used, is not touched by any of this.
func TestPasskeySignIn_AdvancingCounterStillSignsIn(t *testing.T) {
	h, s, key := cloneHarness(t, 5)

	res, body := signInWithKey(t, h, s, key, 6)
	require.Equal(t, http.StatusNoContent, res.StatusCode, "body: %s", body)
	keepLastCookies(s)
	res, body = s.send(t, http.MethodGet, "/api/v1/me", nil)
	require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", body)

	entries, err := h.auditStore.ListByEntity(context.Background(), "user", h.staffID, 10, 0)
	require.NoError(t, err)
	require.Empty(t, entries)
}

// Most authenticators report zero forever, and refusing them is refusing
// honest sign-ins.
func TestPasskeySignIn_ZeroCounterForeverStillSignsIn(t *testing.T) {
	h, s, key := cloneHarness(t, 0)

	res, body := signInWithKey(t, h, s, key, 0)
	require.Equal(t, http.StatusNoContent, res.StatusCode, "body: %s", body)
}
