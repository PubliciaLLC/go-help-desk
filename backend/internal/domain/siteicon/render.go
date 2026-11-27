package siteicon

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"

	"golang.org/x/image/draw"
)

// MasterFrom makes the 512 x 512 master from a decoded upload. The master is
// *image.RGBA, which is premultiplied; scaling in premultiplied alpha is what
// keeps a transparent edge from picking up a dark fringe from the invisible
// pixels beside it.
//
// A source of exactly 512 x 512 is copied, not resampled. EXIF orientation and
// colour profiles are not read: Go's decoders ignore both, the logo does the
// same, and the previews show the administrator the result at once.
//
// A source over bigScale pixels on a side is first averaged down by a whole
// factor (boxShrink) to at most 1024. draw.CatmullRom keeps a work buffer of
// destination width x source height x 32 bytes, which for a 4096 source is
// 64 MiB on top of the 64 MiB source; the upload budget (maxDecodedBytes in
// the server) counts the source and not that buffer. After the first step the
// buffer is 16 MiB at most, and the box average is a better prefilter for a
// fine pattern than skipping pixels would be.
func MasterFrom(src image.Image) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, MasterSide, MasterSide))
	sb := src.Bounds()
	switch {
	case sb.Dx() == MasterSide && sb.Dy() == MasterSide:
		draw.Draw(dst, dst.Bounds(), src, sb.Min, draw.Src)
	default:
		if side := max(sb.Dx(), sb.Dy()); side > bigScale {
			src = boxShrink(src, (side+bigStep-1)/bigStep)
			sb = src.Bounds()
		}
		draw.CatmullRom.Scale(dst, dst.Bounds(), src, sb, draw.Src, nil)
	}
	return dst
}

const (
	// bigScale is the side above which MasterFrom shrinks in two steps. At or
	// under it, the scaler's buffer is 32 MiB at most.
	bigScale = 2048
	// bigStep is the side the first step aims at (never over it).
	bigStep = 1024
)

// boxShrink makes the image f times smaller on each side (f >= 2) by averaging
// the pixels each new one covers. The average is taken in premultiplied alpha,
// so a transparent edge keeps its colour and picks up no fringe. The boxes are
// f or f+1 pixels wide when the side is not a multiple of f; no pixel is left
// out.
func boxShrink(src image.Image, f int) *image.RGBA {
	sb := src.Bounds()
	w, h := max(sb.Dx()/f, 1), max(sb.Dy()/f, 1)
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	// One band of source rows at a time, converted to premultiplied RGBA by
	// draw.Draw (which has a fast path for every decoder's output; asking the
	// image for each pixel boxes a colour per pixel, 64 MiB of garbage for a
	// 4096 square).
	band := image.NewRGBA(image.Rect(0, 0, sb.Dx(), sb.Dy()/h+1))
	for y := range h {
		y0, y1 := y*sb.Dy()/h, (y+1)*sb.Dy()/h
		rows := image.Rect(0, 0, sb.Dx(), y1-y0)
		draw.Draw(band, rows, src, image.Pt(sb.Min.X, sb.Min.Y+y0), draw.Src)
		for x := range w {
			x0, x1 := x*sb.Dx()/w, (x+1)*sb.Dx()/w
			var sum [4]uint64
			for r := range y1 - y0 {
				px := band.Pix[r*band.Stride+4*x0 : r*band.Stride+4*x1]
				for i := 0; i < len(px); i += 4 {
					sum[0], sum[1], sum[2], sum[3] = sum[0]+uint64(px[i]), sum[1]+uint64(px[i+1]), sum[2]+uint64(px[i+2]), sum[3]+uint64(px[i+3])
				}
			}
			n := uint64((y1 - y0) * (x1 - x0))
			// Rounded, and never above 255.
			to8 := func(v uint64) uint8 { return uint8((v + n/2) / n) }
			dst.SetRGBA(x, y, color.RGBA{R: to8(sum[0]), G: to8(sum[1]), B: to8(sum[2]), A: to8(sum[3])})
		}
	}
	return dst
}

