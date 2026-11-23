package web_test

import (
	"regexp"
	"slices"
	"testing"

	"github.com/dennis-dko/qubitdesk/internal/domain/model"
)

// Every kind of thing that happens to a ticket is shown as a sentence, and the
// screen has no sentence for a kind the server never writes.
//
// The history is drawn from a table of its own, by the event each line carries;
// an event the table does not know is shown as the event, which is how a code
// would come to stand in the middle of a German screen.
func TestEveryTicketHistoryEventHasItsSentence(t *testing.T) {
	sentences := regexp.MustCompile(`(?s)const HISTORY_SENTENCES = \{(.*?)\n\};`).FindStringSubmatch(asset(t, "/app.js"))
	if sentences == nil {
		t.Fatal("app.js has no HISTORY_SENTENCES; this test is reading nothing")
	}

	var worded []string
	for _, match := range regexp.MustCompile(`(?m)^\s*([a-z]+): \(\) =>`).FindAllStringSubmatch(sentences[1], -1) {
		worded = append(worded, match[1])
	}

	var written []string
	for _, event := range model.HistoryEvents() {
		written = append(written, string(event))
	}

	for _, event := range written {
		if !slices.Contains(worded, event) {
			t.Errorf("the server writes %q into a ticket's history and the screen has no sentence for it", event)
		}
	}

	for _, event := range worded {
		if !slices.Contains(written, event) {
			t.Errorf("the screen has a sentence for %q, which the server never writes into a history", event)
		}
	}
}
