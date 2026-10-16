package server_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/config"
	"github.com/publiciallc/go-help-desk/backend/internal/database/outboxstore"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/notification"
	"github.com/publiciallc/go-help-desk/backend/internal/server/notify"
)

// resetMailbox is the inbox the password reset links go to: the harness's
// reset service sends to it instead of a mail server.
type resetMailbox struct {
	sent []resetMail
	// failNext makes that many sends fail, as a relay that is down does.
	failNext int
}

type resetMail struct{ to, token string }

func (m *resetMailbox) SendPasswordResetEmail(to, token, _ string) error {
	if m.failNext > 0 {
		m.failNext--
		return errors.New("smtp: connection refused")
	}
	m.sent = append(m.sent, resetMail{to, token})
	return nil
}

func (m *resetMailbox) latest(t *testing.T) resetMail {
	t.Helper()
	require.NotEmpty(t, m.sent, "no reset link was sent")
	return m.sent[len(m.sent)-1]
}

// harnessWithEmail is a harness whose server can send email, which "forgot
// password" requires.
func harnessWithEmail(t *testing.T) (*harness, func()) {
	t.Helper()
	return newHarnessCfg(t, 0, "", emailOn)
}

func forgot(t *testing.T, h *harness, email string) (int, string) {
	t.Helper()
	res := h.doUnauth(t, http.MethodPost, "/api/v1/auth/forgot-password", map[string]any{"email": email})
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	return res.StatusCode, string(b)
}

func resetPassword(t *testing.T, h *harness, token, password string) (*http.Response, string) {
	t.Helper()
	res := h.doUnauth(t, http.MethodPost, "/api/v1/auth/reset-password",
		map[string]any{"token": token, "password": password})
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	return res, string(b)
}

func bodyErrorCode(body string) string {
	var e apiError
	_ = json.Unmarshal([]byte(body), &e)
	return e.Error.Code
}

// makeSSOOnly turns an account into one that signs in through OIDC alone: it
// has an identity provider subject and no password.
func makeSSOOnly(t *testing.T, h *harness, email string) {
	t.Helper()
	_, err := h.tx.Exec(`UPDATE users SET password_hash = '', oidc_subject = 'oidc-' || id::text WHERE email = $1`, email)
	require.NoError(t, err)
}

func passwordSignIn(t *testing.T, h *harness, email, password string) (*http.Response, string) {
	t.Helper()
	s := &session{h: h}
	res, body := s.send(t, http.MethodPost, "/api/v1/auth/local/login",
		map[string]any{"email": email, "password": password})
	return res, string(body)
}

// ── the request ───────────────────────────────────────────────────────────────

// #451: the same answer, to the byte, whether or not the address has an
// account. A different one would make this a way to find out who does.
func TestForgotPassword_AnswersTheSameForAKnownAndAnUnknownAddress(t *testing.T) {
	h, cleanup := harnessWithEmail(t)
	defer cleanup()

	knownStatus, knownBody := forgot(t, h, "user@test.local")
	unknownStatus, unknownBody := forgot(t, h, "nobody-at-all@test.local")

	require.Equal(t, http.StatusAccepted, knownStatus)
	require.Equal(t, http.StatusAccepted, unknownStatus)
	require.Equal(t, knownBody, unknownBody)
	require.Len(t, h.resetMail.sent, 1, "only the account is mailed")
	require.Equal(t, "user@test.local", h.resetMail.sent[0].to)
}

func TestForgotPassword_AlwaysAnswers202ToAWellFormedRequest(t *testing.T) {
	h, cleanup := harnessWithEmail(t)
	defer cleanup()

	for _, in := range []string{"", "   ", "not an address", "Eve <eve@example.com>"} {
		status, _ := forgot(t, h, in)
		require.Equal(t, http.StatusAccepted, status, "%q", in)
	}
	require.Empty(t, h.resetMail.sent)
}

func TestForgotPassword_AMalformedBodyIs400(t *testing.T) {
	h, cleanup := harnessWithEmail(t)
	defer cleanup()
	res := h.doUnauth(t, http.MethodPost, "/api/v1/auth/forgot-password", "this is not an object")
	res.Body.Close()
	require.Equal(t, http.StatusBadRequest, res.StatusCode)
}

