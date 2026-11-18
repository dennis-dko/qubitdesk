//go:build integration

package integration

import (
	"net/http"
	"strconv"
	"testing"
)

type portalTicket struct {
	ID          uint   `json:"id"`
	Number      string `json:"number"`
	Title       string `json:"title"`
	State       string `json:"state"`
	StateType   string `json:"stateType"`
	ContactName string `json:"contactName"`
}

type portalArticle struct {
	ID          uint                 `json:"id"`
	TicketID    uint                 `json:"ticketId"`
	Sender      string               `json:"sender"`
	AuthorName  string               `json:"authorName"`
	Body        string               `json:"body"`
	Attachments []attachmentResponse `json:"attachments"`
}

// portalCustomer gives the contact the portal from the agent's side, and signs
// in as the account it made, past its initial password.
func portalCustomer(t *testing.T, a *app, agent *client, contact uint, email string) *client {
	t.Helper()

	agent.must(agent.api(http.MethodPut, path("/contacts/", contact, "/portal-access"),
		map[string]any{"password": "initial-password-1"}), http.StatusOK)

	first := a.newClient()
	first.signIn(email, "initial-password-1")
	first.must(first.api(http.MethodPut, "/me/password", map[string]string{
		"currentPassword": "initial-password-1", "newPassword": "the-customers-own",
	}), http.StatusOK)

	customer := a.newClient()
	customer.signIn(email, "the-customers-own")

	return customer
}

