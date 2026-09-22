package server_test

import (
	"bytes"
	"context"
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
// That decision rests entirely on this header. Nothing renders attachment
// content today, so nothing has been enforcing it; the day someone adds an
// inline image preview or drops the header for a "view in browser" link, every
// uploaded file becomes a candidate for stored XSS against the staff sessions
// that live on this origin.
//
// So this test exists to fail at that moment rather than after it.
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
	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	fw, err := mw.CreateFormFile("file", "notes.txt")
	require.NoError(t, err)
	_, err = fw.Write([]byte(payload))
	require.NoError(t, err)
	require.NoError(t, mw.Close())

	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/tickets/"+tk.ID.String()+"/attachments", body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "ApiKey "+h.apiKey)
	rec := httptest.NewRecorder()
	h.srv.ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code, "upload: %s", rec.Body.String())

	list := h.do(t, http.MethodGet, "/api/v1/tickets/"+tk.ID.String()+"/attachments", nil)
	raw, _ := io.ReadAll(list.Body)
	list.Body.Close()
	id := attachmentIDFrom(t, string(raw))

	res := h.do(t, http.MethodGet,
		"/api/v1/tickets/"+tk.ID.String()+"/attachments/"+id, nil)
	defer res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)

	disposition := res.Header.Get("Content-Disposition")
	require.True(t, strings.HasPrefix(disposition, "attachment;"),
		"attachments are download-only: Content-Disposition must be attachment, got %q", disposition)
	require.Equal(t, "nosniff", res.Header.Get("X-Content-Type-Options"),
		"without nosniff a browser may decide for itself that this is HTML")

	got, _ := io.ReadAll(res.Body)
	require.Equal(t, payload, string(got),
		"the file is served verbatim, which is safe only because it is never rendered")
}

func attachmentIDFrom(t *testing.T, listJSON string) string {
	t.Helper()
	const key = `"id":"`
	i := strings.Index(listJSON, key)
	require.GreaterOrEqual(t, i, 0, "no attachment in %s", listJSON)
	rest := listJSON[i+len(key):]
	j := strings.Index(rest, `"`)
	require.GreaterOrEqual(t, j, 0)
	return rest[:j]
}