// With email off a reset link cannot be delivered, so the endpoint says so
// instead of answering 202 and mailing nothing.
func TestForgotPassword_Is503WhileEmailIsOff(t *testing.T) {
	h, cleanup := newHarness(t) // no SMTP
	defer cleanup()

	status, body := forgot(t, h, "user@test.local")

	require.Equal(t, http.StatusServiceUnavailable, status)
	require.Equal(t, "email_not_configured", bodyErrorCode(body))
	require.Empty(t, h.resetMail.sent)
}

// The request is counted against the caller's address, as signup is, so the
// endpoint cannot be driven to fill an inbox or the outbox. A nonexistent
// address is counted exactly like a real one.
func TestForgotPassword_IsRateLimited(t *testing.T) {
	h, cleanup := newHarnessCfg(t, 3, "", emailOn)
	defer cleanup()

	for i := 1; i <= 3; i++ {
		status, _ := forgot(t, h, "nobody@test.local")
		require.Equal(t, http.StatusAccepted, status, "attempt %d", i)
	}
	res := h.doUnauth(t, http.MethodPost, "/api/v1/auth/forgot-password", map[string]any{"email": "user@test.local"})
	res.Body.Close()

	require.Equal(t, http.StatusTooManyRequests, res.StatusCode)
	require.NotEmpty(t, res.Header.Get("Retry-After"))
	require.Empty(t, h.resetMail.sent, "a refused request still mailed")
}

// ── deciding at send time ─────────────────────────────────────────────────────

func TestForgotPassword_NoLinkForAnAccountThatCannotHaveOne(t *testing.T) {
	cases := []struct {
		name     string
		setup    func(t *testing.T, h *harness)
		email    string
		wantMail bool
	}{
		{"an ordinary account", func(*testing.T, *harness) {}, "user@test.local", true},
		{"a disabled account", func(t *testing.T, h *harness) {
			_, err := h.tx.Exec(`UPDATE users SET disabled = true WHERE email = 'user@test.local'`)
			require.NoError(t, err)
		}, "user@test.local", false},
		{"a deleted account", func(t *testing.T, h *harness) {
			_, err := h.tx.Exec(`UPDATE users SET deleted_at = now() WHERE email = 'user@test.local'`)
			require.NoError(t, err)
		}, "user@test.local", false},
		{"a requester when SAML limits passwords to administrators", func(t *testing.T, h *harness) {
			require.NoError(t, h.adminSvc.SetBool(context.Background(), admin.KeySAMLEnabled, true))
		}, "user@test.local", false},
		{"staff when SAML limits passwords to administrators", func(t *testing.T, h *harness) {
			require.NoError(t, h.adminSvc.SetBool(context.Background(), admin.KeySAMLEnabled, true))
		}, "staff@test.local", false},
		{"an administrator when SAML limits passwords to administrators", func(t *testing.T, h *harness) {
			require.NoError(t, h.adminSvc.SetBool(context.Background(), admin.KeySAMLEnabled, true))
		}, "admin@test.local", true},
		// #451: "forgot password" is not for accounts that sign in through an
		// identity provider. Giving one a password would put a way in beside the
		// provider's MFA and conditional access.
		{"an account that signs in through OIDC and has no password", func(t *testing.T, h *harness) {
			makeSSOOnly(t, h, "user@test.local")
		}, "user@test.local", false},
		{"an administrator with no password when SAML limits passwords to administrators", func(t *testing.T, h *harness) {
			require.NoError(t, h.adminSvc.SetBool(context.Background(), admin.KeySAMLEnabled, true))
			makeSSOOnly(t, h, "admin@test.local")
		}, "admin@test.local", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, cleanup := harnessWithEmail(t)
			defer cleanup()
			tc.setup(t, h)

			status, _ := forgot(t, h, tc.email)

			require.Equal(t, http.StatusAccepted, status, "the answer must not depend on any of this")
			if tc.wantMail {
				require.Len(t, h.resetMail.sent, 1)
			} else {
				require.Empty(t, h.resetMail.sent)
				var n int
				require.NoError(t, h.tx.QueryRow(`SELECT count(*) FROM password_reset_tokens`).Scan(&n))
				require.Zero(t, n, "a token was made for an account that cannot use it")
			}
		})
	}
}

