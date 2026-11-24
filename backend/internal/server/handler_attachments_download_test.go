package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime"
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
// Three things have to hold together, and each is checked here because any one
// of them alone has a way to fail.
//
// The server says the file is an unknown blob, which no browser renders. It
// says to save rather than open it, which also supplies the filename. And it
// says not to guess the type from the content, which is what would otherwise
// undo the first.
//
// The uploads are a .txt whose content is HTML and a real PDF. The PDF is the
// one that used to matter: it was served as application/pdf, which browsers
// open in a viewer that runs JavaScript, so before the blob change a single
// missing header meant a malicious PDF running on this origin — where the
// staff sessions live. The .txt is here because it shows the upload check does
// not look inside text files, which is the known gap #165 still carries.
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
	assertDownloadsRatherThanRenders(t, h, tk.ID.String(), "notes.txt", []byte(payload))

	// The case with an engine behind it. A minimal but structurally real PDF,
	// because magicOK checks the %PDF prefix and a fake one would be refused
	// before reaching the download path this is about.
	pdf := []byte("%PDF-1.7\n1 0 obj\n<< /Type /Catalog >>\nendobj\ntrailer\n<< /Root 1 0 R >>\n%%EOF\n")
	assertDownloadsRatherThanRenders(t, h, tk.ID.String(), "report.pdf", pdf)
}

// assertDownloadsRatherThanRenders uploads a file and requires the download to
// be inert: forced to disk, with the browser forbidden from second-guessing
// the type, and byte-identical on the way back.
func assertDownloadsRatherThanRenders(t *testing.T, h *harness, ticketID, name string, content []byte) {
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

	// Every attachment goes out as an unknown blob, whatever it claims to be.
	//
	// This is the check that makes the rest of the safety here belt AND
	// braces. Before it, a PDF was served as application/pdf — a type browsers
	// open in a viewer that runs JavaScript — and only the header above kept
	// that from happening.
	require.Equal(t, "application/octet-stream", res.Header.Get("Content-Type"),
		"no attachment may be served as a type a browser would render")

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

// DESIGN.md promises the original filename on download.
//
// The old %q quoting did not actually mangle "café.pdf" — %q leaves printable
// non-ASCII alone, so the bytes went out as they came in. It just had no way
// to say what those bytes were: a bare filename= is defined over a character
// set with no room for UTF-8, and what a client does with raw bytes there is
// the client's business.
//
// RFC 6266 answers that with two forms: a plain ASCII one for old clients, and
// filename*= carrying the real name as percent-encoded UTF-8, labelled, for
// everything current. Both are sent.
func TestAttachmentDownload_KeepsANonASCIIFilename(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	tk, err := h.ticketSvc.Create(context.Background(), ticket.CreateInput{
		Subject: "Filename", Description: "x", CategoryID: h.catID,
		ReporterUserID: &h.staffID,
	})
	require.NoError(t, err)

	const name = "café — rapport №5.txt"
	res := uploadNamed(t, h, tk.ID.String(), name, []byte("contents"))
	res.Body.Close()
	require.Equal(t, http.StatusCreated, res.StatusCode)

	id := attachmentIDNamed(t, h, tk.ID.String(), name)
	dl := h.do(t, http.MethodGet, "/api/v1/tickets/"+tk.ID.String()+"/attachments/"+id, nil)
	defer dl.Body.Close()

	disposition := dl.Header.Get("Content-Disposition")
	require.Contains(t, disposition, "filename*=UTF-8''",
		"the real name needs the encoded form; the ASCII one cannot carry it")

	// What a client that understands the header ends up with. Go's parser
	// decodes filename*= and returns it under "filename", preferring it over
	// the ASCII fallback — which is what a browser does too.
	_, params, err := mime.ParseMediaType(disposition)
	require.NoError(t, err, "the header must parse: %q", disposition)
	require.Equal(t, name, params["filename"],
		"the file must arrive under the name it was uploaded with")

	// The ASCII fallback is still in the raw header for anything that only
	// reads that one, with the accented characters replaced rather than
	// dropped so the name stays recognisable.
	require.Contains(t, disposition, `filename="caf_ _ rapport _5.txt"`)
}

// A filename is chosen by whoever uploads. It decides what a browser writes to
// disk, so it must not be able to carry a path or break out of the header.
func TestAttachmentDownload_AHostileFilenameCannotEscapeTheHeader(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	tk, err := h.ticketSvc.Create(context.Background(), ticket.CreateInput{
		Subject: "Hostile", Description: "x", CategoryID: h.catID,
		ReporterUserID: &h.staffID,
	})
	require.NoError(t, err)

	// Everything in this name is here because it survives a real upload.
	//
	// A quote, which closes the quoted string if it is escaped wrongly. A
	// backslash path, which is what a Windows browser reads as a directory to
	// save into — a forward-slash one would prove nothing, because Go's
	// multipart reader runs filepath.Base over every uploaded filename and
	// "../../etc/passwd.txt" is already "passwd.txt" before our code sees it,
	// while filepath.Base on Linux leaves backslashes alone. And a tab, which
	// is the one control character Go's MIME header parser lets through; it
	// refuses the request outright for the rest, which is why the others are
	// checked against contentDisposition directly instead.
	const hostile = "a\"b\t..\\..\\windows\\system32\\evil.txt"
	res := uploadNamed(t, h, tk.ID.String(), hostile, []byte("contents"))
	res.Body.Close()
	require.Equal(t, http.StatusCreated, res.StatusCode)

	list := h.do(t, http.MethodGet, "/api/v1/tickets/"+tk.ID.String()+"/attachments", nil)
	defer list.Body.Close()
	var attachments []struct {
		ID       string `json:"id"`
		Filename string `json:"filename"`
	}
	require.NoError(t, json.NewDecoder(list.Body).Decode(&attachments))
	require.Len(t, attachments, 1)

	dl := h.do(t, http.MethodGet,
		"/api/v1/tickets/"+tk.ID.String()+"/attachments/"+attachments[0].ID, nil)
	defer dl.Body.Close()

	// The quote is the part that used to break this: an escaping bug turned
	// it into a backslash followed by a bare quote, which closes the quoted
	// string early and leaves the rest of the name as header syntax. A parse
	// failure here is that bug coming back.
	disposition := dl.Header.Get("Content-Disposition")
	typ, params, err := mime.ParseMediaType(disposition)
	require.NoError(t, err, "the header must still parse: %q", disposition)
	require.Equal(t, "attachment", typ)

	require.NotContains(t, params["filename"], `\`,
		"a Windows path separator must not survive to the browser")
	require.NotContains(t, params["filename"], "/",
		"a path separator must not survive to the browser")
	require.Contains(t, params["filename"], "evil.txt",
		"the name itself should still be recognisable")
	require.NotContains(t, params["filename"], "\t",
		"a tab reaches us through the upload and must not reach the header")
}
