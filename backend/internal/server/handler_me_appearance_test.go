package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// The appearance choice on a person's own profile (#709): light, dark or
// device, per account, changed only by the person.

const meAppearance = "/api/v1/me/appearance"

type themeView struct {
	Theme string `json:"theme"`
}

func themeOf(t *testing.T, body []byte) string {
	t.Helper()
	var v themeView
	require.NoError(t, json.Unmarshal(body, &v), "body: %s", body)
	return v.Theme
}

// rawPatch sends body exactly as given, for input that json.Encode cannot make.
func rawPatch(t *testing.T, h *harness, cookie, body string) (*http.Response, []byte) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPatch, meAppearance, strings.NewReader(body))
	req.Header.Set("Cookie", cookie)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.srv.ServeHTTP(rr, req)
	res := rr.Result()
	defer res.Body.Close()
	return res, rr.Body.Bytes()
}

func auditRowCount(t *testing.T, h *harness) int {
	t.Helper()
	var n int
	require.NoError(t, h.tx.QueryRow(`SELECT count(*) FROM audit_log`).Scan(&n))
	return n
}

func TestMe_ReportsDeviceForANewAccount(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	s := loggedIn(t, h)

	res, body := s.send(t, http.MethodGet, "/api/v1/me", nil)
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, "device", themeOf(t, body))
}

func TestMe_AppearanceIsSavedForEveryRole(t *testing.T) {
	for name, who := range map[string]who{"admin": asAdmin, "staff": asStaff, "requester": asReporter} {
		t.Run(name, func(t *testing.T) {
			h, cleanup := newHarness(t)
			defer cleanup()
			cookie := h.session(t, who)

			for _, want := range []string{"dark", "light", "device"} {
				res := h.doSession(t, cookie, http.MethodPatch, meAppearance, map[string]any{"theme": want})
				body := readAll(t, res)
				require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", body)
				require.Equal(t, want, themeOf(t, body), "the answer is the /me view as saved")
				var view map[string]json.RawMessage
				require.NoError(t, json.Unmarshal(body, &view))
				require.Contains(t, view, "email", "the answer is the /me view, not just the theme")
				require.Contains(t, view, "can_create_api_keys")

				res = h.doSession(t, cookie, http.MethodGet, "/api/v1/me", nil)
				body = readAll(t, res)
				require.Equal(t, want, themeOf(t, body), "it persisted")
			}
		})
	}
}

func TestMe_AppearanceRefusesWhatIsNotATheme(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	cookie := h.session(t, asStaff)

	for name, body := range map[string]string{
		"no body":       ``,
		"empty object":  `{}`,
		"null":          `{"theme":null}`,
		"a number":      `{"theme":1}`,
		"a boolean":     `{"theme":true}`,
		"an object":     `{"theme":{}}`,
		"auto":          `{"theme":"auto"}`,
		"system":        `{"theme":"system"}`,
		"wrong case":    `{"theme":"Dark"}`,
		"empty string":  `{"theme":""}`,
		"padded":        `{"theme":" dark"}`,
		"bad JSON":      `{"theme":`,
		"not an object": `"dark"`,
		"another field": `{"colour":"dark"}`,
		"an array":      `["dark"]`,
		"another key":   `{"theme2":"dark"}`,
	} {
		t.Run(name, func(t *testing.T) {
			res, b := rawPatch(t, h, cookie, body)
			require.Equal(t, http.StatusBadRequest, res.StatusCode, "body: %s", b)
			require.Equal(t, "invalid_theme", errCode(t, b))
			require.Contains(t, string(b), "theme must be light, dark or device")
		})
	}
	u, err := h.userSvc.GetByID(context.Background(), h.staffID)
	require.NoError(t, err)
	require.Equal(t, user.ThemeDevice, u.Theme, "a refusal changed something")
}

func TestMe_AppearanceIgnoresUnknownKeys(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	res := h.doSession(t, h.session(t, asStaff), http.MethodPatch, meAppearance,
		map[string]any{"theme": "dark", "role": "admin", "disabled": true})
	body := readAll(t, res)
	require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", body)
	u, err := h.userSvc.GetByID(context.Background(), h.staffID)
	require.NoError(t, err)
	require.Equal(t, user.ThemeDark, u.Theme)
	require.Equal(t, user.RoleStaff, u.Role)
	require.False(t, u.Disabled)
}

