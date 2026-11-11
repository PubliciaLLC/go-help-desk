package server_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// mcpToolText unwraps a tools/call answer to the tool's own text and whether it
// was an error.
func mcpToolText(t *testing.T, raw string) (string, bool) {
	t.Helper()
	var rpc struct {
		Result struct {
			IsError bool `json:"isError"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	require.NoError(t, json.Unmarshal([]byte(raw), &rpc), raw)
	require.NotEmpty(t, rpc.Result.Content, raw)
	return rpc.Result.Content[0].Text, rpc.Result.IsError
}

type mcpAttachment struct {
	ID          string  `json:"id"`
	Filename    string  `json:"filename"`
	MimeType    string  `json:"mime_type"`
	SizeBytes   int64   `json:"size_bytes"`
	VirusName   *string `json:"virus_name"`
	Mismatch    *bool   `json:"mismatch"`
	Quarantined bool    `json:"quarantined"`
}

// #628: an MCP client can see a ticket's attachments and which were
// quarantined.
func TestMCPListAttachments(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	virus := "Eicar-Test-Signature"
	yes, no := true, false
	tk := ticketFor(t, h, "Has files", h.userID)
	badID := addAttachmentRow(t, h, tk.ID, "sample.exe.zip", &virus, &no)
	liarID := addAttachmentRow(t, h, tk.ID, "invoice.pdf", nil, &yes)
	okID := addAttachmentRow(t, h, tk.ID, "notes.txt", nil, &no)
	oldID := addAttachmentRow(t, h, tk.ID, "legacy.doc", nil, nil)

	for _, tr := range mcpTransports {
		t.Run(tr.name, func(t *testing.T) {
			c := tr.open(t, h, h.apiKey)

			text, isErr := mcpToolText(t, c.call(t, "list_attachments", map[string]any{"ticket_id": tk.ID.String()}))
			require.False(t, isErr, text)

			var got []mcpAttachment
			require.NoError(t, json.Unmarshal([]byte(text), &got), text)
			byID := map[string]mcpAttachment{}
			for _, a := range got {
				byID[a.ID] = a
			}
			require.Len(t, byID, 4, text)

			bad := byID[badID.String()]
			require.True(t, bad.Quarantined)
			require.NotNil(t, bad.VirusName)
			require.Equal(t, virus, *bad.VirusName)
			require.Equal(t, "sample.exe.zip", bad.Filename)
			require.Equal(t, "application/zip", bad.MimeType)
			require.EqualValues(t, 10, bad.SizeBytes)

			// A mismatch is not a detection, and a file that predates the
			// inspection (nil everywhere) is not quarantined either.
			liar := byID[liarID.String()]
			require.False(t, liar.Quarantined)
			require.Nil(t, liar.VirusName)
			require.NotNil(t, liar.Mismatch)
			require.True(t, *liar.Mismatch)
			require.False(t, byID[okID.String()].Quarantined)
			require.False(t, byID[oldID.String()].Quarantined)
			require.Nil(t, byID[oldID.String()].Mismatch, "not recorded is not the same as false")

			// Metadata only: download-only means nothing on this surface leads
			// to the bytes, and no lookup is made on the operator's quota.
			require.NotContains(t, text, "storage_path")
			require.NotContains(t, text, "/dev/null")
			require.NotContains(t, text, "reputation")
		})
	}

	t.Run("by tracking number, like get_ticket", func(t *testing.T) {
		c := mcpTransports[0].open(t, h, h.apiKey)
		text, isErr := mcpToolText(t, c.call(t, "list_attachments", map[string]any{"ticket_id": string(tk.TrackingNumber)}))
		require.False(t, isErr, text)
		require.Contains(t, text, "sample.exe.zip")
	})

	t.Run("a ticket with no files is an empty list, not an error", func(t *testing.T) {
		empty := ticketFor(t, h, "Nothing attached", h.userID)
		c := mcpTransports[0].open(t, h, h.apiKey)
		text, isErr := mcpToolText(t, c.call(t, "list_attachments", map[string]any{"ticket_id": empty.ID.String()}))
		require.False(t, isErr, text)
		require.JSONEq(t, "[]", text)
	})
}

// Whoever may see the ticket may see what is attached to it, and nobody else
// learns the ticket exists.
func TestMCPListAttachments_FollowsTicketVisibility(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	virus := "Eicar-Test-Signature"
	mine := ticketFor(t, h, "Mine", h.userID)
	addAttachmentRow(t, h, mine.ID, "sample.exe.zip", &virus, nil)
	theirs := ticketFor(t, h, "Staff's own", h.staffID)
	addAttachmentRow(t, h, theirs.ID, "secret.zip", &virus, nil)

	c := mcpTransports[0].open(t, h, h.userKey)

	text, isErr := mcpToolText(t, c.call(t, "list_attachments", map[string]any{"ticket_id": mine.ID.String()}))
	require.False(t, isErr, text)
	require.Contains(t, text, "sample.exe.zip", "a requester sees the files on their own ticket")

	hidden, isErr := mcpToolText(t, c.call(t, "list_attachments", map[string]any{"ticket_id": theirs.ID.String()}))
	require.True(t, isErr)
	require.NotContains(t, hidden, "secret.zip")
	require.Contains(t, hidden, "not found", "the same refusal as a ticket that does not exist")

	missing, isErr := mcpToolText(t, c.call(t, "list_attachments", map[string]any{"ticket_id": "00000000-0000-0000-0000-000000000000"}))
	require.True(t, isErr)
	require.Contains(t, missing, "not found")
}

func TestMCPListAttachments_RejectsABadTicketID(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	c := mcpTransports[0].open(t, h, h.apiKey)

	text, isErr := mcpToolText(t, c.call(t, "list_attachments", map[string]any{}))
	require.True(t, isErr)
	require.Contains(t, text, "ticket_id")
}
