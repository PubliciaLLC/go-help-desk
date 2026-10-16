package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
)

// The longest a new API key may live (#450): 2 years unless an administrator
// sets another figure in years, or "no limit" (-1). It applies when a key is
// made, not when it is used.

func inYears(y float64) string {
	return time.Now().Add(time.Duration(y * 365 * 24 * float64(time.Hour))).UTC().Format(time.RFC3339)
}

func (h *harness) setMaxYears(t *testing.T, years int) {
	t.Helper()
	require.NoError(t, h.adminSvc.SetInt(context.Background(), admin.KeyAPIKeyMaxLifetimeYears, years))
}

// creators is every route that makes a key, so the cap cannot be missed on one.
func (h *harness) creators(t *testing.T) map[string]func(body map[string]any) (int, []byte) {
	h.allowStaffKeys(t)
	admin, staff := h.session(t, asAdmin), h.session(t, asStaff)
	post := func(cookie, path string, extra map[string]any) func(map[string]any) (int, []byte) {
		return func(body map[string]any) (int, []byte) {
			b := map[string]any{"name": "k", "scopes": []string{"tickets:read"}}
			for k, v := range extra {
				b[k] = v
			}
			for k, v := range body {
				b[k] = v
			}
			res := h.doSession(t, cookie, http.MethodPost, path, b)
			out := readAll(t, res)
			return res.StatusCode, out
		}
	}
	return map[string]func(map[string]any) (int, []byte){
		"staff, own":         post(staff, meKeys, nil),
		"admin, own (/me)":   post(admin, meKeys, nil),
		"admin, own (admin)": post(admin, adminKeys, nil),
		"admin, for staff":   post(admin, adminKeys, map[string]any{"user_id": h.staffID.String()}),
	}
}

func TestAPIKeyLifetime_DefaultIsTwoYearsAndExpiryIsRequired(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	require.Equal(t, 2, h.adminSvc.APIKeyMaxLifetimeYears(context.Background()), "2 years until an administrator says otherwise")

	for name, create := range h.creators(t) {
		t.Run(name, func(t *testing.T) {
			code, b := create(nil)
			require.Equal(t, http.StatusBadRequest, code, "body: %s", b)
			require.Equal(t, "expires_at_required", errCode(t, b))
			require.Contains(t, string(b), "2 years")

			code, b = create(map[string]any{"expires_at": inYears(3)})
			require.Equal(t, http.StatusBadRequest, code, "body: %s", b)
			require.Equal(t, "expires_at_too_far", errCode(t, b))
			require.Contains(t, string(b), "2 years", "the refusal names the limit")

			code, b = create(map[string]any{"expires_at": inYears(1.9)})
			require.Equal(t, http.StatusCreated, code, "body: %s", b)
		})
	}
}

func TestAPIKeyLifetime_FollowsTheSetting(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	creators := h.creators(t)

	t.Run("a longer limit allows longer keys, and names itself", func(t *testing.T) {
		h.setMaxYears(t, 4)
		for name, create := range creators {
			code, b := create(map[string]any{"expires_at": inYears(3.5)})
			require.Equal(t, http.StatusCreated, code, "%s: %s", name, b)
			code, b = create(map[string]any{"expires_at": inYears(5)})
			require.Equal(t, http.StatusBadRequest, code, "%s: %s", name, b)
			require.Contains(t, string(b), "4 years")
		}
	})

	t.Run("no limit makes the expiry optional and uncapped", func(t *testing.T) {
		h.setMaxYears(t, admin.APIKeyNoLimit)
		for name, create := range creators {
			code, b := create(nil)
			require.Equal(t, http.StatusCreated, code, "%s: %s", name, b)
			code, b = create(map[string]any{"expires_at": inYears(30)})
			require.Equal(t, http.StatusCreated, code, "%s: %s", name, b)
		}
	})

	t.Run("one year is singular", func(t *testing.T) {
		h.setMaxYears(t, 1)
		_, b := creators["staff, own"](map[string]any{"expires_at": inYears(2)})
		require.Contains(t, string(b), "1 year")
		require.NotContains(t, string(b), "1 years")
	})
}

