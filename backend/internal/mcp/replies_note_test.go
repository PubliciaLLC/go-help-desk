package mcp

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// A caller is told when replies were asked for and not searched, and only then.
func TestRepliesNote(t *testing.T) {
	tests := []struct {
		status ticket.RepliesStatus
		want   string // "" = no note; otherwise a phrase the note must hold
	}{
		{ticket.RepliesNotRequested, ""},
		{ticket.RepliesSearched, ""},
		{ticket.RepliesPreparing, "still being prepared"},
		{ticket.RepliesTimedOut, "took too long"},
	}
	for _, tc := range tests {
		t.Run(string(tc.status), func(t *testing.T) {
			got := repliesNote(tc.status)
			if tc.want == "" {
				require.Empty(t, got)
				return
			}
			require.Contains(t, got, "Replies were not searched")
			require.Contains(t, got, tc.want)
		})
	}
}
