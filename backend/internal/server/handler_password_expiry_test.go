package server_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// The harness hashes at bcrypt's minimum cost, so nothing in it is "below the
// current cost". These tests swap in a user service whose target is higher, so
// the harness accounts (cost 4) become old hashes, and point the clock's start
// at a date of their choosing.
func withExpiry(t *testing.T, h *harness, startedDaysAgo int) {
	t.Helper()
	ctx := context.Background()
	h.srv.SetUsersForTest(user.NewService(h.userStore,
		user.WithBcryptCost(bcrypt.MinCost+2),
		user.WithPasswordExpiry(h.adminSvc)))
	started := time.Now().AddDate(0, 0, -startedDaysAgo).UTC().Format(time.RFC3339)
	require.NoError(t, h.adminSvc.SetString(ctx, admin.KeyPasswordRehashStartedAt, started))
}

func signInRequest(t *testing.T, h *harness, email string) (*http.Response, []byte) {
	t.Helper()
	s := &session{h: h}
	return s.send(t, http.MethodPost, "/api/v1/auth/local/login",
		map[string]any{"email": email, "password": "password"})
}

func TestLogin_OldHashAfterGraceIsRefusedWithResetMessage(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	withExpiry(t, h, 31) // default N is 30

	res, body := signInRequest(t, h, "user@test.local")
	require.Equal(t, http.StatusForbidden, res.StatusCode, "%s", body)
	require.Contains(t, string(body), "password_reset_required")
	require.Contains(t, string(body), "Forgot password")
	require.Empty(t, res.Cookies(), "a refused sign-in must not start a session")

	stored, err := h.userSvc.GetByIDAdmin(context.Background(), h.userID)
	require.NoError(t, err)
	cost, err := bcrypt.Cost([]byte(stored.PasswordHash))
	require.NoError(t, err)
	require.Equal(t, bcrypt.MinCost, cost, "a refused sign-in leaves the hash alone")
}

func TestLogin_OldHashBeforeGraceSignsInAndIsRehashed(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	withExpiry(t, h, 29)

	res, body := signInRequest(t, h, "user@test.local")
	require.Equal(t, http.StatusOK, res.StatusCode, "%s", body)

	stored, err := h.userSvc.GetByIDAdmin(context.Background(), h.userID)
	require.NoError(t, err)
	cost, err := bcrypt.Cost([]byte(stored.PasswordHash))
	require.NoError(t, err)
	require.Equal(t, bcrypt.MinCost+2, cost)

	// And after the grace period it is no longer an old hash.
	h.srv.SetUsersForTest(user.NewService(h.userStore,
		user.WithBcryptCost(bcrypt.MinCost+2), user.WithPasswordExpiry(h.adminSvc)))
	require.NoError(t, h.adminSvc.SetString(context.Background(), admin.KeyPasswordRehashStartedAt,
		time.Now().AddDate(0, 0, -90).UTC().Format(time.RFC3339)))
	res, body = signInRequest(t, h, "user@test.local")
	require.Equal(t, http.StatusOK, res.StatusCode, "%s", body)
}

func TestLogin_WrongPasswordAfterGraceIsPlainInvalidCredentials(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	withExpiry(t, h, 90)

	s := &session{h: h}
	res, body := s.send(t, http.MethodPost, "/api/v1/auth/local/login",
		map[string]any{"email": "user@test.local", "password": "not it"})
	require.Equal(t, http.StatusUnauthorized, res.StatusCode, "%s", body)
	require.Contains(t, string(body), "invalid_credentials")
	require.NotContains(t, string(body), "password_reset_required")
}