// #451: a link mailed while the account had a password must not give an SSO-only
// account one if the password went away before the link was used. The link is
// refused and spent, no password appears, and local sign-in stays closed.
func TestResetPassword_ALinkForAnAccountThatLostItsPasswordDoesNothing(t *testing.T) {
	h, cleanup := harnessWithEmail(t)
	defer cleanup()
	forgot(t, h, "user@test.local")
	token := h.resetMail.latest(t).token
	makeSSOOnly(t, h, "user@test.local")

	res, body := resetPassword(t, h, token, "a-brand-new-password")

	require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode, "%s", body)
	require.Equal(t, "token_invalid", bodyErrorCode(body))
	var hash string
	require.NoError(t, h.tx.QueryRow(`SELECT password_hash FROM users WHERE email = 'user@test.local'`).Scan(&hash))
	require.Empty(t, hash, "the account was given a password")
	in, inBody := passwordSignIn(t, h, "user@test.local", "a-brand-new-password")
	require.NotEqual(t, http.StatusOK, in.StatusCode, "local sign-in opened for an SSO-only account: %s", inBody)
	again, _ := resetPassword(t, h, token, "a-brand-new-password")
	require.Equal(t, http.StatusUnprocessableEntity, again.StatusCode)
}

// An address is matched the way a sign-in matches it.
func TestForgotPassword_MatchesTheAddressInAnyCase(t *testing.T) {
	h, cleanup := harnessWithEmail(t)
	defer cleanup()
	forgot(t, h, "  User@Test.LOCAL ")
	require.Len(t, h.resetMail.sent, 1)
	require.Equal(t, "user@test.local", h.resetMail.sent[0].to)
}

// Requesting again does not mail again for a while: one account cannot have its
// inbox filled. The first link is still good, so nobody is left without one.
func TestForgotPassword_AnAccountIsMailedAtMostOncePerBudget(t *testing.T) {
	h, cleanup := harnessWithEmail(t)
	defer cleanup()

	forgot(t, h, "user@test.local")
	first := h.resetMail.latest(t).token
	status, _ := forgot(t, h, "user@test.local")

	require.Equal(t, http.StatusAccepted, status, "the answer does not change")
	require.Len(t, h.resetMail.sent, 1, "a second request mailed again")
	res, body := resetPassword(t, h, first, "a-new-long-password")
	require.Equal(t, http.StatusNoContent, res.StatusCode, "the first link must still work: %s", body)
}

