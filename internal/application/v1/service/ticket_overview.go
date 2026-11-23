package service

import (
	"context"
	"slices"
	"time"

	"github.com/dennis-dko/qubitdesk/internal/domain/model"
	"github.com/dennis-dko/qubitdesk/internal/domain/repository"
)

// OverviewDays is how many calendar days the overview counts tickets opened
// and finished on, today included: two working weeks, which shows whether the
// work is being kept up with and is still a row of bars a screen holds.
const OverviewDays = 14

// TicketOverview is what an agent's overview shows them, all of it in the
// queues they reach: the unfinished tickets that are theirs - owned, theirs to
// answer for, watched - the unfinished tickets a promise has run out on, the
// unfinished tickets each queue holds by where they stand, and the tickets
// opened and finished on each of the last days of their own calendar.
//
// Overdue counts only what is still to do. A finished ticket whose first answer
// came late was escalated, and stays so in its own record, but nobody can do
// anything about it any more, and an overview is for what somebody can.
type TicketOverview struct {
	Owned       uint
	Responsible uint
	Watched     uint
	Overdue     uint
	Queues      []QueueLoad
	Days        []DayLoad
}

// QueueLoad is one queue's unfinished tickets by where they stand: not yet
// worked on, being worked on, waiting, and owed something that has run out.
type QueueLoad struct {
	QueueID uint
	New     uint
	Open    uint
	Waiting uint
	Overdue uint
}

// DayLoad is how many tickets were opened, and how many finished, on one
// calendar day of the reader's. Day is midnight UTC of that day, the shape
// model.CalendarDay gives a day that names no moment.
type DayLoad struct {
	Day    time.Time
	Opened uint
	Closed uint
}

// Overview counts what an agent's overview shows them. Nothing in it reaches
// past the queues the caller reaches, as no listing does.
func (s *TicketApplicationService) Overview(ctx context.Context, caller *Principal) (*TicketOverview, error) {
	queues, err := s.queues.GetAll(ctx)
	if err != nil {
		return nil, err
	}

	var reached []uint

	for _, queue := range queues {
		if _, ok := Reach(caller, queue); ok {
			reached = append(reached, queue.ID)
		}
	}

	states, err := s.states.GetAll(ctx)
	if err != nil {
		return nil, err
	}

	byType := map[model.StateType][]uint{}

	var unfinished []uint

	for _, state := range states {
		byType[state.Type] = append(byType[state.Type], state.ID)

		if !slices.Contains(model.FinishedStateTypes(), state.Type) {
			unfinished = append(unfinished, state.ID)
		}
	}

	now := s.now().UTC()
	overview := &TicketOverview{Queues: []QueueLoad{}}

	if err := s.countTheirs(ctx, caller, reached, unfinished, now, overview); err != nil {
		return nil, err
	}

	if overview.Queues, err = s.queueLoads(ctx, reached, unfinished, byType, now); err != nil {
		return nil, err
	}

	if overview.Days, err = s.dayLoads(ctx, caller, reached, now); err != nil {
		return nil, err
	}

	return overview, nil
}

// countTheirs counts the unfinished tickets that are the caller's, and every
// unfinished overdue ticket they reach. An empty list of states would ask for
// every state, so an installation with no unfinished state counts nothing
// rather than everything.
func (s *TicketApplicationService) countTheirs(
	ctx context.Context,
	caller *Principal,
	reached, unfinished []uint,
	now time.Time,
	overview *TicketOverview,
) error {
	if len(unfinished) == 0 {
		return nil
	}

	overdue, err := s.tickets.Count(ctx, repository.TicketFilter{
		QueueIDs: reached, StateIDs: unfinished, OverdueAt: &now,
	})
	if err != nil {
		return err
	}

	overview.Overdue = overdue

	if caller == nil || caller.User == nil {
		return nil
	}

	me := caller.User.ID

	for _, count := range []struct {
		into   *uint
		filter repository.TicketFilter
	}{
		{&overview.Owned, repository.TicketFilter{QueueIDs: reached, StateIDs: unfinished, OwnerID: &me}},
		{&overview.Responsible, repository.TicketFilter{QueueIDs: reached, StateIDs: unfinished, ResponsibleID: &me}},
		{&overview.Watched, repository.TicketFilter{QueueIDs: reached, StateIDs: unfinished, WatcherID: &me}},
	} {
		if *count.into, err = s.tickets.Count(ctx, count.filter); err != nil {
			return err
		}
	}

	return nil
}

