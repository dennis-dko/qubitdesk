package sqldb

import (
	"context"
	"slices"
	"strconv"

	"github.com/dennis-dko/qubitdesk/internal/domain/model"
	"github.com/dennis-dko/qubitdesk/internal/support/apperror"
)

// Merge folds a ticket into the one it names as MergedInto. What it moves is
// read before it is moved, rather than deleted through a subquery on the same
// table, which MySQL refuses.
func (r *TicketRepository) Merge(
	ctx context.Context,
	merged *model.Ticket,
	history []model.HistoryEntry,
	by *uint,
) error {
	if merged.MergedInto == nil {
		return apperror.InvalidFields("intoId")
	}

	into := *merged.MergedInto

	var linker uint
	if by != nil {
		linker = *by
	}

	return r.withTx(ctx, func(tx base) error {
		for _, move := range []string{
			"UPDATE articles SET ticket_id = ? WHERE ticket_id = ?",
			"UPDATE time_accounting SET ticket_id = ? WHERE ticket_id = ?",
		} {
			if _, err := tx.exec(ctx, move, into, merged.ID); err != nil {
				return apperror.Internal(err)
			}
		}

		if err := mergeWatchers(ctx, tx, merged); err != nil {
			return err
		}

		if err := mergeLinks(ctx, tx, merged); err != nil {
			return err
		}

		if _, err := tx.insert(ctx, "INSERT INTO links (source_object, source_id, target_object, target_id, type, "+
			"created_at, created_by) VALUES (?, ?, ?, ?, ?, ?, ?)", model.LinkTicket, into, model.LinkTicket,
			merged.ID, model.LinkParentChild, merged.UpdatedAt, linker); err != nil {
			return apperror.Internal(err)
		}

		if err := writeTicket(ctx, tx, merged); err != nil {
			return err
		}

		if _, err := tx.exec(ctx, "UPDATE tickets SET merged_into = ? WHERE id = ?", into, merged.ID); err != nil {
			return apperror.Internal(err)
		}

		found, err := tx.update(ctx, "tickets", "UPDATE tickets SET updated_at = ? WHERE id = ?", into,
			merged.UpdatedAt, into)
		if err != nil {
			return apperror.Internal(err)
		}

		if !found {
			return apperror.NotFound("ticket", strconv.FormatUint(uint64(into), 10))
		}

		return insertHistory(ctx, tx, history)
	})
}

// writeTicket writes a ticket's row as given, through the connection or the
// transaction given.
func writeTicket(ctx context.Context, tx base, ticket *model.Ticket) error {
	found, err := tx.update(ctx, "tickets",
		"UPDATE tickets SET title = ?, queue_id = ?, state_id = ?, priority_id = ?, type_id = ?, owner_id = ?, "+
			"responsible_id = ?, contact_id = ?, organisation_id = ?, locked = ?, locked_at = ?, updated_at = ?, "+
			"closed_at = ?, pending_until = ?, service_level_id = ?, first_response_due = ?, solution_due = ?, "+
			"update_due = ? WHERE id = ?",
		ticket.ID,
		ticket.Title, ticket.QueueID, ticket.StateID, ticket.PriorityID, ticket.TypeID, ticket.OwnerID,
		ticket.ResponsibleID, ticket.ContactID, ticket.OrganisationID, ticket.Locked, ticket.LockedAt,
		ticket.UpdatedAt, ticket.ClosedAt, ticket.PendingUntil,
		ticket.ServiceLevelID, ticket.FirstResponseDue, ticket.SolutionDue, ticket.UpdateDue, ticket.ID)
	if err != nil {
		return apperror.Internal(err)
	}

	if !found {
		return apperror.NotFound("ticket", strconv.FormatUint(uint64(ticket.ID), 10))
	}

	return nil
}

