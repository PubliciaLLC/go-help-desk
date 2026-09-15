package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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

// mintLegacyKey seeds a credential with no scopes directly in the store.
//
// The API refuses to create one — an empty scope list produces a credential
// that can do nothing, so it is a 400 rather than a silent dead key. But
// exactly that shape exists in every database upgraded from 1.1.1, where the
// admin UI sent an empty list. These tests are about what happens to those
// credentials, so they have to be seeded the way the upgrade leaves them.
func mintLegacyKey(t *testing.T, h *harness) string {
	t.Helper()
	raw, _, err := auth.GenerateToken()
	require.NoError(t, err)
	// Hash AFTER prefixing, as handleCreateAPIKey does. Hashing the bare token
	// stores a digest of something the client never sends, so the key simply
	// never authenticates — a 401 that looks like a scope refusal.
	raw = "GHD_" + raw
	hashed := auth.HashToken(raw)

	require.NoError(t, h.authStore.CreateAPIKey(context.Background(), auth.APIKey{
		ID:          uuid.New(),
		Name:        "pre-1.2.0 key",
		HashedToken: hashed,
		UserID:      h.adminID,
		// An empty array, not nil: the 1.1.1 admin UI sent `scopes: []`, which
		// is what an upgraded database actually contains. nil marshals to SQL
		// NULL and violates the not-null constraint — that was issue #143.
		Scopes:    []string{},
		CreatedAt: time.Now(),
	}))
	return raw
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

	none := mintLegacyKey(t, h)

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

// A reporting user cannot mint a credential at all, so the escalation route of
// "issue myself an admin-scoped key" is closed at the door.
//
// This is RequireRole doing its job, not the scope ordering — the ordering
// property (a scope never grants what the role denies) is pinned at the
// middleware layer by TestRequireScope_CannotGrantBeyondTheRole, where both
// checks can be composed directly. Named for what it actually asserts.
func TestScopeEnforcement_ReportingUsersCannotMintCredentials(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	resp := h.doAsUser(t, http.MethodPost, "/api/v1/admin/api-keys", map[string]any{
		"name": "escalation-attempt", "scopes": []string{"users:write"},
	})
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
}

// The scope catalogue is itself behind a scope. It lists what every credential
// in the system can be granted, which is reconnaissance worth refusing to a
// credential that was granted nothing.
func TestScopeEnforcement_CatalogueIsItselfScoped(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	none := mintLegacyKey(t, h)
	resp := withKey(t, h, none, http.MethodGet, "/api/v1/admin/scopes", nil)
	require.Equal(t, http.StatusForbidden, resp.StatusCode)

	withCreds := mintKey(t, h, []string{"credentials:read"})
	resp = withKey(t, h, withCreds, http.MethodGet, "/api/v1/admin/scopes", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode,
		"credentials:read must be enough to build the picker")
}

// The reference endpoints a ticket client needs are covered by tickets:read,
// not left open. They were reachable by a credential with no scopes at all.
func TestScopeEnforcement_ReferenceEndpointsAreScoped(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	none := mintLegacyKey(t, h)
	for _, path := range []string{
		"/api/v1/tags", "/api/v1/statuses", "/api/v1/admin/security-warnings",
	} {
		t.Run(path, func(t *testing.T) {
			resp := withKey(t, h, none, http.MethodGet, path, nil)
			require.Equal(t, http.StatusForbidden, resp.StatusCode,
				"a credential with no scopes must not read %s", path)
		})
	}

	reader := mintKey(t, h, []string{"tickets:read"})
	for _, path := range []string{"/api/v1/tags", "/api/v1/statuses"} {
		t.Run("allowed "+path, func(t *testing.T) {
			resp := withKey(t, h, reader, http.MethodGet, path, nil)
			require.Equal(t, http.StatusOK, resp.StatusCode,
				"tickets:read must cover the reference data a ticket client needs")
		})
	}
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

// The picker is built from this, so it must match what the server enforces
// exactly: a scope the UI offers but the server rejects fails at creation, and
// one the server knows but the UI omits is silently unreachable.
func TestScopeCatalogue_MatchesWhatIsEnforced(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	resp := h.doAsAdmin(t, http.MethodGet, "/api/v1/admin/scopes", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var got []struct {
		Scope, Resource, Action string
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	require.NotEmpty(t, got)

	served := make([]string, len(got))
	for i, s := range got {
		served[i] = s.Scope
		require.Equal(t, s.Resource+":"+s.Action, s.Scope,
			"the parts must compose into the scope the UI will send")
	}

	// Every advertised scope must be one a credential can actually be created
	// with, and there must be no wildcard among them.
	resp = h.doAsAdmin(t, http.MethodPost, "/api/v1/admin/api-keys",
		map[string]any{"name": "every-scope", "scopes": served})
	require.Equal(t, http.StatusCreated, resp.StatusCode,
		"every scope the catalogue advertises must be accepted at creation")

	for _, s := range served {
		require.NotContains(t, s, "*", "the catalogue must not advertise a wildcard")
	}
}

// Omitting scopes reached the database as NULL and came back as an opaque 500 —
// on the exact call the upgrade notes tell every operator to make, since every
// pre-1.2.0 credential has to be re-issued. The admin UI always sends the field,
// so this only ever bit people using curl, Terraform or a script.
//
// A 400 rather than a default, because with deny-by-default an empty list
// creates a credential that can do nothing and the caller would not find out
// until the integration started returning 403.
func TestCredentialCreation_RequiresScopes(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	for _, path := range []string{"/api/v1/admin/api-keys", "/api/v1/admin/oauth-clients"} {
		t.Run(path, func(t *testing.T) {
			t.Run("omitted entirely", func(t *testing.T) {
				resp := h.doAsAdmin(t, http.MethodPost, path, map[string]any{"name": "no-scopes"})
				require.Equal(t, http.StatusBadRequest, resp.StatusCode,
					"an omitted scopes field must be a 400, not a 500")

				var body struct {
					Error struct{ Code, Message string } `json:"error"`
				}
				require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
				require.Equal(t, "scopes_required", body.Error.Code)
				require.Contains(t, body.Error.Message, "/admin/scopes",
					"the error should say where to find the scope list")
			})

			t.Run("present but empty", func(t *testing.T) {
				resp := h.doAsAdmin(t, http.MethodPost, path,
					map[string]any{"name": "empty-scopes", "scopes": []string{}})
				require.Equal(t, http.StatusBadRequest, resp.StatusCode)
			})

			t.Run("with scopes still works", func(t *testing.T) {
				resp := h.doAsAdmin(t, http.MethodPost, path,
					map[string]any{"name": "fine", "scopes": []string{"tickets:read"}})
				require.Equal(t, http.StatusCreated, resp.StatusCode)
			})
		})
	}
}
