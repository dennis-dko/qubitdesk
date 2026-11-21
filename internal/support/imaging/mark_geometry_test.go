package imaging

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// The mark Mark draws is favicon.svg, number for number.
//
// The tab shows the SVG itself, while the installed app's icons and the Windows
// icon are drawn from the figures in mark.go: two copies of one drawing. Two
// copies nothing compares are how one of them goes on showing an old picture -
// the tab icon once did - so every figure, the tile and its colours are read out
// of the SVG and held to Mark's. Mark fills every figure in the one white and
// draws nothing on the tile but paths, so a figure carrying anything besides its
// path - an opacity, a fill, a transform - and a shape that is not a path are
// refused here, rather than drawn one way in the tab and another on a home
// screen.
func TestTheMarkIsFaviconSVGsGeometry(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "interface", "web", "asset", "favicon.svg"))
	if err != nil {
		t.Fatalf("reading favicon.svg: %v", err)
	}

	svg := string(raw)

	var figures []string

	for _, found := range regexp.MustCompile(`<path d="([^"]+)"/>`).FindAllStringSubmatch(svg, -1) {
		figures = append(figures, found[1])
	}

	if drawn := strings.Count(svg, "<path"); drawn != len(figures) {
		t.Errorf("favicon.svg has %d figures and %d of them carry nothing but their path, which is all Mark draws",
			drawn, len(figures))
	}

	if shape := regexp.MustCompile(`<(circle|ellipse|polygon|polyline|line)\b`).FindString(svg); shape != "" {
		t.Errorf("favicon.svg draws a %s, which Mark does not", shape[1:])
	}

	if rects := strings.Count(svg, "<rect"); rects != 1 {
		t.Errorf("favicon.svg has %d rectangles, where Mark draws the tile and nothing else as one", rects)
	}

	if !slices.Equal(figures, markFigures) {
		t.Errorf("favicon.svg's figures are not the ones Mark draws:\n svg  %v\n mark %v", figures, markFigures)
	}

	for _, want := range []string{
		fmt.Sprintf(`viewBox="0 0 %g %g"`, markBox, markBox),
		fmt.Sprintf(`<rect width="%g" height="%g" rx="%g" fill="url(#ground)"/>`, markBox, markBox, markTileRadius),
		`<linearGradient id="ground" x1="0" y1="0" x2="0" y2="1">`,
		fmt.Sprintf(`<g fill="#%02x%02x%02x">`, figureInk.R, figureInk.G, figureInk.B),
	} {
		if !strings.Contains(svg, want) {
			t.Errorf("favicon.svg has no %s, which is what Mark draws", want)
		}
	}

	for _, stop := range markGround {
		want := fmt.Sprintf(`<stop offset="%g" stop-color="#%02x%02x%02x"/>`, stop.at, stop.ink.R, stop.ink.G, stop.ink.B)
		if !strings.Contains(svg, want) {
			t.Errorf("favicon.svg's tile has no %s, which is a colour Mark draws it in", want)
		}
	}

	if stops := strings.Count(svg, "<stop "); stops != len(markGround) {
		t.Errorf("favicon.svg's tile passes through %d colours and Mark's through %d", stops, len(markGround))
	}
}
