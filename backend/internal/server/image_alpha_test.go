package server_test

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math/rand/v2"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/publiciallc/go-help-desk/backend/internal/domain/ticket"
)

// A picture with transparency in it comes back with the transparency.
//
// Attachments are re-encoded to whichever of JPEG and PNG is smaller, under
// the name they were uploaded with. JPEG has no alpha channel, so for a
// transparent image "smaller" was comparing two different pictures: a 313 KB
// PNG with a fully transparent half came back 37 KB, and every pixel that had
// been invisible was opaque black. It kept its .png name, so somebody who
// attached a logo or an annotated screenshot got a different picture back
// under the name they chose, and was told nothing.
//
// The re-encoding itself is deliberate and documented — it strips camera
// metadata and bounds the stored size. Silently flattening transparency is
// not part of that bargain.
func TestUpload_ATransparentImageKeepsItsTransparency(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	tk, err := h.ticketSvc.Create(t.Context(), ticket.CreateInput{
		Subject: "Transparency", Description: "x", CategoryID: h.catID,
		ReporterUserID: &h.staffID,
	})
	require.NoError(t, err)

	// Half transparent, half a solid colour. Large enough and plain enough
	// that JPEG would win on size if it were allowed to compete — which is
	// the condition under which the bug fired.
	const side = 256
	img := image.NewNRGBA(image.Rect(0, 0, side, side))
	for y := range side {
		for x := range side {
			if x < side/2 {
				img.SetNRGBA(x, y, color.NRGBA{}) // fully transparent
			} else {
				img.SetNRGBA(x, y, color.NRGBA{R: 200, G: 40, B: 40, A: 255})
			}
		}
	}
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))

	res := uploadNamed(t, h, tk.ID.String(), "logo.png", buf.Bytes())
	res.Body.Close()
	require.Equal(t, http.StatusCreated, res.StatusCode)

	list := attachmentsOverHTTP(t, h, tk.ID.String())
	require.Len(t, list, 1)
	require.Equal(t, "logo.png", list[0].Filename)
	require.Equal(t, "image/png", list[0].MimeType,
		"a transparent image stored as image/jpeg has had its transparency deleted")

	dl := h.do(t, http.MethodGet,
		"/api/v1/tickets/"+tk.ID.String()+"/attachments/"+list[0].ID.String(), nil)
	defer dl.Body.Close()
	require.Equal(t, http.StatusOK, dl.StatusCode)

	// The bytes, not just the recorded type. A row saying image/png over a
	// JPEG body is the same defect wearing a label.
	got, _, err := image.Decode(dl.Body)
	require.NoError(t, err)
	_, _, _, a := got.At(10, 10).RGBA()
	require.Zero(t, a, "a pixel that was fully transparent came back opaque")
}

// And an ordinary photograph still gets the smaller of the two, which is the
// whole point of re-encoding. Without this, "never use JPEG" would pass the
// test above and quietly undo the feature.
func TestUpload_AnOpaqueImageStillTakesTheSmallerEncoding(t *testing.T) {
	h, cleanup := newHarness(t)
	defer cleanup()

	tk, err := h.ticketSvc.Create(t.Context(), ticket.CreateInput{
		Subject: "Opaque", Description: "x", CategoryID: h.catID,
		ReporterUserID: &h.staffID,
	})
	require.NoError(t, err)

	// Noise, not a gradient: no transparency, and incompressible enough that
	// PNG cannot win. A patterned image would be the wrong fixture — PNG
	// beats JPEG on those, so the test would pass or fail on the pattern
	// rather than on the rule.
	const side = 256
	rng := rand.New(rand.NewPCG(1, 2))
	img := image.NewNRGBA(image.Rect(0, 0, side, side))
	for y := range side {
		for x := range side {
			img.SetNRGBA(x, y, color.NRGBA{
				R: uint8(rng.UintN(256)),
				G: uint8(rng.UintN(256)),
				B: uint8(rng.UintN(256)),
				A: 255,
			})
		}
	}
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))

	res := uploadNamed(t, h, tk.ID.String(), "photo.png", buf.Bytes())
	res.Body.Close()
	require.Equal(t, http.StatusCreated, res.StatusCode)

	list := attachmentsOverHTTP(t, h, tk.ID.String())
	require.Len(t, list, 1)
	require.Equal(t, "image/jpeg", list[0].MimeType,
		"an opaque image should still be stored in whichever encoding is smaller")
	require.Less(t, list[0].SizeBytes, int64(buf.Len()),
		"the stored file should be smaller than what was uploaded")
}
