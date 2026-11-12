package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
)

func setAllowedDomains(t *testing.T, h *harness, domains []string) {
	t.Helper()
	raw, err := json.Marshal(domains)
	require.NoError(t, err)
	require.NoError(t, h.adminSvc.SetRaw(context.Background(), admin.KeyAllowedEmailDomains, raw))
}

// GET /auth/signup/status reports signup as it is in force. A domain list
// overrides open registration (Register ignores the open setting once there is
// a list), so reporting the raw setting said "anyone may sign up" about an
// instance that only admitted listed domains (#421). restricted_by_domain_list
// is the reason, so the admin page can say it.
func TestSignupStatus_ReportsWhatIsInForce(t *testing.T) {
	cases := []struct {
		name           string
		open           bool
		domains        []string
		wantOpen       bool
		wantRestricted bool
	}{
		{"closed, no list", false, nil, false, false},
		{"open, no list", true, nil, true, false},
		{"open, with a list", true, []string{"example.com"}, false, true},
		{"closed, with a list", false, []string{"example.com"}, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, cleanup := newHarness(t)
			defer cleanup()
			ctx := context.Background()
			require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeySelfSignupEnabled, true))
			require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeyOpenRegistrationEnabled, tc.open))
			if tc.domains != nil {
				setAllowedDomains(t, h, tc.domains)
			}

			res := h.doUnauth(t, http.MethodGet, "/api/v1/auth/signup/status", nil)
			var got map[string]any
			decodeJSON(t, res, &got)
			require.Equal(t, tc.wantOpen, got["open_registration"])
			require.Equal(t, tc.wantRestricted, got["restricted_by_domain_list"])
			require.Equal(t, true, got["enabled"], "the existing keys stay")
			require.Contains(t, got, "saml_enabled")
		})
	}
}

// Who can sign up is exactly what it was: only the reported value changed.
func TestSignup_WhoMayRegisterIsUnchangedByTheStatusFix(t *testing.T) {
	cases := []struct {
		name    string
		open    bool
		domains []string
		email   string
		want    int
	}{
		{"open, no list, anyone", true, nil, "a@anywhere.test", http.StatusAccepted},
		{"closed, no list, refused", false, nil, "a@anywhere.test", http.StatusUnprocessableEntity},
		{"open with a list, listed domain", true, []string{"example.com"}, "a@example.com", http.StatusAccepted},
		{"open with a list, other domain", true, []string{"example.com"}, "a@anywhere.test", http.StatusUnprocessableEntity},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, cleanup := newHarness(t)
			defer cleanup()
			ctx := context.Background()
			require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeySelfSignupEnabled, true))
			require.NoError(t, h.adminSvc.SetBool(ctx, admin.KeyOpenRegistrationEnabled, tc.open))
			if tc.domains != nil {
				setAllowedDomains(t, h, tc.domains)
			}
			res := h.doUnauth(t, http.MethodPost, "/api/v1/auth/signup", map[string]any{"email": tc.email})
			res.Body.Close()
			require.Equal(t, tc.want, res.StatusCode)
		})
	}
}
