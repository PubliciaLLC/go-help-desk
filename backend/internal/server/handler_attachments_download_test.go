package server_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
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
// Three uploads. A .txt whose content is HTML, the same HTML named .pdf, and
// a real PDF. The real PDF is the one that used to matter: it was served as
// application/pdf, which browsers open in a viewer that runs JavaScript, so
// before the blob change a single missing header meant a malicious PDF running
// on this origin — where the staff sessions live.
//
// The .txt is the case where the headers are the *only* defence, which is why
// it is first. It is not wrapped: a file named notes.txt opens in a text
// editor on the reader's machine whatever is inside it, so there is nothing
// for a wrap to take away, and the release that wrapped it also refused
// ordinary crash logs for the same reason. What is left is this server's own
// promise, and these headers are all of it.
//
// The .pdf is the wrapped case, and it is here so that both protections are
// asserted. Wrapping is what a person sees; the headers are what a browser
// obeys. Neither is allowed to be the only one.
func TestAttachmentDownload_IsAlwaysADownloadNeverARender(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	tk, err := h.ticketSvc.Create(ctx, ticket.CreateInput{
		Subject: "Attachment", Description: "has one", CategoryID: h.catID,
		ReporterUserID: &h.staffID,
	})
	require.NoError(t, err)

	// Storing a mismatch rather than refusing it is an operator setting, and
	// it defaults to refusing. This test needs the file stored to have
	// anything to download, so it is the instance that chose to store it.
	require.NoError(t, h.adminSvc.SetString(ctx,
		admin.KeyAttachmentMismatchHandling, admin.MismatchHandlingWrap))

	// A .txt whose content is HTML: exactly what an attacker uploads, and the
	// file this server hands back under its own name and its own bytes. A
	// text-named file is neither wrapped nor refused for its content, so
	// everything that stops this rendering is in the response headers below.
	const payload = `<html><script>alert(document.cookie)</script></html>`
	asText := assertDownloadsRatherThanRenders(t, h, tk.ID.String(), "notes.txt", "notes.txt", []byte(payload))
	require.Equal(t, payload, string(asText),
		"stored as it arrived, which is safe only because it is never rendered")

	// The same HTML under a name that claims a binary format. That is a
	// mismatch of a type this instance does not accept, so it is the one the
	// setting governs and the one that gets wrapped.
	wrapped := fmt.Sprintf("suspicious-%08x.zip", crc32.ChecksumIEEE([]byte(payload)))
	got := assertDownloadsRatherThanRenders(t, h, tk.ID.String(), "invoice.pdf", wrapped, []byte(payload))

	// What downloads is the archive, not the HTML. The payload must still be
	// in there intact — wrapping is containment, not censorship; a help desk
	// that quietly altered an attachment would be worse than one that refused
	// it.
	zr, err := zip.NewReader(bytes.NewReader(got), int64(len(got)))
	require.NoError(t, err, "a wrapped attachment must download as a readable archive")
	require.Len(t, zr.File, 1)
	require.Equal(t, "invoice.pdf", zr.File[0].Name,
		"the sample keeps the name it was uploaded under, inside the archive")
	rc, err := zr.File[0].Open()
	require.NoError(t, err)
	defer rc.Close()
	inner, err := io.ReadAll(rc)
	require.NoError(t, err)
	require.Equal(t, payload, string(inner))
	require.NotContains(t, string(got), "<script",
		"the payload must not sit in the clear in the stored archive")

	// The case with an engine behind it. A minimal but structurally real PDF.
	// It is not a mismatch, so it keeps its own name and is stored as it
	// arrived — which is what makes the byte-for-byte assertion meaningful.
	pdf := []byte("%PDF-1.7\n1 0 obj\n<< /Type /Catalog >>\nendobj\ntrailer\n<< /Root 1 0 R >>\n%%EOF\n")
	served := assertDownloadsRatherThanRenders(t, h, tk.ID.String(), "report.pdf", "report.pdf", pdf)
	require.Equal(t, pdf, served,
		"a file whose content matches its name is served verbatim, which is safe only because it is never rendered")
}

// assertDownloadsRatherThanRenders uploads a file and requires the download to
// be inert: forced to disk, with the browser forbidden from second-guessing
// the type, and byte-identical on the way back.
// storedAs is the filename the attachment is expected to carry once stored,
// which is not the uploaded name for a file #165 wrapped. Returns the
// downloaded bytes: what those should be differs between a wrapped file and
// one stored as it arrived, so the caller asserts it.
func assertDownloadsRatherThanRenders(t *testing.T, h *harness, ticketID, name, storedAs string, content []byte) []byte {
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

	id := attachmentIDNamed(t, h, ticketID, storedAs)

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

	got, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	return got
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
