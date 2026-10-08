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

func TestAdminService_StaffCanViewTicketChangeHistory_Default(t *testing.T) {
	svc := admin.NewService(newFakeAdminStore())
	require.False(t, svc.StaffCanViewTicketChangeHistory(context.Background()))
}

func TestAdminService_StaffCanViewTicketChangeHistory_Stored(t *testing.T) {
	svc := admin.NewService(newFakeAdminStore())
	require.NoError(t, svc.SetBool(context.Background(), admin.KeyStaffCanViewTicketChangeHistory, true))
	require.True(t, svc.StaffCanViewTicketChangeHistory(context.Background()))
}

func TestAdminService_AuditRetentionDays(t *testing.T) {
	cases := []struct {
		name  string
		store func(t *testing.T, svc *admin.Service)
		want  int
	}{
		// Unset is forever, and that is the one that matters. Every release
		// before #129 kept the audit log for good because nothing pruned it,
		// so a default that prunes would delete history on upgrade from a
		// file the operator never edited. Pruning is opt-in.
		{name: "unset keeps entries forever", store: func(t *testing.T, svc *admin.Service) {}, want: admin.AuditRetentionForever},
		{name: "stored positive value is used", store: func(t *testing.T, svc *admin.Service) {
			require.NoError(t, svc.SetInt(context.Background(), admin.KeyAuditRetentionDays, 90))
		}, want: 90},
		// Zero is how an operator turns pruning back off, so it means
		// forever rather than falling back to some window they did not ask
		// for.
		{name: "zero means forever", store: func(t *testing.T, svc *admin.Service) {
			require.NoError(t, svc.SetInt(context.Background(), admin.KeyAuditRetentionDays, 0))
		}, want: admin.AuditRetentionForever},
		// A misconfigured value fails towards keeping evidence, never towards
		// destroying it.
		{name: "negative means forever too", store: func(t *testing.T, svc *admin.Service) {
			require.NoError(t, svc.SetInt(context.Background(), admin.KeyAuditRetentionDays, -5))
		}, want: admin.AuditRetentionForever},
		// And the top of the range, which is where that invariant used to be
		// false. The sweep computes its cutoff with AddDate(0, 0, -days); for
		// a large enough value that wraps and the cutoff lands in the FUTURE,
		// so "keep for 25 quintillion days" deleted everything including
		// today. The handler refuses these now, but the reader clamps too —
		// it should not trust a row it did not validate, and this is the only
		// test that can reach the clamp, since the handler stops such a value
		// ever being stored through the API.
		{name: "a value large enough to wrap AddDate is clamped", store: func(t *testing.T, svc *admin.Service) {
			require.NoError(t, svc.SetInt(context.Background(), admin.KeyAuditRetentionDays, 9223372036854775807))
		}, want: admin.AuditRetentionMaxDays},
		{name: "just over the cap is clamped", store: func(t *testing.T, svc *admin.Service) {
			require.NoError(t, svc.SetInt(context.Background(), admin.KeyAuditRetentionDays, admin.AuditRetentionMaxDays+1))
		}, want: admin.AuditRetentionMaxDays},
		{name: "the cap itself is kept", store: func(t *testing.T, svc *admin.Service) {
			require.NoError(t, svc.SetInt(context.Background(), admin.KeyAuditRetentionDays, admin.AuditRetentionMaxDays))
		}, want: admin.AuditRetentionMaxDays},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := admin.NewService(newFakeAdminStore())
			tc.store(t, svc)
			require.Equal(t, tc.want, svc.AuditRetentionDays(context.Background()))
		})
	}
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

// #362: anything but the three known values reads as "everywhere", the most
// masking — a typo or a damaged row must never show requester names.
func TestAuditMaskRequesterNames(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct{ stored, want string }{
		{"", "everywhere"}, {"everywhere", "everywhere"},
		{"admin_log", "admin_log"}, {"ticket_log", "ticket_log"},
		{"nowhere", "everywhere"}, {"ADMIN_LOG", "everywhere"},
	} {
		svc := admin.NewService(newFakeAdminStore())
		if tc.stored != "" {
			if err := svc.SetString(ctx, admin.KeyAuditMaskRequesterNames, tc.stored); err != nil {
				t.Fatal(err)
			}
		}
		if got := svc.AuditMaskRequesterNames(ctx); got != tc.want {
			t.Errorf("stored %q: got %q, want %q", tc.stored, got, tc.want)
		}
	}
}
