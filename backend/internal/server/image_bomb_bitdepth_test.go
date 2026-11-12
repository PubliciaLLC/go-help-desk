package server

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"testing"
)

// The decode budget is in bytes, because pixels were the wrong unit.
//
// The original cap counted pixels and assumed four bytes each. A 16-bit RGBA
// PNG decodes to eight, so 5120x5120 — 26.2 megapixels, comfortably under the
// pixel cap — is 210 MB of pixel buffer, and both encoders then run on it.
// Measured: 214 KB on the wire, 403 MB live on the heap afterwards, and five
// concurrent uploads reached 1,990 MB. A reporting-role user uploading to
// their own ticket could send it, so a 214 KB request was a way to have the
// help desk killed for running out of memory.
//
// Built as a bare PNG signature plus an IHDR chunk, which is all
// image.DecodeConfig reads. A real 5120x5120 16-bit image would be 210 MB to
// construct, which is the thing this test is about not doing.
func TestDecodedSizeWithin_CountsBytesAndNotJustPixels(t *testing.T) {
	cases := []struct {
		name        string
		w, h        int
		bitDepth    uint8
		colourType  uint8
		wantRefused bool
	}{
		{
			name: "the attack: 16-bit RGBA, under the pixel cap and over the byte budget",
			w:    5120, h: 5120, bitDepth: 16, colourType: 6,
			wantRefused: true,
		},
		{
			// The contrast that shows the byte rule is doing the work. Same
			// dimensions, same pixel count, four bytes a pixel instead of
			// eight: exactly 100 MB, which is the budget, so it is accepted.
			// The only difference between this and the row above is the bit
			// depth, and under the old pixel-counting rule both passed.
			name: "8-bit RGBA at the same size fits, exactly",
			w:    5120, h: 5120, bitDepth: 8, colourType: 6,
			wantRefused: false,
		},
		{
			name: "a 24-megapixel camera photograph, 8-bit RGB",
			w:    6000, h: 4000, bitDepth: 8, colourType: 2,
			wantRefused: false,
		},
		{
			name: "a 5K screenshot, 8-bit RGBA",
			w:    5120, h: 2880, bitDepth: 8, colourType: 6,
			wantRefused: false,
		},
		{
			// Half the pixels of the attack image and the same bit depth:
			// 12.5 megapixels at 8 bytes is exactly 100 MB, so this is the
			// largest 16-bit image that fits.
			name: "16-bit RGBA that does fit",
			w:    3620, h: 3620, bitDepth: 16, colourType: 6,
			wantRefused: false,
		},
		{
			name: "past the pixel cap even though the bytes are cheap (8-bit greyscale)",
			w:    40000, h: 40000, bitDepth: 8, colourType: 0,
			wantRefused: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hdr := pngHeaderOnly(tc.w, tc.h, tc.bitDepth, tc.colourType)

			// The fixture has to be one the decoder actually understands, or
			// the test would pass on a header error rather than on the size.
			cfg, _, err := image.DecodeConfig(bytes.NewReader(hdr))
			if err != nil {
				t.Fatalf("the fixture is not a readable PNG header: %v", err)
			}
			if cfg.Width != tc.w || cfg.Height != tc.h {
				t.Fatalf("fixture reports %dx%d, wanted %dx%d", cfg.Width, cfg.Height, tc.w, tc.h)
			}

			err = decodedSizeWithin(hdr, maxImagePixels, maxDecodedBytes)
			if tc.wantRefused && err == nil {
				t.Errorf("%dx%d at %d bits, colour type %d was accepted; it needs %d MB to decode",
					tc.w, tc.h, tc.bitDepth, tc.colourType,
					(int64(tc.w)*int64(tc.h)*bytesPerPixel(cfg.ColorModel))>>20)
			}
			if !tc.wantRefused && err != nil {
				t.Errorf("an ordinary image was refused: %v", err)
			}
		})
	}
}

// Unknown colour models cost the worst case, not a guess. An underestimate
// here is the whole bug this budget exists to fix.
func TestBytesPerPixel_IsPessimisticAboutWhatItDoesNotKnow(t *testing.T) {
	cases := []struct {
		name  string
		model color.Model
		want  int64
	}{
		{"8-bit greyscale", color.GrayModel, 1},
		{"16-bit greyscale", color.Gray16Model, 2},
		{"JPEG's three planes", color.YCbCrModel, 3},
		{"8-bit colour with alpha", color.NRGBAModel, 4},
		{"16-bit colour with alpha", color.NRGBA64Model, 8},
		{"16-bit colour, premultiplied", color.RGBA64Model, 8},
		{"a palette", color.Palette{color.Black, color.White}, 1},
		{"something this build has never heard of", color.ModelFunc(func(c color.Color) color.Color { return c }), 8},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := bytesPerPixel(tc.model); got != tc.want {
				t.Errorf("bytesPerPixel = %d, want %d", got, tc.want)
			}
		})
	}
}

// pngHeaderOnly builds a PNG signature and one IHDR chunk. Nothing else:
// image.DecodeConfig reads no further, and the point is to describe a huge
// image without allocating one.
func pngHeaderOnly(w, h int, bitDepth, colourType uint8) []byte {
	var ihdr bytes.Buffer
	_ = binary.Write(&ihdr, binary.BigEndian, uint32(w))
	_ = binary.Write(&ihdr, binary.BigEndian, uint32(h))
	ihdr.WriteByte(bitDepth)
	ihdr.WriteByte(colourType)
	ihdr.WriteByte(0) // compression
	ihdr.WriteByte(0) // filter
	ihdr.WriteByte(0) // interlace

	body := append([]byte("IHDR"), ihdr.Bytes()...)

	var out bytes.Buffer
	out.Write([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1A, '\n'})
	_ = binary.Write(&out, binary.BigEndian, uint32(len(body)-4))
	out.Write(body)
	_ = binary.Write(&out, binary.BigEndian, crc32.ChecksumIEEE(body))
	return out.Bytes()
}
