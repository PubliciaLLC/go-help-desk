package siteicon

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"

	"github.com/stretchr/testify/require"
)

// disc is an opaque blue disc on a fully transparent square, the usual shape of
// a logo mark.
func disc(side int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, side, side))
	c, r := float64(side)/2, float64(side)*0.4
	for y := range side {
		for x := range side {
			dx, dy := float64(x)+0.5-c, float64(y)+0.5-c
			if dx*dx+dy*dy <= r*r {
				img.SetNRGBA(x, y, color.NRGBA{R: 20, G: 60, B: 200, A: 255})
			}
		}
	}
	return img
}

func solid(side int, c color.NRGBA) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, side, side))
	for y := range side {
		for x := range side {
			img.SetNRGBA(x, y, c)
		}
	}
	return img
}

func decodePNG(t *testing.T, b []byte) image.Image {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(b))
	require.NoError(t, err)
	return img
}

func rgba(img image.Image, x, y int) color.RGBA {
	return color.RGBAModel.Convert(img.At(x, y)).(color.RGBA)
}

func renderOf(t *testing.T, src image.Image, bg string) Files {
	t.Helper()
	files, err := Render(MasterFrom(src), bg)
	require.NoError(t, err)
	return files
}

func TestRender_Dimensions(t *testing.T) {
	files := renderOf(t, disc(1024), "#ffffff")

	require.Len(t, files, 8, "master.png and the seven served files, nothing else")
	for _, n := range append([]string{Master}, Served...) {
		require.NotEmpty(t, files[n], n)
	}

	want := map[string]int{
		Master: 512, "favicon-16.png": 16, "favicon-32.png": 32, "apple-touch-icon.png": 180,
		"icon-192.png": 192, "icon-512.png": 512, "icon-maskable-512.png": 512,
	}
	for name, side := range want {
		cfg, err := png.DecodeConfig(bytes.NewReader(files[name]))
		require.NoError(t, err, name)
		require.Equal(t, side, cfg.Width, name)
		require.Equal(t, side, cfg.Height, name)
	}

	entries, payloads := parseICO(t, files["favicon.ico"])
	require.Len(t, entries, 3)
	for i, side := range []int{16, 32, 48} {
		require.Equal(t, uint8(side), entries[i].w)
		cfg, err := png.DecodeConfig(bytes.NewReader(payloads[i]))
		require.NoError(t, err)
		require.Equal(t, side, cfg.Width)
		require.Equal(t, side, cfg.Height)
	}
	require.Equal(t, files["favicon-16.png"], payloads[0], "the ICO's 16 is the served favicon-16.png")
	require.Equal(t, files["favicon-32.png"], payloads[1], "the ICO's 32 is the served favicon-32.png")
}

func TestRender_FaviconsKeepTransparency(t *testing.T) {
	files := renderOf(t, disc(1024), "#ff00ff")

	check := func(name string, b []byte) {
		img := decodePNG(t, b)
		require.Zero(t, rgba(img, 0, 0).A, "%s: the corner is still transparent", name)
		w := img.Bounds().Dx()
		require.Equal(t, uint8(255), rgba(img, w/2, w/2).A, "%s: the centre of the disc is opaque", name)
	}
	check("favicon-16.png", files["favicon-16.png"])
	check("favicon-32.png", files["favicon-32.png"])
	_, payloads := parseICO(t, files["favicon.ico"])
	for i, p := range payloads {
		check([]string{"ico 16", "ico 32", "ico 48"}[i], p)
	}
	check(Master, files[Master])
}

func TestRender_HomeScreenIconsAreOpaque(t *testing.T) {
	const bg = "#1a2b3c"
	files := renderOf(t, disc(1024), bg)
	want := color.RGBA{R: 0x1a, G: 0x2b, B: 0x3c, A: 255}

	for _, name := range []string{"apple-touch-icon.png", "icon-192.png", "icon-512.png", "icon-maskable-512.png"} {
		img := decodePNG(t, files[name])
		b := img.Bounds()
		for y := b.Min.Y; y < b.Max.Y; y++ {
			for x := b.Min.X; x < b.Max.X; x++ {
				if rgba(img, x, y).A != 255 {
					t.Fatalf("%s: pixel (%d,%d) is not opaque: %v", name, x, y, rgba(img, x, y))
				}
			}
		}
		require.Equal(t, want, rgba(img, 0, 0), "%s: a transparent source corner becomes the background exactly", name)
		require.Equal(t, want, rgba(img, b.Max.X-1, b.Max.Y-1), name)
	}
}

