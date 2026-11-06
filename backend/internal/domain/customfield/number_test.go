package customfield_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/customfield"
)

func TestIsNumber(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want bool
	}{
		// What the browser's <input type="number"> sends, and what people type.
		{"12", true},
		{" 12 ", true},
		{"-3.5", true},
		{".5", true},
		{"1e3", true},
		{"+1", true},
		{"0", true},
		// Not a number.
		{"abc", false},
		{"1,5", false},
		{"12 apples", false},
		// ParseFloat accepts these; a field value should not be one.
		{"0x10", false},
		{"0X10", false},
		{"-0x1p-2", false},
		{"+0x10", false},
		{"NaN", false},
		{"nan", false},
		{"Inf", false},
		{"-Inf", false},
		{"infinity", false},
		{"1e400", false}, // out of range: ParseFloat returns ErrRange
		{"1_000", false},
		// Nothing left after trimming is a clear, not a number: the caller
		// decides what a clear means.
		{"", false},
		{"   ", false},
	} {
		t.Run(tc.in, func(t *testing.T) {
			require.Equal(t, tc.want, customfield.IsNumber(tc.in))
		})
	}
}