// A customer opens a ticket in the portal and follows it there, on every
// database: in the queue a fresh installation offers, for their contact; the
// agent's answer is shown to them with its file, and the internal note beside it
// with its file is not; their answer opens the closed ticket again, and their
// file goes with their own words only; their colleague's ticket is theirs to see
// once the contact is said to see the organisation's; and nothing of the staff
// side opens to them.
func TestACustomerFollowsTheirTicketsInThePortalOverHTTP(t *testing.T) {
	t.Parallel()

	a, admin, agent := startWithWorker(t)
	desk := theDesk(t, admin)

	var acme, carl, cleo struct {
		ID uint `json:"id"`
	}

	agent.must(agent.api(http.MethodPost, "/organisations", map[string]any{"name": "Acme"}),
		http.StatusCreated, http.StatusOK).Data(t, &acme)
	agent.must(agent.api(http.MethodPost, "/contacts", map[string]any{
		"name": "Carl", "email": "carl@example.com", "organisationId": acme.ID,
	}), http.StatusCreated, http.StatusOK).Data(t, &carl)
	agent.must(agent.api(http.MethodPost, "/contacts", map[string]any{
		"name": "Cleo", "email": "cleo@example.com", "organisationId": acme.ID,
	}), http.StatusCreated, http.StatusOK).Data(t, &cleo)

	customer := portalCustomer(t, a, agent, carl.ID, "carl@example.com")

	var queues struct {
		Items []struct {
			ID   uint   `json:"id"`
			Name string `json:"name"`
		} `json:"items"`
	}

	customer.must(customer.api(http.MethodGet, "/portal/queues", nil), http.StatusOK).Data(t, &queues)

	if len(queues.Items) != 1 || queues.Items[0].ID != desk {
		t.Fatalf("the portal offers %+v; want the Service Desk a fresh installation ships", queues.Items)
	}

	var opened portalTicket

	customer.must(customer.api(http.MethodPost, "/portal/tickets", map[string]any{
		"title": "Screen flickers", "body": "Since this morning.",
	}), http.StatusCreated).Data(t, &opened)

	if opened.Number == "" || opened.State != "new" || opened.ContactName != "Carl" {
		t.Errorf("the ticket opened reads %+v; want it numbered, new and for Carl", opened)
	}

	ticket := strconv.FormatUint(uint64(opened.ID), 10)

	var answer, note struct {
		ID uint `json:"id"`
	}

	agent.must(agent.api(http.MethodPost, "/tickets/"+ticket+"/articles", map[string]any{
		"body": "Please try another cable.", "customerVisible": true,
	}), http.StatusCreated).Data(t, &answer)
	agent.must(agent.api(http.MethodPost, "/tickets/"+ticket+"/articles", map[string]any{
		"body": "Probably the graphics card.",
	}), http.StatusCreated).Data(t, &note)

	var shown, internal attachmentResponse

	agent.must(agent.upload(path("/tickets/", opened.ID, "/articles/", answer.ID, "/attachments"), "file",
		"cable.txt", []byte("how to"), nil), http.StatusCreated).Data(t, &shown)
	agent.must(agent.upload(path("/tickets/", opened.ID, "/articles/", note.ID, "/attachments"), "file",
		"card.txt", []byte("suspicion"), nil), http.StatusCreated).Data(t, &internal)

	var articles struct {
		Items []portalArticle `json:"items"`
	}

	customer.must(customer.api(http.MethodGet, "/portal/tickets/"+ticket+"/articles", nil), http.StatusOK).
		Data(t, &articles)

	if len(articles.Items) != 2 || articles.Items[0].Sender != "customer" || articles.Items[1].ID != answer.ID ||
		articles.Items[1].AuthorName != "Wera" || len(articles.Items[1].Attachments) != 1 {
		t.Fatalf("Carl is shown %+v; want his words and the agent's answer with its file, and no note", articles.Items)
	}

	if got := customer.api(http.MethodGet, path("/portal/tickets/", opened.ID, "/attachments/", shown.ID), nil); got.Status != http.StatusOK ||
		string(got.Body) != "how to" {
		t.Errorf("the answer's file answered %d %q", got.Status, got.Body)
	}

	if got := customer.api(http.MethodGet, path("/portal/tickets/", opened.ID, "/attachments/", internal.ID), nil); got.Status != http.StatusNotFound {
		t.Errorf("the internal note's file answered %d, want 404", got.Status)
	}

	var states catalogList

	admin.must(admin.api(http.MethodGet, "/ticket-states", nil), http.StatusOK).Data(t, &states)

	named := map[string]uint{}
	for _, state := range states.Items {
		named[state.Name] = state.ID
	}

	agent.must(agent.api(http.MethodPut, "/tickets/"+ticket, map[string]any{"stateId": named["closed"]}), http.StatusOK)

	var reply portalArticle

	customer.must(customer.api(http.MethodPost, "/portal/tickets/"+ticket+"/articles", map[string]any{
		"body": "Another cable did not help.",
	}), http.StatusCreated).Data(t, &reply)

	customer.must(customer.api(http.MethodGet, "/portal/tickets/"+ticket, nil), http.StatusOK).Data(t, &opened)

	if reply.TicketID != opened.ID || opened.State != "open" {
		t.Errorf("the answer landed in %d and left the ticket %q; want it in the same ticket, open again",
			reply.TicketID, opened.State)
	}

	customer.must(customer.upload(path("/portal/tickets/", opened.ID, "/articles/", reply.ID, "/attachments"), "file",
		"photo.jpg", []byte("jpeg"), nil), http.StatusCreated)

	if got := customer.upload(path("/portal/tickets/", opened.ID, "/articles/", answer.ID, "/attachments"), "file",
		"photo.jpg", []byte("jpeg"), nil); got.Status != http.StatusNotFound {
		t.Errorf("a file for the agent's answer answered %d, want 404", got.Status)
	}

	colleague := openTicket(t, agent, desk, "Badge lost", map[string]any{"contactId": cleo.ID})

	if got := customer.api(http.MethodGet, path("/portal/tickets/", colleague.ID), nil); got.Status != http.StatusNotFound {
		t.Errorf("Cleo's ticket answered Carl %d, want 404", got.Status)
	}

	agent.must(agent.api(http.MethodPut, path("/contacts/", carl.ID), map[string]any{"seesOrganisation": true}),
		http.StatusOK)

	var mine struct {
		Items      []portalTicket `json:"items"`
		TotalCount uint           `json:"totalCount"`
	}

	customer.must(customer.api(http.MethodGet, "/portal/tickets", nil), http.StatusOK).Data(t, &mine)

	if mine.TotalCount != 2 {
		t.Errorf("Carl, seeing Acme's tickets, follows %+v", mine.Items)
	}

	for _, staff := range []string{"/tickets", "/contacts", "/queues", path("/tickets/", opened.ID)} {
		if got := customer.api(http.MethodGet, staff, nil); got.Status != http.StatusForbidden {
			t.Errorf("GET %s answered the customer %d, want 403", staff, got.Status)
		}
	}
}
