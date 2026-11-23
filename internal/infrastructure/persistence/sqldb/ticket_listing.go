package sqldb

import (
	"context"
	"strings"
	"time"

	"github.com/dennis-dko/qubitdesk/internal/domain/model"
	"github.com/dennis-dko/qubitdesk/internal/domain/repository"
	"github.com/dennis-dko/qubitdesk/internal/support/apperror"
)

// List reads one page of the tickets a filter allows and how many it allows.
func (r *TicketRepository) List(ctx context.Context, filter repository.TicketFilter) ([]*model.Ticket, uint, error) {
	if len(filter.QueueIDs) == 0 {
		return []*model.Ticket{}, 0, nil
	}

	condition, args := ticketConditions(filter)

	var total uint

	if err := r.db.QueryRowContext(ctx,
		r.rebind("SELECT COUNT(*) FROM tickets WHERE "+condition), args...).Scan(&total); err != nil {
		return nil, 0, apperror.Internal(err)
	}

	page := max(filter.Page, 1)
	args = append(args, filter.Limit, (page-1)*filter.Limit)

	rows, err := r.db.QueryContext(ctx, r.rebind(
		"SELECT "+ticketColumns+" FROM tickets WHERE "+condition+" ORDER BY "+ticketOrder(filter.Sort)+" LIMIT ? OFFSET ?"),
		args...)
	if err != nil {
		return nil, 0, apperror.Internal(err)
	}
	defer func() { _ = rows.Close() }()

	tickets := make([]*model.Ticket, 0, filter.Limit)

	for rows.Next() {
		ticket, scanErr := scanTicket(rows)
		if scanErr != nil {
			return nil, 0, apperror.Internal(scanErr)
		}

		tickets = append(tickets, ticket)
	}

	if err := rows.Err(); err != nil {
		return nil, 0, apperror.Internal(err)
	}

	return tickets, total, nil
}

func (r *TicketRepository) Count(ctx context.Context, filter repository.TicketFilter) (uint, error) {
	if len(filter.QueueIDs) == 0 {
		return 0, nil
	}

	condition, args := ticketConditions(filter)

	var total uint

	if err := r.db.QueryRowContext(ctx,
		r.rebind("SELECT COUNT(*) FROM tickets WHERE "+condition), args...).Scan(&total); err != nil {
		return 0, apperror.Internal(err)
	}

	return total, nil
}

func (r *TicketRepository) CountByQueue(ctx context.Context, filter repository.TicketFilter) (map[uint]uint, error) {
	counted := map[uint]uint{}
	if len(filter.QueueIDs) == 0 {
		return counted, nil
	}

	condition, args := ticketConditions(filter)

	rows, err := r.db.QueryContext(ctx,
		r.rebind("SELECT queue_id, COUNT(*) FROM tickets WHERE "+condition+" GROUP BY queue_id"), args...)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var queue, count uint
		if err := rows.Scan(&queue, &count); err != nil {
			return nil, apperror.Internal(err)
		}

		counted[queue] = count
	}

	if err := rows.Err(); err != nil {
		return nil, apperror.Internal(err)
	}

	return counted, nil
}

func (r *TicketRepository) Moments(
	ctx context.Context,
	filter repository.TicketFilter,
	moment repository.TicketMoment,
) ([]time.Time, error) {
	if len(filter.QueueIDs) == 0 {
		return []time.Time{}, nil
	}

	column := "created_at"
	if moment == repository.MomentClosed {
		column = "closed_at"
	}

	condition, args := ticketConditions(filter)

	rows, err := r.db.QueryContext(ctx, r.rebind(
		"SELECT "+column+" FROM tickets WHERE "+condition+" AND "+column+" IS NOT NULL"), args...)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	defer func() { _ = rows.Close() }()

	moments := []time.Time{}

	for rows.Next() {
		var at time.Time
		if err := rows.Scan(&at); err != nil {
			return nil, apperror.Internal(err)
		}

		moments = append(moments, at)
	}

	if err := rows.Err(); err != nil {
		return nil, apperror.Internal(err)
	}

	return moments, nil
}

