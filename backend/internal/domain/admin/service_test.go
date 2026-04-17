package admin_test

import (
	"context"
	"errors"
	"testing"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
	"github.com/stretchr/testify/require"
)

// fakeAdminStore is an in-memory implementation of admin.Store.
type fakeAdminStore struct {
	data map[string][]byte
}

func newFakeAdminStore() *fakeAdminStore {
	return &fakeAdminStore{data: make(map[string][]byte)}
}

func (f *fakeAdminStore) Get(_ context.Context, key string) ([]byte, error) {
	v, ok := f.data[key]
	if !ok {
		return nil, errors.New("not found")
	}
	return v, nil
}

func (f *fakeAdminStore) Set(_ context.Context, key string, value []byte) error {
	f.data[key] = value
	return nil
}

func (f *fakeAdminStore) List(_ context.Context) (map[string][]byte, error) {
	out := make(map[string][]byte, len(f.data))
	for k, v := range f.data {
		out[k] = v
	}
	return out, nil
}

// ── Tests ─────────────────────────────────────────────────────────────────────

func TestAdminService_ReopenWindowDays_Default(t *testing.T) {
	svc := admin.NewService(newFakeAdminStore())
	// No setting stored — should default to 7.
	require.Equal(t, 7, svc.ReopenWindowDays(context.Background()))
}

func TestAdminService_ReopenWindowDays_Stored(t *testing.T) {
	svc := admin.NewService(newFakeAdminStore())
	require.NoError(t, svc.SetInt(context.Background(), admin.KeyReopenWindowDays, 14))
	require.Equal(t, 14, svc.ReopenWindowDays(context.Background()))
}

func TestAdminService_GetSetBool(t *testing.T) {
	svc := admin.NewService(newFakeAdminStore())
	require.NoError(t, svc.SetBool(context.Background(), admin.KeySAMLEnabled, true))
	got, err := svc.GetBool(context.Background(), admin.KeySAMLEnabled)
	require.NoError(t, err)
	require.True(t, got)
}

func TestAdminService_SAMLEnabled_Default(t *testing.T) {
	svc := admin.NewService(newFakeAdminStore())
	require.False(t, svc.SAMLEnabled(context.Background()))
}

func TestAdminService_MFAEnabled_Default(t *testing.T) {
	svc := admin.NewService(newFakeAdminStore())
	require.False(t, svc.MFAEnabled(context.Background()))
}

func TestAdminService_GuestSubmissionEnabled_Default(t *testing.T) {
	svc := admin.NewService(newFakeAdminStore())
	require.False(t, svc.GuestSubmissionEnabled(context.Background()))
}

func TestAdminService_MFARequiredFor(t *testing.T) {
	ctx := context.Background()

	cases := []struct {
		name         string
		enabled      bool
		enforcedRaw  string
		role         string
		wantRequired bool
	}{
		{"mfa disabled globally", false, `["admin","staff","user"]`, "admin", false},
		{"mfa enabled, no roles enforced", true, `[]`, "admin", false},
		{"mfa enabled, admin enforced, admin user", true, `["admin"]`, "admin", true},
		{"mfa enabled, admin enforced, staff user", true, `["admin"]`, "staff", false},
		{"mfa enabled, all roles enforced", true, `["admin","staff","user"]`, "user", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := admin.NewService(newFakeAdminStore())
			require.NoError(t, svc.SetBool(ctx, admin.KeyMFAEnabled, tc.enabled))
			require.NoError(t, svc.SetRaw(ctx, admin.KeyMFAEnforcedRoles, []byte(tc.enforcedRaw)))
			require.Equal(t, tc.wantRequired, svc.MFARequiredFor(ctx, tc.role))
		})
	}
}
