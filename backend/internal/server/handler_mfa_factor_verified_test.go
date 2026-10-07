package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
)

// #333: "nothing was owed at login" is not "a second factor was proved".
//
// With MFA on but optional for the account's role, a password-only login
// gets MFAPassed=true because nothing was owed. The add/remove-factor guard
// and enrolment's re-enrol check both read MFAPassed, so a session like that
// was treated as having proved a factor it never had. Each test below is one
// way an attacker holding only the password turned that into replacing the
// owner's second factor.

// mfaOptionalForStaff turns MFA on but enforces it only for administrators,
// so the seeded staff account may enrol and is not made to.
func mfaOptionalForStaff(t *testing.T, h *harness) {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeyMFAEnabled, true))
	require.NoError(t, h.adminSvc.SetRaw(ctx, admin.KeyMFAEnforcedRoles, []byte(`["admin"]`)))
}

func staffPasswordLogin(t *testing.T, h *harness) *session {
	t.Helper()
	s := &session{h: h}
	res, body := s.send(t, http.MethodPost, "/api/v1/auth/local/login", map[string]any{
		"email": "staff@test.local", "password": "password",
	})
	require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", body)
	return s
}

// enrolTOTP runs the whole enrolment from a session and returns the secret.
func enrolTOTP(t *testing.T, s *session) string {
	t.Helper()
	res, body := s.send(t, http.MethodPost, "/api/v1/me/mfa/enroll", nil)
	require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", body)
	var staged map[string]string
	require.NoError(t, json.Unmarshal(body, &staged))
	code, err := totp.GenerateCode(staged["secret"], time.Now())
	require.NoError(t, err)
	res, body = s.send(t, http.MethodPost, "/api/v1/me/mfa/enroll/confirm", map[string]any{"code": code})
	require.Equal(t, http.StatusNoContent, res.StatusCode, "body: %s", body)
	return staged["secret"]
}

// The #327 race, in the configuration #327's fix did not cover.
func TestMFA_OptionalRole_ConfirmCannotOverwriteAFactorAddedDuringEnrollment(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	mfaOptionalForStaff(t, h)

	attacker := staffPasswordLogin(t, h)
	res, body := attacker.send(t, http.MethodPost, "/api/v1/me/mfa/enroll", nil)
	require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", body)
	var staged map[string]string
	require.NoError(t, json.Unmarshal(body, &staged))

	victimSecret := enrolTOTP(t, staffPasswordLogin(t, h))

	code, err := totp.GenerateCode(staged["secret"], time.Now())
	require.NoError(t, err)
	res, body = attacker.send(t, http.MethodPost, "/api/v1/me/mfa/enroll/confirm", map[string]any{"code": code})
	require.NotEqual(t, http.StatusNoContent, res.StatusCode, "the attacker's stale enrolment was adopted; body: %s", body)

	after, err := h.userSvc.GetByID(context.Background(), h.staffID)
	require.NoError(t, err)
	require.Equal(t, victimSecret, after.MFASecret, "the victim's secret was overwritten")
}

// Worse than the race: no timing needed. A session opened before the owner
// enrolled still carried MFAPassed=true afterwards, and that alone let it
// start a fresh enrolment and replace the owner's factor whenever it liked.
func TestMFA_OptionalRole_AnOldSessionCannotReplaceTheFactorLater(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	mfaOptionalForStaff(t, h)

	attacker := staffPasswordLogin(t, h)
	victimSecret := enrolTOTP(t, staffPasswordLogin(t, h))

	res, body := attacker.send(t, http.MethodPost, "/api/v1/me/mfa/enroll", nil)
	require.NotEqual(t, http.StatusOK, res.StatusCode,
		"a session that never proved a factor started re-enrolment over one; body: %s", body)

	after, err := h.userSvc.GetByID(context.Background(), h.staffID)
	require.NoError(t, err)
	require.Equal(t, victimSecret, after.MFASecret)
}

// The session-shape half, apart from revocation: a session that owed nothing
// at login and proved nothing must not be able to replace an existing factor.
// MFA switched off instance-wide is the plainest way to get one — the owner
// still has a TOTP, login asks for nothing, and MFAPassed is true. When MFA
// is switched back on, whoever replaced the secret holds the account.
func TestMFA_ASessionThatProvedNoFactorCannotReplaceOne(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	mfaOptionalForStaff(t, h)
	victimSecret := enrolTOTP(t, staffPasswordLogin(t, h))
	require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeyMFAEnabled, false))

	s := staffPasswordLogin(t, h)
	res, body := s.send(t, http.MethodPost, "/api/v1/me/mfa/enroll", nil)
	require.Equal(t, http.StatusForbidden, res.StatusCode, "body: %s", body)

	after, err := h.userSvc.GetByID(ctx, h.staffID)
	require.NoError(t, err)
	require.Equal(t, victimSecret, after.MFASecret)
}

