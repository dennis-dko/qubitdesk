package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/dennis-dko/go-ticket-system/internal/application/v1/command"
	"github.com/dennis-dko/go-ticket-system/internal/application/v1/service"
	"github.com/dennis-dko/go-ticket-system/internal/domain/model"
	"github.com/dennis-dko/go-ticket-system/internal/domain/repository"
	"github.com/dennis-dko/go-ticket-system/internal/support/apperror"
)

// meddled is a ticket repository through which somebody changes every ticket
// the housekeeping finds, after it has found it and before it writes.
type meddled struct {
	repository.TicketRepository
	meddle func(found *model.Ticket)
}

func (m meddled) PendingDue(ctx context.Context, stateIDs []uint, now time.Time) ([]*model.Ticket, error) {
	found, err := m.TicketRepository.PendingDue(ctx, stateIDs, now)
	for _, ticket := range found {
		m.meddle(ticket)
	}

	return found, err
}

func (m meddled) LockedTickets(ctx context.Context) ([]*model.Ticket, error) {
	found, err := m.TicketRepository.LockedTickets(ctx)
	for _, ticket := range found {
		m.meddle(ticket)
	}

	return found, err
}

// A ticket waiting to close by itself moves on once its moment has come - to the
// state the system configuration names, or to closed where that names one that
// waits itself - and not before, written into its history by the application.
func TestATicketWhoseWaitIsOverMovesOn(t *testing.T) {
	ctx := context.Background()
	f := newTicketFixture(t)
	settings := newStubSettings()
	config := service.NewSysConfigService(settings)
	f.tickets.WithConfig(config)

	now := time.Now().UTC()

	waiting := func(title string) *model.Ticket {
		ticket := f.open(t, f.agent, f.desk, title)

		moment := now.Add(time.Hour)
		waits, err := f.tickets.UpdateTicket(ctx, f.agent, ticket.ID, command.UpdateTicketCommand{
			StateID: &f.states["pending-auto-close"].ID, PendingUntil: &moment,
		})
		if err != nil {
			t.Fatal(err)
		}

		return waits
	}

	printer := waiting("Printer")

	if moved, _, err := f.tickets.Housekeep(ctx, now); err != nil || moved != 0 {
		t.Fatalf("before its moment the housekeeping moved %d, %v", moved, err)
	}

	moved, _, err := f.tickets.Housekeep(ctx, now.Add(2*time.Hour))
	if err != nil || moved != 1 {
		t.Fatalf("after its moment the housekeeping moved %d, %v; want the printer", moved, err)
	}

	closed, _ := f.tickets.GetTicket(ctx, f.agent, printer.ID)
	if closed.StateID != f.states["closed"].ID || closed.ClosedAt == nil || closed.PendingUntil != nil {
		t.Errorf("the printer was left as %+v; want it closed, with no moment to wait for", closed)
	}

	history, _ := f.tickets.History(ctx, f.agent, printer.ID)
	if last := history[len(history)-1]; last.Event != model.HistoryState || last.Actor != model.SenderSystem || last.NewValue != "closed" {
		t.Errorf("the printer's history ends with %+v; want the application closing it", last)
	}

	if _, err := config.Set(ctx, model.ConfigTicketStateAfterPending, "open"); err != nil {
		t.Fatal(err)
	}

	scanner := waiting("Scanner")
	if _, _, err := f.tickets.Housekeep(ctx, now.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}

	if opened, _ := f.tickets.GetTicket(ctx, f.agent, scanner.ID); opened.StateID != f.states["open"].ID {
		t.Errorf("with open named the scanner went to state %d, want open", opened.StateID)
	}

	if _, err := config.Set(ctx, model.ConfigTicketStateAfterPending, "pending-reminder"); err != nil {
		t.Fatal(err)
	}

	monitor := waiting("Monitor")
	if _, _, err := f.tickets.Housekeep(ctx, now.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}

	if fallen, _ := f.tickets.GetTicket(ctx, f.agent, monitor.ID); fallen.StateID != f.states["closed"].ID {
		t.Errorf("with a waiting state named the monitor went to state %d, want closed", fallen.StateID)
	}
}

