package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// #616 part c: roles that must finish every sign-in with a passkey. A TOTP code
// proves a factor for such a role but does not pass MFA; the policy is applied
// when a session is created; and it is suspended while passkeys are unavailable
// (#494).

// requirePasskeysFor turns MFA on and lists roles in passkey_required_roles. It
// writes nothing to mfa_enforced_roles: the implication is computed when read.
func requirePasskeysFor(t *testing.T, h *harness, roles ...string) {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeyMFAEnabled, true))
	raw, err := json.Marshal(roles)
	require.NoError(t, err)
	require.NoError(t, h.adminSvc.SetRaw(ctx, admin.KeyPasskeyRequiredRoles, raw))
}

// addKey registers a software key on the account, as a finished sign-in would
// have left it.
func addKey(t *testing.T, h *harness, userID uuid.UUID) *testutil.SoftKey {
	t.Helper()
	key := testutil.NewSoftKey(t, "help.test.local", "https://help.test.local")
	cred := passkeyFor(userID, "unused")
	cred.CredentialID = key.CredentialID
	cred.PublicKey = key.PublicKey(t)
	require.NoError(t, h.passkeyStore.Create(context.Background(), cred))
	return key
}

type loginAnswer struct {
	MFANeeded               bool `json:"mfa_needed"`
	PasskeyNeeded           bool `json:"passkey_needed"`
	MFAEnrollmentNeeded     bool `json:"mfa_enrollment_needed"`
	PasskeyEnrollmentNeeded bool `json:"passkey_enrollment_needed"`
	PasswordChangeRequired  bool `json:"password_change_required"`
}

func loginAs(t *testing.T, h *harness, email string) (*session, loginAnswer) {
	t.Helper()
	s := &session{h: h}
	res, body := s.send(t, http.MethodPost, "/api/v1/auth/local/login",
		map[string]any{"email": email, "password": "password"})
	require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", body)
	var out loginAnswer
	require.NoError(t, json.Unmarshal(body, &out))
	return s, out
}

func requireMFARefused(t *testing.T, s *session) {
	t.Helper()
	res, body := s.send(t, http.MethodGet, "/api/v1/me", nil)
	require.Equal(t, http.StatusForbidden, res.StatusCode, "body: %s", body)
	require.Contains(t, string(body), "mfa_required")
}

func requireSignedIn(t *testing.T, s *session) {
	t.Helper()
	res, body := s.send(t, http.MethodGet, "/api/v1/me", nil)
	require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", body)
}

func totpCodeFor(t *testing.T, h *harness, id uuid.UUID) string {
	t.Helper()
	u, err := h.userSvc.GetByID(context.Background(), id)
	require.NoError(t, err)
	code, err := totp.GenerateCode(u.MFASecret, time.Now())
	require.NoError(t, err)
	return code
}

func TestLogin_PasskeyRequiredRole_TOTPOnlyVerifiesThenRegistersAKey(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	enrollMFA(t, ctx, h.userSvc, h.staffID)
	requirePasskeysFor(t, h, "staff")

	s, got := loginAs(t, h, "staff@test.local")
	require.Equal(t, loginAnswer{MFANeeded: true, PasskeyEnrollmentNeeded: true}, got)
	requireMFARefused(t, s)

	_, before, ok := storedSession(t, s)
	require.True(t, ok)
	require.True(t, before.PasskeyOwed)
	require.False(t, before.PendingUntil.IsZero())

	// A TOTP code proves a factor, and does not pass MFA.
	res, body := s.send(t, http.MethodPost, "/api/v1/auth/local/mfa/verify",
		map[string]any{"code": totpCodeFor(t, h, h.staffID)})
	require.Equal(t, http.StatusNoContent, res.StatusCode, "body: %s", body)
	requireMFARefused(t, s)

	_, after, ok := storedSession(t, s)
	require.True(t, ok)
	require.True(t, after.PasskeyOwed)
	require.True(t, after.FactorVerified, "the proved factor is what lets a key be registered")
	require.False(t, after.MFAPassed)
	require.True(t, before.PendingUntil.Equal(after.PendingUntil), "verifying must not extend the deadline (#452)")

	// Registering the key passes MFA and clears the debt.
	res, body = registerKey(t, s)
	keepLastCookies(s)
	require.Equal(t, http.StatusNoContent, res.StatusCode, "body: %s", body)
	requireSignedIn(t, s)
	_, done, ok := storedSession(t, s)
	require.True(t, ok)
	require.False(t, done.PasskeyOwed)

	// And a later TOTP step-up must not take MFAPassed away again.
	res, body = s.send(t, http.MethodPost, "/api/v1/auth/local/mfa/verify",
		map[string]any{"code": totpCodeFor(t, h, h.staffID)})
	require.Equal(t, http.StatusNoContent, res.StatusCode, "body: %s", body)
	requireSignedIn(t, s)
}

