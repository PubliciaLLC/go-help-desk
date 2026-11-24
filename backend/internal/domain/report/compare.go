package report

// ChangeKind says how a measure's period-over-period change is expressed.
type ChangeKind string

const (
	// ChangeRelative is (cur − prev) / prev, for counts and durations.
	ChangeRelative ChangeKind = "relative"
	// ChangePoints is cur − prev, for rates (both are fractions).
	ChangePoints ChangeKind = "points"
)

// ChangeState says which rule of the change table applied.
type ChangeState string

const (
	StateChange     ChangeState = "change"
	StateFromZero   ChangeState = "from_zero"
	StateNoPrevious ChangeState = "no_previous"
	StateNoCurrent  ChangeState = "no_current"
)

// Change is the period-over-period change of one measure. Value is nil unless
// State is StateChange.
type Change struct {
	Kind  ChangeKind
	State ChangeState
	Value *float64
}

// Compare applies the change table of DESIGN.md, the first rule that holds:
// no current value; no previous value; equal values (a change of 0); a
// relative change from 0 (no value); otherwise the change, rounded to four
// places. Any kind other than ChangeRelative is taken as ChangePoints.
func Compare(cur, prev *float64, kind ChangeKind) Change {
	switch {
	case cur == nil:
		return Change{Kind: kind, State: StateNoCurrent}
	case prev == nil:
		return Change{Kind: kind, State: StateNoPrevious}
	case *cur == *prev:
		return Change{Kind: kind, State: StateChange, Value: fp(0)}
	case kind == ChangeRelative && *prev == 0:
		return Change{Kind: kind, State: StateFromZero}
	}
	var v float64
	if kind == ChangeRelative {
		v = (*cur - *prev) / *prev
	} else {
		v = *cur - *prev
	}
	v = round4(v)
	return Change{Kind: kind, State: StateChange, Value: &v}
}

// ComparedMeasure is one measure the page compares with the previous period.
// Value reads it from a Measures; nil means no value.
type ComparedMeasure struct {
	Key   string
	Kind  ChangeKind
	Value func(Measures) *float64
}

// Compared are the nine compared measures, in the order the response lists
// them.
var Compared = []ComparedMeasure{
	{"created", ChangeRelative, func(m Measures) *float64 { return count(m.Created) }},
	{"resolved", ChangeRelative, func(m Measures) *float64 { return count(m.Resolved) }},
	{"time_to_resolve_median", ChangeRelative, func(m Measures) *float64 { return seconds(m.TimeToResolve.MedianSeconds) }},
	{"time_to_resolve_p90", ChangeRelative, func(m Measures) *float64 { return seconds(m.TimeToResolve.P90Seconds) }},
	{"first_response_median", ChangeRelative, func(m Measures) *float64 { return seconds(m.FirstResponse.MedianSeconds) }},
	{"sla_met_rate", ChangePoints, func(m Measures) *float64 { return m.SLA.MetRate }},
	{"sla_response_met_rate", ChangePoints, func(m Measures) *float64 { return m.SLA.ResponseMetRate }},
	{"sla_resolution_met_rate", ChangePoints, func(m Measures) *float64 { return m.SLA.ResolutionMetRate }},
	{"reopen_rate", ChangePoints, func(m Measures) *float64 { return m.Reopen.Rate }},
}

func fp(f float64) *float64 { return &f }

func count(n int64) *float64 { return fp(float64(n)) }

func seconds(n *int64) *float64 {
	if n == nil {
		return nil
	}
	return count(*n)
}
