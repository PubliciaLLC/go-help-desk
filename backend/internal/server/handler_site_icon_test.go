package server_test

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/binary"
	"encoding/json"
	"hash/crc32"
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"runtime"
	"runtime/debug"
	"runtime/metrics"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/audit"
	"github.com/publiciallc/go-help-desk/backend/internal/domain/siteicon"
)

// The site icon (#708): upload, the files it makes, who may reach them, and what
// the public routes send.

// ── Fixtures ─────────────────────────────────────────────────────────────────

// A blue disc on a transparent square, the shape of a typical logo mark.
func iconDisc(side int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, side, side))
	c, r := float64(side)/2, float64(side)*0.4
	for y := range side {
		for x := range side {
			dx, dy := float64(x)+0.5-c, float64(y)+0.5-c
			if dx*dx+dy*dy <= r*r {
				img.SetNRGBA(x, y, color.NRGBA{R: 20, G: 60, B: 200, A: 255})
			}
		}
	}
	return img
}

func iconPNG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var b bytes.Buffer
	require.NoError(t, png.Encode(&b, img))
	return b.Bytes()
}

func iconSolid(w, h int, c color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), image.NewUniform(c), image.Point{}, draw.Src)
	return img
}

// iconPNGHeader is a PNG signature, an IHDR and a stub of IDAT: all that
// image.DecodeConfig reads, so a huge image can be described without making one.
func iconPNGHeader(w, h int, bitDepth, colourType uint8) []byte {
	chunk := func(kind string, data []byte) []byte {
		var b bytes.Buffer
		_ = binary.Write(&b, binary.BigEndian, uint32(len(data)))
		body := append([]byte(kind), data...)
		b.Write(body)
		_ = binary.Write(&b, binary.BigEndian, crc32.ChecksumIEEE(body))
		return b.Bytes()
	}
	var ihdr bytes.Buffer
	_ = binary.Write(&ihdr, binary.BigEndian, uint32(w))
	_ = binary.Write(&ihdr, binary.BigEndian, uint32(h))
	ihdr.Write([]byte{bitDepth, colourType, 0, 0, 0})
	var idat bytes.Buffer
	zw := zlib.NewWriter(&idat)
	_, _ = zw.Write(make([]byte, 64))
	_ = zw.Close()

	out := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1A, '\n'}
	out = append(out, chunk("IHDR", ihdr.Bytes())...)
	out = append(out, chunk("IDAT", idat.Bytes())...)
	return append(out, chunk("IEND", nil)...)
}

// ── Requests ─────────────────────────────────────────────────────────────────

type iconUpload struct {
	field, filename, partType string
	data                      []byte
	background                *string
}

func newIconUpload(data []byte) iconUpload {
	return iconUpload{field: "icon", filename: "icon.png", partType: "image/png", data: data}
}

func (u iconUpload) withBackground(bg string) iconUpload { u.background = &bg; return u }

func iconRequest(t *testing.T, method, path string, u iconUpload) *http.Request {
	t.Helper()
	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	if u.field != "" {
		hdr := textproto.MIMEHeader{}
		hdr.Set("Content-Disposition", `form-data; name="`+u.field+`"; filename="`+u.filename+`"`)
		hdr.Set("Content-Type", u.partType)
		fw, err := mw.CreatePart(hdr)
		require.NoError(t, err)
		_, err = fw.Write(u.data)
		require.NoError(t, err)
	}
	if u.background != nil {
		require.NoError(t, mw.WriteField("background", *u.background))
	}
	require.NoError(t, mw.Close())
	req := httptest.NewRequest(method, path, body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return req
}

func (h *harness) serve(req *http.Request) *http.Response {
	rr := httptest.NewRecorder()
	h.srv.ServeHTTP(rr, req)
	return rr.Result()
}

// uploadIcon posts as the administrator.
func (h *harness) uploadIcon(t *testing.T, u iconUpload) *http.Response {
	t.Helper()
	req := iconRequest(t, http.MethodPost, "/api/v1/admin/settings/icon", u)
	req.Header.Set("Authorization", "ApiKey "+h.adminKey)
	return h.serve(req)
}

// publicGet fetches a public path with no credential and the given headers.
func (h *harness) publicGet(path string, headers ...string) *http.Response {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	return h.serve(req)
}

type iconFileJSON struct {
	Name  string `json:"name"`
	URL   string `json:"url"`
	Sizes []int  `json:"sizes"`
	Bytes int    `json:"bytes"`
}

type iconStatusJSON struct {
	Set        bool           `json:"set"`
	Background string         `json:"background"`
	Version    string         `json:"version"`
	SourceSize int            `json:"source_size"`
	UpdatedAt  *string        `json:"updated_at"`
	Files      []iconFileJSON `json:"files"`
}

type apiErrJSON struct {
	Error struct{ Code, Message string } `json:"error"`
}

func readStatus(t *testing.T, res *http.Response) iconStatusJSON {
	t.Helper()
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, res.StatusCode, string(raw))
	var s iconStatusJSON
	require.NoError(t, json.Unmarshal(raw, &s))
	return s
}

func readAPIError(t *testing.T, res *http.Response) (int, string, string) {
	t.Helper()
	defer res.Body.Close()
	var e apiErrJSON
	raw, _ := io.ReadAll(res.Body)
	require.NoError(t, json.Unmarshal(raw, &e), string(raw))
	return res.StatusCode, e.Error.Code, e.Error.Message
}

func (h *harness) iconStatus(t *testing.T) iconStatusJSON {
	t.Helper()
	return readStatus(t, h.doAsAdmin(t, http.MethodGet, "/api/v1/admin/settings/icon", nil))
}

func (h *harness) iconBytes(t *testing.T, name string) []byte {
	t.Helper()
	res := h.publicGet("/api/v1/branding/icons/" + name)
	defer res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode, name)
	b, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	return b
}

func (h *harness) iconPixel(t *testing.T, name string, x, y int) color.RGBA {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(h.iconBytes(t, name)))
	require.NoError(t, err, name)
	return color.RGBAModel.Convert(img.At(x, y)).(color.RGBA)
}