func TestLogin_PasskeyRequiredRole_PasskeyAndTOTPOwesThePasskeyOnly(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	enrollMFA(t, ctx, h.userSvc, h.staffID)
	key := addKey(t, h, h.staffID)
	requirePasskeysFor(t, h, "staff")

	s, got := loginAs(t, h, "staff@test.local")
	require.Equal(t, loginAnswer{PasskeyNeeded: true}, got)
	requireMFARefused(t, s)

	res, body := signInWithKey(t, h, s, key, 1)
	require.Equal(t, http.StatusNoContent, res.StatusCode, "body: %s", body)
	keepLastCookies(s)
	requireSignedIn(t, s)
	_, sd, ok := storedSession(t, s)
	require.True(t, ok)
	require.False(t, sd.PasskeyOwed, "a passkey sign-in clears the debt")
}

func TestLogin_PasskeyRequiredRole_NoFactorRegistersAKeyAndTOTPDoesNotPass(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	requirePasskeysFor(t, h, "staff")

	s, got := loginAs(t, h, "staff@test.local")
	require.Equal(t, loginAnswer{PasskeyEnrollmentNeeded: true}, got,
		"a listed role is enforced by implication, but enrols a key and never TOTP")
	requireMFARefused(t, s)

	// TOTP enrolment still works as a way to prove a factor, but does not pass.
	enrolTOTP(t, s)
	requireMFARefused(t, s)

	res, body := registerKey(t, s)
	keepLastCookies(s)
	require.Equal(t, http.StatusNoContent, res.StatusCode, "body: %s", body)
	requireSignedIn(t, s)
}

// mfa_enforced_roles is not written on the policy's behalf.
func TestPasskeyRequired_DoesNotWriteTheEnforcedList(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	requirePasskeysFor(t, h, "staff")
	require.Empty(t, h.adminSvc.MFAEnforcedRoles(context.Background()))
	require.True(t, h.adminSvc.MFARequiredFor(context.Background(), "staff"))
}

// The existing four outcomes, with the list set for another role.
func TestLogin_RoleNotInPasskeyList_Unchanged(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, h *harness)
		want  loginAnswer
	}{
		{"totp", func(t *testing.T, h *harness) { enrollMFA(t, context.Background(), h.userSvc, h.staffID) },
			loginAnswer{MFANeeded: true}},
		{"passkey only", func(t *testing.T, h *harness) { addKey(t, h, h.staffID) },
			loginAnswer{PasskeyNeeded: true}},
		{"enforced, no factor", func(t *testing.T, h *harness) {
			require.NoError(t, h.adminSvc.SetRaw(context.Background(), admin.KeyMFAEnforcedRoles, []byte(`["staff"]`)))
		}, loginAnswer{MFAEnrollmentNeeded: true}},
		{"nothing owed", func(t *testing.T, h *harness) {}, loginAnswer{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, cleanup := newHarness(t)
			defer cleanup()
			tc.setup(t, h)
			requirePasskeysFor(t, h, "admin")

			_, got := loginAs(t, h, "staff@test.local")
			require.Equal(t, tc.want, got)
		})
	}
}

