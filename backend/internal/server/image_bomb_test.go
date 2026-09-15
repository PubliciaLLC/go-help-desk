package server

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

// bombPNG builds a small file that decodes enormous: uniform colour compresses
// to almost nothing, so the file is KBs and the raster is GBs.
func bombPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewGray(image.Rect(0, 0, w, h))
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return buf.Bytes()
}

// A byte-size limit is not a memory limit. Under the 25 MB upload cap a single
// request could ask for roughly 20 GB of heap, and requests run concurrently —
// so one authenticated user uploading to their own ticket could take the
// process down.
func TestCompressImage_RefusesADecompressionBomb(t *testing.T) {
	// ~12000x12000 = 144 Mpx. Small file, 142 MB decoded.
	data := bombPNG(t, 12000, 12000)
	require.Less(t, len(data), 1<<20, "the attack is that the FILE is small")

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)

	_, _, err := compressImage(data, ".png")

	runtime.ReadMemStats(&after)
	require.Error(t, err, "a 144-megapixel image must be refused")
	require.Contains(t, err.Error(), "pixels")

	// Refused by reading the header, not by decoding and then measuring.
	grew := int64(after.TotalAlloc) - int64(before.TotalAlloc)
	require.Less(t, grew, int64(32<<20),
		"the refusal allocated %d bytes — it must reject from the header, not decode first", grew)
}

func TestResizeRasterLogo_RefusesADecompressionBomb(t *testing.T) {
	data := bombPNG(t, 12000, 12000)
	_, err := resizeRasterLogo(data, "png")
	require.Error(t, err)
	require.Contains(t, err.Error(), "pixels")
}

// An ordinary image must still work. A limit that refused everything would pass
// the tests above.
func TestCompressImage_AcceptsANormalImage(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 1920, 1080))
	for x := 0; x < 64; x++ {
		img.Set(x, x, color.RGBA{R: uint8(x), G: 40, B: 90, A: 255})
	}
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))

	out, ext, err := compressImage(buf.Bytes(), ".png")
	require.NoError(t, err, "a 1920x1080 screenshot must still upload")
	require.NotEmpty(t, out)
	require.Contains(t, []string{".jpg", ".png"}, ext)
}

// A truncated or non-image body must be refused cleanly rather than panicking.
func TestCompressImage_RefusesGarbage(t *testing.T) {
	_, _, err := compressImage([]byte("not an image at all"), ".png")
	require.Error(t, err)
}
