package siteicon

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image/png"
)

// ICOEntry is one image of an .ico: a PNG and the side it is.
type ICOEntry struct {
	Side int
	PNG  []byte
}

// EncodeICO writes the entries as an .ico with PNG-compressed images, which
// every browser reads (and Windows since Vista). Written by hand, because the
// format is 22 bytes of header per image and a dependency would be larger than
// that.
//
// Entries are written in the order given. The directory states a side as one
// byte, where 0 means 256, so a side outside 1..255 is refused, and so is a
// payload that is not a PNG of the side it claims: a mislabelled entry is a
// favicon the browser draws at the wrong size.
func EncodeICO(entries []ICOEntry) ([]byte, error) {
	if len(entries) == 0 {
		return nil, errors.New("an icon file needs at least one image")
	}
	for i, e := range entries {
		if e.Side < 1 || e.Side > 255 {
			return nil, fmt.Errorf("image %d: side %d is outside 1 to 255", i, e.Side)
		}
		cfg, err := png.DecodeConfig(bytes.NewReader(e.PNG))
		if err != nil {
			return nil, fmt.Errorf("image %d is not a PNG: %w", i, err)
		}
		if cfg.Width != e.Side || cfg.Height != e.Side {
			return nil, fmt.Errorf("image %d is %d x %d, not the %d it says", i, cfg.Width, cfg.Height, e.Side)
		}
	}

	var b bytes.Buffer
	le := binary.LittleEndian
	// ICONDIR: reserved, type 1 (icon), count.
	_ = binary.Write(&b, le, [3]uint16{0, 1, uint16(len(entries))})
	offset := uint32(6 + 16*len(entries))
	for _, e := range entries {
		b.Write([]byte{uint8(e.Side), uint8(e.Side), 0, 0}) // width, height, colour count, reserved
		_ = binary.Write(&b, le, [2]uint16{1, 32})          // planes, bits per pixel
		_ = binary.Write(&b, le, [2]uint32{uint32(len(e.PNG)), offset})
		offset += uint32(len(e.PNG))
	}
	for _, e := range entries {
		b.Write(e.PNG)
	}
	return b.Bytes(), nil
}