// Through the real outbox and worker: the row holds the address and no token,
// and a send that fails is retried rather than swallowed by the budget (the
// budget is spent once per request, not once per attempt, as for guest links).
func TestForgotPassword_ThroughTheOutbox(t *testing.T) {
	h, cleanup := harnessWithEmail(t)
	defer cleanup()
	ctx := context.Background()
	store := outboxstore.New(h.q)
	h.resetQueue.Dispatcher = notify.NewOutboxDispatcher(store, []string{"email", "webhook", "verification"}, nil)

	status, _ := forgot(t, h, "user@test.local")
	require.Equal(t, http.StatusAccepted, status)

	rows, err := store.Claim(ctx, 10, time.Minute)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Contains(t, string(rows[0].Event), "user@test.local")
	require.NotContains(t, strings.ToLower(string(rows[0].Event)), "token")
	require.NoError(t, store.Retry(ctx, rows[0].ID, time.Now().Add(-time.Second), "released by test"))

	h.resetMail.failNext = 1
	send := notify.NewVerificationDispatcher(func(context.Context, uuid.UUID) error { return nil }).
		WithPasswordReset(h.srv.SendPasswordReset)
	w := notify.NewWorker(store, map[string]notification.Dispatcher{
		"email": notification.Noop{}, "webhook": notification.Noop{}, "verification": send,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	// Due at once; see TestGuestResend_AFailedSendIsRetriedNotSwallowedByTheBudget.
	w.Backoff = func(int) time.Duration { return -time.Hour }

	_, err = w.RunOnce(ctx) // attempt 1: the relay is down
	require.NoError(t, err)
	require.Empty(t, h.resetMail.sent)
	_, err = w.RunOnce(ctx) // the retry

	require.NoError(t, err)
	require.Len(t, h.resetMail.sent, 1, "the retry was refused and the link never sent")
	res, body := resetPassword(t, h, h.resetMail.latest(t).token, "a-new-long-password")
	require.Equal(t, http.StatusNoContent, res.StatusCode, "%s", body)
}

// ── the link ──────────────────────────────────────────────────────────────────

func TestResetPassword_SetsThePasswordAndGivesNoSession(t *testing.T) {
	h, cleanup := harnessWithEmail(t)
	defer cleanup()
	forgot(t, h, "user@test.local")
	token := h.resetMail.latest(t).token

	res, body := resetPassword(t, h, token, "a-brand-new-password")

	require.Equal(t, http.StatusNoContent, res.StatusCode, "%s", body)
	require.Empty(t, res.Cookies(), "the link must never sign anybody in")

	old, _ := passwordSignIn(t, h, "user@test.local", "password")
	require.Equal(t, http.StatusUnauthorized, old.StatusCode, "the old password still works")
	now, nowBody := passwordSignIn(t, h, "user@test.local", "a-brand-new-password")
	require.Equal(t, http.StatusOK, now.StatusCode, "%s", nowBody)
}

// A second factor is still owed after a reset: a stolen inbox alone must not
// be a stolen account.
func TestResetPassword_StillOwesTheSecondFactor(t *testing.T) {
	h, cleanup := harnessWithEmail(t)
	defer cleanup()
	mfaOptionalForStaff(t, h)
	enrolTOTP(t, staffPasswordLogin(t, h))

	forgot(t, h, "staff@test.local")
	res, body := resetPassword(t, h, h.resetMail.latest(t).token, "a-brand-new-password")
	require.Equal(t, http.StatusNoContent, res.StatusCode, "%s", body)
	require.Empty(t, res.Cookies())

	s := &session{h: h}
	signedIn, signedInBody := s.send(t, http.MethodPost, "/api/v1/auth/local/login",
		map[string]any{"email": "staff@test.local", "password": "a-brand-new-password"})
	require.Equal(t, http.StatusOK, signedIn.StatusCode, "%s", signedInBody)
	require.Contains(t, string(signedInBody), `"mfa_needed":true`)
	me, _ := s.send(t, http.MethodGet, "/api/v1/me", nil)
	require.Equal(t, http.StatusForbidden, me.StatusCode, "the password alone opened the account")
}

func TestResetPassword_EndsEverySessionTheAccountHolds(t *testing.T) {
	h, cleanup := harnessWithEmail(t)
	defer cleanup()
	signedIn := loggedIn(t, h) // staff@test.local, with a working cookie

	forgot(t, h, "staff@test.local")
	res, body := resetPassword(t, h, h.resetMail.latest(t).token, "a-brand-new-password")
	require.Equal(t, http.StatusNoContent, res.StatusCode, "%s", body)

	me, _ := signedIn.send(t, http.MethodGet, "/api/v1/me", nil)
	require.Equal(t, http.StatusUnauthorized, me.StatusCode, "a session survived the password reset")
}

func TestResetPassword_ALinkWorksOnce(t *testing.T) {
	h, cleanup := harnessWithEmail(t)
	defer cleanup()
	forgot(t, h, "user@test.local")
	token := h.resetMail.latest(t).token

	first, _ := resetPassword(t, h, token, "the-first-new-password")
	require.Equal(t, http.StatusNoContent, first.StatusCode)
	second, body := resetPassword(t, h, token, "the-second-new-password")

	require.Equal(t, http.StatusUnprocessableEntity, second.StatusCode)
	require.Equal(t, "token_invalid", bodyErrorCode(body))
	ok, _ := passwordSignIn(t, h, "user@test.local", "the-first-new-password")
	require.Equal(t, http.StatusOK, ok.StatusCode, "the used link changed the password again")
}

func TestResetPassword_ALinkIsGoodForThirtyMinutes(t *testing.T) {
	cases := []struct {
		name string
		age  string // how long ago the link was made
		want int
	}{
		{"29 minutes old", "29 minutes", http.StatusNoContent},
		{"31 minutes old", "31 minutes", http.StatusUnprocessableEntity},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, cleanup := harnessWithEmail(t)
			defer cleanup()
			forgot(t, h, "user@test.local")
			token := h.resetMail.latest(t).token
			// The link's deadline is set from a 30 minute lifetime when it is
			// made; ageing it moves the deadline back by how old it is.
			_, err := h.tx.Exec(`UPDATE password_reset_tokens
				SET expires_at = clock_timestamp() + interval '30 minutes' - interval '` + tc.age + `'`)
			require.NoError(t, err)

			res, body := resetPassword(t, h, token, "a-brand-new-password")

			require.Equal(t, tc.want, res.StatusCode, "%s", body)
			if tc.want == http.StatusUnprocessableEntity {
				require.Equal(t, "token_invalid", bodyErrorCode(body))
			}
		})
	}
}