// The account did not change, so updated_at stays, and nothing is audited: it is
// a display preference.
func TestMe_AppearanceLeavesUpdatedAtAndWritesNoAudit(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	before, err := h.userSvc.GetByID(context.Background(), h.staffID)
	require.NoError(t, err)
	auditBefore := auditRowCount(t, h)

	res := h.doSession(t, h.session(t, asStaff), http.MethodPatch, meAppearance, map[string]any{"theme": "dark"})
	body := readAll(t, res)
	require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", body)

	after, err := h.userSvc.GetByID(context.Background(), h.staffID)
	require.NoError(t, err)
	require.True(t, before.UpdatedAt.Equal(after.UpdatedAt), "updated_at moved")
	require.Equal(t, auditBefore, auditRowCount(t, h), "a display preference was audited")
}

// A key and an OAuth client have no screen to look at, and the route sits with
// the rest of the self-service profile: refused to machine credentials.
func TestMe_AppearanceIsNotForMachineCredentials(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	t.Run("an API key", func(t *testing.T) {
		res := h.do(t, http.MethodPatch, meAppearance, map[string]any{"theme": "dark"})
		res.Body.Close()
		require.Equal(t, http.StatusForbidden, res.StatusCode)
		u, err := h.userSvc.GetByID(context.Background(), h.staffID)
		require.NoError(t, err)
		require.Equal(t, user.ThemeDevice, u.Theme)
	})

	t.Run("an OAuth client", func(t *testing.T) {
		resp := h.createCredential(t, "/api/v1/admin/oauth-clients", map[string]any{
			"name": "integration", "scopes": []string{"tickets:read"},
		})
		require.Equal(t, http.StatusCreated, resp.StatusCode)
		var created struct {
			ClientID     string `json:"client_id"`
			ClientSecret string `json:"client_secret"`
		}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))
		resp.Body.Close()
		token := h.oauthToken(t, created.ClientID, created.ClientSecret)

		req := httptest.NewRequest(http.MethodPatch, meAppearance, strings.NewReader(`{"theme":"dark"}`))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		h.srv.ServeHTTP(rr, req)
		require.Equal(t, http.StatusForbidden, rr.Code, "body: %s", rr.Body.String())
	})

	t.Run("nobody signed in", func(t *testing.T) {
		res := h.doUnauth(t, http.MethodPatch, meAppearance, map[string]any{"theme": "dark"})
		res.Body.Close()
		require.Equal(t, http.StatusUnauthorized, res.StatusCode)
	})
}

// A session that has passed the password but not the second factor reaches
// nothing here.
func TestMe_AppearanceRequiresMFA(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	half := h.sessionCookieFor(t, auth.SessionData{
		UserID: h.staffID, Role: user.RoleStaff, MFAPassed: false,
		PendingUntil: time.Now().Add(time.Hour),
	})
	res := h.doSession(t, half, http.MethodPatch, meAppearance, map[string]any{"theme": "dark"})
	res.Body.Close()
	require.Equal(t, http.StatusForbidden, res.StatusCode)
	u, err := h.userSvc.GetByID(context.Background(), h.staffID)
	require.NoError(t, err)
	require.Equal(t, user.ThemeDevice, u.Theme)
}

// The answer to a sign-in carries the theme, so the browser can apply the
// account's choice before it has asked /me.
func TestLogin_AnswerCarriesTheTheme(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	require.NoError(t, h.userSvc.SetTheme(context.Background(), h.staffID, user.ThemeDark))

	s := &session{h: h}
	res, body := s.send(t, http.MethodPost, "/api/v1/auth/local/login",
		map[string]any{"email": "staff@test.local", "password": "password"})
	require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", body)
	var got struct {
		User themeView `json:"user"`
	}
	require.NoError(t, json.Unmarshal(body, &got))
	require.Equal(t, "dark", got.User.Theme)
}

// The administrator's user page cannot set, or put back, somebody's choice.
func TestAdminUpdateUser_DoesNotTouchTheTheme(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	require.NoError(t, h.userSvc.SetTheme(context.Background(), h.staffID, user.ThemeLight))

	res := h.doAsAdminSession(t, http.MethodPatch, "/api/v1/admin/users/"+h.staffID.String(),
		map[string]any{"display_name": "Renamed", "theme": "dark"})
	body := readAll(t, res)
	require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", body)
	require.Equal(t, "light", themeOf(t, body), "the administrator's view reports it, read-only")

	u, err := h.userSvc.GetByID(context.Background(), h.staffID)
	require.NoError(t, err)
	require.Equal(t, "Renamed", u.DisplayName)
	require.Equal(t, user.ThemeLight, u.Theme)
}
