package server

import (
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

// The HTTP test can only observe the cap indirectly — it would need more than
// 200 tickets to see it bite. This asserts it directly.
func TestPageParams(t *testing.T) {
	cases := []struct {
		name                 string
		query                string
		wantLimit, wantOffet int
	}{
		{name: "no parameters keeps the old behaviour", query: "", wantLimit: 100},
		{name: "limit is honoured", query: "?limit=25", wantLimit: 25},
		{name: "offset is honoured", query: "?limit=25&offset=50", wantLimit: 25, wantOffet: 50},
		{
			// One request must not be able to ask for the whole table.
			name: "limit is capped", query: "?limit=100000", wantLimit: 200,
		},
		{name: "limit exactly at the cap", query: "?limit=200", wantLimit: 200},
		{name: "zero limit falls back", query: "?limit=0", wantLimit: 100},
		{name: "negative limit falls back", query: "?limit=-5", wantLimit: 100},
		{name: "unparseable limit falls back", query: "?limit=abc", wantLimit: 100},
		{
			// A negative offset reaches Postgres as "OFFSET must not be
			// negative" and became a 500 for what is a bad request.
			name: "negative offset falls back to zero", query: "?offset=-1", wantLimit: 100,
		},
		{name: "unparseable offset falls back to zero", query: "?offset=abc", wantLimit: 100},
		{
			// int32 overflow in the store layer turned a huge offset negative.
			name: "absurd offset is still non-negative", query: "?offset=3000000000",
			wantLimit: 100, wantOffet: 3000000000,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			limit, offset := pageParams(httptest.NewRequest("GET", "/tickets"+tc.query, nil))
			require.Equal(t, tc.wantLimit, limit)
			require.Equal(t, tc.wantOffet, offset)
			require.Positive(t, limit, "a non-positive limit returns nothing at all")
			require.GreaterOrEqual(t, offset, 0, "a negative offset is a database error")
		})
	}
}
