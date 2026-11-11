package auditstore_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/auditstore"
	"github.com/publiciallc/go-help-desk/backend/internal/database/userstore"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/audit"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/user"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

// Searching the change details costs a read of every row it covers, so it
// covers the newest DiffSearchWindow entries that pass the other filters, and
// no more (#629, measured at 2,000,000 entries). Older entries are still found
// by entity, action and actor; a date range moves the window back.
func TestSearch_ChangeDetailsAreSearchedInTheNewestWindowOnly(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, tx, rollback := testutil.TxQueriesTx(t, db)
	defer rollback()
	ctx := context.Background()

	actor := uuid.New()
	require.NoError(t, userstore.New(q).Create(ctx, user.User{
		ID: actor, Email: "window-" + actor.String() + "@test.local", DisplayName: "Window Actor", Role: user.RoleStaff,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}))
	// Five entries, one a day, oldest first, each with the needle in a shown key.
	base := time.Now().UTC().Truncate(time.Second).AddDate(0, 0, -10)
	ids := make([]uuid.UUID, 5)
	for i := range ids {
		ids[i] = uuid.New()
		_, err := tx.ExecContext(ctx, `INSERT INTO audit_log (id, actor_id, entity_type, entity_id, action, after, created_at)
			VALUES ($1, $2, 'window', $3, $4, '{"scope_path": "zz-window-needle"}', $5)`,
			ids[i], actor, uuid.New(), "act_"+string(rune('a'+i)), base.AddDate(0, 0, i))
		require.NoError(t, err)
	}

	s := auditstore.NewWithDiffWindowForTest(q, 3)
	found := func(f audit.Filter) []uuid.UUID {
		f.ActorID = &actor
		pg, err := s.Search(ctx, f, 50, 0)
		require.NoError(t, err)
		var out []uuid.UUID
		for _, e := range pg.Entries {
			out = append(out, e.ID)
		}
		return out
	}

	require.ElementsMatch(t, ids[2:], found(audit.Filter{Q: "zz-window-needle"}),
		"the newest 3 of 5: the change details of the older two are not searched")
	require.ElementsMatch(t, ids[:1], found(audit.Filter{Q: "act_a"}),
		"an older entry is still found by what is cheap to search, its action")

	from := base.AddDate(0, 0, -1)
	to := base.AddDate(0, 0, 3)
	require.ElementsMatch(t, ids[1:4], found(audit.Filter{Q: "zz-window-needle", From: &from, To: &to}),
		"a date range moves the window back: the newest 3 inside it, which now reaches entries the first search did not")
}
