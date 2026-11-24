package report

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

type fakeStore struct {
	raw      Raw
	err      error
	today    Day
	gotTZ    string
	gotNow   time.Time
	gotReq   Request
	kpiCalls int
}

func (f *fakeStore) Today(_ context.Context, tz string, now time.Time) (Day, error) {
	f.gotTZ, f.gotNow = tz, now
	return f.today, f.err
}

func (f *fakeStore) KPI(_ context.Context, req Request) (Raw, error) {
	f.kpiCalls++
	f.gotReq = req
	return f.raw, f.err
}

func sp(s string) *string { return &s }

func idp(id uuid.UUID) *uuid.UUID { return &id }

func ip(n int64) *int64 { return &n }

func TestServiceToday_PassesThrough(t *testing.T) {
	d, err := ParseDay("2026-11-24")
	if err != nil {
		t.Fatal(err)
	}
	st := &fakeStore{today: d}
	now := time.Date(2026, 11, 24, 3, 0, 0, 0, time.UTC)
	got, err := NewService(st).Today(context.Background(), "Europe/London", now)
	if err != nil || got != d || st.gotTZ != "Europe/London" || !st.gotNow.Equal(now) {
		t.Fatalf("got %v, %v; store saw %q %v", got, err, st.gotTZ, st.gotNow)
	}

	boom := errors.New("boom")
	_, err = NewService(&fakeStore{err: boom}).Today(context.Background(), "UTC", now)
	if !errors.Is(err, boom) {
		t.Fatalf("a store error must come back as it is: %v", err)
	}
}

func TestServiceKPI_StoreErrorIsWrapped(t *testing.T) {
	boom := errors.New("boom")
	_, err := NewService(&fakeStore{err: boom}).KPI(context.Background(), Request{})
	if !errors.Is(err, boom) {
		t.Fatalf("got %v", err)
	}
}