// iconAudit lists the site icon's audit entries, oldest first.
func (h *harness) iconAudit(t *testing.T) []audit.Entry {
	t.Helper()
	id := uuid.NewSHA1(uuid.NameSpaceOID, []byte("setting:site_icon"))
	got, err := h.auditStore.ListByEntity(context.Background(), "setting", id, 100, 0)
	require.NoError(t, err)
	var out []audit.Entry
	for i := len(got) - 1; i >= 0; i-- { // newest first from the store
		out = append(out, got[i])
	}
	return out
}

func hasSandboxedCSP(t *testing.T, res *http.Response) {
	t.Helper()
	csp := res.Header.Get("Content-Security-Policy")
	for _, want := range []string{"sandbox", "default-src 'none'", "script-src 'none'"} {
		require.Contains(t, csp, want, "an uploaded image is served inert whatever it contains")
	}
	require.Equal(t, "nosniff", res.Header.Get("X-Content-Type-Options"))
}

// ── Upload ───────────────────────────────────────────────────────────────────

func TestUploadSiteIcon_PNG1024(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	src := iconPNG(t, iconDisc(1024))
	res := h.uploadIcon(t, newIconUpload(src))
	st := readStatus(t, res)

	require.True(t, st.Set)
	require.Equal(t, "#ffffff", st.Background, "the default when none is sent")
	require.Regexp(t, `^[0-9a-f]{16}$`, st.Version)
	require.Equal(t, 1024, st.SourceSize)
	require.NotNil(t, st.UpdatedAt)
	require.Len(t, st.Files, 7)

	wantSizes := map[string][]int{
		"favicon.ico": {16, 32, 48}, "favicon-16.png": {16}, "favicon-32.png": {32},
		"apple-touch-icon.png": {180}, "icon-192.png": {192}, "icon-512.png": {512}, "icon-maskable-512.png": {512},
	}
	wantType := map[string]string{"favicon.ico": "image/x-icon"}
	for i, f := range st.Files {
		require.Equal(t, siteicon.Served[i], f.Name, "files are listed in the served order")
		require.Equal(t, wantSizes[f.Name], f.Sizes, f.Name)
		require.Equal(t, "/api/v1/branding/icons/"+f.Name+"?v="+st.Version, f.URL)
		require.Positive(t, f.Bytes, f.Name)

		// Public: no credential.
		pub := h.publicGet("/api/v1/branding/icons/" + f.Name)
		body, err := io.ReadAll(pub.Body)
		pub.Body.Close()
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, pub.StatusCode, f.Name)
		require.Equal(t, f.Bytes, len(body), "the status reports what is served")
		ct := wantType[f.Name]
		if ct == "" {
			ct = "image/png"
		}
		require.Equal(t, ct, pub.Header.Get("Content-Type"), f.Name)
		hasSandboxedCSP(t, pub)

		if ct == "image/png" {
			cfg, err := png.DecodeConfig(bytes.NewReader(body))
			require.NoError(t, err, f.Name)
			require.Equal(t, wantSizes[f.Name][0], cfg.Width, f.Name)
			require.Equal(t, wantSizes[f.Name][0], cfg.Height, f.Name)
		} else {
			require.Equal(t, []byte{0, 0, 1, 0, 3, 0}, body[:6], "an ICO of three images")
		}
	}

	// A transparent corner: kept in a favicon, filled in a home screen icon.
	require.Zero(t, h.iconPixel(t, "favicon-32.png", 0, 0).A)
	require.Equal(t, color.RGBA{R: 255, G: 255, B: 255, A: 255}, h.iconPixel(t, "icon-192.png", 0, 0))
	require.Equal(t, color.RGBA{R: 255, G: 255, B: 255, A: 255}, h.iconPixel(t, "apple-touch-icon.png", 0, 0))

	// The master is never served.
	res = h.publicGet("/api/v1/branding/icons/master.png")
	res.Body.Close()
	require.Equal(t, http.StatusNotFound, res.StatusCode)

	// One entry, with the four keys.
	entries := h.iconAudit(t)
	require.Len(t, entries, 1)
	e := entries[0]
	require.Equal(t, "site_icon_uploaded", e.Action)
	require.Equal(t, "setting", e.EntityType)
	require.NotNil(t, e.ActorID)
	require.Equal(t, h.adminID, *e.ActorID)
	require.Nil(t, e.Before, "nothing was replaced")
	require.Equal(t, st.Version, e.After["icon_version"])
	require.Equal(t, "#ffffff", e.After["icon_background"])
	require.EqualValues(t, 1024, e.After["icon_source_size"])
	require.Regexp(t, `^[0-9a-f]{64}$`, e.After["icon_sha256"])
	require.Len(t, e.After, 4)

	// And the status is what GET says.
	require.Equal(t, st, h.iconStatus(t))
}

func TestUploadSiteIcon_ReplacingRecordsWhatWasThere(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	first := readStatus(t, h.uploadIcon(t, newIconUpload(iconPNG(t, iconDisc(600))).withBackground("#102030")))
	second := readStatus(t, h.uploadIcon(t, newIconUpload(iconPNG(t, iconSolid(512, 512, color.RGBA{R: 200, A: 255})))))
	require.NotEqual(t, first.Version, second.Version)
	require.Equal(t, "#102030", second.Background, "no colour sent: the stored icon's is kept")
	require.Equal(t, 512, second.SourceSize)

	entries := h.iconAudit(t)
	require.Len(t, entries, 2)
	require.Equal(t, first.Version, entries[1].Before["icon_version"])
	require.Equal(t, "#102030", entries[1].Before["icon_background"])
	require.Len(t, entries[1].Before, 3)
	require.Equal(t, second.Version, entries[1].After["icon_version"])
}