// And the way back in for the owner: proving the factor they have.
func TestMFA_ProvingTheFactorAllowsRotation(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	mfaOptionalForStaff(t, h)
	secret := enrolTOTP(t, staffPasswordLogin(t, h))
	require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeyMFAEnabled, false))

	s := staffPasswordLogin(t, h)
	code, err := totp.GenerateCode(secret, time.Now())
	require.NoError(t, err)
	res, body := s.send(t, http.MethodPost, "/api/v1/auth/local/mfa/verify", map[string]any{"code": code})
	require.Equal(t, http.StatusNoContent, res.StatusCode, "body: %s", body)

	res, body = s.send(t, http.MethodPost, "/api/v1/me/mfa/enroll", nil)
	require.Equal(t, http.StatusOK, res.StatusCode, "a session that proved its factor was refused; body: %s", body)
}

// Adding a factor ends every other session, the way changing a password
// does: a session someone opened with the password alone, before the owner
// protected the account, must not outlive that protection. The session that
// added the factor stays signed in.
func TestMFA_AddingAFactorSignsOutOtherSessions(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	mfaOptionalForStaff(t, h)

	other := staffPasswordLogin(t, h)
	res, _ := other.send(t, http.MethodGet, "/api/v1/me", nil)
	require.Equal(t, http.StatusOK, res.StatusCode, "precondition: the other session works")

	owner := staffPasswordLogin(t, h)
	enrolTOTP(t, owner)

	res, _ = other.send(t, http.MethodGet, "/api/v1/me", nil)
	require.Equal(t, http.StatusUnauthorized, res.StatusCode, "a session from before the factor survived it")
	res, body := owner.send(t, http.MethodGet, "/api/v1/me", nil)
	require.Equal(t, http.StatusOK, res.StatusCode, "the session that added the factor was signed out; body: %s", body)
}

// #327's own check — re-run the guard at confirm, not only at stage — on its
// own. Ending other sessions (above) now refuses the attacker first in the
// end-to-end race, so here the factor appears without any session ending:
// written straight to the account, as a concurrent request on another
// replica or an administrator's tooling could. Only the confirm-time
// re-check stands between the staged secret and the account.
func TestMFA_ConfirmReChecksEvenWhenTheSessionSurvives(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeyMFAEnabled, true))
	require.NoError(t, h.adminSvc.SetRaw(ctx, admin.KeyMFAEnforcedRoles, []byte(`["staff","admin"]`)))

	attacker := staffPasswordLogin(t, h)
	res, body := attacker.send(t, http.MethodPost, "/api/v1/me/mfa/enroll", nil)
	require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", body)
	var staged map[string]string
	require.NoError(t, json.Unmarshal(body, &staged))

	enrollMFA(t, ctx, h.userSvc, h.staffID) // the factor appears; no session ends
	before, err := h.userSvc.GetByID(ctx, h.staffID)
	require.NoError(t, err)

	code, err := totp.GenerateCode(staged["secret"], time.Now())
	require.NoError(t, err)
	res, body = attacker.send(t, http.MethodPost, "/api/v1/me/mfa/enroll/confirm", map[string]any{"code": code})
	require.Equal(t, http.StatusForbidden, res.StatusCode, "body: %s", body)

	after, err := h.userSvc.GetByID(ctx, h.staffID)
	require.NoError(t, err)
	require.Equal(t, before.MFASecret, after.MFASecret)
}

// The add/remove-factor guard on its own. TOTP enrolment start also has the
// service's re-enrol check behind the guard, so it cannot show the guard
// alone; the passkey routes have nothing else. A session that owed nothing
// at login (MFA off instance-wide) on an account with a TOTP must not be able
// to start registering a passkey.
func TestPasskeys_ASessionThatProvedNoFactorCannotAddOne(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	mfaOptionalForStaff(t, h)
	enrolTOTP(t, staffPasswordLogin(t, h))
	require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeyMFAEnabled, false))

	s := staffPasswordLogin(t, h)
	res, body := s.send(t, http.MethodPost, "/api/v1/me/passkeys/register/start", nil)
	require.Equal(t, http.StatusForbidden, res.StatusCode, "body: %s", body)
}

// Changing the password re-issues the session. It must carry FactorVerified
// across, or a user who just proved their factor and then changed their
// password would be refused at the next factor change for no reason.
func TestMFA_PasswordChangeKeepsAProvedFactor(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	mfaOptionalForStaff(t, h)
	s := staffPasswordLogin(t, h)
	enrolTOTP(t, s) // this session has now proved a factor

	res, body := s.send(t, http.MethodPatch, "/api/v1/me/password", map[string]any{
		"current_password": "password", "new_password": "a-much-longer-new-password",
	})
	require.Less(t, res.StatusCode, 300, "body: %s", body)

	res, body = s.send(t, http.MethodPost, "/api/v1/me/mfa/enroll", nil)
	require.Equal(t, http.StatusOK, res.StatusCode, "the proved factor was lost with the password change; body: %s", body)
}
