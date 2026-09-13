package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
)

// These tests pin the fix for a full MFA bypass.
//
// MFA enrolment overwrites the stored TOTP secret and returns the new one to
// the caller, and the enrolment route sits outside RequireMFA on purpose — a
// user compelled to enrol has not passed a challenge yet and must still be able
// to finish. Before the fix, EnrollMFA did not distinguish "has not enrolled"
// from "is already protected", so an attacker holding only the victim's
// PASSWORD could log in, re-enrol, and verify with a code of their own making.
// Verified by running the attack end to end: enrol returned 200 with a fresh
// secret, verify returned 204, and /me behind RequireMFA returned 200.
//
// The first test is that attack, and it must fail at the enrolment step.

// session drives a cookie-carrying conversation with the server, the way a
// browser (or an attacker) would.
type session struct {
	h   *harness
	jar []*http.Cookie
}

func (s *session) send(t *testing.T, method, path string, body any) (*http.Response, []byte) {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		require.NoError(t, json.NewEncoder(&buf).Encode(body))
	}
	req := httptest.NewRequest(method, path, &buf)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for _, c := range s.jar {
		req.AddCookie(c)
	}
	rr := httptest.NewRecorder()
	s.h.srv.ServeHTTP(rr, req)
	res := rr.Result()
	if cs := res.Cookies(); len(cs) > 0 {
		s.jar = cs
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	return res, b
}

// sendWithHeaders is send with extra request headers.
func (s *session) sendWithHeaders(t *testing.T, method, path string, body any, headers map[string]string) (*http.Response, []byte) {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		require.NoError(t, json.NewEncoder(&buf).Encode(body))
	}
	req := httptest.NewRequest(method, path, &buf)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	for _, c := range s.jar {
		req.AddCookie(c)
	}
	rr := httptest.NewRecorder()
	s.h.srv.ServeHTTP(rr, req)
	res := rr.Result()
	if cs := res.Cookies(); len(cs) > 0 {
		s.jar = cs
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	return res, b
}

func mfaProtectedStaff(t *testing.T, h *harness) {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeyMFAEnabled, true))
	require.NoError(t, h.adminSvc.SetRaw(ctx, admin.KeyMFAEnforcedRoles, []byte(`["staff","admin"]`)))
	enrollMFA(t, ctx, h.userSvc, h.staffID)
}

func TestMFA_PasswordAloneCannotReEnrol(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	mfaProtectedStaff(t, h)

	s := &session{h: h}

	// The attacker has the password and nothing else.
	res, body := s.send(t, http.MethodPost, "/api/v1/auth/local/login", map[string]any{
		"email": "staff@test.local", "password": "password",
	})
	require.Equal(t, http.StatusOK, res.StatusCode)
	var login map[string]any
	require.NoError(t, json.Unmarshal(body, &login))
	require.True(t, login["mfa_needed"].(bool), "precondition: the account is MFA-protected")

	// The bypass: re-enrol from a session that has not passed the challenge.
	res, body = s.send(t, http.MethodGet, "/api/v1/me/mfa/enroll", nil)
	require.Equal(t, http.StatusForbidden, res.StatusCode,
		"re-enrolment from an MFA-unverified session must be refused; got body %s", body)
	require.NotContains(t, string(body), "secret",
		"the response must not hand out a TOTP secret")

	// And the session must still be locked out of MFA-protected routes.
	res, _ = s.send(t, http.MethodGet, "/api/v1/me", nil)
	require.NotEqual(t, http.StatusOK, res.StatusCode,
		"a password-only session must not reach an MFA-protected endpoint")
}

// The victim's existing authenticator must keep working — a refused attack that
// still rotated the secret would lock the victim out, which is most of the harm.
func TestMFA_RefusedReEnrolmentLeavesTheSecretIntact(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	mfaProtectedStaff(t, h)

	before, err := h.userSvc.GetByID(ctx, h.staffID)
	require.NoError(t, err)

	s := &session{h: h}
	s.send(t, http.MethodPost, "/api/v1/auth/local/login", map[string]any{
		"email": "staff@test.local", "password": "password",
	})
	s.send(t, http.MethodGet, "/api/v1/me/mfa/enroll", nil)

	after, err := h.userSvc.GetByID(ctx, h.staffID)
	require.NoError(t, err)
	require.Equal(t, before.MFASecret, after.MFASecret,
		"a refused re-enrolment must not rotate the victim's secret")
	require.True(t, after.MFAEnabled)
}

// The legitimate path must survive the fix: someone who still holds their
// current authenticator can move to a new one without an administrator.
func TestMFA_HolderOfCurrentAuthenticatorCanRotate(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	mfaProtectedStaff(t, h)

	original, err := h.userSvc.GetByID(ctx, h.staffID)
	require.NoError(t, err)

	s := &session{h: h}
	s.send(t, http.MethodPost, "/api/v1/auth/local/login", map[string]any{
		"email": "staff@test.local", "password": "password",
	})

	// Pass the challenge with the CURRENT authenticator.
	code, err := totp.GenerateCode(original.MFASecret, time.Now())
	require.NoError(t, err)
	res, body := s.send(t, http.MethodPost, "/api/v1/auth/local/mfa/verify", map[string]any{"code": code})
	require.Equal(t, http.StatusNoContent, res.StatusCode, "body: %s", body)

	// Now rotating to a new authenticator is allowed.
	res, body = s.send(t, http.MethodGet, "/api/v1/me/mfa/enroll", nil)
	require.Equal(t, http.StatusOK, res.StatusCode,
		"a fully verified user must still be able to rotate authenticators; body %s", body)

	var enroll map[string]string
	require.NoError(t, json.Unmarshal(body, &enroll))
	require.NotEmpty(t, enroll["secret"])
	require.NotEqual(t, original.MFASecret, enroll["secret"], "rotation must issue a new secret")
}
