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
func MasterFrom(src image.Image) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, MasterSide, MasterSide))
	sb := src.Bounds()
	if sb.Dx() == MasterSide && sb.Dy() == MasterSide {
		draw.Draw(dst, dst.Bounds(), src, sb.Min, draw.Src)
	} else {
		draw.CatmullRom.Scale(dst, dst.Bounds(), src, sb, draw.Src, nil)
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
