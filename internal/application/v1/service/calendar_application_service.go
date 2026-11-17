package service

import (
	"context"
	"strconv"
	"strings"

	"github.com/dennis-dko/go-ticket-system/internal/application/v1/command"
	"github.com/dennis-dko/go-ticket-system/internal/domain/model"
	"github.com/dennis-dko/go-ticket-system/internal/domain/repository"
	"github.com/dennis-dko/go-ticket-system/internal/support/apperror"
)

// CalendarApplicationService administers the calendars a service level's
// minutes are counted in, one of which the installation may name as its own.
type CalendarApplicationService struct {
	calendars repository.CalendarRepository

	// config is the system configuration, which names the installation's
	// calendar; nil leaves it naming none.
	config ConfigReader
}

// NewCalendarApplicationService works on the calendars alone.
func NewCalendarApplicationService(calendars repository.CalendarRepository) *CalendarApplicationService {
	return &CalendarApplicationService{calendars: calendars}
}

// WithConfig reads which calendar the installation counts in, so that the one
// it names is not deleted from under it.
func (s *CalendarApplicationService) WithConfig(config ConfigReader) *CalendarApplicationService {
	s.config = config

	return s
}

// CanBeTheInstallations refuses a calendar the system configuration may not
// name as the installation's: one that is not there, or no longer offered.
// Naming none is always allowed: it counts around the clock.
func (s *CalendarApplicationService) CanBeTheInstallations(ctx context.Context, value string) error {
	if value == "" {
		return nil
	}

	id, err := strconv.ParseUint(value, 10, 0)
	if err != nil || id == 0 {
		return apperror.InvalidFields("value")
	}

	calendar, err := s.calendars.GetByID(ctx, uint(id))
	if apperror.KindOf(err) == apperror.KindNotFound {
		return apperror.InvalidFields("value")
	}

	if err != nil {
		return err
	}

	if !calendar.Valid {
		return apperror.InvalidFields("value")
	}

	return nil
}

func (s *CalendarApplicationService) List(ctx context.Context) ([]*model.Calendar, error) {
	return s.calendars.GetAll(ctx)
}

func (s *CalendarApplicationService) Get(ctx context.Context, id uint) (*model.Calendar, error) {
	if id == 0 {
		return nil, apperror.InvalidFields("id")
	}

	return s.calendars.GetByID(ctx, id)
}

func (s *CalendarApplicationService) Create(ctx context.Context, cmd command.CalendarCommand) (*model.Calendar, error) {
	if cmd.Name == nil {
		return nil, apperror.InvalidFields("name")
	}

	calendar := &model.Calendar{Valid: true}
	if err := applyCalendar(calendar, cmd); err != nil {
		return nil, err
	}

	return s.calendars.Save(ctx, calendar)
}

func (s *CalendarApplicationService) Update(
	ctx context.Context,
	id uint,
	cmd command.CalendarCommand,
) (*model.Calendar, error) {
	calendar, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}

	if err := applyCalendar(calendar, cmd); err != nil {
		return nil, err
	}

	return s.calendars.Update(ctx, calendar)
}

// Delete deletes a calendar no service level counts in. One that a level does
// is refused, with how many, and is retired instead.
func (s *CalendarApplicationService) Delete(ctx context.Context, id uint) error {
	calendar, err := s.Get(ctx, id)
	if err != nil {
		return err
	}

	levels, err := s.calendars.CountServiceLevels(ctx, id)
	if err != nil {
		return err
	}

	if levels > 0 {
		return apperror.Conflictf("%d service levels count in the calendar %q", levels, calendar.Name).
			WithCode("calendarInUse", calendar.Name, levels)
	}

	if s.config != nil {
		named, err := s.config.String(ctx, model.ConfigTimeWorkingHours)
		if err != nil {
			return err
		}

		if named == strconv.FormatUint(uint64(id), 10) {
			return apperror.Conflictf("the installation counts in the calendar %q", calendar.Name).
				WithCode("calendarCountsTheInstallation", calendar.Name)
		}
	}

	return s.calendars.Delete(ctx, id)
}

// applyCalendar writes a command's fields into a calendar, refusing every field
// it cannot hold at once. A zone is one the runtime knows rather than one that
// looks like a zone, because a plausible wrong name would count every deadline
// in UTC; an empty zone or place follows the installation's own.
func applyCalendar(calendar *model.Calendar, cmd command.CalendarCommand) error {
	var invalid []string

	if cmd.Name != nil {
		if name := strings.TrimSpace(*cmd.Name); name == "" || model.TooLong(name, model.MaxNameLength) {
			invalid = append(invalid, "name")
		} else {
			calendar.Name = name
		}
	}

	if cmd.Description != nil {
		if description := strings.TrimSpace(*cmd.Description); model.TooLong(description, model.MaxDescriptionLength) {
			invalid = append(invalid, "description")
		} else {
			calendar.Description = description
		}
	}

	if cmd.Zone != nil {
		if zone := strings.TrimSpace(*cmd.Zone); zone != "" && !model.IsSupportedTimezone(zone) {
			invalid = append(invalid, "zone")
		} else {
			calendar.Zone = zone
		}
	}

	if cmd.HolidayPlace != nil {
		if place := strings.TrimSpace(*cmd.HolidayPlace); !model.ValidHolidayChoice(place) {
			invalid = append(invalid, "holidayPlace")
		} else {
			calendar.HolidayPlace = place
		}
	}

	if cmd.Hours != nil {
		if model.ValidHours(*cmd.Hours) {
			calendar.Hours = *cmd.Hours
		} else {
			invalid = append(invalid, "hours")
		}
	}

	if cmd.FreeDays != nil {
		if days, ok := freeDaysOf(*cmd.FreeDays); ok {
			calendar.FreeDays = days
		} else {
			invalid = append(invalid, "freeDays")
		}
	}

	if cmd.Valid != nil {
		calendar.Valid = *cmd.Valid
	}

	if len(invalid) > 0 {
		return apperror.InvalidFields(invalid...)
	}

	return nil
}

// freeDaysOf is a calendar's free days as they are kept - each a calendar day
// with a name trimmed and bounded - or false for one that cannot be.
func freeDaysOf(sent []model.FreeDay) ([]model.FreeDay, bool) {
	days := make([]model.FreeDay, 0, len(sent))

	for _, free := range sent {
		name := strings.TrimSpace(free.Name)
		if free.Day.IsZero() || model.TooLong(name, model.MaxNameLength) {
			return nil, false
		}

		days = append(days, model.FreeDay{Day: model.CalendarDay(free.Day), Recurring: free.Recurring, Name: name})
	}

	return days, true
}
