package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
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
	// content is never looked at. Since #165 step 1 it downloads as an opaque
	// blob like every other attachment, so nothing renders it here; what is
	// left is that the saved file is HTML and the name says otherwise.
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

// A filename is raw bytes off the wire. Multipart declares no encoding for it,
// so nothing guarantees it is text at all — and the column it is stored in is
// TEXT, which Postgres will only accept as valid UTF-8.
//
// Before this check the bad name went all the way through: allowed extension,
// content scanned, file written to disk, and then the insert failed. The
// caller got 500 db_error, which blames the server for a malformed request,
// and the handler was left removing a file it should never have written.
func TestUpload_RefusesAFilenameThatIsNotText(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	tk, err := h.ticketSvc.Create(context.Background(), ticket.CreateInput{
		Subject: "Filename bytes", Description: "x", CategoryID: h.catID,
		ReporterUserID: &h.staffID,
	})
	require.NoError(t, err)

	// 0xff and 0xfe never appear in valid UTF-8. Written as a raw multipart
	// body because Go's own writer would not produce this.
	var body bytes.Buffer
	body.WriteString("--B\r\nContent-Disposition: form-data; name=\"file\"; filename=\"a\xff\xfeb.txt\"\r\n")
	body.WriteString("Content-Type: text/plain\r\n\r\nhello\r\n--B--\r\n")

	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/tickets/"+tk.ID.String()+"/attachments", &body)
	req.Header.Set("Content-Type", "multipart/form-data; boundary=B")
	req.Header.Set("Authorization", "ApiKey "+h.apiKey)
	rec := httptest.NewRecorder()
	h.srv.ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code,
		"a filename that cannot be stored is a bad request, not a server fault: %s",
		rec.Body.String())
	require.Contains(t, rec.Body.String(), "invalid_filename")

	assertUploadLeftNothingBehind(t, h, tk.ID.String())
}

// A NUL is valid UTF-8, so utf8.ValidString accepts it — and Postgres does
// not. It arrives through the RFC 5987 form of the header, which the MIME
// parser percent-decodes, so a parser that refuses a raw control character
// hands this one straight over.
func TestUpload_RefusesANULInTheFilename(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	tk, err := h.ticketSvc.Create(context.Background(), ticket.CreateInput{
		Subject: "NUL", Description: "x", CategoryID: h.catID,
		ReporterUserID: &h.staffID,
	})
	require.NoError(t, err)

	var body bytes.Buffer
	body.WriteString("--B\r\nContent-Disposition: form-data; name=\"file\"; filename*=UTF-8''a%00b.txt\r\n")
	body.WriteString("Content-Type: text/plain\r\n\r\nhello\r\n--B--\r\n")

	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/tickets/"+tk.ID.String()+"/attachments", &body)
	req.Header.Set("Content-Type", "multipart/form-data; boundary=B")
	req.Header.Set("Authorization", "ApiKey "+h.apiKey)
	rec := httptest.NewRecorder()
	h.srv.ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code,
		"a NUL cannot be stored, so this is a bad request: %s", rec.Body.String())
	require.Contains(t, rec.Body.String(), "invalid_filename")
	assertUploadLeftNothingBehind(t, h, tk.ID.String())
}

// assertUploadLeftNothingBehind checks both places a refused upload could
// leave something: the attachment list, and the disk.
//
// The disk half is the point. An earlier version only read the list, and
// returning the 400 *after* os.WriteFile still passed it — so the test did
// not hold the property its own comment claimed.
func assertUploadLeftNothingBehind(t *testing.T, h *harness, ticketID string) {
	t.Helper()

	list := h.do(t, http.MethodGet, "/api/v1/tickets/"+ticketID+"/attachments", nil)
	defer list.Body.Close()
	var attachments []struct{}
	require.NoError(t, json.NewDecoder(list.Body).Decode(&attachments))
	require.Empty(t, attachments, "a refused upload must not be recorded")

	// Walked rather than looked up by path. The first version joined
	// attachDir with the subdirectory layout the handler happens to use and
	// returned early if that directory did not exist — so changing the layout
	// made the check pass while a file sat on disk, which is the exact
	// weakness it was written to close. Nothing here knows where uploads go;
	// it just requires that nothing arrived anywhere.
	var found []string
	err := filepath.WalkDir(h.attachDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() {
			found = append(found, path)
		}
		return nil
	})
	require.NoError(t, err)
	require.Empty(t, found, "a refused upload must not leave a file under %s", h.attachDir)
}
