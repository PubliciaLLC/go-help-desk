package ticket

import (
	"errors"
	"testing"
)

func TestCheckFreeText(t *testing.T) {
	cases := []struct {
		name      string
		v         string
		multiline bool
		bad       bool
	}{
		{"plain", "hello world", false, false},
		{"unicode", "Zoë 日本", false, false},
		{"NUL", "a\x00b", true, true},
		{"bell", "a\x07b", true, true},
		{"escape", "a\x1bb", true, true},
		{"DEL", "a\x7fb", true, true},
		{"C1", "a\u0085b", true, true},
		{"invalid UTF-8", "a\xffb", true, true},
		{"newline in one line", "a\nb", false, true},
		{"tab in one line", "a\tb", false, true},
		{"newline, tab and CR in multiline", "a\r\nb\tc", true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckFreeText("f", tc.v, tc.multiline)
			if tc.bad != (err != nil) {
				t.Fatalf("CheckFreeText(%q, %v) = %v", tc.v, tc.multiline, err)
			}
			if err != nil && !errors.Is(err, ErrValidation) {
				t.Fatalf("not a validation error: %v", err)
			}
		})
	}
}