func TestUploadSiteIcon_JPEGAndGIF(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	t.Run("jpeg", func(t *testing.T) {
		var b bytes.Buffer
		require.NoError(t, jpeg.Encode(&b, iconSolid(600, 600, color.RGBA{R: 200, G: 30, B: 30, A: 255}), &jpeg.Options{Quality: 95}))
		up := newIconUpload(b.Bytes())
		up.filename, up.partType = "icon.jpg", "image/jpeg"
		st := readStatus(t, h.uploadIcon(t, up))
		require.Equal(t, 600, st.SourceSize)
		px := h.iconPixel(t, "icon-512.png", 256, 256)
		require.InDelta(t, 200, int(px.R), 12)
		require.Less(t, int(px.G), 60)
	})

	t.Run("an animated gif contributes its first frame", func(t *testing.T) {
		pal := color.Palette{color.RGBA{R: 255, A: 255}, color.RGBA{B: 255, A: 255}}
		frame := func(idx uint8) *image.Paletted {
			p := image.NewPaletted(image.Rect(0, 0, 640, 640), pal)
			for i := range p.Pix {
				p.Pix[i] = idx
			}
			return p
		}
		var b bytes.Buffer
		require.NoError(t, gif.EncodeAll(&b, &gif.GIF{Image: []*image.Paletted{frame(0), frame(1)}, Delay: []int{10, 10}}))
		up := newIconUpload(b.Bytes())
		up.filename, up.partType = "icon.gif", "image/gif"
		st := readStatus(t, h.uploadIcon(t, up))
		require.Equal(t, 640, st.SourceSize)
		px := h.iconPixel(t, "icon-512.png", 256, 256)
		require.Greater(t, int(px.R), 240, "frame one is red")
		require.Less(t, int(px.B), 20)
	})
}

// A big source goes through the whole pipeline, at its largest allowed size.
func TestUploadSiteIcon_LargestAllowed(t *testing.T) {
	if testing.Short() {
		t.Skip("decodes a 4096 x 4096 image")
	}
	h, cleanup := newHarness(t)
	defer cleanup()
	st := readStatus(t, h.uploadIcon(t, newIconUpload(iconPNG(t, iconSolid(4096, 4096, color.RGBA{G: 120, A: 255})))))
	require.Equal(t, 4096, st.SourceSize)
	px := h.iconPixel(t, "icon-512.png", 100, 100)
	require.InDelta(t, 120, int(px.G), 2)
}

