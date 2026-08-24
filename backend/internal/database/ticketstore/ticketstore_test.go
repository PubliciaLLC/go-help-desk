package ticketstore

import "testing"

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