// The limit is checked when a key is made. A key with no expiry that already
// exists keeps working, and is flagged so an administrator can rotate it.
func TestAPIKeyLifetime_ExistingKeysKeepWorkingAndAreMarked(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	mk := func(userID uuid.UUID, name string, exp *time.Time) string {
		raw, _, err := auth.GenerateToken()
		require.NoError(t, err)
		raw = "GHD_" + raw
		require.NoError(t, h.authStore.CreateAPIKey(ctx, auth.APIKey{
			ID: uuid.New(), Name: name, HashedToken: auth.HashToken(raw), UserID: userID,
			Scopes: []string{"tickets:read"}, ExpiresAt: exp, CreatedAt: time.Now(),
		}))
		return raw
	}
	in18Months := time.Now().Add(18 * 30 * 24 * time.Hour)
	in5Years := time.Now().Add(5 * 365 * 24 * time.Hour)
	forever := mk(h.staffID, "forever", nil)
	mk(h.staffID, "eighteen months", &in18Months)
	mk(h.staffID, "five years", &in5Years)

	flagged := func(path string, cookie string) map[string]bool {
		res := h.doSession(t, cookie, http.MethodGet, path, nil)
		var list []struct {
			Name        string `json:"name"`
			BeyondLimit *bool  `json:"beyond_limit"`
		}
		b := readAll(t, res)
		require.NoError(t, json.Unmarshal(b, &list), "body: %s", b)
		out := map[string]bool{}
		for _, k := range list {
			require.NotNil(t, k.BeyondLimit, "beyond_limit is always present (%s)", k.Name)
			out[k.Name] = *k.BeyondLimit
		}
		return out
	}

	t.Run("at the default limit", func(t *testing.T) {
		for _, f := range []map[string]bool{
			flagged(meKeys, h.session(t, asStaff)),
			flagged(adminKeys, h.session(t, asAdmin)),
		} {
			require.True(t, f["forever"], "no expiry is longer than any limit")
			require.False(t, f["eighteen months"])
			require.True(t, f["five years"])
		}
	})

	t.Run("after the limit is lowered", func(t *testing.T) {
		h.setMaxYears(t, 1)
		f := flagged(meKeys, h.session(t, asStaff))
		require.True(t, f["eighteen months"], "beyond the new maximum")
	})

	t.Run("with no limit nothing is flagged", func(t *testing.T) {
		h.setMaxYears(t, admin.APIKeyNoLimit)
		for name, v := range flagged(adminKeys, h.session(t, asAdmin)) {
			require.False(t, v, name)
		}
	})

	t.Run("a key beyond the limit still authenticates", func(t *testing.T) {
		res := requestWithKey(t, h, forever, "/api/v1/tickets")
		res.Body.Close()
		require.Equal(t, http.StatusOK, res.StatusCode)
	})
}

func TestMe_ReportsTheKeyLifetimeLimit(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	limit := func() int {
		res := h.doSession(t, h.session(t, asStaff), http.MethodGet, "/api/v1/me", nil)
		var me struct {
			Years *int `json:"api_key_max_lifetime_years"`
		}
		require.NoError(t, json.Unmarshal(readAll(t, res), &me))
		require.NotNil(t, me.Years)
		return *me.Years
	}
	require.Equal(t, 2, limit())
	h.setMaxYears(t, admin.APIKeyNoLimit)
	require.Equal(t, -1, limit(), "-1 is no limit")
}

func TestAPIKeyMaxLifetimeSetting(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	sess := signInAsAdmin(t, h)
	patch := func(v any) (int, string) {
		res, b := sess.send(t, http.MethodPatch, "/api/v1/admin/settings", map[string]any{"api_key_max_lifetime_years": v})
		return res.StatusCode, string(b)
	}

	t.Run("1 to 5 years save, and -1 means no limit", func(t *testing.T) {
		for _, y := range []int{1, 2, 5, 3, -1} {
			code, b := patch(y)
			require.Equal(t, http.StatusNoContent, code, b)
			require.Equal(t, y, h.adminSvc.APIKeyMaxLifetimeYears(context.Background()))
		}
	})
	t.Run("anything else is refused by value, not as an unknown setting", func(t *testing.T) {
		require.NoError(t, h.adminSvc.SetInt(context.Background(), admin.KeyAPIKeyMaxLifetimeYears, 3))
		for name, v := range map[string]any{"zero": 0, "six": 6, "seven": 7, "far too large": 100, "below no-limit": -2, "string": "2", "null": nil, "fraction": 1.5} {
			code, b := patch(v)
			require.Equal(t, http.StatusBadRequest, code, "%s: %s", name, b)
			require.NotContains(t, b, "unknown_setting", name)
			require.Equal(t, 3, h.adminSvc.APIKeyMaxLifetimeYears(context.Background()), "%s must not be stored", name)
		}
	})
	t.Run("session only", func(t *testing.T) {
		res := h.doAsAdmin(t, http.MethodPatch, "/api/v1/admin/settings", map[string]any{"api_key_max_lifetime_years": 0})
		b := readAll(t, res)
		require.Equal(t, http.StatusForbidden, res.StatusCode)
		require.Equal(t, "session_required", errCode(t, b))
		require.Contains(t, admin.AuthCriticalKeys(), admin.KeyAPIKeyMaxLifetimeYears)
		require.Contains(t, admin.KnownKeys(), admin.KeyAPIKeyMaxLifetimeYears)
	})
	t.Run("an unreadable stored value reads as the default", func(t *testing.T) {
		require.NoError(t, h.adminSvc.SetInt(context.Background(), admin.KeyAPIKeyMaxLifetimeYears, 9))
		require.Equal(t, 2, h.adminSvc.APIKeyMaxLifetimeYears(context.Background()))
	})
}
