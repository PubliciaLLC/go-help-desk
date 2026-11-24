package ticketstore

import (
	"strings"
	"testing"
)

func TestBuildSearchTSQuery(t *testing.T) {
	cases := []struct {
		name string
		q    string
		want string
	}{
		{name: "single word", q: "printer", want: "printer:*"},
		{name: "two words ANDed with prefix", q: "print jam", want: "print:* & jam:*"},
		{name: "lowercases input", q: "VPN Login", want: "vpn:* & login:*"},
		{name: "strips punctuation between tokens", q: "won't-print", want: "won:* & t:* & print:*"},
		{name: "collapses extra whitespace", q: "  printer   jam  ", want: "printer:* & jam:*"},
		{name: "pure punctuation yields no tokens", q: "---", want: ""},
		{name: "empty input yields no tokens", q: "", want: ""},
		{name: "non-Latin script tokenizes and lowercases", q: "Принтер сломан", want: "принтер:* & сломан:*"},
		{name: "CJK characters are tokenized", q: "打印机", want: "打印机:*"},
		{name: "accented Latin tokenizes as one word", q: "café", want: "café:*"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := buildSearchTSQuery(tc.q)
			if got != tc.want {
				t.Errorf("buildSearchTSQuery(%q) = %q, want %q", tc.q, got, tc.want)
			}
		})
	}
}

func TestBuildSearchTSQuery_Caps(t *testing.T) {
	t.Run("caps token count", func(t *testing.T) {
		words := make([]string, maxSearchTokens+5)
		for i := range words {
			words[i] = "word"
		}
		got := buildSearchTSQuery(strings.Join(words, " "))
		if n := strings.Count(got, "&") + 1; n != maxSearchTokens {
			t.Errorf("got %d tokens, want %d (query: %q)", n, maxSearchTokens, got)
		}
	})

	t.Run("caps token length so an unbroken long string can't reach to_tsquery's operand limit", func(t *testing.T) {
		long := strings.Repeat("a", 5000)
		got := buildSearchTSQuery(long)
		wantToken := strings.Repeat("a", maxSearchTokenRunes) + ":*"
		if got != wantToken {
			t.Errorf("buildSearchTSQuery(long input) = %q (len %d), want %q (len %d)", got, len(got), wantToken, len(wantToken))
		}
	})

	t.Run("caps length per-token, not just the first", func(t *testing.T) {
		long := strings.Repeat("b", 200)
		got := buildSearchTSQuery("short " + long)
		want := "short:* & " + strings.Repeat("b", maxSearchTokenRunes) + ":*"
		if got != want {
			t.Errorf("buildSearchTSQuery(...) = %q, want %q", got, want)
		}
	})
}
