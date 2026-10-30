package server

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// The label somebody gave a key is bounded, and bounded without corrupting it.
//
// Found by the pre-merge gate on #302: the name arrived from a query string
// and went into a TEXT column with nothing between.
func TestCapPasskeyName(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty is left alone", "", ""},
		{"an ordinary label is untouched", "Work laptop", "Work laptop"},
		{
			"exactly at the cap is untouched",
			strings.Repeat("k", maxPasskeyNameBytes),
			strings.Repeat("k", maxPasskeyNameBytes),
		},
		{
			"one byte over is cut to the cap",
			strings.Repeat("k", maxPasskeyNameBytes+1),
			strings.Repeat("k", maxPasskeyNameBytes),
		},
		{
			"far over is cut to the cap",
			strings.Repeat("k", maxPasskeyNameBytes*10),
			strings.Repeat("k", maxPasskeyNameBytes),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := capPasskeyName(tc.in); got != tc.want {
				t.Errorf("capPasskeyName(%d bytes) gave %d bytes, want %d",
					len(tc.in), len(got), len(tc.want))
			}
		})
	}

	// The case a byte-slice would get wrong. "日" is three bytes, so a name
	// made of them does not divide evenly into the cap: cutting at exactly
	// maxPasskeyNameBytes lands mid-character.
	t.Run("a multi-byte name is cut on a character boundary", func(t *testing.T) {
		in := strings.Repeat("日", maxPasskeyNameBytes) // 3x the cap in bytes
		got := capPasskeyName(in)

		if !utf8.ValidString(got) {
			t.Fatalf("the cut produced invalid UTF-8: %q", got)
		}
		if len(got) > maxPasskeyNameBytes {
			t.Errorf("result is %d bytes, over the cap of %d", len(got), maxPasskeyNameBytes)
		}
		// And it did not throw the whole thing away to achieve that.
		if len(got) < maxPasskeyNameBytes-utf8.UTFMax {
			t.Errorf("result is %d bytes, which discarded more than one character's worth", len(got))
		}
		if maxPasskeyNameBytes%3 == 0 {
			t.Skip("cap divides evenly by this rune's width; the boundary case is not exercised")
		}
	})
}
