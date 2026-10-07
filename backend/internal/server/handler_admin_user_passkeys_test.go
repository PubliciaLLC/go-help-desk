package server_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// The admin page shows "Reset MFA" only for an account that has a factor to
// reset. Before #307 it keyed off the TOTP flag alone, so a passkey-only
// account had no button at all, and the fix to what the button clears would
// have been unreachable for exactly the account it was for. The detail
// response says how many passkeys the account holds so the page can tell.
func TestAdminGetUser_ReportsPasskeyCount(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	get := func(t *testing.T) int {
		t.Helper()
		res := h.doAsAdmin(t, http.MethodGet, "/api/v1/admin/users/"+h.userID.String(), nil)
		defer res.Body.Close()
		body, _ := io.ReadAll(res.Body)
		require.Equal(t, http.StatusOK, res.StatusCode, "%s", body)
		var out struct {
			PasskeyCount *int `json:"passkey_count"`
		}
		require.NoError(t, json.Unmarshal(body, &out))
		require.NotNil(t, out.PasskeyCount, "passkey_count missing from the admin detail: %s", body)
		return *out.PasskeyCount
	}

	require.Equal(t, 0, get(t))
	require.NoError(t, h.passkeyStore.Create(ctx, passkeyFor(h.userID, "one")))
	require.NoError(t, h.passkeyStore.Create(ctx, passkeyFor(h.userID, "two")))
	require.Equal(t, 2, get(t))
}
