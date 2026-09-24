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
// So the nine extensions this project ships, whose spellings are the
// detector's own, can be contained. Anything the operator added is flagged at
// most. They asked for the type; the least we owe them is not to refuse it
// while telling them something untrue about why.
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

	// HTML named .pdf: .pdf is ours, its spelling is the detector's own, and
	// HTML is not on this instance's list under any spelling.
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