func TestResetPassword_TheLinkIsStoredHashed(t *testing.T) {
	h, cleanup := harnessWithEmail(t)
	defer cleanup()
	forgot(t, h, "user@test.local")
	token := h.resetMail.latest(t).token

	var stored string
	require.NoError(t, h.tx.QueryRow(`SELECT token_hash FROM password_reset_tokens`).Scan(&stored))
	require.NotEqual(t, token, stored)
	require.NotContains(t, stored, token)
}

func TestResetPassword_RefusesAnUnknownOrMissingLink(t *testing.T) {
	h, cleanup := harnessWithEmail(t)
	defer cleanup()
	for _, tok := range []string{"", "not-a-token", strings.Repeat("a", 64)} {
		res, body := resetPassword(t, h, tok, "a-brand-new-password")
		require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode, "%q: %s", tok, body)
		require.Equal(t, "token_invalid", bodyErrorCode(body))
	}
}

// A password the rules refuse does not use the link up, so the person can try
// again.
func TestResetPassword_ARefusedPasswordLeavesTheLinkWorking(t *testing.T) {
	cases := []struct {
		name, password, code string
	}{
		{"too short", "short", "password_too_short"},
		{"too long", strings.Repeat("x", 73), "password_too_long"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, cleanup := harnessWithEmail(t)
			defer cleanup()
			forgot(t, h, "user@test.local")
			token := h.resetMail.latest(t).token

			res, body := resetPassword(t, h, token, tc.password)
			require.Equal(t, http.StatusBadRequest, res.StatusCode, "%s", body)
			require.Equal(t, tc.code, bodyErrorCode(body))

			res, body = resetPassword(t, h, token, "a-brand-new-password")
			require.Equal(t, http.StatusNoContent, res.StatusCode, "the refused password used the link up: %s", body)
		})
	}
}

func TestResetPassword_AMalformedBodyIs400(t *testing.T) {
	h, cleanup := harnessWithEmail(t)
	defer cleanup()
	res := h.doUnauth(t, http.MethodPost, "/api/v1/auth/reset-password", "nope")
	res.Body.Close()
	require.Equal(t, http.StatusBadRequest, res.StatusCode)
}

// A link already in an inbox keeps working if email is switched off afterwards:
// it was a promise made while it was on.
func TestResetPassword_DoesNotNeedEmailToBeOn(t *testing.T) {
	var cfg *config.Config
	h, cleanup := newHarnessCfg(t, 0, "", func(c *config.Config) { emailOn(c); cfg = c })
	defer cleanup()
	forgot(t, h, "user@test.local")
	token := h.resetMail.latest(t).token
	cfg.SMTPHost = ""

	res, body := resetPassword(t, h, token, "a-brand-new-password")
	require.Equal(t, http.StatusNoContent, res.StatusCode, "%s", body)
}

