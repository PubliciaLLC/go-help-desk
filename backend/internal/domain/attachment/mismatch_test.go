package attachment_test

import (
	"testing"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/attachment"
)

// Whether the content contradicts the name.
//
// The rule has exactly two exceptions and a catch-all, and the value of the
// whole control depends on the exceptions staying that small. Widen them and
// the flag stops meaning anything; drop one and the flag fires on ordinary
// files, which is worse than no flag because staff learn to click past it.
//
// Nothing here blocks an upload. A mismatch is recorded and shown, because
// after step 1 nothing renders and the risk is not to this server — it is that
// someone is about to open a file that is not what the help desk called it.
func TestIsMismatch(t *testing.T) {
	cases := []struct {
		name        string
		claimedExt  string
		detectedExt string
		// detectedMIME is what Detect returned alongside the extension. Left
		// empty in most cases and derived from the extension, because for
		// every format with a signature the two are interchangeable. It is set
		// explicitly only where the media type is the thing under test — the
		// textual formats, where the extension alone cannot say whether the
		// content displays or runs.
		detectedMIME string
		want         bool
	}{
		// --- The ordinary case: content agrees with the name. ---
		{
			name:       "a PDF named .pdf",
			claimedExt: ".pdf", detectedExt: ".pdf",
			want: false,
		},
		{
			name:       "a PNG named .png",
			claimedExt: ".png", detectedExt: ".png",
			want: false,
		},
		{
			// The case Go's own sniffer gets wrong, calling it application/zip.
			// Every DOCX on the instance would be flagged.
			name:       "a DOCX named .docx",
			claimedExt: ".docx", detectedExt: ".docx",
			want: false,
		},

		// --- Rule 1: .jpeg and .jpg are one format spelled two ways. ---
		{
			name:       "a JPEG named .jpeg, detected as .jpg",
			claimedExt: ".jpeg", detectedExt: ".jpg",
			want: false,
		},
		{
			// The reverse direction. The detector does not currently produce
			// ".jpeg", but the rule is about two spellings of one format, not
			// about which side a particular detector puts them on.
			name:       "a JPEG named .jpg, detected as .jpeg",
			claimedExt: ".jpg", detectedExt: ".jpeg",
			want: false,
		},

		// --- Rule 2: content detected as plain text matches a text extension. ---
		{
			name:       "text named .txt",
			claimedExt: ".txt", detectedExt: ".txt",
			want: false,
		},
		{
			// The case the rule exists for. A log file has no signature, so
			// the detector can only ever call it .txt; flagging that would
			// mean flagging every log anyone attaches.
			name:       "a log file, which content cannot tell from .txt",
			claimedExt: ".log", detectedExt: ".txt",
			want: false,
		},
		{
			name:       "a CSV the detector read as plain text",
			claimedExt: ".csv", detectedExt: ".txt",
			want: false,
		},
		{
			name:       "markdown, which is plain text",
			claimedExt: ".md", detectedExt: ".txt",
			want: false,
		},
		{
			// The limit of rule 2, and the case that decides whether the rule
			// is useful. "Detected plain text" must not become a blanket
			// excuse: a PDF whose content is plain text is precisely the
			// deception this control is for.
			name:       "a .pdf whose content is plain text",
			claimedExt: ".pdf", detectedExt: ".txt",
			want: true,
		},

		// --- The catch-all: anything else that differs. ---
		{
			// The headline case from the issue.
			name:       "HTML named .pdf",
			claimedExt: ".pdf", detectedExt: ".html",
			want: true,
		},
		{
			// HTML was excluded from rule 2 on the argument that HTML runs
			// when it is opened. That is false in the way that decides this
			// case: what opens a file is chosen by its name, and a thing
			// called notes.txt opens in a text editor whatever is inside it.
			// The exclusion protected nothing and flagged ordinary files.
			name:       "HTML named .txt",
			claimedExt: ".txt", detectedExt: ".html",
			want: false,
		},
		{
			// The design's own canonical ordinary attachment, named as such
			// in #165 and in DESIGN.md: a captured HTTP response saved out of
			// devtools. Flagging it was the product warning about its own
			// documented example.
			name:       "a .log holding a captured HTML response",
			claimedExt: ".log", detectedExt: ".html",
			want: false,
		},
		{
			// Rule 2 is about inert text, not about text extensions being a
			// blanket pass. A rotated log compressed in place is ordinary
			// too, but gzip is not text and the row should say so.
			name:         "a gzipped log still named .log",
			claimedExt:   ".log",
			detectedExt:  ".gz",
			detectedMIME: "application/gzip",
			want:         true,
		},
		{
			name:       "HTML named .png",
			claimedExt: ".png", detectedExt: ".html",
			want: true,
		},
		{
			name:       "a PNG named .jpg",
			claimedExt: ".jpg", detectedExt: ".png",
			want: true,
		},
		{
			// A plain archive under an Office name. There is no "ZIP family"
			// exception: the normalisations are the two above and no others.
			name:       "a plain ZIP named .docx",
			claimedExt: ".docx", detectedExt: ".zip",
			want: true,
		},

		// --- Nothing recognisable, under a claimed binary format, is a
		// mismatch. Under a claimed text extension it is not. ---
		{
			// Saying nothing here would make an unidentifiable file look
			// exactly like a verified one. A PDF that cannot be identified as
			// a PDF is worth a sentence.
			name:       "content that could not be identified, named .pdf",
			claimedExt: ".pdf", detectedExt: "",
			want: true,
		},
		{
			name:       "content that could not be identified, named .png",
			claimedExt: ".png", detectedExt: "",
			want: true,
		},
		{
			// And the case the whole regression was: one NUL byte, one UTF-16
			// code unit, anything the detector treats as binary, and an
			// ordinary log came back unplaceable. The detector failing to
			// place a file is a limitation of the detector, not evidence that
			// the uploader lied — and under a text name there is nothing the
			// flag could usefully warn about anyway.
			name:       "content that could not be identified, named .txt",
			claimedExt: ".txt", detectedExt: "",
			want: false,
		},
		{
			name:       "content that could not be identified, named .log",
			claimedExt: ".log", detectedExt: "",
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mime := tc.detectedMIME
			if mime == "" {
				mime = mimeForExt(tc.detectedExt)
			}
			got := attachment.IsMismatch(tc.claimedExt, tc.detectedExt, mime)
			if got != tc.want {
				t.Errorf("IsMismatch(%q, %q, %q) = %v, want %v",
					tc.claimedExt, tc.detectedExt, mime, got, tc.want)
			}
		})
	}
}

