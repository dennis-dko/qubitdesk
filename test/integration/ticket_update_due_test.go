//go:build integration

package integration

import (
	"net/http"
	"testing"
	"time"
)

// A ticket is owed its next answer over HTTP and through the database, on every
// database: from its opening; from the customer's answer in the portal, and
// still from it when they write again; none while it waits; and from that first
// answer again once their next one opens the ticket.
func TestTheNextAnswerIsOwedOverHTTP(t *testing.T) {
	t.Parallel()

	a, admin, agent := startWithWorker(t)
	desk := theDesk(t, admin)

	var gold struct {
		ID uint `json:"id"`
	}

	admin.must(admin.api(http.MethodPost, "/service-levels", map[string]any{"name": "Gold", "updateMinutes": 120}),
		http.StatusCreated, http.StatusOK).Data(t, &gold)
	admin.must(admin.api(http.MethodPut, path("/queues/", desk), map[string]any{"serviceLevelId": gold.ID}),
		http.StatusOK)

	var carl struct {
		ID uint `json:"id"`
	}

	agent.must(agent.api(http.MethodPost, "/contacts", map[string]any{"name": "Carl", "email": "carl@example.com"}),
		http.StatusCreated, http.StatusOK).Data(t, &carl)

	customer := portalCustomer(t, a, agent, carl.ID, "carl@example.com")
	opened := openTicket(t, agent, desk, "Printer", map[string]any{"contactId": carl.ID})

	read := func() (due *time.Time, created time.Time) {
		t.Helper()

		var ticket struct {
			UpdateDue *time.Time `json:"updateDue"`
			CreatedAt time.Time  `json:"createdAt"`
		}

		agent.must(agent.api(http.MethodGet, path("/tickets/", opened.ID), nil), http.StatusOK).Data(t, &ticket)

		return ticket.UpdateDue, ticket.CreatedAt
	}

	lastWritten := func() time.Time {
		t.Helper()

		var articles struct {
			Items []struct {
				CreatedAt time.Time `json:"createdAt"`
			} `json:"items"`
		}

		agent.must(agent.api(http.MethodGet, path("/tickets/", opened.ID, "/articles"), nil), http.StatusOK).
			Data(t, &articles)

		return articles.Items[len(articles.Items)-1].CreatedAt
	}

	if due, created := read(); !within(due, created, 2*time.Hour) {
		t.Errorf("the ticket opened owes its next answer by %v, want two hours after its opening %v", due, created)
	}

	answer := func(body string) {
		t.Helper()

		customer.must(customer.api(http.MethodPost, path("/portal/tickets/", opened.ID, "/articles"),
			map[string]any{"body": body}), http.StatusCreated)
	}

	answer("It is still jammed.")
	first := lastWritten()

	if due, _ := read(); !within(due, first, 2*time.Hour) {
		t.Errorf("after the customer's answer the next one is owed by %v, want two hours after %v", due, first)
	}

	answer("Really jammed.")

	if due, _ := read(); !within(due, first, 2*time.Hour) {
		t.Errorf("writing again moved what is owed to %v, want it still two hours after %v", due, first)
	}

	var states catalogList

	admin.must(admin.api(http.MethodGet, "/ticket-states", nil), http.StatusOK).Data(t, &states)

	for _, state := range states.Items {
		if state.Name == "pending-reminder" {
			agent.must(agent.api(http.MethodPut, path("/tickets/", opened.ID), map[string]any{
				"stateId": state.ID, "pendingUntil": time.Now().Add(48 * time.Hour).UTC().Format(time.RFC3339),
			}), http.StatusOK)
		}
	}

	if due, _ := read(); due != nil {
		t.Errorf("the waiting ticket owes its next answer by %v, want none", due)
	}

	answer("Any news?")

	if due, _ := read(); !within(due, first, 2*time.Hour) {
		t.Errorf("once the customer's answer opened it, the next one is owed by %v, want two hours after %v", due, first)
	}
}
