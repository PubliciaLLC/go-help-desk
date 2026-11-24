// Package report is the domain of the KPI reports (#131): the calendar days a
// report is counted in and the store contract it reads through.
package report

import (
	"fmt"
	"time"
)

// Day is a calendar date, held as the time.Time at 00:00 UTC that names it.
// Only the date means anything: it is the local date in some time zone, which
// is chosen by whoever produced it, not carried here.
type Day time.Time

const dayLayout = "2006-01-02"

// ParseDay reads YYYY-MM-DD, and nothing else.
func ParseDay(s string) (Day, error) {
	t, err := time.Parse(dayLayout, s)
	if err != nil {
		return Day{}, fmt.Errorf("parsing day %q: %w", s, err)
	}
	return Day(t), nil
}

// Add is the day n calendar days later (earlier when n is negative).
func (d Day) Add(n int) Day { return Day(time.Time(d).AddDate(0, 0, n)) }

// Time is the day as a time.Time at 00:00 UTC.
func (d Day) Time() time.Time { return time.Time(d) }

// String is YYYY-MM-DD.
func (d Day) String() string { return time.Time(d).Format(dayLayout) }
