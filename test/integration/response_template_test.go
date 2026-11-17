//go:build integration

package integration

import (
	"net/http"
	"strconv"
	"testing"
)

// An answer template is made for a queue over HTTP and through the database,
// handed to an agent for a ticket in that queue with the ticket's placeholders
// filled, offered in every queue once its queue is cleared, and deleted; the
// administration log names each change.
func TestAnAnswerTemplateIsUsedOverHTTP(t *testing.T) {
	t.Parallel()

	a := start(t)
	admin := a.signInAsAdmin("a-much-better-password")
	agnes := a.signInAsAgent(admin, "Agnes", "agnes@example.com")

	var queues struct {
		Items []struct {
			ID   uint   `json:"id"`
			Name string `json:"name"`
		} `json:"items"`
	}

	admin.must(admin.api(http.MethodGet, "/queues", nil), http.StatusOK).Data(t, &queues)

	var queue struct{ ID uint }

	for _, found := range queues.Items {
		if found.Name == "Service Desk" {
			queue.ID = found.ID
		}
	}

	var made struct {
		ID      uint  `json:"id"`
		QueueID *uint `json:"queueId"`
	}

	admin.must(admin.api(http.MethodPost, "/response-templates", map[string]any{
		"name": "Desk thanks", "subject": "Re: <KIX_TICKET_Title>", "body": "We have <KIX_TICKET_Title> in hand.",
		"queueId": queue.ID,
	}), http.StatusCreated).Data(t, &made)

	if made.QueueID == nil || *made.QueueID != queue.ID {
		t.Fatalf("the template was made as %+v", made)
	}

	var ticket struct {
		ID uint `json:"id"`
	}

	agnes.must(agnes.api(http.MethodPost, "/tickets", map[string]any{
		"title": "Printer", "queueId": queue.ID, "body": "It jams.",
	}), http.StatusCreated).Data(t, &ticket)

	var offered struct {
		Items []struct {
			Name    string `json:"name"`
			Subject string `json:"subject"`
			Body    string `json:"body"`
		} `json:"items"`
	}

	agnes.must(agnes.api(http.MethodGet, "/tickets/"+strconv.FormatUint(uint64(ticket.ID), 10)+"/templates", nil),
		http.StatusOK).Data(t, &offered)

	if len(offered.Items) != 1 || offered.Items[0].Subject != "Re: Printer" ||
		offered.Items[0].Body != "We have Printer in hand." {
		t.Fatalf("Agnes was offered %+v; want the desk's template, filled", offered.Items)
	}

	path := "/response-templates/" + strconv.FormatUint(uint64(made.ID), 10)

	admin.must(admin.api(http.MethodPut, path, map[string]any{"clearQueue": true}), http.StatusOK).Data(t, &made)

	if made.QueueID != nil {
		t.Errorf("clearing the template's queue left it in %d", *made.QueueID)
	}

	admin.must(admin.api(http.MethodDelete, path, nil), http.StatusOK, http.StatusNoContent)
	admin.must(admin.api(http.MethodGet, path, nil), http.StatusNotFound)

	var log adminChanges

	admin.must(admin.api(http.MethodGet, "/admin/changes", nil), http.StatusOK).Data(t, &log)

	seen := map[string]bool{}
	for _, entry := range log.Entries {
		seen[entry.Action] = true
	}

	for _, want := range []string{"template.created", "template.updated", "template.deleted"} {
		if !seen[want] {
			t.Errorf("the administration log has no %s", want)
		}
	}
}

// A queue an answer template is offered in alone is refused its deletion, with
// how many templates name it, rather than failing on the database's key.
func TestAQueueAnAnswerTemplateNamesIsKept(t *testing.T) {
	t.Parallel()

	a := start(t)
	admin := a.signInAsAdmin("a-much-better-password")

	var queue struct {
		ID uint `json:"id"`
	}

	admin.must(admin.api(http.MethodPost, "/queues", map[string]any{"name": "Hardware"}), http.StatusCreated).
		Data(t, &queue)
	admin.must(admin.api(http.MethodPost, "/response-templates", map[string]any{
		"name": "Hardware thanks", "body": "Thanks.", "queueId": queue.ID,
	}), http.StatusCreated)

	if code := errorCode(t, admin.api(http.MethodDelete, "/queues/"+strconv.FormatUint(uint64(queue.ID), 10), nil)); code != "queueHasTemplates" {
		t.Errorf("deleting a queue a template names was answered %q, want queueHasTemplates", code)
	}
}
