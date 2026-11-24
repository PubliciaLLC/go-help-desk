package server

import (
	"testing"
	"time"
)

// TestTransferTimeout pins the exact deadline for a body of n bytes. The
// values are compared with == on purpose: the rule is exact at whole MiB and
// a tolerance would hide a truncating rewrite.
func TestTransferTimeout(t *testing.T) {
	const mib = 1 << 20
	cases := []struct {
		name string
		n    int64
		want time.Duration
	}{
		{name: "empty body", n: 0, want: 5 * time.Minute},
		{name: "1 MiB", n: 1 * mib, want: 5 * time.Minute},
		{name: "25 MiB anchor", n: 25 * mib, want: 5 * time.Minute},
		{name: "50 MiB", n: 50 * mib, want: 10 * time.Minute},
		{name: "100 MiB", n: 100 * mib, want: 20 * time.Minute},
		{name: "101 MiB", n: 101 * mib, want: 20*time.Minute + 12*time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := transferTimeout(tc.n); got != tc.want {
				t.Fatalf("transferTimeout(%d) = %v, want %v", tc.n, got, tc.want)
			}
		})
	}
}