// A lock that has run as long as the queue allows is freed, for a ticket that
// is new or open; a queue allowing nothing frees nothing, and a ticket that
// waits keeps its owner.
func TestALockThatHasRunItsTimeIsFreed(t *testing.T) {
	ctx := context.Background()
	f := newTicketFixture(t)
	now := time.Now().UTC()
	locked := true

	lockedIn := func(title string) *model.Ticket {
		ticket := f.open(t, f.agent, f.desk, title)

		ticket, err := f.tickets.UpdateTicket(ctx, f.agent, ticket.ID, command.UpdateTicketCommand{Locked: &locked})
		if err != nil {
			t.Fatal(err)
		}

		return ticket
	}

	idle := lockedIn("Printer")

	if _, freed, err := f.tickets.Housekeep(ctx, now.Add(time.Hour)); err != nil || freed != 0 {
		t.Errorf("a queue allowing nothing freed %d locks, %v", freed, err)
	}

	f.desk.UnlockMinutes = 30
	if _, err := f.queues.Update(ctx, f.desk); err != nil {
		t.Fatal(err)
	}

	moment := now.Add(3 * time.Hour)
	waits := lockedIn("Scanner")
	if _, err := f.tickets.UpdateTicket(ctx, f.agent, waits.ID, command.UpdateTicketCommand{
		StateID: &f.states["pending-reminder"].ID, PendingUntil: &moment,
	}); err != nil {
		t.Fatal(err)
	}

	if _, freed, err := f.tickets.Housekeep(ctx, now.Add(10*time.Minute)); err != nil || freed != 0 {
		t.Errorf("ten minutes in, the housekeeping freed %d locks, %v", freed, err)
	}

	_, freed, err := f.tickets.Housekeep(ctx, now.Add(time.Hour))
	if err != nil || freed != 1 {
		t.Fatalf("an hour in, the housekeeping freed %d locks, %v; want the printer's alone", freed, err)
	}

	if free, _ := f.tickets.GetTicket(ctx, f.agent, idle.ID); free.Locked || free.LockedAt != nil {
		t.Errorf("the printer was left as %+v; want it unlocked", free)
	}

	if kept, _ := f.tickets.GetTicket(ctx, f.agent, waits.ID); !kept.Locked {
		t.Error("a ticket that waits lost its lock")
	}

	history, _ := f.tickets.History(ctx, f.agent, idle.ID)
	if last := history[len(history)-1]; last.Event != model.HistoryLock || last.Actor != model.SenderSystem || last.NewValue != "false" {
		t.Errorf("the printer's history ends with %+v; want the application freeing it", last)
	}
}