func TestResetPassword_IsRecordedInTheAuditTrail(t *testing.T) {
	h, cleanup := harnessWithEmail(t)
	defer cleanup()
	ctx := context.Background()
	forgot(t, h, "user@test.local")
	res, body := resetPassword(t, h, h.resetMail.latest(t).token, "a-brand-new-password")
	require.Equal(t, http.StatusNoContent, res.StatusCode, "%s", body)

	entries, err := h.auditStore.ListByEntity(ctx, "user", h.userID, 10, 0)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, "password_reset_by_email", entries[0].Action)
	require.NotEqual(t, "password_reset_by_admin", entries[0].Action)
	require.Nil(t, entries[0].ActorID)
}

// A reset does not touch the administrator's own route, which still works with
// email off and is the way back when email is not configured.
func TestResetPassword_LeavesTheAdministratorsResetAlone(t *testing.T) {
	h, cleanup := newHarness(t) // email off
	defer cleanup()
	res := h.doAsAdmin(t, http.MethodPost, "/api/v1/admin/users/"+h.userID.String()+"/password",
		map[string]any{"new_password": "set-by-an-administrator"})
	res.Body.Close()
	require.Equal(t, http.StatusNoContent, res.StatusCode)
	ok, body := passwordSignIn(t, h, "user@test.local", "set-by-an-administrator")
	require.Equal(t, http.StatusOK, ok.StatusCode, "%s", body)
}

// ── what the sign-in page is told ─────────────────────────────────────────────

func TestAuthProviders_SaysWhetherPasswordResetIsAvailable(t *testing.T) {
	read := func(h *harness) bool {
		res := h.doUnauth(t, http.MethodGet, "/api/v1/auth/providers", nil)
		defer res.Body.Close()
		var out struct {
			PasswordReset *bool `json:"password_reset"`
		}
		require.NoError(t, json.NewDecoder(res.Body).Decode(&out))
		require.NotNil(t, out.PasswordReset, "password_reset is missing from /auth/providers")
		return *out.PasswordReset
	}
	// One harness at a time: each holds a transaction that has inserted the same
	// seeded accounts, so a second opened beside the first waits on it forever.
	t.Run("email off", func(t *testing.T) {
		h, cleanup := newHarness(t)
		defer cleanup()
		require.False(t, read(h), "email is off, so there is no reset to offer")
	})
	t.Run("email on", func(t *testing.T) {
		h, cleanup := harnessWithEmail(t)
		defer cleanup()
		require.True(t, read(h))
	})
}

// #493's refusal points somewhere real now: with email on it offers the link,
// with email off it says an administrator has to do it, and either way it still
// names "Forgot password" (the wording handler_password_expiry_test.go pins).
func TestLogin_OldHashRefusalPointsAtTheResetThatExists(t *testing.T) {
	cases := []struct {
		name  string
		mod   func(*config.Config)
		wants []string
	}{
		{"email on", emailOn, []string{"Forgot password", "email you a link"}},
		{"email off", nil, []string{"Forgot password", "administrator", "cannot send email"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, cleanup := newHarnessCfg(t, 0, "", tc.mod)
			defer cleanup()
			withExpiry(t, h, 31)

			res, body := signInRequest(t, h, "user@test.local")

			require.Equal(t, http.StatusForbidden, res.StatusCode, "%s", body)
			require.Contains(t, string(body), "password_reset_required")
			for _, w := range tc.wants {
				require.Contains(t, string(body), w)
			}
		})
	}
}

// Deciding not to mail settles the row. An error here would be retried by the
// worker for as long as it keeps failing, and the request (which already
// answered 202) cannot see it: this is the only place it shows.
func TestSendPasswordReset_DecidingNotToMailIsNotAFailure(t *testing.T) {
	h, cleanup := harnessWithEmail(t)
	defer cleanup()
	_, err := h.tx.Exec(`UPDATE users SET disabled = true WHERE email = 'user@test.local'`)
	require.NoError(t, err)
	require.NoError(t, h.adminSvc.SetBool(context.Background(), admin.KeySAMLEnabled, true))

	for _, addr := range []string{"nobody@test.local", "user@test.local", "staff@test.local"} {
		err := h.srv.SendPasswordReset(context.Background(), notification.Event{
			Type: notification.EventPasswordResetRequested, Recipient: addr})
		require.NoError(t, err, addr)
	}
	require.Empty(t, h.resetMail.sent)
}
