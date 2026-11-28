package report_test

import (
	"testing"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/report"
)

func TestPeriodDays(t *testing.T) {
	cases := []struct {
		name     string
		from, to string
		want     int
	}{
		{"one day", "2026-11-10", "2026-11-10", 1},
		{"a month", "2026-10-26", "2026-11-24", 30},
		{"over a leap day", "2024-02-28", "2024-03-01", 3},
		{"a non-leap February", "2026-02-01", "2026-02-28", 28},
		{"366 days", "2025-11-24", "2026-11-24", 366},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := report.Period{From: mustDay(t, tc.from), To: mustDay(t, tc.to)}
			if got := p.Days(); got != tc.want {
				t.Errorf("Days() = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestPeriodPrevious(t *testing.T) {
	cases := []struct {
		name     string
		from, to string
		wantFrom string
		wantTo   string
	}{
		{"a month back", "2026-10-26", "2026-11-24", "2026-09-26", "2026-10-25"},
		{"a 31-day month after a short month", "2026-03-01", "2026-03-31", "2026-01-29", "2026-02-28"},
		{"a single day after a leap day", "2024-03-01", "2024-03-01", "2024-02-29", "2024-02-29"},
		{"a range across a year end", "2026-01-01", "2026-01-07", "2025-12-25", "2025-12-31"},
		{"a range ending on a leap day", "2024-02-20", "2024-02-29", "2024-02-10", "2024-02-19"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := report.Period{From: mustDay(t, tc.from), To: mustDay(t, tc.to)}
			got := p.Previous()
			if got.From.String() != tc.wantFrom || got.To.String() != tc.wantTo {
				t.Errorf("Previous() = %s..%s, want %s..%s", got.From, got.To, tc.wantFrom, tc.wantTo)
			}
			if got.Days() != p.Days() {
				t.Errorf("Previous() has %d days, want %d", got.Days(), p.Days())
			}
		})
	}
}
