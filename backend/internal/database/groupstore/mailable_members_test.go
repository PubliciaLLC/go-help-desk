package groupstore_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/groupstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/userstore"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/group"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// The members staff mail can go to (#441): not a service account (#490), not a
// disabled or deleted account, and not somebody moved to a requester role.
// The mail path filters again in Go (User.CanReceiveStaffMail); this is the
// query refusing to offer them at all.
func TestListMailableMembers_LeavesOutWhoCannotBeMailed(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, tx, rollback := testutil.TxQueriesTx(t, db)
	defer rollback()
	ctx := context.Background()
	users, groups := userstore.New(q), groupstore.New(q)

	make := func(name string, role user.Role) uuid.UUID {
		u := user.User{ID: uuid.New(), Email: name + "-" + uuid.NewString() + "@test.local", DisplayName: name,
			Role: role, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
		require.NoError(t, users.Create(ctx, u))
		return u.ID
	}
	ok := make("ok", user.RoleStaff)
	admin := make("admin", user.RoleAdmin)
	bot := make("bot", user.RoleStaff)
	disabled := make("disabled", user.RoleStaff)
	deleted := make("deleted", user.RoleStaff)
	requester := make("requester", user.RoleUser)

	_, err := tx.ExecContext(ctx, `UPDATE users SET service_account = TRUE WHERE id = $1`, bot)
	require.NoError(t, err)
	require.NoError(t, users.Disable(ctx, disabled))
	require.NoError(t, users.SoftDelete(ctx, deleted))

	g := group.Group{ID: uuid.New(), Name: "Desk " + uuid.NewString()}
	require.NoError(t, groups.Create(ctx, g))
	for _, id := range []uuid.UUID{ok, admin, bot, disabled, deleted, requester} {
		require.NoError(t, groups.AddMember(ctx, g.ID, id))
	}

	got, err := groups.ListMailableMembers(ctx, g.ID)
	require.NoError(t, err)
	require.ElementsMatch(t, []uuid.UUID{ok, admin}, got)

	// The plain member list is unchanged: this is a different question.
	all, err := groups.ListMembers(ctx, g.ID)
	require.NoError(t, err)
	require.Len(t, all, 6)
}
