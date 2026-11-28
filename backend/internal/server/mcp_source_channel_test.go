package server_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// #130: every ticket MCP opens is "mcp", including one filed for somebody else
// (the web form's "phone" is not available here), and the tool cannot be told
// otherwise.
func TestMCPSourceChannel(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	writer := h.newServiceClient(t, "Nagios", "tickets:read", "tickets:write")
	sess := newMCPSession(t, h, writer.Token)
	base := func(extra map[string]any) map[string]any {
		args := map[string]any{"subject": "Raised over MCP", "description": "x", "category_id": h.catID.String()}
		for k, v := range extra {
			args[k] = v
		}
		return args
	}
	channelOf := func(text string) ticket.SourceChannel {
		var out struct {
			ID            string               `json:"id"`
			SourceChannel ticket.SourceChannel `json:"source_channel"`
		}
		require.NoError(t, json.Unmarshal([]byte(text), &out), text)
		return out.SourceChannel
	}

	t.Run("create_ticket", func(t *testing.T) {
		text, isErr := sess.tool("create_ticket", base(nil))
		require.False(t, isErr, text)
		require.Equal(t, ticket.ChannelMCP, channelOf(text), "and staff are shown it")
	})
	t.Run("create_ticket for somebody else is still mcp", func(t *testing.T) {
		text, isErr := sess.tool("create_ticket", base(map[string]any{"reporter_user_id": h.userID.String()}))
		require.False(t, isErr, text)
		require.Equal(t, ticket.ChannelMCP, channelOf(text))
	})
	t.Run("get_ticket shows staff the channel", func(t *testing.T) {
		text, _ := sess.tool("create_ticket", base(nil))
		var created struct {
			ID string `json:"id"`
		}
		require.NoError(t, json.Unmarshal([]byte(text), &created))
		got, isErr := sess.tool("get_ticket", map[string]any{"id": created.ID})
		require.False(t, isErr, got)
		var out struct {
			SourceChannel ticket.SourceChannel `json:"source_channel"`
		}
		require.NoError(t, json.Unmarshal([]byte(got), &out))
		require.Equal(t, ticket.ChannelMCP, out.SourceChannel)
	})
	t.Run("create_follow_up", func(t *testing.T) {
		closed := reporterClosedTicket(t, h)
		text, isErr := sess.tool("create_follow_up", map[string]any{"ticket_id": closed.ID.String()})
		require.False(t, isErr, text)
		require.Equal(t, ticket.ChannelMCP, channelOf(text))
	})
	t.Run("create_ticket refuses a channel", func(t *testing.T) {
		for _, v := range []any{"phone", "mcp", "web", "api", "guest", "email", "", float64(1)} {
			before := ticketCount(t, h)
			text, isErr := sess.tool("create_ticket", base(map[string]any{"source_channel": v}))
			require.True(t, isErr, "%v: %s", v, text)
			require.Contains(t, text, "source_channel")
			require.Equal(t, before, ticketCount(t, h), "a refused call writes nothing")
		}
	})
}
