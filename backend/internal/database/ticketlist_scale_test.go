package database_test

// Not a test of behaviour: it drives GET /tickets' list query against the database
// seeded by scripts/bench-ticket-list/seed.sql and prints timings, and is skipped
// unless BENCH_LIST is set. The buffers come from auto_explain (parse_plans.py).

import (
	"context"
	"fmt"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/database/ticketstore"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
	"github.com/publiciallc/go-help-desk/backend/internal/testutil"
)

func TestListScale(t *testing.T) {
	if os.Getenv("BENCH_LIST") == "" {
		t.Skip()
	}
	db, closeDB := testutil.NewDB(t)
	defer closeDB()
	ctx := context.Background()
	ts := ticketstore.New(db.Queries)
	id := func(s string) uuid.UUID { return uuid.MustParse("00000000-0000-0000-0000-0000000000" + s) }
	callers := []struct {
		name string
		f    ticket.Filter
	}{
		{"staff-in-one-group", ticket.Filter{ActorID: id("b1"), Visibility: ticket.VisibilityScoped, Queue: ticket.QueueAny}},
		{"staff-no-group", ticket.Filter{ActorID: id("b2"), Visibility: ticket.VisibilityScoped, Queue: ticket.QueueAny}},
		{"staff-rare-category", ticket.Filter{ActorID: id("b3"), Visibility: ticket.VisibilityScoped, Queue: ticket.QueueAny}},
		{"staff-rare-type", ticket.Filter{ActorID: id("b4"), Visibility: ticket.VisibilityScoped, Queue: ticket.QueueAny}},
		{"staff-no-group-mine", ticket.Filter{ActorID: id("b2"), Visibility: ticket.VisibilityAll, Queue: ticket.QueueMine}},
		{"admin-mine", ticket.Filter{ActorID: id("a1"), Visibility: ticket.VisibilityAll, Queue: ticket.QueueMine}},
		// The queues of #676. assigned_to_me and my_groups are the two arms of mine,
		// so each should drive from one assignee index, never the created_at walk.
		{"staff-no-group-my-groups", ticket.Filter{ActorID: id("b2"), Visibility: ticket.VisibilityAll, Queue: ticket.QueueMyGroups}},
		{"staff-in-one-group-my-groups", ticket.Filter{ActorID: id("b1"), Visibility: ticket.VisibilityAll, Queue: ticket.QueueMyGroups}},
		{"staff-held-by-nobody", ticket.Filter{ActorID: id("b1"), Visibility: ticket.VisibilityAll, Queue: ticket.QueueMyGroups, HeldBy: ticket.HeldNobody}},
		{"staff-scoped-my-groups", ticket.Filter{ActorID: id("b1"), Visibility: ticket.VisibilityScoped, Queue: ticket.QueueMyGroups}},
		{"staff-assigned-to-me", ticket.Filter{ActorID: id("b1"), Visibility: ticket.VisibilityAll, Queue: ticket.QueueAssignedToMe}},
		{"admin-assigned-to-me", ticket.Filter{ActorID: id("a1"), Visibility: ticket.VisibilityAll, Queue: ticket.QueueAssignedToMe}},
		{"staff-scoped-assigned-to-me", ticket.Filter{ActorID: id("b1"), Visibility: ticket.VisibilityScoped, Queue: ticket.QueueAssignedToMe}},
		{"admin-all", ticket.Filter{ActorID: id("a1"), Visibility: ticket.VisibilityAll, Queue: ticket.QueueAny}},
		{"requester", ticket.Filter{ActorID: id("c1"), Visibility: ticket.VisibilityReporter, Queue: ticket.QueueAny}},
	}
	for _, c := range callers {
		c.f.Limit = 50
		var ds []time.Duration
		rows := 0
		for i := 0; i < 12; i++ {
			_, err := db.SQL.ExecContext(ctx, fmt.Sprintf("SELECT 'MARK %s %d'", c.name, i))
			require.NoError(t, err)
			s := time.Now()
			got, err := ts.ListFiltered(ctx, c.f)
			require.NoError(t, err)
			ds = append(ds, time.Since(s))
			rows = len(got)
		}
		rest := slices.Clone(ds[6:])
		slices.Sort(rest)
		fmt.Printf("BENCH %-22s rows=%-3d run1=%-9v median(7-12)=%-9v max=%v\n", c.name, rows,
			ds[0].Round(100*time.Microsecond), rest[len(rest)/2].Round(100*time.Microsecond), rest[len(rest)-1].Round(100*time.Microsecond))
	}

	// The dashboard's own queries (#675), under the same MARK convention so
	// parse_plans.py reports them: the count behind each queue cell, with and
	// without awaiting_reply, and the trend as an administrator, whose resolved
	// arm must be planned through tickets_done_at_idx (read the plan of "trend-admin").
	counts := []struct {
		name string
		f    ticket.Filter
	}{
		{"count-assigned-to-me", ticket.Filter{ActorID: id("b1"), Visibility: ticket.VisibilityAll, Queue: ticket.QueueAssignedToMe}},
		{"count-assigned-to-me-awaiting", ticket.Filter{ActorID: id("b1"), Visibility: ticket.VisibilityAll, Queue: ticket.QueueAssignedToMe, AwaitingReply: true}},
		{"count-scoped-assigned-to-me", ticket.Filter{ActorID: id("b1"), Visibility: ticket.VisibilityScoped, Queue: ticket.QueueAssignedToMe}},
		{"count-my-groups-awaiting", ticket.Filter{ActorID: id("b1"), Visibility: ticket.VisibilityAll, Queue: ticket.QueueMyGroups, AwaitingReply: true}},
	}
	trends := []struct {
		name string
		f    ticket.Filter
	}{
		{"trend-admin", ticket.Filter{ActorID: id("a1"), Visibility: ticket.VisibilityAll}},
		{"trend-scoped-staff", ticket.Filter{ActorID: id("b1"), Visibility: ticket.VisibilityScoped}},
	}
	timed := func(name string, run func() error) {
		var ds []time.Duration
		for i := 0; i < 12; i++ {
			_, err := db.SQL.ExecContext(ctx, fmt.Sprintf("SELECT 'MARK %s %d'", name, i))
			require.NoError(t, err)
			s := time.Now()
			require.NoError(t, run())
			ds = append(ds, time.Since(s))
		}
		rest := slices.Clone(ds[6:])
		slices.Sort(rest)
		fmt.Printf("BENCH %-30s run1=%-9v median(7-12)=%-9v max=%v\n", name,
			ds[0].Round(100*time.Microsecond), rest[len(rest)/2].Round(100*time.Microsecond), rest[len(rest)-1].Round(100*time.Microsecond))
	}
	for _, c := range counts {
		timed(c.name, func() error { _, err := ts.CountFiltered(ctx, c.f); return err })
	}
	for _, c := range trends {
		timed(c.name, func() error { _, err := ts.VolumeByDay(ctx, c.f, "UTC", time.Now()); return err })
	}
}
