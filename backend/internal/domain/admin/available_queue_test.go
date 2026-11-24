package admin_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
)

// available_queue_roles decides who may open the Available view (#676). An
// administrator always may; the setting only ever adds staff.
func TestAvailableQueueAllowed(t *testing.T) {
	cases := []struct {
		name  string
		raw   string // stored JSON; empty means the row is absent
		role  string
		allow bool
	}{
		{"absent admin", "", "admin", true},
		{"absent staff", "", "staff", false},
		{"absent user", "", "user", false},
		{"empty admin", `[]`, "admin", true},
		{"empty staff", `[]`, "staff", false},
		{"staff only, staff", `["staff"]`, "staff", true},
		{"staff only, admin", `["staff"]`, "admin", true},
		{"staff only, user", `["staff"]`, "user", false},
		{"admin only, staff", `["admin"]`, "staff", false},
		{"admin only, admin", `["admin"]`, "admin", true},
		{"both, staff", `["admin","staff"]`, "staff", true},
		{"damaged object, staff", `{"staff":true}`, "staff", false},
		{"damaged object, admin", `{"staff":true}`, "admin", true},
		{"damaged string, staff", `"staff"`, "staff", false},
		{"damaged null, staff", `null`, "staff", false},
		{"damaged garbage, staff", `not json`, "staff", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newFakeAdminStore()
			if tc.raw != "" {
				store.data[admin.KeyAvailableQueueRoles] = []byte(tc.raw)
			}
			svc := admin.NewService(store)
			require.Equal(t, tc.allow, svc.AvailableQueueAllowed(context.Background(), tc.role))
		})
	}
}

func TestAvailableQueueRoles(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want []string
	}{
		{"absent", "", nil},
		{"empty", `[]`, []string{}},
		{"staff", `["staff"]`, []string{"staff"}},
		{"damaged", `{"a":1}`, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newFakeAdminStore()
			if tc.raw != "" {
				store.data[admin.KeyAvailableQueueRoles] = []byte(tc.raw)
			}
			svc := admin.NewService(store)
			got := svc.AvailableQueueRoles(context.Background())
			require.ElementsMatch(t, tc.want, got)
			require.Equal(t, tc.want == nil, got == nil)
		})
	}
}

// The setting is a permission widening, so a machine credential may not change
// it, and PATCH /admin/settings must accept it at all.
func TestAvailableQueueRoles_IsKnownAndSessionOnly(t *testing.T) {
	require.Contains(t, admin.KnownKeys(), admin.KeyAvailableQueueRoles)
	require.Contains(t, admin.AuthCriticalKeys(), admin.KeyAvailableQueueRoles)
}
