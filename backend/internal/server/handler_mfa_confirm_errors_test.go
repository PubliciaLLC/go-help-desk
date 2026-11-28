package server_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// failFirstMFA wraps the real store and fails only the write that adopts a
// first TOTP secret.
type failFirstMFA struct{ user.Store }

func (failFirstMFA) SetFirstMFA(context.Context, uuid.UUID, string) (bool, error) {
	return false, errors.New("connection refused")
}

// POST /me/mfa/enroll/confirm: the person's mistakes are 400 invalid_code; a
// fault on the server's side is a 500 and no longer tells them their code was
// wrong (#421).
func TestMFAEnrollConfirm_SplitsMistakesFromFaults(t *testing.T) {
	enrol := func(t *testing.T, h *harness) (*session, string) {
		t.Helper()
		s := signedInUser(t, h)
		res, body := s.send(t, http.MethodPost, "/api/v1/me/mfa/enroll", nil)
		require.Equal(t, http.StatusOK, res.StatusCode, string(body))
		var out struct {
			Secret string `json:"secret"`
		}
		require.NoError(t, json.Unmarshal(body, &out))
		return s, out.Secret
	}

	t.Run("a wrong code is 400 invalid_code", func(t *testing.T) {
		h, cleanup := newHarness(t)
		defer cleanup()
		s, _ := enrol(t, h)
		res, body := s.send(t, http.MethodPost, "/api/v1/me/mfa/enroll/confirm", map[string]any{"code": "000000"})
		require.Equal(t, http.StatusBadRequest, res.StatusCode, string(body))
		require.Contains(t, string(body), "invalid_code")
		require.Contains(t, string(body), "invalid TOTP code", "the message text is unchanged")
	})

	t.Run("nothing started is 400 invalid_code", func(t *testing.T) {
		h, cleanup := newHarness(t)
		defer cleanup()
		s := signedInUser(t, h)
		res, body := s.send(t, http.MethodPost, "/api/v1/me/mfa/enroll/confirm", map[string]any{"code": "000000"})
		require.Equal(t, http.StatusBadRequest, res.StatusCode, string(body))
		require.Contains(t, string(body), "invalid_code")
		require.Contains(t, string(body), "MFA enrollment not started", "the message text is unchanged")
	})

	t.Run("a storage fault is a 500", func(t *testing.T) {
		h, cleanup := newHarness(t)
		defer cleanup()
		s, secret := enrol(t, h)
		code, err := totp.GenerateCode(secret, time.Now())
		require.NoError(t, err)
		h.srv.SetUsersForTest(user.NewService(failFirstMFA{h.userStore}))

		res, body := s.send(t, http.MethodPost, "/api/v1/me/mfa/enroll/confirm", map[string]any{"code": code})
		require.Equal(t, http.StatusInternalServerError, res.StatusCode, string(body))
		require.Contains(t, string(body), "internal_error")
		require.NotContains(t, string(body), "connection refused", "the cause is for the log")
	})

	t.Run("a good code still enrols", func(t *testing.T) {
		h, cleanup := newHarness(t)
		defer cleanup()
		s, secret := enrol(t, h)
		code, err := totp.GenerateCode(secret, time.Now())
		require.NoError(t, err)
		res, body := s.send(t, http.MethodPost, "/api/v1/me/mfa/enroll/confirm", map[string]any{"code": code})
		require.Equal(t, http.StatusNoContent, res.StatusCode, string(body))
	})
}
