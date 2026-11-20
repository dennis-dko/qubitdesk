package model_test

import (
	"testing"

	"github.com/dennis-dko/go-ticket-system/internal/domain/model"
)

// Go-Ticket-System is the name of every installation, and what an installation writes
// is added to it after a bar rather than put in its place.
func TestAnAdditionFollowsGo-Ticket-SystemAndNeverReplacesIt(t *testing.T) {
	t.Parallel()

	for addition, want := range map[string]string{
		"":            "Go-Ticket-System",
		"   ":         "Go-Ticket-System",
		"htp.net":     "Go-Ticket-System | htp.net",
		"  htp.net  ": "Go-Ticket-System | htp.net",

		// The name written again adds nothing. It was the obvious answer to "name
		// this installation" while the name could still be replaced, so it is
		// stored on installations that never meant to add anything.
		"Go-Ticket-System":  "Go-Ticket-System",
		"go-ticket-system ": "Go-Ticket-System",
	} {
		if got := model.Named(addition); got != want {
			t.Errorf("%q is named %q, want %q", addition, got, want)
		}
	}
}