// Whoever changes a ticket while the housekeeping is at work has the last word:
// a wait made longer is waited out, a lock taken again or a ticket put to wait
// keeps its lock, and what the housekeeping does write leaves everything else
// as they left it.
func TestTheHousekeepingLeavesWhatSomebodyChangedMeanwhile(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	at := now.Add(2 * time.Hour)

	// stage opens a ticket waiting to close an hour from now and one locked, in
	// a queue that frees a lock after half an hour - and the housekeeping over
	// tables in which somebody changes the ticket named, the moment it is found.
	stage := func(t *testing.T, target string, change func(ticketFixture, *model.Ticket)) (ticketFixture, map[string]*model.Ticket, *service.TicketApplicationService) {
		t.Helper()

		f := newTicketFixture(t)
		f.desk.UnlockMinutes = 30

		if _, err := f.queues.Update(ctx, f.desk); err != nil {
			t.Fatal(err)
		}

		later, locked := now.Add(time.Hour), true
		opened := map[string]*model.Ticket{}

		for title, cmd := range map[string]command.UpdateTicketCommand{
			"Printer": {StateID: &f.states["pending-auto-close"].ID, PendingUntil: &later},
			"Scanner": {Locked: &locked},
		} {
			ticket := f.open(t, f.agent, f.desk, title)

			changed, err := f.tickets.UpdateTicket(ctx, f.agent, ticket.ID, cmd)
			if err != nil {
				t.Fatal(err)
			}

			opened[title] = changed
		}

		housekeeping := f.serviceOver(meddled{TicketRepository: f.repo, meddle: func(found *model.Ticket) {
			if found.ID != opened[target].ID {
				return
			}

			current, err := f.repo.GetByID(ctx, found.ID)
			if err != nil {
				t.Fatal(err)
			}

			change(f, current)
			current.UpdatedAt = at

			if _, err := f.repo.Update(ctx, current, nil); err != nil {
				t.Fatal(err)
			}
		}})

		return f, opened, housekeeping
	}

	t.Run("a wait made longer", func(t *testing.T) {
		longer := at.Add(time.Hour)
		f, opened, housekeeping := stage(t, "Printer", func(_ ticketFixture, ticket *model.Ticket) { ticket.PendingUntil = &longer })

		moved, _, err := housekeeping.Housekeep(ctx, at)

		read, _ := f.tickets.GetTicket(ctx, f.agent, opened["Printer"].ID)
		if err != nil || moved != 0 || read.StateID != f.states["pending-auto-close"].ID ||
			read.PendingUntil == nil || !read.PendingUntil.Equal(longer) {
			t.Errorf("a wait made longer meanwhile: moved %d, %v, and the printer reads %+v", moved, err, read)
		}
	})

	t.Run("a ticket renamed while its wait ends", func(t *testing.T) {
		f, opened, housekeeping := stage(t, "Printer", func(_ ticketFixture, ticket *model.Ticket) { ticket.Title = "Printer jams" })

		moved, _, err := housekeeping.Housekeep(ctx, at)

		read, _ := f.tickets.GetTicket(ctx, f.agent, opened["Printer"].ID)
		if err != nil || moved != 1 || read.StateID != f.states["closed"].ID || read.Title != "Printer jams" {
			t.Errorf("renamed meanwhile: moved %d, %v, and the printer reads %+v; want it closed as Printer jams",
				moved, err, read)
		}
	})

	t.Run("a lock taken again", func(t *testing.T) {
		f, opened, housekeeping := stage(t, "Scanner", func(_ ticketFixture, ticket *model.Ticket) { ticket.LockedAt = &at })

		_, freed, err := housekeeping.Housekeep(ctx, at)

		read, _ := f.tickets.GetTicket(ctx, f.agent, opened["Scanner"].ID)
		if err != nil || freed != 0 || !read.Locked || read.LockedAt == nil || !read.LockedAt.Equal(at) {
			t.Errorf("locked again meanwhile: freed %d, %v, and the scanner reads %+v", freed, err, read)
		}
	})

	t.Run("a ticket put to wait", func(t *testing.T) {
		until := at.Add(time.Hour)
		f, opened, housekeeping := stage(t, "Scanner", func(f ticketFixture, ticket *model.Ticket) {
			ticket.StateID, ticket.PendingUntil = f.states["pending-reminder"].ID, &until
		})

		_, freed, err := housekeeping.Housekeep(ctx, at)

		read, _ := f.tickets.GetTicket(ctx, f.agent, opened["Scanner"].ID)
		if err != nil || freed != 0 || !read.Locked {
			t.Errorf("put to wait meanwhile: freed %d, %v, and the scanner reads %+v", freed, err, read)
		}
	})

	t.Run("a ticket renamed while its lock runs out", func(t *testing.T) {
		f, opened, housekeeping := stage(t, "Scanner", func(_ ticketFixture, ticket *model.Ticket) { ticket.Title = "Scanner jams" })

		_, freed, err := housekeeping.Housekeep(ctx, at)

		read, _ := f.tickets.GetTicket(ctx, f.agent, opened["Scanner"].ID)
		if err != nil || freed != 1 || read.Locked || read.Title != "Scanner jams" {
			t.Errorf("renamed meanwhile: freed %d, %v, and the scanner reads %+v; want it free as Scanner jams",
				freed, err, read)
		}
	})
}

