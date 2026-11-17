package service_test

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/dennis-dko/go-ticket-system/internal/application/v1/service"
	"github.com/dennis-dko/go-ticket-system/internal/domain/model"
	"github.com/dennis-dko/go-ticket-system/internal/infrastructure/persistence/memory"
)

// automationFixture is the automation over a ticket fixture: macros, jobs and
// runs in memory, the jobs run through the fixture's ticket service.
type automationFixture struct {
	ticketFixture
	automation *service.AutomationService
	macros     *memory.MacroRepository
	jobs       *memory.JobRepository
	plans      *memory.ExecPlanRepository
}

func newAutomationFixture(t *testing.T) automationFixture {
	t.Helper()

	f := newTicketFixture(t)
	macros, jobs, plans := memory.NewMacroRepository(), memory.NewJobRepository(), memory.NewExecPlanRepository()

	return automationFixture{
		ticketFixture: f,
		automation: service.NewAutomationService(service.AutomationRepositories{
			Jobs: jobs, Macros: macros, Plans: plans, Runs: memory.NewJobRunRepository(),
		}, f.repo, f.tickets, func(context.Context) string { return "Europe/Berlin" }),
		macros: macros,
		jobs:   jobs,
		plans:  plans,
	}
}

func (f automationFixture) macro(t *testing.T, name string, valid bool, steps ...model.MacroAction) uint {
	t.Helper()

	macro, err := f.macros.Save(context.Background(), &model.Macro{Name: name, Valid: valid, Actions: steps})
	if err != nil {
		t.Fatal(err)
	}

	return macro.ID
}

func (f automationFixture) job(t *testing.T, criteria model.TicketCriteria, macros ...uint) uint {
	t.Helper()

	all, _ := f.jobs.GetAll(context.Background())

	job, err := f.jobs.Save(context.Background(), &model.Job{
		Name: "Job " + strconv.Itoa(len(all)+1), Valid: true, Criteria: criteria, MacroIDs: macros,
	})
	if err != nil {
		t.Fatal(err)
	}

	return job.ID
}

func idText(id uint) string { return strconv.FormatUint(uint64(id), 10) }

// A job takes its macros through every ticket its criteria find, step by step
// and as the application: each change written into the history as the
// system's, the article it writes signed by the system, a ticket its criteria
// do not take left alone, and the run kept with when the job ran.
func TestAJobRunsItsMacrosThroughEveryTicketItFinds(t *testing.T) {
	ctx := context.Background()
	f := newAutomationFixture(t)

	printer := f.open(t, f.admin, f.desk, "Printer")
	scanner := f.open(t, f.admin, f.desk, "Scanner")
	ledger := f.open(t, f.admin, f.readOnly, "Ledger")

	escalate := f.macro(t, "Escalate", true,
		model.MacroAction{Type: model.ActionPrioritySet, Parameters: map[string]string{"priorityId": idText(f.priority["high"].ID)}},
		model.MacroAction{Type: model.ActionArticleCreate, Parameters: map[string]string{"channel": "note", "body": "Escalated by the night shift."}},
	)

	job := f.job(t, model.TicketCriteria{QueueIDs: []uint{f.desk.ID}}, escalate)

	run, err := f.automation.Run(ctx, job, model.RunManual)
	if err != nil {
		t.Fatal(err)
	}

	if run.Tickets != 2 || run.Changed != 2 || run.Failed != 0 || len(run.Lines) != 0 || run.Trigger != model.RunManual {
		t.Fatalf("the run reads %+v; want both of the desk's tickets taken through", run)
	}

	for _, ticket := range []*model.Ticket{printer, scanner} {
		read, _ := f.tickets.GetTicket(ctx, f.admin, ticket.ID)
		if read.PriorityID != f.priority["high"].ID {
			t.Errorf("%s was left at priority %d, want high", read.Title, read.PriorityID)
		}

		history, _ := f.tickets.History(ctx, f.admin, ticket.ID)
		if last := history[len(history)-1]; last.Actor != model.SenderSystem || last.ActorID != nil {
			t.Errorf("%s's history ends with %+v; want the system", read.Title, last)
		}

		articles, _ := f.tickets.Articles(ctx, f.admin, ticket.ID)
		if last := articles[len(articles)-1]; last.Sender != model.SenderSystem || last.AuthorID != nil || last.Body != "Escalated by the night shift." {
			t.Errorf("%s's last article is %+v; want the system's note", read.Title, last)
		}
	}

	if read, _ := f.tickets.GetTicket(ctx, f.admin, ledger.ID); read.PriorityID == f.priority["high"].ID {
		t.Error("a ticket of another queue was escalated")
	}

	ran, _ := f.jobs.GetByID(ctx, job)
	if ran.LastRunAt == nil || !ran.LastRunAt.Equal(run.StartedAt) {
		t.Errorf("the job ran last at %v; want the run's start %v", ran.LastRunAt, run.StartedAt)
	}

	if runs, err := f.automation.Runs(ctx, job); err != nil || len(runs) != 1 || runs[0].ID != run.ID {
		t.Errorf("the job's runs read %+v, %v", runs, err)
	}
}

