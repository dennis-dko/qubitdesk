package model_test

import (
	"testing"

	"github.com/dennis-dko/go-ticket-system/internal/domain/model"
)

// What an installation calls itself in the language that is reading: Go-Ticket-System,
// and the addition written for that language.
func TestTheInstanceIsNamedInTheLanguageThatAsked(t *testing.T) {
	t.Parallel()

	branding := model.Branding{
		Title: "htp.net",
		Translations: map[string]model.BrandingText{
			"de": {Title: "htp.net Deutschland"},
			"en": {Title: "htp.net"},
		},
	}

	if got := branding.NameIn("de"); got != "Go-Ticket-System | htp.net Deutschland" {
		t.Errorf("a German reader is shown %q", got)
	}

	// A language with no addition of its own falls back to the untranslated one
	// rather than to nothing.
	if got := branding.NameIn("fr"); got != "Go-Ticket-System | htp.net" {
		t.Errorf("an untranslated language is shown %q", got)
	}

	// And a translation that exists but is empty is not an addition.
	empty := model.Branding{
		Title:        "htp.net",
		Translations: map[string]model.BrandingText{"de": {Title: "   "}},
	}

	if got := empty.NameIn("de"); got != "Go-Ticket-System | htp.net" {
		t.Errorf("a blank translation was used as an addition: %q", got)
	}

	// Nothing written anywhere is Go-Ticket-System alone.
	if got := (model.Branding{}).NameIn("de"); got != "Go-Ticket-System" {
		t.Errorf("an installation that added nothing is called %q", got)
	}
}
