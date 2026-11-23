//go:build integration

package integration

import (
	"net/http"
	"strconv"
	"testing"
	"time"
)

type ticketResponse struct {
	ID               uint       `json:"id"`
	Number           string     `json:"number"`
	Title            string     `json:"title"`
	QueueID          uint       `json:"queueId"`
	StateID          uint       `json:"stateId"`
	PriorityID       uint       `json:"priorityId"`
	OwnerID          *uint      `json:"ownerId"`
	OwnerName        string     `json:"ownerName"`
	Locked           bool       `json:"locked"`
	PendingUntil     *time.Time `json:"pendingUntil"`
	AccountedMinutes int        `json:"accountedMinutes"`
	Watching         bool       `json:"watching"`
}

type ticketList struct {
	Items      []ticketResponse `json:"items"`
	TotalCount uint             `json:"totalCount"`
}

func ticketPath(id uint, rest ...string) string {
	path := "/tickets/" + strconv.FormatUint(uint64(id), 10)
	for _, part := range rest {
		path += "/" + part
	}

	return path
}

// theDesk is the queue a fresh installation ships, which the agents work in.
func theDesk(t *testing.T, c *client) uint {
	t.Helper()

	var queues queueList

	c.must(c.api(http.MethodGet, "/queues", nil), http.StatusOK).Data(t, &queues)

	for _, queue := range queues.Items {
		if queue.Name == "Service Desk" {
			return queue.ID
		}
	}

	t.Fatal("the Service Desk is not among the queues")

	return 0
}

// openTicket opens a ticket in the queue and answers it as the API read it back.
func openTicket(t *testing.T, c *client, queue uint, title string, extra map[string]any) ticketResponse {
	t.Helper()

	body := map[string]any{"title": title, "queueId": queue, "body": "What happened, as it was told."}
	for key, value := range extra {
		body[key] = value
	}

	var ticket ticketResponse

	c.must(c.api(http.MethodPost, "/tickets", body), http.StatusCreated).Data(t, &ticket)

	return ticket
}

// A ticket is opened, read, changed, written in and watched over HTTP, with
// everything that happened in its history and the time noted on it summed.
func TestATicketIsWorkedOverHTTP(t *testing.T) {
	t.Parallel()

	a := start(t)
	admin := a.signInAsAdmin("a-much-better-password")
	desk := theDesk(t, admin)

	ticket := openTicket(t, admin, desk, "Printer jams", map[string]any{"channel": "phone", "minutes": 10})

	if ticket.Number == "" || ticket.AccountedMinutes != 10 || ticket.Watching {
		t.Errorf("the new ticket reads as %+v", ticket)
	}

	var changed ticketResponse

	admin.must(admin.api(http.MethodPut, ticketPath(ticket.ID), map[string]any{
		"title": "Printer jams on the second floor", "locked": true,
	}), http.StatusOK).Data(t, &changed)

	if !changed.Locked || changed.OwnerID == nil || changed.OwnerName == "" {
		t.Errorf("locking a ticket nobody owned left it as %+v; want it the caller's", changed)
	}

	admin.must(admin.api(http.MethodPost, ticketPath(ticket.ID, "articles"), map[string]any{
		"body": "Replaced the roller.", "minutes": 25,
	}), http.StatusCreated)

	var articles struct {
		Items []struct {
			Body string `json:"body"`
		} `json:"items"`
	}

	admin.must(admin.api(http.MethodGet, ticketPath(ticket.ID, "articles"), nil), http.StatusOK).Data(t, &articles)

	if len(articles.Items) != 2 || articles.Items[1].Body != "Replaced the roller." {
		t.Errorf("the ticket's articles read as %+v", articles.Items)
	}

	var history struct {
		Items []struct {
			Event string `json:"event"`
		} `json:"items"`
	}

	admin.must(admin.api(http.MethodGet, ticketPath(ticket.ID, "history"), nil), http.StatusOK).Data(t, &history)

	events := map[string]bool{}
	for _, entry := range history.Items {
		events[entry.Event] = true
	}

	for _, want := range []string{"created", "title", "owner", "lock", "article", "time"} {
		if !events[want] {
			t.Errorf("the history has no %q line: %v", want, events)
		}
	}

	admin.must(admin.api(http.MethodPut, ticketPath(ticket.ID, "watch"), nil), http.StatusOK)

	var read ticketResponse

	admin.must(admin.api(http.MethodGet, ticketPath(ticket.ID), nil), http.StatusOK).Data(t, &read)

	if !read.Watching || read.AccountedMinutes != 35 {
		t.Errorf("after watching and noting more time the ticket reads as %+v", read)
	}

	admin.must(admin.api(http.MethodDelete, ticketPath(ticket.ID, "watch"), nil), http.StatusNoContent)
	admin.must(admin.api(http.MethodDelete, ticketPath(ticket.ID), nil), http.StatusNoContent)

	if gone := admin.api(http.MethodGet, ticketPath(ticket.ID), nil); gone.Status != http.StatusNotFound {
		t.Errorf("the deleted ticket was answered %d, want 404", gone.Status)
	}
}

