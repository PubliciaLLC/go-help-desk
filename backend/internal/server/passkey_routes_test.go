package server_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// Where the passkey routes sit is as load-bearing as what they do.
//
// Registration inherits both of TOTP enrolment's placements, and each one
// prevents a different disaster: refused to machine credentials, so a leaked
// API key cannot add a way of BECOMING its owner; and outside RequireMFA, so
// somebody compelled to enrol can finish — which is what lets a sole
// administrator with no working factor recover alone.
func TestPasskeyRoutes_SitWhereTheyMust(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	// An API key may not touch how its owner authenticates. Without this a
	// leaked key issued for a cron job is account takeover: register a
	// passkey, then sign in as them forever.
	t.Run("a machine credential cannot register a passkey", func(t *testing.T) {
		for _, path := range []string{
			"/api/v1/me/passkeys/register/start",
			"/api/v1/me/passkeys/register/finish",
		} {
			res := h.do(t, http.MethodPost, path, map[string]any{})
			b, _ := io.ReadAll(res.Body)
			res.Body.Close()
			require.Equal(t, http.StatusForbidden, res.StatusCode,
				"%s accepted an API key; a leaked key could add a way of becoming its owner. body: %s", path, b)
		}
	})

	t.Run("and cannot remove one", func(t *testing.T) {
		res := h.do(t, http.MethodDelete, "/api/v1/me/passkeys/"+uuid.NewString(), nil)
		defer res.Body.Close()
		require.Equal(t, http.StatusForbidden, res.StatusCode)
	})

	// The recovery path. A session that still owes a second factor must be
	// able to reach registration, or an administrator whose only key is lost
	// has no way back and setup does not reopen.
	t.Run("registration is reachable by a session that still owes a factor", func(t *testing.T) {
		ctx := context.Background()
		setup := loggedInAdmin(t, h)
		setRes, setBody := setup.send(t, http.MethodPatch, "/api/v1/admin/settings", map[string]any{
			"mfa_enabled": true, "mfa_enforced_roles": []string{"admin"},
		})
		require.Equal(t, http.StatusNoContent, setRes.StatusCode, "could not enforce MFA: %s", setBody)
		require.NoError(t, h.userSvc.ResetMFA(ctx, h.adminID, nil))
		require.True(t, h.adminSvc.MFARequiredFor(ctx, "admin"))

		s := &session{h: h}
		res, body := s.send(t, http.MethodPost, "/api/v1/auth/local/login",
			map[string]any{"email": "admin@test.local", "password": "password"})
		require.Equal(t, http.StatusOK, res.StatusCode, "the password should still work: %s", body)

		// Owes an enrolment, so protected routes refuse it...
		res, _ = s.send(t, http.MethodGet, "/api/v1/me", nil)
		require.Equal(t, http.StatusForbidden, res.StatusCode,
			"a session owing an enrolment reached a protected route")

		// ...and registration is still open, which is the whole point.
		res, body = s.send(t, http.MethodPost, "/api/v1/me/passkeys/register/start", nil)
		require.NotEqual(t, http.StatusForbidden, res.StatusCode,
			"the sole administrator cannot reach passkey registration, so nobody can recover this instance: %s", body)
		require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", body)
		require.Contains(t, string(body), "challenge",
			"registration answered without a challenge to sign: %s", body)
	})
}

// A credential belongs to one account. An id is not an authorisation, and the
// answer to "is this id real" must not differ between somebody else's
// credential and one that does not exist.
func TestDeletePasskey_IsScopedToItsOwner(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	owner, err := h.userSvc.Create(ctx, user.CreateUserInput{
		Email: "owner-" + uuid.NewString() + "@test.local", DisplayName: "Owner",
		Role: user.RoleStaff, Password: "a-real-passphrase-here",
	})
	require.NoError(t, err)

	// A real credential on the owner's account.
	require.NoError(t, h.passkeyStore.Create(ctx, passkeyFor(owner.ID, "theirs")))
	stored, err := h.passkeyStore.GetByCredentialID(ctx, []byte("theirs"))
	require.NoError(t, err)

	// A different signed-in person asks for it by id.
	s := &session{h: h}
	res, body := s.send(t, http.MethodPost, "/api/v1/auth/local/login",
		map[string]any{"email": "user@test.local", "password": "password"})
	require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", body)

	res, _ = s.send(t, http.MethodDelete, "/api/v1/me/passkeys/"+stored.ID.String(), nil)
	require.Equal(t, http.StatusNotFound, res.StatusCode,
		"one account removed another's passkey")

	// Unchanged, and indistinguishable from an id that never existed.
	_, err = h.passkeyStore.GetByCredentialID(ctx, []byte("theirs"))
	require.NoError(t, err, "the credential was deleted despite the refusal")

	res, _ = s.send(t, http.MethodDelete, "/api/v1/me/passkeys/"+uuid.NewString(), nil)
	require.Equal(t, http.StatusNotFound, res.StatusCode,
		"a real credential and an invented id answered differently, which confirms the real one exists")
}