func TestUploadSiteIcon_Refusals(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	// 5 MB and a byte: a valid PNG signature, then padding.
	overByte := append([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1A, '\n'}, make([]byte, siteicon.MaxUploadBytes+1-8)...)
	eightMB := append([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1A, '\n'}, make([]byte, 8<<20)...)

	// A progressive JPEG whose header names a readable frame and then lies about
	// a segment's length: DecodeConfig is content, the decode budget is not.
	sos := []byte{0x01, 0x01, 0x00, 0x00, 0x3F, 0x00}
	var sof bytes.Buffer
	sof.WriteByte(8)
	_ = binary.Write(&sof, binary.BigEndian, uint16(600))
	_ = binary.Write(&sof, binary.BigEndian, uint16(600))
	sof.Write([]byte{3, 1, 0x11, 0, 2, 0x11, 0, 3, 0x11, 0})
	badJPEG := []byte{0xFF, 0xD8, 0xFF, 0xC2, byte((sof.Len() + 2) >> 8), byte(sof.Len() + 2)}
	badJPEG = append(badJPEG, sof.Bytes()...)
	badJPEG = append(badJPEG, 0xFF, 0xDA, byte((len(sos)+2)>>8), byte(len(sos)+2))
	badJPEG = append(badJPEG, sos...)
	badJPEG = append(badJPEG, 0x11, 0x22, 0x33, 0xFF, 0xFE, 0x00, 0x40, 0x00, 0x00)

	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="1024" height="1024"><circle cx="512" cy="512" r="400"/></svg>`)
	webp := append([]byte("RIFF\x24\x00\x00\x00WEBPVP8 "), make([]byte, 40)...)

	cases := []struct {
		name     string
		up       iconUpload
		status   int
		code     string
		contains string
	}{
		{"not square", newIconUpload(iconPNG(t, iconSolid(800, 600, color.RGBA{A: 255}))),
			400, "icon_not_square", "800 × 600"},
		{"one pixel off square", newIconUpload(iconPNG(t, iconSolid(513, 512, color.RGBA{A: 255}))),
			400, "icon_not_square", "513 × 512"},
		{"too small", newIconUpload(iconPNG(t, iconSolid(256, 256, color.RGBA{A: 255}))),
			400, "icon_too_small", "256 × 256"},
		{"one under the minimum", newIconUpload(iconPNG(t, iconSolid(511, 511, color.RGBA{A: 255}))),
			400, "icon_too_small", "at least 512 × 512"},
		{"too large, header only: never decoded", newIconUpload(iconPNGHeader(5000, 5000, 8, 6)),
			400, "icon_too_large", "5000 × 5000"},
		{"over the decode budget although the side is allowed", newIconUpload(iconPNGHeader(4096, 4096, 16, 6)),
			400, "invalid_image", "The image could not be read"},
		{"five megabytes and a byte", newIconUpload(overByte), 400, "file_too_large", "5 MB"},
		{"a request body far over the limit", newIconUpload(eightMB), 400, "file_too_large", "5 MB"},
		{"SVG, whatever the name and the declared type", newIconUpload(svg), 400, "invalid_type", "PNG, JPG, or GIF"},
		{"SVG named .svg", iconUpload{field: "icon", filename: "icon.svg", partType: "image/svg+xml", data: svg},
			400, "invalid_type", "PNG, JPG, or GIF"},
		{"WebP", newIconUpload(webp), 400, "invalid_type", "PNG, JPG, or GIF"},
		{"nothing at all", newIconUpload(nil), 400, "invalid_type", ""},
		{"PNG magic and garbage", newIconUpload(append([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1A, '\n'}, []byte("this is not a png")...)),
			400, "invalid_image", "The image could not be read"},
		{"a JPEG whose header cannot be read", newIconUpload(badJPEG), 400, "invalid_image", "The image could not be read"},
		{"truncated PNG data", newIconUpload(iconPNGHeader(1024, 1024, 8, 6)), 400, "invalid_image", "The image could not be read"},
		{"no icon field", iconUpload{field: "logo", filename: "icon.png", partType: "image/png", data: iconPNG(t, iconDisc(600))},
			400, "missing_field", "'icon'"},
		{"no file at all", iconUpload{}, 400, "missing_field", "'icon'"},
		{"bad background, three digits", newIconUpload(iconPNG(t, iconDisc(600))).withBackground("#fff"),
			400, "invalid_background", "#ffffff"},
		{"bad background, a name", newIconUpload(iconPNG(t, iconDisc(600))).withBackground("white"),
			400, "invalid_background", "hex colour"},

		// An upload that breaks two rules gets the code of the one checked first.
		// The order is documented (openapi.yaml, uploadSiteIcon): the form, the
		// field, the colour, the file's size, its type, its header, its shape
		// (not square, then too small, then too large), and last what decoding
		// would cost. A refusal that depended on anything else would send the
		// administrator to fix the wrong thing first.
		{"colour before type: bad background and SVG", newIconUpload(svg).withBackground("#fff"),
			400, "invalid_background", "#ffffff"},
		{"colour before size: bad background and over 5 MB", newIconUpload(overByte).withBackground("white"),
			400, "invalid_background", "hex colour"},
		{"colour before shape: bad background and not square", newIconUpload(iconPNG(t, iconSolid(800, 600, color.RGBA{A: 255}))).withBackground("#12345"),
			400, "invalid_background", "#ffffff"},
		{"size before type: over 5 MB and SVG", newIconUpload(append(append([]byte{}, svg...), make([]byte, siteicon.MaxUploadBytes)...)),
			400, "file_too_large", "5 MB"},
		{"type before shape: SVG that is not square", newIconUpload([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="800" height="600"/>`)),
			400, "invalid_type", "PNG, JPG, or GIF"},
		{"header before shape: garbage that is not square", newIconUpload(append([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1A, '\n'}, make([]byte, 24)...)),
			400, "invalid_image", "could not be read"},
		{"shape before size: not square and too small", newIconUpload(iconPNG(t, iconSolid(300, 200, color.RGBA{A: 255}))),
			400, "icon_not_square", "300 × 200"},
		{"shape before size: not square and too large", newIconUpload(iconPNGHeader(5000, 4000, 8, 6)),
			400, "icon_not_square", "5000 × 4000"},
		{"too small before too large: square checks in order", newIconUpload(iconPNG(t, iconSolid(100, 100, color.RGBA{A: 255}))),
			400, "icon_too_small", "100 × 100"},
		{"shape before the decode budget: not square and over it", newIconUpload(iconPNGHeader(4096, 4000, 16, 6)),
			400, "icon_not_square", "4096 × 4000"},
		{"size before the decode budget: too large and over it", newIconUpload(iconPNGHeader(5000, 5000, 16, 6)),
			400, "icon_too_large", "5000 × 5000"},
		{"shape before the decode: not square and truncated", newIconUpload(iconPNGHeader(800, 600, 8, 6)),
			400, "icon_not_square", "800 × 600"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, code, msg := readAPIError(t, h.uploadIcon(t, tc.up))
			require.Equal(t, tc.status, status)
			require.Equal(t, tc.code, code, msg)
			require.Contains(t, msg, tc.contains)

			// Nothing was stored and nothing was recorded, whichever check refused.
			require.False(t, h.iconStatus(t).Set)
			require.Empty(t, h.iconAudit(t))
		})
	}

	t.Run("not multipart", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/settings/icon", strings.NewReader("icon=1"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Authorization", "ApiKey "+h.adminKey)
		status, code, _ := readAPIError(t, h.serve(req))
		require.Equal(t, 400, status)
		require.Equal(t, "invalid_form", code)
	})
}

func TestUploadSiteIcon_RefusalKeepsPreviousIcon(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	good := readStatus(t, h.uploadIcon(t, newIconUpload(iconPNG(t, iconDisc(700))).withBackground("#445566")))
	before := h.iconBytes(t, "icon-512.png")

	status, code, _ := readAPIError(t, h.uploadIcon(t, newIconUpload(iconPNG(t, iconSolid(900, 800, color.RGBA{A: 255})))))
	require.Equal(t, 400, status)
	require.Equal(t, "icon_not_square", code)

	require.Equal(t, good, h.iconStatus(t), "the first icon is still the one in force")
	require.Equal(t, before, h.iconBytes(t, "icon-512.png"))
	require.Len(t, h.iconAudit(t), 1)
}

// A small file whose header claims an enormous image is refused from the
// header. The memory check is the point: a decode of 30000 x 30000 would
// allocate 900 MB before it found the data was missing.
func TestUploadSiteIcon_DecompressionBombNeverDecodes(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	for name, data := range map[string][]byte{
		"30000 x 30000, 8-bit grey": iconPNGHeader(30000, 30000, 8, 0),
		"16-bit RGBA at 4096":       iconPNGHeader(4096, 4096, 16, 6),
	} {
		t.Run(name, func(t *testing.T) {
			require.Less(t, len(data), 1<<10, "the attack is that the file is small")
			req := iconRequest(t, http.MethodPost, "/api/v1/admin/settings/icon", newIconUpload(data))
			req.Header.Set("Authorization", "ApiKey "+h.adminKey)

			runtime.GC()
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			res := h.serve(req)
			runtime.ReadMemStats(&after)

			status, _, _ := readAPIError(t, res)
			require.Equal(t, http.StatusBadRequest, status)
			grew := int64(after.TotalAlloc) - int64(before.TotalAlloc)
			require.Less(t, grew, int64(50<<20),
				"the refusal allocated %d bytes: it must reject from the header, not decode first", grew)
			require.False(t, h.iconStatus(t).Set)
		})
	}
}