// A ticket in a queue the agent's role does not reach is not found - not refused
// - read or changed, and is not in their listing; and deleting any ticket is
// refused for the right itself, which says nothing about the ticket.
func TestATicketOutOfTheAgentsReachIsNotFound(t *testing.T) {
	t.Parallel()

	a := start(t)
	admin := a.signInAsAdmin("a-much-better-password")
	agent := a.signInAsAgent(admin, "Agent", "agent@example.com")

	var board queueResponse

	admin.must(admin.api(http.MethodPost, "/queues", map[string]any{"name": "Board"}),
		http.StatusCreated).Data(t, &board)

	hidden := openTicket(t, admin, board.ID, "Board business", nil)
	visible := openTicket(t, admin, theDesk(t, admin), "A printer", nil)

	for _, attempt := range []response{
		agent.api(http.MethodGet, ticketPath(hidden.ID), nil),
		agent.api(http.MethodPut, ticketPath(hidden.ID), map[string]any{"title": "Mine"}),
		agent.api(http.MethodGet, ticketPath(hidden.ID, "articles"), nil),
	} {
		if attempt.Status != http.StatusNotFound {
			t.Errorf("an agent reaching for a ticket outside their queues was answered %d %s, want 404",
				attempt.Status, attempt.Body)
		}
	}

	var listed ticketList

	agent.must(agent.api(http.MethodGet, "/tickets", nil), http.StatusOK).Data(t, &listed)

	if listed.TotalCount != 1 || len(listed.Items) != 1 || listed.Items[0].ID != visible.ID {
		t.Errorf("an agent lists %+v; want only the ticket in the desk", listed)
	}

	if refused := agent.api(http.MethodDelete, ticketPath(visible.ID), nil); refused.Status != http.StatusForbidden ||
		errorCode(t, refused) != "missingPermission" {
		t.Errorf("an agent deleting a ticket was answered %d %s, want 403 missingPermission", refused.Status, refused.Body)
	}
}

// A listing answers one page and the true total, and a limit above the maximum -
// or one that is not a number - is refused rather than quietly read as something
// else.
func TestATicketListingAnswersOnePageAndTheTrueTotal(t *testing.T) {
	t.Parallel()

	a := start(t)
	admin := a.signInAsAdmin("a-much-better-password")
	desk := theDesk(t, admin)

	for _, title := range []string{"One", "Two", "Three"} {
		openTicket(t, admin, desk, title, nil)
	}

	var page ticketList

	admin.must(admin.api(http.MethodGet, "/tickets?limit=2", nil), http.StatusOK).Data(t, &page)

	if page.TotalCount != 3 || len(page.Items) != 2 || page.Items[0].Title != "Three" {
		t.Errorf("the first page holds %+v; want two of three, the newest first", page)
	}

	for _, query := range []string{"?limit=201", "?limit=lots", "?page=-1"} {
		if refused := admin.api(http.MethodGet, "/tickets"+query, nil); refused.Status != http.StatusBadRequest {
			t.Errorf("listing with %s was answered %d, want 400", query, refused.Status)
		}
	}
}

// Deleting an account that owns unfinished tickets says how many first, and once
// confirmed the tickets stay and lose their owner.
func TestDeletingAnAccountThatOwnsTicketsReleasesThemOnceConfirmed(t *testing.T) {
	t.Parallel()

	a := start(t)
	admin := a.signInAsAdmin("a-much-better-password")
	agent := a.signInAsAgent(admin, "Leaving Agent", "leaving@example.com")
	desk := theDesk(t, admin)

	var me struct {
		User userResponse `json:"user"`
	}

	agent.must(agent.api(http.MethodGet, "/me", nil), http.StatusOK).Data(t, &me)

	owned := openTicket(t, admin, desk, "Owned by somebody leaving", map[string]any{"ownerId": me.User.ID})

	userPath := "/users/" + strconv.FormatUint(uint64(me.User.ID), 10)

	asked := admin.api(http.MethodDelete, userPath, nil)
	if asked.Status != http.StatusConflict || errorCode(t, asked) != "deletionNeedsConfirming" {
		t.Fatalf("deleting an account owning a ticket was answered %d %s, want 409 deletionNeedsConfirming",
			asked.Status, asked.Body)
	}

	admin.must(admin.api(http.MethodDelete, userPath+"?confirm=true", nil), http.StatusNoContent, http.StatusOK)

	var released ticketResponse

	admin.must(admin.api(http.MethodGet, ticketPath(owned.ID), nil), http.StatusOK).Data(t, &released)

	if released.OwnerID != nil || released.Locked {
		t.Errorf("the erased account's ticket reads as %+v; want it kept, without an owner and unlocked", released)
	}
}
