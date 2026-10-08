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

// verifyMailbox is the inbox the signup verification links go to: the
// harness's registration service sends to it instead of a mail server.
type verifyMailbox struct{ tokens []string }

func (m *verifyMailbox) SendVerificationEmail(_, token, _ string) error {
	m.tokens = append(m.tokens, token)
	return nil
}

func (m *verifyMailbox) latest(t *testing.T) string {
	t.Helper()
	require.NotEmpty(t, m.tokens, "no verification link was sent")
	return m.tokens[len(m.tokens)-1]
}

// #360: a signup used to carry the password, and a second signup for the same
// address replaced it. So anyone who knew Alice's address could sign up as her
// with their own password after she did; the newest link went to Alice, she
// clicked it, and her account was created with the other person's password.
//
// The password is now chosen on the verification page, so only whoever reads
// the inbox chooses it. A password sent with the signup is ignored.
func TestSignup_PasswordIsChosenAtVerificationNotAtSignup(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeySelfSignupEnabled, true))
	require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeyOpenRegistrationEnabled, true))
	const email = "alice@signup.test"

	signup := func(password string) {
		res := h.doUnauth(t, http.MethodPost, "/api/v1/auth/signup",
			map[string]any{"email": email, "display_name": "Alice", "password": password})
		res.Body.Close()
		require.Equal(t, http.StatusAccepted, res.StatusCode)
	}
	verify := func(body map[string]any) (int, string) {
		res := h.doUnauth(t, http.MethodPost, "/api/v1/auth/verify-email", body)
		defer res.Body.Close()
		var out struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		_ = json.NewDecoder(res.Body).Decode(&out)
		return res.StatusCode, out.Error.Code
	}

	signup("alice-would-have-typed-this")
	first := h.verifyMail.latest(t)
	signup("mallory-chose-this-one") // Mallory, with Alice's address
	link := h.verifyMail.latest(t)
	require.NotEqual(t, first, link, "a second signup re-issues the link")

	var stored *string
	require.NoError(t, h.tx.QueryRow(
		`SELECT password_hash FROM pending_registrations WHERE lower(email) = $1`, email).Scan(&stored))
	require.Nil(t, stored, "a pending signup must hold no password")

	status, code := verify(map[string]any{"token": first, "password": "alice-chooses-now"})
	require.Equal(t, http.StatusUnprocessableEntity, status, "the replaced link must not work")
	require.Equal(t, "token_invalid", code)

	// No password, or a short one, is refused and does not use up the link.
	status, code = verify(map[string]any{"token": link})
	require.Equal(t, http.StatusBadRequest, status)
	require.Equal(t, "password_too_short", code)
	status, code = verify(map[string]any{"token": link, "password": "short"})
	require.Equal(t, http.StatusBadRequest, status)
	require.Equal(t, "password_too_short", code)

	status, _ = verify(map[string]any{"token": link, "password": "alice-chooses-now"})
	require.Equal(t, http.StatusOK, status)

	_, err := h.userSvc.VerifyPassword(ctx, email, "alice-chooses-now")
	require.NoError(t, err, "the account must have the password chosen at verification")
	for _, p := range []string{"mallory-chose-this-one", "alice-would-have-typed-this"} {
		_, err := h.userSvc.VerifyPassword(ctx, email, p)
		require.Error(t, err, "a password sent with a signup must not open the account")
	}
}

// #370: GET /auth/verify-email looks a link up without using it, so the page
// can show the address and name before asking for a password. A dead link
// answers as POST would.
func TestVerifyEmailLookup(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeySelfSignupEnabled, true))
	require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeyOpenRegistrationEnabled, true))

	res := h.doUnauth(t, http.MethodPost, "/api/v1/auth/signup",
		map[string]any{"email": "bob@signup.test", "display_name": "Bob"})
	res.Body.Close()
	require.Equal(t, http.StatusAccepted, res.StatusCode)
	link := h.verifyMail.latest(t)

	lookup := func(token string) (int, map[string]any) {
		res := h.doUnauth(t, http.MethodGet, "/api/v1/auth/verify-email?token="+token, nil)
		defer res.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(res.Body).Decode(&out)
		return res.StatusCode, out
	}

	status, body := lookup(link)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, map[string]any{"email": "bob@signup.test"}, body,
		"the lookup shows the address and nothing else: no token, no ids, and not the "+
			"display name, which whoever signed up first chose")

	// Looking it up does not use it.
	status, _ = lookup(link)
	require.Equal(t, http.StatusOK, status)

	for _, bad := range []string{"not-a-uuid", "00000000-0000-4000-8000-000000000000"} {
		status, body = lookup(bad)
		require.Equal(t, http.StatusUnprocessableEntity, status, bad)
		require.Equal(t, "token_invalid", body["error"].(map[string]any)["code"], bad)
	}

	_, err := h.tx.Exec(`UPDATE pending_registrations SET expires_at = now() - interval '1 minute' WHERE token = $1`, link)
	require.NoError(t, err)
	status, body = lookup(link)
	require.Equal(t, http.StatusUnprocessableEntity, status)
	require.Equal(t, "token_expired", body["error"].(map[string]any)["code"])
}

// #369: with MFA required for requesters, verifying a signup gives a session
// that owes enrolment, and says so. That session can reach enrolment and
// finish it; until then everything behind the MFA gate is refused. The server
// half was already right — the page ignored the flag — and this pins it.
func TestVerifyEmail_ASessionThatOwesEnrolmentCanEnrol(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeySelfSignupEnabled, true))
	require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeyOpenRegistrationEnabled, true))
	require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeyMFAEnabled, true))
	require.NoError(t, h.adminSvc.SetRaw(ctx, admin.KeyMFAEnforcedRoles, []byte(`["user"]`)))

	res := h.doUnauth(t, http.MethodPost, "/api/v1/auth/signup",
		map[string]any{"email": "carol@signup.test", "display_name": "Carol"})
	res.Body.Close()

	s := &session{h: h}
	res, body := s.send(t, http.MethodPost, "/api/v1/auth/verify-email",
		map[string]any{"token": h.verifyMail.latest(t), "password": "carol-chooses-this"})
	require.Equal(t, http.StatusOK, res.StatusCode, string(body))
	var verified struct {
		MFAEnrollmentNeeded bool `json:"mfa_enrollment_needed"`
	}
	require.NoError(t, json.Unmarshal(body, &verified))
	require.True(t, verified.MFAEnrollmentNeeded)

	res, _ = s.send(t, http.MethodGet, "/api/v1/tickets", nil)
	require.Equal(t, http.StatusForbidden, res.StatusCode, "the gate must hold until enrolment")

	res, body = s.send(t, http.MethodPost, "/api/v1/me/mfa/enroll", nil)
	require.Equal(t, http.StatusOK, res.StatusCode, string(body))
	var enrol struct {
		Secret string `json:"secret"`
	}
	require.NoError(t, json.Unmarshal(body, &enrol))
	code, err := totp.GenerateCode(enrol.Secret, time.Now())
	require.NoError(t, err)
	res, body = s.send(t, http.MethodPost, "/api/v1/me/mfa/enroll/confirm", map[string]any{"code": code})
	require.Less(t, res.StatusCode, 300, string(body))

	res, _ = s.send(t, http.MethodGet, "/api/v1/tickets", nil)
	require.Equal(t, http.StatusOK, res.StatusCode, "enrolment must open the gate")
}
