package report

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"
)

// Service turns what the store read into the KPI the page shows.
type Service struct {
	store Store
}

// NewService returns a Service reading through store.
func NewService(store Store) *Service { return &Service{store: store} }

// Today is Store.Today: the local date of now in tz. A zone Postgres does not
// know is ticket.ErrUnknownTimeZone.
func (s *Service) Today(ctx context.Context, tz string, now time.Time) (Day, error) {
	return s.store.Today(ctx, tz, now)
}

// KPI reads one request's rows and computes what the response carries: the
// rates, the change of each compared measure, the backlog in its buckets and
// every list in its order.
func (s *Service) KPI(ctx context.Context, req Request) (KPI, error) {
	raw, err := s.store.KPI(ctx, req)
	if err != nil {
		return KPI{}, fmt.Errorf("reading the KPI rows: %w", err)
	}
	cur, prev := measures(raw.Current), measures(raw.Previous)
	change := make(map[string]Change, len(Compared))
	for _, m := range Compared {
		change[m.Key] = Compare(m.Value(cur), m.Value(prev), m.Kind)
	}

	buckets := Buckets(raw.Ages)
	var backlog int64
	for _, b := range buckets {
		backlog += b.Count
	}
	byAssignee, byGroup := holders(raw.ByAssignee), holders(raw.ByGroup)

	return KPI{
		Current:  cur,
		Previous: prev,
		Change:   change,
		Volume: Volume{
			Days:       raw.Days,
			ByCategory: categories(raw.ByCategory),
			ByPriority: priorities(raw.ByPriority),
		},
		Backlog:  Backlog{Total: backlog, Buckets: buckets},
		Workload: Workload{Total: total(byAssignee), ByAssignee: byAssignee, ByGroup: byGroup},
	}, nil
}

// measures is a period's row with its rates taken. A median the store left nil
// stays nil; a rate over an empty population is nil (Rate).
func measures(r PeriodRow) Measures {
	return Measures{
		Created:       r.Created,
		Resolved:      r.Resolved,
		TimeToResolve: TimeToResolve{Count: r.Resolved, MedianSeconds: r.ResolveMedianSeconds, P90Seconds: r.ResolveP90Seconds},
		FirstResponse: FirstResponse{Count: r.FirstResponseCount, MedianSeconds: r.FirstResponseMedianSeconds},
		SLA: SLA{
			Tracked:           r.SLATracked,
			Met:               r.SLAMet,
			ResponseMet:       r.SLAResponseMet,
			ResolutionMet:     r.SLAResolutionMet,
			MetRate:           Rate(r.SLAMet, r.SLATracked),
			ResponseMetRate:   Rate(r.SLAResponseMet, r.SLATracked),
			ResolutionMetRate: Rate(r.SLAResolutionMet, r.SLATracked),
		},
		Reopen: Reopen{Reopened: r.Reopened, Rate: Rate(r.Reopened, r.Resolved)},
	}
}

// categories is rows ordered by count, largest first, then by name.
func categories(rows []CategoryCount) []CategoryCount {
	out := slices.Clone(rows)
	slices.SortStableFunc(out, func(a, b CategoryCount) int {
		if a.Count != b.Count {
			return int(b.Count - a.Count)
		}
		return strings.Compare(a.Name, b.Name)
	})
	return out
}

// priorities is one row for each of PriorityOrder, in that order, zeros
// included.
func priorities(rows []PriorityCount) []PriorityCount {
	out := make([]PriorityCount, len(PriorityOrder))
	for i, p := range PriorityOrder {
		out[i].Priority = p
	}
	for _, r := range rows {
		if i := slices.Index(PriorityOrder, r.Priority); i >= 0 {
			out[i].Count += r.Count
		}
	}
	return out
}

// holders is rows ordered by count, largest first, then by name, with the row
// that has no holder (nil ID) last whatever its count.
func holders(rows []Holder) []Holder {
	out := slices.Clone(rows)
	slices.SortStableFunc(out, func(a, b Holder) int {
		switch {
		case (a.ID == nil) != (b.ID == nil):
			if a.ID == nil {
				return 1
			}
			return -1
		case a.Count != b.Count:
			return int(b.Count - a.Count)
		case a.Name != nil && b.Name != nil:
			return strings.Compare(*a.Name, *b.Name)
		}
		return 0
	})
	return out
}

func total(rows []Holder) int64 {
	var n int64
	for _, r := range rows {
		n += r.Count
	}
	return n
}
