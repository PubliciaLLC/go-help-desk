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
		opt         fixtureOpts
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
			// Three components, but the decoder converts the whole thing
			// into a second full-resolution image because the file says RGB
			// rather than YCbCr. Missed the first time: the surcharge was
			// applied only to four-component files. Measured on a real one,
			// 6 MB on the wire and 175 MB live.
			name: "RGB by component identifier, 4:4:4",
			w:    5120, h: 5120,
			comps:       []sampling{{1, 1}, {1, 1}, {1, 1}},
			opt:         fixtureOpts{ids: []byte{'R', 'G', 'B'}},
			wantMB:      175,
			wantRefused: true,
		},
		{
			name: "RGB by Adobe transform 0, 4:2:0",
			w:    6000, h: 4000,
			comps:       []sampling{{2, 2}, {1, 1}, {1, 1}},
			opt:         fixtureOpts{adobe: true, adobeTransform: 0},
			wantMB:      125,
			wantRefused: true,
		},
		{
			// JFIF settles it: that header means YCbCr whatever the
			// component identifiers say, and the decoder does not convert.
			// Getting this wrong the other way would refuse ordinary photos.
			name: "component identifiers that say RGB, on a JFIF file",
			w:    5120, h: 5120,
			comps:       []sampling{{1, 1}, {1, 1}, {1, 1}},
			opt:         fixtureOpts{ids: []byte{'R', 'G', 'B'}, jfif: true},
			wantMB:      75,
			wantRefused: false,
		},
		{
			name: "an Adobe file that says YCbCr",
			w:    5120, h: 5120,
			comps:       []sampling{{1, 1}, {1, 1}, {1, 1}},
			opt:         fixtureOpts{adobe: true, adobeTransform: 1},
			wantMB:      75,
			wantRefused: false,
		},
		{
			// The marker moved to AFTER the scan data. Go's decoder keeps
			// reading markers until end-of-image and decides whether to
			// convert at the very end, so this still makes it an RGB file —
			// and a parser that stopped at the scan never saw it. Measured:
			// estimated 37 MB, decoded to 137 MB.
			name: "RGB declared by an Adobe marker placed after the scan",
			w:    5120, h: 5120,
			comps: []sampling{{2, 2}, {1, 1}, {1, 1}},
			opt: fixtureOpts{
				adobe: true, adobeTransform: 0, adobeAfterScan: true,
			},
			wantMB:      137,
			wantRefused: true,
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
			hdr := jpegHeaderOnly(tc.w, tc.h, tc.progressive, tc.comps, tc.opt)

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
		}, fixtureOpts{})},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, ok := jpegDecodeBytes(tc.data); ok {
				t.Error("claimed to understand a header it should not have")
			}
		})
	}
}

// jpegHeaderOnly builds a JPEG signature, optionally the two markers that
// decide whether the file is RGB, and one frame header — the only parts this
// estimate reads, and the only parts a huge image needs to describe itself.
func jpegHeaderOnly(w, h int, progressive bool, comps []sampling, opt fixtureOpts) []byte {
	marker := byte(0xC0)
	if progressive {
		marker = 0xC2
	}

	ids := opt.ids
	if len(ids) != len(comps) {
		ids = make([]byte, len(comps))
		for i := range ids {
			ids[i] = byte(i + 1)
		}
	}

	var seg bytes.Buffer
	seg.WriteByte(8) // sample precision
	_ = binary.Write(&seg, binary.BigEndian, uint16(h))
	_ = binary.Write(&seg, binary.BigEndian, uint16(w))
	seg.WriteByte(byte(len(comps)))
	for i, c := range comps {
		seg.WriteByte(ids[i])
		seg.WriteByte(byte(c.h)<<4 | byte(c.v)&0x0f)
		seg.WriteByte(0) // quantisation table
	}

	var out bytes.Buffer
	out.Write([]byte{0xFF, 0xD8}) // SOI

	if opt.jfif {
		payload := append([]byte("JFIF\x00"), make([]byte, 9)...)
		out.Write([]byte{0xFF, 0xE0})
		_ = binary.Write(&out, binary.BigEndian, uint16(len(payload)+2))
		out.Write(payload)
	}
	adobe := func() {
		payload := make([]byte, 12)
		copy(payload, "Adobe")
		payload[11] = opt.adobeTransform
		out.Write([]byte{0xFF, 0xEE})
		_ = binary.Write(&out, binary.BigEndian, uint16(len(payload)+2))
		out.Write(payload)
	}
	if opt.adobe && !opt.adobeAfterScan {
		adobe()
	}

	out.Write([]byte{0xFF, marker})
	_ = binary.Write(&out, binary.BigEndian, uint16(seg.Len()+2))
	out.Write(seg.Bytes())

	if opt.adobeAfterScan {
		// A scan header, a little compressed data with a stuffed 0xFF and a
		// restart marker in it, then the Adobe marker and end-of-image. The
		// entropy bytes are there so the skip has something to skip.
		sos := []byte{0x01, 0x01, 0x00, 0x00, 0x3F, 0x00}
		out.Write([]byte{0xFF, 0xDA})
		_ = binary.Write(&out, binary.BigEndian, uint16(len(sos)+2))
		out.Write(sos)
		out.Write([]byte{0x12, 0xFF, 0x00, 0x34, 0xFF, 0xD0, 0x56})
		if opt.adobe {
			adobe()
		}
		out.Write([]byte{0xFF, 0xD9})
	}
	return out.Bytes()
}

// fixtureOpts is everything about a JPEG besides its frame that changes what
// the decoder allocates.
type fixtureOpts struct {
	ids            []byte
	jfif           bool
	adobe          bool
	adobeTransform byte
	// adobeAfterScan puts the Adobe marker past the compressed data instead
	// of before the frame header, which is where it has to be caught.
	adobeAfterScan bool
}
