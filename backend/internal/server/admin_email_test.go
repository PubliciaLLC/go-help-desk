package server_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// An address typed with a capital letter does not lock the account out.
//
// Login lowercases before it looks anyone up. Create, SAML and OIDC all
// normalise on the way in; the admin edit stored whatever was typed. So an
// administrator correcting somebody's name and touching the email field —
// "Staff@Test.Local" — left an account that could no longer log in under
// either spelling, with nothing to say why.
func TestAdminUpdateUser_NormalisesTheEmail(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	res := h.doAsAdmin(t, http.MethodPatch, "/api/v1/admin/users/"+h.staffID.String(),
		map[string]any{"email": "  Staff@Test.Local  "})
	defer res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)

	login := h.do(t, http.MethodPost, "/api/v1/auth/local/login",
		map[string]any{"email": "staff@test.local", "password": "password"})
	defer login.Body.Close()
	require.Equal(t, http.StatusOK, login.StatusCode,
		"the account can no longer be logged into after an ordinary admin edit")
}
