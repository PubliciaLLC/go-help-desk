package report_test

import (
	"testing"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/report"
)

func TestCompare(t *testing.T) {
	cases := []struct {
		name      string
		cur, prev *float64
		kind      report.ChangeKind
		want      report.Change
	}{
		{
			name: "no current, relative",
			cur:  nil, prev: fp(4), kind: report.ChangeRelative,
			want: report.Change{Kind: report.ChangeRelative, State: report.StateNoCurrent},
		},
		{
			name: "no current, points",
			cur:  nil, prev: fp(0.5), kind: report.ChangePoints,
			want: report.Change{Kind: report.ChangePoints, State: report.StateNoCurrent},
		},
		{
			name: "no previous, relative",
			cur:  fp(4), prev: nil, kind: report.ChangeRelative,
			want: report.Change{Kind: report.ChangeRelative, State: report.StateNoPrevious},
		},
		{
			name: "no previous, points",
			cur:  fp(0.5), prev: nil, kind: report.ChangePoints,
			want: report.Change{Kind: report.ChangePoints, State: report.StateNoPrevious},
		},
		{
			name: "both nil: no current comes first",
			cur:  nil, prev: nil, kind: report.ChangeRelative,
			want: report.Change{Kind: report.ChangeRelative, State: report.StateNoCurrent},
		},
		{
			name: "equal counts",
			cur:  fp(7), prev: fp(7), kind: report.ChangeRelative,
			want: report.Change{Kind: report.ChangeRelative, State: report.StateChange, Value: fp(0)},
		},
		{
			name: "equal rates",
			cur:  fp(0.25), prev: fp(0.25), kind: report.ChangePoints,
			want: report.Change{Kind: report.ChangePoints, State: report.StateChange, Value: fp(0)},
		},
		{
			name: "equal zeros, relative: equal wins over from_zero",
			cur:  fp(0), prev: fp(0), kind: report.ChangeRelative,
			want: report.Change{Kind: report.ChangeRelative, State: report.StateChange, Value: fp(0)},
		},
		{
			name: "equal zeros, points",
			cur:  fp(0), prev: fp(0), kind: report.ChangePoints,
			want: report.Change{Kind: report.ChangePoints, State: report.StateChange, Value: fp(0)},
		},
		{
			name: "count up from zero has no relative value",
			cur:  fp(3), prev: fp(0), kind: report.ChangeRelative,
			want: report.Change{Kind: report.ChangeRelative, State: report.StateFromZero},
		},
		{
			name: "rate up from zero is a points change",
			cur:  fp(0.0506), prev: fp(0), kind: report.ChangePoints,
			want: report.Change{Kind: report.ChangePoints, State: report.StateChange, Value: fp(0.0506)},
		},
		{
			name: "count rise",
			cur:  fp(12), prev: fp(10), kind: report.ChangeRelative,
			want: report.Change{Kind: report.ChangeRelative, State: report.StateChange, Value: fp(0.2)},
		},
		{
			name: "count fall",
			cur:  fp(3), prev: fp(4), kind: report.ChangeRelative,
			want: report.Change{Kind: report.ChangeRelative, State: report.StateChange, Value: fp(-0.25)},
		},
		{
			name: "relative change rounds to four places",
			cur:  fp(84), prev: fp(72), kind: report.ChangeRelative,
			want: report.Change{Kind: report.ChangeRelative, State: report.StateChange, Value: fp(0.1667)},
		},
		{
			name: "points rise",
			cur:  fp(0.0506), prev: fp(0.02), kind: report.ChangePoints,
			want: report.Change{Kind: report.ChangePoints, State: report.StateChange, Value: fp(0.0306)},
		},
		{
			name: "points fall",
			cur:  fp(0.8525), prev: fp(0.8835), kind: report.ChangePoints,
			want: report.Change{Kind: report.ChangePoints, State: report.StateChange, Value: fp(-0.031)},
		},
		{
			name: "points rounding",
			cur:  fp(0.9344), prev: fp(0.9223), kind: report.ChangePoints,
			want: report.Change{Kind: report.ChangePoints, State: report.StateChange, Value: fp(0.0121)},
		},
		{
			name: "a difference too small to show rounds to zero and is still a change",
			cur:  fp(0.00001), prev: fp(0), kind: report.ChangePoints,
			want: report.Change{Kind: report.ChangePoints, State: report.StateChange, Value: fp(0)},
		},
		{
			name: "a fall too small to show is zero, not negative zero",
			cur:  fp(0.99999), prev: fp(1), kind: report.ChangePoints,
			want: report.Change{Kind: report.ChangePoints, State: report.StateChange, Value: fp(0)},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := report.Compare(tc.cur, tc.prev, tc.kind)
			if got.Kind != tc.want.Kind || got.State != tc.want.State {
				t.Fatalf("Compare = {%s %s}, want {%s %s}", got.Kind, got.State, tc.want.Kind, tc.want.State)
			}
			switch {
			case got.Value == nil && tc.want.Value == nil:
			case got.Value == nil || tc.want.Value == nil:
				t.Fatalf("Value = %v, want %v", got.Value, tc.want.Value)
			case *got.Value != *tc.want.Value || (*got.Value == 0 && 1/(*got.Value) < 0):
				t.Errorf("Value = %v, want %v", *got.Value, *tc.want.Value)
			}
		})
	}
}

