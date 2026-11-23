package imaging

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"strconv"

	"golang.org/x/image/vector"
)

// The mark, in the 560-unit box favicon.svg is drawn in: QubitDesk's Q and D in
// white on a blue tile, the right of the Q's ring the stem of the D, and a
// ticket in the Q's counter.
//
// Drawn rather than kept as pictures, because a picture in the tree is a copy of
// favicon.svg nobody can regenerate - and a copy that silently keeps an old mark
// is what happened to the tab icon once already. The numbers are the SVG's, and
// TestTheMarkIsFaviconSVGsGeometry fails the moment the two part. There is no
// renderer here that reads SVG documents, and one added to draw two figures
// would be a dependency doing far more than this needs; the figures are written
// in lines and elliptical arcs, which trace below follows.
const (
	markBox        = 560.0
	markTileRadius = 29.0
)

// groundStop is one colour the tile passes through, at a fraction of the way
// down it.
type groundStop struct {
	at  float64
	ink color.NRGBA
}

// markGround is the tile's colour from its top edge to its bottom one.
var markGround = []groundStop{
	{0, color.NRGBA{R: 0x0a, G: 0x69, B: 0xa7, A: 0xff}},
	{0.5, color.NRGBA{R: 0x09, G: 0x57, B: 0x95, A: 0xff}},
	{1, color.NRGBA{R: 0x03, G: 0x44, B: 0x7a, A: 0xff}},
}

// markFigures are the white shapes, each a path of its own as the SVG has them:
// the Q and the D, their counters cut out by running the other way round, and
// the ticket with its notches and its three perforations.
var markFigures = []string{
	"M73.36 268A140 140 0 0 1 302.82 160.31V128H346.64A140 140 0 0 1 486.64 268A140 140 0 0 1 346.64 408H333.06" +
		"L297.64 443.42L255.67 401.45A140 140 0 0 1 73.36 268ZM111.36 268A102 102 0 0 0 213.36 370" +
		"A102 102 0 0 0 315.36 268A102 102 0 0 0 213.36 166A102 102 0 0 0 111.36 268ZM346.64 166H309.26" +
		"A140 140 0 0 1 309.26 370H346.64A102 102 0 0 0 448.64 268A102 102 0 0 0 346.64 166Z",
	"M155.38 249.62L194.98 210.02A8 8 0 0 1 206.29 210.02L230.33 234.06A12 12 0 0 0 230.33 251.03" +
		"A12 12 0 0 0 247.3 251.03L271.34 275.07A8 8 0 0 1 271.34 286.38L231.74 325.98A8 8 0 0 1 220.43 325.98" +
		"L196.39 301.94A12 12 0 0 0 196.39 284.97A12 12 0 0 0 179.42 284.97L155.38 260.93A8 8 0 0 1 155.38 249.62" +
		"ZM200.1 275.6A4 4 0 0 0 200.1 281.26A4 4 0 0 0 205.76 281.26L206.47 280.55A4 4 0 0 0 206.47 274.89" +
		"A4 4 0 0 0 200.81 274.89ZM210.18 265.53A4 4 0 0 0 210.18 271.18A4 4 0 0 0 215.83 271.18" +
		"L216.54 270.47A4 4 0 0 0 216.54 264.82A4 4 0 0 0 210.89 264.82ZM220.25 255.45" +
		"A4 4 0 0 0 220.25 261.11A4 4 0 0 0 225.91 261.11L226.62 260.4A4 4 0 0 0 226.62 254.74" +
		"A4 4 0 0 0 220.96 254.74Z",
}

var (
	figureInk  = color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}
	cubicKappa = 0.55228
)

// Mark draws the mark this application ships with, size pixels square.
//
// Each size is drawn at that size rather than scaled from a larger one: a 256
// shrunk to 16 loses the corners of the tile, and a 64 blown up to 512 is a blur
// on exactly the home screen it was made for.
func Mark(size int) (image.Image, error) {
	scale := float64(size) / markBox

	tile := coverage(size, func(r *vector.Rasterizer) error {
		roundedRect(r, markBox*scale, markTileRadius*scale)

		return nil
	})

	// Each figure is filled on its own and laid over the others, as the SVG paints
	// its elements; filled together, two that overlap the other way round would
	// cancel where they meet.
	figures := image.NewAlpha(image.Rect(0, 0, size, size))

	for _, data := range markFigures {
		var traced error

		layer(figures, coverage(size, func(r *vector.Rasterizer) error {
			traced = trace(r, data, scale)

			return traced
		}))

		if traced != nil {
			return nil, fmt.Errorf("tracing the mark: %w", traced)
		}
	}

	out := image.NewNRGBA(image.Rect(0, 0, size, size))

	for y := range size {
		ground := groundAt((float64(y) + 0.5) / float64(size))

		for x := range size {
			covered := tile.AlphaAt(x, y).A
			if covered == 0 {
				continue
			}

			shade := blend(ground, figureInk, float64(figures.AlphaAt(x, y).A)/255)
			shade.A = covered

			out.SetNRGBA(x, y, shade)
		}
	}

	return out, nil
}

