//go:build integration

package integration

import (
	"net/http"
	"strconv"
	"testing"
	"time"
)

type notificationList struct {
	Items []struct {
		ID       uint       `json:"id"`
		TicketID uint       `json:"ticketId"`
		Subject  string     `json:"subject"`
		Body     string     `json:"body"`
		ReadAt   *time.Time `json:"readAt"`
	} `json:"items"`
	Unread int `json:"unread"`
}

// A ticket opened in a queue tells the agents who work in it, over HTTP and
// through the database: the agent reads it in their language with the ticket's
// placeholders filled, marks it read, and the one who opened the ticket was told
// nothing; somebody else's notification is not found, and the account's export
// still reads what it was told.
func TestAnOpenedTicketIsSentToTheAgentsAroundIt(t *testing.T) {
	t.Parallel()

	a := start(t)
	admin := a.signInAsAdmin("a-much-better-password")
	agnes := a.signInAsAgent(admin, "Agnes", "agnes@example.com")

	agnes.must(agnes.api(http.MethodPut, "/me/language", map[string]string{"language": "de"}), http.StatusOK)

	admin.must(admin.api(http.MethodPost, "/notification-definitions", map[string]any{
		"name": "Opened", "events": []string{"TicketCreate"}, "recipients": []string{"AgentWritePermissions"},
		"messages": []map[string]string{
			{"language": "en", "subject": "New: <KIX_TICKET_Title>", "body": "<KIX_TICKET_State>"},
			{"language": "de", "subject": "Neu: <KIX_TICKET_Title>", "body": "<KIX_TICKET_State>"},
		},
	}), http.StatusCreated)

	var queues struct {
		Items []struct {
			ID   uint   `json:"id"`
			Name string `json:"name"`
		} `json:"items"`
	}

	admin.must(admin.api(http.MethodGet, "/queues", nil), http.StatusOK).Data(t, &queues)

	var desk uint

	for _, queue := range queues.Items {
		if queue.Name == "Service Desk" {
			desk = queue.ID
		}
	}

	var opened struct {
		ID uint `json:"id"`
	}

	admin.must(admin.api(http.MethodPost, "/tickets", map[string]any{
		"title": "Printer", "queueId": desk, "body": "It jams.",
	}), http.StatusCreated).Data(t, &opened)

	var told notificationList

	agnes.must(agnes.api(http.MethodGet, "/me/notifications", nil), http.StatusOK).Data(t, &told)

	if len(told.Items) != 1 || told.Unread != 1 || told.Items[0].TicketID != opened.ID ||
		told.Items[0].Subject != "Neu: Printer" || told.Items[0].Body != "neu" {
		t.Fatalf("Agnes was told %+v; want the ticket opened, in German", told)
	}

	var opener notificationList

	admin.must(admin.api(http.MethodGet, "/me/notifications", nil), http.StatusOK).Data(t, &opener)

	if len(opener.Items) != 0 {
		t.Errorf("the administrator who opened the ticket was told %+v", opener.Items)
	}

	path := "/me/notifications/" + strconv.FormatUint(uint64(told.Items[0].ID), 10) + "/read"

	admin.must(admin.api(http.MethodPut, path, nil), http.StatusNotFound)
	agnes.must(agnes.api(http.MethodPut, path, nil), http.StatusOK)
	agnes.must(agnes.api(http.MethodGet, "/me/notifications", nil), http.StatusOK).Data(t, &told)

	if told.Unread != 0 || told.Items[0].ReadAt == nil {
		t.Errorf("Agnes's notification marked read reads %+v", told)
	}

	agnes.must(agnes.api(http.MethodPut, "/me/notifications/read", nil), http.StatusOK)
	agnes.must(agnes.api(http.MethodGet, "/me/export", nil), http.StatusOK)

	// Once a day is asked of the database, in each dialect's way of comparing a
	// moment.
	admin.must(admin.api(http.MethodPost, "/notification-definitions", map[string]any{
		"name": "Daily", "events": []string{"TicketTitleUpdate"}, "recipients": []string{"AgentWritePermissions"},
		"oncePerDay": true, "messages": []map[string]string{{"language": "en", "subject": "Daily", "body": "Changed."}},
	}), http.StatusCreated)

	ticket := "/tickets/" + strconv.FormatUint(uint64(opened.ID), 10)

	for _, title := range []string{"Printer jams", "Printer jams again"} {
		admin.must(admin.api(http.MethodPut, ticket, map[string]any{"title": title}), http.StatusOK)
	}

	agnes.must(agnes.api(http.MethodGet, "/me/notifications", nil), http.StatusOK).Data(t, &told)

	if len(told.Items) != 2 || told.Items[0].Subject != "Daily" {
		t.Errorf("two new titles told Agnes %+v; want the daily notification once", told.Items)
	}
}
