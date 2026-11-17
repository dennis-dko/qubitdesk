package service

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/dennis-dko/go-ticket-system/internal/application/v1/command"
	"github.com/dennis-dko/go-ticket-system/internal/domain/model"
	"github.com/dennis-dko/go-ticket-system/internal/domain/repository"
	"github.com/dennis-dko/go-ticket-system/internal/support/apperror"
)

// ExecPlanApplicationService administers the execution plans: when the jobs
// that name one run. A plan holds only what its kind reads - a time-based one
// its weekdays and times, an event-based one its events - so that nothing kept
// in it says it runs when it does not.
type ExecPlanApplicationService struct {
	plans repository.ExecPlanRepository
}

// NewExecPlanApplicationService keeps the plans in the repository given.
func NewExecPlanApplicationService(plans repository.ExecPlanRepository) *ExecPlanApplicationService {
	return &ExecPlanApplicationService{plans: plans}
}

func (s *ExecPlanApplicationService) List(ctx context.Context) ([]*model.ExecPlan, error) {
	return s.plans.GetAll(ctx)
}

func (s *ExecPlanApplicationService) Get(ctx context.Context, id uint) (*model.ExecPlan, error) {
	if id == 0 {
		return nil, apperror.InvalidFields("id")
	}

	return s.plans.GetByID(ctx, id)
}

func (s *ExecPlanApplicationService) Create(ctx context.Context, cmd command.ExecPlanCommand) (*model.ExecPlan, error) {
	var missing []string

	if cmd.Name == nil {
		missing = append(missing, "name")
	}

	if cmd.Type == nil {
		missing = append(missing, "type")
	}

	if len(missing) > 0 {
		return nil, apperror.InvalidFields(missing...)
	}

	plan := &model.ExecPlan{Valid: true}
	if err := s.apply(plan, cmd); err != nil {
		return nil, err
	}

	return s.plans.Save(ctx, plan)
}

func (s *ExecPlanApplicationService) Update(ctx context.Context, id uint, cmd command.ExecPlanCommand) (*model.ExecPlan, error) {
	plan, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}

	if err := s.apply(plan, cmd); err != nil {
		return nil, err
	}

	return s.plans.Update(ctx, plan)
}

// Delete deletes a plan no job names. One a job names is refused, with how
// many, and is retired instead - made invalid - so that it runs nothing and the
// jobs keep it.
func (s *ExecPlanApplicationService) Delete(ctx context.Context, id uint) error {
	plan, err := s.Get(ctx, id)
	if err != nil {
		return err
	}

	jobs, err := s.plans.CountJobs(ctx, id)
	if err != nil {
		return err
	}

	if jobs > 0 {
		return apperror.Conflictf("%d job(s) run on the plan %q", jobs, plan.Name).WithCode("planInUse", plan.Name, jobs)
	}

	return s.plans.Delete(ctx, id)
}

// apply writes a command into a plan, refusing every field it cannot hold at
// once. A plan of one kind keeps nothing of the other, so a change of kind
// takes the other kind's values away rather than leaving them to look as if
// they still applied; sending them for the wrong kind is refused.
func (s *ExecPlanApplicationService) apply(plan *model.ExecPlan, cmd command.ExecPlanCommand) error {
	var invalid []string

	name := strings.TrimSpace(valueOf(cmd.Name))
	if cmd.Name != nil && (name == "" || model.TooLong(name, model.MaxCatalogNameLength)) {
		invalid = append(invalid, "name")
	}

	description := strings.TrimSpace(valueOf(cmd.Description))
	if model.TooLong(description, model.MaxDescriptionLength) {
		invalid = append(invalid, "description")
	}

	kind := plan.Type
	if cmd.Type != nil {
		kind = model.ExecPlanType(strings.TrimSpace(*cmd.Type))
	}

	if kind != model.PlanTimeBased && kind != model.PlanEventBased {
		invalid = append(invalid, "type")
	}

	weekdays, weekdaysOK := weekdaysOf(cmd.Weekdays, plan.Weekdays)
	times, timesOK := timesOf(cmd.Times, plan.Times)
	events, eventsOK := eventsOf(cmd.Events, plan.Events)

	switch kind {
	case model.PlanTimeBased:
		if !weekdaysOK || len(weekdays) == 0 {
			invalid = append(invalid, "weekdays")
		}

		if !timesOK || len(times) == 0 {
			invalid = append(invalid, "times")
		}

		if cmd.Events != nil && len(*cmd.Events) > 0 {
			invalid = append(invalid, "events")
		}

		events = nil
	case model.PlanEventBased:
		if !eventsOK || len(events) == 0 {
			invalid = append(invalid, "events")
		}

		if cmd.Weekdays != nil && len(*cmd.Weekdays) > 0 {
			invalid = append(invalid, "weekdays")
		}

		if cmd.Times != nil && len(*cmd.Times) > 0 {
			invalid = append(invalid, "times")
		}

		weekdays, times = nil, nil
	}

	if len(invalid) > 0 {
		return apperror.InvalidFields(invalid...)
	}

	if cmd.Name != nil {
		plan.Name = name
	}

	if cmd.Description != nil {
		plan.Description = description
	}

	if cmd.Valid != nil {
		plan.Valid = *cmd.Valid
	}

	plan.Type, plan.Weekdays, plan.Times, plan.Events = kind, weekdays, times, events

	return nil
}

// weekdaysOf is the weekdays sent, each once and in order, or the ones kept
// where none were sent; a day past Saturday cannot be held.
func weekdaysOf(sent *[]int, kept []time.Weekday) ([]time.Weekday, bool) {
	if sent == nil {
		return kept, true
	}

	days := make([]time.Weekday, 0, len(*sent))

	for _, day := range *sent {
		if day < 0 || day > 6 {
			return nil, false
		}

		if !slices.Contains(days, time.Weekday(day)) {
			days = append(days, time.Weekday(day))
		}
	}

	slices.Sort(days)

	return days, true
}

// timesOf is the times of day sent, each once and in order, or the ones kept
// where none were sent; a minute past the day's last, or more of them than a
// plan holds, cannot be held.
func timesOf(sent *[]int, kept []int) ([]int, bool) {
	if sent == nil {
		return kept, true
	}

	times := make([]int, 0, len(*sent))

	for _, minute := range *sent {
		if minute < 0 || minute >= model.MinutesPerDay {
			return nil, false
		}

		if !slices.Contains(times, minute) {
			times = append(times, minute)
		}
	}

	slices.Sort(times)

	return times, len(times) <= model.MaxPlanTimes
}

// eventsOf is the events sent, each once and in the order a form offers them,
// or the ones kept where none were sent; an event there is not cannot be held.
func eventsOf(sent *[]string, kept []model.TicketEvent) ([]model.TicketEvent, bool) {
	if sent == nil {
		return kept, true
	}

	for _, event := range *sent {
		if !slices.Contains(model.TicketEvents(), model.TicketEvent(strings.TrimSpace(event))) {
			return nil, false
		}
	}

	events := make([]model.TicketEvent, 0, len(*sent))

	for _, event := range model.TicketEvents() {
		if slices.ContainsFunc(*sent, func(named string) bool { return strings.TrimSpace(named) == string(event) }) {
			events = append(events, event)
		}
	}

	return events, true
}
