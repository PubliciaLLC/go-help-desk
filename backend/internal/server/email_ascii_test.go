package server_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
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
			requireLoginRefused(t, oh.harness, resp, tc.code)
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

func TestGuestResend_RefusesANonASCIIEmail(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	enableGuests(t, h)
	tk, _ := seedGuestTicketFor(t, h, "jim@example.test")

	_, err := h.ticketSvc.GuestTicketIDFor(ctx, tk.TrackingNumber, "jim@example.test")
	require.NoError(t, err, "precondition: the ASCII address resolves")
	_, err = h.ticketSvc.GuestTicketIDFor(ctx, tk.TrackingNumber, "jüm@example.test")
	require.ErrorIs(t, err, ticket.ErrGuestTokenNotFound)
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
		return h.doAsAdminSession(t, http.MethodPatch, "/api/v1/admin/users/"+id.String(), body)
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
	res := h.doAsAdminSession(t, http.MethodPatch, "/api/v1/admin/settings",
		map[string]any{admin.KeyAllowedEmailDomains: []string{"cörp.test"}})
	res.Body.Close()
	require.Equal(t, http.StatusBadRequest, res.StatusCode)

	res = h.doAsAdminSession(t, http.MethodPatch, "/api/v1/admin/settings",
		map[string]any{admin.KeyAllowedEmailDomains: []string{"xn--crp-6ka.test", "corp.test"}})
	res.Body.Close()
	require.Equal(t, http.StatusNoContent, res.StatusCode)
}

// The routes that take a typed address all end at user.ValidateEmail. These pin
// each one at the door, so a route added later that skips it shows up here.
func TestTypedEmailRoutes_RefuseANonASCIIEmail(t *testing.T) {
	const addr = "jüm@example.test"

	t.Run("signup", func(t *testing.T) {
		h, cleanup := newHarness(t)
		defer cleanup()
		ctx := context.Background()
		require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeySelfSignupEnabled, true))
		require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeyOpenRegistrationEnabled, true))
		res := h.doUnauth(t, http.MethodPost, "/api/v1/auth/signup", map[string]any{"email": addr})
		res.Body.Close()
		require.Equal(t, http.StatusBadRequest, res.StatusCode)
	})

	t.Run("guest submission", func(t *testing.T) {
		h, cleanup := newHarness(t)
		defer cleanup()
		enableGuests(t, h)
		before := ticketCount(t, h)
		res := h.doGuest(t, http.MethodPost, "/api/v1/guest/tickets", "", map[string]any{
			"subject": "Door will not open", "description": "it sticks",
			"category_id": h.catID.String(), "guest_email": addr, "guest_name": "Jim",
		})
		res.Body.Close()
		require.Equal(t, http.StatusBadRequest, res.StatusCode)
		require.Equal(t, before, ticketCount(t, h), "no ticket is written")
	})

	t.Run("reporter_email", func(t *testing.T) {
		h, cleanup := newHarness(t)
		defer cleanup()
		enableGuests(t, h)
		before := ticketCount(t, h)
		res := h.postOnBehalf(t, "staff", map[string]any{"reporter_email": addr, "guest_name": "Jim"})
		res.Body.Close()
		require.Equal(t, http.StatusBadRequest, res.StatusCode)
		require.Equal(t, before, ticketCount(t, h), "no ticket is written")
	})
}

// A guest ticket stored with a non-ASCII address before this rule keeps working:
// a follow-up copies the stored address, which is not a newly typed one, so it
// is not held to the rule again.
func TestFollowUp_OfAGuestTicketWithAStoredNonASCIIAddress(t *testing.T) {
	const stored = "jüm@example.test"
	seedClosed := func(t *testing.T, h *harness) (ticket.Ticket, string) {
		t.Helper()
		enableGuests(t, h)
		tk, token := seedGuestTicketFor(t, h, "jim@example.test")
		_, err := h.tx.ExecContext(context.Background(),
			`UPDATE tickets SET guest_email = $1 WHERE id = $2`, stored, tk.ID)
		require.NoError(t, err)
		closeGuestTicket(t, h, tk)
		return tk, token
	}

	t.Run("staff", func(t *testing.T) {
		h, cleanup := newHarness(t)
		defer cleanup()
		tk, _ := seedClosed(t, h)
		res := h.do(t, http.MethodPost, "/api/v1/tickets/"+tk.ID.String()+"/follow-up", nil)
		got := decodeTicket(t, res)
		require.NotNil(t, got.GuestEmail)
		require.Equal(t, stored, *got.GuestEmail)
	})

	t.Run("guest link", func(t *testing.T) {
		h, cleanup := newHarness(t)
		defer cleanup()
		_, token := seedClosed(t, h)
		res := h.doGuest(t, http.MethodPost, "/api/v1/guest/follow-up", token, map[string]any{})
		res.Body.Close()
		require.Equal(t, http.StatusCreated, res.StatusCode)
	})
}

// The forgot-password answer does not depend on the address: a non-ASCII one
// gets the same status and body as an ASCII one with no account, and no mail.
func TestForgotPassword_NonASCIIAddressAnswersLikeAnyOther(t *testing.T) {
	h, cleanup := harnessWithEmail(t)
	defer cleanup()
	wantStatus, wantBody := forgot(t, h, "nobody@test.local")
	gotStatus, gotBody := forgot(t, h, "jüm@test.local")
	require.Equal(t, wantStatus, gotStatus)
	require.Equal(t, wantBody, gotBody)
	require.Empty(t, h.resetMail.sent)
}
