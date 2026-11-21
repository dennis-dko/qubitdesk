package imaging_test

import (
	"bytes"
	"image/png"
	"testing"

	"github.com/dennis-dko/qubitdesk/internal/support/imaging"
)

// The mark is drawn at the size asked for, and is the mark: an hourglass in
// white on a tile of the accent, one colour from top to bottom, its lower half
// dimmer than its upper one, and nothing outside the tile's rounded corners.
//
// Each point is named in the 560-unit box favicon.svg is drawn in.
func TestTheShippedMarkIsDrawnAtTheSizeAsked(t *testing.T) {
	for _, size := range []int{16, 192, 512} {
		encoded, err := imaging.MarkPNG(size)
		if err != nil {
			t.Fatalf("drawing the mark at %d: %v", size, err)
		}

		img, err := png.Decode(bytes.NewReader(encoded))
		if err != nil {
			t.Fatalf("the mark at %d is not a PNG: %v", size, err)
		}

		if b := img.Bounds(); b.Dx() != size || b.Dy() != size {
			t.Fatalf("the mark asked for at %d is %dx%d", size, b.Dx(), b.Dy())
		}

		// At sixteen pixels a half of the hourglass is a few pixels and its
		// edges are all anti-aliasing, so only the size is asked of it.
		if size < 64 {
			continue
		}

		at := func(x, y float64) [4]uint32 {
			r, g, b, a := img.At(int(x*float64(size)/560), int(y*float64(size)/560)).RGBA()

			return [4]uint32{r >> 8, g >> 8, b >> 8, a >> 8}
		}

		if corner := at(20, 20); corner[3] != 0 {
			t.Errorf("the corner of the %d mark is not transparent: %v", size, corner)
		}

		accent := [4]uint32{0x2f, 0x6f, 0xeb, 0xff}

		tile := func(where string, p [4]uint32) {
			if p != accent {
				t.Errorf("%s of the %d mark reads as rgba%v, want the accent rgba%v", where, size, p, accent)
			}
		}

		tile("the tile above the hourglass", at(280, 40))
		tile("the tile beside its waist", at(60, 280))
		tile("the tile below the hourglass", at(280, 520))

		if upper := at(280, 170); upper[3] != 0xff || upper[0] < 0xf0 || upper[1] < 0xf0 || upper[2] < 0xf0 {
			t.Errorf("the upper half of the %d mark reads as rgba%v, want white", size, upper)
		}

		// White at 0.55 over the accent: lighter than the tile by far, and short
		// of white by as far.
		if lower := at(280, 390); lower[3] != 0xff || lower[0] < 0x80 || lower[0] > 0xc0 {
			t.Errorf("the lower half of the %d mark reads as rgba%v, want white dimmed over the accent", size, lower)
		}
	}
}
