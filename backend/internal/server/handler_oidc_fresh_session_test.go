package server_test

import (
	"context"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// The callback used to write the session it had loaded, with only the identity
// fields replaced. Whatever the browser held before (a password sign-in that
// owes a password change, a staged TOTP secret, a pending MFA deadline) rode
// along onto the identity the IdP vouched for, and /me answered 403
// password_change_required to someone who owed nothing.
func TestOIDCCallback_DoesNotInheritTheBrowsersPreviousSessionFlags(t *testing.T) {
	oh, cleanup := newOIDCHarness(t)
	defer cleanup()
	ctx := context.Background()

	// A federated account that owes nothing.
	fed, err := oh.userSvc.Create(ctx, user.CreateUserInput{
		Email:       "federated@test.local",
		DisplayName: "Federated",
		Role:        user.RoleUser,
		OIDCSubject: "federated-sub",
	})
	require.NoError(t, err)

	// A different account on the same browser is flagged and signs in with its password.
	_, err = oh.tx.ExecContext(ctx, `UPDATE users SET must_change_password = true WHERE id = $1`, oh.staffID)
	require.NoError(t, err)
	s := &session{h: oh.harness}
	res, body := s.send(t, http.MethodPost, "/api/v1/auth/local/login",
		map[string]any{"email": "staff@test.local", "password": "password"})
	require.Equal(t, http.StatusOK, res.StatusCode, "%s", body)
	require.Contains(t, string(body), `"password_change_required":true`)

	// Plant the other staged state the callback must not carry over.
	rewrite(t, s, func(sd *auth.SessionData) {
		sd.PendingMFASecret = "STAGEDSECRET"
		sd.PendingPasskey = `{"staged":true}`
		sd.PendingUntil = time.Now().Add(time.Hour)
	})

	// OIDC on the same cookies.
	state, nonce, cookies := oh.startOIDCLoginFull(t, s.jar)
	oh.idp.staged = oh.idp.mint(t, fakeOIDCClaims{
		Subject:       "federated-sub",
		Email:         "federated@test.local",
		EmailVerified: true,
		Name:          "Federated",
		Nonce:         nonce,
	})
	resp := oh.callback(t, url.Values{"state": {state}, "code": {"the-code"}}, cookies)
	require.Equal(t, http.StatusSeeOther, resp.StatusCode)
	require.Equal(t, "/", resp.Header.Get("Location"))

	got, ok := whoami(t, oh.harness, resp.Cookies())
	require.True(t, ok, "the OIDC sign-in inherited the previous session's password_change_required")
	require.Equal(t, fed.ID, got.ID)

	after := &session{h: oh.harness, jar: resp.Cookies()}
	_, sd, ok := storedSession(t, after)
	require.True(t, ok)
	require.Equal(t, auth.SessionData{
		UserID:    fed.ID,
		Role:      user.RoleUser,
		MFAPassed: true,
	}, sd, "the session must be built fresh, carrying nothing from the one it replaced")
}
