package userstore_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/userstore"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// CountBelowCost is the number the post-upgrade wizard shows: the accounts the
// old-password reset window (#453) will catch. It is a property of the SQL, so
// it is tested against a real database. Hashes only need the bcrypt prefix.
func TestCountBelowCost(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	t.Cleanup(closeDB)
	q, tx, rollback := testutil.TxQueriesTx(t, db)
	t.Cleanup(rollback)
	store := userstore.New(q)
	ctx := context.Background()
	now := time.Now().UTC()

	body := "abcdefghijklmnopqrstuvabcdefghijklmnopqrstuvabcdefghi"
	add := func(role user.Role, hash, extra string) {
		t.Helper()
		id := uuid.New()
		require.NoError(t, store.Create(ctx, user.User{
			ID: id, Email: id.String() + "@test.local", DisplayName: "x", Role: role, CreatedAt: now, UpdatedAt: now,
		}))
		_, err := tx.ExecContext(ctx, `UPDATE users SET password_hash = $2 `+extra+` WHERE id = $1`, id, hash)
		require.NoError(t, err)
	}
	count := func() int {
		t.Helper()
		n, err := store.CountBelowCost(ctx, 12)
		require.NoError(t, err)
		return n
	}
	before := count()

	add(user.RoleStaff, "$2a$10$"+body, "")                     // counted
	add(user.RoleUser, "$2b$04$"+body, "")                      // counted: any bcrypt prefix, any lower cost
	add(user.RoleStaff, "$2a$12$"+body, "")                     // current cost
	add(user.RoleAdmin, "$2a$10$"+body, "")                     // administrators are exempt
	add(user.RoleStaff, "$2a$10$"+body, ", disabled = true")    // cannot sign in anyway
	add(user.RoleStaff, "$2a$10$"+body, ", deleted_at = now()") // gone
	add(user.RoleStaff, "", "")                                 // SSO only, no password
	add(user.RoleStaff, "not-a-bcrypt-hash", "")                // unreadable: not guessed at

	require.Equal(t, before+2, count())
}