// While passkeys are unavailable (#494) the policy is suspended: TOTP passes.
func TestLogin_PasskeyRequired_SuspendedWhilePasskeysUnavailable(t *testing.T) {
	t.Run("a TOTP holder passes on TOTP", func(t *testing.T) {
		h, cleanup := newHarnessPasskeysOff(t)
		defer cleanup()
		enrollMFA(t, context.Background(), h.userSvc, h.staffID)
		requirePasskeysFor(t, h, "staff")

		s, got := loginAs(t, h, "staff@test.local")
		require.Equal(t, loginAnswer{MFANeeded: true}, got)
		res, body := s.send(t, http.MethodPost, "/api/v1/auth/local/mfa/verify",
			map[string]any{"code": totpCodeFor(t, h, h.staffID)})
		require.Equal(t, http.StatusNoContent, res.StatusCode, "body: %s", body)
		requireSignedIn(t, s)
	})
	t.Run("no factor is still enforced, with TOTP", func(t *testing.T) {
		h, cleanup := newHarnessPasskeysOff(t)
		defer cleanup()
		requirePasskeysFor(t, h, "staff")

		_, got := loginAs(t, h, "staff@test.local")
		require.Equal(t, loginAnswer{MFAEnrollmentNeeded: true}, got)
	})
	t.Run("a passkey-only account is still refused", func(t *testing.T) {
		h, cleanup := newHarnessPasskeysOff(t)
		defer cleanup()
		addKey(t, h, h.staffID)
		requirePasskeysFor(t, h, "staff")

		s, res, out := signIn(t, h, "staff@test.local")
		requireRefusedPasskeyUnavailable(t, s, res, out)
	})
}

// A policy change applies at the next sign-in. It revokes nobody (ruling 5).
func TestPolicyChange_DoesNotRevokeSessions(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	enrollMFA(t, ctx, h.userSvc, h.staffID)
	require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeyMFAEnabled, true))

	s, got := loginAs(t, h, "staff@test.local")
	require.True(t, got.MFANeeded)
	res, body := s.send(t, http.MethodPost, "/api/v1/auth/local/mfa/verify",
		map[string]any{"code": totpCodeFor(t, h, h.staffID)})
	require.Equal(t, http.StatusNoContent, res.StatusCode, "body: %s", body)
	requireSignedIn(t, s)

	requirePasskeysFor(t, h, "staff")
	requireSignedIn(t, s)

	// The Account-page step-up on this fully signed-in session keeps it so.
	res, body = s.send(t, http.MethodPost, "/api/v1/auth/local/mfa/verify",
		map[string]any{"code": totpCodeFor(t, h, h.staffID)})
	require.Equal(t, http.StatusNoContent, res.StatusCode, "body: %s", body)
	requireSignedIn(t, s)
	_, sd, ok := storedSession(t, s)
	require.True(t, ok)
	require.False(t, sd.PasskeyOwed)
	require.True(t, sd.MFAPassed)
}

// Without carrying the debt through a password change, a must-change-password
// session in a listed role could enrol TOTP and walk through the gate.
func TestChangePassword_CarriesPasskeyOwed(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	requirePasskeysFor(t, h, "staff")
	_, err := h.tx.ExecContext(ctx, `UPDATE users SET must_change_password = true WHERE id = $1`, h.staffID)
	require.NoError(t, err)

	s, got := loginAs(t, h, "staff@test.local")
	require.Equal(t, loginAnswer{PasskeyEnrollmentNeeded: true, PasswordChangeRequired: true}, got)

	res, body := s.send(t, http.MethodPatch, "/api/v1/me/password",
		map[string]any{"current_password": "password", "new_password": "a-brand-new-password"})
	require.Equal(t, http.StatusNoContent, res.StatusCode, "body: %s", body)
	_, sd, ok := storedSession(t, s)
	require.True(t, ok)
	require.True(t, sd.PasskeyOwed)
	require.False(t, sd.PendingUntil.IsZero())

	enrolTOTP(t, s)
	requireMFARefused(t, s)

	res, body = registerKey(t, s)
	keepLastCookies(s)
	require.Equal(t, http.StatusNoContent, res.StatusCode, "body: %s", body)
	requireSignedIn(t, s)
}

