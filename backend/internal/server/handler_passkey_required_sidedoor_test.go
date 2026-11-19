package server_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// #616 part c, review blockers. A role that must use a passkey and whose
// account already holds one is owed THAT key: a factor proved some other way
// (a TOTP code, an IdP's MFA assertion) must not be a way to register a
// replacement, or to delete the owner's keys so the session becomes the
// "TOTP then register" case.

func keyCount(t *testing.T, h *harness) int64 {
	t.Helper()
	n, err := h.passkeyStore.CountForUser(context.Background(), h.staffID)
	require.NoError(t, err)
	return n
}

func TestPasskeyRequired_HeldKey_TOTPDoesNotOpenKeyRegistrationOrDeletion(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	enrollMFA(t, ctx, h.userSvc, h.staffID)
	key := addKey(t, h, h.staffID)
	requirePasskeysFor(t, h, "staff")

	s, got := loginAs(t, h, "staff@test.local")
	require.Equal(t, loginAnswer{PasskeyNeeded: true}, got)

	// The code is accepted (it is a proved factor)...
	res, body := s.send(t, http.MethodPost, "/api/v1/auth/local/mfa/verify",
		map[string]any{"code": totpCodeFor(t, h, h.staffID)})
	require.Equal(t, http.StatusNoContent, res.StatusCode, "body: %s", body)
	requireMFARefused(t, s)

	// ...and does not let the session add a key of its own,
	res, body = s.send(t, http.MethodPost, "/api/v1/me/passkeys/register/start", nil)
	require.Equal(t, http.StatusForbidden, res.StatusCode, "body: %s", body)
	require.Contains(t, string(body), "passkey_required")
	res, body = s.send(t, http.MethodPost, "/api/v1/me/passkeys/register/finish", map[string]any{})
	require.Equal(t, http.StatusForbidden, res.StatusCode, "body: %s", body)

	// or strip the owner's.
	creds, err := h.passkeyStore.ListByUser(ctx, h.staffID)
	require.NoError(t, err)
	require.Len(t, creds, 1)
	res, body = s.send(t, http.MethodDelete, "/api/v1/me/passkeys/"+creds[0].ID.String(), nil)
	require.Equal(t, http.StatusForbidden, res.StatusCode, "body: %s", body)
	require.EqualValues(t, 1, keyCount(t, h))
	requireMFARefused(t, s)

	// Using the key it already has finishes the sign-in.
	res, body = signInWithKey(t, h, s, key, 1)
	require.Equal(t, http.StatusNoContent, res.StatusCode, "body: %s", body)
	keepLastCookies(s)
	requireSignedIn(t, s)
	_, sd, ok := storedSession(t, s)
	require.True(t, ok)
	require.False(t, sd.PasskeyHeld, "cleared with the debt")
}

// The session that is owed a key it does not have is unaffected: that is the
// onboarding path.
func TestPasskeyRequired_NoKey_TOTPStillOpensRegistration(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	enrollMFA(t, context.Background(), h.userSvc, h.staffID)
	requirePasskeysFor(t, h, "staff")

	s, _ := loginAs(t, h, "staff@test.local")
	_, sd, ok := storedSession(t, s)
	require.True(t, ok)
	require.False(t, sd.PasskeyHeld)
	res, body := s.send(t, http.MethodPost, "/api/v1/auth/local/mfa/verify",
		map[string]any{"code": totpCodeFor(t, h, h.staffID)})
	require.Equal(t, http.StatusNoContent, res.StatusCode, "body: %s", body)
	res, body = registerKey(t, s)
	require.Equal(t, http.StatusNoContent, res.StatusCode, "body: %s", body)
}

// An IdP that asserted MFA but not a passkey sets FactorVerified at once; a
// listed user who already has a key must still use it.
func TestOIDCCallback_PasskeyRequired_HeldKey_MFAAssertionDoesNotOpenKeyRegistration(t *testing.T) {
	oh, cleanup := newOIDCHarness(t)
	defer cleanup()
	ctx := context.Background()
	require.NoError(t, oh.adminSvc.SetBool(ctx, "mfa_enabled", true))
	claims := fakeOIDCClaims{Subject: "held-sub", Email: "held@test.local", EmailVerified: true, Name: "Held"}
	require.Equal(t, "/", oh.login(t, claims).Header.Get("Location"))
	u, ok := findUser(t, oh.harness, claims.Email)
	require.True(t, ok)
	addKey(t, oh.harness, u.ID)
	requirePasskeysFor(t, oh.harness, "user")

	claims.AMR = []string{"pwd", "mfa"}
	resp := oh.login(t, claims)
	require.Equal(t, "/login?sso_step=passkey", resp.Header.Get("Location"))
	s := &session{h: oh.harness, jar: resp.Cookies()}
	sd := sessionFrom(t, oh.harness, s.jar)
	require.True(t, sd.FactorVerified)
	require.True(t, sd.PasskeyHeld)

	res, body := s.send(t, http.MethodPost, "/api/v1/me/passkeys/register/start", nil)
	require.Equal(t, http.StatusForbidden, res.StatusCode, "body: %s", body)
	creds, err := oh.passkeyStore.ListByUser(ctx, u.ID)
	require.NoError(t, err)
	require.Len(t, creds, 1)
	res, body = s.send(t, http.MethodDelete, "/api/v1/me/passkeys/"+creds[0].ID.String(), nil)
	require.Equal(t, http.StatusForbidden, res.StatusCode, "body: %s", body)
	n, err := oh.passkeyStore.CountForUser(ctx, u.ID)
	require.NoError(t, err)
	require.EqualValues(t, 1, n)
	require.Equal(t, http.StatusForbidden, meStatus(t, oh.harness, s.jar))
}

