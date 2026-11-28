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

// Searching the change details costs a read of every row it covers, so it runs
// only inside a date range of at most audit.DetailsSearchMaxDays (#629, owner
// 2026-11-11). Outside one, q still matches entity, action and actor.
func TestSearch_ChangeDetailsAreSearchedOnlyInsideADateRange(t *testing.T) {
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	q, tx, rollback := testutil.TxQueriesTx(t, db)
	defer rollback()
	ctx := context.Background()

	actor := uuid.New()
	require.NoError(t, userstore.New(q).Create(ctx, user.User{
		ID: actor, Email: "range-" + actor.String() + "@test.local", DisplayName: "Range Actor", Role: user.RoleStaff,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}))
	// Entries 0, 100 and 200 days old, each with the needle in a shown key.
	now := time.Now().UTC().Truncate(time.Second)
	ages := []int{200, 100, 0}
	ids := make([]uuid.UUID, len(ages))
	for i, age := range ages {
		ids[i] = uuid.New()
		_, err := tx.ExecContext(ctx, `INSERT INTO audit_log (id, actor_id, entity_type, entity_id, action, after, created_at)
			VALUES ($1, $2, 'range', $3, $4, '{"scope_path": "zz-range-needle"}', $5)`,
			ids[i], actor, uuid.New(), "act_"+string(rune('a'+i)), now.AddDate(0, 0, -age))
		require.NoError(t, err)
	}

	s := auditstore.New(q)
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
	const needle = "zz-range-needle"
	rangeOf := func(q string, span time.Duration) audit.Filter {
		from, to := now.Add(-span), now
		return audit.Filter{Q: q, From: &from, To: &to}
	}
	day := 24 * time.Hour
	maxSpan := audit.DetailsSearchMaxSpan

	require.ElementsMatch(t, ids[2:], found(rangeOf(needle, 30*day)), "inside the limit, the details of what the range holds are searched")
	require.ElementsMatch(t, ids[2:], found(rangeOf(needle, maxSpan)), "a range of exactly the limit is inside it")
	require.Empty(t, found(rangeOf(needle, maxSpan+time.Second)), "a second over the limit, the details are not searched")
	require.Empty(t, found(rangeOf(needle, 150*day)), "a range over the limit is the same as none, though it holds a hit")

	require.Empty(t, found(audit.Filter{Q: needle}), "no range, no search of the details")
	from, to := now.Add(-30*day), now
	require.Empty(t, found(audit.Filter{Q: needle, From: &from}), "half a range is no range")
	require.Empty(t, found(audit.Filter{Q: needle, To: &to}), "half a range is no range")

	// What is cheap to search is searched whatever the range.
	require.ElementsMatch(t, ids[:1], found(audit.Filter{Q: "act_a"}), "by action, with no range")
	require.ElementsMatch(t, ids[1:2], found(rangeOf("act_b", 150*day)), "by action, over the limit")
	require.Empty(t, found(rangeOf("act_a", 150*day)), "the range still limits which entries are returned")
	require.ElementsMatch(t, ids, found(audit.Filter{Q: "range Actor"}), "by the actor's name, with no range")
}
