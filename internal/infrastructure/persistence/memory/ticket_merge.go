package memory

import (
	"context"
	"strconv"

	"github.com/dennis-dko/qubitdesk/internal/domain/model"
	"github.com/dennis-dko/qubitdesk/internal/support/apperror"
)

// Merge folds a ticket into the one it names as MergedInto: its articles, the
// files that came with them, the time noted on it and its watchers. The links
// are the link repository's, which this one does not hold.
func (r *TicketRepository) Merge(_ context.Context, merged *model.Ticket, history []model.HistoryEntry, _ *uint) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if merged.MergedInto == nil {
		return apperror.InvalidFields("intoId")
	}

	into, found := r.tickets[*merged.MergedInto]
	if !found {
		return apperror.NotFound("ticket", strconv.FormatUint(uint64(*merged.MergedInto), 10))
	}

	if _, found := r.tickets[merged.ID]; !found {
		return apperror.NotFound("ticket", strconv.FormatUint(uint64(merged.ID), 10))
	}

	for i := range r.articles {
		if r.articles[i].TicketID == merged.ID {
			r.articles[i].TicketID = into.ID
		}
	}

	for i := range r.attachments {
		if r.attachments[i].TicketID == merged.ID {
			r.attachments[i].TicketID = into.ID
		}
	}

	for i := range r.time {
		if r.time[i].TicketID == merged.ID {
			r.time[i].TicketID = into.ID
		}
	}

	for user := range r.watchers[merged.ID] {
		if r.watchers[into.ID] == nil {
			r.watchers[into.ID] = map[uint]bool{}
		}

		r.watchers[into.ID][user] = true
	}

	delete(r.watchers, merged.ID)

	r.tickets[merged.ID] = *merged
	into.UpdatedAt = merged.UpdatedAt
	r.tickets[into.ID] = into
	r.addHistory(history)

	return nil
}