func TestServiceKPI_PassesTheRequestOn(t *testing.T) {
	st := &fakeStore{}
	req := Request{ActorID: uuid.New(), Query: Query{TZ: "UTC"}, Now: time.Date(2026, 11, 24, 0, 0, 0, 0, time.UTC)}
	if _, err := NewService(st).KPI(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if st.kpiCalls != 1 || st.gotReq.ActorID != req.ActorID || !st.gotReq.Now.Equal(req.Now) {
		t.Fatalf("store saw %+v after %d calls", st.gotReq, st.kpiCalls)
	}
}

func TestServiceKPI_Measures(t *testing.T) {
	raw := Raw{
		Current: PeriodRow{
			Created: 84, Resolved: 80,
			ResolveMedianSeconds: ip(52200), ResolveP90Seconds: ip(345600),
			FirstResponseCount: 61, FirstResponseMedianSeconds: ip(3900),
			SLATracked: 60, SLAMet: 45, SLAResponseMet: 57, SLAResolutionMet: 54,
			Reopened: 4,
		},
		Previous: PeriodRow{Created: 72, Resolved: 0},
	}
	got, err := NewService(&fakeStore{raw: raw}).KPI(context.Background(), Request{})
	if err != nil {
		t.Fatal(err)
	}
	c := got.Current
	if c.Created != 84 || c.Resolved != 80 {
		t.Fatalf("counts: %+v", c)
	}
	if c.TimeToResolve.Count != 80 || *c.TimeToResolve.MedianSeconds != 52200 || *c.TimeToResolve.P90Seconds != 345600 {
		t.Fatalf("time to resolve: %+v", c.TimeToResolve)
	}
	if c.FirstResponse.Count != 61 || *c.FirstResponse.MedianSeconds != 3900 {
		t.Fatalf("first response: %+v", c.FirstResponse)
	}
	s := c.SLA
	if s.Tracked != 60 || s.Met != 45 || s.ResponseMet != 57 || s.ResolutionMet != 54 ||
		*s.MetRate != 0.75 || *s.ResponseMetRate != 0.95 || *s.ResolutionMetRate != 0.9 {
		t.Fatalf("sla: %+v", s)
	}
	if c.Reopen.Reopened != 4 || *c.Reopen.Rate != 0.05 {
		t.Fatalf("reopen: %+v", c.Reopen)
	}

	p := got.Previous
	if p.Created != 72 || p.TimeToResolve.MedianSeconds != nil || p.FirstResponse.MedianSeconds != nil ||
		p.SLA.MetRate != nil || p.SLA.ResponseMetRate != nil || p.SLA.ResolutionMetRate != nil || p.Reopen.Rate != nil {
		t.Fatalf("an empty population is nil, not zero: %+v", p)
	}
}

func TestServiceKPI_Change(t *testing.T) {
	raw := Raw{
		// Reopen rate 0 / 5 = 0.0 last period (data), 1 / 5 = 0.2 now.
		Previous: PeriodRow{Created: 0, Resolved: 5, ResolveMedianSeconds: ip(100), ResolveP90Seconds: ip(100)},
		Current:  PeriodRow{Created: 10, Resolved: 5, ResolveMedianSeconds: ip(150), ResolveP90Seconds: ip(100), Reopened: 1},
	}
	got, err := NewService(&fakeStore{raw: raw}).KPI(context.Background(), Request{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Change) != 9 || len(got.Change) != len(Compared) {
		t.Fatalf("want the nine compared measures, got %d: %v", len(got.Change), got.Change)
	}
	for _, m := range Compared {
		c, ok := got.Change[m.Key]
		if !ok || c.Kind != m.Kind {
			t.Fatalf("%s: %+v", m.Key, c)
		}
	}
	cases := []struct {
		key   string
		state ChangeState
		value *float64
	}{
		{"created", StateFromZero, nil},
		{"resolved", StateChange, fp(0)},
		{"time_to_resolve_median", StateChange, fp(0.5)},
		{"time_to_resolve_p90", StateChange, fp(0)},
		{"first_response_median", StateNoCurrent, nil},
		{"sla_met_rate", StateNoCurrent, nil},
		{"reopen_rate", StateChange, fp(0.2)}, // a previous rate of 0.0 is data: points
	}
	for _, tc := range cases {
		c := got.Change[tc.key]
		if c.State != tc.state || (tc.value == nil) != (c.Value == nil) || (tc.value != nil && *c.Value != *tc.value) {
			t.Errorf("%s: got %+v, want %s %v", tc.key, c, tc.state, tc.value)
		}
	}
	if got.Change["reopen_rate"].Kind != ChangePoints {
		t.Errorf("a rate's change is in points")
	}
}

func TestServiceKPI_Volume(t *testing.T) {
	a, b, c := uuid.New(), uuid.New(), uuid.New()
	raw := Raw{
		Days: []ticket.DayVolume{{Day: "2026-11-23", Created: 1, Resolved: 2}, {Day: "2026-11-24", Created: 3}},
		ByCategory: []CategoryCount{
			{ID: c, Name: "Zulu", Count: 5},
			{ID: a, Name: "Beta", Count: 2},
			{ID: b, Name: "Alpha", Count: 2},
			{ID: uuid.New(), Name: "Top", Count: 9},
		},
		ByPriority: []PriorityCount{
			{Priority: ticket.PriorityLow, Count: 4},
			{Priority: ticket.PriorityHigh, Count: 1},
		},
	}
	got, err := NewService(&fakeStore{raw: raw}).KPI(context.Background(), Request{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Volume.Days) != 2 || got.Volume.Days[0].Day != "2026-11-23" || got.Volume.Days[1].Created != 3 {
		t.Fatalf("days: %+v", got.Volume.Days)
	}
	var names []string
	for _, r := range got.Volume.ByCategory {
		names = append(names, r.Name)
	}
	if want := []string{"Top", "Zulu", "Alpha", "Beta"}; !equalStrings(names, want) {
		t.Fatalf("categories by count then name: %v, want %v", names, want)
	}
	want := []PriorityCount{
		{ticket.PriorityCritical, 0}, {ticket.PriorityHigh, 1}, {ticket.PriorityMedium, 0}, {ticket.PriorityLow, 4},
	}
	if len(got.Volume.ByPriority) != 4 {
		t.Fatalf("the four priorities always: %+v", got.Volume.ByPriority)
	}
	for i, w := range want {
		if got.Volume.ByPriority[i] != w {
			t.Fatalf("priority %d: got %+v, want %+v", i, got.Volume.ByPriority[i], w)
		}
	}
}

func TestServiceKPI_NothingIsStillFourPrioritiesAndFourBuckets(t *testing.T) {
	got, err := NewService(&fakeStore{}).KPI(context.Background(), Request{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Volume.ByPriority) != 4 || len(got.Backlog.Buckets) != 4 {
		t.Fatalf("zeros, in order: %+v %+v", got.Volume.ByPriority, got.Backlog)
	}
	for i, p := range PriorityOrder {
		if got.Volume.ByPriority[i] != (PriorityCount{Priority: p}) {
			t.Fatalf("%d: %+v", i, got.Volume.ByPriority[i])
		}
	}
}

func TestServiceKPI_Backlog(t *testing.T) {
	raw := Raw{Ages: []AgeCount{{0, 2}, {2, 1}, {3, 4}, {7, 1}, {8, 3}, {30, 1}, {31, 5}}}
	got, err := NewService(&fakeStore{raw: raw}).KPI(context.Background(), Request{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Backlog.Total != 17 {
		t.Fatalf("total %d", got.Backlog.Total)
	}
	var counts []int64
	for _, b := range got.Backlog.Buckets {
		counts = append(counts, b.Count)
	}
	if len(counts) != 4 || counts[0] != 3 || counts[1] != 5 || counts[2] != 4 || counts[3] != 5 {
		t.Fatalf("buckets %v", counts)
	}
}

func TestServiceKPI_Workload(t *testing.T) {
	ada, bob, cy := uuid.New(), uuid.New(), uuid.New()
	g1, g2 := uuid.New(), uuid.New()
	raw := Raw{
		ByAssignee: []Holder{
			{Count: 9}, // nobody, the biggest: still last
			{ID: idp(bob), Name: sp("Bob"), Count: 2},
			{ID: idp(ada), Name: sp("Ada"), Count: 2},
			{ID: idp(cy), Name: sp("Cy"), Count: 4},
		},
		ByGroup: []Holder{
			{ID: idp(g2), Name: sp("Network"), Count: 1},
			{Count: 1},
			{ID: idp(g1), Name: sp("Desk"), Count: 16},
		},
	}
	got, err := NewService(&fakeStore{raw: raw}).KPI(context.Background(), Request{})
	if err != nil {
		t.Fatal(err)
	}
	w := got.Workload
	if w.Total != 17 {
		t.Fatalf("total %d", w.Total)
	}
	names := func(hs []Holder) []string {
		var out []string
		for _, h := range hs {
			if h.Name == nil {
				out = append(out, "-")
			} else {
				out = append(out, *h.Name)
			}
		}
		return out
	}
	if want := []string{"Cy", "Ada", "Bob", "-"}; !equalStrings(names(w.ByAssignee), want) {
		t.Fatalf("people: %v, want %v", names(w.ByAssignee), want)
	}
	if w.ByAssignee[3].ID != nil || w.ByAssignee[3].Count != 9 {
		t.Fatalf("the NULL row keeps its count: %+v", w.ByAssignee[3])
	}
	if want := []string{"Desk", "Network", "-"}; !equalStrings(names(w.ByGroup), want) {
		t.Fatalf("groups: %v, want %v", names(w.ByGroup), want)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
