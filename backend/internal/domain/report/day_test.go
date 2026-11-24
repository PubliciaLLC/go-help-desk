package report_test

import (
	"testing"
	"time"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/report"
)

func TestParseDay(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{in: "2026-06-15", want: "2026-06-15"},
		{in: "2024-02-29", want: "2024-02-29"},
		{in: "2026-02-29", wantErr: true},
		{in: "2026-13-01", wantErr: true},
		{in: "2026-6-5", wantErr: true},
		{in: "2026-06-15T00:00:00Z", wantErr: true},
		{in: "15/06/2026", wantErr: true},
		{in: " 2026-06-15", wantErr: true},
		{in: "", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got, err := report.ParseDay(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseDay(%q) = %v, want an error", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseDay(%q): %v", tc.in, err)
			}
			if got.String() != tc.want {
				t.Errorf("ParseDay(%q) = %s, want %s", tc.in, got, tc.want)
			}
			tm := got.Time()
			if want := time.Date(tm.Year(), tm.Month(), tm.Day(), 0, 0, 0, 0, time.UTC); !tm.Equal(want) || tm.Location() != time.UTC {
				t.Errorf("ParseDay(%q) is %v, want 00:00 UTC", tc.in, tm)
			}
		})
	}
}

func TestDayAdd(t *testing.T) {
	cases := []struct {
		name string
		from string
		n    int
		want string
	}{
		{"zero", "2026-06-15", 0, "2026-06-15"},
		{"one", "2026-06-15", 1, "2026-06-16"},
		{"back 29", "2026-06-15", -29, "2026-05-17"},
		{"month end", "2026-01-31", 1, "2026-02-01"},
		{"leap day", "2024-02-28", 1, "2024-02-29"},
		{"year boundary back", "2026-01-01", -1, "2025-12-31"},
		{"365 days", "2026-01-01", 365, "2027-01-01"},
		{"366 days over a leap day", "2024-01-01", 366, "2025-01-01"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, err := report.ParseDay(tc.from)
			if err != nil {
				t.Fatal(err)
			}
			if got := d.Add(tc.n).String(); got != tc.want {
				t.Errorf("%s.Add(%d) = %s, want %s", tc.from, tc.n, got, tc.want)
			}
		})
	}
}