// The sole administrator can recover: sign in, register a key, in.
func TestSoleAdministrator_PasskeyRequiredWithNoFactorCanSelfRecover(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	requireSoleAdministrator(t, h)
	requirePasskeysFor(t, h, "admin")

	s, got := loginAs(t, h, "admin@test.local")
	require.Equal(t, loginAnswer{PasskeyEnrollmentNeeded: true}, got)
	res, body := registerKey(t, s)
	keepLastCookies(s)
	require.Equal(t, http.StatusNoContent, res.StatusCode, "body: %s", body)
	res, body = s.send(t, http.MethodGet, "/api/v1/admin/settings", nil)
	require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", body)
}

func TestVerifyEmail_PasskeyRequiredUserRole(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeySelfSignupEnabled, true))
	require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeyOpenRegistrationEnabled, true))
	requirePasskeysFor(t, h, "user")

	res := h.doUnauth(t, http.MethodPost, "/api/v1/auth/signup",
		map[string]any{"email": "dora@signup.test", "display_name": "Dora"})
	res.Body.Close()

	s := &session{h: h}
	res, body := s.send(t, http.MethodPost, "/api/v1/auth/verify-email",
		map[string]any{"token": h.verifyMail.latest(t), "display_name": "Dora", "password": "dora-chooses-this"})
	require.Equal(t, http.StatusOK, res.StatusCode, string(body))
	var verified struct {
		MFAEnrollmentNeeded     bool `json:"mfa_enrollment_needed"`
		PasskeyEnrollmentNeeded bool `json:"passkey_enrollment_needed"`
	}
	require.NoError(t, json.Unmarshal(body, &verified))
	require.True(t, verified.PasskeyEnrollmentNeeded)
	require.False(t, verified.MFAEnrollmentNeeded)
	requireMFARefused(t, s)

	enrolTOTP(t, s)
	requireMFARefused(t, s)
	res, body = registerKey(t, s)
	keepLastCookies(s)
	require.Equal(t, http.StatusNoContent, res.StatusCode, "body: %s", body)
	requireSignedIn(t, s)
}

func TestPatchSettings_PasskeyRequiredRoles(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	admin := loggedInAdmin(t, h)

	for _, tc := range []struct {
		name string
		body any
		want int
	}{
		{"valid", []string{"staff", "user"}, http.StatusNoContent},
		{"empty", []string{}, http.StatusNoContent},
		{"unknown role", []string{"root"}, http.StatusBadRequest},
		{"not an array", "staff", http.StatusBadRequest},
		{"null", nil, http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res, body := admin.send(t, http.MethodPatch, "/api/v1/admin/settings",
				map[string]any{"passkey_required_roles": tc.body})
			require.Equal(t, tc.want, res.StatusCode, "body: %s", body)
		})
	}

	t.Run("a machine credential is refused", func(t *testing.T) {
		res := h.doAsAdmin(t, http.MethodPatch, "/api/v1/admin/settings",
			map[string]any{"passkey_required_roles": []string{"staff"}})
		res.Body.Close()
		require.Equal(t, http.StatusForbidden, res.StatusCode)
	})

	t.Run("is returned by GET and leaves the enforced list alone", func(t *testing.T) {
		res, body := admin.send(t, http.MethodPatch, "/api/v1/admin/settings",
			map[string]any{"passkey_required_roles": []string{"user"}})
		require.Equal(t, http.StatusNoContent, res.StatusCode, "body: %s", body)
		res, body = admin.send(t, http.MethodGet, "/api/v1/admin/settings", nil)
		require.Equal(t, http.StatusOK, res.StatusCode)
		var got map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(body, &got))
		require.JSONEq(t, `["user"]`, string(got["passkey_required_roles"]))
		require.JSONEq(t, `[]`, string(got["mfa_enforced_roles"]))
	})
}

