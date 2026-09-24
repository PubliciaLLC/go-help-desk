package server_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/admin"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// A type the operator added is never refused for being what they asked for.
//
// The detector reports one canonical extension per format. `.htm` is HTML and
// `.tif` is TIFF, but the detector calls them `.html` and `.tiff`, so an
// operator who allowed `.htm` and received genuine HTML got a 415 reading
// "file content does not match the expected type" — a sentence that is false
// about a file that matches its name exactly, refusing a type they had
// explicitly allowed.
//
// The honest fix is not a bigger synonym table. Containment means we are
// confident the name lied, and for an extension we did not ship we cannot
// establish what it promised: the two libraries involved do not even agree on
// a name for the same format — the standard library calls a Windows executable
// application/x-msdownload where the detector calls it
// application/vnd.microsoft.portable-executable.
//
// So the nine extensions this project ships, each checked against the
// detector, can be contained. Anything the operator added is stored under its
// own name with the detected type recorded and no verdict — neither contained
// nor flagged, because "no contradiction" is a claim we cannot make about a
// spelling we cannot check either. They asked for the type; the least we owe
// them is not to refuse it while telling them something untrue about why.
func TestUpload_AnOperatorAddedTypeIsNotRefusedForMatchingItsName(t *testing.T) {
	for _, handling := range []string{"refuse", "wrap"} {
		t.Run("with mismatch handling "+handling, func(t *testing.T) {
			h, cleanup := newHarness(t)
			defer cleanup()
			ctx := context.Background()

			require.NoError(t, h.adminSvc.SetRaw(ctx, admin.KeyAttachmentMismatchHandling,
				[]byte(`"`+handling+`"`)))
			require.NoError(t, h.adminSvc.SetRaw(ctx, admin.KeyAttachmentAllowedTypes,
				[]byte(`[".pdf",".txt",".log",".png",".htm",".tif",".tgz"]`)))

			tk, err := h.ticketSvc.Create(ctx, ticket.CreateInput{
				Subject: "Operator types", Description: "x", CategoryID: h.catID,
				ReporterUserID: &h.staffID,
			})
			require.NoError(t, err)

			var gz bytes.Buffer
			zw := gzip.NewWriter(&gz)
			_, _ = zw.Write([]byte("2026 INFO rotated\n"))
			require.NoError(t, zw.Close())

			var tiff bytes.Buffer
			// A TIFF header is enough for the detector; the body does not
			// have to be a decodable image for this path.
			tiff.Write([]byte{0x49, 0x49, 0x2A, 0x00})
			tiff.Write(make([]byte, 64))

			cases := []struct {
				name, filename string
				content        []byte
			}{
				{"genuine HTML under the spelling the operator chose", "page.htm",
					[]byte("<html><body><p>a saved page</p></body></html>")},
				{"a genuine TIFF under the short spelling", "scan.tif", tiff.Bytes()},
				{"a gzipped log under the operator's own extension", "logs.tgz", gz.Bytes()},
			}

			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					res := uploadNamed(t, h, tk.ID.String(), tc.filename, tc.content)
					body, _ := readAllBody(res)
					res.Body.Close()
					require.Equal(t, http.StatusCreated, res.StatusCode,
						"the operator allowed this type and the file is what it claims: %s", body)

					got := attachmentOverHTTP(t, h, tk.ID.String(), tc.filename)
					require.Equal(t, tc.filename, got.Filename,
						"a type the operator added must not be renamed into an archive")

					// And not accused, either. The flag is the half staff
					// read, so leaving it asserting a contradiction would be
					// the same false claim in the place it is actually seen:
					// "Content looks like HTML, not a .htm file", about a
					// genuine HTML page under a spelling the operator chose.
					require.Nil(t, got.ContentMismatch,
						"we cannot judge a spelling we did not ship, and saying so is "+
							"not the same as saying nothing is wrong")
					require.NotNil(t, got.DetectedMime,
						"what the content is, is still recorded — that is the part we know")
				})
			}
		})
	}
}

