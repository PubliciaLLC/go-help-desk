package server_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"io/fs"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// #165 step 3: SVG leaves the logo uploader.
//
// None of the download-only reasoning that makes attachments safe applies
// here. The logo is rendered inline in the app chrome, which makes it the one
// uploaded file that really does execute in this origin, where the staff
// sessions live.
//
// The existing defence is a regex sanitiser, and regex-filtering SVG is a
// losing game: the 1.2.0 advisory already contains one escape from it, through
// XML character references that the raw match never saw. PNG covers every real
// use, so the branch is deleted rather than hardened.
//
// These tests are against the HTTP endpoint on purpose. A unit test of the
// sanitiser cannot tell "SVG is refused" from "the sanitiser got stricter",
// and after this change there is no sanitiser to call.

// uploadLogo posts a file to the logo endpoint as the administrator.
// The logo route is not session-gated — it is not in AuthCriticalKeys — so the
// admin API key reaches it, which is what an existing integration would use.
func uploadLogo(t *testing.T, h *harness, filename string, content []byte) *http.Response {
	t.Helper()
	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	fw, err := mw.CreateFormFile("logo", filename)
	require.NoError(t, err)
	_, err = fw.Write(content)
	require.NoError(t, err)
	require.NoError(t, mw.Close())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/settings/logo", body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "ApiKey "+h.adminKey)
	rec := httptest.NewRecorder()
	h.srv.ServeHTTP(rec, req)
	return rec.Result()
}

const plainSVGLogo = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100">` +
	`<circle cx="50" cy="50" r="40" fill="#336699"/></svg>`

// Every SVG is refused now, not only the ones a pattern recognises as
// dangerous. That is the change: the sanitiser accepted anything it did not
// recognise, and "did not recognise" is where every bypass lives.
func TestUploadLogo_RefusesSVG(t *testing.T) {
	// gzipped SVG — what a .svgz actually is. Refused today because the
	// sniffer cannot see "<svg" through the compression, which is luck rather
	// than a decision; pinned so that a future sniffer that learns to
	// decompress does not quietly re-open the door.
	var svgz bytes.Buffer
	zw := gzip.NewWriter(&svgz)
	_, err := zw.Write([]byte(plainSVGLogo))
	require.NoError(t, err)
	require.NoError(t, zw.Close())

	for _, tc := range []struct {
		name     string
		filename string
		content  []byte
	}{
		// The ordinary case, and the one that is accepted today: a perfectly
		// well-behaved logo with no script anywhere in it. It is refused for
		// being SVG, not for being dangerous.
		{"a plain svg", "logo.svg", []byte(plainSVGLogo)},

		// With an XML declaration in front, which the sniffer explicitly
		// handles by looking into the first 512 bytes.
		{"an svg behind an xml declaration", "logo.svg",
			[]byte(`<?xml version="1.0" encoding="UTF-8"?>` + plainSVGLogo)},

		// A lying extension. The handler has never looked at the filename —
		// it sniffs content — so this is the case that catches an
		// implementation which removes SVG by rejecting the extension.
		{"svg content named .png", "logo.png", []byte(plainSVGLogo)},
		{"svg content with no extension", "logo", []byte(plainSVGLogo)},

		// .svgz, as asked for in the issue.
		{"a gzipped svg", "logo.svgz", svgz.Bytes()},

		// These were refused by the sanitiser and must stay refused. If only
		// these still fail, the branch was hardened rather than removed.
		{"an svg carrying a script element", "logo.svg",
			[]byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`)},
		{"an svg carrying an event handler", "logo.svg",
			[]byte(`<svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"></svg>`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// A harness per case. Sharing one would let an SVG accepted by an
			// earlier case sit on disk and fail the disk assertion of a later
			// one, which would report a case as red for somebody else's
			// reason.
			h, cleanup := newHarness(t)
			defer cleanup()

			res := uploadLogo(t, h, tc.filename, tc.content)
			defer res.Body.Close()
			require.Equal(t, http.StatusBadRequest, res.StatusCode,
				"the logo is rendered inline in this origin; no SVG is accepted there")

			// Refused at the door is not enough. The accepted path writes the
			// file and then records the URL in settings, so a refusal that
			// happened after the write would leave markup sitting in the
			// directory the logo route serves from.
			assertNoSVGLogoOnDisk(t, h)
			require.Empty(t, h.adminSvc.SiteLogoURL(context.Background()),
				"a refused logo must not be recorded as the site logo")
		})
	}
}

// A refusal that also refused PNG would pass every case above. This is the
// format the issue says covers every real use, so it has to keep working.
func TestUploadLogo_StillAcceptsPNG(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	res := uploadLogo(t, h, "logo.png", samplePNG(t))
	defer res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode,
		"PNG is what the SVG removal is justified by; it must still upload")
}

// The handler decides on content, not on the filename, and always has. A PNG
// sent under an .svg name is a PNG.
//
// Pinned because "remove SVG" is easiest to implement as a filename check, and
// a filename check both lets real SVG through under another name (covered
// above) and refuses real PNGs under this one.
func TestUploadLogo_JudgesContentNotTheFilename(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	res := uploadLogo(t, h, "logo.svg", samplePNG(t))
	defer res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode,
		"the name is the uploader's claim; the bytes are the fact")
}

// assertNoSVGLogoOnDisk walks the whole attachment directory rather than
// looking up the path the handler happens to use. A check that joined the
// directory layout together would pass if the layout changed while a file sat
// on disk, which is the exact weakness it exists to close.
func assertNoSVGLogoOnDisk(t *testing.T, h *harness) {
	t.Helper()
	var found []string
	err := filepath.WalkDir(h.attachDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() && strings.HasSuffix(strings.ToLower(path), ".svg") {
			found = append(found, path)
		}
		return nil
	})
	require.NoError(t, err)
	require.Empty(t, found, "a refused logo must not leave an SVG under %s", h.attachDir)
}

// Guard against the setting being left behind rather than the file: the logo
// URL is what the frontend renders, so clearing one without the other is a
// broken image at best.
func TestUploadLogo_RefusedSVGLeavesAnExistingPNGLogoAlone(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	ok := uploadLogo(t, h, "logo.png", samplePNG(t))
	defer ok.Body.Close()
	require.Equal(t, http.StatusOK, ok.StatusCode)

	before := h.adminSvc.SiteLogoURL(context.Background())
	require.NotEmpty(t, before, "precondition: a logo is set")

	bad := uploadLogo(t, h, "logo.svg", []byte(plainSVGLogo))
	defer bad.Body.Close()
	require.Equal(t, http.StatusBadRequest, bad.StatusCode)

	require.Equal(t, before, h.adminSvc.SiteLogoURL(context.Background()),
		"a refused upload must not disturb the logo that is already in use")

	// deleteLogoFile runs on the accepted path only; a refusal that reached it
	// would remove the working logo before failing.
	var pngs int
	require.NoError(t, filepath.WalkDir(h.attachDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() && strings.HasSuffix(strings.ToLower(path), ".png") {
			pngs++
		}
		return nil
	}))
	require.Equal(t, 1, pngs, "the existing PNG logo must survive a refused SVG upload")
}
