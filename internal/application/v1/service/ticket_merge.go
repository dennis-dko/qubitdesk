package service

import (
	"context"
	"strconv"

	"github.com/dennis-dko/qubitdesk/internal/application/v1/command"
	"github.com/dennis-dko/qubitdesk/internal/domain/model"
	"github.com/dennis-dko/qubitdesk/internal/support/apperror"
)

// MergeTicket folds a ticket into another, as OTRS's TicketMerge does: its
// articles, the time noted on it, its watchers and its links go to the other,
// the two are linked as parent and child, and the ticket is left merged and
// unlocked, naming the one it went into. Both change, so the caller works in
// both queues; a ticket is not merged into itself, and neither may be merged
// already.
//
// The other ticket's next answer stays owed as it was until it next changes.
// OTRS writes no note into the merged ticket either way here: the ticket names
// what it went into, and every screen says so in its reader's own words, where
// a note would be written once, in one language, for every reader after.
func (s *TicketApplicationService) MergeTicket(
	ctx context.Context,
	caller *Principal,
	id uint,
	cmd command.MergeTicketCommand,
) (*model.Ticket, error) {
	if cmd.IntoID == 0 || cmd.IntoID == id {
		return nil, apperror.InvalidFields("intoId")
	}

	merged, _, err := s.reached(ctx, caller, id, true)
	if err != nil {
		return nil, err
	}

	into, _, err := s.reached(ctx, caller, cmd.IntoID, true)
	if err != nil {
		return nil, err
	}

	standing, err := s.mergeable(ctx, merged)
	if err != nil {
		return nil, err
	}

	if _, err := s.mergeable(ctx, into); err != nil {
		return nil, err
	}

	mergedState, err := s.stateNamed(ctx, model.StateMerged)
	if err != nil {
		return nil, err
	}

	now := s.now().UTC()
	change := historyOf(merged.ID, now, caller)
	change.add(model.HistoryState, standing.Name, mergedState.Name)

	if merged.Locked {
		change.add(model.HistoryLock, strconv.FormatBool(true), strconv.FormatBool(false))
	}

	change.add(model.HistoryMerged, merged.Number, into.Number)

	told := historyOf(into.ID, now, caller)
	told.add(model.HistoryMerged, merged.Number, into.Number)

	if err := s.placeInState(merged, mergedState, nil, now); err != nil {
		return nil, err
	}

	merged.Locked, merged.LockedAt = false, nil
	merged.MergedInto = &into.ID
	merged.UpdatedAt = now

	if merged.UpdateDue, err = s.updateOwedNow(ctx, merged); err != nil {
		return nil, err
	}

	if err := s.tickets.Merge(ctx, merged, append(change.entries, told.entries...), callerID(caller)); err != nil {
		return nil, err
	}

	s.raise(ctx, caller, merged.ID, model.EventsOf(change.entries))

	return s.tickets.GetByID(ctx, merged.ID)
}

// mergeable is a ticket's state, or the refusal of a ticket merged already.
func (s *TicketApplicationService) mergeable(ctx context.Context, ticket *model.Ticket) (*model.TicketState, error) {
	state, err := s.states.GetByID(ctx, ticket.StateID)
	if err != nil {
		return nil, err
	}

	if state.Type == model.StateTypeMerged {
		return nil, apperror.Conflictf("ticket %s is merged already", ticket.Number).
			WithCode("ticketMergedAlready", ticket.Number)
	}

	return state, nil
}