// And the nine we ship keep their containment, or the fix above would be a
// way to turn the whole tier off by adding one extension.
func TestUpload_AShippedTypeIsStillContained(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	require.NoError(t, h.adminSvc.SetRaw(ctx, admin.KeyAttachmentMismatchHandling, []byte(`"wrap"`)))
	require.NoError(t, h.adminSvc.SetRaw(ctx, admin.KeyAttachmentAllowedTypes,
		[]byte(`[".pdf",".txt",".log",".png",".htm"]`)))

	tk, err := h.ticketSvc.Create(ctx, ticket.CreateInput{
		Subject: "Shipped", Description: "x", CategoryID: h.catID,
		ReporterUserID: &h.staffID,
	})
	require.NoError(t, err)

	var img bytes.Buffer
	require.NoError(t, png.Encode(&img, image.NewRGBA(image.Rect(0, 0, 4, 4))))

	// HTML named .pdf: .pdf is ours and was checked against the detector, and
	// .html — the detector's spelling of HTML — is not on this instance's
	// list. Note that .htm is, and that it makes no difference here; see
	// TestUpload_TheEscapeIsDecidedOnTheDetectorsSpelling below.
	res := uploadNamed(t, h, tk.ID.String(), "invoice.pdf",
		[]byte("<html><body>not a pdf</body></html>"))
	res.Body.Close()
	require.Equal(t, http.StatusCreated, res.StatusCode)

	list := attachmentsOverHTTP(t, h, tk.ID.String())
	require.Len(t, list, 1)
	require.NotEqual(t, "invoice.pdf", list[0].Filename,
		"a shipped extension whose content contradicts it is still contained")
}

func readAllBody(res *http.Response) (string, error) {
	var buf bytes.Buffer
	_, err := buf.ReadFrom(res.Body)
	return buf.String(), err
}

var _ = json.Marshal

// Whether a lying file is contained depends on how the operator spelled the
// type it turned out to be.
//
// The containment arm's last question is "is the format this file actually is
// one this instance accepts?", and it answers by looking the detector's
// extension up in the operator's list. Those are two vocabularies and they
// disagree: the detector spells HTML .html, an operator may have written
// .htm, and the same lying file is then contained on one instance and merely
// flagged on another.
//
// Pinned rather than fixed, so that it is a decision and not an accident. The
// fix would be a synonym table, which this branch rejected for containment
// itself: the two libraries involved do not agree on names for one format, so
// there is nothing canonical to build one from, and guessing that two
// spellings mean one format is how a real contradiction stops being
// contained. The error here runs the other way — a file only reaches this
// question by already lying about its name — so the cost is a lying file
// treated strictly, not an ordinary file refused. DESIGN.md says so under
// "Accepted there means the detector's spelling".
func TestUpload_TheEscapeIsDecidedOnTheDetectorsSpelling(t *testing.T) {
	// Genuine HTML under a name claiming PDF. Both instances below accept
	// HTML; they differ only in how they wrote it down.
	const page = "<html><body><p>a saved page</p></body></html>"

	cases := []struct {
		name          string
		allowed       string
		wantContained bool
	}{
		{
			name:          "the operator spelled it the detector's way",
			allowed:       `[".pdf",".html"]`,
			wantContained: false,
		},
		{
			name:          "the operator spelled it the other way",
			allowed:       `[".pdf",".htm"]`,
			wantContained: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, cleanup := newHarness(t)
			defer cleanup()
			ctx := context.Background()

			// wrap rather than refuse, so both outcomes are a 201 and the
			// difference shows in the stored name rather than in a status
			// code. Under refuse the same condition returns 415.
			require.NoError(t, h.adminSvc.SetRaw(ctx, admin.KeyAttachmentMismatchHandling, []byte(`"wrap"`)))
			require.NoError(t, h.adminSvc.SetRaw(ctx, admin.KeyAttachmentAllowedTypes, []byte(tc.allowed)))

			tk, err := h.ticketSvc.Create(ctx, ticket.CreateInput{
				Subject: "Spelling", Description: "x", CategoryID: h.catID,
				ReporterUserID: &h.staffID,
			})
			require.NoError(t, err)

			res := uploadNamed(t, h, tk.ID.String(), "report.pdf", []byte(page))
			res.Body.Close()
			require.Equal(t, http.StatusCreated, res.StatusCode)

			list := attachmentsOverHTTP(t, h, tk.ID.String())
			require.Len(t, list, 1)
			if tc.wantContained {
				require.NotEqual(t, "report.pdf", list[0].Filename,
					"the detector's spelling of HTML is not on this list, so the file is contained")
				return
			}
			require.Equal(t, "report.pdf", list[0].Filename,
				"HTML is accepted here under the detector's own spelling, so there is "+
					"nothing to contain — only something to say")
			require.NotNil(t, list[0].ContentMismatch)
			require.True(t, *list[0].ContentMismatch,
				".pdf is one we ship, and this file is not a PDF")
		})
	}
}
