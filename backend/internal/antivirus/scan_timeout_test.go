package antivirus

import (
	"testing"
	"time"
)

// TestScanTimeoutFor pins the exact scan deadline for a file of n bytes. The
// values are compared with == on purpose, as for the transfer deadline.
func TestScanTimeoutFor(t *testing.T) {
	const mib = 1 << 20
	cases := []struct {
		name string
		n    int
		want time.Duration
	}{
		{name: "empty file", n: 0, want: time.Minute},
		{name: "25 MiB anchor", n: 25 * mib, want: time.Minute},
		{name: "50 MiB", n: 50 * mib, want: 2 * time.Minute},
		{name: "100 MiB", n: 100 * mib, want: 4 * time.Minute},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := scanTimeoutFor(tc.n); got != tc.want {
				t.Fatalf("scanTimeoutFor(%d) = %v, want %v", tc.n, got, tc.want)
			}
		})
	}
}
