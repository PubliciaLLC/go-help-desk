package report

import (
	"net/url"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// DefaultDays is the length of the period when a request names none: the days
// ending today.
const DefaultDays = 30

// MaxDays is the longest period one request may ask for. It bounds what one
// request scans.
const MaxDays = 366

// PriorityOrder is the order priorities are listed in, most urgent first.
var PriorityOrder = []ticket.Priority{
	ticket.PriorityCritical,
	ticket.PriorityHigh,
	ticket.PriorityMedium,
	ticket.PriorityLow,
}

// earliestFrom is the earliest from a request may name.
var earliestFrom = time.Date(1970, time.January, 1, 0, 0, 0, 0, time.UTC)

// maxPriorities is how many priority values one request may name: all four.
const maxPriorities = 4

// Query is the parsed parameters of a KPI request. Every field is optional. A
// nil From and To means the default period; a nil filter means no filter.
// TZ is the caller's zone as sent: the handler checks it, not ParseQuery.
type Query struct {
	TZ         string
	From, To   *Day
	CategoryID *uuid.UUID
	Priorities []ticket.Priority
	GroupID    *uuid.UUID
}

// ParseQuery reads the parameters of GET /reports/kpi. It checks them in a
// fixed order and returns the first failure as a *ticket.FilterError of kind
// FilterInvalid, so the handler answers with its message. An empty value is
// the same as an absent one.
func ParseQuery(q url.Values) (Query, error) {
	var out Query
	out.TZ = q.Get("tz")

	from, to := q.Get("from"), q.Get("to")
	if (from == "") != (to == "") {
		return Query{}, invalid("from and to go together")
	}
	if from != "" {
		d, err := ParseDay(from)
		if err != nil {
			return Query{}, invalid("invalid from")
		}
		out.From = &d
	}
	if to != "" {
		d, err := ParseDay(to)
		if err != nil {
			return Query{}, invalid("invalid to")
		}
		out.To = &d
	}

	if v := q.Get("category_id"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			return Query{}, invalid("invalid category_id")
		}
		out.CategoryID = &id
	}

	values := nonEmpty(q["priority"])
	if len(values) > maxPriorities {
		return Query{}, invalid("too many priority values")
	}
	for _, v := range values {
		p := ticket.Priority(v)
		if !p.Valid() {
			return Query{}, invalid("invalid priority")
		}
		out.Priorities = append(out.Priorities, p)
	}
	slices.SortFunc(out.Priorities, comparePriority)
	// A value sent twice is one filter; the count limit above still counts both.
	out.Priorities = slices.Compact(out.Priorities)

	if v := q.Get("assignee_group_id"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			return Query{}, invalid("invalid assignee_group_id")
		}
		out.GroupID = &id
	}
	return out, nil
}

// Period is the period the query asks for, measured from today. Both dates
// absent is the DefaultDays ending today. A range that is not whole (one end
// missing) is refused, as ParseQuery refuses it.
func (q Query) Period(today Day) (Period, error) {
	if q.From == nil && q.To == nil {
		return Period{From: today.Add(1 - DefaultDays), To: today}, nil
	}
	if q.From == nil || q.To == nil {
		return Period{}, invalid("from and to go together")
	}
	p := Period{From: *q.From, To: *q.To}
	if p.From.Time().After(p.To.Time()) {
		return Period{}, invalid("from must not be after to")
	}
	// The previous period starts as many days before from; a date near year 1
	// would put it before the year Postgres can represent, a 500 instead of a 400.
	if p.From.Time().Before(earliestFrom) {
		return Period{}, invalid("from is too early")
	}
	if p.Days() > MaxDays {
		return Period{}, invalid("the range is longer than 366 days")
	}
	return p, nil
}

// invalid is a refused request in the wording the API answers with.
func invalid(msg string) error {
	return &ticket.FilterError{Kind: ticket.FilterInvalid, Msg: msg}
}

// nonEmpty drops the empty values of a repeatable key.
func nonEmpty(vs []string) []string {
	var out []string
	for _, v := range vs {
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

// comparePriority orders priorities by PriorityOrder.
func comparePriority(a, b ticket.Priority) int {
	return slices.Index(PriorityOrder, a) - slices.Index(PriorityOrder, b)
}
