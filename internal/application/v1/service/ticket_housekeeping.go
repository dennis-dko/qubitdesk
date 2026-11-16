package service

import (
	"context"
	"slices"
	"strconv"
	"time"

	"github.com/dennis-dko/go-ticket-system/internal/domain/model"
	"github.com/dennis-dko/go-ticket-system/internal/support/apperror"
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

// endWaits moves every ticket whose pending-auto wait is over, and frees the
// lock of one its next state closes, as KIX18's pending check does.
func (s *TicketApplicationService) endWaits(ctx context.Context, states []*model.TicketState, now time.Time) (int, error) {
	due, err := s.tickets.PendingDue(ctx, ofType(states, model.StateTypePendingAuto), now)
	if err != nil || len(due) == 0 {
		return 0, err
	}

	next, err := s.stateAfterPending(ctx)
	if err != nil {
		return 0, err
	}

	moved := 0

	for _, ticket := range due {
		from, release := ticket.StateID, ticket.Locked && next.Type == model.StateTypeClosed

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

// stateAfterPending is the state the system configuration names for a wait
// that is over - closed unless it says otherwise - and closed again where the
// state it names can no longer end one, renamed or retired since it was named.
func (s *TicketApplicationService) stateAfterPending(ctx context.Context) (*model.TicketState, error) {
	name := model.StateClosed

	if s.config != nil {
		configured, err := s.config.String(ctx, model.ConfigTicketStateAfterPending)
		if err != nil {
			return nil, err
		}

		name = configured
	}

	state, err := s.states.GetByName(ctx, name)
	if err != nil && apperror.KindOf(err) != apperror.KindNotFound {
		return nil, err
	}

	if err == nil && endsAWait(state) {
		return state, nil
	}

	return s.states.GetByName(ctx, model.StateClosed)
}

// CanEndAWait refuses a state the system configuration may not name for a
// ticket whose wait is over: one that is not there or no longer offered, one
// that waits itself, where the wait would never end, and merged, which only
// merging reaches. Stored, a misspelt name would quietly have closed every
// ticket instead.
func (s *TicketApplicationService) CanEndAWait(ctx context.Context, name string) error {
	state, err := s.states.GetByName(ctx, name)
	if apperror.KindOf(err) == apperror.KindNotFound {
		return apperror.InvalidFields("value")
	}

	if err != nil {
		return err
	}

	if !endsAWait(state) {
		return apperror.InvalidFields("value")
	}

	return nil
}

// endsAWait says whether a ticket whose wait is over may move to a state.
func endsAWait(state *model.TicketState) bool {
	return state.Valid && !slices.Contains([]model.StateType{
		model.StateTypePendingAuto, model.StateTypePendingReminder, model.StateTypeMerged,
	}, state.Type)
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