// The self-lockout guard (#616c, owner ruling): a save that newly lists the
// saving administrator's own role is refused unless they hold a passkey, so an
// administrator does not require a passkey of their own role before they have
// shown they can hold one (they could be stuck at registration at next
// sign-in). Only a newly listed own role matters: any other edit is accepted.
func TestPatchSettings_PasskeyRequiredRoles_RefusesSelfLockout(t *testing.T) {
	cases := []struct {
		name       string
		stored     []string
		hasKey     bool
		hasTOTP    bool
		send       []string
		wantStatus int
		wantStored []string
	}{
		{"own role, no key", nil, false, false, []string{"admin"}, http.StatusBadRequest, nil},
		{"own role among others, no key", nil, false, false, []string{"staff", "admin"}, http.StatusBadRequest, nil},
		{"own role, TOTP but no key", nil, false, true, []string{"admin"}, http.StatusBadRequest, nil},
		{"own role, with a key", nil, true, false, []string{"admin"}, http.StatusNoContent, []string{"admin"}},
		{"own role, TOTP and a key", nil, true, true, []string{"admin"}, http.StatusNoContent, []string{"admin"}},
		{"list without own role", nil, false, false, []string{"staff", "user"}, http.StatusNoContent, []string{"staff", "user"}},
		{"unchanged list", []string{"admin", "staff"}, false, false, []string{"staff", "admin"}, http.StatusNoContent, []string{"staff", "admin"}},
		{"role removed", []string{"admin", "staff"}, false, false, []string{"staff"}, http.StatusNoContent, []string{"staff"}},
		{"list emptied", []string{"admin"}, false, false, []string{}, http.StatusNoContent, []string{}},
		{"other role removed while own role stays", []string{"admin", "staff"}, false, false, []string{"admin"}, http.StatusNoContent, []string{"admin"}},
		{"other role added while own role stays", []string{"admin"}, false, false, []string{"admin", "user"}, http.StatusNoContent, []string{"admin", "user"}},
		{"own role newly added beside a stored one", []string{"staff"}, false, false, []string{"staff", "admin"}, http.StatusBadRequest, []string{"staff"}},
		{"own role swapped in for another", []string{"staff"}, false, false, []string{"admin", "user"}, http.StatusBadRequest, []string{"staff"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, cleanup := newHarness(t)
			defer cleanup()
			ctx := context.Background()
			if tc.hasTOTP {
				enrollMFA(t, ctx, h.userSvc, h.adminID)
			}
			if tc.hasKey {
				addKey(t, h, h.adminID)
			}
			if tc.stored != nil {
				raw, err := json.Marshal(tc.stored)
				require.NoError(t, err)
				require.NoError(t, h.adminSvc.SetRaw(ctx, admin.KeyPasskeyRequiredRoles, raw))
			}
			s := loggedInAdmin(t, h)

			res, body := s.send(t, http.MethodPatch, "/api/v1/admin/settings",
				map[string]any{"passkey_required_roles": tc.send})
			require.Equal(t, tc.wantStatus, res.StatusCode, "body: %s", body)
			if tc.wantStatus == http.StatusBadRequest {
				require.Contains(t, string(body), `"bad_request"`)
				require.Contains(t, string(body), "invalid input: your role cannot be listed in passkey_required_roles because you have no passkey: register a passkey on your Account page first, then save again")
			}
			require.ElementsMatch(t, tc.wantStored, h.adminSvc.PasskeyRequiredRoles(ctx),
				"a refused save must leave the stored list as it was")
		})
	}
}

