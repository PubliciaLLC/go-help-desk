package server

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// What image/jpeg will allocate, read out of the file's own header.
//
// The byte budget was built from the colour model, which describes the
// picture the decoder produces and says nothing about what it needs on the
// way. Two shapes get nowhere near it. A progressive JPEG holds every DCT
// coefficient until the image is reconstructed — 256 bytes per 8x8 block per
// component, twelve bytes a pixel at 4:4:4 on top of the three the picture
// costs. A CMYK JPEG decodes into a YCbCr image, a separate black plane and
// then a third CMYK image, about eight bytes a pixel where the colour model
// says four.
//
// Measured against real files, both pass the pixel cap exactly at 5120x5120
// and then allocate four to six times the ceiling: a 308 KB progressive file
// leaves 375 MB live, and a 615 KB progressive CMYK one leaves 600 MB. That
// is the denial of service the byte budget exists to close, arriving through
// a file type this application accepts by default, from any signed-in
// reporter uploading to their own ticket.
//
// The expected numbers below were checked against the real decoder with
// runtime.MemStats and match to the megabyte.
func TestJPEGDecodeBytes_CountsWhatTheDecoderActuallyNeeds(t *testing.T) {
	const mb = 1 << 20

	cases := []struct {
		name        string
		w, h        int
		progressive bool
		comps       []sampling
		wantMB      int64
		wantRefused bool
	}{
		{
			name: "the attack: 5120x5120 progressive 4:4:4, 308 KB on the wire",
			w:    5120, h: 5120, progressive: true,
			comps:       []sampling{{1, 1}, {1, 1}, {1, 1}},
			wantMB:      375,
			wantRefused: true,
		},
		{
			name: "the same picture baseline, which is what the old rule budgeted for",
			w:    5120, h: 5120,
			comps:       []sampling{{1, 1}, {1, 1}, {1, 1}},
			wantMB:      75,
			wantRefused: false,
		},
		{
			name: "CMYK, where the colour model says four bytes and the decoder needs eight",
			w:    5120, h: 5120,
			comps:       []sampling{{1, 1}, {1, 1}, {1, 1}, {1, 1}},
			wantMB:      200,
			wantRefused: true,
		},
		{
			name: "progressive CMYK, the worst of both",
			w:    5120, h: 5120, progressive: true,
			comps:       []sampling{{1, 1}, {1, 1}, {1, 1}, {1, 1}},
			wantMB:      600,
			wantRefused: true,
		},
		{
			// The reason this reads the header instead of applying a blanket
			// surcharge. A surcharge big enough to be safe for 4:4:4 would
			// refuse this, and a 12-megapixel progressive photograph is an
			// ordinary thing to attach to a ticket.
			name: "an ordinary 12-megapixel progressive photograph, 4:2:0",
			w:    4032, h: 3024, progressive: true,
			comps:       []sampling{{2, 2}, {1, 1}, {1, 1}},
			wantMB:      87,
			wantRefused: false,
		},
		{
			name: "a 24-megapixel baseline photograph, 4:2:0",
			w:    6000, h: 4000,
			comps:       []sampling{{2, 2}, {1, 1}, {1, 1}},
			wantMB:      34,
			wantRefused: false,
		},
		{
			name: "a 5K screenshot, baseline 4:4:4",
			w:    5120, h: 2880,
			comps:       []sampling{{1, 1}, {1, 1}, {1, 1}},
			wantMB:      42,
			wantRefused: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hdr := jpegHeaderOnly(tc.w, tc.h, tc.progressive, tc.comps)

			got, ok := jpegDecodeBytes(hdr)
			if !ok {
				t.Fatalf("the fixture was not recognised as a JPEG frame header")
			}
			if got>>20 != tc.wantMB {
				t.Errorf("estimate is %d MB, want %d MB", got>>20, tc.wantMB)
			}

			// The budget comparison, which is what decodedSizeWithin does
			// with this number. Asserted here rather than by calling that
			// function, because DecodeConfig wants more of a JPEG than a
			// frame header — and a frame header is the whole point of the
			// fixture: it describes a 5120x5120 image in forty bytes, which
			// is the shape of the attack.
			refused := got > maxDecodedBytes
			if refused != tc.wantRefused {
				t.Errorf("needs %d MB against a limit of %d MB, refused=%v, want refused=%v",
					got>>20, int64(maxDecodedBytes)>>20, refused, tc.wantRefused)
			}
			_ = mb
		})
	}
}

// Anything that is not a JPEG frame header falls back to the colour model,
// rather than being waved through on a zero estimate.
func TestJPEGDecodeBytes_SaysSoWhenItCannotTell(t *testing.T) {
	cases := []struct {
		name string
		data []byte
	}{
		{"a PNG", pngHeaderOnly(100, 100, 8, 6)},
		{"empty", nil},
		{"a JPEG start and nothing else", []byte{0xFF, 0xD8, 0xFF, 0xE0}},
		{"a frame header that runs off the end", []byte{0xFF, 0xD8, 0xFF, 0xC0, 0x00, 0x11, 0x08}},
		{"a component count nothing produces", jpegHeaderOnly(10, 10, false, []sampling{
			{1, 1}, {1, 1}, {1, 1}, {1, 1}, {1, 1},
		})},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, ok := jpegDecodeBytes(tc.data); ok {
				t.Error("claimed to understand a header it should not have")
			}
		})
	}
}

// jpegHeaderOnly builds a JPEG signature and one frame header — the only part
// this estimate reads, and the only part a huge image needs in order to
// describe itself.
func jpegHeaderOnly(w, h int, progressive bool, comps []sampling) []byte {
	marker := byte(0xC0)
	if progressive {
		marker = 0xC2
	}

	var seg bytes.Buffer
	seg.WriteByte(8) // sample precision
	_ = binary.Write(&seg, binary.BigEndian, uint16(h))
	_ = binary.Write(&seg, binary.BigEndian, uint16(w))
	seg.WriteByte(byte(len(comps)))
	for i, c := range comps {
		seg.WriteByte(byte(i + 1))                   // component id
		seg.WriteByte(byte(c.h)<<4 | byte(c.v)&0x0f) // sampling factors
		seg.WriteByte(0)                             // quantisation table
	}

	var out bytes.Buffer
	out.Write([]byte{0xFF, 0xD8}) // SOI
	out.Write([]byte{0xFF, marker})
	_ = binary.Write(&out, binary.BigEndian, uint16(seg.Len()+2))
	out.Write(seg.Bytes())
	return out.Bytes()
}
