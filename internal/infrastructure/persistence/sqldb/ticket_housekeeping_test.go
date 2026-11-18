package sqldb_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/dennis-dko/go-ticket-system/internal/domain/model"
)

// numbered lists the ids of the tickets found, so a failure says which.
func numbered(found []*model.Ticket) []uint {
	ids := make([]uint, 0, len(found))
	for _, ticket := range found {
		ids = append(ids, ticket.ID)
	}

	return ids
}

// set changes rows the way no repository method would, to stage a ticket.
func (tables ticketTables) set(t *testing.T, query string, args ...any) {
	t.Helper()

	if _, err := tables.db.Exec(query, args...); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

// waitingState is the state seeded to close a ticket once its moment comes.
func (tables ticketTables) waitingState(t *testing.T) uint {
	t.Helper()

	var waiting uint
	if err := tables.db.QueryRow("SELECT id FROM ticket_states WHERE name = 'pending-auto-close'").Scan(&waiting); err != nil {
		t.Fatal(err)
	}

	return waiting
}

// The housekeeping finds the tickets of a waiting state whose moment has come,
// and the locked tickets - and nothing else.
func TestTheHousekeepingFindsWhatItIsFor(t *testing.T) {
	ctx := context.Background()
	tables := newTicketTables(t)
	at := time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)
	waiting := tables.waitingState(t)

	due := tables.create(t, "Printer", at, 0)
	later := tables.create(t, "Scanner", at, 0)
	locked := tables.create(t, "Monitor", at, 0)

	tables.set(t, "UPDATE tickets SET state_id = ?, pending_until = ? WHERE id = ?", waiting, at.Add(time.Hour), due.ID)
	tables.set(t, "UPDATE tickets SET state_id = ?, pending_until = ? WHERE id = ?", waiting, at.Add(3*time.Hour), later.ID)
	tables.set(t, "UPDATE tickets SET locked = ?, locked_at = ? WHERE id = ?", true, at, locked.ID)

	found, err := tables.tickets.PendingDue(ctx, []uint{waiting}, at.Add(2*time.Hour))
	if err != nil || len(found) != 1 || found[0].ID != due.ID {
		t.Errorf("two hours in, the waits over are %v, %v; want the printer's alone, %d", numbered(found), err, due.ID)
	}

	if none, err := tables.tickets.PendingDue(ctx, nil, at.Add(2*time.Hour)); err != nil || len(none) != 0 {
		t.Errorf("no waiting states found %v, %v", numbered(none), err)
	}

	held, err := tables.tickets.LockedTickets(ctx)
	if err != nil || len(held) != 1 || held[0].ID != locked.ID || held[0].LockedAt == nil {
		t.Errorf("the locked tickets are %v, %v; want the monitor, %d, with its moment", numbered(held), err, locked.ID)
	}
}