func TestRender_MaskableSafeZone(t *testing.T) {
	const bg = "#ff8800"
	green := color.NRGBA{R: 0, G: 160, B: 0, A: 255}
	files := renderOf(t, solid(1024, green), bg)
	img := decodePNG(t, files["icon-maskable-512.png"])
	want := color.RGBA{R: 0xff, G: 0x88, B: 0x00, A: 255}

	inner := image.Rect(MaskableOffset, MaskableOffset, MaskableOffset+MaskableContent, MaskableOffset+MaskableContent)
	require.Equal(t, image.Rect(51, 51, 461, 461), inner)
	for y := range 512 {
		for x := range 512 {
			if !image.Pt(x, y).In(inner) {
				if rgba(img, x, y) != want {
					t.Fatalf("pixel (%d,%d) outside the safe zone is %v, want the background", x, y, rgba(img, x, y))
				}
			}
		}
	}
	require.Equal(t, color.RGBA{R: 0, G: 160, B: 0, A: 255}, rgba(img, 256, 256), "the content fills the safe zone")
	require.Equal(t, color.RGBA{R: 0, G: 160, B: 0, A: 255}, rgba(img, 52, 52), "and reaches its edge")

	// The plain 512 icon is not padded: content to the edge.
	plain := decodePNG(t, files["icon-512.png"])
	require.Equal(t, color.RGBA{R: 0, G: 160, B: 0, A: 255}, rgba(plain, 2, 2))
}

// Scaling a transparent edge in straight (non-premultiplied) alpha pulls the
// invisible black of the transparent pixels into the colour: a dark halo. The
// master is premultiplied, which is what prevents it.
func TestRender_NoFringe(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 1024, 1024))
	red := color.NRGBA{R: 255, A: 255}
	for y := 300; y < 700; y++ {
		for x := 300; x < 700; x++ {
			src.SetNRGBA(x, y, red)
		}
	}
	luma := func(c color.RGBA) float64 { return 0.299*float64(c.R) + 0.587*float64(c.G) + 0.114*float64(c.B) }
	for _, bg := range []string{"#ffffff", "#0000ff"} {
		files := renderOf(t, src, bg)
		bgc := color.RGBA{B: 255}
		if bg == "#ffffff" {
			bgc = color.RGBA{R: 255, G: 255, B: 255}
		}
		floor := min(luma(color.RGBA{R: 255}), luma(bgc)) - 3
		for _, name := range []string{"apple-touch-icon.png", "icon-192.png", "icon-512.png", "icon-maskable-512.png"} {
			img := decodePNG(t, files[name])
			b := img.Bounds()
			for y := b.Min.Y; y < b.Max.Y; y++ {
				for x := b.Min.X; x < b.Max.X; x++ {
					if l := luma(rgba(img, x, y)); l < floor {
						t.Fatalf("bg %s %s: pixel (%d,%d) %v has luma %.1f, darker than both red and the background (%.1f)",
							bg, name, x, y, rgba(img, x, y), l, floor)
					}
				}
			}
		}
	}
}

func TestRender_SameMasterSameOutput(t *testing.T) {
	m := MasterFrom(disc(1024))
	a, err := Render(m, "#ffffff")
	require.NoError(t, err)
	b, err := Render(m, "#ffffff")
	require.NoError(t, err)
	require.Equal(t, a, b)
	require.Equal(t, Version(a), Version(b))
}

// The re-colour path, from what is stored. Everything that does not depend on
// the background must come out byte for byte the same, or the transparent files
// would change on every colour change.
func TestRecolour(t *testing.T) {
	first := renderOf(t, disc(1024), "#ffffff")
	second, err := Recolour(first, "#112233")
	require.NoError(t, err)

	require.Len(t, second, 8)
	for _, n := range []string{Master, "favicon.ico", "favicon-16.png", "favicon-32.png"} {
		require.Equal(t, first[n], second[n], "%s does not depend on the background", n)
	}
	for _, n := range []string{"apple-touch-icon.png", "icon-192.png", "icon-512.png", "icon-maskable-512.png"} {
		require.NotEqual(t, first[n], second[n], "%s is filled with the background", n)
		img := decodePNG(t, second[n])
		require.Equal(t, color.RGBA{R: 0x11, G: 0x22, B: 0x33, A: 255}, rgba(img, 0, 0), n)
	}
	require.NotEqual(t, Version(first), Version(second))

	// Many colour changes in a row leave the transparent files as they were.
	cur := second
	for _, c := range []string{"#000000", "#ff0000", "#ABCDEF", "#ffffff"} {
		cur, err = Recolour(cur, c)
		require.NoError(t, err)
	}
	for _, n := range []string{Master, "favicon.ico", "favicon-16.png", "favicon-32.png"} {
		require.Equal(t, first[n], cur[n], n)
	}
	// Back at the original colour, the picture is the same to within the one
	// rounding the PNG master costs (a level per channel), and the corner is
	// the background exactly.
	require.Equal(t, color.RGBA{R: 255, G: 255, B: 255, A: 255}, rgba(decodePNG(t, cur["icon-512.png"]), 0, 0))
	a, b := decodePNG(t, first["icon-512.png"]), decodePNG(t, cur["icon-512.png"])
	for y := range 512 {
		for x := range 512 {
			pa, pb := rgba(a, x, y), rgba(b, x, y)
			for _, d := range []int{int(pa.R) - int(pb.R), int(pa.G) - int(pb.G), int(pa.B) - int(pb.B)} {
				if d > 2 || d < -2 {
					t.Fatalf("pixel (%d,%d): %v vs %v", x, y, pa, pb)
				}
			}
		}
	}
}