// A password alone cannot add or remove a second factor on an account that
// already has one.
//
// These routes sit outside RequireMFA so somebody with NO factor can recover,
// which means a session holding only the password reaches them. Without a
// guard that is not a weakened control, it is a bypass of the control: an
// attacker who knows the password registers their own key and now holds a
// second factor. GenerateMFASecret has refused exactly this for TOTP since
// the re-enrolment fix; the passkey routes copied its placement and not its
// guard, and an automated review caught it.
func TestPasskeys_APasswordAloneCannotChangeAProtectedAccountsFactors(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	// An account protected by a passkey, and an attacker holding only the
	// password for it.
	require.NoError(t, h.passkeyStore.Create(ctx, passkeyFor(h.userID, "victims-key")))
	stored, err := h.passkeyStore.GetByCredentialID(ctx, []byte("victims-key"))
	require.NoError(t, err)

	setup := loggedInAdmin(t, h)
	setRes, setBody := setup.send(t, http.MethodPatch, "/api/v1/admin/settings", map[string]any{
		"mfa_enabled": true, "mfa_enforced_roles": []string{"user"},
	})
	require.Equal(t, http.StatusNoContent, setRes.StatusCode, "could not enforce MFA: %s", setBody)

	s := &session{h: h}
	res, body := s.send(t, http.MethodPost, "/api/v1/auth/local/login",
		map[string]any{"email": "user@test.local", "password": "password"})
	require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", body)

	t.Run("cannot register a second factor of their own", func(t *testing.T) {
		res, body := s.send(t, http.MethodPost, "/api/v1/me/passkeys/register/start", nil)
		require.Equal(t, http.StatusForbidden, res.StatusCode,
			"a password alone registered a passkey on a protected account, which IS the second factor: %s", body)
	})

	t.Run("cannot strip the existing one", func(t *testing.T) {
		res, body := s.send(t, http.MethodDelete, "/api/v1/me/passkeys/"+stored.ID.String(), nil)
		require.Equal(t, http.StatusForbidden, res.StatusCode,
			"a password alone removed the account's second factor: %s", body)

		_, err := h.passkeyStore.GetByCredentialID(ctx, []byte("victims-key"))
		require.NoError(t, err, "the credential was deleted despite the refusal")
	})

	// And the recovery path is untouched: an account with NO factor at all
	// still reaches registration, which is why these routes are outside
	// RequireMFA in the first place. The two pull in opposite directions and
	// both have to hold.
	t.Run("but an account with no factor at all still recovers", func(t *testing.T) {
		require.NoError(t, h.passkeyStore.Delete(ctx, stored.ID, h.userID))
		require.NoError(t, h.userSvc.ResetMFA(ctx, h.userID, nil))

		fresh := &session{h: h}
		res, body := fresh.send(t, http.MethodPost, "/api/v1/auth/local/login",
			map[string]any{"email": "user@test.local", "password": "password"})
		require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", body)

		res, body = fresh.send(t, http.MethodPost, "/api/v1/me/passkeys/register/start", nil)
		require.Equal(t, http.StatusOK, res.StatusCode,
			"an account with nothing enrolled could not reach registration, so it cannot recover: %s", body)
	})
}