// Every administrator keeps signing in (Erik's rule, replacing the
// last-administrator one); staff and requesters are refused.
func TestLogin_AdministratorsAreExemptFromTheRefusal(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	withExpiry(t, h, 90)

	// A second administrator on a cost-4 hash, an old hash too.
	other := user.NewService(h.userStore, user.WithBcryptCost(bcrypt.MinCost))
	_, err := other.Create(context.Background(), user.CreateUserInput{
		Email: "admin2@test.local", DisplayName: "Admin Two", Role: user.RoleAdmin, Password: "password",
	})
	require.NoError(t, err)

	for _, email := range []string{"admin@test.local", "admin2@test.local"} {
		res, body := signInRequest(t, h, email)
		require.Equal(t, http.StatusOK, res.StatusCode, "%s: %s", email, body)
	}

	for _, email := range []string{"staff@test.local", "user@test.local"} {
		res, body := signInRequest(t, h, email)
		require.Equal(t, http.StatusForbidden, res.StatusCode, "%s: %s", email, body)
		require.Contains(t, string(body), "password_reset_required")
	}
}

// When SAML restricts password sign-in to administrators, a non-admin gets the
// SAML refusal, not an instruction to reset a password they cannot use.
func TestLogin_SAMLRefusalComesBeforePasswordReset(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	withExpiry(t, h, 90)
	require.NoError(t, h.adminSvc.SetBool(context.Background(), admin.KeySAMLEnabled, true))

	res, body := signInRequest(t, h, "user@test.local")
	require.Equal(t, http.StatusForbidden, res.StatusCode, "%s", body)
	require.Contains(t, string(body), "saml_required")
	require.NotContains(t, string(body), "password_reset_required")
}

func TestUpdateSettings_PasswordRehashGraceDaysIsValidated(t *testing.T) {
	cases := []struct {
		name  string
		value any
		want  int // expected status
	}{
		{"default value", 30, http.StatusNoContent},
		{"minimum", 1, http.StatusNoContent},
		{"maximum", admin.PasswordRehashGraceMax, http.StatusNoContent},
		{"zero", 0, http.StatusBadRequest},
		{"negative", -1, http.StatusBadRequest},
		{"over the maximum", admin.PasswordRehashGraceMax + 1, http.StatusBadRequest},
		{"a string", "30", http.StatusBadRequest},
		{"null", nil, http.StatusBadRequest},
		{"a fraction", 1.5, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, cleanup := newHarness(t)
			defer cleanup()
			before := h.adminSvc.PasswordRehashGraceDays(context.Background())

			res, body := loggedInAdmin(t, h).send(t, http.MethodPatch, "/api/v1/admin/settings",
				map[string]any{admin.KeyPasswordRehashGraceDays: tc.value})
			require.Equal(t, tc.want, res.StatusCode, "%s", body)

			got := h.adminSvc.PasswordRehashGraceDays(context.Background())
			if tc.want == http.StatusNoContent {
				require.Equal(t, tc.value, got)
			} else {
				require.Equal(t, before, got, "a refused value must not be stored")
			}
		})
	}
}

// The start of the clock cannot be rewritten through the API.
func TestUpdateSettings_PasswordRehashStartIsReadOnly(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	res, body := loggedInAdmin(t, h).send(t, http.MethodPatch, "/api/v1/admin/settings",
		map[string]any{admin.KeyPasswordRehashStartedAt: time.Now().UTC().Format(time.RFC3339)})
	require.Equal(t, http.StatusBadRequest, res.StatusCode, "%s", body)
	require.Contains(t, string(body), "readonly_setting")

	// And the dump the page echoes back does not carry it.
	sess := loggedInAdmin(t, h)
	res, body = sess.send(t, http.MethodGet, "/api/v1/admin/settings", nil)
	require.Equal(t, http.StatusOK, res.StatusCode, "%s", body)
	require.NotContains(t, string(body), "password_rehash_started_at")
}

// An API key may not move the deadline.
func TestUpdateSettings_PasswordRehashGraceDaysNeedsASession(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	res := h.doUnauthWithHeaders(t, http.MethodPatch, "/api/v1/admin/settings",
		map[string]any{admin.KeyPasswordRehashGraceDays: 365},
		map[string]string{"Authorization": "ApiKey " + h.adminKey})
	require.Equal(t, http.StatusForbidden, res.StatusCode)
}
