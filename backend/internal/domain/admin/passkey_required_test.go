package admin_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
)

// #616 part c: a role that must use a passkey is thereby a role whose MFA is
// enforced. The implication is computed when read; mfa_enforced_roles itself is
// never written on its behalf.
func TestMFARequiredFor_PasskeyRequiredImpliesEnforced(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name     string
		mfaOn    bool
		enforced string
		passkey  string
		role     string
		want     bool
	}{
		{"only in the passkey list", true, `[]`, `["staff"]`, "staff", true},
		{"in both lists", true, `["staff"]`, `["staff"]`, "staff", true},
		{"a different role in the passkey list", true, `[]`, `["admin"]`, "staff", false},
		{"enforced only, unchanged", true, `["staff"]`, `[]`, "staff", true},
		{"neither", true, `[]`, `[]`, "staff", false},
		{"mfa off makes both lists inert", false, `["staff"]`, `["staff"]`, "staff", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := admin.NewService(newFakeAdminStore())
			require.NoError(t, svc.SetBool(ctx, admin.KeyMFAEnabled, tc.mfaOn))
			require.NoError(t, svc.SetRaw(ctx, admin.KeyMFAEnforcedRoles, []byte(tc.enforced)))
			require.NoError(t, svc.SetRaw(ctx, admin.KeyPasskeyRequiredRoles, []byte(tc.passkey)))
			require.Equal(t, tc.want, svc.MFARequiredFor(ctx, tc.role))
		})
	}
}

func TestPasskeyRequiredFor(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name    string
		mfaOn   bool
		passkey string
		role    string
		want    bool
	}{
		{"listed", true, `["staff","admin"]`, "admin", true},
		{"not listed", true, `["staff"]`, "admin", false},
		{"empty list", true, `[]`, "admin", false},
		{"mfa off", false, `["admin"]`, "admin", false},
		{"damaged row reads as empty", true, `not json`, "admin", false},
		{"a JSON object reads as empty", true, `{"admin":true}`, "admin", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := admin.NewService(newFakeAdminStore())
			require.NoError(t, svc.SetBool(ctx, admin.KeyMFAEnabled, tc.mfaOn))
			require.NoError(t, svc.SetRaw(ctx, admin.KeyPasskeyRequiredRoles, []byte(tc.passkey)))
			require.Equal(t, tc.want, svc.PasskeyRequiredFor(ctx, tc.role))
		})
	}
}

func TestPasskeyRequiredRoles_UnsetReadsEmpty(t *testing.T) {
	svc := admin.NewService(newFakeAdminStore())
	require.Empty(t, svc.PasskeyRequiredRoles(context.Background()))
	require.False(t, svc.PasskeyRequiredFor(context.Background(), "admin"))
}

func TestPasskeyRequiredRoles_IsAuthCriticalAndKnown(t *testing.T) {
	require.True(t, admin.IsKnownKey(admin.KeyPasskeyRequiredRoles))
	require.Contains(t, admin.AuthCriticalKeys(), admin.KeyPasskeyRequiredRoles)
}
