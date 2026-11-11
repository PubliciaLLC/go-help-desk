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
}
