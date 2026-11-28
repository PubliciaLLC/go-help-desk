package server_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// addAttachmentRow writes an attachment row straight to the store. A row with a
// non-nil virus is a quarantined one; that is the only thing "quarantined"
// means, so the tests here build it the way the upload handler records it.
func addAttachmentRow(t *testing.T, h *harness, ticketID uuid.UUID, name string, virus *string, mismatch *bool) uuid.UUID {
	t.Helper()
	id := uuid.New()
	sum := "ab" + id.String()
	require.NoError(t, h.ticketStore.CreateAttachment(context.Background(), ticket.Attachment{
		ID: id, TicketID: ticketID, Filename: name,
		MimeType: "application/zip", SizeBytes: 10, StoragePath: "/dev/null",
		SHA256: &sum, VirusName: virus, ContentMismatch: mismatch,
	}))
	return id
}

func ticketFor(t *testing.T, h *harness, subject string, reporter uuid.UUID) ticket.Ticket {
	t.Helper()
	tk, err := h.ticketSvc.Create(context.Background(), ticket.CreateInput{
		DescriptionFormat: ticket.TextFormatPlain, SourceChannel: ticket.ChannelWeb,
		Subject: subject, Description: "x", CategoryID: h.catID, ReporterUserID: &reporter,
	})
	require.NoError(t, err)
	return tk
}

// listBySubject reads the ticket list and returns each ticket's raw JSON object
// keyed by subject, so a test can ask whether a KEY is there, not only what it
// holds.
func listBySubject(t *testing.T, res *http.Response) map[string]map[string]any {
	t.Helper()
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", b)
	var rows []map[string]any
	require.NoError(t, json.Unmarshal(b, &rows), string(b))
	out := map[string]map[string]any{}
	for _, r := range rows {
		out[r["subject"].(string)] = r
	}
	return out
}

// #628: staff scanning the queue can tell which tickets hold an infected file
// without opening each one.
func TestTicketList_MarksTicketsHoldingAQuarantinedAttachment(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	virus := "Eicar-Test-Signature"
	no := false

	bad := ticketFor(t, h, "Holds a sample", h.staffID)
	addAttachmentRow(t, h, bad.ID, "sample.exe.zip", &virus, &no)

	// Two quarantined files and a clean one on the same ticket: it is marked
	// once, because the marker is a fact about the ticket and not a count.
	many := ticketFor(t, h, "Holds several", h.staffID)
	addAttachmentRow(t, h, many.ID, "a.zip", &virus, nil)
	addAttachmentRow(t, h, many.ID, "b.zip", &virus, nil)
	addAttachmentRow(t, h, many.ID, "notes.txt", nil, &no)

	// A file the scanner passed, even one whose content contradicts its name,
	// is not quarantined: that is a different warning with a different meaning.
	mismatch := true
	clean := ticketFor(t, h, "Clean file", h.staffID)
	addAttachmentRow(t, h, clean.ID, "pic.png", nil, &mismatch)

	ticketFor(t, h, "No files", h.staffID)

	// The default queue is tickets assigned to the caller, so both ask for
	// everything they may see.
	for name, call := range map[string]func() *http.Response{
		"staff": func() *http.Response { return h.do(t, http.MethodGet, "/api/v1/tickets?scope=visible", nil) },
		"admin": func() *http.Response { return h.doAsAdmin(t, http.MethodGet, "/api/v1/tickets?scope=visible", nil) },
	} {
		t.Run(name, func(t *testing.T) {
			got := listBySubject(t, call())
			require.Equal(t, true, got["Holds a sample"]["has_quarantined_attachment"])
			require.Equal(t, true, got["Holds several"]["has_quarantined_attachment"])
			for _, subject := range []string{"Clean file", "No files"} {
				require.Contains(t, got, subject)
				require.NotContains(t, got[subject], "has_quarantined_attachment",
					"%s must not carry the marker", subject)
			}
		})
	}
}

// The marker is a staff triage aid, like the SLA and Reopened markers. A
// requester reads their own ticket's attachments on the ticket page; they are
// not handed an extra flag on the list.
func TestTicketList_RequesterGetsNoQuarantineMarker(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	virus := "Eicar-Test-Signature"
	own := ticketFor(t, h, "Mine", h.userID)
	addAttachmentRow(t, h, own.ID, "sample.exe.zip", &virus, nil)

	got := listBySubject(t, h.doAsUser(t, http.MethodGet, "/api/v1/tickets", nil))
	require.Contains(t, got, "Mine", "the requester must see their own ticket in the list")
	require.NotContains(t, got["Mine"], "has_quarantined_attachment")

	res := h.doAsUser(t, http.MethodGet, "/api/v1/tickets/"+own.ID.String(), nil)
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	require.Equal(t, http.StatusOK, res.StatusCode, "body: %s", b)
	require.NotContains(t, string(b), "has_quarantined_attachment")
}
