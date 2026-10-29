package userstore

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

// sqlc emits int32 for LIMIT and OFFSET. Converting a Go int straight across
// wraps past MaxInt32: 2147483648 goes negative and Postgres answers "OFFSET
// must not be negative" as a 500, and 4294967296 wraps to zero and quietly
// serves the first page. The handlers bound their own input, but the store
// takes any int from any caller, so the clamp belongs at the conversion.
func TestPageInt32(t *testing.T) {
	cases := []struct {
		name string
		in   int
		want int32
	}{
		{name: "ordinary value", in: 100, want: 100},
		{name: "zero", in: 0, want: 0},
		{name: "the largest value that fits", in: math.MaxInt32, want: math.MaxInt32},
		{name: "one past the boundary", in: math.MaxInt32 + 1, want: math.MaxInt32},
		{name: "the value that used to 500", in: 2147483648, want: math.MaxInt32},
		{name: "the value that used to wrap to page one", in: 4294967296, want: math.MaxInt32},
		{name: "negative is floored, not wrapped", in: -1, want: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, pageInt32(tc.in))
		})
	}
}

func TestPageInt32_NeverNegative(t *testing.T) {
	for _, n := range []int{math.MinInt32, math.MaxInt32, math.MaxInt32 + 1, math.MaxInt32 * 2, -1, 0, 1, 1 << 40} {
		require.GreaterOrEqual(t, pageInt32(n), int32(0), "input %d", n)
	}
}