// The other end of the budget: an image the decode check ACCEPTS must cost what
// the check says. A 4096 x 4096 RGBA PNG is 64 MiB decoded, under the 100 MB a
// slot is allowed, and scaling it in one step used to add a work buffer of
// another 64 MiB (draw.CatmullRom: destination width x source height x 32).
// With four slots, that was 600 MB of heap on a 5 MB upload.
//
// The peak is sampled while the request runs, with the collector made eager so
// that dead buffers are not counted as live.
func TestUploadSiteIcon_LargestAcceptedImageStaysInTheSlotBudget(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	img := image.NewRGBA(image.Rect(0, 0, siteicon.MaxSide, siteicon.MaxSide))
	for y := range siteicon.MaxSide {
		for x := range siteicon.MaxSide {
			if x%8 < 4 {
				img.SetRGBA(x, y, color.RGBA{R: 20, G: 60, B: 200, A: 255})
			}
		}
	}
	data := iconPNG(t, img)
	img = nil
	require.Less(t, len(data), siteicon.MaxUploadBytes, "the fixture must be an acceptable upload")
	req := iconRequest(t, http.MethodPost, "/api/v1/admin/settings/icon", newIconUpload(data))
	req.Header.Set("Authorization", "ApiKey "+h.adminKey)

	defer debug.SetGCPercent(debug.SetGCPercent(5))
	runtime.GC()
	sample := []metrics.Sample{{Name: "/memory/classes/heap/objects:bytes"}}
	metrics.Read(sample)
	base := sample[0].Value.Uint64()

	var peak atomic.Uint64
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		s := []metrics.Sample{{Name: sample[0].Name}}
		for {
			metrics.Read(s)
			if v := s[0].Value.Uint64(); v > peak.Load() {
				peak.Store(v)
			}
			select {
			case <-stop:
				return
			case <-time.After(time.Millisecond):
			}
		}
	}()
	st := readStatus(t, h.serve(req))
	close(stop)
	<-done

	require.Equal(t, siteicon.MaxSide, st.SourceSize)
	used := int64(peak.Load()) - int64(base)
	t.Logf("peak heap above the baseline: %d MiB", used>>20)
	require.Less(t, used, int64(100<<20), "one 4096 px upload held %d bytes at its peak", used)
}

func TestUploadSiteIcon_Background(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	src := iconPNG(t, iconDisc(600))

	// Sent in upper case, stored in lower.
	st := readStatus(t, h.uploadIcon(t, newIconUpload(src).withBackground("#AABBCC")))
	require.Equal(t, "#aabbcc", st.Background)
	require.Equal(t, color.RGBA{R: 0xaa, G: 0xbb, B: 0xcc, A: 255}, h.iconPixel(t, "icon-192.png", 0, 0))

	// Empty is "not sent": the stored icon's colour is kept.
	st = readStatus(t, h.uploadIcon(t, newIconUpload(src).withBackground("")))
	require.Equal(t, "#aabbcc", st.Background)

	// After the icon is removed, the default again.
	res := h.doAsAdmin(t, http.MethodDelete, "/api/v1/admin/settings/icon", nil)
	res.Body.Close()
	st = readStatus(t, h.uploadIcon(t, newIconUpload(src)))
	require.Equal(t, "#ffffff", st.Background)
}

// ── Status ───────────────────────────────────────────────────────────────────

func TestGetSiteIcon_NotSet(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	res := h.doAsAdmin(t, http.MethodGet, "/api/v1/admin/settings/icon", nil)
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.JSONEq(t, `{"set": false, "background": "#ffffff", "version": "", "source_size": 0, "updated_at": null, "files": []}`, string(raw))
}

// ── Public routes ────────────────────────────────────────────────────────────

func TestSiteIconRoutes_Caching(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	st := readStatus(t, h.uploadIcon(t, newIconUpload(iconPNG(t, iconDisc(600)))))
	etag := `"` + st.Version + `"`
	path := "/api/v1/branding/icons/icon-192.png"

	get := func(query string, headers ...string) *http.Response {
		res := h.publicGet(path+query, headers...)
		return res
	}

	t.Run("the current version is immutable", func(t *testing.T) {
		res := get("?v=" + st.Version)
		defer res.Body.Close()
		require.Equal(t, 200, res.StatusCode)
		require.Equal(t, "public, max-age=31536000, immutable", res.Header.Get("Cache-Control"))
		require.Equal(t, etag, res.Header.Get("ETag"))
		require.Equal(t, "image/png", res.Header.Get("Content-Type"))
		hasSandboxedCSP(t, res)
	})

	t.Run("no version, or a stale one, must revalidate and gets the same bytes", func(t *testing.T) {
		cur := h.iconBytes(t, "icon-192.png")
		for _, q := range []string{"", "?v=0000000000000000", "?v=", "?x=1"} {
			res := get(q)
			body, _ := io.ReadAll(res.Body)
			res.Body.Close()
			require.Equal(t, 200, res.StatusCode, q)
			require.Equal(t, "no-cache", res.Header.Get("Cache-Control"), q)
			require.Equal(t, etag, res.Header.Get("ETag"), q)
			require.Equal(t, cur, body, q)
		}
	})

	t.Run("If-None-Match", func(t *testing.T) {
		for _, inm := range []string{etag, "*", `W/` + etag, `"other", ` + etag} {
			res := get("?v="+st.Version, "If-None-Match", inm)
			body, _ := io.ReadAll(res.Body)
			res.Body.Close()
			require.Equal(t, http.StatusNotModified, res.StatusCode, inm)
			require.Empty(t, body, inm)
			require.Equal(t, etag, res.Header.Get("ETag"))
			require.Equal(t, "public, max-age=31536000, immutable", res.Header.Get("Cache-Control"))
			hasSandboxedCSP(t, res)
		}
		res := get("", "If-None-Match", `"0000000000000000"`)
		res.Body.Close()
		require.Equal(t, 200, res.StatusCode, "another version's tag is not a match")
	})

	t.Run("a new icon has a new tag", func(t *testing.T) {
		st2 := readStatus(t, h.uploadIcon(t, newIconUpload(iconPNG(t, iconSolid(512, 512, color.RGBA{R: 9, A: 255})))))
		require.NotEqual(t, st.Version, st2.Version)
		res := get("?v="+st.Version, "If-None-Match", etag)
		res.Body.Close()
		require.Equal(t, 200, res.StatusCode, "the old tag no longer matches")
		require.Equal(t, "no-cache", res.Header.Get("Cache-Control"), "and the old version is no longer immutable")
	})
}

