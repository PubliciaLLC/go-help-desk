package user_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
)

// Who can be sent staff mail (#441). Every staff-mail recipient passes through
// this one method, which is also where service accounts (#434) will be
// refused.
func TestUser_CanReceiveStaffMail(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name   string
		mutate func(*user.User)
		want   bool
	}{
		{"active staff", func(*user.User) {}, true},
		{"active admin", func(u *user.User) { u.Role = user.RoleAdmin }, true},
		{"a requester account is not staff", func(u *user.User) { u.Role = user.RoleUser }, false},
		{"disabled", func(u *user.User) { u.Disabled = true }, false},
		{"deleted", func(u *user.User) { u.DeletedAt = &now }, false},
		{"no address", func(u *user.User) { u.Email = "" }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u := user.User{ID: uuid.New(), Email: "a@example.com", Role: user.RoleStaff}
			tc.mutate(&u)
			require.Equal(t, tc.want, u.CanReceiveStaffMail())
		})
	}
}

func TestService_SetEmailPreferences(t *testing.T) {
	ctx := context.Background()
	store := newFakeUserStore()
	svc := user.NewService(store, user.WithBcryptCost(4))
	id := uuid.New()
	store.seed(user.User{ID: id, Email: "s@example.com", DisplayName: "S", Role: user.RoleStaff,
		EmailNotifyAssigned: true, EmailNotifyActivity: true, EmailNotifyGroup: true})

	off := false
	require.NoError(t, svc.SetEmailPreferences(ctx, id, user.EmailPreferencesPatch{Group: &off}))

	got, err := svc.GetByID(ctx, id)
	require.NoError(t, err)
	require.True(t, got.EmailNotifyAssigned, "a switch left out of the patch is left alone")
	require.True(t, got.EmailNotifyActivity)
	require.False(t, got.EmailNotifyGroup)
}
