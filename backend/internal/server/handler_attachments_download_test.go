package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// Attachments are download-only. There is no previewer in Go Help Desk and
// there is not going to be one — see docs/DESIGN.md.
//
// Today Content-Type comes from the claimed extension, so each allowlisted
// type is served as itself, and Content-Disposition is what stops the browser
// acting on that. The case that actually bites is PDF: application/pdf renders
// in the browser's built-in viewer, and those viewers run JavaScript. Drop the
// header and a malicious PDF executes on this origin, where the staff sessions
// live.
//
// The uploads below are a .txt whose content is HTML and a real PDF. The first
// is served as text/plain and would only display — it is here because it also
// shows that the type check does not look at content, which is #165's problem.
// The second is the one with an engine behind it.
//
// Once #165 serves everything as application/octet-stream this header becomes
// defence in depth rather than the control, and this test should grow an
// octet-stream assertion at that point. It does not shrink: octet-stream stops
// the rendering, Content-Disposition still supplies the filename, and neither
// is free to remove.
func TestAttachmentDownload_IsAlwaysADownloadNeverARender(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	tk, err := h.ticketSvc.Create(ctx, ticket.CreateInput{
		Subject: "Attachment", Description: "has one", CategoryID: h.catID,
		ReporterUserID: &h.staffID,
	})
	require.NoError(t, err)

	// A .txt whose content is HTML. magicOK does not check text content, so
	// this is exactly what an attacker uploads — and it is only harmless
	// because of the header asserted below.
	const payload = `<html><script>alert(document.cookie)</script></html>`
	assertDownloadsRatherThanRenders(t, h, tk.ID.String(), "notes.txt", []byte(payload), "text/plain")

	// The case with an engine behind it. A minimal but structurally real PDF,
	// because magicOK checks the %PDF prefix and a fake one would be refused
	// before reaching the download path this is about.
	pdf := []byte("%PDF-1.7\n1 0 obj\n<< /Type /Catalog >>\nendobj\ntrailer\n<< /Root 1 0 R >>\n%%EOF\n")
	assertDownloadsRatherThanRenders(t, h, tk.ID.String(), "report.pdf", pdf, "application/pdf")
}

// assertDownloadsRatherThanRenders uploads a file and requires the download to
// be inert: forced to disk, with the browser forbidden from second-guessing
// the type, and byte-identical on the way back.
func assertDownloadsRatherThanRenders(t *testing.T, h *harness, ticketID, name string, content []byte, wantContentType string) {
	t.Helper()
	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	fw, err := mw.CreateFormFile("file", name)
	require.NoError(t, err)
	_, err = fw.Write(content)
	require.NoError(t, err)
	require.NoError(t, mw.Close())

	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/tickets/"+ticketID+"/attachments", body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "ApiKey "+h.apiKey)
	rec := httptest.NewRecorder()
	h.srv.ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code, "upload: %s", rec.Body.String())

	id := attachmentIDNamed(t, h, ticketID, name)

	res := h.do(t, http.MethodGet,
		"/api/v1/tickets/"+ticketID+"/attachments/"+id, nil)
	defer res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)

	disposition := res.Header.Get("Content-Disposition")
	require.True(t, strings.HasPrefix(disposition, "attachment;"),
		"attachments are download-only: Content-Disposition must be attachment, got %q", disposition)
	require.Equal(t, "nosniff", res.Header.Get("X-Content-Type-Options"),
		"without nosniff a browser may decide for itself that this is HTML")

	// The type the file is served as. A mutant changing this to text/html
	// survived an earlier version of this test, because with the header above
	// the response still downloads — so this is not load-bearing for safety.
	// It is here because DESIGN.md says a file is served as what it claims to
	// be, and an undocumented claim is one that quietly stops being true.
	//
	// Step 1 of #165 changes this to application/octet-stream for everything.
	// This assertion is meant to fail then: that is the signal to update it,
	// not a nuisance.
	require.Equal(t, wantContentType, res.Header.Get("Content-Type"),
		"a file is served as the type it was stored as")

	got, _ := io.ReadAll(res.Body)
	require.Equal(t, content, got,
		"the file is served verbatim, which is safe only because it is never rendered")
}

// attachmentIDNamed finds the attachment with this filename.
//
// Decoded rather than string-searched. The first version of this took the
// first "id":" in the response, which was fine while a ticket had one
// attachment and silently returned the wrong one the moment it had two — the
// PDF case above failed with the .txt's bytes, which is a confusing way to
// learn that a test helper is lying to you.
func attachmentIDNamed(t *testing.T, h *harness, ticketID, filename string) string {
	t.Helper()
	res := h.do(t, http.MethodGet, "/api/v1/tickets/"+ticketID+"/attachments", nil)
	defer res.Body.Close()

	var list []struct {
		ID       string `json:"id"`
		Filename string `json:"filename"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&list))
	for _, a := range list {
		if a.Filename == filename {
			return a.ID
		}
	}
	t.Fatalf("no attachment named %q on ticket %s; got %+v", filename, ticketID, list)
	return ""
}