// A wait that ends writes only while the ticket still waits in the state it
// was found in for a moment that has come - and, freeing the lock, only while
// it is still locked - with its history only where it wrote, and nothing but
// what it is for.
func TestAWaitEndsOnlyWhileItIsStillOver(t *testing.T) {
	ctx := context.Background()
	tables := newTicketTables(t)
	at := time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)
	waiting := tables.waitingState(t)
	over := at.Add(2 * time.Hour)

	tickets := map[string]*model.Ticket{}
	for _, title := range []string{"Printer", "Router", "Phone"} {
		tickets[title] = tables.create(t, title, at, 0)
		tables.set(t, "UPDATE tickets SET state_id = ?, pending_until = ? WHERE id = ?",
			waiting, at.Add(time.Hour), tickets[title].ID)
	}

	for _, title := range []string{"Printer", "Phone"} {
		tables.set(t, "UPDATE tickets SET locked = ?, locked_at = ? WHERE id = ?", true, at, tickets[title].ID)
	}

	written := func(title string) int {
		history, err := tables.tickets.History(ctx, tickets[title].ID)
		if err != nil {
			t.Fatal(err)
		}

		return len(history)
	}

	closing := func(title string) (*model.Ticket, []model.HistoryEntry) {
		return &model.Ticket{ID: tickets[title].ID, StateID: tables.closed, ClosedAt: &over, UpdatedAt: over},
			[]model.HistoryEntry{{TicketID: tickets[title].ID, At: over, Actor: model.SenderSystem, Event: model.HistoryState}}
	}

	before := written("Printer")
	printer, line := closing("Printer")

	// Whatever the service worked out is owed next goes in with the move.
	owed := over.Add(2 * time.Hour)
	printer.UpdateDue = &owed

	if wrote, err := tables.tickets.EndWait(ctx, printer, tables.open, true, over, line); err != nil || wrote {
		t.Errorf("a ticket no longer in the state it was found in was moved: %v, %v", wrote, err)
	}

	if wrote, err := tables.tickets.EndWait(ctx, printer, waiting, true, at.Add(30*time.Minute), line); err != nil || wrote {
		t.Errorf("a ticket whose moment had not come was moved: %v, %v", wrote, err)
	}

	if after := written("Printer"); after != before {
		t.Errorf("writes that changed nothing wrote %d history lines", after-before)
	}

	if wrote, err := tables.tickets.EndWait(ctx, printer, waiting, true, over, line); err != nil || !wrote {
		t.Fatalf("a wait that is over did not end: %v, %v", wrote, err)
	}

	read, err := tables.tickets.GetByID(ctx, printer.ID)
	if err != nil || read.StateID != tables.closed || read.ClosedAt == nil || read.PendingUntil != nil ||
		read.Locked || read.LockedAt != nil || read.Title != "Printer" || written("Printer") != before+1 ||
		read.UpdateDue == nil || !read.UpdateDue.Equal(owed) {
		t.Errorf("the printer reads %+v, %v; want it closed and free, waiting for nothing, under its title, "+
			"owing what it was worked out to owe, with one line more", read, err)
	}

	router, line := closing("Router")

	if wrote, err := tables.tickets.EndWait(ctx, router, waiting, true, over, line); err != nil || wrote {
		t.Errorf("a lock that was not there was freed: %v, %v", wrote, err)
	}

	phone, line := closing("Phone")
	phone.UpdateDue = &owed

	if wrote, err := tables.tickets.EndWait(ctx, phone, waiting, false, over, line); err != nil || !wrote {
		t.Fatalf("a wait that is over did not end: %v, %v", wrote, err)
	}

	if read, err := tables.tickets.GetByID(ctx, phone.ID); err != nil || !read.Locked || read.LockedAt == nil ||
		!read.LockedAt.Equal(at) || read.UpdateDue == nil || !read.UpdateDue.Equal(owed) {
		t.Errorf("the phone reads %+v, %v; want its lock left as it was and what it owes written", read, err)
	}
}

