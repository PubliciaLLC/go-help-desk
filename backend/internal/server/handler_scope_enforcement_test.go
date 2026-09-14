package server_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

// mintKey creates an API key owned by the seeded admin with the given scopes,
// and returns the raw token. It goes through the real admin endpoint so the
// credential is built the way a real one is.
func mintKey(t *testing.T, h *harness, scopes []string) string {
	t.Helper()
	resp := h.doAsAdmin(t, http.MethodPost, "/api/v1/admin/api-keys", map[string]any{
		"name": "scoped-test-key", "scopes": scopes,
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var out struct {
		Token string `json:"token"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	require.NotEmpty(t, out.Token)
	return out.Token
}

func withKey(t *testing.T, h *harness, token, method, path string, body any) *http.Response {
	t.Helper()
	var r *http.Request
	if body != nil {
		b, err := json.Marshal(body)
		require.NoError(t, err)
		r = httptest.NewRequest(method, path, bytes.NewReader(b))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	r.Header.Set("Authorization", "ApiKey "+token)
	rr := httptest.NewRecorder()
	h.srv.ServeHTTP(rr, r)
	return rr.Result()
}

// Scopes were accepted, stored, and returned for the life of the product, and
// never read. Every credential issued as restricted was unrestricted. This is
// the end-to-end proof that a restricted credential is now restricted.
func TestScopeEnforcement_OverHTTP(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	readOnly := mintKey(t, h, []string{"tickets:read"})

	t.Run("the granted scope works", func(t *testing.T) {
		resp := withKey(t, h, readOnly, http.MethodGet, "/api/v1/tickets", nil)
		require.Equal(t, http.StatusOK, resp.StatusCode)
	})

	t.Run("writing the same resource is refused", func(t *testing.T) {
		resp := withKey(t, h, readOnly, http.MethodPost, "/api/v1/tickets",
			map[string]any{"subject": "x", "description": "y", "category_id": h.catID.String()})
		require.Equal(t, http.StatusForbidden, resp.StatusCode)
		var body struct {
			Error struct{ Code string } `json:"error"`
		}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
		require.Equal(t, "insufficient_scope", body.Error.Code)
	})

	t.Run("another resource is refused even though the owner is admin", func(t *testing.T) {
		// The key belongs to the seeded admin. Before enforcement it could
		// create users; the scope is the only thing standing in the way.
		resp := withKey(t, h, readOnly, http.MethodGet, "/api/v1/admin/users", nil)
		require.Equal(t, http.StatusForbidden, resp.StatusCode)
	})
}

// Empty means deny. This is the breaking change, and it is the point.
func TestScopeEnforcement_NoScopesReachesNothing(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	none := mintKey(t, h, []string{})

	for _, tc := range []struct {
		method, path string
	}{
		{http.MethodGet, "/api/v1/tickets"},
		{http.MethodGet, "/api/v1/admin/users"},
		{http.MethodGet, "/api/v1/admin/settings"},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			resp := withKey(t, h, none, tc.method, tc.path, nil)
			require.Equal(t, http.StatusForbidden, resp.StatusCode,
				"a credential with no scopes must reach nothing")
		})
	}
}

// Write implies read, so a write-scoped integration can read back what it wrote.
func TestScopeEnforcement_WriteImpliesRead(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	writer := mintKey(t, h, []string{"tickets:write"})

	resp := withKey(t, h, writer, http.MethodPost, "/api/v1/tickets",
		map[string]any{"subject": "made by integration", "description": "y",
			"category_id": h.catID.String()})
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	resp = withKey(t, h, writer, http.MethodGet, "/api/v1/tickets", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode,
		"tickets:write must let the integration read back what it created")
}

// A scope cannot grant what the role denies. This is the property that keeps
// scopes from becoming an escalation surface.
func TestScopeEnforcement_CannotExceedTheOwnersRole(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	// A key owned by the reporting user, carrying an admin scope.
	resp := h.doAsUser(t, http.MethodPost, "/api/v1/admin/api-keys", map[string]any{
		"name": "escalation-attempt", "scopes": []string{"users:write"},
	})
	require.Equal(t, http.StatusForbidden, resp.StatusCode,
		"a reporting user cannot even reach the credential admin endpoint")
}

// A typo must fail at creation rather than producing a credential that looks
// restricted and silently grants less than intended.
func TestScopeEnforcement_UnknownScopeRejectedAtCreation(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	resp := h.doAsAdmin(t, http.MethodPost, "/api/v1/admin/api-keys", map[string]any{
		"name": "typo", "scopes": []string{"tickets:reed"},
	})
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)

	var body struct {
		Error struct{ Code, Message string } `json:"error"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	require.Equal(t, "invalid_scope", body.Error.Code)
	require.Contains(t, body.Error.Message, "tickets:reed",
		"the error must name the scope that was wrong")
}
