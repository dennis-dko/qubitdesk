//go:build integration

package integration

import (
	"net/http"
	"slices"
	"strconv"
	"testing"
)

type notificationDefinitionResponse struct {
	ID         uint     `json:"id"`
	Name       string   `json:"name"`
	Events     []string `json:"events"`
	Recipients []string `json:"recipients"`
	Criteria   struct {
		QueueIDs []uint `json:"queueIds"`
	} `json:"criteria"`
	Messages []struct {
		Language string `json:"language"`
		Subject  string `json:"subject"`
		Body     string `json:"body"`
	} `json:"messages"`
}

// A notification is made with what it says in two languages, read back so,
// refused a language the installation does not speak and a second notification
// of its name, changed, and deleted, over HTTP and through the database; the
// list tells a form what one can name and speak, an agent who does not set up
// the queues is refused it, and the administration log names each change.
func TestANotificationIsSetUpOverHTTP(t *testing.T) {
	t.Parallel()

	a := start(t)
	admin := a.signInAsAdmin("a-much-better-password")

	var queues struct {
		Items []struct {
			ID uint `json:"id"`
		} `json:"items"`
	}

	admin.must(admin.api(http.MethodGet, "/queues", nil), http.StatusOK).Data(t, &queues)

	if len(queues.Items) == 0 {
		t.Fatal("a new installation has no queue to name")
	}

	desk := queues.Items[0].ID
	sent := map[string]any{
		"name": "Opened", "events": []string{"TicketCreate"}, "recipients": []string{"AgentWritePermissions"},
		"criteria": map[string]any{"queueIds": []uint{desk}},
		"messages": []map[string]string{
			{"language": "en", "subject": "New: <KIX_TICKET_Title>", "body": "A ticket was opened."},
			{"language": "de", "subject": "Neu: <KIX_TICKET_Title>", "body": "Ein Ticket wurde eröffnet."},
		},
	}

	var made notificationDefinitionResponse

	admin.must(admin.api(http.MethodPost, "/notification-definitions", sent), http.StatusCreated).Data(t, &made)

	if !slices.Equal(made.Events, []string{"TicketCreate"}) || !slices.Equal(made.Criteria.QueueIDs, []uint{desk}) ||
		len(made.Messages) != 2 || made.Messages[0].Language != "de" || made.Messages[0].Subject != "Neu: <KIX_TICKET_Title>" {
		t.Fatalf("the notification was made as %+v", made)
	}

	if got := admin.api(http.MethodPost, "/notification-definitions", sent); got.Status != http.StatusConflict {
		t.Errorf("a second notification named Opened was answered %d, want 409", got.Status)
	}

	french := map[string]any{
		"name": "Ouvert", "events": []string{"TicketCreate"}, "recipients": []string{"AgentOwner"},
		"messages": []map[string]string{{"language": "fr", "subject": "Nouveau", "body": "Un ticket."}},
	}
	if got := admin.api(http.MethodPost, "/notification-definitions", french); got.Status != http.StatusBadRequest {
		t.Errorf("a notification in French was answered %d, want 400", got.Status)
	}

	var list struct {
		Items      []notificationDefinitionResponse `json:"items"`
		Events     []string                         `json:"events"`
		Recipients []string                         `json:"recipients"`
		Languages  []string                         `json:"languages"`
	}

	admin.must(admin.api(http.MethodGet, "/notification-definitions", nil), http.StatusOK).Data(t, &list)

	if len(list.Items) != 1 || !slices.Contains(list.Events, "ArticleCreate") ||
		!slices.Contains(list.Recipients, "AgentWatcher") || !slices.Equal(list.Languages, []string{"en", "de"}) {
		t.Errorf("the list reads %+v; want the notification and what one can name and speak", list)
	}

	path := "/notification-definitions/" + strconv.FormatUint(uint64(made.ID), 10)

	var changed notificationDefinitionResponse

	admin.must(admin.api(http.MethodPut, path, map[string]any{"recipients": []string{"AgentOwner", "AgentWatcher"}}),
		http.StatusOK).Data(t, &changed)

	if !slices.Equal(changed.Recipients, []string{"AgentOwner", "AgentWatcher"}) || len(changed.Messages) != 2 {
		t.Errorf("the changed notification reads %+v", changed)
	}

	agent := a.signInAsAgent(admin, "Agnes", "agnes@example.com")
	agent.must(agent.api(http.MethodGet, "/notification-definitions", nil), http.StatusForbidden)

	admin.must(admin.api(http.MethodDelete, path, nil), http.StatusNoContent, http.StatusOK)
	admin.must(admin.api(http.MethodGet, path, nil), http.StatusNotFound)

	var log adminChanges

	admin.must(admin.api(http.MethodGet, "/admin/changes", nil), http.StatusOK).Data(t, &log)

	actions := make([]string, 0, len(log.Entries))
	for _, entry := range log.Entries {
		actions = append(actions, entry.Action)
	}

	for _, want := range []string{"notification.created", "notification.updated", "notification.deleted"} {
		if !slices.Contains(actions, want) {
			t.Errorf("the administration log reads %v; want %s among it", actions, want)
		}
	}
}
