package service

import (
	"context"
	"slices"
	"strings"

	"github.com/dennis-dko/go-ticket-system/internal/application/v1/command"
	"github.com/dennis-dko/go-ticket-system/internal/domain/model"
	"github.com/dennis-dko/go-ticket-system/internal/domain/repository"
	"github.com/dennis-dko/go-ticket-system/internal/support/apperror"
)

// JobApplicationService administers the jobs: which tickets the automation
// takes, what it runs on them and when. A job is checked as it is saved
// against what it names - criteria naming records that are there, macros and
// plans that are there and, where it names them anew, still offered - and is
// refused criteria that would take every ticket there is.
type JobApplicationService struct {
	jobs   repository.JobRepository
	macros repository.MacroRepository
	plans  repository.ExecPlanRepository
	refs   MacroReferences
}

// NewJobApplicationService reads the macros, the plans and the records a job's
// criteria may name.
func NewJobApplicationService(
	jobs repository.JobRepository,
	macros repository.MacroRepository,
	plans repository.ExecPlanRepository,
	refs MacroReferences,
) *JobApplicationService {
	return &JobApplicationService{jobs: jobs, macros: macros, plans: plans, refs: refs}
}

func (s *JobApplicationService) List(ctx context.Context) ([]*model.Job, error) {
	return s.jobs.GetAll(ctx)
}

func (s *JobApplicationService) Get(ctx context.Context, id uint) (*model.Job, error) {
	if id == 0 {
		return nil, apperror.InvalidFields("id")
	}

	return s.jobs.GetByID(ctx, id)
}

func (s *JobApplicationService) Create(ctx context.Context, cmd command.JobCommand) (*model.Job, error) {
	var missing []string

	if cmd.Name == nil {
		missing = append(missing, "name")
	}

	if cmd.Criteria == nil {
		missing = append(missing, "criteria")
	}

	if cmd.MacroIDs == nil {
		missing = append(missing, "macroIds")
	}

	if len(missing) > 0 {
		return nil, apperror.InvalidFields(missing...)
	}

	job := &model.Job{Valid: true}
	if err := s.apply(ctx, job, cmd); err != nil {
		return nil, err
	}

	return s.jobs.Save(ctx, job)
}

func (s *JobApplicationService) Update(ctx context.Context, id uint, cmd command.JobCommand) (*model.Job, error) {
	job, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}

	if err := s.apply(ctx, job, cmd); err != nil {
		return nil, err
	}

	return s.jobs.Update(ctx, job)
}

func (s *JobApplicationService) Delete(ctx context.Context, id uint) error {
	if id == 0 {
		return apperror.InvalidFields("id")
	}

	return s.jobs.Delete(ctx, id)
}

// apply writes a command into a job, refusing every field it cannot hold at
// once.
func (s *JobApplicationService) apply(ctx context.Context, job *model.Job, cmd command.JobCommand) error {
	var invalid []string

	name := strings.TrimSpace(valueOf(cmd.Name))
	if cmd.Name != nil && (name == "" || model.TooLong(name, model.MaxCatalogNameLength)) {
		invalid = append(invalid, "name")
	}

	description := strings.TrimSpace(valueOf(cmd.Description))
	if model.TooLong(description, model.MaxDescriptionLength) {
		invalid = append(invalid, "description")
	}

	criteria := job.Criteria
	if cmd.Criteria != nil {
		criteria = model.TicketCriteria{
			QueueIDs: distinct(cmd.Criteria.QueueIDs), StateIDs: distinct(cmd.Criteria.StateIDs),
			PriorityIDs: distinct(cmd.Criteria.PriorityIDs), TypeIDs: distinct(cmd.Criteria.TypeIDs),
			Locked: cmd.Criteria.Locked,
		}

		named, err := criteriaNameWhatIsThere(ctx, s.refs, criteria)
		if err != nil {
			return err
		}

		if !named || criteria.Empty() {
			invalid = append(invalid, "criteria")
		}
	}

	macros := job.MacroIDs
	if cmd.MacroIDs != nil {
		macros = slices.Clone(*cmd.MacroIDs)

		offered, err := s.macrosOffered(ctx, macros, job.MacroIDs)
		if err != nil {
			return err
		}

		if !offered || len(macros) == 0 || len(macros) > model.MaxJobMacros {
			invalid = append(invalid, "macroIds")
		}
	}

	plans := job.PlanIDs
	if cmd.PlanIDs != nil {
		plans = distinct(*cmd.PlanIDs)

		offered, err := s.plansOffered(ctx, plans, job.PlanIDs)
		if err != nil {
			return err
		}

		if !offered {
			invalid = append(invalid, "planIds")
		}
	}

	if len(invalid) > 0 {
		return apperror.InvalidFields(invalid...)
	}

	if cmd.Name != nil {
		job.Name = name
	}

	if cmd.Description != nil {
		job.Description = description
	}

	if cmd.Valid != nil {
		job.Valid = *cmd.Valid
	}

	job.Criteria, job.MacroIDs, job.PlanIDs = criteria, macros, plans

	return nil
}

// criteriaNameWhatIsThere says whether every record the criteria name is
// there. A retired one may be named: a job or a notification may well be for the
// tickets still carrying it.
func criteriaNameWhatIsThere(ctx context.Context, refs MacroReferences, criteria model.TicketCriteria) (bool, error) {
	for _, check := range []struct {
		ids  []uint
		find func(context.Context, uint) error
	}{
		{criteria.QueueIDs, func(ctx context.Context, id uint) error { _, err := refs.Queues.GetByID(ctx, id); return err }},
		{criteria.StateIDs, func(ctx context.Context, id uint) error {
			_, err := refs.Catalog.States.GetByID(ctx, id)
			return err
		}},
		{criteria.PriorityIDs, func(ctx context.Context, id uint) error {
			_, err := refs.Catalog.Priorities.GetByID(ctx, id)
			return err
		}},
		{criteria.TypeIDs, func(ctx context.Context, id uint) error {
			_, err := refs.Catalog.Types.GetByID(ctx, id)
			return err
		}},
	} {
		for _, id := range check.ids {
			err := check.find(ctx, id)
			if apperror.KindOf(err) == apperror.KindNotFound {
				return false, nil
			}

			if err != nil {
				return false, err
			}
		}
	}

	return true, nil
}

// macrosOffered says whether every macro named is there, and offered where the
// job did not name it before: a retired macro is kept where it already stands
// and skipped when the job runs, but not chosen anew.
func (s *JobApplicationService) macrosOffered(ctx context.Context, named, before []uint) (bool, error) {
	for _, id := range named {
		macro, err := s.macros.GetByID(ctx, id)
		if apperror.KindOf(err) == apperror.KindNotFound {
			return false, nil
		}

		if err != nil {
			return false, err
		}

		if !macro.Valid && !slices.Contains(before, id) {
			return false, nil
		}
	}

	return true, nil
}

// plansOffered is macrosOffered for the plans that say when the job runs.
func (s *JobApplicationService) plansOffered(ctx context.Context, named, before []uint) (bool, error) {
	for _, id := range named {
		plan, err := s.plans.GetByID(ctx, id)
		if apperror.KindOf(err) == apperror.KindNotFound {
			return false, nil
		}

		if err != nil {
			return false, err
		}

		if !plan.Valid && !slices.Contains(before, id) {
			return false, nil
		}
	}

	return true, nil
}

// distinct is the ids each once, in the order they were first given.
func distinct(ids []uint) []uint {
	kept := make([]uint, 0, len(ids))
	for _, id := range ids {
		if !slices.Contains(kept, id) {
			kept = append(kept, id)
		}
	}

	return kept
}