// Whether passkeys are available is the one thing the message may speak to:
// without them nobody can register a key, so BASE_URL is the thing to fix.
func TestPatchSettings_PasskeyRequiredRoles_RefusalNamesBaseURLOnlyWhenPasskeysAreOff(t *testing.T) {
	for _, tc := range []struct {
		name    string
		newH    func(*testing.T) (*harness, func())
		wantURL bool
	}{
		{"passkeys available", newHarness, false},
		{"passkeys unavailable", newHarnessPasskeysOff, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, cleanup := tc.newH(t)
			defer cleanup()
			s := loggedInAdmin(t, h)
			res, body := s.send(t, http.MethodPatch, "/api/v1/admin/settings",
				map[string]any{"passkey_required_roles": []string{"admin"}})
			require.Equal(t, http.StatusBadRequest, res.StatusCode, "body: %s", body)
			require.Equal(t, tc.wantURL, strings.Contains(string(body), "BASE_URL"), "body: %s", body)
		})
	}
}

type suspendedWarning struct {
	Passkeys struct {
		Available      bool      `json:"available"`
		SuspendedRoles *[]string `json:"suspended_roles"`
	} `json:"passkeys"`
}

func getSuspended(t *testing.T, h *harness) suspendedWarning {
	t.Helper()
	res := h.doAsAdmin(t, http.MethodGet, "/api/v1/admin/security-warnings", nil)
	var out suspendedWarning
	decodeJSON(t, res, &out)
	require.NotNil(t, out.Passkeys.SuspendedRoles, "suspended_roles must always be an array")
	return out
}

func TestSecurityWarnings_PasskeyPolicySuspended(t *testing.T) {
	t.Run("unavailable, mfa on, roles listed", func(t *testing.T) {
		h, cleanup := newHarnessPasskeysOff(t)
		defer cleanup()
		requirePasskeysFor(t, h, "staff", "admin")
		got := getSuspended(t, h)
		require.False(t, got.Passkeys.Available)
		require.Equal(t, []string{"staff", "admin"}, *got.Passkeys.SuspendedRoles)
	})
	t.Run("unavailable, mfa off", func(t *testing.T) {
		h, cleanup := newHarnessPasskeysOff(t)
		defer cleanup()
		requirePasskeysFor(t, h, "staff")
		require.NoError(t, h.adminSvc.SetBool(context.Background(), admin.KeyMFAEnabled, false))
		require.Empty(t, *getSuspended(t, h).Passkeys.SuspendedRoles)
	})
	t.Run("unavailable, nothing listed", func(t *testing.T) {
		h, cleanup := newHarnessPasskeysOff(t)
		defer cleanup()
		requirePasskeysFor(t, h)
		require.Empty(t, *getSuspended(t, h).Passkeys.SuspendedRoles)
	})
	t.Run("available", func(t *testing.T) {
		h, cleanup := newHarness(t)
		defer cleanup()
		requirePasskeysFor(t, h, "staff")
		got := getSuspended(t, h)
		require.True(t, got.Passkeys.Available)
		require.Empty(t, *got.Passkeys.SuspendedRoles)
	})
}

// The session a listed-role sign-in with a held key leaves is owed that key
// (PasskeyOwed, PasskeyHeld). Finishing the passkey login is what satisfies the
// debt: MFA passes and both facts are cleared, or the session would stay
// restricted (PasskeyHeld blocks key registration and deletion) after signing in.
func TestPasskeyLoginFinish_ClearsPasskeyOwed(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	key := addKey(t, h, h.staffID)
	requirePasskeysFor(t, h, "staff")

	s, got := loginAs(t, h, "staff@test.local")
	require.Equal(t, loginAnswer{PasskeyNeeded: true}, got)
	_, before, ok := storedSession(t, s)
	require.True(t, ok)
	require.False(t, before.MFAPassed, "precondition")
	require.True(t, before.PasskeyOwed, "precondition")
	require.True(t, before.PasskeyHeld, "precondition")

	res, body := signInWithKey(t, h, s, key, 1)
	require.Equal(t, http.StatusNoContent, res.StatusCode, "body: %s", body)
	keepLastCookies(s)

	_, after, ok := storedSession(t, s)
	require.True(t, ok)
	require.True(t, after.MFAPassed)
	require.False(t, after.PasskeyOwed)
	require.False(t, after.PasskeyHeld)
}
