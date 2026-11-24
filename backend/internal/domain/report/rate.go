package report

import "math"

// Rate is n/d as a fraction rounded to four places, or nil when d is 0: a rate
// with no tickets under it is no data, not zero.
func Rate(n, d int64) *float64 {
	if d == 0 {
		return nil
	}
	v := round4(float64(n) / float64(d))
	return &v
}

// round4 rounds to four decimal places. A result of zero is +0, so a small
// negative difference does not encode as "-0".
func round4(v float64) float64 {
	r := math.Round(v*10000) / 10000
	if r == 0 {
		return 0
	}
	return r
}
