package server_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// Self-service API keys (#450): /me/api-keys. A key acts as its owner, so the
// requests below that matter most are the ones that must be refused.

const meKeys = "/api/v1/me/api-keys"

func readAll(t *testing.T, res *http.Response) []byte {
	t.Helper()
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	return b
}

func errCode(t *testing.T, body []byte) string {
	t.Helper()
	var e struct{ Error struct{ Code string } }
	require.NoError(t, json.Unmarshal(body, &e), "body: %s", body)
	return e.Error.Code
}

type issuedKey struct {
	ID    uuid.UUID `json:"id"`
	Token string    `json:"token"`
}

// mintMine creates a key through /me/api-keys on the given session and returns it.
func (h *harness) mintMine(t *testing.T, cookie string, scopes ...string) issuedKey {
	t.Helper()
	res := h.doSession(t, cookie, http.MethodPost, meKeys, map[string]any{"name": "mine", "scopes": scopes})
	b := readAll(t, res)
	require.Equal(t, http.StatusCreated, res.StatusCode, "body: %s", b)
	var k issuedKey
	require.NoError(t, json.Unmarshal(b, &k))
	require.NotEmpty(t, k.Token)
	return k
}

func (h *harness) allowStaffKeys(t *testing.T) {
	t.Helper()
	require.NoError(t, h.adminSvc.SetBool(context.Background(), admin.KeyStaffCanCreateAPIKeys, true))
}

func (h *harness) auditFor(t *testing.T, keyID uuid.UUID) []map[string]any {
	t.Helper()
	entries, err := h.auditStore.ListByEntity(context.Background(), "api_key", keyID, 20, 0)
	require.NoError(t, err)
	var out []map[string]any
	for _, e := range entries {
		m := map[string]any{"action": e.Action, "after": e.After}
		if e.ActorID != nil {
			m["actor"] = *e.ActorID
		}
		out = append(out, m)
	}
	return out
}

func TestMeAPIKeys_StaffNeedsTheSetting(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	cookie := h.session(t, asStaff)

	t.Run("off: create is refused, with a reason", func(t *testing.T) {
		res := h.doSession(t, cookie, http.MethodPost, meKeys,
			map[string]any{"name": "x", "scopes": []string{"tickets:read"}})
		b := readAll(t, res)
		require.Equal(t, http.StatusForbidden, res.StatusCode, "body: %s", b)
		require.Equal(t, "api_keys_not_allowed", errCode(t, b))
	})

	t.Run("on: staff creates a key", func(t *testing.T) {
		h.allowStaffKeys(t)
		h.mintMine(t, cookie, "tickets:read")
	})
}

func TestMeAPIKeys_AdministratorsDoNotNeedTheSetting(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	cookie := h.session(t, asAdmin)

	k := h.mintMine(t, cookie, "users:write", "tickets:read")

	// It is the administrator's own key, and it works as one.
	res := requestWithKey(t, h, k.Token, "/api/v1/admin/users")
	res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)
}

func TestMeAPIKeys_RequestersNeverGetKeys(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	h.allowStaffKeys(t) // on, and still not for a requester
	cookie := h.session(t, asReporter)

	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, meKeys},
		{http.MethodPost, meKeys},
		{http.MethodDelete, meKeys + "/" + uuid.NewString()},
	} {
		res := h.doSession(t, cookie, tc.method, tc.path, map[string]any{"name": "x", "scopes": []string{"tickets:read"}})
		b := readAll(t, res)
		require.Equal(t, http.StatusForbidden, res.StatusCode, "%s %s: %s", tc.method, tc.path, b)
	}
}

// The key is the staff member's: it works, it acts as staff, and it reaches no
// administrative route whatever scope it carries.
func TestMeAPIKeys_AStaffKeyActsAsStaff(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	h.allowStaffKeys(t)
	k := h.mintMine(t, h.session(t, asStaff), "tickets:read", "audit:read", "groups:read")

	res := requestWithKey(t, h, k.Token, "/api/v1/me")
	b := readAll(t, res)
	require.Equal(t, http.StatusOK, res.StatusCode)
	var me struct {
		Role string `json:"role"`
		ID   string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(b, &me))
	require.Equal(t, "staff", me.Role)
	require.Equal(t, h.staffID.String(), me.ID)

	for _, path := range []string{"/api/v1/admin/users", "/api/v1/admin/api-keys", "/api/v1/admin/settings"} {
		res := requestWithKey(t, h, k.Token, path)
		res.Body.Close()
		require.Equal(t, http.StatusForbidden, res.StatusCode, "%s must stay closed to a staff key", path)
	}
}

