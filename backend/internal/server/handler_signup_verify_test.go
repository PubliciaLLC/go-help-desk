package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

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