// Both arguments are normalised, not just one.
//
// The claimed extension comes from a filename and the detected one from a
// library, so the two sides arrive in different shapes and neither caller is
// asked to tidy up first. A normaliser applied to one argument only passes
// every case in the table above, because every entry there is already in the
// canonical form.
func TestIsMismatch_NormalisesBothArguments(t *testing.T) {
	spellings := []string{".pdf", "pdf", ".PDF", "PDF", ".Pdf"}

	for _, claimed := range spellings {
		for _, detected := range spellings {
			if attachment.IsMismatch(claimed, detected, "application/pdf") {
				t.Errorf("IsMismatch(%q, %q) = true: these are the same extension",
					claimed, detected)
			}
		}
	}

	// And normalisation must not flatten a real difference into a match.
	for _, claimed := range spellings {
		for _, detected := range []string{".html", "html", ".HTML", "HTML"} {
			if !attachment.IsMismatch(claimed, detected, "text/html") {
				t.Errorf("IsMismatch(%q, %q) = false: HTML is not a PDF",
					claimed, detected)
			}
		}
	}
}

// mimeForExt is what Detect returns alongside each extension the table uses.
// Written out rather than calling Detect, so a change in the library cannot
// quietly change what this test means.
func mimeForExt(ext string) string {
	switch ext {
	case ".pdf":
		return "application/pdf"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".docx":
		return "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	case ".zip":
		return "application/zip"
	case ".html":
		return "text/html"
	case ".txt":
		return "text/plain"
	case "":
		return "application/octet-stream"
	}
	return "application/octet-stream"
}

// JSON and XML keep their promise whatever else the format is called.
//
// Measured against the real detector, not asserted from the registry: a
// GeoJSON document is application/geo+json and an SVG is image/svg+xml, and
// under a .txt name both were flagged as contradicting themselves. Both are
// text, and both say so in the only part of the name a reader that has never
// heard of them can act on — the ending IANA calls a structured syntax
// suffix. This is the same false positive the rule already fixed for NDJSON,
// text/xml and vCard, one family further out.
//
// Run through Detect rather than with hand-written media types so it fails if
// the detector's spelling changes under us, which is how every previous
// version of this rule went wrong.
func TestIsMismatch_StructuredJSONAndXMLAreTextUnderATextName(t *testing.T) {
	cases := []struct {
		name    string
		content []byte
		wantExt string
	}{
		{
			name:    "GeoJSON, which is JSON",
			content: []byte(`{"type":"FeatureCollection","features":[{"type":"Feature","geometry":{"type":"Point","coordinates":[1,2]},"properties":{}}]}`),
			wantExt: ".geojson",
		},
		{
			name:    "SVG, which is XML",
			content: []byte(`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg" width="4" height="4"><rect width="4" height="4"/></svg>`),
			wantExt: ".svg",
		},
		{
			name:    "XHTML, which is XML",
			content: []byte(`<?xml version="1.0"?><!DOCTYPE html><html xmlns="http://www.w3.org/1999/xhtml"><head><title>x</title></head><body>hi</body></html>`),
			wantExt: ".html",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			detectedExt, detectedMIME := attachment.Detect(tc.content)
			if detectedExt != tc.wantExt {
				t.Fatalf("the detector now calls this %q, not %q — the test is about %s, so fix the fixture",
					detectedExt, tc.wantExt, tc.name)
			}
			for _, claimed := range []string{".txt", ".log", ".csv", ".md"} {
				if attachment.IsMismatch(claimed, detectedExt, detectedMIME) {
					t.Errorf("%s under %s is reported as lying about itself (detected %s)",
						tc.name, claimed, detectedMIME)
				}
			}
			// Still a contradiction under a name that promises something else
			// entirely. The relaxation is for text names, not a blanket pass.
			if !attachment.IsMismatch(".pdf", detectedExt, detectedMIME) {
				t.Errorf("%s under .pdf should still be a contradiction", tc.name)
			}
		})
	}
}
