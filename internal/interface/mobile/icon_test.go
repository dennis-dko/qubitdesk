package mobile

import (
	"regexp"
	"strings"
	"testing"
)

// The app's icons are favicon.svg's drawing.
//
// The launcher's tile and figures, the status bar's shape and the mark on the
// app's own screens are vector drawables, and the hourglass was once written
// into each by hand, in coordinates worked out from the SVG's - and once more as
// drawing code in Look.kt. Each drawable carries the SVG's own path data,
// opacities and colours now, placed by a group, so each is held to the file
// here: a figure the SVG gains, loses, moves or dims that a drawable does not is
// reported rather than found on somebody's home screen.
func TestTheAppsIconsAreTheMark(t *testing.T) {
	svg := read(t, "../web/asset/favicon.svg")

	// Each figure's path, and how much of the white it is painted with: the
	// SVG's opacity, a drawable's fillAlpha.
	figures := map[string]string{}

	for _, found := range regexp.MustCompile(`<path d="([^"]+)"( opacity="([^"]+)")?`).FindAllStringSubmatch(svg, -1) {
		figures[found[1]] = found[3]
	}

	if len(figures) == 0 {
		t.Fatal("favicon.svg has no figures, so this compares nothing")
	}

	var colours []string

	for _, found := range regexp.MustCompile(`stop-color="#([0-9a-f]{6})"`).FindAllStringSubmatch(svg, -1) {
		colours = append(colours, `android:color="#FF`+strings.ToUpper(found[1])+`"`)
	}

	drawable := func(name string) string {
		return read(t, "src/main/res/drawable/"+name)
	}

	for _, name := range []string{"ic_launcher_foreground.xml", "ic_notification.xml", "ic_mark.xml"} {
		drawn := map[string]string{}

		for _, path := range regexp.MustCompile(`(?s)<path\b.*?/>`).FindAllString(drawable(name), -1) {
			data := regexp.MustCompile(`android:pathData="([^"]+)"`).FindStringSubmatch(path)
			if data == nil {
				continue
			}

			alpha := ""
			if found := regexp.MustCompile(`android:fillAlpha="([^"]+)"`).FindStringSubmatch(path); found != nil {
				alpha = found[1]
			}

			drawn[data[1]] = alpha
		}

		for figure, opacity := range figures {
			alpha, ok := drawn[figure]

			switch {
			case !ok:
				t.Errorf("%s does not draw favicon.svg's figure %.48q", name, figure)
			case alpha != opacity:
				t.Errorf("%s paints favicon.svg's figure %.48q at fillAlpha %q, where the SVG has opacity %q",
					name, figure, alpha, opacity)
			}
		}
	}

	for _, name := range []string{"ic_launcher_background.xml", "ic_mark.xml"} {
		body := drawable(name)

		for _, colour := range colours {
			if !strings.Contains(body, colour) {
				t.Errorf("%s's tile does not pass through %s, a colour of favicon.svg's", name, colour)
			}
		}

		if stops := strings.Count(body, "<item "); stops != len(colours) {
			t.Errorf("%s's tile passes through %d colours and favicon.svg's through %d", name, stops, len(colours))
		}
	}

	if !strings.Contains(source(t, "Look.kt"), "painterResource(R.drawable.ic_mark)") {
		t.Error("Look.kt does not take the mark from ic_mark, so the app's own screens draw a copy of their own")
	}
}
