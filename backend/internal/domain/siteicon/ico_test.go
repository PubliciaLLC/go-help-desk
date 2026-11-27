package siteicon

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/png"
	"testing"

	"github.com/stretchr/testify/require"
)

func solidPNG(t *testing.T, side int) []byte {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, side, side))))
	return buf.Bytes()
}

type icoEntry struct {
	w, h, colors, reserved uint8
	planes, bits           uint16
	size, offset           uint32
}

// parseICO reads the container back the way a decoder would, not the way the
// writer wrote it.
func parseICO(t *testing.T, b []byte) (entries []icoEntry, payloads [][]byte) {
	t.Helper()
	require.GreaterOrEqual(t, len(b), 6)
	require.Equal(t, []byte{0, 0, 1, 0}, b[:4], "reserved 0, type 1")
	n := int(binary.LittleEndian.Uint16(b[4:6]))
	require.GreaterOrEqual(t, len(b), 6+16*n)
	for i := range n {
		e := b[6+16*i : 6+16*(i+1)]
		ent := icoEntry{
			w: e[0], h: e[1], colors: e[2], reserved: e[3],
			planes: binary.LittleEndian.Uint16(e[4:6]),
			bits:   binary.LittleEndian.Uint16(e[6:8]),
			size:   binary.LittleEndian.Uint32(e[8:12]),
			offset: binary.LittleEndian.Uint32(e[12:16]),
		}
		entries = append(entries, ent)
		require.LessOrEqual(t, int(ent.offset+ent.size), len(b))
		payloads = append(payloads, b[ent.offset:ent.offset+ent.size])
	}
	return entries, payloads
}

func TestEncodeICO_Format(t *testing.T) {
	p16, p32, p48 := solidPNG(t, 16), solidPNG(t, 32), solidPNG(t, 48)
	out, err := EncodeICO([]ICOEntry{{16, p16}, {32, p32}, {48, p48}})
	require.NoError(t, err)

	require.Equal(t, []byte{0, 0, 1, 0, 3, 0}, out[:6], "ICONDIR: reserved, icon, three images")

	entries, payloads := parseICO(t, out)
	require.Len(t, entries, 3)
	wantSide := []uint8{16, 32, 48}
	wantPNG := [][]byte{p16, p32, p48}
	offset := uint32(6 + 16*3)
	total := offset
	for i, e := range entries {
		require.Equal(t, wantSide[i], e.w)
		require.Equal(t, wantSide[i], e.h)
		require.Zero(t, e.colors)
		require.Zero(t, e.reserved)
		require.Equal(t, uint16(1), e.planes)
		require.Equal(t, uint16(32), e.bits)
		require.Equal(t, uint32(len(wantPNG[i])), e.size)
		require.Equal(t, offset, e.offset, "entry %d offset", i)
		require.Equal(t, wantPNG[i], payloads[i])
		cfg, err := png.DecodeConfig(bytes.NewReader(payloads[i]))
		require.NoError(t, err)
		require.Equal(t, int(wantSide[i]), cfg.Width)
		offset += e.size
		total += e.size
	}
	require.Equal(t, int(total), len(out), "the payload lengths account for every byte of the file")
	require.Equal(t, uint32(54), entries[0].offset)
}

func TestEncodeICO_Refusals(t *testing.T) {
	good := solidPNG(t, 16)
	cases := []struct {
		name    string
		entries []ICOEntry
	}{
		{"no entries", nil},
		{"side 256 would read as 0 (256 px) in the directory", []ICOEntry{{256, solidPNG(t, 256)}}},
		{"side 0", []ICOEntry{{0, good}}},
		{"negative side", []ICOEntry{{-1, good}}},
		{"not a PNG", []ICOEntry{{16, []byte("GIF89a not a png")}}},
		{"empty payload", []ICOEntry{{16, nil}}},
		{"PNG of another size than stated", []ICOEntry{{32, good}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := EncodeICO(tc.entries)
			require.Error(t, err)
		})
	}
	// 255 is the largest the directory can state.
	_, err := EncodeICO([]ICOEntry{{255, solidPNG(t, 255)}})
	require.NoError(t, err)
}