func TestRecolour_Refusals(t *testing.T) {
	good := renderOf(t, disc(1024), "#ffffff")
	_, err := Recolour(good, "red")
	require.ErrorIs(t, err, ErrInvalidBackground)

	_, err = Recolour(Files{Master: []byte("not a png")}, "#000000")
	require.Error(t, err)

	missing := Files{Master: good[Master], "favicon.ico": good["favicon.ico"]}
	_, err = Recolour(missing, "#000000")
	require.Error(t, err, "the transparent files must come from the stored icon")

	small := Files{Master: solidPNG(t, 64)}
	_, err = Recolour(small, "#000000")
	require.Error(t, err)
}

func TestRender_Refusals(t *testing.T) {
	_, err := Render(MasterFrom(disc(1024)), "#fff")
	require.ErrorIs(t, err, ErrInvalidBackground)
	_, err = Render(image.NewRGBA(image.Rect(0, 0, 256, 256)), "#ffffff")
	require.Error(t, err, "a master is 512 x 512")
	_, err = Render(nil, "#ffffff")
	require.Error(t, err)
}

func TestMasterFrom_ExactSizeIsCopiedNotResampled(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 512, 512))
	src.SetNRGBA(0, 0, color.NRGBA{R: 1, G: 2, B: 3, A: 255})
	src.SetNRGBA(511, 511, color.NRGBA{R: 200, G: 100, B: 50, A: 255})
	src.SetNRGBA(10, 10, color.NRGBA{R: 255, A: 128})
	m := MasterFrom(src)
	require.Equal(t, image.Rect(0, 0, 512, 512), m.Bounds())
	require.Equal(t, color.RGBA{R: 1, G: 2, B: 3, A: 255}, m.RGBAAt(0, 0))
	require.Equal(t, color.RGBA{R: 200, G: 100, B: 50, A: 255}, m.RGBAAt(511, 511))
	require.Equal(t, color.RGBA{R: 128, A: 128}, m.RGBAAt(10, 10), "premultiplied")
}

func TestMasterFrom_SourceWithNonZeroOrigin(t *testing.T) {
	// A sub-image keeps its parent's coordinates.
	parent := solid(1200, color.NRGBA{R: 9, G: 9, B: 9, A: 255})
	sub := parent.SubImage(image.Rect(100, 100, 612, 612))
	m := MasterFrom(sub)
	require.Equal(t, color.RGBA{R: 9, G: 9, B: 9, A: 255}, m.RGBAAt(0, 0))
}

// exifOrientation6 is an APP1 segment saying "rotate 90 degrees clockwise to
// display": big-endian TIFF, one IFD entry, tag 0x0112 = 6.
func exifOrientation6() []byte {
	tiff := []byte{
		'M', 'M', 0, 0x2a, 0, 0, 0, 8, // header, first IFD at 8
		0, 1, // one entry
		0x01, 0x12, 0, 3, 0, 0, 0, 1, 0, 6, 0, 0, // Orientation, SHORT, count 1, value 6
		0, 0, 0, 0, // no next IFD
	}
	payload := append([]byte("Exif\x00\x00"), tiff...)
	n := len(payload) + 2
	return append([]byte{0xFF, 0xE1, byte(n >> 8), byte(n)}, payload...)
}

// Go's decoders do not read EXIF, so a sideways camera JPEG stays sideways. That
// is the specified behaviour (the logo does the same, and the previews show the
// result at once); this pins it so a later "fix" is a decision, not an accident.
// What also must hold: nothing from the source's metadata reaches an output.
func TestRender_ExifOrientationIgnored(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 1024, 1024))
	for y := range 1024 {
		for x := range 1024 {
			if x < 512 && y < 512 {
				src.SetRGBA(x, y, color.RGBA{R: 255, A: 255})
			} else {
				src.SetRGBA(x, y, color.RGBA{B: 255, A: 255})
			}
		}
	}
	var plain bytes.Buffer
	require.NoError(t, jpeg.Encode(&plain, src, &jpeg.Options{Quality: 95}))
	b := plain.Bytes()
	withExif := append(append(append([]byte{}, b[:2]...), exifOrientation6()...), b[2:]...)
	require.Contains(t, string(withExif), "Exif", "the fixture really carries the tag")

	img, err := jpeg.Decode(bytes.NewReader(withExif))
	require.NoError(t, err)
	files := renderOf(t, img, "#ffffff")

	master := decodePNG(t, files[Master])
	tl := rgba(master, 20, 20)
	require.Greater(t, int(tl.R), 200, "top-left is still red: orientation 6 was not applied")
	require.Less(t, int(tl.B), 60)
	br := rgba(master, 490, 490)
	require.Greater(t, int(br.B), 200)

	for name, data := range files {
		if name == "favicon.ico" {
			continue // its payloads are the PNGs checked below
		}
		for _, chunk := range []string{"eXIf", "tEXt", "iCCP", "zTXt", "iTXt"} {
			require.NotContains(t, string(data), chunk, "%s must carry no %s chunk", name, chunk)
		}
	}
}
