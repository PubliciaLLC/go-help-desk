package ticket

import (
	"errors"
	"testing"
)

// Postgres refuses NUL and invalid UTF-8 in text, and nothing else: 1.2 took
// every other character, and stored tickets (which a follow-up copies) may hold
// them, so only those two are refused.
func TestCheckFreeText(t *testing.T) {
	cases := []struct {
		name string
		v    string
		bad  bool
	}{
		{"plain", "hello world", false},
		{"unicode", "Zoë 日本", false},
		{"NUL", "a\x00b", true},
		{"NUL alone", "\x00", true},
		{"invalid UTF-8", "a\xffb", true},
		{"tab", "a\tb", false},
		{"newline and CR", "a\r\nb", false},
		{"bell", "a\x07b", false},
		{"ESC (ANSI)", "\x1b[31mred\x1b[0m", false},
		{"form feed", "a\fb", false},
		{"DEL", "a\x7fb", false},
		{"C1 (U+0085)", "a\u0085b", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckFreeText("f", tc.v)
			if tc.bad != (err != nil) {
				t.Fatalf("CheckFreeText(%q) = %v", tc.v, err)
			}
			if err != nil && !errors.Is(err, ErrValidation) {
				t.Fatalf("not a validation error: %v", err)
			}
		})
	}
}
