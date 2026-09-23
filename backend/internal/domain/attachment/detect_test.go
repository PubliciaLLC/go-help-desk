package attachment_test

import (
	"mime"
	"strings"
	"testing"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/attachment"
)

// What a file is, asked of the bytes and nothing else.
//
// The point of returning an extension alongside the MIME type is that the
// answer can be compared with a claimed extension directly, with no table
// mapping one detector's vocabulary onto another's. So both halves are pinned:
// a detector that returned the right MIME type and an empty extension would
// leave IsMismatch with nothing to compare, and every file would be flagged.
//
// The cases are the ones the issue's comparison table names. The three at the
// bottom — .docx, .svg, .csv — are there because Go's own sniffer answers
// "application/zip", "text/xml" and "text/plain" for them, which would fire a
// false mismatch on ordinary help desk attachments. A warning that is always
// on is a warning staff learn to click past, so a detector that is merely
// cautious is not good enough here.
func TestDetect(t *testing.T) {
	cases := []struct {
		name     string
		data     []byte
		wantExt  string
		wantMime string
	}{
		{
			name:     "PNG",
			data:     pngFixture(t),
			wantExt:  ".png",
			wantMime: "image/png",
		},
		{
			// JPEG's canonical spelling is .jpg, and IsMismatch relies on it
			// being the same string every time rather than sometimes .jpeg.
			name:     "JPEG",
			data:     jpegFixture(t),
			wantExt:  ".jpg",
			wantMime: "image/jpeg",
		},
		{
			name:     "BMP",
			data:     bmpFixture(t),
			wantExt:  ".bmp",
			wantMime: "image/bmp",
		},
		{
			name:     "PDF",
			data:     pdfFixture(),
			wantExt:  ".pdf",
			wantMime: "application/pdf",
		},
		{
			// A ZIP full of XML. Answering ".zip" here would be true and
			// useless: every DOCX on the instance would be flagged.
			name:     "DOCX",
			data:     docxFixture(t),
			wantExt:  ".docx",
			wantMime: "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		},
		{
			name:     "XLSX",
			data:     xlsxFixture(t),
			wantExt:  ".xlsx",
			wantMime: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
		},
		{
			// The file the control exists for.
			name:     "HTML",
			data:     htmlFixture(),
			wantExt:  ".html",
			wantMime: "text/html",
		},
		{
			name:     "HTML with no doctype declaration",
			data:     htmlNoDoctypeFixture(),
			wantExt:  ".html",
			wantMime: "text/html",
		},
		{
			// XML with a scripting model. Not .xml, or an SVG could never be
			// told from a data file.
			name:     "SVG",
			data:     svgFixture(),
			wantExt:  ".svg",
			wantMime: "image/svg+xml",
		},
		{
			name:     "CSV",
			data:     csvFixture(),
			wantExt:  ".csv",
			wantMime: "text/csv",
		},
		{
			name:     "plain text",
			data:     logFixture(),
			wantExt:  ".txt",
			wantMime: "text/plain",
		},
		{
			// "Nothing recognisable" is an answer, not a failure — which is
			// why Detect has no error to return. IsMismatch turns it into a
			// flag saying the content could not be identified, so an
			// unidentifiable file does not look like a verified one.
			name:     "bytes no format claims",
			data:     unrecognisableFixture(),
			wantExt:  "",
			wantMime: "application/octet-stream",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ext, mimeType := attachment.Detect(tc.data)

			if ext != tc.wantExt {
				t.Errorf("Detect() extension = %q, want %q", ext, tc.wantExt)
			}

			// The parameters are a formatting choice — "text/html" and
			// "text/html; charset=utf-8" are the same answer — so compare the
			// media type and let the charset be whatever it is.
			media, _, err := mime.ParseMediaType(mimeType)
			if err != nil {
				t.Fatalf("Detect() MIME type %q does not parse: %v", mimeType, err)
			}
			if media != tc.wantMime {
				t.Errorf("Detect() MIME type = %q, want %q", media, tc.wantMime)
			}

			// The extension is compared against one taken from a filename, so
			// its shape has to be the shape filepath.Ext produces, lowercased.
			if ext != "" && !strings.HasPrefix(ext, ".") {
				t.Errorf("Detect() extension %q has no leading dot; it is compared with filepath.Ext output", ext)
			}
			if ext != strings.ToLower(ext) {
				t.Errorf("Detect() extension %q is not lowercase", ext)
			}
		})
	}
}