func TestSiteIconRoutes_Unknown(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	check := func(t *testing.T, path string) {
		t.Helper()
		res := h.publicGet(path)
		defer res.Body.Close()
		require.Equal(t, http.StatusNotFound, res.StatusCode, path)
		require.True(t, strings.HasPrefix(res.Header.Get("Content-Type"), "text/plain"), path)
	}

	t.Run("nothing stored", func(t *testing.T) {
		for _, n := range siteicon.Served {
			check(t, "/api/v1/branding/icons/"+n)
		}
	})

	h.uploadIcon(t, newIconUpload(iconPNG(t, iconDisc(600)))).Body.Close()

	t.Run("a name that is not served", func(t *testing.T) {
		for _, n := range []string{"master.png", "foo.png", "favicon-48.png", "..%2Fx", "%2e%2e%2fmaster.png", "icon-192.PNG", "icon-192.png%00", ""} {
			check(t, "/api/v1/branding/icons/"+n)
		}
		check(t, "/api/v1/branding/icons/../icons/master.png")
	})
	t.Run("served once something is stored", func(t *testing.T) {
		for _, n := range siteicon.Served {
			res := h.publicGet("/api/v1/branding/icons/" + n)
			res.Body.Close()
			require.Equal(t, 200, res.StatusCode, n)
		}
	})
}

// ── Background change ────────────────────────────────────────────────────────

func (h *harness) patchIcon(t *testing.T, body any) *http.Response {
	t.Helper()
	return h.doAsAdmin(t, http.MethodPatch, "/api/v1/admin/settings/icon", body)
}