// A ticket whose wait closes it loses its lock with it, as KIX18's pending check
// has it, both lines written by the application; a wait that ends in a state
// that does not close the ticket leaves the lock to its owner.
func TestAWaitThatClosesATicketFreesItsLock(t *testing.T) {
	ctx := context.Background()
	f := newTicketFixture(t)
	settings := newStubSettings()
	config := service.NewSysConfigService(settings)
	f.tickets.WithConfig(config)

	now := time.Now().UTC()
	moment, locked := now.Add(time.Hour), true

	waitingLocked := func(title string) *model.Ticket {
		ticket := f.open(t, f.agent, f.desk, title)

		waits, err := f.tickets.UpdateTicket(ctx, f.agent, ticket.ID, command.UpdateTicketCommand{
			StateID: &f.states["pending-auto-close"].ID, PendingUntil: &moment, Locked: &locked,
		})
		if err != nil {
			t.Fatal(err)
		}

		return waits
	}

	printer := waitingLocked("Printer")

	if _, _, err := f.tickets.Housekeep(ctx, now.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}

	closed, _ := f.tickets.GetTicket(ctx, f.agent, printer.ID)
	if closed.StateID != f.states["closed"].ID || closed.Locked || closed.LockedAt != nil {
		t.Errorf("the printer was left as %+v; want it closed and free", closed)
	}

	history, _ := f.tickets.History(ctx, f.agent, printer.ID)

	var said []string
	for _, entry := range history[len(history)-2:] {
		if entry.Actor == model.SenderSystem {
			said = append(said, string(entry.Event)+":"+entry.NewValue)
		}
	}

	if len(said) != 2 || said[0] != string(model.HistoryState)+":closed" || said[1] != string(model.HistoryLock)+":false" {
		t.Errorf("the printer's history ends with %v; want the application closing it and freeing it", said)
	}

	if _, err := config.Set(ctx, model.ConfigTicketStateAfterPending, "open"); err != nil {
		t.Fatal(err)
	}

	scanner := waitingLocked("Scanner")
	if _, _, err := f.tickets.Housekeep(ctx, now.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}

	if opened, _ := f.tickets.GetTicket(ctx, f.agent, scanner.ID); opened.StateID != f.states["open"].ID || !opened.Locked {
		t.Errorf("a wait ending in open left the scanner as %+v; want it open and still locked", opened)
	}
}

// An answer the customer sees starts the time a lock runs for again, as KIX18
// counts it; a note the customer does not see does not.
func TestAnAnswerTheCustomerSeesStartsTheLockAgain(t *testing.T) {
	ctx := context.Background()
	f := newTicketFixture(t)
	now := time.Now().UTC()
	locked := true

	f.desk.UnlockMinutes = 30
	if _, err := f.queues.Update(ctx, f.desk); err != nil {
		t.Fatal(err)
	}

	ticket := f.open(t, f.agent, f.desk, "Printer")
	if _, err := f.tickets.UpdateTicket(ctx, f.agent, ticket.ID, command.UpdateTicketCommand{Locked: &locked}); err != nil {
		t.Fatal(err)
	}

	// lockedLongAgo puts the lock back to an hour before now, through the tables.
	lockedLongAgo := func() {
		current, err := f.repo.GetByID(ctx, ticket.ID)
		if err != nil {
			t.Fatal(err)
		}

		long := now.Add(-time.Hour)
		current.LockedAt = &long

		if _, err := f.repo.Update(ctx, current, nil); err != nil {
			t.Fatal(err)
		}
	}

	lockedLongAgo()

	if _, err := f.tickets.AddArticle(ctx, f.agent, ticket.ID, command.AddArticleCommand{
		Body: ptr("A new roller is on its way."), CustomerVisible: true,
	}); err != nil {
		t.Fatal(err)
	}

	if _, freed, err := f.tickets.Housekeep(ctx, now.Add(10*time.Minute)); err != nil || freed != 0 {
		t.Errorf("ten minutes after an answer the customer saw, %d locks were freed, %v", freed, err)
	}

	lockedLongAgo()

	if _, err := f.tickets.AddArticle(ctx, f.agent, ticket.ID, command.AddArticleCommand{Body: ptr("Ordered.")}); err != nil {
		t.Fatal(err)
	}

	if _, freed, err := f.tickets.Housekeep(ctx, now.Add(10*time.Minute)); err != nil || freed != 1 {
		t.Errorf("a note the customer does not see kept the lock: %d freed, %v", freed, err)
	}
}