func TestMeAPIKeys_ScopesNeverExceedTheOwnersRole(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	h.allowStaffKeys(t)
	staff := h.session(t, asStaff)

	cases := []struct {
		name   string
		scopes []string
		want   int
		code   string
	}{
		{"a staff scope", []string{"tickets:write"}, http.StatusCreated, ""},
		{"an admin-only scope", []string{"users:write"}, http.StatusBadRequest, "invalid_scope"},
		{"admin-only read", []string{"settings:read"}, http.StatusBadRequest, "invalid_scope"},
		{"credentials", []string{"credentials:write"}, http.StatusBadRequest, "invalid_scope"},
		{"one admin-only scope among allowed ones", []string{"tickets:read", "webhooks:read"}, http.StatusBadRequest, "invalid_scope"},
		{"a scope that does not exist", []string{"sprockets:read"}, http.StatusBadRequest, "invalid_scope"},
		{"none", nil, http.StatusBadRequest, "scopes_required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := h.doSession(t, staff, http.MethodPost, meKeys, map[string]any{"name": "x", "scopes": tc.scopes})
			b := readAll(t, res)
			require.Equal(t, tc.want, res.StatusCode, "body: %s", b)
			if tc.code != "" {
				require.Equal(t, tc.code, errCode(t, b))
			}
		})
	}

	// Nothing was stored for the refusals.
	keys, err := h.authStore.ListByUser(context.Background(), h.staffID)
	require.NoError(t, err)
	var mine int
	for _, k := range keys {
		if k.Name == "x" {
			mine++
		}
	}
	require.Equal(t, 1, mine, "only the accepted request made a key")
}

func TestMeAPIKeys_ScopeListIsTheCallersRole(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	h.allowStaffKeys(t)

	scopesOf := func(cookie string) []string {
		res := h.doSession(t, cookie, http.MethodGet, meKeys+"/scopes", nil)
		b := readAll(t, res)
		require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", b)
		var list []struct {
			Scope string `json:"scope"`
		}
		require.NoError(t, json.Unmarshal(b, &list))
		out := []string{}
		for _, s := range list {
			out = append(out, s.Scope)
		}
		return out
	}
	require.ElementsMatch(t, []string{"tickets:read", "tickets:write", "groups:read", "audit:read"}, scopesOf(h.session(t, asStaff)))
	require.Contains(t, scopesOf(h.session(t, asAdmin)), "users:write")
}

func TestMeAPIKeys_Expiry(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	h.allowStaffKeys(t)
	staff := h.session(t, asStaff)

	post := func(exp any) (int, []byte) {
		res := h.doSession(t, staff, http.MethodPost, meKeys,
			map[string]any{"name": "e", "scopes": []string{"tickets:read"}, "expires_at": exp})
		b := readAll(t, res)
		return res.StatusCode, b
	}

	future := time.Now().Add(48 * time.Hour).UTC().Truncate(time.Second)
	code, b := post(future.Format(time.RFC3339))
	require.Equal(t, http.StatusCreated, code, "body: %s", b)
	var got struct {
		ExpiresAt time.Time `json:"expires_at"`
	}
	require.NoError(t, json.Unmarshal(b, &got))
	require.True(t, future.Equal(got.ExpiresAt))

	code, _ = post("2020-01-01T00:00:00Z")
	require.Equal(t, http.StatusBadRequest, code, "an expiry in the past")
	code, _ = post("next tuesday")
	require.Equal(t, http.StatusBadRequest, code, "an unreadable expiry")
}

func TestMeAPIKeys_ListShowsOnlyYourOwnAndNeverTheSecret(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	h.allowStaffKeys(t)
	staff := h.session(t, asStaff)
	mine := h.mintMine(t, staff, "tickets:read")
	// An administrator's key, which the staff member must not see.
	h.mintMine(t, h.session(t, asAdmin), "tickets:read")

	res := h.doSession(t, staff, http.MethodGet, meKeys, nil)
	b := readAll(t, res)
	require.Equal(t, http.StatusOK, res.StatusCode)
	var list []map[string]any
	require.NoError(t, json.Unmarshal(b, &list))
	var names []string
	var ids []string
	for _, k := range list {
		ids = append(ids, k["id"].(string))
		names = append(names, k["name"].(string))
		require.NotContains(t, k, "token")
		require.NotContains(t, k, "hashed_token")
		require.Equal(t, h.staffID.String(), k["user_id"])
	}
	// The harness gave staff one key of its own ("test-key") besides this one.
	require.ElementsMatch(t, []string{"test-key", "mine"}, names)
	require.Contains(t, ids, mine.ID.String())
	require.NotContains(t, string(b), mine.Token)
}

