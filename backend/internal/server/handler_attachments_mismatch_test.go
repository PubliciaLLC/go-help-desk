package server_test

import (
	"bytes"
	"context"
	"fmt"
	"hash/crc32"
	"image"
	"image/png"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// Whether an upload's content matched the name it arrived under is recorded on
// the row, and these are the tests that make that true rather than decorative.
//
// They exist because a mutation run found the column completely unguarded:
// hardcoding it to true, or to false, broke nothing. That is worse than it
// sounds in each direction. Always-true paints a warning on every ordinary
// attachment, and a warning that fires on everything is one staff learn to
// click past — which costs more than it saves, because the one file that
// really is lying is then buried in the noise. Always-false silently deletes
// the feature: nothing is ever flagged and nothing says so.

func TestUpload_RecordsWhetherTheContentMatchedTheName(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	tk, err := h.ticketSvc.Create(context.Background(), ticket.CreateInput{
		Subject: "Mismatch", Description: "x", CategoryID: h.catID,
		ReporterUserID: &h.staffID,
	})
	require.NoError(t, err)

	cases := []struct {
		name     string
		filename string
		content  []byte
		want     bool
		// storedAs differs from filename for a file that gets wrapped, which
		// is its own reminder that the two are not the same thing.
		storedAs string
	}{
		{
			name:     "a PDF that really is a PDF",
			filename: "report.pdf",
			content:  []byte("%PDF-1.7\n1 0 obj\n<< /Type /Catalog >>\nendobj\ntrailer\n<< /Root 1 0 R >>\n%%EOF\n"),
			want:     false,
			storedAs: "report.pdf",
		},
		{
			name:     "HTML wearing a .txt",
			filename: "notes.txt",
			content:  []byte(`<html><body><p>not a text file</p></body></html>`),
			want:     true,
			// HTML is not an accepted type, so this one is also wrapped.
			storedAs: fmt.Sprintf("suspicious-%08x.zip",
				crc32.ChecksumIEEE([]byte(`<html><body><p>not a text file</p></body></html>`))),
		},
		{
			name:     "a .log holding plain text",
			filename: "app.log",
			content:  []byte("2026-09-23 09:00:00 INFO started\n2026-09-23 09:00:01 INFO ready\n"),
			want:     false,
			storedAs: "app.log",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := uploadNamed(t, h, tk.ID.String(), tc.filename, tc.content)
			res.Body.Close()
			require.Equal(t, http.StatusCreated, res.StatusCode)

			att := attachmentNamedAnyOf(t, h, tk.ID.String(), tc.storedAs)
			require.NotNil(t, att.ContentMismatch,
				"nil means nobody looked, which is not true of a file we just inspected")
			require.Equal(t, tc.want, *att.ContentMismatch)
		})
	}
}

// A mismatch whose content is a type this instance accepts anyway is flagged
// and left alone. Wrapping it would be the warning-on-an-ordinary-file problem
// in physical form: the operator has said they accept PNGs, and a PNG is what
// this is, so the only thing wrong is the name.
//
// The mutation that made this necessary was dropping the allowlist half of the
// wrap condition, which wrapped every mismatch and broke nothing.
func TestUpload_AMismatchOfAnAcceptedTypeIsFlaggedButNotWrapped(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	tk, err := h.ticketSvc.Create(context.Background(), ticket.CreateInput{
		Subject: "Allowed mismatch", Description: "x", CategoryID: h.catID,
		ReporterUserID: &h.staffID,
	})
	require.NoError(t, err)

	// A real PNG under a .pdf name. PNG is on the shipped allowlist, so the
	// content is something this instance would have accepted under its own
	// name.
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 4, 4))))

	res := uploadNamed(t, h, tk.ID.String(), "report.pdf", buf.Bytes())
	res.Body.Close()
	require.Equal(t, http.StatusCreated, res.StatusCode)

	att := attachmentNamedAnyOf(t, h, tk.ID.String(), "report.pdf")
	require.NotNil(t, att.ContentMismatch)
	require.True(t, *att.ContentMismatch, "a PNG named .pdf is still a contradiction and is still recorded")
	require.Equal(t, "report.pdf", att.Filename,
		"an accepted type is not wrapped, however it was named")
	require.NotNil(t, att.DetectedMime)
	require.Equal(t, "image/png", *att.DetectedMime)
}

// An infected file that was named honestly is not also accused of lying.
//
// This is the bug the stored column exists for. Computing the flag on read
// compares the stored name against the content, and a quarantined file's
// stored name is ours — notes.txt.zip — so an honestly named sample came back
// as a mismatch. Recorded at upload, before any rename, it is false, which is
// the truth.
func TestUpload_AnInfectedFileTruthfullyNamedIsNotAlsoAMismatch(t *testing.T) {
	h, cleanup := newHarnessWith(t, 0, fakeInfectedScanner(t))
	defer cleanup()

	require.NoError(t, h.adminSvc.SetRaw(context.Background(),
		"attachment_infected_handling", []byte(`"quarantine"`)))

	tk, err := h.ticketSvc.Create(context.Background(), ticket.CreateInput{
		Subject: "Infected", Description: "x", CategoryID: h.catID,
		ReporterUserID: &h.staffID,
	})
	require.NoError(t, err)

	res := uploadNamed(t, h, tk.ID.String(), "notes.txt", []byte("plain text, and the scanner hates it\n"))
	res.Body.Close()
	require.Equal(t, http.StatusCreated, res.StatusCode)

	att := attachmentNamedAnyOf(t, h, tk.ID.String(), "notes.txt.zip")
	require.NotNil(t, att.VirusName, "the scanner identified this one")
	require.NotNil(t, att.ContentMismatch)
	require.False(t, *att.ContentMismatch,
		"the file was named honestly; only our wrapper renamed it, and that is not the uploader lying")
}

// attachmentNamedAnyOf returns the attachment stored under this filename.
func attachmentNamedAnyOf(t *testing.T, h *harness, ticketID, filename string) ticket.Attachment {
	t.Helper()
	id, err := uuid.Parse(ticketID)
	require.NoError(t, err)
	list, err := h.ticketSvc.ListAttachments(context.Background(), id)
	require.NoError(t, err)
	for _, a := range list {
		if a.Filename == filename {
			return a
		}
	}
	names := make([]string, 0, len(list))
	for _, a := range list {
		names = append(names, a.Filename)
	}
	t.Fatalf("no attachment named %q; got %v", filename, names)
	return ticket.Attachment{}
}
