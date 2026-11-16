//go:build integration

package integration

import (
	"net/http"
	"strconv"
	"testing"
	"time"
)

type owedTicket struct {
	ID               uint       `json:"id"`
	ServiceLevelID   *uint      `json:"serviceLevelId"`
	CreatedAt        time.Time  `json:"createdAt"`
	FirstResponseDue *time.Time `json:"firstResponseDue"`
	FirstResponseAt  *time.Time `json:"firstResponseAt"`
	SolutionDue      *time.Time `json:"solutionDue"`
	Escalated        bool       `json:"escalated"`
}

// within says whether a moment lies the given time after another, give or take
// the second a database rounds a moment to.
func within(due *time.Time, from time.Time, after time.Duration) bool {
	if due == nil {
		return false
	}

	off := due.Sub(from.Add(after))

	return off > -time.Second && off < time.Second
}

// A ticket opened in a queue that names a service level is owed its first answer
// and its solution, over HTTP and through the database; the first answer the
// customer sees is kept as the moment it was answered - a second answer moves it
// nowhere - and putting the ticket under no level takes what it is owed away.
func TestATicketIsOwedItsServiceLevelOverHTTP(t *testing.T) {
	t.Parallel()

	a := start(t)
	admin := a.signInAsAdmin("a-much-better-password")
	desk := theDesk(t, admin)

	var gold struct {
		ID uint `json:"id"`
	}

	admin.must(admin.api(http.MethodPost, "/service-levels", map[string]any{
		"name": "Gold", "firstResponseMinutes": 60, "solutionMinutes": 480,
	}), http.StatusCreated).Data(t, &gold)

	admin.must(admin.api(http.MethodPut, "/queues/"+strconv.FormatUint(uint64(desk), 10),
		map[string]any{"serviceLevelId": gold.ID}), http.StatusOK)

	var ticket owedTicket

	admin.must(admin.api(http.MethodPost, "/tickets", map[string]any{
		"title": "Printer", "queueId": desk, "body": "It jams.",
	}), http.StatusCreated).Data(t, &ticket)

	if ticket.ServiceLevelID == nil || *ticket.ServiceLevelID != gold.ID || ticket.Escalated ||
		!within(ticket.FirstResponseDue, ticket.CreatedAt, time.Hour) ||
		!within(ticket.SolutionDue, ticket.CreatedAt, 8*time.Hour) {
		t.Fatalf("a ticket of the desk reads %+v; want it owed under Gold", ticket)
	}

	answer := func(body string) owedTicket {
		admin.must(admin.api(http.MethodPost, ticketPath(ticket.ID, "articles"), map[string]any{
			"body": body, "customerVisible": true,
		}), http.StatusCreated, http.StatusOK)

		var read owedTicket

		admin.must(admin.api(http.MethodGet, ticketPath(ticket.ID), nil), http.StatusOK).Data(t, &read)

		return read
	}

	first := answer("A new roller is on its way.")
	if first.FirstResponseAt == nil {
		t.Fatal("an answer the customer sees did not answer the ticket")
	}

	if second := answer("It is fitted."); second.FirstResponseAt == nil || !second.FirstResponseAt.Equal(*first.FirstResponseAt) {
		t.Errorf("a second answer moved the first one from %v to %v", first.FirstResponseAt, second.FirstResponseAt)
	}

	var cleared owedTicket

	admin.must(admin.api(http.MethodPut, ticketPath(ticket.ID), map[string]any{"clearServiceLevel": true}),
		http.StatusOK).Data(t, &cleared)

	if cleared.ServiceLevelID != nil || cleared.FirstResponseDue != nil || cleared.SolutionDue != nil {
		t.Errorf("under no level the ticket reads %+v", cleared)
	}
}
