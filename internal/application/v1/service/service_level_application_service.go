package service

import (
	"context"
	"strings"

	"github.com/dennis-dko/qubitdesk/internal/application/v1/command"
	"github.com/dennis-dko/qubitdesk/internal/domain/model"
	"github.com/dennis-dko/qubitdesk/internal/domain/repository"
	"github.com/dennis-dko/qubitdesk/internal/support/apperror"
)

// SLAApplicationService administers the service levels.
type SLAApplicationService struct {
	levels    repository.ServiceLevelRepository
	calendars repository.CalendarRepository
}

// NewSLAApplicationService reads the calendars a level is counted in.
func NewSLAApplicationService(
	levels repository.ServiceLevelRepository,
	calendars repository.CalendarRepository,
) *SLAApplicationService {
	return &SLAApplicationService{levels: levels, calendars: calendars}
}

func (s *SLAApplicationService) List(ctx context.Context) ([]*model.ServiceLevel, error) {
	return s.levels.GetAll(ctx)
}

func (s *SLAApplicationService) Get(ctx context.Context, id uint) (*model.ServiceLevel, error) {
	if id == 0 {
		return nil, apperror.InvalidFields("id")
	}

	return s.levels.GetByID(ctx, id)
}

func (s *SLAApplicationService) Create(
	ctx context.Context,
	cmd command.ServiceLevelCommand,
) (*model.ServiceLevel, error) {
	if cmd.Name == nil {
		return nil, apperror.InvalidFields("name")
	}

	level := &model.ServiceLevel{Valid: true}
	if err := s.apply(ctx, level, cmd); err != nil {
		return nil, err
	}

	return s.levels.Save(ctx, level)
}

func (s *SLAApplicationService) Update(
	ctx context.Context,
	id uint,
	cmd command.ServiceLevelCommand,
) (*model.ServiceLevel, error) {
	level, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}

	if err := s.apply(ctx, level, cmd); err != nil {
		return nil, err
	}

	return s.levels.Update(ctx, level)
}

// Delete deletes a service level nothing uses. One that queues start their
// tickets with or tickets carry is refused, with how many, and is retired
// instead - made invalid - as every value tickets carry is.
func (s *SLAApplicationService) Delete(ctx context.Context, id uint) error {
	level, err := s.Get(ctx, id)
	if err != nil {
		return err
	}

	queues, tickets, err := s.levels.CountUses(ctx, id)
	if err != nil {
		return err
	}

	if queues > 0 || tickets > 0 {
		return apperror.Conflictf("%d queues and %d tickets use the service level %q", queues, tickets, level.Name).
			WithCode("serviceLevelInUse", level.Name, queues, tickets)
	}

	return s.levels.Delete(ctx, id)
}

// apply writes a command's fields into a service level, refusing every field it
// cannot hold at once. A calendar no longer offered is not chosen anew, and
// stays where it was already chosen.
func (s *SLAApplicationService) apply(
	ctx context.Context,
	level *model.ServiceLevel,
	cmd command.ServiceLevelCommand,
) error {
	var invalid []string

	if cmd.Name != nil {
		if name := strings.TrimSpace(*cmd.Name); name == "" || model.TooLong(name, model.MaxNameLength) {
			invalid = append(invalid, "name")
		} else {
			level.Name = name
		}
	}

	if cmd.Description != nil {
		if description := strings.TrimSpace(*cmd.Description); model.TooLong(description, model.MaxDescriptionLength) {
			invalid = append(invalid, "description")
		} else {
			level.Description = description
		}
	}

	switch {
	case cmd.ClearCalendar && cmd.CalendarID != nil:
		invalid = append(invalid, "calendarId")
	case cmd.ClearCalendar:
		level.CalendarID = nil
	case cmd.CalendarID != nil:
		ok, err := s.offered(ctx, *cmd.CalendarID, level.CalendarID)
		if err != nil {
			return err
		}

		if ok {
			level.CalendarID = cmd.CalendarID
		} else {
			invalid = append(invalid, "calendarId")
		}
	}

	if !takeMinutes(cmd.FirstResponseMinutes, &level.FirstResponseMinutes) {
		invalid = append(invalid, "firstResponseMinutes")
	}

	if !takeMinutes(cmd.UpdateMinutes, &level.UpdateMinutes) {
		invalid = append(invalid, "updateMinutes")
	}

	if !takeMinutes(cmd.SolutionMinutes, &level.SolutionMinutes) {
		invalid = append(invalid, "solutionMinutes")
	}

	if cmd.Valid != nil {
		level.Valid = *cmd.Valid
	}

	if len(invalid) > 0 {
		return apperror.InvalidFields(invalid...)
	}

	return nil
}

// takeMinutes writes minutes that were sent into a promise, and answers false
// for a count below none or past a year; nothing sent leaves the promise alone.
func takeMinutes(sent *int, into *int) bool {
	if sent == nil {
		return true
	}

	if *sent < 0 || *sent > model.MaxServiceMinutes {
		return false
	}

	*into = *sent

	return true
}

// offered answers whether a calendar may be chosen for a level: one that is
// there and still offered, or the one the level already counts in.
func (s *SLAApplicationService) offered(ctx context.Context, id uint, current *uint) (bool, error) {
	calendar, err := s.calendars.GetByID(ctx, id)
	if apperror.KindOf(err) == apperror.KindNotFound {
		return false, nil
	}

	if err != nil {
		return false, err
	}

	return calendar.Valid || (current != nil && *current == id), nil
}
