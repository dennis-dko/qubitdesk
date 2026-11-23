package model_test

import (
	"testing"

	"github.com/dennis-dko/qubitdesk/internal/domain/model"
)

// QubitDesk is the name of every installation, and what an installation writes
// is added to it after a bar rather than put in its place.
func TestAnAdditionFollowsQubitDeskAndNeverReplacesIt(t *testing.T) {
	t.Parallel()

	for addition, want := range map[string]string{
		"":            "QubitDesk",
		"   ":         "QubitDesk",
		"htp.net":     "QubitDesk | htp.net",
		"  htp.net  ": "QubitDesk | htp.net",

		// The name written again adds nothing. It was the obvious answer to "name
		// this installation" while the name could still be replaced, so it is
		// stored on installations that never meant to add anything.
		"QubitDesk":  "QubitDesk",
		"qubitdesk ": "QubitDesk",
	} {
		if got := model.Named(addition); got != want {
			t.Errorf("%q is named %q, want %q", addition, got, want)
		}
	}
}
