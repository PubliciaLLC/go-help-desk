package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/auth"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// Email addresses must be ASCII, so that they compare reliably: a non-ASCII
// address is refused.

func TestOIDC_RefusesANonASCIIEmail(t *testing.T) {
	for _, tc := range []struct {
		name    string
		allowed string
		email   string
		code    string
	}{
		{"no allow-list", `[]`, "jüm@corp.test", "invalid_email"},
		{"allow-list set, non-ASCII local part", `["corp.test"]`, "jüm@corp.test", "invalid_email"},
		{"allow-list set, non-ASCII domain", `["corp.test"]`, "jim@cörp.test", "domain_not_allowed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			oh, cleanup := newOIDCHarness(t)
			defer cleanup()
			require.NoError(t, oh.adminSvc.SetRaw(context.Background(),
				admin.KeyAllowedEmailDomains, []byte(tc.allowed)))

			resp := oh.login(t, fakeOIDCClaims{Subject: "sub-new", Email: tc.email, EmailVerified: true, Name: "N"})
			require.GreaterOrEqual(t, resp.StatusCode, 400)
			require.Equal(t, tc.code, errorCode(t, resp))
			_, ok := findUser(t, oh.harness, tc.email)
			require.False(t, ok, "no account is created for it")
		})
	}
}

func TestSAML_RefusesANonASCIIEmail(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	for _, allowed := range [][]string{nil, {"corp.test"}} {
		_, err := h.userSvc.UpsertSAMLUser(ctx, "saml-new", "jüm@corp.test", "N", allowed)
		require.Error(t, err)
	}
	_, ok := findUser(t, h, "jüm@corp.test")
	require.False(t, ok)
}

func TestCreateUser_RefusesANonASCIIEmail(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	_, err := h.userSvc.Create(context.Background(),
		user.CreateUserInput{Email: "josé@example.com", DisplayName: "Jose", Role: user.RoleUser})
	require.ErrorIs(t, err, user.ErrValidation)
}

func TestGuestTicket_RefusesANonASCIIEmail(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	email := "j\u00fcm@example.test"
	_, err := h.ticketSvc.Create(context.Background(), ticket.CreateInput{
		Subject: "Printer jammed", Description: "again", CategoryID: h.catID,
		GuestEmail: &email, GuestName: "Jum",
	})
	require.ErrorIs(t, err, ticket.ErrValidation)
}

// An account stored with a non-ASCII address before this rule keeps working: an
// edit that leaves the address alone is not held to the rule, and an edit that
// sets a new address is, before anything is written.
func TestAdminEdit_ExistingNonASCIIAccount(t *testing.T) {
	seed := func(t *testing.T, h *harness) user.User {
		t.Helper()
		u := user.User{
			ID: uuid.New(), Email: "jüm@corp.test", DisplayName: "Jum",
			Role: user.RoleUser, CreatedAt: time.Now(), UpdatedAt: time.Now(),
		}
		require.NoError(t, h.userStore.Create(context.Background(), u))
		return u
	}
	patch := func(h *harness, id uuid.UUID, body map[string]any) *http.Response {
		return h.doAsAdmin(t, http.MethodPatch, "/api/v1/admin/users/"+id.String(), body)
	}

	t.Run("role change succeeds", func(t *testing.T) {
		h, cleanup := newHarness(t)
		defer cleanup()
		u := seed(t, h)
		res := patch(h, u.ID, map[string]any{"role": "staff"})
		res.Body.Close()
		require.Equal(t, http.StatusOK, res.StatusCode)
		got, err := h.userSvc.GetByIDAdmin(context.Background(), u.ID)
		require.NoError(t, err)
		require.Equal(t, user.RoleStaff, got.Role)
		require.Equal(t, "jüm@corp.test", got.Email)
	})

	t.Run("rename succeeds", func(t *testing.T) {
		h, cleanup := newHarness(t)
		defer cleanup()
		u := seed(t, h)
		res := patch(h, u.ID, map[string]any{"display_name": "Renamed", "email": "jüm@corp.test"})
		res.Body.Close()
		require.Equal(t, http.StatusOK, res.StatusCode)
		got, err := h.userSvc.GetByIDAdmin(context.Background(), u.ID)
		require.NoError(t, err)
		require.Equal(t, "Renamed", got.DisplayName)
	})

	t.Run("a new non-ASCII address is refused and nothing is applied", func(t *testing.T) {
		h, cleanup := newHarness(t)
		defer cleanup()
		u := seed(t, h)
		res := patch(h, u.ID, map[string]any{"role": "staff", "email": "josé@corp.test"})
		res.Body.Close()
		require.Equal(t, http.StatusBadRequest, res.StatusCode)
		got, err := h.userSvc.GetByIDAdmin(context.Background(), u.ID)
		require.NoError(t, err)
		require.Equal(t, user.RoleUser, got.Role, "a refused edit changes nothing")
	})

	t.Run("an ASCII address replaces it", func(t *testing.T) {
		h, cleanup := newHarness(t)
		defer cleanup()
		u := seed(t, h)
		res := patch(h, u.ID, map[string]any{"email": "jum@corp.test"})
		res.Body.Close()
		require.Equal(t, http.StatusOK, res.StatusCode)
	})
}

func TestAdminSettings_RefusesANonASCIIAllowedDomain(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	// allowed_email_domains is a setting only a signed-in person may change.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	sess, err := h.sessions.New(req, auth.SessionName)
	require.NoError(t, err)
	sess.Values[auth.SessionDataKey] = auth.SessionData{UserID: h.adminID, Role: user.RoleAdmin, MFAPassed: true}
	require.NoError(t, h.sessions.Save(req, rec, sess))
	cookie := rec.Header().Get("Set-Cookie")
	require.NotEmpty(t, cookie)

	patch := func(domains []string) int {
		raw, err := json.Marshal(map[string]any{admin.KeyAllowedEmailDomains: domains})
		require.NoError(t, err)
		r := httptest.NewRequest(http.MethodPatch, "/api/v1/admin/settings", bytes.NewReader(raw))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Cookie", cookie)
		w := httptest.NewRecorder()
		h.srv.ServeHTTP(w, r)
		return w.Result().StatusCode
	}
	require.Equal(t, http.StatusBadRequest, patch([]string{"c\u00f6rp.test"}))
	require.Equal(t, http.StatusNoContent, patch([]string{"xn--crp-6ka.test", "corp.test"}))
}
