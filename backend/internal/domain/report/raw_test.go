package report

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// TestRequestAndRaw pins the shape Store.KPI is written against: a Request
// carries the actor, the visibility, the query, both periods and now, and Raw
// carries the unprocessed rows of the eight queries.
func TestRequestAndRaw(t *testing.T) {
	today, err := ParseDay("2026-11-24")
	if err != nil {
		t.Fatal(err)
	}
	cur := Period{From: today.Add(-6), To: today}
	req := Request{
		ActorID:    uuid.New(),
		Visibility: ticket.VisibilityScoped,
		Query:      Query{TZ: "UTC"},
		Current:    cur,
		Previous:   cur.Previous(),
		Now:        time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC),
	}
	if req.Previous.To != cur.From.Add(-1) {
		t.Fatalf("previous period does not end the day before the current one")
	}

	median := int64(60)
	raw := Raw{
		Current:    PeriodRow{Created: 3, Resolved: 2, ResolveMedianSeconds: &median},
		Previous:   PeriodRow{},
		Days:       []ticket.DayVolume{{Day: "2026-11-24", Created: 3, Resolved: 2}},
		ByCategory: []CategoryCount{{ID: uuid.New(), Name: "Network", Count: 3}},
		ByPriority: []PriorityCount{{Priority: ticket.PriorityHigh, Count: 3}},
		Ages:       []AgeCount{{Days: 0, Count: 1}},
		ByAssignee: []Holder{{Count: 1}},
		ByGroup:    []Holder{{Count: 1}},
	}
	if raw.Current.Created != 3 || raw.Previous.Created != 0 || raw.Ages[0].Count != 1 {
		t.Fatalf("unexpected raw: %+v", raw)
	}
}