// MarkPNG is Mark, encoded.
func MarkPNG(size int) ([]byte, error) {
	drawn, err := Mark(size)
	if err != nil {
		return nil, err
	}

	var encoded bytes.Buffer

	if err := png.Encode(&encoded, drawn); err != nil {
		return nil, fmt.Errorf("encoding the mark at %dpx: %w", size, err)
	}

	return encoded.Bytes(), nil
}

// coverage rasterises one path into a mask, anti-aliased.
func coverage(size int, path func(*vector.Rasterizer) error) *image.Alpha {
	r := vector.NewRasterizer(size, size)

	mask := image.NewAlpha(image.Rect(0, 0, size, size))
	if path(r) != nil {
		return mask
	}

	r.Draw(mask, mask.Bounds(), image.Opaque, image.Point{})

	return mask
}

// layer lays one mask over another, as one opaque paint over another.
func layer(under, over *image.Alpha) {
	for i, a := range over.Pix {
		under.Pix[i] += uint8(int(a) * (255 - int(under.Pix[i])) / 255)
	}
}

// groundAt is the tile's colour a fraction of the way down it.
func groundAt(down float64) color.NRGBA {
	for i := 1; i < len(markGround); i++ {
		from, to := markGround[i-1], markGround[i]
		if down <= to.at {
			return blend(from.ink, to.ink, math.Max(0, (down-from.at)/(to.at-from.at)))
		}
	}

	return markGround[len(markGround)-1].ink
}

// errUnreadablePath is a figure written in something trace does not follow.
var errUnreadablePath = errors.New("unreadable path data")

// pathArguments is how many numbers each command trace follows takes. A command
// missing here takes none and is refused.
var pathArguments = map[byte]int{'M': 2, 'L': 2, 'H': 1, 'V': 1, 'A': 7, 'Z': 0}

// trace feeds one path's data to the rasteriser, scaled from the mark's box to
// the picture's pixels.
//
// It follows the commands the figures are written in - moves, lines, horizontal
// and vertical lines, elliptical arcs and closes, absolute or relative - and
// refuses any other, so a figure that gained a curve it cannot follow fails the
// tests rather than quietly leaving the icon.
func trace(r *vector.Rasterizer, data string, scale float64) error {
	tokens := pathTokens(data)

	var x, y, startX, startY float64

	command := byte(0)

	for i := 0; i < len(tokens); {
		if letter := tokens[i]; len(letter) == 1 && isCommand(letter[0]) {
			command = letter[0]
			i++
		} else if command == 0 {
			return fmt.Errorf("%w: %q before any command", errUnreadablePath, letter)
		}

		relative := command >= 'a'

		values := make([]float64, pathArguments[command&^0x20])
		for n := range values {
			if i >= len(tokens) {
				return fmt.Errorf("%w: %q ends inside %c", errUnreadablePath, data, command)
			}

			v, err := strconv.ParseFloat(tokens[i], 64)
			if err != nil {
				return fmt.Errorf("%w: %v", errUnreadablePath, err)
			}

			values[n] = v
			i++
		}

		ox, oy := 0.0, 0.0
		if relative {
			ox, oy = x, y
		}

		switch command &^ 0x20 {
		case 'M':
			x, y = ox+values[0], oy+values[1]
			startX, startY = x, y
			r.MoveTo(float32(x*scale), float32(y*scale))

			// Pairs after a move's first are lines.
			command = 'L'
			if relative {
				command = 'l'
			}
		case 'L':
			x, y = ox+values[0], oy+values[1]
			r.LineTo(float32(x*scale), float32(y*scale))
		case 'H':
			x = ox + values[0]
			r.LineTo(float32(x*scale), float32(y*scale))
		case 'V':
			y = oy + values[0]
			r.LineTo(float32(x*scale), float32(y*scale))
		case 'A':
			toX, toY := ox+values[5], oy+values[6]
			arc(r, x, y, values[0], values[1], values[2], values[3] != 0, values[4] != 0, toX, toY, scale)
			x, y = toX, toY
		case 'Z':
			r.ClosePath()
			x, y = startX, startY

			// A close takes no numbers, so one written after it has no command to
			// belong to.
			command = 0
		default:
			return fmt.Errorf("%w: the %c command", errUnreadablePath, command)
		}
	}

	return nil
}

