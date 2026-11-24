package server_test

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// TestListEndpointsReturnEmptyArrayNotNull pins a contract the whole frontend
// depends on: a list endpoint with no rows answers [], never null.
//
// Nothing enforces it structurally. JSON() encodes whatever it is given, and Go
// marshals a nil slice to null — so a handler returning `var xs []T` on an early
// path emits null, the client calls data.map(...) on it, and the page throws. It
// holds today only because every store builds its result with make(), which is a
// convention rather than a guarantee.
//
// Written after checking whether the frontend's twenty list helpers needed null
// guards. They do not — measured against every endpoint below, not inferred from
// the code. This test is what keeps that true, and is cheaper and more honest
// than twenty defensive `?? []` guards in a client that never receives null.
func TestListEndpointsReturnEmptyArrayNotNull(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	// A random UUID: these are scoped to a parent that does not exist, which is
	// exactly the empty case worth checking.
	orphan := uuid.New().String()

	paths := []string{
		"/api/v1/admin/groups",
		"/api/v1/admin/groups/" + orphan + "/members",
		"/api/v1/admin/groups/" + orphan + "/scopes",
		"/api/v1/admin/oauth-clients",
		"/api/v1/admin/webhooks",
		"/api/v1/admin/canned-responses",
		"/api/v1/admin/custom-fields",
		"/api/v1/admin/tags",
		"/api/v1/admin/sla/policies",
		"/api/v1/tickets",
		"/api/v1/tags",
	}

	for _, p := range paths {
		t.Run(p, func(t *testing.T) {
			res := h.doAsAdmin(t, http.MethodGet, p, nil)
			defer res.Body.Close()
			b, err := io.ReadAll(res.Body)
			require.NoError(t, err)

			require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", b)
			body := strings.TrimSpace(string(b))
			require.NotEqual(t, "null", body,
				"an empty list must serialise as [], not null — the client maps over it")
			require.True(t, strings.HasPrefix(body, "["),
				"expected a JSON array, got: %s", body)
		})
	}
}

// The settings endpoint is the same contract in map form: an instance with no
// settings set must answer {}, not null. getSettings() in the frontend spreads
// the result straight into form state, and null there turns every controlled
// input into an uncontrolled one on the first keystroke.
//
// It holds because the handler builds with make(); as with the lists above,
// that is a convention this test keeps honest rather than a guarantee.
func TestSettingsEndpointReturnsObjectNotNull(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	res := h.doAsAdmin(t, http.MethodGet, "/api/v1/admin/settings", nil)
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	require.NoError(t, err)

	require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", b)
	body := strings.TrimSpace(string(b))
	require.NotEqual(t, "null", body, "an empty settings map must serialise as {}, not null")
	require.True(t, strings.HasPrefix(body, "{"), "expected a JSON object, got: %s", body)
}
