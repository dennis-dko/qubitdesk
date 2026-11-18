package memory

import (
	"context"
	"slices"
	"time"

	"github.com/dennis-dko/go-ticket-system/internal/domain/model"
)

func (r *TicketRepository) PendingDue(_ context.Context, stateIDs []uint, now time.Time) ([]*model.Ticket, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	found := make([]*model.Ticket, 0)

	for _, ticket := range r.tickets {
		if slices.Contains(stateIDs, ticket.StateID) && ticket.PendingUntil != nil && !ticket.PendingUntil.After(now) {
			copied := ticket
			found = append(found, &copied)
		}
	}

	slices.SortFunc(found, func(a, b *model.Ticket) int { return int(a.ID) - int(b.ID) })

	return found, nil
}

func (r *TicketRepository) EndWait(
	_ context.Context,
	ticket *model.Ticket,
	from uint,
	release bool,
	now time.Time,
	history []model.HistoryEntry,
) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	stored, ok := r.tickets[ticket.ID]
	if !ok || stored.StateID != from || stored.PendingUntil == nil || stored.PendingUntil.After(now) ||
		(release && !stored.Locked) {
		return false, nil
	}

	stored.StateID, stored.ClosedAt, stored.PendingUntil, stored.UpdatedAt = ticket.StateID, ticket.ClosedAt, nil, ticket.UpdatedAt
	stored.UpdateDue = ticket.UpdateDue
	if release {
		stored.Locked, stored.LockedAt = false, nil
	}

	r.tickets[ticket.ID] = stored
	r.addHistory(history)

	return true, nil
}

func (r *TicketRepository) FreeLock(
	_ context.Context,
	id uint,
	lockedBy time.Time,
	working []uint,
	now time.Time,
	history []model.HistoryEntry,
) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	stored, ok := r.tickets[id]
	if !ok || !stored.Locked || !slices.Contains(working, stored.StateID) ||
		stored.LockedAt == nil || stored.LockedAt.After(lockedBy) {
		return false, nil
	}

	stored.Locked, stored.LockedAt, stored.UpdatedAt = false, nil, now
	r.tickets[id] = stored
	r.addHistory(history)

	return true, nil
}

func (r *TicketRepository) LockedTickets(_ context.Context) ([]*model.Ticket, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	found := make([]*model.Ticket, 0)

	for _, ticket := range r.tickets {
		if ticket.Locked {
			copied := ticket
			found = append(found, &copied)
		}
	}

	slices.SortFunc(found, func(a, b *model.Ticket) int { return int(a.ID) - int(b.ID) })

	return found, nil
}

func (r *TicketRepository) Matching(_ context.Context, criteria model.TicketCriteria, limit int) ([]uint, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	ids := make([]uint, 0)

	for _, ticket := range r.tickets {
		if criteria.Matches(&ticket) {
			ids = append(ids, ticket.ID)
		}
	}

	slices.Sort(ids)

	if len(ids) > limit {
		ids = ids[:limit]
	}

	return ids, nil
}