// Render makes the master PNG and the seven served files from a master and a
// background colour ("#rrggbb", any case). It is pure: the same master and
// colour give the same bytes.
func Render(master *image.RGBA, background string) (Files, error) {
	bg, err := parseColour(background, master)
	if err != nil {
		return nil, err
	}
	out := Files{}
	if out[Master], err = encodePNG(master); err != nil {
		return nil, err
	}
	var ico []ICOEntry
	for _, n := range []int{16, 32, 48} {
		// Transparent: scaled straight, alpha kept.
		dst := image.NewRGBA(image.Rect(0, 0, n, n))
		draw.CatmullRom.Scale(dst, dst.Bounds(), master, master.Bounds(), draw.Src, nil)
		p, err := encodePNG(dst)
		if err != nil {
			return nil, err
		}
		ico = append(ico, ICOEntry{Side: n, PNG: p})
		switch n {
		case 16:
			out["favicon-16.png"] = p
		case 32:
			out["favicon-32.png"] = p
		}
	}
	if out["favicon.ico"], err = EncodeICO(ico); err != nil {
		return nil, err
	}
	if err := renderFilled(out, master, bg); err != nil {
		return nil, err
	}
	return out, nil
}

// Recolour is Render for a new background, from files already stored: the
// master and the three transparent files are carried over byte for byte, and
// only the four that are filled with the background are made again.
//
// It is not Render of the decoded master. The master is stored as a PNG, which
// is straight alpha, and reading it back into the premultiplied form scaling
// wants rounds every semi-transparent pixel once more; made again, the
// transparent files would drift by a level on each colour change. Carried over,
// they cannot.
func Recolour(stored Files, background string) (Files, error) {
	img, err := png.Decode(bytes.NewReader(stored[Master]))
	if err != nil {
		return nil, fmt.Errorf("reading the stored master: %w", err)
	}
	b := img.Bounds()
	if b.Dx() != MasterSide || b.Dy() != MasterSide {
		return nil, fmt.Errorf("the stored master is %d x %d, not %d", b.Dx(), b.Dy(), MasterSide)
	}
	master := MasterFrom(img)
	bg, err := parseColour(background, master)
	if err != nil {
		return nil, err
	}
	out := Files{Master: stored[Master]}
	for _, n := range []string{"favicon.ico", "favicon-16.png", "favicon-32.png"} {
		if len(stored[n]) == 0 {
			return nil, fmt.Errorf("the stored icon has no %s", n)
		}
		out[n] = stored[n]
	}
	if err := renderFilled(out, master, bg); err != nil {
		return nil, err
	}
	return out, nil
}

// renderFilled adds the four files that are filled with the background: the
// background first, the picture over it, so every pixel is opaque.
func renderFilled(out Files, master *image.RGBA, bg color.RGBA) error {
	fill := func(name string, n int, into image.Rectangle) error {
		dst := image.NewRGBA(image.Rect(0, 0, n, n))
		draw.Draw(dst, dst.Bounds(), image.NewUniform(bg), image.Point{}, draw.Src)
		draw.CatmullRom.Scale(dst, into, master, master.Bounds(), draw.Over, nil)
		p, err := encodePNG(dst)
		if err != nil {
			return err
		}
		out[name] = p
		return nil
	}
	for _, f := range []struct {
		name string
		n    int
	}{{"apple-touch-icon.png", 180}, {"icon-192.png", 192}, {"icon-512.png", 512}} {
		if err := fill(f.name, f.n, image.Rect(0, 0, f.n, f.n)); err != nil {
			return err
		}
	}
	inner := image.Rect(MaskableOffset, MaskableOffset, MaskableOffset+MaskableContent, MaskableOffset+MaskableContent)
	return fill("icon-maskable-512.png", MasterSide, inner)
}

// parseColour checks the master is the right size and reads the background.
func parseColour(background string, master *image.RGBA) (color.RGBA, error) {
	hex, err := ParseBackground(background)
	if err != nil {
		return color.RGBA{}, err
	}
	if master == nil || master.Bounds() != image.Rect(0, 0, MasterSide, MasterSide) {
		return color.RGBA{}, errors.New("the master must be 512 x 512")
	}
	bg := color.RGBA{A: 255}
	if _, err := fmt.Sscanf(hex, "#%02x%02x%02x", &bg.R, &bg.G, &bg.B); err != nil {
		return color.RGBA{}, fmt.Errorf("reading background colour: %w", err)
	}
	return bg, nil
}

func encodePNG(img image.Image) ([]byte, error) {
	var b bytes.Buffer
	e := png.Encoder{CompressionLevel: png.BestCompression}
	if err := e.Encode(&b, img); err != nil {
		return nil, fmt.Errorf("encoding png: %w", err)
	}
	return b.Bytes(), nil
}