// The other door. An account protected by a passkey and no TOTP must not let
// a password-only session enrol TOTP instead.
//
// GenerateMFASecret's own guard reads u.MFAEnabled — the TOTP column — which
// was a complete answer to "does this account have a second factor" while
// TOTP was the only kind. Passkeys made it partial, so closing the passkey
// route alone left the account exactly as reachable through this one.
func TestMFAEnrol_IsRefusedToAPasswordOnlySessionOnAPasskeyProtectedAccount(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	setup := loggedInAdmin(t, h)
	res, body := setup.send(t, http.MethodPatch, "/api/v1/admin/settings", map[string]any{
		"mfa_enabled": true, "mfa_enforced_roles": []string{"user"},
	})
	require.Equal(t, http.StatusNoContent, res.StatusCode, "%s", body)

	// Protected by a passkey only: no TOTP, so MFAEnabled is false.
	require.NoError(t, h.passkeyStore.Create(ctx, passkeyFor(h.userID, "the-only-factor")))
	stored, err := h.userSvc.GetByIDAdmin(ctx, h.userID)
	require.NoError(t, err)
	require.False(t, stored.MFAEnabled, "precondition: the TOTP column says no factor")

	s := &session{h: h}
	res, body = s.send(t, http.MethodPost, "/api/v1/auth/local/login",
		map[string]any{"email": "user@test.local", "password": "password"})
	require.Equal(t, http.StatusOK, res.StatusCode, "%s", body)

	res, body = s.send(t, http.MethodPost, "/api/v1/me/mfa/enroll", nil)
	require.Equal(t, http.StatusForbidden, res.StatusCode,
		"a password alone enrolled TOTP on a passkey-protected account, which hands it the second factor: %s", body)
}

// An account whose only factor is a passkey must be asked for the passkey.
//
// Session B found this in review before any of it was built. The two booleans
// that carried the gate both keyed off u.MFAEnabled — the TOTP column, not
// "this account has a second factor". An account with a passkey and no TOTP
// reads false, so the server answered "you still need to enrol" on every
// sign-in and never offered the key. Combined with the guard that refuses
// enrolment on an account that already has a factor, that account could never
// sign in again: told to enrol, and refused when it tried.
func TestLogin_AsksForThePasskeyWhenThatIsTheOnlyFactor(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	setup := loggedInAdmin(t, h)
	res, body := setup.send(t, http.MethodPatch, "/api/v1/admin/settings", map[string]any{
		"mfa_enabled": true, "mfa_enforced_roles": []string{"user"},
	})
	require.Equal(t, http.StatusNoContent, res.StatusCode, "%s", body)

	answer := func(t *testing.T) map[string]any {
		t.Helper()
		s := &session{h: h}
		res, body := s.send(t, http.MethodPost, "/api/v1/auth/local/login",
			map[string]any{"email": "user@test.local", "password": "password"})
		require.Equal(t, http.StatusOK, res.StatusCode, "%s", body)
		var out map[string]any
		require.NoError(t, json.Unmarshal(body, &out))
		return out
	}

	t.Run("no factor at all: asked to enrol", func(t *testing.T) {
		got := answer(t)
		require.Equal(t, true, got["mfa_enrollment_needed"])
		require.Equal(t, false, got["passkey_needed"])
		require.Equal(t, false, got["mfa_needed"])
	})

	t.Run("a passkey and no TOTP: asked for the passkey", func(t *testing.T) {
		require.NoError(t, h.passkeyStore.Create(ctx, passkeyFor(h.userID, "only-factor")))
		got := answer(t)
		require.Equal(t, true, got["passkey_needed"],
			"the server did not ask for the passkey")
		require.Equal(t, false, got["mfa_enrollment_needed"],
			"the account was told to enrol despite already having a factor — and enrolment would refuse it, "+
				"so it could never sign in again")
		require.Equal(t, false, got["mfa_needed"])
	})

	// Both enrolled: TOTP is asked for, because it is what the account had
	// first and registering a key is not a request to stop using it.
	t.Run("both: asked for TOTP", func(t *testing.T) {
		// Enrol TOTP alongside the passkey, through the store, since the
		// service's own path stages a secret in a session.
		require.NoError(t, h.userStore.SetMFA(ctx, h.userID, "ATOTPSECRET", true))
		got := answer(t)
		require.Equal(t, true, got["mfa_needed"])
		require.Equal(t, false, got["passkey_needed"])
	})
}
