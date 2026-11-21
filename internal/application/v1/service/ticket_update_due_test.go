package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/dennis-dko/qubitdesk/internal/application/v1/command"
	"github.com/dennis-dko/qubitdesk/internal/domain/model"
)

// A ticket is owed its next answer by the time between answers its service
// level promises, counted as OTRS counts it: from the opening, then from each
// answer the customer sees, then from the first of what the customer wrote
// since - not from their second, and not moved by a note nobody outside saw. A
// ticket put to wait or finished owes none, one opened again by the customer's
// answer owes one again, and one the housekeeping moves on into an open state
// owes one as well. The deadline gone by escalates the ticket.
func TestTheNextAnswerIsOwedAsTheServiceLevelPromises(t *testing.T) {
	ctx := context.Background()
	f := newPortalTickets(t)

	// A minute on with every reading, so that no two things written fall on one
	// moment - the wall's clock moves in ticks, and two writes in one tick
	// would make "counted from this one" and "from that one" the same answer.
	clock := time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC)
	f.tickets.WithClock(func() time.Time {
		clock = clock.Add(time.Minute)

		return clock
	})

	gold, err := f.levels.Save(ctx, &model.ServiceLevel{Name: "Gold", UpdateMinutes: 120, Valid: true})
	if err != nil {
		t.Fatal(err)
	}

	f.desk.ServiceLevelID = &gold.ID
	if _, err := f.queues.Update(ctx, f.desk); err != nil {
		t.Fatal(err)
	}

	owed := func(ticket *model.Ticket) *time.Time {
		t.Helper()

		read, err := f.repo.GetByID(ctx, ticket.ID)
		if err != nil {
			t.Fatal(err)
		}

		return read.UpdateDue
	}

	latest := func(ticket *model.Ticket) time.Time {
		t.Helper()

		articles, _ := f.repo.Articles(ctx, ticket.ID)

		return articles[len(articles)-1].CreatedAt
	}

	expect := func(what string, ticket *model.Ticket, from time.Time) {
		t.Helper()

		if due := owed(ticket); due == nil || !due.Equal(from.Add(2*time.Hour)) {
			t.Errorf("%s: the next answer is owed by %v, want two hours after %v", what, due, from)
		}
	}

	expectNone := func(what string, ticket *model.Ticket) {
		t.Helper()

		if due := owed(ticket); due != nil {
			t.Errorf("%s: the next answer is owed by %v, want none", what, due)
		}
	}

	ticket := f.forContact(t, f.carl, "Printer jammed")
	expect("opened with a note nobody outside saw", ticket, ticket.CreatedAt)

	answer := func(body string) {
		t.Helper()

		if _, err := f.tickets.AddArticle(ctx, f.agent, ticket.ID, command.AddArticleCommand{
			Body: ptr(body), CustomerVisible: true,
		}); err != nil {
			t.Fatal(err)
		}
	}

	write := func(body string) {
		t.Helper()

		if _, err := f.tickets.AnswerFromPortal(ctx, f.customer, ticket.ID, command.PortalAnswerCommand{
			Body: ptr(body),
		}); err != nil {
			t.Fatal(err)
		}
	}

	answer("Please try another cable.")
	expect("the agent answered", ticket, latest(ticket))

	write("Another cable did not help.")
	firstOfRun := latest(ticket)
	expect("the customer wrote", ticket, firstOfRun)

	write("Still dark.")
	expect("the customer wrote again", ticket, firstOfRun)

	if _, err := f.tickets.AddArticle(ctx, f.agent, ticket.ID, command.AddArticleCommand{
		Body: ptr("Probably the graphics card."),
	}); err != nil {
		t.Fatal(err)
	}

	expect("an internal note after the customer", ticket, firstOfRun)

	later := clock.Add(48 * time.Hour)
	f.setState(t, ticket, "pending-reminder", &later)
	expectNone("put to wait", ticket)

	write("Any news?")
	expect("the customer's answer opened it again", ticket, firstOfRun)

	if read, _ := f.repo.GetByID(ctx, ticket.ID); !read.Escalated(firstOfRun.Add(2*time.Hour + time.Minute)) {
		t.Error("the ticket is not escalated once its next answer is overdue")
	}

	answer("A new card is fitted.")
	expect("the agent answered again", ticket, latest(ticket))

	f.setState(t, ticket, "closed", nil)
	expectNone("closed", ticket)

	reopening := f.waitingState(t, "pending-open", f.states["open"])
	moving := f.forContact(t, f.carl, "Keyboard")
	soon := clock.Add(time.Hour)

	if _, err := f.tickets.UpdateTicket(ctx, f.agent, moving.ID, command.UpdateTicketCommand{
		StateID: &reopening.ID, PendingUntil: &soon,
	}); err != nil {
		t.Fatal(err)
	}

	expectNone("waiting to move on", moving)

	if _, _, err := f.tickets.Housekeep(ctx, soon.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	expect("moved on into an open state by the housekeeping", moving, moving.CreatedAt)
}
