package server_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

// #496 refuses a passkey-only account while passkeys are unavailable, and #497
// is the way back: unlock clears every factor, so the account then signs in
// with the new password, must change it, and is an ordinary account again.
//
// The two userStore calls are exactly what the console command runs in its
// transaction (ClearFactors, then Unlock); the command itself is in package
// main and is tested there.
func TestPasskeysUnavailable_AfterUnlockThePasskeyOnlyAdminSignsInAndMustChangeThePassword(t *testing.T) {
	h, cleanup := newHarnessPasskeysOff(t)
	defer cleanup()
	ctx := context.Background()
	requireSoleAdministrator(t, h)
	enforceMFA(t, h)
	require.NoError(t, h.passkeyStore.Create(ctx, passkeyFor(h.adminID, "admin-key")))

	s, res, out := signIn(t, h, "admin@test.local")
	requireRefusedPasskeyUnavailable(t, s, res, out)

	const generated = "Generated-By-Unlock-1"
	hash, err := bcrypt.GenerateFromPassword([]byte(generated), bcrypt.MinCost)
	require.NoError(t, err)
	_, err = h.userStore.ClearFactors(ctx, h.adminID)
	require.NoError(t, err)
	require.NoError(t, h.userStore.Unlock(ctx, h.adminID, string(hash)))

	in := &session{h: h}
	res, body := in.send(t, http.MethodPost, "/api/v1/auth/local/login",
		map[string]any{"email": "admin@test.local", "password": generated})
	require.Equal(t, http.StatusOK, res.StatusCode, "the unlocked account must be able to sign in: %s", body)
	require.Contains(t, string(body), `"password_change_required":true`)

	res, _ = in.send(t, http.MethodGet, "/api/v1/me", nil)
	require.Equal(t, http.StatusForbidden, res.StatusCode)

	res, body = in.send(t, http.MethodPatch, "/api/v1/me/password",
		map[string]any{"current_password": generated, "new_password": "my-own-new-password"})
	require.Equal(t, http.StatusNoContent, res.StatusCode, "%s", body)

	res, body = in.send(t, http.MethodGet, "/api/v1/me", nil)
	require.Equal(t, http.StatusOK, res.StatusCode, "%s", body)
}
