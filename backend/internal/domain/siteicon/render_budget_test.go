package siteicon

import (
	"image"
	"image/color"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/image/draw"
)

// stripes is a fine line pattern: black lines of the given width on white,
// vertical, every period pixels. The worst case for a downscale that skips
// pixels instead of averaging them.
func stripes(side, period, width int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, side, side))
	for y := range side {
		for x := range side {
			c := color.RGBA{R: 255, G: 255, B: 255, A: 255}
			if x%period < width {
				c = color.RGBA{A: 255}
			}
			img.SetRGBA(x, y, c)
		}
	}
	return img
}

// A large source is not scaled in one step: draw.CatmullRom keeps a buffer of
// (destination width x source height x 32) bytes, 64 MiB for a 4096 side, on
// top of the 64 MiB source. MasterFrom shrinks big sources first, so what it
// allocates stays a fraction of the source.
func TestMasterFrom_LargeSourceStaysWithinBudget(t *testing.T) {
	src := stripes(MaxSide, 8, 4)

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	m := MasterFrom(src)
	runtime.ReadMemStats(&after)

	require.Equal(t, image.Rect(0, 0, MasterSide, MasterSide), m.Bounds())
	grew := int64(after.TotalAlloc - before.TotalAlloc)
	require.Less(t, grew, int64(32<<20), "MasterFrom of a %d px source allocated %d bytes", MaxSide, grew)
}

// The two-step shrink must be as good as the one-step. The sides below are
// multiples of 512 or the lines repeat inside each output pixel's footprint, so
// the true value of an output pixel is known: the share of white. A resampler
// that skips pixels instead of averaging them shows as ripple around that
// value. The two-step result may not ripple more than the one-step by more than
// a level.
//
// The last two rows are the awkward cases: the lines are finer than the first
// step can represent (it averages boxes of 4 px at 3584 and 3 at 2560, so
// periods under 8 and 6) and a box of a different width than their period
// leaves a faint beat.
// The one-step scaler has none only because its footprint happens to be a whole
// number of periods. The beat is bounded at 4% of white, which no icon shows.
func TestMasterFrom_LargeSourceLooksLikeOneStep(t *testing.T) {
	for _, tc := range []struct {
		name                string
		side, period, width int
		slack               float64 // levels over the one-step result
	}{
		{"4096, 8 px lines", 4096, 8, 4, 1},
		{"4096, 4 px lines", 4096, 4, 2, 1},
		{"4096, one px lines", 4096, 2, 1, 1},
		{"3584, 7 px lines, below the first step's Nyquist", 3584, 7, 3, 10},
		{"2560, 5 px lines, below the first step's Nyquist", 2560, 5, 2, 10},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := stripes(tc.side, tc.period, tc.width)
			ideal := 255 * float64(tc.period-tc.width) / float64(tc.period)
			ripple := func(m *image.RGBA) float64 {
				var sum float64
				var n int
				for y := 8; y < MasterSide-8; y += 7 { // not the border, where kernels clamp
					for x := 8; x < MasterSide-8; x++ {
						d := float64(m.RGBAAt(x, y).G) - ideal
						if d < 0 {
							d = -d
						}
						sum += d
						n++
					}
				}
				return sum / float64(n)
			}
			direct := image.NewRGBA(image.Rect(0, 0, MasterSide, MasterSide))
			draw.CatmullRom.Scale(direct, direct.Bounds(), src, src.Bounds(), draw.Src, nil)

			two, one := ripple(MasterFrom(src)), ripple(direct)
			t.Logf("mean distance from the true value: two steps %.2f levels, one step %.2f", two, one)
			require.LessOrEqual(t, two, one+tc.slack)
		})
	}
}

// Averaging is done in premultiplied alpha: a half-transparent red stays red,
// it does not turn grey or dark.
func TestMasterFrom_LargeSourceKeepsTransparentColour(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 2560, 2560))
	for y := range 2560 {
		for x := range 1280 {
			src.SetNRGBA(x, y, color.NRGBA{R: 255, A: 128}) // the right half stays transparent black
		}
	}
	m := MasterFrom(src)
	left := m.RGBAAt(100, 256)
	require.InDelta(t, 128, int(left.A), 1)
	require.InDelta(t, 128, int(left.R), 2, "premultiplied red")
	require.Zero(t, left.G)
	require.Equal(t, color.RGBA{}, m.RGBAAt(400, 256))
}