// A step refused leaves the ticket where it stands, with the refusal's code and
// values kept among the run's lines, and the steps after it untaken; a retired
// macro is passed over and said so; a step deleting the ticket ends what is
// done to it without a refusal.
func TestARunKeepsWhatItCouldNotDo(t *testing.T) {
	ctx := context.Background()
	f := newAutomationFixture(t)

	printer := f.open(t, f.admin, f.desk, "Printer")

	resolved, err := f.stateRepo.Save(ctx, &model.TicketState{Name: "resolved", Type: model.StateTypeClosed, Valid: true})
	if err != nil {
		t.Fatal(err)
	}

	resolve := f.macro(t, "Resolve", true,
		model.MacroAction{Type: model.ActionStateSet, Parameters: map[string]string{"stateId": idText(resolved.ID)}},
		model.MacroAction{Type: model.ActionPrioritySet, Parameters: map[string]string{"priorityId": idText(f.priority["high"].ID)}},
	)
	old := f.macro(t, "Old way", false,
		model.MacroAction{Type: model.ActionLockSet, Parameters: map[string]string{"locked": "true"}})

	resolved.Valid = false
	if _, err := f.stateRepo.Update(ctx, resolved); err != nil {
		t.Fatal(err)
	}

	run, err := f.automation.Run(ctx, f.job(t, model.TicketCriteria{QueueIDs: []uint{f.desk.ID}}, old, resolve), model.RunManual)
	if err != nil {
		t.Fatal(err)
	}

	if run.Tickets != 1 || run.Changed != 0 || run.Failed != 1 || len(run.Lines) != 2 {
		t.Fatalf("the run reads %+v; want the printer refused and the old macro passed over", run)
	}

	if passed := run.Lines[0]; passed.Code != "macroPassedOver" || passed.TicketID != nil || *passed.MacroID != old {
		t.Errorf("the first line is %+v; want the old macro passed over", passed)
	}

	if refused := run.Lines[1]; refused.Code != "stateRetired" || len(refused.Values) != 1 || refused.Values[0] != "resolved" ||
		*refused.TicketID != printer.ID || *refused.MacroID != resolve || refused.Step != 1 {
		t.Errorf("the second line is %+v; want step 1 of resolve refused for the retired state", refused)
	}

	if read, _ := f.tickets.GetTicket(ctx, f.admin, printer.ID); read.PriorityID == f.priority["high"].ID {
		t.Error("the step after the refused one was taken")
	}

	remove := f.macro(t, "Remove", true,
		model.MacroAction{Type: model.ActionTicketDelete},
		model.MacroAction{Type: model.ActionPrioritySet, Parameters: map[string]string{"priorityId": idText(f.priority["high"].ID)}},
	)

	gone, err := f.automation.Run(ctx, f.job(t, model.TicketCriteria{QueueIDs: []uint{f.desk.ID}}, remove), model.RunManual)
	if err != nil || gone.Changed != 1 || gone.Failed != 0 || len(gone.Lines) != 0 {
		t.Fatalf("the deleting run reads %+v, %v", gone, err)
	}

	if _, err := f.tickets.GetTicket(ctx, f.admin, printer.ID); err == nil {
		t.Error("the deleted printer can still be read")
	}
}

