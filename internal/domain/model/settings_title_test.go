package model_test

import (
	"testing"

	"github.com/dennis-dko/go-ticket-system/internal/domain/model"
)

// The tab carries Go-Ticket-System and an addition of its own where an installation
// wrote one, and the title's addition where it did not.
//
// The fallback is what every installation that has never opened this setting
// relies on: the field arrived after they were all configured, so it is empty
// everywhere, and an empty field that meant no addition would take the title's
// off every tab.
func TestTheTabAddsItsOwnWordsOrTheTitles(t *testing.T) {
	for name, tc := range map[string]struct {
		branding model.Branding
		want     string
	}{
		"nothing added": {
			branding: model.Branding{},
			want:     "Go-Ticket-System",
		},
		"the title's addition": {
			branding: model.Branding{Title: "Beispiel GmbH & Co. KG"},
			want:     "Go-Ticket-System | Beispiel GmbH & Co. KG",
		},
		"its own addition": {
			branding: model.Branding{Title: "Beispiel GmbH & Co. KG", TabTitle: "Beispiel"},
			want:     "Go-Ticket-System | Beispiel",
		},
		// Spaces are not an addition. Somebody clearing the field leaves whatever
		// the keyboard left behind, and a tab with " " added has nothing added.
		"blank": {
			branding: model.Branding{Title: "htp.net", TabTitle: "   "},
			want:     "Go-Ticket-System | htp.net",
		},
	} {
		t.Run(name, func(t *testing.T) {
			if got := tc.branding.TabName(); got != tc.want {
				t.Errorf("the tab is called %q, want %q", got, tc.want)
			}
		})
	}
}
