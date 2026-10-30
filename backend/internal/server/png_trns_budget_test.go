package server

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"hash/crc32"
	"image"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

// A grayscale PNG with a tRNS chunk decodes four times wider than its colour
// model says, and the budget believed the colour model.
//
// image/png's DecodeConfig returns at IDAT, and for a non-paletted image the
// model it reports comes from IHDR's colour type alone — there is no branch
// in it for transparency. parsetRNS sets useTransparent, and readImagePass
// reads that flag and builds an NRGBA where it would otherwise build a Gray.
//
// Measured before the fix, 5120x5120:
//
//	depth  8  model=Gray    ESTIMATE= 25 MB  ACTUAL=100 MB  under by 4.0x
//	depth 16  model=Gray16  ESTIMATE= 50 MB  ACTUAL=200 MB  under by 4.0x
//
// The 16-bit case is twice the entire per-request budget on a single upload,
// and both compress to almost nothing because every pixel can be the same
// value. Found by the session-B review of #292 — the one shape eight rounds
// of budget work here left open.
func TestPNGBudget_GrayscaleWithTransparencyIsCostedProperly(t *testing.T) {
	const w, h = 512, 512
	px := int64(w) * int64(h)

	cases := []struct {
		name      string
		depth     byte
		trns      bool
		wantBytes int64
	}{
		{"8-bit grayscale, no tRNS", 8, false, px * 1},
		{"8-bit grayscale with tRNS", 8, true, px * 4},
		{"16-bit grayscale, no tRNS", 16, false, px * 2},
		{"16-bit grayscale with tRNS", 16, true, px * 8},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := grayPNG(t, w, h, tc.depth, tc.trns)

			// What the decoder really allocates, measured rather than argued.
			var before, after runtime.MemStats
			runtime.GC()
			runtime.ReadMemStats(&before)
			img, _, err := image.Decode(bytes.NewReader(data))
			require.NoError(t, err)
			runtime.ReadMemStats(&after)
			actual := int64(after.TotalAlloc - before.TotalAlloc)
			runtime.KeepAlive(img)

			// The budget must accept it at its true cost and refuse it one
			// byte below. That pins the estimate to the real number from
			// both sides, so an estimate that is merely "big enough" fails
			// this as surely as one that is too small.
			require.NoError(t, decodedSizeWithin(data, px+1, tc.wantBytes),
				"refused at its own true cost")
			require.Error(t, decodedSizeWithin(data, px+1, tc.wantBytes-1),
				"accepted on a budget smaller than it needs: estimate is under the real cost")

			t.Logf("%-28s estimate=%d MB  measured=%d MB  (%T)",
				tc.name, tc.wantBytes>>20, actual>>20, img)
			require.LessOrEqual(t, actual, tc.wantBytes*2,
				"measured allocation is far above the estimate, so the estimate is wrong")
		})
	}
}

// A PNG whose chunk stream cannot be walked is budgeted as though it were
// transparent, not as though it were not.
//
// The first version of this fix refused such a file outright, on the JPEG
// rule that something unmeasurable should not be allowed. That was wrong
// here, and the existing budget tests caught it: they synthesise PNGs that
// carry an IHDR and no pixel data at all, which DecodeConfig reads happily
// while the walk runs off the end looking for an IDAT that is not there.
// Four ordinary images were refused with "this PNG's chunks could not be
// read".
//
// Guessing "transparent" costs nothing when it is wrong — for a truecolor
// image the budget is 4 or 8 bytes either way — and for a grayscale one it
// errs towards the larger number, which is the direction that is safe.
func TestPNGBudget_AnUnwalkableChunkStreamIsBudgetedAsTransparent(t *testing.T) {
	const w, h = 256, 256
	px := int64(w) * int64(h)

	full := grayPNG(t, w, h, 8, false)
	// Everything up to and including IHDR, and nothing after it: no tRNS to
	// find and no IDAT to stop at.
	headerOnly := full[:8+8+13+4]

	_, _, err := image.DecodeConfig(bytes.NewReader(headerOnly))
	require.NoError(t, err, "this test needs a header DecodeConfig still accepts")

	require.NoError(t, decodedSizeWithin(headerOnly, px+1, px*4),
		"budgeted above the transparent cost, or refused outright")
	require.Error(t, decodedSizeWithin(headerOnly, px+1, px*4-1),
		"budgeted below the transparent cost, so an unreadable stream is trusted")
}

// grayPNG builds a grayscale PNG by hand, with or without a tRNS chunk. The
// stdlib encoder will not emit tRNS, so there is no other way to make one.
func grayPNG(t *testing.T, w, h int, bitDepth byte, trns bool) []byte {
	t.Helper()
	var buf bytes.Buffer
	buf.Write([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'})

	chunk := func(typ string, payload []byte) {
		var l [4]byte
		binary.BigEndian.PutUint32(l[:], uint32(len(payload)))
		buf.Write(l[:])
		body := append([]byte(typ), payload...)
		buf.Write(body)
		var c [4]byte
		binary.BigEndian.PutUint32(c[:], crc32.ChecksumIEEE(body))
		buf.Write(c[:])
	}

	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:], uint32(w))
	binary.BigEndian.PutUint32(ihdr[4:], uint32(h))
	ihdr[8] = bitDepth
	ihdr[9] = 0 // colour type 0: grayscale
	chunk("IHDR", ihdr)

	if trns {
		// For grayscale, tRNS is one 2-byte grey level meaning transparent.
		chunk("tRNS", []byte{0x00, 0x01})
	}

	bpp := 1
	if bitDepth == 16 {
		bpp = 2
	}
	raw := make([]byte, 0, h*(1+w*bpp))
	for y := 0; y < h; y++ {
		raw = append(raw, 0) // filter: none
		raw = append(raw, bytes.Repeat([]byte{0x7f}, w*bpp)...)
	}
	var z bytes.Buffer
	zw := zlib.NewWriter(&z)
	_, err := zw.Write(raw)
	require.NoError(t, err)
	require.NoError(t, zw.Close())

	chunk("IDAT", z.Bytes())
	chunk("IEND", nil)
	return buf.Bytes()
}