func TestMeAPIKeys_Revoke(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	h.allowStaffKeys(t)
	staff := h.session(t, asStaff)
	k := h.mintMine(t, staff, "tickets:read")

	res := requestWithKey(t, h, k.Token, "/api/v1/tickets")
	res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)

	res = h.doSession(t, staff, http.MethodDelete, meKeys+"/"+k.ID.String(), nil)
	res.Body.Close()
	require.Equal(t, http.StatusNoContent, res.StatusCode)

	res = requestWithKey(t, h, k.Token, "/api/v1/tickets")
	res.Body.Close()
	require.Equal(t, http.StatusUnauthorized, res.StatusCode, "a revoked key stops working at once")
}

// Another person's key answers exactly as a key that does not exist, and is
// still there afterwards.
func TestMeAPIKeys_CannotTouchSomeoneElsesKey(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	h.allowStaffKeys(t)
	adminKey := h.mintMine(t, h.session(t, asAdmin), "tickets:read")
	staff := h.session(t, asStaff)

	res := h.doSession(t, staff, http.MethodDelete, meKeys+"/"+adminKey.ID.String(), nil)
	b := readAll(t, res)
	require.Equal(t, http.StatusNotFound, res.StatusCode, "body: %s", b)

	res = h.doSession(t, staff, http.MethodDelete, meKeys+"/"+uuid.NewString(), nil)
	b2 := readAll(t, res)
	require.Equal(t, http.StatusNotFound, res.StatusCode)
	require.JSONEq(t, string(replaceID(b, adminKey.ID)), string(replaceID(b2, uuid.Nil)),
		"the two 404s must not differ by anything but the id they echo")

	r := requestWithKey(t, h, adminKey.Token, "/api/v1/tickets")
	r.Body.Close()
	require.Equal(t, http.StatusOK, r.StatusCode, "the administrator's key is untouched")
}

// replaceID blanks the id a 404 echoes so two of them can be compared.
func replaceID(b []byte, id uuid.UUID) []byte {
	var e map[string]map[string]string
	_ = json.Unmarshal(b, &e)
	e["error"]["message"] = "x"
	out, _ := json.Marshal(e)
	return out
}

// An API key or OAuth client cannot mint, list or revoke keys: it would turn a
// leaked credential into as many more as the attacker likes, and revoking the
// first would not remove them.
func TestMeAPIKeys_KeysCannotMintKeys(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	h.allowStaffKeys(t)

	body := map[string]any{"name": "x", "scopes": []string{"tickets:read"}}
	staffKey := func(method, path string, b any) *http.Response { return h.do(t, method, path, b) }
	client := h.newServiceClient(t, "svc", "tickets:read")

	for _, tc := range []struct {
		name string
		do   func(method, path string, b any) *http.Response
	}{
		{"an API key", staffKey},
		{"the administrator's API key", func(m, p string, b any) *http.Response { return h.doAsAdmin(t, m, p, b) }},
		{"an OAuth client", func(m, p string, b any) *http.Response { return h.doBearer(t, client.Token, m, p, b) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, r := range []struct {
				method, path string
				body         any
			}{
				{http.MethodPost, meKeys, body},
				{http.MethodGet, meKeys, nil},
				{http.MethodGet, meKeys + "/scopes", nil},
				{http.MethodDelete, meKeys + "/" + uuid.NewString(), nil},
			} {
				res := tc.do(r.method, r.path, r.body)
				b := readAll(t, res)
				require.Equal(t, http.StatusForbidden, res.StatusCode, "%s %s: %s", r.method, r.path, b)
				require.Equal(t, "session_required", errCode(t, b))
			}
		})
	}
}

// A session that has passed the password but not the second factor reaches
// nothing here.
func TestMeAPIKeys_RequiresMFA(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	h.allowStaffKeys(t)
	half := h.sessionCookieFor(t, auth.SessionData{
		UserID: h.staffID, Role: user.RoleStaff, MFAPassed: false,
		PendingUntil: time.Now().Add(time.Hour),
	})
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		res := h.doSession(t, half, method, meKeys, map[string]any{"name": "x", "scopes": []string{"tickets:read"}})
		res.Body.Close()
		require.Equal(t, http.StatusForbidden, res.StatusCode, method)
	}
	res := h.doSession(t, half, http.MethodGet, meKeys, nil)
	res.Body.Close()
	keys, err := h.authStore.ListByUser(context.Background(), h.staffID)
	require.NoError(t, err)
	require.Len(t, keys, 1, "nothing was minted (the harness's own key is the one)")
}