func TestUpdateSiteIconBackground(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	// Nothing to recolour yet.
	status, code, _ := readAPIError(t, h.patchIcon(t, map[string]string{"background": "#112233"}))
	require.Equal(t, http.StatusNotFound, status)
	require.Equal(t, "no_icon", code)

	first := readStatus(t, h.uploadIcon(t, newIconUpload(iconPNG(t, iconDisc(1024)))))
	fav32 := h.iconBytes(t, "favicon-32.png")
	ico := h.iconBytes(t, "favicon.ico")
	apple := h.iconBytes(t, "apple-touch-icon.png")

	st := readStatus(t, h.patchIcon(t, map[string]string{"background": "#112233"}))
	require.Equal(t, "#112233", st.Background)
	require.NotEqual(t, first.Version, st.Version, "the filled files changed, so the version did")
	require.Equal(t, first.SourceSize, st.SourceSize)
	require.Len(t, st.Files, 7)
	for _, f := range st.Files {
		require.Contains(t, f.URL, "?v="+st.Version)
	}

	require.Equal(t, color.RGBA{R: 0x11, G: 0x22, B: 0x33, A: 255}, h.iconPixel(t, "apple-touch-icon.png", 0, 0))
	require.Equal(t, color.RGBA{R: 0x11, G: 0x22, B: 0x33, A: 255}, h.iconPixel(t, "icon-maskable-512.png", 0, 0))
	require.NotEqual(t, apple, h.iconBytes(t, "apple-touch-icon.png"))
	require.Equal(t, fav32, h.iconBytes(t, "favicon-32.png"), "the transparent files do not depend on the colour")
	require.Equal(t, ico, h.iconBytes(t, "favicon.ico"))
	require.Zero(t, h.iconPixel(t, "favicon-32.png", 0, 0).A)

	entries := h.iconAudit(t)
	require.Len(t, entries, 2)
	require.Equal(t, "site_icon_background_changed", entries[1].Action)
	require.Equal(t, map[string]any{"icon_background": "#ffffff", "icon_version": first.Version}, entries[1].Before)
	require.Equal(t, map[string]any{"icon_background": "#112233", "icon_version": st.Version}, entries[1].After)
	require.Equal(t, h.adminID, *entries[1].ActorID)

	// The same colour again, in any case: 200, nothing written, nothing recorded.
	again := readStatus(t, h.patchIcon(t, map[string]string{"background": "#112233"}))
	require.Equal(t, st, again)
	require.Len(t, h.iconAudit(t), 2)

	// Upper case is the same colour once stored lower-case.
	upper := readStatus(t, h.patchIcon(t, map[string]string{"background": "#ABCDEF"}))
	require.Equal(t, "#abcdef", upper.Background)
	require.Len(t, h.iconAudit(t), 3)
	same := readStatus(t, h.patchIcon(t, map[string]string{"background": "#abcdef"}))
	require.Equal(t, upper, same)
	require.Len(t, h.iconAudit(t), 3)
	st = upper

	// Refusals.
	for name, body := range map[string]any{
		"too short": map[string]string{"background": "#12"}, "missing": map[string]string{},
		"a number": map[string]any{"background": 123}, "null": map[string]any{"background": nil},
	} {
		status, code, _ := readAPIError(t, h.patchIcon(t, body))
		require.Equal(t, http.StatusBadRequest, status, name)
		require.Equal(t, "invalid_background", code, name)
	}
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/admin/settings/icon", strings.NewReader("{not json"))
	req.Header.Set("Authorization", "ApiKey "+h.adminKey)
	req.Header.Set("Content-Type", "application/json")
	status, code, _ = readAPIError(t, h.serve(req))
	require.Equal(t, http.StatusBadRequest, status)
	require.Equal(t, "bad_request", code)
	require.Equal(t, st, h.iconStatus(t), "refusals changed nothing")
	require.Len(t, h.iconAudit(t), 3)

	// The recolour drifts nothing: colour back to white leaves the corner white.
	readStatus(t, h.patchIcon(t, map[string]string{"background": "#ffffff"}))
	require.Equal(t, color.RGBA{R: 255, G: 255, B: 255, A: 255}, h.iconPixel(t, "icon-512.png", 0, 0))
	require.Equal(t, fav32, h.iconBytes(t, "favicon-32.png"))
}

// ── Delete ───────────────────────────────────────────────────────────────────

func TestDeleteSiteIcon(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	del := func() *http.Response {
		res := h.doAsAdmin(t, http.MethodDelete, "/api/v1/admin/settings/icon", nil)
		res.Body.Close()
		return res
	}

	// Idempotent from the start: nothing there, 204, no entry.
	require.Equal(t, http.StatusNoContent, del().StatusCode)
	require.Empty(t, h.iconAudit(t))

	st := readStatus(t, h.uploadIcon(t, newIconUpload(iconPNG(t, iconDisc(600)))))
	m := h.manifest(t)
	require.Len(t, m.Icons, 3, "an icon is in force: the manifest names its files")
	require.Equal(t, st.Background, m.ThemeColor)

	require.Equal(t, http.StatusNoContent, del().StatusCode)
	require.False(t, h.iconStatus(t).Set)
	for _, n := range siteicon.Served {
		res := h.publicGet("/api/v1/branding/icons/" + n)
		res.Body.Close()
		require.Equal(t, http.StatusNotFound, res.StatusCode, n)
	}
	m = h.manifest(t)
	require.Len(t, m.Icons, 1, "the manifest is back to the built-in icon")
	require.Equal(t, "/favicon.svg", m.Icons[0].Src)
	require.Empty(t, m.ThemeColor)

	entries := h.iconAudit(t)
	require.Len(t, entries, 2)
	require.Equal(t, "site_icon_removed", entries[1].Action)
	require.Equal(t, st.Version, entries[1].Before["icon_version"])
	require.Equal(t, "#ffffff", entries[1].Before["icon_background"])
	require.Regexp(t, `^[0-9a-f]{64}$`, entries[1].Before["icon_sha256"])
	require.Nil(t, entries[1].After)
	require.Equal(t, h.adminID, *entries[1].ActorID)

	// Again: still 204, and no second entry.
	require.Equal(t, http.StatusNoContent, del().StatusCode)
	require.Len(t, h.iconAudit(t), 2)
}

// ── Who may reach it ─────────────────────────────────────────────────────────

func TestSiteIconAdmin_Auth(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	png := iconPNG(t, iconDisc(600))
	type call struct {
		method string
		body   any
		upload bool
	}
	calls := map[string]call{
		"GET":    {method: http.MethodGet},
		"POST":   {method: http.MethodPost, upload: true},
		"PATCH":  {method: http.MethodPatch, body: map[string]string{"background": "#000000"}},
		"DELETE": {method: http.MethodDelete},
	}
	const path = "/api/v1/admin/settings/icon"
	send := func(c call, key string) *http.Response {
		var req *http.Request
		if c.upload {
			req = iconRequest(t, c.method, path, newIconUpload(png))
		} else {
			var buf bytes.Buffer
			if c.body != nil {
				require.NoError(t, json.NewEncoder(&buf).Encode(c.body))
			}
			req = httptest.NewRequest(c.method, path, &buf)
			if c.body != nil {
				req.Header.Set("Content-Type", "application/json")
			}
		}
		if key != "" {
			req.Header.Set("Authorization", "ApiKey "+key)
		}
		return h.serve(req)
	}

	for name, c := range calls {
		t.Run(name, func(t *testing.T) {
			for who, key := range map[string]string{"staff": h.apiKey, "reporter": h.userKey} {
				res := send(c, key)
				res.Body.Close()
				require.Equal(t, http.StatusForbidden, res.StatusCode, who)
			}
			res := send(c, "")
			res.Body.Close()
			require.Equal(t, http.StatusUnauthorized, res.StatusCode, "unauthenticated")
		})
	}
	require.False(t, h.iconStatus(t).Set, "none of the refused calls wrote anything")

	// An administrator's API key with the settings scope is allowed all four,
	// as it is for the logo.
	for _, name := range []string{"POST", "GET", "PATCH", "DELETE"} {
		res := send(calls[name], h.adminKey)
		res.Body.Close()
		require.Less(t, res.StatusCode, 300, name)
	}
}

// ── Manifest ─────────────────────────────────────────────────────────────────

type manifestJSON struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	ShortName       string `json:"short_name"`
	StartURL        string `json:"start_url"`
	Scope           string `json:"scope"`
	Display         string `json:"display"`
	BackgroundColor string `json:"background_color"`
	ThemeColor      string `json:"theme_color"`
	Icons           []struct {
		Src     string `json:"src"`
		Sizes   string `json:"sizes"`
		Type    string `json:"type"`
		Purpose string `json:"purpose"`
	} `json:"icons"`
}

func (h *harness) manifest(t *testing.T) manifestJSON {
	t.Helper()
	res := h.publicGet("/api/v1/branding/manifest.webmanifest")
	defer res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)
	var m manifestJSON
	require.NoError(t, json.NewDecoder(res.Body).Decode(&m))
	return m
}

func TestManifest_Default(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	res := h.publicGet("/api/v1/branding/manifest.webmanifest")
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)

	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, "application/manifest+json; charset=utf-8", res.Header.Get("Content-Type"))
	require.Equal(t, "no-cache", res.Header.Get("Cache-Control"))
	etag := res.Header.Get("ETag")
	require.Regexp(t, `^"[0-9a-f]{16}"$`, etag)
	require.JSONEq(t, `{
		"id": "/", "name": "Go Help Desk", "short_name": "Go Help Desk",
		"start_url": "/", "scope": "/", "display": "standalone",
		"background_color": "#ffffff",
		"icons": [{"src": "/favicon.svg", "sizes": "any", "type": "image/svg+xml", "purpose": "any"}]
	}`, string(raw))
	require.NotContains(t, string(raw), "theme_color", "no icon, no theme colour")
	// No default CSP replacement: the manifest is not an uploaded file.
	require.NotContains(t, res.Header.Get("Content-Security-Policy"), "sandbox")

	// Revalidation.
	res2 := h.publicGet("/api/v1/branding/manifest.webmanifest", "If-None-Match", etag)
	body2, _ := io.ReadAll(res2.Body)
	res2.Body.Close()
	require.Equal(t, http.StatusNotModified, res2.StatusCode)
	require.Empty(t, body2)
	require.Equal(t, etag, res2.Header.Get("ETag"))
	require.Equal(t, "no-cache", res2.Header.Get("Cache-Control"))
}

