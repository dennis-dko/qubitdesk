package service

import (
	"strings"
	"testing"

	"github.com/dennis-dko/qubitdesk/internal/support/i18n"
)

// Everything the demo writes is in every dictionary, under demo., and nothing
// there is a word the demo no longer writes.
//
// The demo is written in the language the installer was read in, from the one
// dictionary every part of the application reads; its English stands beside the
// code. A text missing from a dictionary would be written in English into an
// installation set up in another language, which nothing else would notice.
func TestTheDemosWordsAreInEveryDictionary(t *testing.T) {
	written := map[string]bool{}

	for _, word := range demoWords() {
		if !strings.HasPrefix(word.key, "demo.") || word.english == "" {
			t.Errorf("the demo writes %q under %q, which is no word of the demo's", word.english, word.key)
		}

		if written[word.key] {
			t.Errorf("the demo writes two texts under %s", word.key)
		}

		written[word.key] = true
	}

	for _, language := range i18n.Languages()[1:] {
		words := i18n.Dictionary(language)

		for key := range written {
			if words[key] == "" {
				t.Errorf("the demo writes %s and %s has no words for it", key, language)
			}
		}

		for key := range words {
			if strings.HasPrefix(key, "demo.") && !written[key] {
				t.Errorf("%s has %s, which the demo no longer writes", language, key)
			}
		}
	}

	if got := demoTickets[0].title.in(i18n.For("de")); got != "Der Drucker im zweiten Stock druckt leere Seiten" {
		t.Errorf("the first demo ticket's German title reads %q", got)
	}
}

// Everything the demo dates has passed by the time it runs, a ticket's steps
// come after it was opened and in the order they are written, and only a step
// into a waiting state waits - which is what lets the ticket service take each
// of them as the moment it happened.
func TestTheDemosTimelineHasPassed(t *testing.T) {
	contacts := map[string]bool{}

	for _, o := range demoOrganisations {
		for _, name := range o.contacts {
			contacts[name] = true
		}
	}

	for _, ticket := range demoTickets {
		if ticket.opened <= 0 {
			t.Errorf("%s is opened %v before the demo runs, which is not before it", ticket.title.key, ticket.opened)
		}

		if !contacts[ticket.contact] {
			t.Errorf("%s is for %q, whom no demo organisation has", ticket.title.key, ticket.contact)
		}

		last := ticket.opened

		for _, step := range ticket.steps {
			if step.ago <= 0 || step.ago >= last {
				t.Errorf("a step of %s is %v before the demo, after %v: out of order or not in the past",
					ticket.title.key, step.ago, last)
			}

			if waits := strings.HasPrefix(step.state, "pending"); waits != (step.waitFor > 0) {
				t.Errorf("a step of %s puts the ticket into %q waiting %v", ticket.title.key, step.state, step.waitFor)
			}

			last = step.ago
		}
	}
}