// ticketConditions is the WHERE a filter asks for and its arguments, for a
// filter naming at least one queue. The conditions are put together from a
// fixed set of clauses, each written out whole with its placeholders; only how
// many placeholders an IN list has comes from the filter, and never a value.
func ticketConditions(filter repository.TicketFilter) (string, []any) {
	where := []string{"queue_id IN " + marks(len(filter.QueueIDs))}
	args := make([]any, 0, len(filter.QueueIDs)+len(filter.StateIDs)+3)

	for _, id := range filter.QueueIDs {
		args = append(args, id)
	}

	if len(filter.StateIDs) > 0 {
		where = append(where, "state_id IN "+marks(len(filter.StateIDs)))

		for _, id := range filter.StateIDs {
			args = append(args, id)
		}
	}

	if filter.OwnerID != nil {
		where = append(where, "owner_id = ?")
		args = append(args, *filter.OwnerID)
	}

	if filter.ContactID != nil {
		where = append(where, "contact_id = ?")
		args = append(args, *filter.ContactID)
	}

	if filter.OrganisationID != nil {
		where = append(where, "organisation_id = ?")
		args = append(args, *filter.OrganisationID)
	}

	switch follower := filter.Follower; {
	case follower == nil:
	case follower.OrganisationID != nil:
		where = append(where, "(contact_id = ? OR organisation_id = ?)")
		args = append(args, follower.ContactID, *follower.OrganisationID)
	default:
		where = append(where, "contact_id = ?")
		args = append(args, follower.ContactID)
	}

	if search := strings.TrimSpace(filter.Search); search != "" {
		where = append(where, "(LOWER(title) LIKE ? ESCAPE '!' OR number LIKE ? ESCAPE '!')")
		pattern := "%" + strings.ToLower(likeEscaped(search)) + "%"
		args = append(args, pattern, pattern)
	}

	where, args = narrowedFurther(filter, where, args)

	return strings.Join(where, " AND "), args
}

// narrowedFurther adds the conditions of a search beyond the queues, states,
// people and words a plain listing knows. Each is a clause written out whole;
// only how many placeholders a list has comes from the filter.
//
// A moment is compared in UTC, because SQLite keeps one as the text the driver
// writes and compares that text: the tickets' moments are written in UTC, and
// the same moment written with another offset sorts as another time.
func narrowedFurther(filter repository.TicketFilter, where []string, args []any) ([]string, []any) {
	for _, in := range []struct {
		column string
		ids    []uint
	}{{"priority_id", filter.PriorityIDs}, {"type_id", filter.TypeIDs}} {
		if len(in.ids) == 0 {
			continue
		}

		where = append(where, in.column+" IN "+marks(len(in.ids)))

		for _, id := range in.ids {
			args = append(args, id)
		}
	}

	if filter.ResponsibleID != nil {
		where = append(where, "responsible_id = ?")
		args = append(args, *filter.ResponsibleID)
	}

	if filter.WatcherID != nil {
		where = append(where, "EXISTS (SELECT 1 FROM ticket_watchers w WHERE w.ticket_id = tickets.id AND w.user_id = ?)")
		args = append(args, *filter.WatcherID)
	}

	if filter.Locked != nil {
		where = append(where, "locked = ?")
		args = append(args, *filter.Locked)
	}

	if filter.ServiceLevelID != nil {
		where = append(where, "service_level_id = ?")
		args = append(args, *filter.ServiceLevelID)
	}

	for _, in := range []struct {
		column string
		span   *repository.Span
	}{{"created_at", filter.Created}, {"updated_at", filter.Changed}, {"closed_at", filter.Closed}} {
		if in.span == nil {
			continue
		}

		if in.span.From != nil {
			where = append(where, in.column+" >= ?")
			args = append(args, in.span.From.UTC())
		}

		if in.span.Before != nil {
			where = append(where, in.column+" < ?")
			args = append(args, in.span.Before.UTC())
		}
	}

	if text := filter.Text; text != nil {
		articles := "SELECT 1 FROM articles a WHERE a.ticket_id = tickets.id AND "
		if text.CustomerArticlesOnly {
			articles += "a.customer_visible = ? AND "
		}

		clause := "(LOWER(title) LIKE ? ESCAPE '!' OR number LIKE ? ESCAPE '!' OR EXISTS (" + articles +
			"(LOWER(a.subject) LIKE ? ESCAPE '!' OR LOWER(a.body) LIKE ? ESCAPE '!')))"

		for _, word := range text.Words {
			pattern := "%" + strings.ToLower(likeEscaped(word)) + "%"
			where = append(where, clause)
			args = append(args, pattern, pattern)

			if text.CustomerArticlesOnly {
				args = append(args, true)
			}

			args = append(args, pattern, pattern)
		}
	}

	if at := filter.OverdueAt; at != nil {
		where = append(where, "((first_response_due IS NOT NULL AND first_response_at IS NULL AND first_response_due < ?) "+
			"OR (update_due IS NOT NULL AND update_due < ?) "+
			"OR (solution_due IS NOT NULL AND closed_at IS NULL AND solution_due < ?))")
		args = append(args, at.UTC(), at.UTC(), at.UTC())
	}

	return where, args
}

// ticketOrder is the ORDER BY a sort asks for, chosen from a fixed set rather
// than written from it.
func ticketOrder(sort repository.TicketSort) string {
	direction := "DESC"
	if sort.Ascending {
		direction = "ASC"
	}

	switch sort.By {
	case repository.SortByCreated:
		return "created_at " + direction + ", id " + direction
	case repository.SortByPriority:
		// Level 1 is the most urgent, so urgency runs against the level.
		urgency := "ASC"
		if sort.Ascending {
			urgency = "DESC"
		}

		return "(SELECT p.level FROM ticket_priorities p WHERE p.id = tickets.priority_id) " + urgency +
			", created_at ASC, id ASC"
	default:
		return "updated_at " + direction + ", id " + direction
	}
}