// queueLoads counts each reached queue's unfinished tickets by where they
// stand, every queue reached listed even where it holds none.
func (s *TicketApplicationService) queueLoads(
	ctx context.Context,
	reached, unfinished []uint,
	byType map[model.StateType][]uint,
	now time.Time,
) ([]QueueLoad, error) {
	waiting := slices.Concat(byType[model.StateTypePendingReminder], byType[model.StateTypePendingAuto])

	overdue := statesIn(reached, unfinished)
	if overdue != nil {
		overdue.OverdueAt = &now
	}

	counted := make([]map[uint]uint, 4)

	for i, filter := range []*repository.TicketFilter{
		statesIn(reached, byType[model.StateTypeNew]),
		statesIn(reached, byType[model.StateTypeOpen]),
		statesIn(reached, waiting),
		overdue,
	} {
		counted[i] = map[uint]uint{}
		if filter == nil {
			continue
		}

		var err error
		if counted[i], err = s.tickets.CountByQueue(ctx, *filter); err != nil {
			return nil, err
		}
	}

	loads := make([]QueueLoad, 0, len(reached))
	for _, queue := range reached {
		loads = append(loads, QueueLoad{
			QueueID: queue, New: counted[0][queue], Open: counted[1][queue],
			Waiting: counted[2][queue], Overdue: counted[3][queue],
		})
	}

	return loads, nil
}

// statesIn is a filter for the tickets in some states of the reached queues -
// nil where there are no such states, because an empty list asks for them all.
func statesIn(reached, states []uint) *repository.TicketFilter {
	if len(states) == 0 {
		return nil
	}

	return &repository.TicketFilter{QueueIDs: reached, StateIDs: states}
}

// dayLoads counts the tickets opened and finished on each of the last
// OverviewDays days of the caller's calendar, today last.
func (s *TicketApplicationService) dayLoads(
	ctx context.Context,
	caller *Principal,
	reached []uint,
	now time.Time,
) ([]DayLoad, error) {
	zone := s.readerZone(ctx, caller)
	wall := now.In(zone)
	first := time.Date(wall.Year(), wall.Month(), wall.Day()-(OverviewDays-1), 0, 0, 0, 0, zone)

	days := make([]DayLoad, OverviewDays)
	for i := range days {
		days[i].Day = model.CalendarDay(first.AddDate(0, 0, i))
	}

	since := &repository.Span{From: &first}

	for _, counted := range []struct {
		moment repository.TicketMoment
		filter repository.TicketFilter
		add    func(*DayLoad)
	}{
		{repository.MomentOpened, repository.TicketFilter{QueueIDs: reached, Created: since},
			func(day *DayLoad) { day.Opened++ }},
		{repository.MomentClosed, repository.TicketFilter{QueueIDs: reached, Closed: since},
			func(day *DayLoad) { day.Closed++ }},
	} {
		moments, err := s.tickets.Moments(ctx, counted.filter, counted.moment)
		if err != nil {
			return nil, err
		}

		for _, at := range moments {
			// Counted between the days' midnights in UTC rather than between
			// moments in the zone, so that a day an hour short or long there is
			// still one day.
			index := int(model.CalendarDay(at.In(zone)).Sub(days[0].Day).Hours() / 24)
			if index >= 0 && index < OverviewDays {
				counted.add(&days[index])
			}
		}
	}

	return days, nil
}
