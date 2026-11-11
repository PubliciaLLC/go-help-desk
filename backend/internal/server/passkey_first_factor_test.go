package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/webauthn"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// #617: a passkey registration writes the account's FIRST factor under a lock,
// and a session that has proved a factor writes with no such condition.
//
// The interleaving itself is pinned at the store, against a real pool
// (userstore's first_factor_race_test.go). What this pins is the handler's half:
// that it asks for the conditional write, and answers its refusal the way the
// TOTP confirm does.

// racingStore puts a TOTP on the account just before the credential is written,
// as a TOTP confirm that won the race would have.
type racingStore struct {
	webauthn.Store
	beforeWrite func()
}

func (r racingStore) CreateFirstFactor(ctx context.Context, c webauthn.Credential) error {
	r.beforeWrite()
	return r.Store.CreateFirstFactor(ctx, c)
}

// registerKey runs register/start and register/finish with a fresh software key.
func registerKey(t *testing.T, s *session) (*http.Response, []byte) {
	t.Helper()
	res, body := s.send(t, http.MethodPost, "/api/v1/me/passkeys/register/start", nil)
	require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", body)
	var opts struct {
		PublicKey struct {
			Challenge string `json:"challenge"`
		} `json:"publicKey"`
	}
	require.NoError(t, json.Unmarshal(body, &opts))

	key := testutil.NewSoftKey(t, "help.test.local", "https://help.test.local")
	return s.send(t, http.MethodPost, "/api/v1/me/passkeys/register/finish",
		json.RawMessage(key.Register(t, opts.PublicKey.Challenge)))
}

func TestPasskeyRegister_FirstFactorOnAnUnprotectedAccountIsWritten(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	mfaOptionalForStaff(t, h)

	res, body := registerKey(t, staffPasswordLogin(t, h))
	require.Equal(t, http.StatusNoContent, res.StatusCode, "body: %s", body)
	n, err := h.passkeyStore.CountForUser(context.Background(), h.staffID)
	require.NoError(t, err)
	require.EqualValues(t, 1, n)
}

func TestPasskeyRegister_LosesTheRaceToATOTPAndWritesNothing(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	mfaOptionalForStaff(t, h)
	h.srv.SetPasskeyStoreForTest(racingStore{
		Store:       h.passkeyStore,
		beforeWrite: func() { enrollMFA(t, ctx, h.userSvc, h.staffID) },
	})

	res, body := registerKey(t, staffPasswordLogin(t, h))
	require.Equal(t, http.StatusForbidden, res.StatusCode, "body: %s", body)
	require.Contains(t, string(body), "mfa_required")

	n, err := h.passkeyStore.CountForUser(ctx, h.staffID)
	require.NoError(t, err)
	require.Zero(t, n, "a key was written next to a TOTP that won the race")
}

// A session that proved the account's factor is adding a second one on purpose.
func TestPasskeyRegister_ASessionThatProvedAFactorMayAddAnother(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	mfaOptionalForStaff(t, h)
	secret := enrolTOTP(t, staffPasswordLogin(t, h))

	s := staffPasswordLogin(t, h)
	code, err := totp.GenerateCode(secret, time.Now())
	require.NoError(t, err)
	res, body := s.send(t, http.MethodPost, "/api/v1/auth/local/mfa/verify", map[string]any{"code": code})
	require.Equal(t, http.StatusNoContent, res.StatusCode, "body: %s", body)

	res, body = registerKey(t, s)
	require.Equal(t, http.StatusNoContent, res.StatusCode, "body: %s", body)
}