// mergeWatchers hands the merged ticket's watchers to the other, each once:
// one already watching the other keeps that and loses the merged ticket.
func mergeWatchers(ctx context.Context, tx base, merged *model.Ticket) error {
	into := *merged.MergedInto

	watching, err := watchersThrough(ctx, tx, into)
	if err != nil {
		return err
	}

	moving, err := watchersThrough(ctx, tx, merged.ID)
	if err != nil {
		return err
	}

	for _, user := range moving {
		statement := "UPDATE ticket_watchers SET ticket_id = ? WHERE ticket_id = ? AND user_id = ?"
		args := []any{into, merged.ID, user}

		if slices.Contains(watching, user) {
			statement, args = "DELETE FROM ticket_watchers WHERE ticket_id = ? AND user_id = ?", []any{merged.ID, user}
		}

		if _, err := tx.exec(ctx, statement, args...); err != nil {
			return apperror.Internal(err)
		}
	}

	return nil
}

// watchersThrough are the accounts watching a ticket, read through the
// transaction given.
func watchersThrough(ctx context.Context, tx base, ticketID uint) ([]uint, error) {
	rows, err := tx.db.QueryContext(ctx, tx.rebind("SELECT user_id FROM ticket_watchers WHERE ticket_id = ?"),
		ticketID)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	defer func() { _ = rows.Close() }()

	var users []uint

	for rows.Next() {
		var user uint
		if err := rows.Scan(&user); err != nil {
			return nil, apperror.Internal(err)
		}

		users = append(users, user)
	}

	if err := rows.Err(); err != nil {
		return nil, apperror.Internal(err)
	}

	return users, nil
}

// linkEnd is one end of a link: a kind of record and its id.
type linkEnd struct {
	object string
	id     uint
}

// ticketLink is a link that has a ticket at one of its ends.
type ticketLink struct {
	id             uint
	source, target linkEnd
}

// far is the end of a link that is not the ticket given.
func (l ticketLink) far(ticketID uint) linkEnd {
	if l.source == (linkEnd{string(model.LinkTicket), ticketID}) {
		return l.target
	}

	return l.source
}

// mergeLinks hands the merged ticket's links to the other, as OTRS's
// TicketMergeLinkedObjects does. A link between the two goes, and so does one
// to a record the other is linked to already, because two records are joined
// once; the rest are moved.
func mergeLinks(ctx context.Context, tx base, merged *model.Ticket) error {
	into := *merged.MergedInto

	held, err := linksOfTicket(ctx, tx, into)
	if err != nil {
		return err
	}

	moving, err := linksOfTicket(ctx, tx, merged.ID)
	if err != nil {
		return err
	}

	joined := map[linkEnd]bool{{string(model.LinkTicket), into}: true}
	for _, link := range held {
		joined[link.far(into)] = true
	}

	for _, link := range moving {
		far := link.far(merged.ID)

		statement := "UPDATE links SET target_id = ? WHERE id = ?"
		args := []any{into, link.id}

		switch {
		case joined[far]:
			statement, args = "DELETE FROM links WHERE id = ?", []any{link.id}
		case link.source == (linkEnd{string(model.LinkTicket), merged.ID}):
			statement = "UPDATE links SET source_id = ? WHERE id = ?"
		}

		if _, err := tx.exec(ctx, statement, args...); err != nil {
			return apperror.Internal(err)
		}

		joined[far] = true
	}

	return nil
}

// linksOfTicket are the links a ticket is an end of, read through the
// transaction given.
func linksOfTicket(ctx context.Context, tx base, ticketID uint) ([]ticketLink, error) {
	rows, err := tx.db.QueryContext(ctx, tx.rebind("SELECT id, source_object, source_id, target_object, target_id "+
		"FROM links WHERE (source_object = ? AND source_id = ?) OR (target_object = ? AND target_id = ?)"),
		model.LinkTicket, ticketID, model.LinkTicket, ticketID)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	defer func() { _ = rows.Close() }()

	var links []ticketLink

	for rows.Next() {
		var link ticketLink
		if err := rows.Scan(&link.id, &link.source.object, &link.source.id, &link.target.object,
			&link.target.id); err != nil {
			return nil, apperror.Internal(err)
		}

		links = append(links, link)
	}

	if err := rows.Err(); err != nil {
		return nil, apperror.Internal(err)
	}

	return links, nil
}
