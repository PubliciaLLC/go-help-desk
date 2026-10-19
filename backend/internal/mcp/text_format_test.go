package mcp

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// #443: the text an agent writes through create_ticket and add_reply is shown
// as Markdown by default, so the tool has to say so. An agent that is not told
// writes `*` and `#` it means literally, and staff read formatting it did not
// intend.
func TestTools_DescribeMarkdownEscapingAndTheFormatArgument(t *testing.T) {
	s := New(nil, nil, nil, nil)
	byName := map[string]string{}
	for _, st := range s.mcp.ListTools() {
		schema, err := st.Tool.InputSchema.MarshalJSON()
		require.NoError(t, err)
		byName[st.Tool.Name] = string(schema)
	}
	for _, name := range []string{"create_ticket", "add_reply"} {
		schema, ok := byName[name]
		require.True(t, ok, name)
		require.Contains(t, schema, "Markdown", name)
		require.Contains(t, schema, "Escape characters you mean literally", name)
		require.Contains(t, schema, `format`, name)
		require.Contains(t, schema, `plain`, name)
		require.Contains(t, schema, `no images, no raw HTML`, name)
	}
}

func TestMCPTextFormat(t *testing.T) {
	got, err := mcpTextFormat(map[string]any{})
	require.NoError(t, err)
	require.Equal(t, ticket.TextFormatMarkdown, got, "absent means markdown for MCP")

	got, err = mcpTextFormat(map[string]any{"format": "plain"})
	require.NoError(t, err)
	require.Equal(t, ticket.TextFormatPlain, got)

	for _, bad := range []any{"", "html", "Markdown", float64(1), nil, true} {
		_, err := mcpTextFormat(map[string]any{"format": bad})
		require.ErrorIs(t, err, ticket.ErrInvalidTextFormat, "%v", bad)
	}
}
