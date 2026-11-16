package service

import (
	"context"
	"slices"
	"strconv"
	"time"

	"github.com/dennis-dko/go-ticket-system/internal/domain/model"
)

// Housekeep does what a ticket is owed without anybody asking, KIX18's daemon
// tasks: a ticket waiting in a pending-auto state whose moment has come moves
// to the state the system configuration names, losing its lock where that
// state closes it, and a lock that has run as long as the queue allows is
// freed, for a ticket still new or open - a ticket that waits keeps its owner.
// Both are written into the history by the application rather than by an
// account, and it answers how many tickets it moved and how many locks it
// freed.
//
// Whoever changes a ticket while it runs has the last word. The tables ask
// again as they write whether the ticket still waits, or its lock has still
// run its time, and write only what the housekeeping changes: written whole, a
// ticket read a moment before would put back whatever somebody changed in that
// moment - a wait just made longer, a title, a lock just taken again.
func (s *TicketApplicationService) Housekeep(ctx context.Context, now time.Time) (int, int, error) {
	now = now.UTC()

	states, err := s.states.GetAll(ctx)
	if err != nil {
		return 0, 0, err
	}

	moved, err := s.endWaits(ctx, states, now)
	if err != nil {
		return moved, 0, err
	}

	freed, err := s.freeLocks(ctx, states, now)

	return moved, freed, err
}

// endWaits moves every ticket whose pending-auto wait is over on to the state
// its waiting state names, and frees the lock of one that state closes, as
// KIX18's pending check does.
func (s *TicketApplicationService) endWaits(ctx context.Context, states []*model.TicketState, now time.Time) (int, error) {
	due, err := s.tickets.PendingDue(ctx, ofType(states, model.StateTypePendingAuto), now)
	if err != nil || len(due) == 0 {
		return 0, err
	}

	closed, err := s.states.GetByName(ctx, model.StateClosed)
	if err != nil {
		return 0, err
	}

	moved := 0

	for _, ticket := range due {
		from := ticket.StateID
		next := nextAfter(states, from, closed)
		release := ticket.Locked && next.Type == model.StateTypeClosed

		change := historyBySystem(ticket.ID, now)
		change.add(model.HistoryState, s.stateName(states, from), next.Name)

		if release {
			change.add(model.HistoryLock, strconv.FormatBool(true), strconv.FormatBool(false))
		}

		if err := s.placeInState(ticket, next, nil, now); err != nil {
			return moved, err
		}

		ticket.UpdatedAt = now

		wrote, err := s.tickets.EndWait(ctx, ticket, from, release, now, change.entries)
		if err != nil {
			return moved, err
		}

		if wrote {
			moved++
		}
	}

	return moved, nil
}

// nextAfter is the state a waiting state moves a ticket on to - closed where it
// names none that can still end a wait, which the catalog's rules leave only a
// database changed by hand doing.
func nextAfter(states []*model.TicketState, from uint, closed *model.TicketState) *model.TicketState {
	for _, state := range states {
		if state.ID != from || state.NextStateID == nil {
			continue
		}

		for _, next := range states {
			if next.ID == *state.NextStateID && next.EndsAWait() {
				return next
			}
		}
	}

	return closed
}

// freeLocks frees every lock that has run as long as its ticket's queue
// allows, counted from when it was taken or last answered under; a queue
// allowing nothing frees nothing. Only a ticket in a state of a type the
// installation names loses its lock - new and open as it ships, as KIX18's
// Ticket::UnlockStateType does.
func (s *TicketApplicationService) freeLocks(ctx context.Context, states []*model.TicketState, now time.Time) (int, error) {
	locked, err := s.tickets.LockedTickets(ctx)
	if err != nil || len(locked) == 0 {
		return 0, err
	}

	queues, err := s.queues.GetAll(ctx)
	if err != nil {
		return 0, err
	}

	allowed := map[uint]time.Duration{}
	for _, queue := range queues {
		allowed[queue.ID] = time.Duration(queue.UnlockMinutes) * time.Minute
	}

	unlocking, err := s.unlockTypes.GetAll(ctx)
	if err != nil {
		return 0, err
	}

	types := make([]model.StateType, 0, len(unlocking))
	for _, entry := range unlocking {
		types = append(types, entry.Type)
	}

	working := ofType(states, types...)
	freed := 0

	for _, ticket := range locked {
		limit := allowed[ticket.QueueID]
		if limit <= 0 || !slices.Contains(working, ticket.StateID) || ticket.LockedAt == nil ||
			now.Sub(*ticket.LockedAt) < limit {
			continue
		}

		change := historyBySystem(ticket.ID, now)
		change.add(model.HistoryLock, strconv.FormatBool(true), strconv.FormatBool(false))

		wrote, err := s.tickets.FreeLock(ctx, ticket.ID, working, limit, now, change.entries)
		if err != nil {
			return freed, err
		}

		if wrote {
			freed++
		}
	}

	return freed, nil
}

// ofType lists the states of the types given, by id.
func ofType(states []*model.TicketState, types ...model.StateType) []uint {
	var ids []uint

	for _, state := range states {
		if slices.Contains(types, state.Type) {
			ids = append(ids, state.ID)
		}
	}

	return ids
}

// stateName is a state's name for the history.
func (s *TicketApplicationService) stateName(states []*model.TicketState, id uint) string {
	for _, state := range states {
		if state.ID == id {
			return state.Name
		}
	}

	return ""
}