// The route always issues for the caller: naming someone else is refused, not
// ignored, because an ignored user_id would hand the key to the wrong person.
func TestMeAPIKeys_UserIDIsNotAccepted(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	h.allowStaffKeys(t)
	res := h.doSession(t, h.session(t, asStaff), http.MethodPost, meKeys, map[string]any{
		"name": "x", "scopes": []string{"tickets:read"}, "user_id": h.adminID.String(),
	})
	b := readAll(t, res)
	require.Equal(t, http.StatusBadRequest, res.StatusCode, "body: %s", b)
}

// Turning the owner off stops their keys: the existing rule holds for keys
// they made themselves.
func TestMeAPIKeys_DisablingTheOwnerStopsTheKey(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	h.allowStaffKeys(t)
	k := h.mintMine(t, h.session(t, asStaff), "tickets:read")

	res := h.doAsAdmin(t, http.MethodPatch, "/api/v1/admin/users/"+h.staffID.String(), map[string]any{"disabled": true})
	res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)

	res = requestWithKey(t, h, k.Token, "/api/v1/tickets")
	res.Body.Close()
	require.Equal(t, http.StatusUnauthorized, res.StatusCode)
}

func TestMeAPIKeys_IssuingAndRevokingIsAudited(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	h.allowStaffKeys(t)
	staff := h.session(t, asStaff)
	k := h.mintMine(t, staff, "tickets:read", "tickets:write")

	got := h.auditFor(t, k.ID)
	require.Len(t, got, 1)
	require.Equal(t, "api_key_issued", got[0]["action"])
	require.Equal(t, h.staffID, got[0]["actor"])
	after := got[0]["after"].(map[string]any)
	require.Equal(t, h.staffID.String(), after["owner_user_id"])
	require.Equal(t, false, after["on_behalf"])
	require.Equal(t, "tickets:read,tickets:write", after["scopes"])
	require.NotContains(t, after, "name", "a key's name is free text and stays out of the audit log")

	res := h.doSession(t, staff, http.MethodDelete, meKeys+"/"+k.ID.String(), nil)
	res.Body.Close()
	require.Equal(t, http.StatusNoContent, res.StatusCode)

	got = h.auditFor(t, k.ID)
	require.Len(t, got, 2)
	require.Equal(t, "api_key_revoked", got[0]["action"], "newest first")
	require.Equal(t, h.staffID, got[0]["actor"])
	require.Equal(t, false, got[0]["after"].(map[string]any)["on_behalf"])
}

// An administrator-issued key is the staff member's to see and revoke, even
// with the setting off: revoking only ever removes access.
func TestMeAPIKeys_StaffCanRevokeAKeyIssuedForThemWithTheSettingOff(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	res := h.doSession(t, h.session(t, asAdmin), http.MethodPost, "/api/v1/admin/api-keys", map[string]any{
		"name": "for staff", "scopes": []string{"tickets:read"}, "user_id": h.staffID.String(),
	})
	b := readAll(t, res)
	require.Equal(t, http.StatusCreated, res.StatusCode, "body: %s", b)
	var k issuedKey
	require.NoError(t, json.Unmarshal(b, &k))

	staff := h.session(t, asStaff)
	res = h.doSession(t, staff, http.MethodGet, meKeys, nil)
	lb := readAll(t, res)
	require.Contains(t, string(lb), k.ID.String())
	require.Contains(t, string(lb), h.adminID.String(), "who issued it is shown")

	res = h.doSession(t, staff, http.MethodDelete, meKeys+"/"+k.ID.String(), nil)
	res.Body.Close()
	require.Equal(t, http.StatusNoContent, res.StatusCode)
}

func TestMe_ReportsWhetherTheCallerMayCreateKeys(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	can := func(w who) bool {
		res := h.doSession(t, h.session(t, w), http.MethodGet, "/api/v1/me", nil)
		b := readAll(t, res)
		require.Equal(t, http.StatusOK, res.StatusCode)
		var me struct {
			Can *bool `json:"can_create_api_keys"`
		}
		require.NoError(t, json.Unmarshal(b, &me))
		require.NotNil(t, me.Can, "the field is always present: %s", b)
		return *me.Can
	}
	require.True(t, can(asAdmin))
	require.False(t, can(asStaff))
	require.False(t, can(asReporter))
	h.allowStaffKeys(t)
	require.True(t, can(asAdmin))
	require.True(t, can(asStaff))
	require.False(t, can(asReporter), "never for a requester")
}
