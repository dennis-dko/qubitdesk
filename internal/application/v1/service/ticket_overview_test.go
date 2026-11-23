package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/dennis-dko/qubitdesk/internal/application/v1/command"
	"github.com/dennis-dko/qubitdesk/internal/application/v1/service"
	"github.com/dennis-dko/qubitdesk/internal/domain/model"
)

// The overview counts, in the queues the agent reaches and nowhere else, the
// unfinished tickets that are theirs - owned, theirs to answer for, watched -
// what has run out unkept, each queue's unfinished tickets by where they
// stand, and the tickets opened and finished on each of the last fourteen days
// of the agent's calendar, today last.
func TestTheOverviewCountsWhatIsTheAgentsAndWhatWaits(t *testing.T) {
	ctx := context.Background()
	f := newTicketFixture(t)
	opened := time.Date(2026, time.November, 20, 9, 0, 0, 0, time.UTC)
	me := f.agentUser.ID

	gold, err := f.levels.Save(ctx, &model.ServiceLevel{Name: "Gold", FirstResponseMinutes: 60, Valid: true})
	if err != nil {
		t.Fatal(err)
	}

	f.tickets.WithClock(func() time.Time { return opened })

	owned := f.open(t, f.agent, f.desk, "Owned")
	if _, err := f.tickets.UpdateTicket(ctx, f.agent, owned.ID, command.UpdateTicketCommand{
		OwnerID: &me, ResponsibleID: &me,
	}); err != nil {
		t.Fatal(err)
	}

	watched := f.open(t, f.agent, f.desk, "Watched")
	if err := f.tickets.SetWatching(ctx, f.agent, watched.ID, true); err != nil {
		t.Fatal(err)
	}

	waiting := f.open(t, f.agent, f.desk, "Waiting")
	until := opened.Add(48 * time.Hour)
	if _, err := f.tickets.UpdateTicket(ctx, f.agent, waiting.ID, command.UpdateTicketCommand{
		StateID: &f.states["pending-reminder"].ID, PendingUntil: &until,
	}); err != nil {
		t.Fatal(err)
	}

	finished := f.open(t, f.agent, f.desk, "Finished")
	if _, err := f.tickets.UpdateTicket(ctx, f.agent, finished.ID, command.UpdateTicketCommand{
		StateID: &f.states["closed"].ID, OwnerID: &me,
	}); err != nil {
		t.Fatal(err)
	}

	f.desk.ServiceLevelID = &gold.ID
	if _, err := f.queues.Update(ctx, f.desk); err != nil {
		t.Fatal(err)
	}

	f.open(t, f.agent, f.desk, "Owed an answer")
	f.open(t, f.admin, f.hidden, "In a queue the agent does not reach")

	// Escalated in its own record once its hour has passed, and finished, so
	// nothing anybody could still do.
	unanswered := f.open(t, f.agent, f.desk, "Finished unanswered")
	if _, err := f.tickets.UpdateTicket(ctx, f.agent, unanswered.ID, command.UpdateTicketCommand{
		StateID: &f.states["closed"].ID,
	}); err != nil {
		t.Fatal(err)
	}

	f.tickets.WithClock(func() time.Time { return opened.Add(2 * time.Hour) })

	overview, err := f.tickets.Overview(ctx, f.agent)
	if err != nil {
		t.Fatal(err)
	}

	if overview.Owned != 1 || overview.Responsible != 1 || overview.Watched != 1 || overview.Overdue != 1 {
		t.Errorf("the agent's own work counts owned %d, responsible %d, watched %d, overdue %d; want one each",
			overview.Owned, overview.Responsible, overview.Watched, overview.Overdue)
	}

	want := map[uint]service.QueueLoad{
		f.desk.ID:     {QueueID: f.desk.ID, New: 3, Waiting: 1, Overdue: 1},
		f.readOnly.ID: {QueueID: f.readOnly.ID},
	}

	if len(overview.Queues) != len(want) {
		t.Errorf("the overview lists %d queues, want the %d the agent reaches", len(overview.Queues), len(want))
	}

	for _, load := range overview.Queues {
		if load != want[load.QueueID] {
			t.Errorf("queue %d counts %+v, want %+v", load.QueueID, load, want[load.QueueID])
		}
	}

	if len(overview.Days) != service.OverviewDays {
		t.Fatalf("the overview counts %d days, want %d", len(overview.Days), service.OverviewDays)
	}

	today := overview.Days[service.OverviewDays-1]
	if !today.Day.Equal(model.CalendarDay(opened)) || today.Opened != 6 || today.Closed != 2 {
		t.Errorf("today counts %+v; want the 20th with the six tickets the agent reaches opened and two finished",
			today)
	}

	if first := overview.Days[0]; !first.Day.Equal(model.CalendarDay(opened.AddDate(0, 0, 1-service.OverviewDays))) ||
		first.Opened != 0 || first.Closed != 0 {
		t.Errorf("the first day counts %+v, want the 7th with nothing on it", first)
	}
}

// The overview's days are the agent's own: a ticket opened at half past
// eleven in the morning UTC on the 20th is counted on the 21st in Auckland,
// where that is already the next day, and that day is the overview's last.
func TestTheOverviewCountsDaysOnTheAgentsCalendar(t *testing.T) {
	ctx := context.Background()
	f := newTicketFixture(t)
	opened := time.Date(2026, time.November, 20, 11, 30, 0, 0, time.UTC)

	f.tickets.WithClock(func() time.Time { return opened })
	f.open(t, f.agent, f.desk, "Opened at half past eleven")

	auckland := *f.agentUser
	auckland.Timezone = "Pacific/Auckland"

	overview, err := f.tickets.Overview(ctx, &service.Principal{
		User: &auckland, Role: f.agent.Role, Permissions: f.agent.Permissions,
	})
	if err != nil {
		t.Fatal(err)
	}

	today := overview.Days[service.OverviewDays-1]
	if !today.Day.Equal(time.Date(2026, time.November, 21, 0, 0, 0, 0, time.UTC)) || today.Opened != 1 {
		t.Errorf("in Auckland the last day counts %+v, want the 21st with the ticket opened on it", today)
	}
}

// An installation without a waiting state counts nothing as waiting. The
// states are the installation's to delete, and a list of states left empty
// asks for every state - which counted every ticket in the queue as waiting.
func TestTheOverviewCountsNothingInAStateTheInstallationHasNone(t *testing.T) {
	ctx := context.Background()
	f := newTicketFixture(t)

	for _, name := range []string{"pending-reminder", "pending-auto-close"} {
		if err := f.stateRepo.Delete(ctx, f.states[name].ID); err != nil {
			t.Fatal(err)
		}
	}

	f.open(t, f.agent, f.desk, "Printer")
	f.open(t, f.agent, f.desk, "Paper")

	overview, err := f.tickets.Overview(ctx, f.agent)
	if err != nil {
		t.Fatal(err)
	}

	for _, load := range overview.Queues {
		if load.QueueID == f.desk.ID && (load.New != 2 || load.Waiting != 0) {
			t.Errorf("without a waiting state the desk counts %+v, want two new and none waiting", load)
		}
	}
}