func TestManifest_WithIconAndNames(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()
	require.NoError(t, h.adminSvc.SetString(ctx, "site_name", "Acme IT Help Desk"))

	st := readStatus(t, h.uploadIcon(t, newIconUpload(iconPNG(t, iconDisc(600))).withBackground("#102030")))
	res := h.publicGet("/api/v1/branding/manifest.webmanifest")
	raw, _ := io.ReadAll(res.Body)
	res.Body.Close()
	require.JSONEq(t, `{
		"id": "/", "name": "Acme IT Help Desk", "short_name": "Acme IT Help Desk",
		"start_url": "/", "scope": "/", "display": "standalone",
		"background_color": "#102030", "theme_color": "#102030",
		"icons": [
			{"src": "/api/v1/branding/icons/icon-192.png?v=`+st.Version+`", "sizes": "192x192", "type": "image/png", "purpose": "any"},
			{"src": "/api/v1/branding/icons/icon-512.png?v=`+st.Version+`", "sizes": "512x512", "type": "image/png", "purpose": "any"},
			{"src": "/api/v1/branding/icons/icon-maskable-512.png?v=`+st.Version+`", "sizes": "512x512", "type": "image/png", "purpose": "maskable"}
		]
	}`, string(raw))
	// The order the fields are written in, which a browser ignores and a diff reader does not.
	require.Less(t, strings.Index(string(raw), `"id"`), strings.Index(string(raw), `"name"`))
	require.Less(t, strings.Index(string(raw), `"scope"`), strings.Index(string(raw), `"icons"`))
	etag1 := res.Header.Get("ETag")

	// The short name, when there is one.
	require.NoError(t, h.adminSvc.SetString(ctx, "site_short_name", "IT"))
	m := h.manifest(t)
	require.Equal(t, "Acme IT Help Desk", m.Name)
	require.Equal(t, "IT", m.ShortName)
	res = h.publicGet("/api/v1/branding/manifest.webmanifest")
	res.Body.Close()
	require.NotEqual(t, etag1, res.Header.Get("ETag"), "the tag follows the content")

	// Only the names and the icon's own facts: nothing about the instance or its people.
	res = h.publicGet("/api/v1/branding/manifest.webmanifest")
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	var generic map[string]any
	require.NoError(t, json.Unmarshal(body, &generic))
	require.ElementsMatch(t, []string{"id", "name", "short_name", "start_url", "scope", "display",
		"background_color", "theme_color", "icons"}, iconManifestKeys(generic))
}

func iconManifestKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// A site name is the administrator's text, and this document is read by every
// browser that visits: it is escaped as JSON, so no raw markup survives in it.
func TestManifest_HostileNameIsData(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	const name = `</script><b>"&'`
	require.NoError(t, h.adminSvc.SetString(context.Background(), "site_name", name))

	res := h.publicGet("/api/v1/branding/manifest.webmanifest")
	raw, _ := io.ReadAll(res.Body)
	res.Body.Close()
	require.NotContains(t, string(raw), "<", "markup characters are \\u-escaped in the JSON text")
	var m manifestJSON
	require.NoError(t, json.Unmarshal(raw, &m))
	require.Equal(t, name, m.Name, "and decode back to exactly what was typed")
	require.Equal(t, name, m.ShortName)
}

// ── The setting ──────────────────────────────────────────────────────────────

func TestUpdateSettings_SiteShortName(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()
	patch := func(v any) *http.Response {
		return h.doAsAdmin(t, http.MethodPatch, "/api/v1/admin/settings", map[string]any{"site_short_name": v})
	}

	for name, v := range map[string]any{
		"thirty-one characters": strings.Repeat("a", 31), "a number": 123, "null": nil,
		"a control character": "\u0007", "a newline": "a\nb", "a boolean": true,
		"a right-to-left override": "a\u202eb", "a zero-width space": "a\u200bb", "U+2028": "a\u2028b",
	} {
		t.Run("refused: "+name, func(t *testing.T) {
			status, code, msg := readAPIError(t, patch(v))
			require.Equal(t, http.StatusBadRequest, status)
			require.Equal(t, "invalid_site_short_name", code)
			require.Contains(t, msg, "30 characters")
		})
	}
	st, err := h.adminSvc.ListAll(context.Background())
	require.NoError(t, err)
	require.NotContains(t, st, "site_short_name", "refusals wrote nothing")

	for name, v := range map[string]string{"empty": "", "a name": "Help", "thirty": strings.Repeat("é", 30)} {
		t.Run("accepted: "+name, func(t *testing.T) {
			res := patch(v)
			res.Body.Close()
			require.Equal(t, http.StatusNoContent, res.StatusCode)
		})
	}

	res := patch("Help")
	res.Body.Close()
	res = h.doAsAdmin(t, http.MethodGet, "/api/v1/admin/settings", nil)
	defer res.Body.Close()
	var dump map[string]any
	require.NoError(t, json.NewDecoder(res.Body).Decode(&dump))
	require.Equal(t, "Help", dump["site_short_name"])

	// A machine credential may set it, as it may the site name.
	res = h.doAsAdmin(t, http.MethodPatch, "/api/v1/admin/settings", map[string]any{"site_short_name": "API"})
	res.Body.Close()
	require.Equal(t, http.StatusNoContent, res.StatusCode)
}
