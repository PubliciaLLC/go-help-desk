package report_test

import (
	"testing"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/report"
)

func fp(f float64) *float64 { return &f }

func TestRate(t *testing.T) {
	cases := []struct {
		name string
		n, d int64
		want *float64
	}{
		{"zero denominator is no rate", 0, 0, nil},
		{"zero denominator with a count is still no rate", 3, 0, nil},
		{"zero numerator is a rate of 0", 0, 5, fp(0)},
		{"whole", 5, 5, fp(1)},
		{"a half", 1, 2, fp(0.5)},
		{"rounds to four places", 1, 3, fp(0.3333)},
		{"rounds up at the fifth place", 2, 3, fp(0.6667)},
		{"a share of 4 in 79", 4, 79, fp(0.0506)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := report.Rate(tc.n, tc.d)
			switch {
			case got == nil && tc.want == nil:
			case got == nil || tc.want == nil:
				t.Fatalf("Rate(%d, %d) = %v, want %v", tc.n, tc.d, got, tc.want)
			case *got != *tc.want:
				t.Errorf("Rate(%d, %d) = %v, want %v", tc.n, tc.d, *got, *tc.want)
			}
		})
	}
}
