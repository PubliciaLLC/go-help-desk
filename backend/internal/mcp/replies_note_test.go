package mcp

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// A caller is told when replies were asked for and not searched, and only then.
func TestRepliesNote(t *testing.T) {
	yes, no := true, false
	tests := []struct {
		meta ticket.ReplySearchMeta
		want string // "" = no note; otherwise a phrase the note must hold
	}{
		{ticket.ReplySearchMeta{}, ""},
		{ticket.ReplySearchMeta{Searched: &yes}, ""},
		{ticket.ReplySearchMeta{Searched: &no, IndexBuilding: true}, "still being prepared"},
		{ticket.ReplySearchMeta{Searched: &no}, "took too long"},
	}
	for _, tc := range tests {
		t.Run(tc.want+"/"+fmt.Sprint(tc.meta.IndexBuilding), func(t *testing.T) {
			got := repliesNote(tc.meta)
			if tc.want == "" {
				require.Empty(t, got)
				return
			}
			require.Contains(t, got, "Replies were not searched")
			require.Contains(t, got, tc.want)
		})
	}
}
