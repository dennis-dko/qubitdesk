package service

import (
	"context"
	"strconv"
	"time"

	"github.com/dennis-dko/go-ticket-system/internal/domain/model"
	"github.com/dennis-dko/go-ticket-system/internal/domain/repository"
	"github.com/dennis-dko/go-ticket-system/internal/support/apperror"
)

// WithServiceLevels attaches what decides what a ticket is owed: the service
// levels, the calendars their minutes are counted in, and the installation's
// public holidays, which a calendar naming none follows - as zone, given to the
// constructor, is the installation's zone. Optional: without it a ticket is
// opened owed nothing.
func (s *TicketApplicationService) WithServiceLevels(
	levels repository.ServiceLevelRepository,
	calendars repository.CalendarRepository,
	holidays func(ctx context.Context) string,
) *TicketApplicationService {
	s.levels, s.calendars, s.holidays = levels, calendars, holidays

	return s
}

// openOwed gives a ticket being opened its service level - the one chosen, or
// the one its queue starts tickets with while that is still offered - and the
// moments it is owed.
func (s *TicketApplicationService) openOwed(
	ctx context.Context,
	ticket *model.Ticket,
	chosen *uint,
	queue *model.Queue,
) error {
	if s.levels == nil {
		return nil
	}

	var level *model.ServiceLevel

	switch {
	case chosen != nil:
		found, err := s.offeredLevel(ctx, *chosen, nil)
		if err != nil {
			return err
		}

		level = found
	case queue.ServiceLevelID != nil:
		found, err := s.levels.GetByID(ctx, *queue.ServiceLevelID)
		if err != nil && apperror.KindOf(err) != apperror.KindNotFound {
			return err
		}

		if found != nil && found.Valid {
			level = found
		}
	}

	return s.owe(ctx, ticket, level)
}

// changeOwed puts a ticket under another service level, or under none, and
// counts the moments it is owed afresh from its opening; an answer it has
// already been given stays given.
func (s *TicketApplicationService) changeOwed(
	ctx context.Context,
	ticket *model.Ticket,
	chosen *uint,
	clear bool,
	change *history,
) error {
	if chosen == nil && !clear {
		return nil
	}

	if chosen != nil && clear {
		return apperror.InvalidFields("serviceLevelId")
	}

	if s.levels == nil || (chosen != nil && sameID(chosen, ticket.ServiceLevelID)) || (clear && ticket.ServiceLevelID == nil) {
		return nil
	}

	var level *model.ServiceLevel

	if chosen != nil {
		found, err := s.offeredLevel(ctx, *chosen, ticket.ServiceLevelID)
		if err != nil {
			return err
		}

		level = found
	}

	was := s.levelName(ctx, ticket.ServiceLevelID)

	if err := s.owe(ctx, ticket, level); err != nil {
		return err
	}

	change.add(model.HistoryServiceLevel, was, s.levelName(ctx, ticket.ServiceLevelID))

	return nil
}

// offeredLevel is a service level a ticket may be put under: one that is there
// and still offered, or the one it is already under. Anything else is refused
// as the field it came in.
func (s *TicketApplicationService) offeredLevel(
	ctx context.Context,
	id uint,
	current *uint,
) (*model.ServiceLevel, error) {
	level, err := s.levels.GetByID(ctx, id)
	if apperror.KindOf(err) == apperror.KindNotFound {
		return nil, apperror.InvalidFields("serviceLevelId")
	}

	if err != nil {
		return nil, err
	}

	if !level.Valid && !sameID(&id, current) {
		return nil, apperror.InvalidFields("serviceLevelId")
	}

	return level, nil
}

// owe puts a ticket under a service level, or none, with the moments it is owed
// from its opening.
func (s *TicketApplicationService) owe(ctx context.Context, ticket *model.Ticket, level *model.ServiceLevel) error {
	ticket.ServiceLevelID, ticket.FirstResponseDue, ticket.SolutionDue = nil, nil, nil

	if level == nil {
		return nil
	}

	id := level.ID
	ticket.ServiceLevelID = &id

	deadline, err := s.counter(ctx, level)
	if err != nil {
		return err
	}

	ticket.FirstResponseDue = deadline(ticket.CreatedAt, level.FirstResponseMinutes)
	ticket.SolutionDue = deadline(ticket.CreatedAt, level.SolutionMinutes)

	return nil
}

// counter is how a service level counts its minutes: in its calendar, in the
// calendar's zone and with its place's public holidays - the installation's
// where it names none - or around the clock where the level names no calendar.
// A promise of no minutes, or one a calendar never lets pass, owes no moment.
func (s *TicketApplicationService) counter(
	ctx context.Context,
	level *model.ServiceLevel,
) (func(from time.Time, minutes int) *time.Time, error) {
	if level.CalendarID == nil {
		return func(from time.Time, minutes int) *time.Time {
			if minutes <= 0 {
				return nil
			}

			due := from.Add(time.Duration(minutes) * time.Minute).UTC()

			return &due
		}, nil
	}

	calendar, err := s.calendars.GetByID(ctx, *level.CalendarID)
	if err != nil {
		return nil, err
	}

	zoneName := calendar.Zone
	if zoneName == "" {
		zoneName = s.zone(ctx)
	}

	zone, err := time.LoadLocation(zoneName)
	if err != nil {
		zone = time.UTC
	}

	place := calendar.HolidayPlace
	if place == "" && s.holidays != nil {
		place = s.holidays(ctx)
	}

	return func(from time.Time, minutes int) *time.Time {
		if minutes <= 0 {
			return nil
		}

		due, ok := calendar.Deadline(from, minutes, zone, place)
		if !ok {
			return nil
		}

		due = due.UTC()

		return &due
	}, nil
}

// levelName is a service level's name for the history, by the name it has now,
// or nothing for none.
func (s *TicketApplicationService) levelName(ctx context.Context, id *uint) string {
	if id == nil {
		return ""
	}

	level, err := s.levels.GetByID(ctx, *id)
	if err != nil {
		return "#" + strconv.FormatUint(uint64(*id), 10)
	}

	return level.Name
}