func TestSAMLComplete_PasskeyRequired_HeldKey_MFAAssertionDoesNotOpenKeyRegistration(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	idp := samlIdPFor(t, h)
	requirePasskeysFor(t, h, "user")

	// First sign-in provisions the account.
	first := samlComplete(t, h, idp, "sso@test.local", nil)
	require.Equal(t, "/login?sso_step=passkey_enroll", first.Header.Get("Location"))
	u, ok := findUser(t, h, "sso@test.local")
	require.True(t, ok)
	addKey(t, h, u.ID)

	res := samlComplete(t, h, idp, "sso@test.local", samlAttrs(ssoMultiple))
	require.Equal(t, "/login?sso_step=passkey", res.Header.Get("Location"))
	s := &session{h: h, jar: appCookies(res)}
	require.True(t, sessionFrom(t, h, s.jar).FactorVerified)

	reg, body := s.send(t, http.MethodPost, "/api/v1/me/passkeys/register/start", nil)
	require.Equal(t, http.StatusForbidden, reg.StatusCode, "body: %s", body)
	requireMFARefused(t, s)
}

// A listed account that must change its password, holds TOTP and has no key:
// the password change and the key registration each wait on the other unless
// the verified-TOTP session may change the password first.
func TestPasskeyRequired_MustChangePassword_TOTPHolderCanFinishSignIn(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	enrollMFA(t, ctx, h.userSvc, h.staffID)
	requirePasskeysFor(t, h, "staff")
	_, err := h.tx.ExecContext(ctx, `UPDATE users SET must_change_password = true WHERE id = $1`, h.staffID)
	require.NoError(t, err)

	s, got := loginAs(t, h, "staff@test.local")
	require.Equal(t, loginAnswer{MFANeeded: true, PasskeyEnrollmentNeeded: true, PasswordChangeRequired: true}, got)

	// Before the code, the password change is refused: a password alone must
	// not replace the password of an account that has a factor (#498).
	change := map[string]any{"current_password": "password", "new_password": "a-brand-new-password"}
	res, body := s.send(t, http.MethodPatch, "/api/v1/me/password", change)
	require.Equal(t, http.StatusForbidden, res.StatusCode, "body: %s", body)

	res, body = s.send(t, http.MethodPost, "/api/v1/auth/local/mfa/verify",
		map[string]any{"code": totpCodeFor(t, h, h.staffID)})
	require.Equal(t, http.StatusNoContent, res.StatusCode, "body: %s", body)

	res, body = s.send(t, http.MethodPatch, "/api/v1/me/password", change)
	require.Equal(t, http.StatusNoContent, res.StatusCode, "body: %s", body)
	_, sd, ok := storedSession(t, s)
	require.True(t, ok)
	require.True(t, sd.PasskeyOwed)
	require.True(t, sd.FactorVerified)
	require.False(t, sd.MFAPassed)
	requireMFARefused(t, s)

	res, body = registerKey(t, s)
	keepLastCookies(s)
	require.Equal(t, http.StatusNoContent, res.StatusCode, "body: %s", body)
	requireSignedIn(t, s)
}

// The same account with a key already registered owes that key: a TOTP code
// does not admit the password change, which waits for the key.
func TestPasskeyRequired_MustChangePassword_HeldKeyIsNotBypassedByTOTP(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	enrollMFA(t, ctx, h.userSvc, h.staffID)
	addKey(t, h, h.staffID)
	requirePasskeysFor(t, h, "staff")
	_, err := h.tx.ExecContext(ctx, `UPDATE users SET must_change_password = true WHERE id = $1`, h.staffID)
	require.NoError(t, err)

	s, got := loginAs(t, h, "staff@test.local")
	require.True(t, got.PasskeyNeeded)
	res, body := s.send(t, http.MethodPost, "/api/v1/auth/local/mfa/verify",
		map[string]any{"code": totpCodeFor(t, h, h.staffID)})
	require.Equal(t, http.StatusNoContent, res.StatusCode, "body: %s", body)
	res, body = s.send(t, http.MethodPatch, "/api/v1/me/password",
		map[string]any{"current_password": "password", "new_password": "a-brand-new-password"})
	require.Equal(t, http.StatusForbidden, res.StatusCode, "body: %s", body)
}