// A lock is freed only once it has run its time, while the ticket is still
// locked and in a state a lock runs out in, and then nothing but the lock
// changes; an answer the customer sees starts its time again.
func TestALockIsFreedOnlyOnceItHasRunItsTime(t *testing.T) {
	ctx := context.Background()
	tables := newTicketTables(t)
	at := time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)
	working := []uint{tables.open}

	scanner := tables.create(t, "Scanner", at, 0)
	keyboard := tables.create(t, "Keyboard", at, 0)
	tables.set(t, "UPDATE tickets SET locked = ?, locked_at = ? WHERE id = ?", true, at, scanner.ID)

	line := []model.HistoryEntry{{TicketID: scanner.ID, At: at, Actor: model.SenderSystem, Event: model.HistoryLock}}

	if wrote, err := tables.tickets.FreeLock(ctx, scanner.ID, at.Add(10*time.Minute).Add(-30*time.Minute), working, at.Add(10*time.Minute), line); err != nil || wrote {
		t.Errorf("a lock ten minutes old was freed: %v, %v", wrote, err)
	}

	if wrote, err := tables.tickets.FreeLock(ctx, keyboard.ID, at.Add(time.Hour).Add(-30*time.Minute), working, at.Add(time.Hour), line); err != nil || wrote {
		t.Errorf("a ticket nobody locked was freed: %v, %v", wrote, err)
	}

	if wrote, err := tables.tickets.FreeLock(ctx, scanner.ID, at.Add(time.Hour).Add(-30*time.Minute), []uint{tables.closed}, at.Add(time.Hour), line); err != nil || wrote {
		t.Errorf("a ticket in a state not named was freed: %v, %v", wrote, err)
	}

	answered := at.Add(time.Hour)
	if _, err := tables.tickets.AddArticle(ctx, &model.Article{
		TicketID: scanner.ID, Sender: model.SenderAgent, Channel: model.ChannelNote, CustomerVisible: true,
		Body: "A new one is on its way.", CreatedAt: answered,
	}, 0, nil, nil, true, nil); err != nil {
		t.Fatal(err)
	}

	if _, err := tables.tickets.AddArticle(ctx, &model.Article{
		TicketID: keyboard.ID, Sender: model.SenderAgent, Channel: model.ChannelNote, CustomerVisible: true,
		Body: "Which one?", CreatedAt: answered,
	}, 0, nil, nil, true, nil); err != nil {
		t.Fatal(err)
	}

	if read, _ := tables.tickets.GetByID(ctx, keyboard.ID); read.Locked || read.LockedAt != nil {
		t.Errorf("an answer gave a ticket nobody locked a lock's moment: %+v", read)
	}

	if _, err := tables.tickets.AddArticle(ctx, &model.Article{
		TicketID: scanner.ID, Sender: model.SenderAgent, Channel: model.ChannelNote,
		Body: "Ordered.", CreatedAt: answered.Add(10 * time.Minute),
	}, 0, nil, nil, false, nil); err != nil {
		t.Fatal(err)
	}

	if read, _ := tables.tickets.GetByID(ctx, scanner.ID); read.LockedAt == nil || !read.LockedAt.Equal(answered) {
		t.Fatalf("after an answer and a note the lock counts from %v, want the answer's moment %v", read.LockedAt, answered)
	}

	if wrote, err := tables.tickets.FreeLock(ctx, scanner.ID, answered.Add(20*time.Minute).Add(-30*time.Minute), working, answered.Add(20*time.Minute), line); err != nil || wrote {
		t.Errorf("a lock answered under twenty minutes ago was freed: %v, %v", wrote, err)
	}

	freedAt := answered.Add(time.Hour)
	if wrote, err := tables.tickets.FreeLock(ctx, scanner.ID, freedAt.Add(-30*time.Minute), working, freedAt, line); err != nil || !wrote {
		t.Fatalf("a lock that had run its time was kept: %v, %v", wrote, err)
	}

	if read, err := tables.tickets.GetByID(ctx, scanner.ID); err != nil || read.Locked || read.LockedAt != nil ||
		!read.UpdatedAt.Equal(freedAt) || read.Title != "Scanner" {
		t.Errorf("the scanner reads %+v, %v; want it free as of the moment it was freed, under its title", read, err)
	}
}

// A job's criteria find, in SQL, the tickets they take - in the values each
// list names, locked or not where that is said - the oldest first and no more
// than the limit.
func TestAJobsCriteriaFindTheTicketsTheyTake(t *testing.T) {
	ctx := context.Background()
	tables := newTicketTables(t)
	at := time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)

	printer := tables.create(t, "Printer", at, 0)
	scanner := tables.create(t, "Scanner", at, 0)
	monitor := tables.create(t, "Monitor", at, 0)

	tables.set(t, "UPDATE tickets SET state_id = ? WHERE id = ?", tables.closed, monitor.ID)
	tables.set(t, "UPDATE tickets SET locked = ?, locked_at = ? WHERE id = ?", true, at, scanner.ID)

	locked, free := true, false

	for name, tc := range map[string]struct {
		criteria model.TicketCriteria
		limit    int
		want     []uint
	}{
		"the desk":          {model.TicketCriteria{QueueIDs: []uint{tables.desk}}, 10, []uint{printer.ID, scanner.ID, monitor.ID}},
		"open in the desk":  {model.TicketCriteria{QueueIDs: []uint{tables.desk}, StateIDs: []uint{tables.open}}, 10, []uint{printer.ID, scanner.ID}},
		"locked":            {model.TicketCriteria{Locked: &locked}, 10, []uint{scanner.ID}},
		"free and closed":   {model.TicketCriteria{Locked: &free, StateIDs: []uint{tables.closed}}, 10, []uint{monitor.ID}},
		"the two oldest":    {model.TicketCriteria{PriorityIDs: []uint{tables.normal}}, 2, []uint{printer.ID, scanner.ID}},
		"a type nothing is": {model.TicketCriteria{TypeIDs: []uint{9999}}, 10, []uint{}},
	} {
		got, err := tables.tickets.Matching(ctx, tc.criteria, tc.limit)
		if err != nil || !slices.Equal(got, tc.want) {
			t.Errorf("%s found %v, %v; want %v", name, got, err, tc.want)
		}
	}
}
