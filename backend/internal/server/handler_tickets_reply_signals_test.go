package server

import (
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// GET /tickets answers with a bare array, so what the reply search could not do
// travels in headers. The store says it (ticket.ReplySearchMeta, filled by the
// reply-search change); the handler only turns it into headers, and only for a
// request that asked for reply search.

func TestWriteReplySearchHeaders(t *testing.T) {
	yes, no := true, false
	cases := []struct {
		name          string
		searchReplies bool
		meta          ticket.ReplySearchMeta
		want          map[string]string
	}{
		{"not asked for, nothing to say", false, ticket.ReplySearchMeta{}, nil},
		{"not asked for, so a stale value is not sent", false, ticket.ReplySearchMeta{Searched: &no, IndexBuilding: true}, nil},
		{"asked for, no verdict from the store", true, ticket.ReplySearchMeta{}, nil},
		{"replies were not fully searched", true, ticket.ReplySearchMeta{Searched: &no},
			map[string]string{"X-Replies-Searched": "false"}},
		{"replies were fully searched", true, ticket.ReplySearchMeta{Searched: &yes},
			map[string]string{"X-Replies-Searched": "true"}},
		{"the index is still building", true, ticket.ReplySearchMeta{IndexBuilding: true},
			map[string]string{"X-Reply-Index": "building"}},
		{"both", true, ticket.ReplySearchMeta{Searched: &no, IndexBuilding: true},
			map[string]string{"X-Replies-Searched": "false", "X-Reply-Index": "building"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			writeReplySearchHeaders(rr, ticket.Filter{SearchReplies: tc.searchReplies}, tc.meta)
			got := map[string]string{}
			for _, k := range []string{"X-Replies-Searched", "X-Reply-Index"} {
				if v := rr.Header().Get(k); v != "" {
					got[k] = v
				}
			}
			if tc.want == nil {
				require.Empty(t, got)
				return
			}
			require.Equal(t, tc.want, got)
		})
	}
}
