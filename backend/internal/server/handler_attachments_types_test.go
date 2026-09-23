package server_test

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// DESIGN.md says types a browser will execute are refused outright rather than
// cleaned, and that the allowlist therefore does not have to be a sanitiser.
//
// That was true in the code and pinned by nothing: allowedExt is a map, and a
// map is one line away from gaining an entry. Someone adding ".html" because a
// customer asked to attach a saved web page would break the stated design and
// no test would notice.
func TestUpload_RefusesTypesABrowserWouldExecute(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	tk, err := h.ticketSvc.Create(context.Background(), ticket.CreateInput{
		Subject: "Types", Description: "x", CategoryID: h.catID,
		ReporterUserID: &h.staffID,
	})
	require.NoError(t, err)

	for _, tc := range []struct {
		name    string
		content []byte
	}{
		{"page.html", []byte("<html><script>alert(1)</script></html>")},
		{"page.htm", []byte("<html><body>hi</body></html>")},
		{"script.js", []byte("alert(document.cookie)")},
		{"vector.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`)},
		{"data.xml", []byte(`<?xml version="1.0"?><root/>`)},
		{"page.xhtml", []byte("<html xmlns='http://www.w3.org/1999/xhtml'/>")},
		{"vector.svgz", []byte("\x1f\x8b\x08")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := uploadNamed(t, h, tk.ID.String(), tc.name, tc.content)
			defer res.Body.Close()
			require.Equal(t, http.StatusUnsupportedMediaType, res.StatusCode,
				"%s must be refused: the allowlist is what lets the rest of the design skip sanitising",
				tc.name)
		})
	}
}

// And a file whose content does not match its name is refused for the types
// that have a signature to check — which is the other half of what the docs
// claim, and the half that has a documented gap for .txt and .log.
func TestUpload_RefusesContentThatContradictsTheExtension(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	tk, err := h.ticketSvc.Create(context.Background(), ticket.CreateInput{
		Subject: "Content", Description: "x", CategoryID: h.catID,
		ReporterUserID: &h.staffID,
	})
	require.NoError(t, err)

	html := []byte("<html><script>alert(1)</script></html>")

	t.Run("html named .png is refused", func(t *testing.T) {
		res := uploadNamed(t, h, tk.ID.String(), "image.png", html)
		defer res.Body.Close()
		require.Equal(t, http.StatusUnsupportedMediaType, res.StatusCode)
	})

	t.Run("html named .pdf is refused", func(t *testing.T) {
		res := uploadNamed(t, h, tk.ID.String(), "doc.pdf", html)
		defer res.Body.Close()
		require.Equal(t, http.StatusUnsupportedMediaType, res.StatusCode)
	})

	// The documented gap, asserted so that closing it in #165 is a deliberate
	// change to a test rather than a surprise. A .txt has no signature, so its
	// content is not checked; it is served as text/plain and displays.
	t.Run("html named .txt is accepted, which is the known gap", func(t *testing.T) {
		res := uploadNamed(t, h, tk.ID.String(), "notes.txt", html)
		defer res.Body.Close()
		require.Equal(t, http.StatusCreated, res.StatusCode,
			"if this starts failing, #165 has closed the gap and DESIGN.md needs updating")
	})
}

func uploadNamed(t *testing.T, h *harness, ticketID, filename string, content []byte) *http.Response {
	t.Helper()
	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	fw, err := mw.CreateFormFile("file", filename)
	require.NoError(t, err)
	_, err = fw.Write(content)
	require.NoError(t, err)
	require.NoError(t, mw.Close())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/tickets/"+ticketID+"/attachments", body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "ApiKey "+h.apiKey)
	rec := httptest.NewRecorder()
	h.srv.ServeHTTP(rec, req)
	return rec.Result()
}
