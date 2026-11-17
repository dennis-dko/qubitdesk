package service

import (
	"context"
	"strconv"
	"time"

	"github.com/dennis-dko/go-ticket-system/internal/application/v1/command"
	"github.com/dennis-dko/go-ticket-system/internal/domain/model"
	"github.com/dennis-dko/go-ticket-system/internal/domain/repository"
	"github.com/dennis-dko/go-ticket-system/internal/support/apperror"
)

// AutomationService runs the jobs. It finds the tickets a job's criteria take
// and takes the job's macros through each of them, step by step, by way of the
// ticket service and as the application - so that a step meets every rule a
// person's change meets and is written into the history as the system's - and
// keeps what each run did.
type AutomationService struct {
	jobs    repository.JobRepository
	macros  repository.MacroRepository
	runs    repository.JobRunRepository
	found   repository.TicketRepository
	tickets *TicketApplicationService
	now     func() time.Time
}

// NewAutomationService runs jobs through the ticket service given.
func NewAutomationService(
	jobs repository.JobRepository,
	macros repository.MacroRepository,
	runs repository.JobRunRepository,
	found repository.TicketRepository,
	tickets *TicketApplicationService,
) *AutomationService {
	return &AutomationService{jobs: jobs, macros: macros, runs: runs, found: found, tickets: tickets, now: time.Now}
}

// Run runs a job once, as the trigger given, on the tickets its criteria take
// now - the oldest first, at most model.MaxRunTickets of them - and keeps what
// it did. A retired job runs when somebody asks for it; that it runs on no plan
// is the plans' concern.
func (s *AutomationService) Run(ctx context.Context, jobID uint, trigger model.RunTrigger) (*model.JobRun, error) {
	if jobID == 0 {
		return nil, apperror.InvalidFields("id")
	}

	job, err := s.jobs.GetByID(ctx, jobID)
	if err != nil {
		return nil, err
	}

	return s.run(ctx, job, trigger)
}

// Runs lists a job's latest runs, the latest first.
func (s *AutomationService) Runs(ctx context.Context, jobID uint) ([]*model.JobRun, error) {
	if _, err := s.jobs.GetByID(ctx, jobID); err != nil {
		return nil, err
	}

	return s.runs.ForJob(ctx, jobID, 20)
}

// Prune removes the runs older than model.RunsKeptFor, and says how many.
func (s *AutomationService) Prune(ctx context.Context) (int, error) {
	return s.runs.PruneBefore(ctx, s.now().UTC().Add(-model.RunsKeptFor))
}

func (s *AutomationService) run(ctx context.Context, job *model.Job, trigger model.RunTrigger) (*model.JobRun, error) {
	started := s.now().UTC()
	run := &model.JobRun{JobID: job.ID, Trigger: trigger, StartedAt: started, Lines: []model.RunLine{}}

	macros := make([]*model.Macro, 0, len(job.MacroIDs))

	for _, id := range job.MacroIDs {
		macro, err := s.macros.GetByID(ctx, id)
		if err != nil && apperror.KindOf(err) != apperror.KindNotFound {
			return nil, err
		}

		// A retired macro is kept by the job and passed over when it runs, and
		// the run says so: a job that quietly did less than it names is a job
		// nobody can tell is broken.
		if err != nil || !macro.Valid {
			passed := apperror.Conflictf("the macro %d is retired or gone and was passed over", id).
				WithCode("macroPassedOver")
			run.Lines = append(run.Lines, lineOf(nil, id, 0, passed, started))

			continue
		}

		macros = append(macros, macro)
	}

	ids, err := s.found.Matching(ctx, job.Criteria, model.MaxRunTickets)
	if err != nil {
		return nil, err
	}

	run.Tickets = len(ids)

	for _, id := range ids {
		if s.takeThrough(ctx, id, macros, run) {
			run.Changed++
		} else {
			run.Failed++
		}
	}

	run.FinishedAt = s.now().UTC()

	recorded, err := s.runs.Record(ctx, run)
	if err != nil {
		return nil, err
	}

	if err := s.jobs.RanAt(ctx, job.ID, started); err != nil {
		return nil, err
	}

	return recorded, nil
}

