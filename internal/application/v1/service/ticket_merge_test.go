package service_test

import (
	"context"
	"testing"

	"github.com/dennis-dko/qubitdesk/internal/application/v1/command"
	"github.com/dennis-dko/qubitdesk/internal/domain/model"
	"github.com/dennis-dko/qubitdesk/internal/support/apperror"
)

// A ticket merged into another hands it its articles, the time noted on it and
// its watchers, and is left merged, unlocked and naming the other, with a line
// saying so in the history of both.
func TestAMergedTicketHandsItsArticlesTimeAndWatchersToTheOther(t *testing.T) {
	ctx := context.Background()
	f := newTicketFixture(t)
	printer := f.open(t, f.agent, f.desk, "Printer jams")

	again, err := f.tickets.CreateTicket(ctx, f.agent, command.CreateTicketCommand{
		Title: ptr("Printer jams again"), QueueID: &f.desk.ID, Body: ptr("The same printer."), Minutes: 10,
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := f.tickets.SetWatching(ctx, f.agent, again.ID, true); err != nil {
		t.Fatal(err)
	}

	if _, err := f.tickets.UpdateTicket(ctx, f.agent, again.ID,
		command.UpdateTicketCommand{Locked: ptr(true)}); err != nil {
		t.Fatal(err)
	}

	merged, err := f.tickets.MergeTicket(ctx, f.agent, again.ID, command.MergeTicketCommand{IntoID: printer.ID})
	if err != nil {
		t.Fatalf("merging the ticket: %v", err)
	}

	if merged.StateID != f.states["merged"].ID || merged.MergedInto == nil || *merged.MergedInto != printer.ID ||
		merged.Locked || merged.ClosedAt == nil {
		t.Errorf("the merged ticket reads %+v; want it merged into the printer's, unlocked and finished", merged)
	}

	if articles, _ := f.tickets.Articles(ctx, f.agent, printer.ID); len(articles) != 2 {
		t.Errorf("the printer's ticket holds %d articles after the merge, want both tickets' two", len(articles))
	}

	if left, _ := f.tickets.Articles(ctx, f.agent, again.ID); len(left) != 0 {
		t.Errorf("the merged ticket still holds %d articles", len(left))
	}

	if minutes, _ := f.tickets.AccountedMinutes(ctx, f.agent, printer.ID); minutes != 10 {
		t.Errorf("the printer's ticket accounts %d minutes after the merge, want the merged ticket's ten", minutes)
	}

	if watching, _ := f.repo.Watching(ctx, printer.ID, f.agentUser.ID); !watching {
		t.Error("the merged ticket's watcher does not watch the ticket it went into")
	}

	written := func(id uint) map[model.HistoryEvent]bool {
		events := map[model.HistoryEvent]bool{}
		history, _ := f.tickets.History(ctx, f.agent, id)

		for _, entry := range history {
			events[entry.Event] = true
		}

		return events
	}

	if events := written(again.ID); !events[model.HistoryMerged] || !events[model.HistoryState] ||
		!events[model.HistoryLock] {
		t.Errorf("the merged ticket's history holds %v; want the merge, the state and the lock", events)
	}

	if events := written(printer.ID); !events[model.HistoryMerged] {
		t.Errorf("the printer's history holds %v; want the merge", events)
	}
}

// A ticket is not merged into itself, nor twice, nor into one merged already,
// nor by somebody who only reads one of the two.
func TestATicketIsMergedOnceAndOnlyWhereTheCallerWorksBoth(t *testing.T) {
	ctx := context.Background()
	f := newTicketFixture(t)
	printer := f.open(t, f.agent, f.desk, "Printer jams")
	again := f.open(t, f.agent, f.desk, "Printer jams again")
	third := f.open(t, f.agent, f.desk, "Printer jams once more")
	invoice := f.open(t, f.admin, f.readOnly, "An invoice")

	if _, err := f.tickets.MergeTicket(ctx, f.agent, printer.ID,
		command.MergeTicketCommand{IntoID: printer.ID}); apperror.KindOf(err) != apperror.KindInvalid {
		t.Errorf("a ticket merged into itself was answered %v, want it refused", err)
	}

	if _, err := f.tickets.MergeTicket(ctx, f.agent, again.ID,
		command.MergeTicketCommand{IntoID: invoice.ID}); apperror.KindOf(err) != apperror.KindNotFound {
		t.Errorf("a merge into a ticket of a queue only read was answered %v, want not found", err)
	}

	if _, err := f.tickets.MergeTicket(ctx, f.agent, again.ID,
		command.MergeTicketCommand{IntoID: printer.ID}); err != nil {
		t.Fatal(err)
	}

	for name, merge := range map[string]struct{ ticket, into uint }{
		"the merged ticket merged again":    {again.ID, third.ID},
		"a ticket merged into a merged one": {third.ID, again.ID},
	} {
		_, err := f.tickets.MergeTicket(ctx, f.agent, merge.ticket, command.MergeTicketCommand{IntoID: merge.into})
		if detail, ok := apperror.Detail(err); !ok || detail.Code != "ticketMergedAlready" {
			t.Errorf("%s was answered %v, want ticketMergedAlready", name, err)
		}
	}
}
