package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
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

// #360, #374: a signup used to carry the password and the display name, and a
// second signup for the same address replaced them. So anyone who knew Alice's address could sign up as her
// with their own password after she did; the newest link went to Alice, she
// clicked it, and her account was created with the other person's password.
//
// Both are now chosen on the verification page, so only whoever reads the
// inbox chooses them. A password or name sent with the signup is ignored.
func TestSignup_PasswordIsChosenAtVerificationNotAtSignup(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeySelfSignupEnabled, true))
	require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeyOpenRegistrationEnabled, true))
	const email = "alice@signup.test"

	signup := func(name, password string) {
		res := h.doUnauth(t, http.MethodPost, "/api/v1/auth/signup",
			map[string]any{"email": email, "display_name": name, "password": password})
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

	signup("Alice", "alice-would-have-typed-this")
	first := h.verifyMail.latest(t)
	signup("Your account is locked, call 555-0100", "mallory-chose-this-one") // Mallory, with Alice's address
	link := h.verifyMail.latest(t)
	require.NotEqual(t, first, link, "a second signup re-issues the link")

	var storedHash, storedName *string
	require.NoError(t, h.tx.QueryRow(
		`SELECT password_hash, display_name FROM pending_registrations WHERE lower(email) = $1`, email).
		Scan(&storedHash, &storedName))
	require.Nil(t, storedHash, "a pending signup must hold no password")
	require.Nil(t, storedName, "a pending signup must hold no display name (#374)")

	status, code := verify(map[string]any{"token": first, "display_name": "Alice", "password": "alice-chooses-now"})
	require.Equal(t, http.StatusUnprocessableEntity, status, "the replaced link must not work")
	require.Equal(t, "token_invalid", code)

	// No password, a short one, a long one, or no name is refused and does not use up
	// the link.
	status, code = verify(map[string]any{"token": link, "display_name": "Alice"})
	require.Equal(t, http.StatusBadRequest, status)
	require.Equal(t, "password_too_short", code)
	status, code = verify(map[string]any{"token": link, "display_name": "Alice", "password": "short"})
	require.Equal(t, http.StatusBadRequest, status)
	require.Equal(t, "password_too_short", code)
	status, code = verify(map[string]any{"token": link, "display_name": "Alice", "password": strings.Repeat("密", 25)})
	require.Equal(t, http.StatusBadRequest, status)
	require.Equal(t, "password_too_long", code)
	status, code = verify(map[string]any{"token": link, "display_name": "  ", "password": "alice-chooses-now"})
	require.Equal(t, http.StatusBadRequest, status)
	require.Equal(t, "display_name_required", code)

	status, _ = verify(map[string]any{"token": link, "display_name": "Alice", "password": "alice-chooses-now"})
	require.Equal(t, http.StatusOK, status)

	u, err := h.userSvc.VerifyPassword(ctx, email, "alice-chooses-now")
	require.NoError(t, err, "the account must have the password chosen at verification")
	require.Equal(t, "Alice", u.DisplayName, "the account must have the name chosen at verification, "+
		"not one a signup sent (#374)")
	for _, p := range []string{"mallory-chose-this-one", "alice-would-have-typed-this"} {
		_, err := h.userSvc.VerifyPassword(ctx, email, p)
		require.Error(t, err, "a password sent with a signup must not open the account")
	}
}

// #370: GET /auth/verify-email looks a link up without using it, so the page
// can show the address before asking for a name and password. A dead link
// answers as the POST would; a fault is a 500 on both
// (TestVerifyEmail_AFaultIsNotADeadLink).
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
		"the lookup shows the address and nothing else: no token, no ids")

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
		map[string]any{"token": h.verifyMail.latest(t), "display_name": "Carol", "password": "carol-chooses-this"})
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

// #373: a fault is not a verdict on the link. With the database refusing every
// statement, the lookup and the verification both answer 500, never 422
// token_invalid: an outage must not tell somebody their link is dead. The real
// store, so its own mapping (only sql.ErrNoRows is ErrNotFound) is under test
// too, not a fake's.
func TestVerifyEmail_AFaultIsNotADeadLink(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	// A failed statement aborts the harness transaction, and Postgres then
	// refuses every statement until it is rolled back (SQLSTATE 25P02).
	_, err := h.tx.Exec(`SELECT 1/0`)
	require.Error(t, err)

	const token = "00000000-0000-4000-8000-000000000000"
	for _, tc := range []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, "/api/v1/auth/verify-email?token=" + token, nil},
		{http.MethodPost, "/api/v1/auth/verify-email",
			map[string]any{"token": token, "display_name": "Alice", "password": "alice-chooses-now"}},
	} {
		res := h.doUnauth(t, tc.method, tc.path, tc.body)
		var out struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		_ = json.NewDecoder(res.Body).Decode(&out)
		res.Body.Close()
		require.Equal(t, http.StatusInternalServerError, res.StatusCode, tc.method)
		require.Equal(t, "internal_error", out.Error.Code, tc.method)
	}
}

// #373: a link that passes the lookup can still fail at the POST when the
// address has since gained an account (DESIGN.md, Self-Service Signup). That
// is a verdict, answered like a used link: not 409 email_taken, not a 500.
func TestVerifyEmail_AnAddressThatGainedAnAccountIsADeadLink(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeySelfSignupEnabled, true))
	require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeyOpenRegistrationEnabled, true))
	const email = "dave@signup.test"

	res := h.doUnauth(t, http.MethodPost, "/api/v1/auth/signup", map[string]any{"email": email})
	res.Body.Close()
	require.Equal(t, http.StatusAccepted, res.StatusCode)
	link := h.verifyMail.latest(t)
	_, err := h.userSvc.Create(ctx, user.CreateUserInput{
		Email: email, DisplayName: "Dave", Role: user.RoleUser, Password: "created-by-an-admin",
	})
	require.NoError(t, err)

	res = h.doUnauth(t, http.MethodPost, "/api/v1/auth/verify-email",
		map[string]any{"token": link, "display_name": "Dave", "password": "dave-chooses-this"})
	defer res.Body.Close()
	var out struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&out))
	require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode)
	require.Equal(t, "token_invalid", out.Error.Code)
}

// #373: an expired link is a verdict on the POST as on the lookup. If the POST
// stopped answering token_expired, an expired link would get a 500, the page
// would keep the form forever, and the advice to sign up again would never show.
func TestVerifyEmail_AnExpiredLinkIsADeadLink(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeySelfSignupEnabled, true))
	require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeyOpenRegistrationEnabled, true))
	const email = "erin@signup.test"

	res := h.doUnauth(t, http.MethodPost, "/api/v1/auth/signup", map[string]any{"email": email})
	res.Body.Close()
	require.Equal(t, http.StatusAccepted, res.StatusCode)
	link := h.verifyMail.latest(t)

	_, err := h.tx.Exec(`UPDATE pending_registrations SET expires_at = now() - interval '1 minute' WHERE token = $1`, link)
	require.NoError(t, err)

	res = h.doUnauth(t, http.MethodPost, "/api/v1/auth/verify-email",
		map[string]any{"token": link, "display_name": "Erin", "password": "erin-chooses-this"})
	defer res.Body.Close()
	var out struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&out))
	require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode)
	require.Equal(t, "token_expired", out.Error.Code)

	_, err = h.userSvc.VerifyPassword(ctx, email, "erin-chooses-this")
	require.Error(t, err, "an expired link must not create the account")
}