func TestCompared(t *testing.T) {
	want := []struct {
		key  string
		kind report.ChangeKind
	}{
		{"created", report.ChangeRelative},
		{"resolved", report.ChangeRelative},
		{"time_to_resolve_median", report.ChangeRelative},
		{"time_to_resolve_p90", report.ChangeRelative},
		{"first_response_median", report.ChangeRelative},
		{"sla_met_rate", report.ChangePoints},
		{"sla_response_met_rate", report.ChangePoints},
		{"sla_resolution_met_rate", report.ChangePoints},
		{"reopen_rate", report.ChangePoints},
	}
	if len(report.Compared) != len(want) {
		t.Fatalf("Compared has %d entries, want %d", len(report.Compared), len(want))
	}
	seen := map[string]bool{}
	for i, w := range want {
		c := report.Compared[i]
		if c.Key != w.key || c.Kind != w.kind {
			t.Errorf("Compared[%d] = {%s %s}, want {%s %s}", i, c.Key, c.Kind, w.key, w.kind)
		}
		if c.Value == nil {
			t.Errorf("Compared[%d] (%s) has no Value", i, c.Key)
		}
		if seen[c.Key] {
			t.Errorf("key %s appears twice", c.Key)
		}
		seen[c.Key] = true
	}
}

func TestComparedValues(t *testing.T) {
	m := report.Measures{
		Created:  84,
		Resolved: 79,
		TimeToResolve: report.TimeToResolve{
			Count:         79,
			MedianSeconds: ip64(52200),
			P90Seconds:    ip64(345600),
		},
		FirstResponse: report.FirstResponse{Count: 61, MedianSeconds: ip64(3900)},
		SLA: report.SLA{
			Tracked:           61,
			MetRate:           fp(0.8525),
			ResponseMetRate:   fp(0.9344),
			ResolutionMetRate: fp(0.8852),
		},
		Reopen: report.Reopen{Reopened: 4, Rate: fp(0.0506)},
	}
	want := map[string]float64{
		"created":                 84,
		"resolved":                79,
		"time_to_resolve_median":  52200,
		"time_to_resolve_p90":     345600,
		"first_response_median":   3900,
		"sla_met_rate":            0.8525,
		"sla_response_met_rate":   0.9344,
		"sla_resolution_met_rate": 0.8852,
		"reopen_rate":             0.0506,
	}
	for _, c := range report.Compared {
		got := c.Value(m)
		if got == nil {
			t.Errorf("%s = nil, want %v", c.Key, want[c.Key])
			continue
		}
		if *got != want[c.Key] {
			t.Errorf("%s = %v, want %v", c.Key, *got, want[c.Key])
		}
	}
	// A median with no rows is no value, not zero.
	if v := report.Compared[2].Value(report.Measures{}); v != nil {
		t.Errorf("time_to_resolve_median of empty measures = %v, want nil", *v)
	}
}

func ip64(n int64) *int64 { return &n }