// isCommand reports whether a letter in path data is a command rather than the
// exponent of a number.
func isCommand(c byte) bool {
	return (c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z') && c != 'e' && c != 'E'
}

// pathTokens splits path data into its commands and numbers. A number ends at a
// separator, a command, a sign that is not an exponent's, or a second decimal
// point - so "1.5.5" is two numbers, as SVG reads it.
func pathTokens(data string) []string {
	var tokens []string

	start, dotted := -1, false

	flush := func(at int) {
		if start >= 0 {
			tokens = append(tokens, data[start:at])
		}

		start, dotted = -1, false
	}

	for i := 0; i < len(data); i++ {
		c := data[i]

		switch {
		case c == ' ' || c == ',' || c == '\t' || c == '\n' || c == '\r':
			flush(i)
		case isCommand(c):
			flush(i)
			tokens = append(tokens, data[i:i+1])
		case c == '-' || c == '+':
			if start >= 0 && (data[i-1] == 'e' || data[i-1] == 'E') {
				continue
			}

			flush(i)
			start = i
		case c == '.':
			if dotted {
				flush(i)
			}

			if start < 0 {
				start = i
			}

			dotted = true
		default:
			if start < 0 {
				start = i
			}
		}
	}

	flush(len(data))

	return tokens
}

// arc follows an elliptical arc as SVG writes one - from the current point to
// the end, on an ellipse of the given radii turned by rotation degrees, the
// larger or the smaller way round, in the sweep direction - as cubic curves,
// which is what the rasteriser draws.
//
// The ellipse's centre is worked out from its two ends as the SVG specification
// does it (appendix F.6.5, with the radii grown in F.6.6 where they cannot reach),
// and the arc is cut into pieces of at most a quarter turn, each close enough to
// a cubic that the difference is a small fraction of a pixel at 512.
func arc(r *vector.Rasterizer, x1, y1, rx, ry, rotation float64, large, sweep bool, x2, y2, scale float64) {
	if x1 == x2 && y1 == y2 {
		return
	}

	rx, ry = math.Abs(rx), math.Abs(ry)
	if rx == 0 || ry == 0 {
		r.LineTo(float32(x2*scale), float32(y2*scale))

		return
	}

	phi := rotation * math.Pi / 180
	cos, sin := math.Cos(phi), math.Sin(phi)

	hx, hy := (x1-x2)/2, (y1-y2)/2
	px, py := cos*hx+sin*hy, -sin*hx+cos*hy

	if grow := px*px/(rx*rx) + py*py/(ry*ry); grow > 1 {
		rx, ry = rx*math.Sqrt(grow), ry*math.Sqrt(grow)
	}

	reach := 0.0
	if below := rx*rx*py*py + ry*ry*px*px; below > 0 {
		reach = math.Sqrt(math.Max(0, (rx*rx*ry*ry-below)/below))
	}

	if large == sweep {
		reach = -reach
	}

	ccx, ccy := reach*rx*py/ry, -reach*ry*px/rx
	cx, cy := cos*ccx-sin*ccy+(x1+x2)/2, sin*ccx+cos*ccy+(y1+y2)/2

	from := math.Atan2((py-ccy)/ry, (px-ccx)/rx)
	turn := math.Atan2((-py-ccy)/ry, (-px-ccx)/rx) - from

	switch {
	case sweep && turn < 0:
		turn += 2 * math.Pi
	case !sweep && turn > 0:
		turn -= 2 * math.Pi
	}

	pieces := int(math.Ceil(math.Abs(turn) / (math.Pi / 2)))
	step := turn / float64(pieces)
	k := 4.0 / 3.0 * math.Tan(step/4)

	point := func(ux, uy float64) (float32, float32) {
		ex, ey := rx*ux, ry*uy

		return float32((cos*ex - sin*ey + cx) * scale), float32((sin*ex + cos*ey + cy) * scale)
	}

	for piece := range pieces {
		a := from + float64(piece)*step
		b := a + step

		c1x, c1y := point(math.Cos(a)-k*math.Sin(a), math.Sin(a)+k*math.Cos(a))
		c2x, c2y := point(math.Cos(b)+k*math.Sin(b), math.Sin(b)-k*math.Cos(b))
		ex, ey := point(math.Cos(b), math.Sin(b))

		r.CubeTo(c1x, c1y, c2x, c2y, ex, ey)
	}
}

// roundedRect traces the tile, which fills the picture. The corners are cubics,
// the usual approximation of a quarter circle, exact enough at 512 pixels, let
// alone 16.
func roundedRect(r *vector.Rasterizer, side, radius float64) {
	k := radius * cubicKappa

	f := func(v float64) float32 { return float32(v) }

	r.MoveTo(f(radius), 0)
	r.LineTo(f(side-radius), 0)
	r.CubeTo(f(side-radius+k), 0, f(side), f(radius-k), f(side), f(radius))
	r.LineTo(f(side), f(side-radius))
	r.CubeTo(f(side), f(side-radius+k), f(side-radius+k), f(side), f(side-radius), f(side))
	r.LineTo(f(radius), f(side))
	r.CubeTo(f(radius-k), f(side), 0, f(side-radius+k), 0, f(side-radius))
	r.LineTo(0, f(radius))
	r.CubeTo(0, f(radius-k), f(radius-k), 0, f(radius), 0)
	r.ClosePath()
}

// blend mixes towards a colour by a fraction of it.
func blend(base, over color.NRGBA, amount float64) color.NRGBA {
	mix := func(a, b uint8) uint8 {
		return uint8(float64(a)*(1-amount) + float64(b)*amount + 0.5)
	}

	return color.NRGBA{R: mix(base.R, over.R), G: mix(base.G, over.G), B: mix(base.B, over.B), A: base.A}
}
