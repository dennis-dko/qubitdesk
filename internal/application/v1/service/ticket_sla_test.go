package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/dennis-dko/qubitdesk/internal/application/v1/command"
	"github.com/dennis-dko/qubitdesk/internal/application/v1/service"
	"github.com/dennis-dko/qubitdesk/internal/domain/model"
	"github.com/dennis-dko/qubitdesk/internal/support/apperror"
)

// A ticket opened in a queue that names a service level is owed its first answer
// and its solution by moments counted from its opening - around the clock for a
// level without a calendar, in the calendar's working hours for one with - and
// the first article of an agent the customer sees answers it, while an internal
// note does not.
func TestATicketIsOwedWhatItsServiceLevelPromises(t *testing.T) {
	ctx := context.Background()
	f := newTicketFixture(t)

	gold, err := f.levels.Save(ctx, &model.ServiceLevel{
		Name: "Gold", FirstResponseMinutes: 60, SolutionMinutes: 480, Valid: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	f.desk.ServiceLevelID = &gold.ID
	if _, err := f.queues.Update(ctx, f.desk); err != nil {
		t.Fatal(err)
	}

	ticket := f.open(t, f.agent, f.desk, "Printer")

	if ticket.ServiceLevelID == nil || *ticket.ServiceLevelID != gold.ID {
		t.Fatalf("a ticket of the desk is owed %v, want Gold", ticket.ServiceLevelID)
	}

	if want := ticket.CreatedAt.Add(time.Hour); ticket.FirstResponseDue == nil || !ticket.FirstResponseDue.Equal(want) {
		t.Errorf("the first answer is owed by %v, want an hour after the opening, %v", ticket.FirstResponseDue, want)
	}

	if want := ticket.CreatedAt.Add(8 * time.Hour); ticket.SolutionDue == nil || !ticket.SolutionDue.Equal(want) {
		t.Errorf("the solution is owed by %v, want eight hours after the opening, %v", ticket.SolutionDue, want)
	}

	if _, err := f.tickets.AddArticle(ctx, f.agent, ticket.ID, command.AddArticleCommand{Body: ptr("Looking into it.")}); err != nil {
		t.Fatal(err)
	}

	if read, _ := f.tickets.GetTicket(ctx, f.agent, ticket.ID); read.FirstResponseAt != nil {
		t.Errorf("an internal note answered the ticket at %v", read.FirstResponseAt)
	}

	if _, err := f.tickets.AddArticle(ctx, f.agent, ticket.ID, command.AddArticleCommand{
		Body: ptr("A new roller is on its way."), CustomerVisible: true,
	}); err != nil {
		t.Fatal(err)
	}

	answered, _ := f.tickets.GetTicket(ctx, f.agent, ticket.ID)
	if answered.FirstResponseAt == nil {
		t.Fatal("an answer the customer sees did not answer the ticket")
	}

	first := *answered.FirstResponseAt

	if _, err := f.tickets.AddArticle(ctx, f.agent, ticket.ID, command.AddArticleCommand{
		Body: ptr("It is fitted."), CustomerVisible: true,
	}); err != nil {
		t.Fatal(err)
	}

	if again, _ := f.tickets.GetTicket(ctx, f.agent, ticket.ID); !again.FirstResponseAt.Equal(first) {
		t.Errorf("a second answer moved the first one from %v to %v", first, again.FirstResponseAt)
	}

	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatal(err)
	}

	// Every day but today, all day: whenever the case runs, counting in this
	// calendar starts tomorrow, so it can never come out as counting around the
	// clock would - office hours did, whenever the case ran early on a weekday.
	var everyDay []model.WorkingHours
	for day := time.Sunday; day <= time.Saturday; day++ {
		everyDay = append(everyDay, model.WorkingHours{Weekday: day, From: 0, To: model.MinutesPerDay})
	}

	office, err := f.calendars.Save(ctx, &model.Calendar{
		Name: "Office", Valid: true, Hours: everyDay,
		FreeDays: []model.FreeDay{{Day: model.CalendarDay(time.Now().In(berlin)), Name: "Today"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	silver, err := f.levels.Save(ctx, &model.ServiceLevel{
		Name: "Silver", CalendarID: &office.ID, FirstResponseMinutes: 120, Valid: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	chosen, err := f.tickets.CreateTicket(ctx, f.agent, command.CreateTicketCommand{
		Title: ptr("Scanner"), QueueID: &f.desk.ID, Body: ptr("It scans blank pages."), ServiceLevelID: &silver.ID,
	})
	if err != nil {
		t.Fatal(err)
	}

	// The calendar names no zone, so it follows the installation's - Berlin here.
	want, _ := office.Deadline(chosen.CreatedAt, 120, berlin, "")
	if chosen.FirstResponseDue == nil || !chosen.FirstResponseDue.Equal(want) || chosen.SolutionDue != nil {
		t.Errorf("a ticket under Silver is owed %v and %v; want its answer by %v, counted from tomorrow, and no solution",
			chosen.FirstResponseDue, chosen.SolutionDue, want)
	}
}

// A ticket is put under another service level - what it is owed counted afresh
// from its opening, and the history saying so - or under none; a level no longer
// offered is not chosen, and a queue whose level was retired opens its tickets
// owed nothing.
func TestATicketChangesItsServiceLevel(t *testing.T) {
	ctx := context.Background()
	f := newTicketFixture(t)

	gold, _ := f.levels.Save(ctx, &model.ServiceLevel{Name: "Gold", SolutionMinutes: 480, Valid: true})
	bronze, _ := f.levels.Save(ctx, &model.ServiceLevel{Name: "Bronze", SolutionMinutes: 2880, Valid: true})

	ticket := f.open(t, f.agent, f.desk, "Monitor")

	moved, err := f.tickets.UpdateTicket(ctx, f.agent, ticket.ID, command.UpdateTicketCommand{ServiceLevelID: &bronze.ID})
	if err != nil {
		t.Fatal(err)
	}

	if want := moved.CreatedAt.Add(48 * time.Hour); moved.SolutionDue == nil || !moved.SolutionDue.Equal(want) {
		t.Errorf("under Bronze the solution is owed by %v, want two days after the opening", moved.SolutionDue)
	}

	history, _ := f.tickets.History(ctx, f.agent, ticket.ID)
	if last := history[len(history)-1]; last.Event != model.HistoryServiceLevel || last.NewValue != "Bronze" {
		t.Errorf("the history ends with %+v; want the move to Bronze", last)
	}

	cleared, err := f.tickets.UpdateTicket(ctx, f.agent, ticket.ID, command.UpdateTicketCommand{ClearServiceLevel: true})
	if err != nil || cleared.ServiceLevelID != nil || cleared.SolutionDue != nil {
		t.Errorf("taking the level off answered %+v, %v", cleared, err)
	}

	gold.Valid = false
	if _, err := f.levels.Update(ctx, gold); err != nil {
		t.Fatal(err)
	}

	if _, err := f.tickets.UpdateTicket(ctx, f.agent, ticket.ID, command.UpdateTicketCommand{ServiceLevelID: &gold.ID}); apperror.KindOf(err) != apperror.KindInvalid {
		t.Errorf("a retired level chosen for a ticket was answered %v", err)
	}

	f.desk.ServiceLevelID = &gold.ID
	if _, err := f.queues.Update(ctx, f.desk); err != nil {
		t.Fatal(err)
	}

	if retired := f.open(t, f.agent, f.desk, "Keyboard"); retired.ServiceLevelID != nil {
		t.Errorf("a ticket of a queue whose level was retired is owed %v", retired.ServiceLevelID)
	}
}

// A promise has run out unkept when the first answer is late or the solution is.
func TestATicketIsEscalatedOnceAPromiseRunsOut(t *testing.T) {
	now := time.Date(2026, time.November, 16, 12, 0, 0, 0, time.UTC)
	past, future := now.Add(-time.Minute), now.Add(time.Minute)

	for _, tc := range []struct {
		name   string
		ticket model.Ticket
		want   bool
	}{
		{"owed nothing", model.Ticket{}, false},
		{"answer still to come", model.Ticket{FirstResponseDue: &future}, false},
		{"answer late", model.Ticket{FirstResponseDue: &past}, true},
		{"answered late", model.Ticket{FirstResponseDue: &past, FirstResponseAt: &now}, false},
		{"solution late", model.Ticket{SolutionDue: &past}, true},
		{"finished late", model.Ticket{SolutionDue: &past, ClosedAt: &now}, false},
	} {
		if got := tc.ticket.Escalated(now); got != tc.want {
			t.Errorf("%s: escalated %t, want %t", tc.name, got, tc.want)
		}
	}
}

// A service level that names no calendar of its own counts in the
// installation's where the system configuration names one, and around the
// clock where it names none: in a calendar in which time never passes, the
// ticket is owed no moment at all.
func TestALevelWithoutACalendarCountsInTheInstallations(t *testing.T) {
	ctx := context.Background()
	f := newTicketFixture(t)
	config := service.NewSysConfigService(newStubSettings())
	f.tickets.WithConfig(config)

	gold, err := f.levels.Save(ctx, &model.ServiceLevel{Name: "Gold", FirstResponseMinutes: 60, Valid: true})
	if err != nil {
		t.Fatal(err)
	}

	f.desk.ServiceLevelID = &gold.ID
	if _, err := f.queues.Update(ctx, f.desk); err != nil {
		t.Fatal(err)
	}

	clock := f.open(t, f.agent, f.desk, "Printer")
	if want := clock.CreatedAt.Add(time.Hour); clock.FirstResponseDue == nil || !clock.FirstResponseDue.Equal(want) {
		t.Fatalf("with no calendar named the printer is owed an answer by %v, want %v", clock.FirstResponseDue, want)
	}

	never, err := f.calendars.Save(ctx, &model.Calendar{Name: "Never", Valid: true})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := config.Set(ctx, model.ConfigTimeWorkingHours, float64(never.ID)); err != nil {
		t.Fatal(err)
	}

	if counted := f.open(t, f.agent, f.desk, "Scanner"); counted.FirstResponseDue != nil {
		t.Errorf("counted in a calendar in which time never passes, the scanner is owed an answer by %v", counted.FirstResponseDue)
	}
}
