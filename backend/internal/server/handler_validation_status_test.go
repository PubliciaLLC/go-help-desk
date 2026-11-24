package server_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
)

// A mistyped email address is the caller's mistake, and the answer has to say
// so. Adding validation without a matching error mapping turned every one of
// these into 500 "an internal error occurred", logged at error level — a
// signup form that rejects a typo by reporting a server fault, and a pager
// that goes off for it.
func TestSignup_MalformedEmailIs400NotAServerError(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	require.NoError(t, h.adminSvc.SetBool(context.Background(), admin.KeySelfSignupEnabled, true))

	for _, email := range []string{
		"not-an-address",
		`"Call 555-0100" <victim@test.local>`,
		"victim@test.local\r\nBcc: attacker@evil.test",
		"a@test.local, b@test.local",
	} {
		t.Run(email, func(t *testing.T) {
			res := h.doUnauth(t, http.MethodPost, "/api/v1/auth/signup",
				map[string]any{"email": email, "display_name": "X", "password": "a-long-enough-password"})
			defer res.Body.Close()
			require.Equal(t, http.StatusBadRequest, res.StatusCode)
		})
	}
}

// Same for an administrator editing a user. This one also covers accounts that
// predate the validation: their stored address may not pass it, and the
// administrator has to be told what is wrong rather than shown a fault.
func TestAdminUserUpdate_MalformedEmailIs400NotAServerError(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	res := h.doAsAdmin(t, http.MethodPatch, "/api/v1/admin/users/"+h.userID.String(),
		map[string]any{"email": "not-an-address"})
	defer res.Body.Close()
	require.Equal(t, http.StatusBadRequest, res.StatusCode)
}
