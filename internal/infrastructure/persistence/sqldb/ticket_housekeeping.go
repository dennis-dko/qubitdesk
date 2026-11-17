package sqldb

import (
	"context"
	"time"

	"github.com/dennis-dko/go-ticket-system/internal/domain/model"
	"github.com/dennis-dko/go-ticket-system/internal/support/apperror"
)

func (r *TicketRepository) PendingDue(ctx context.Context, stateIDs []uint, now time.Time) ([]*model.Ticket, error) {
	if len(stateIDs) == 0 {
		return []*model.Ticket{}, nil
	}

	args := make([]any, 0, len(stateIDs)+1)
	for _, id := range stateIDs {
		args = append(args, id)
	}

	args = append(args, now)

	return r.tickets(ctx, "SELECT "+ticketColumns+" FROM tickets WHERE state_id IN "+marks(len(stateIDs))+
		" AND pending_until IS NOT NULL AND pending_until <= ? ORDER BY id", args...)
}

func (r *TicketRepository) LockedTickets(ctx context.Context) ([]*model.Ticket, error) {
	return r.tickets(ctx, "SELECT "+ticketColumns+" FROM tickets WHERE locked = ? ORDER BY id", true)
}

func (r *TicketRepository) EndWait(
	ctx context.Context,
	ticket *model.Ticket,
	from uint,
	release bool,
	now time.Time,
	history []model.HistoryEntry,
) (bool, error) {
	if !release {
		return r.guarded(ctx, history,
			"UPDATE tickets SET state_id = ?, closed_at = ?, pending_until = NULL, updated_at = ? "+
				"WHERE id = ? AND state_id = ? AND pending_until IS NOT NULL AND pending_until <= ?",
			ticket.StateID, ticket.ClosedAt, ticket.UpdatedAt, ticket.ID, from, now)
	}

	// The lock goes only while it is still held, or the history would say a
	// lock was freed that was not there.
	return r.guarded(ctx, history,
		"UPDATE tickets SET state_id = ?, closed_at = ?, pending_until = NULL, locked = ?, locked_at = NULL, "+
			"updated_at = ? WHERE id = ? AND state_id = ? AND pending_until IS NOT NULL AND pending_until <= ? "+
			"AND locked = ?",
		ticket.StateID, ticket.ClosedAt, false, ticket.UpdatedAt, ticket.ID, from, now, true)
}

func (r *TicketRepository) FreeLock(
	ctx context.Context,
	id uint,
	lockedBy time.Time,
	working []uint,
	now time.Time,
	history []model.HistoryEntry,
) (bool, error) {
	if len(working) == 0 {
		return false, nil
	}

	args := []any{false, now, id, true}
	for _, state := range working {
		args = append(args, state)
	}

	args = append(args, lockedBy)

	return r.guarded(ctx, history,
		"UPDATE tickets SET locked = ?, locked_at = NULL, updated_at = ? "+
			"WHERE id = ? AND locked = ? AND state_id IN "+marks(len(working))+" AND locked_at <= ?", args...)
}

// guarded runs an UPDATE whose condition asks again what made the housekeeping
// pick the ticket, and writes the history only where it changed the row: the
// ticket may have changed since it was read, and writing it back whole would
// put back what somebody changed in the meantime. It counts with exec rather
// than update: a row the condition matches always changes, so the rows MySQL
// says it changed are the rows it matched.
func (r *TicketRepository) guarded(ctx context.Context, history []model.HistoryEntry, query string, args ...any) (bool, error) {
	changed := false

	err := r.withTx(ctx, func(tx base) error {
		affected, err := tx.exec(ctx, query, args...)
		if err != nil {
			return apperror.Internal(err)
		}

		if affected == 0 {
			return nil
		}

		changed = true

		return insertHistory(ctx, tx, history)
	})
	if err != nil {
		return false, err
	}

	return changed, nil
}

// tickets reads the tickets a query finds.
func (r *TicketRepository) tickets(ctx context.Context, query string, args ...any) ([]*model.Ticket, error) {
	rows, err := r.db.QueryContext(ctx, r.rebind(query), args...)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	defer func() { _ = rows.Close() }()

	found := make([]*model.Ticket, 0)

	for rows.Next() {
		ticket, scanErr := scanTicket(rows)
		if scanErr != nil {
			return nil, apperror.Internal(scanErr)
		}

		found = append(found, ticket)
	}

	if err := rows.Err(); err != nil {
		return nil, apperror.Internal(err)
	}

	return found, nil
}

func (r *TicketRepository) Matching(ctx context.Context, criteria model.TicketCriteria, limit int) ([]uint, error) {
	query := "SELECT id FROM tickets WHERE 1 = 1"
	args := make([]any, 0)

	for _, named := range []struct {
		column string
		ids    []uint
	}{
		{"queue_id", criteria.QueueIDs},
		{"state_id", criteria.StateIDs},
		{"priority_id", criteria.PriorityIDs},
		{"type_id", criteria.TypeIDs},
	} {
		if len(named.ids) == 0 {
			continue
		}

		query += " AND " + named.column + " IN " + marks(len(named.ids))
		for _, id := range named.ids {
			args = append(args, id)
		}
	}

	if criteria.Locked != nil {
		query += " AND locked = ?"
		args = append(args, *criteria.Locked)
	}

	rows, err := r.db.QueryContext(ctx, r.rebind(query+" ORDER BY id LIMIT ?"), append(args, limit)...)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	defer func() { _ = rows.Close() }()

	ids := make([]uint, 0)

	for rows.Next() {
		var id uint
		if err := rows.Scan(&id); err != nil {
			return nil, apperror.Internal(err)
		}

		ids = append(ids, id)
	}

	if err := rows.Err(); err != nil {
		return nil, apperror.Internal(err)
	}

	return ids, nil
}