// The system configuration names only a state a wait can end in for a ticket
// whose wait is over: one that is there and offered, does not wait itself and
// is not merged - anything else is refused as it is set rather than quietly
// closing every ticket instead.
func TestOnlyAStateAWaitCanEndInIsNamedForIt(t *testing.T) {
	ctx := context.Background()
	f := newTicketFixture(t)
	config := service.NewSysConfigService(newStubSettings()).
		WithCheck(model.ConfigTicketStateAfterPending, f.tickets.CanEndAWait)

	if _, err := f.stateRepo.Save(ctx, &model.TicketState{Name: "resolved", Type: model.StateTypeClosed}); err != nil {
		t.Fatal(err)
	}

	for _, refused := range []string{"nothing like it", "pending-reminder", "pending-auto-close", "merged", "resolved"} {
		if _, err := config.Set(ctx, model.ConfigTicketStateAfterPending, refused); apperror.KindOf(err) != apperror.KindInvalid {
			t.Errorf("naming %q was answered %v, want it refused as invalid", refused, err)
		}
	}

	if _, err := config.Set(ctx, model.ConfigTicketStateAfterPending, "open"); err != nil {
		t.Fatalf("naming open was refused: %v", err)
	}

	if named, err := config.String(ctx, model.ConfigTicketStateAfterPending); err != nil || named != "open" {
		t.Errorf("after naming open the option reads %q, %v", named, err)
	}
}

// A lock runs out in the state types the installation names: a ticket that
// waits keeps its lock as an installation ships, and loses it once the types
// it waits in are named too.
func TestALockRunsOutInTheStateTypesTheInstallationNames(t *testing.T) {
	ctx := context.Background()
	f := newTicketFixture(t)
	now := time.Now().UTC()
	locked, moment := true, now.Add(3*time.Hour)

	f.desk.UnlockMinutes = 30
	if _, err := f.queues.Update(ctx, f.desk); err != nil {
		t.Fatal(err)
	}

	waits := f.open(t, f.agent, f.desk, "Scanner")
	if _, err := f.tickets.UpdateTicket(ctx, f.agent, waits.ID, command.UpdateTicketCommand{
		Locked: &locked, StateID: &f.states["pending-reminder"].ID, PendingUntil: &moment,
	}); err != nil {
		t.Fatal(err)
	}

	if _, freed, err := f.tickets.Housekeep(ctx, now.Add(time.Hour)); err != nil || freed != 0 {
		t.Fatalf("as shipped, a lock on a ticket that waits was freed: %d, %v", freed, err)
	}

	if err := f.unlockTypes.Replace(ctx, []model.StateType{model.StateTypePendingReminder}); err != nil {
		t.Fatal(err)
	}

	if _, freed, err := f.tickets.Housekeep(ctx, now.Add(time.Hour)); err != nil || freed != 1 {
		t.Errorf("with the waiting type named, %d locks were freed, %v; want the scanner's", freed, err)
	}
}