// takeThrough takes a ticket through the macros' steps in order. It stops at
// the first step refused, whose refusal the run keeps - the steps after it were
// written for a ticket the refused one would have made - and once a step has
// deleted the ticket. It says whether every step was taken.
func (s *AutomationService) takeThrough(ctx context.Context, ticketID uint, macros []*model.Macro, run *model.JobRun) bool {
	for _, macro := range macros {
		for position, step := range macro.Actions {
			deleted, err := s.take(ctx, ticketID, step)
			if err != nil {
				run.Lines = append(run.Lines, lineOf(&ticketID, macro.ID, position+1, err, s.now().UTC()))

				return false
			}

			if deleted {
				return true
			}
		}
	}

	return true
}

// take takes one step on a ticket as the application, and says whether it
// deleted the ticket.
func (s *AutomationService) take(ctx context.Context, ticketID uint, step model.MacroAction) (bool, error) {
	caller := theApplication()
	given := step.Parameters

	switch step.Type {
	case model.ActionTicketDelete:
		return true, s.tickets.DeleteTicket(ctx, caller, ticketID)
	case model.ActionArticleCreate:
		minutes, _ := strconv.Atoi(given["minutes"])
		channel, subject, body := given["channel"], given["subject"], given["body"]

		_, err := s.tickets.AddArticle(ctx, caller, ticketID, command.AddArticleCommand{
			Channel: &channel, CustomerVisible: given["customerVisible"] == "true", Subject: &subject, Body: &body,
			Minutes: minutes,
		})

		return false, err
	}

	change, err := s.changeOf(step)
	if err != nil {
		return false, err
	}

	_, err = s.tickets.UpdateTicket(ctx, caller, ticketID, change)

	return false, err
}

// changeOf is the change a step makes to a ticket's fields. A person, a
// contact or an organisation left out clears the ticket's; a waiting state
// waits for the minutes given from the moment the step is taken.
func (s *AutomationService) changeOf(step model.MacroAction) (command.UpdateTicketCommand, error) {
	given := step.Parameters

	id := func(name string) *uint {
		value, err := strconv.ParseUint(given[name], 10, 0)
		if err != nil || value == 0 {
			return nil
		}

		named := uint(value)

		return &named
	}

	var change command.UpdateTicketCommand

	switch step.Type {
	case model.ActionStateSet:
		change.StateID = id("stateId")

		if minutes, err := strconv.Atoi(given["pendingMinutes"]); err == nil && minutes > 0 {
			until := s.now().UTC().Add(time.Duration(minutes) * time.Minute)
			change.PendingUntil = &until
		}
	case model.ActionPrioritySet:
		change.PriorityID = id("priorityId")
	case model.ActionTypeSet:
		change.TypeID = id("typeId")
	case model.ActionTeamSet:
		change.QueueID = id("queueId")
	case model.ActionOwnerSet:
		change.OwnerID = id("ownerId")
		change.ClearOwner = change.OwnerID == nil
	case model.ActionResponsibleSet:
		change.ResponsibleID = id("responsibleId")
		change.ClearResponsible = change.ResponsibleID == nil
	case model.ActionLockSet:
		locked := given["locked"] == "true"
		change.Locked = &locked
	case model.ActionTitleSet:
		title := given["title"]
		change.Title = &title
	case model.ActionContactSet:
		change.ContactID = id("contactId")
		change.ClearContact = change.ContactID == nil
	case model.ActionOrganisationSet:
		change.OrganisationID = id("organisationId")
		change.ClearOrganisation = change.OrganisationID == nil
	default:
		return change, apperror.InvalidFields("type")
	}

	return change, nil
}

// lineOf is what a run keeps of a step refused, or of a macro passed over with
// no ticket to say it of: the refusal's code and values, as the API would have
// answered them, and its English.
func lineOf(ticketID *uint, macroID uint, step int, err error, at time.Time) model.RunLine {
	line := model.RunLine{TicketID: ticketID, MacroID: &macroID, Step: step, Code: apperror.CodeInternal,
		Message: err.Error(), At: at}

	detail, ok := apperror.Detail(err)
	if !ok {
		return line
	}

	switch {
	case detail.Code != "":
		line.Code, line.Values = detail.Code, detail.Values
	case detail.Kind == apperror.KindNotFound:
		line.Code, line.Values = apperror.CodeNotFound, []any{detail.Entity, detail.ID}
	case detail.Kind == apperror.KindInvalid && len(detail.Fields) > 0:
		values := make([]any, 0, len(detail.Fields))
		for _, field := range detail.Fields {
			values = append(values, field)
		}

		line.Code, line.Values = apperror.CodeInvalidFields, values
	}

	return line
}