// A job whose event-based plan waits for what a person's change did runs on the
// ticket it was done to, if its criteria take it, as a run started by the
// event; the change the job itself makes raises nothing, so the job does not
// start itself again; and a ticket the criteria do not take is left alone.
func TestAnEventStartsTheJobsWaitingForIt(t *testing.T) {
	ctx := context.Background()
	f := newAutomationFixture(t)
	f.tickets.WithEvents(func(ctx context.Context, ticketID uint, events []model.TicketEvent) {
		if err := f.automation.OnEvents(ctx, ticketID, events); err != nil {
			t.Error(err)
		}
	})

	arrivals, err := f.plans.Save(ctx, &model.ExecPlan{Name: "Arrivals", Valid: true, Type: model.PlanEventBased,
		Events: []model.TicketEvent{model.EventTicketCreate, model.EventTicketPriorityUpdate}})
	if err != nil {
		t.Fatal(err)
	}

	raise := f.macro(t, "Raise", true,
		model.MacroAction{Type: model.ActionPrioritySet, Parameters: map[string]string{"priorityId": idText(f.priority["high"].ID)}})

	job, err := f.jobs.Save(ctx, &model.Job{Name: "Raise the desk", Valid: true,
		Criteria: model.TicketCriteria{QueueIDs: []uint{f.desk.ID}}, MacroIDs: []uint{raise}, PlanIDs: []uint{arrivals.ID}})
	if err != nil {
		t.Fatal(err)
	}

	printer := f.open(t, f.admin, f.desk, "Printer")
	ledger := f.open(t, f.admin, f.readOnly, "Ledger")

	if read, _ := f.tickets.GetTicket(ctx, f.admin, printer.ID); read.PriorityID != f.priority["high"].ID {
		t.Errorf("a ticket opened in the desk was left at priority %d, want high", read.PriorityID)
	}

	if read, _ := f.tickets.GetTicket(ctx, f.admin, ledger.ID); read.PriorityID == f.priority["high"].ID {
		t.Error("a ticket of another queue was raised")
	}

	runs, err := f.automation.Runs(ctx, job.ID)
	if err != nil || len(runs) != 1 || runs[0].Trigger != model.RunEvent || runs[0].Tickets != 1 {
		t.Errorf("the job's runs read %+v, %v; want the one the opening started, and none its own change started", runs, err)
	}
}

// A job whose time-based plan's time has come runs as a run started by the
// time, and once: the next look, a minute on, finds it has run since.
func TestATimeStartsTheJobsItIsDueFor(t *testing.T) {
	ctx := context.Background()
	f := newAutomationFixture(t)

	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatal(err)
	}

	monday := time.Date(2026, time.November, 16, 8, 0, 10, 0, berlin)

	mornings, err := f.plans.Save(ctx, &model.ExecPlan{Name: "Mornings", Valid: true, Type: model.PlanTimeBased,
		Weekdays: []time.Weekday{time.Monday}, Times: []int{8 * 60}})
	if err != nil {
		t.Fatal(err)
	}

	f.open(t, f.admin, f.desk, "Printer")

	note := f.macro(t, "Note", true,
		model.MacroAction{Type: model.ActionArticleCreate, Parameters: map[string]string{"channel": "note", "body": "Good morning."}})

	job, err := f.jobs.Save(ctx, &model.Job{Name: "Mornings", Valid: true,
		Criteria: model.TicketCriteria{QueueIDs: []uint{f.desk.ID}}, MacroIDs: []uint{note}, PlanIDs: []uint{mornings.ID}})
	if err != nil {
		t.Fatal(err)
	}

	// It last ran on Friday, so that only its having run since stops a second
	// run a minute on - a job that never ran is held by the minute alone.
	if err := f.jobs.RanAt(ctx, job.ID, monday.AddDate(0, 0, -3)); err != nil {
		t.Fatal(err)
	}

	if ran, err := f.automation.RunDue(ctx, monday); err != nil || ran != 1 {
		t.Fatalf("at eight the automation ran %d jobs, %v; want the morning job", ran, err)
	}

	if ran, err := f.automation.RunDue(ctx, monday.Add(time.Minute)); err != nil || ran != 0 {
		t.Errorf("a minute later the automation ran %d jobs, %v; want none", ran, err)
	}

	runs, _ := f.automation.Runs(ctx, job.ID)
	if len(runs) != 1 || runs[0].Trigger != model.RunTime {
		t.Errorf("the job's runs read %+v; want one started by the time", runs)
	}
}
