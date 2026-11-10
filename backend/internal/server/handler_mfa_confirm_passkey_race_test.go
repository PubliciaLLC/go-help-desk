package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/require"
)

// TestMFA_ConfirmReChecksAgainstAPasskeyRegisteredAfterStaging is
// TestMFA_ConfirmReChecksEvenWhenTheSessionSurvives with a passkey as the
// factor that appears. The guard counts passkeys as well as TOTP, but the
// confirm-time re-check had only ever been exercised with a TOTP secret, and
// the service's own already-enrolled check knows nothing about passkeys, so
// the handler's re-check is the only thing between the staged secret and an
// account whose owner has since registered a key.
func TestMFA_ConfirmReChecksAgainstAPasskeyRegisteredAfterStaging(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	mfaOptionalForStaff(t, h)

	// The attacker's password-only session stages a secret while the account
	// has no factor at all.
	attacker := staffPasswordLogin(t, h)
	res, body := attacker.send(t, http.MethodPost, "/api/v1/me/mfa/enroll", nil)
	require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", body)
	var staged map[string]string
	require.NoError(t, json.Unmarshal(body, &staged))

	// The owner then registers a passkey. Written straight to the account so no
	// session is ended, which is what would otherwise refuse the attacker first.
	require.NoError(t, h.passkeyStore.Create(ctx, passkeyFor(h.staffID, "owners-key")))

	code, err := totp.GenerateCode(staged["secret"], time.Now())
	require.NoError(t, err)
	res, body = attacker.send(t, http.MethodPost, "/api/v1/me/mfa/enroll/confirm", map[string]any{"code": code})
	require.Equal(t, http.StatusForbidden, res.StatusCode, "body: %s", body)
	require.Contains(t, string(body), "mfa_required")

	after, err := h.userSvc.GetByID(ctx, h.staffID)
	require.NoError(t, err)
	require.False(t, after.MFAEnabled, "the attacker's secret was adopted next to the owner's passkey")
}
