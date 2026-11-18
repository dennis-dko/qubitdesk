package sqldb

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/dennis-dko/go-ticket-system/internal/domain/model"
	"github.com/dennis-dko/go-ticket-system/internal/domain/repository"
	"github.com/dennis-dko/go-ticket-system/internal/support/apperror"
)

// TicketRepository stores the tickets, what is written in them, the time noted on
// them and their history.
type TicketRepository struct {
	base
}

// NewTicketRepository creates a ticket repository for the given dialect.
func NewTicketRepository(db DB, dialect string) *TicketRepository {
	return &TicketRepository{base{db: db, dialect: dialect}}
}

var _ repository.TicketRepository = (*TicketRepository)(nil)

const ticketColumns = "id, number, title, queue_id, state_id, priority_id, type_id, owner_id, responsible_id, " +
	"contact_id, organisation_id, locked, locked_at, channel, created_by, created_at, updated_at, closed_at, " +
	"pending_until, service_level_id, first_response_due, first_response_at, solution_due, update_due"

func (r *TicketRepository) Create(
	ctx context.Context,
	ticket *model.Ticket,
	first *model.Article,
	minutes int,
	numberOf func(id uint) string,
) (*model.Ticket, error) {
	var id uint

	err := r.withTx(ctx, func(tx base) error {
		newID, err := tx.insert(ctx,
			"INSERT INTO tickets (title, queue_id, state_id, priority_id, type_id, owner_id, responsible_id, "+
				"contact_id, organisation_id, locked, locked_at, channel, created_by, created_at, updated_at, "+
				"closed_at, pending_until, service_level_id, first_response_due, solution_due, update_due) "+
				"VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
			ticket.Title, ticket.QueueID, ticket.StateID, ticket.PriorityID, ticket.TypeID,
			ticket.OwnerID, ticket.ResponsibleID, ticket.ContactID, ticket.OrganisationID,
			ticket.Locked, ticket.LockedAt, string(ticket.Channel),
			ticket.CreatedBy, ticket.CreatedAt, ticket.UpdatedAt, ticket.ClosedAt, ticket.PendingUntil,
			ticket.ServiceLevelID, ticket.FirstResponseDue, ticket.SolutionDue, ticket.UpdateDue)
		if err != nil {
			return apperror.Internal(err)
		}

		id = newID

		if _, err := tx.exec(ctx, "UPDATE tickets SET number = ? WHERE id = ?", numberOf(newID), newID); err != nil {
			return apperror.Internal(err)
		}

		first.TicketID = newID

		if _, err := insertArticle(ctx, tx, first, minutes); err != nil {
			return err
		}

		return insertHistory(ctx, tx, []model.HistoryEntry{{
			TicketID: newID, At: ticket.CreatedAt, Actor: first.Sender, ActorID: ticket.CreatedBy,
			Event: model.HistoryCreated, NewValue: ticket.Title,
		}})
	})
	if err != nil {
		return nil, err
	}

	return r.GetByID(ctx, id)
}

func (r *TicketRepository) GetByID(ctx context.Context, id uint) (*model.Ticket, error) {
	ticket, err := scanTicket(r.db.QueryRowContext(ctx,
		r.rebind("SELECT "+ticketColumns+" FROM tickets WHERE id = ?"), id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, apperror.NotFound("ticket", strconv.FormatUint(uint64(id), 10))
	}

	if err != nil {
		return nil, apperror.Internal(err)
	}

	return ticket, nil
}

// List reads one page of the tickets a filter allows and how many it allows.
//
// The conditions are put together from a fixed set of clauses, each written out
// whole with its placeholders; only how many placeholders an IN list has comes
// from the filter, and never a value.
func (r *TicketRepository) List(ctx context.Context, filter repository.TicketFilter) ([]*model.Ticket, uint, error) {
	if len(filter.QueueIDs) == 0 {
		return []*model.Ticket{}, 0, nil
	}

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

	condition := strings.Join(where, " AND ")

	var total uint

	if err := r.db.QueryRowContext(ctx,
		r.rebind("SELECT COUNT(*) FROM tickets WHERE "+condition), args...).Scan(&total); err != nil {
		return nil, 0, apperror.Internal(err)
	}

	page := max(filter.Page, 1)
	args = append(args, filter.Limit, (page-1)*filter.Limit)

	rows, err := r.db.QueryContext(ctx, r.rebind(
		"SELECT "+ticketColumns+" FROM tickets WHERE "+condition+" ORDER BY updated_at DESC, id DESC LIMIT ? OFFSET ?"),
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

func (r *TicketRepository) Update(
	ctx context.Context,
	ticket *model.Ticket,
	history []model.HistoryEntry,
) (*model.Ticket, error) {
	err := r.withTx(ctx, func(tx base) error {
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

		return insertHistory(ctx, tx, history)
	})
	if err != nil {
		return nil, err
	}

	return r.GetByID(ctx, ticket.ID)
}

func (r *TicketRepository) AddArticle(
	ctx context.Context,
	article *model.Article,
	minutes int,
	history []model.HistoryEntry,
	firstResponse *time.Time,
	renewsLock bool,
	updateDue *time.Time,
) (*model.Article, error) {
	var id uint

	err := r.withTx(ctx, func(tx base) error {
		// COALESCE keeps the moment a ticket was first answered: a later answer
		// is not its first.
		found, err := tx.update(ctx, "tickets",
			"UPDATE tickets SET updated_at = ?, first_response_at = COALESCE(first_response_at, ?), update_due = ? "+
				"WHERE id = ?",
			article.TicketID, article.CreatedAt, firstResponse, updateDue, article.TicketID)
		if err != nil {
			return apperror.Internal(err)
		}

		if !found {
			return apperror.NotFound("ticket", strconv.FormatUint(uint64(article.TicketID), 10))
		}

		if renewsLock {
			if _, err := tx.exec(ctx, "UPDATE tickets SET locked_at = ? WHERE id = ? AND locked = ?",
				article.CreatedAt, article.TicketID, true); err != nil {
				return apperror.Internal(err)
			}
		}

		if id, err = insertArticle(ctx, tx, article, minutes); err != nil {
			return err
		}

		return insertHistory(ctx, tx, history)
	})
	if err != nil {
		return nil, err
	}

	stored := *article
	stored.ID = id

	return &stored, nil
}

func (r *TicketRepository) AddFollowUp(
	ctx context.Context,
	article *model.Article,
	history []model.HistoryEntry,
	reopen *model.Reopening,
	updateDue *time.Time,
) (*model.Article, bool, error) {
	var (
		id    uint
		moved bool
	)

	err := r.withTx(ctx, func(tx base) error {
		found, err := tx.update(ctx, "tickets", "UPDATE tickets SET updated_at = ? WHERE id = ?",
			article.TicketID, article.CreatedAt, article.TicketID)
		if err != nil {
			return apperror.Internal(err)
		}

		if !found {
			return apperror.NotFound("ticket", strconv.FormatUint(uint64(article.TicketID), 10))
		}

		entries := history

		if reopen != nil {
			changed, err := tx.exec(ctx,
				"UPDATE tickets SET state_id = ?, pending_until = NULL, closed_at = NULL WHERE id = ? AND state_id = ?",
				reopen.To, article.TicketID, reopen.From)
			if err != nil {
				return apperror.Internal(err)
			}

			if moved = changed > 0; moved {
				entries = append(slices.Clone(history), reopen.History)
			}
		}

		// What is owed next is counted for the state the answer meant to leave
		// the ticket in; a move somebody else's change prevented leaves it as
		// their change left it.
		if reopen == nil || moved {
			if _, err := tx.exec(ctx, "UPDATE tickets SET update_due = ? WHERE id = ?", updateDue, article.TicketID); err != nil {
				return apperror.Internal(err)
			}
		}

		if id, err = insertArticle(ctx, tx, article, 0); err != nil {
			return err
		}

		return insertHistory(ctx, tx, entries)
	})
	if err != nil {
		return nil, false, err
	}

	stored := *article
	stored.ID = id

	return &stored, moved, nil
}

func (r *TicketRepository) Articles(ctx context.Context, ticketID uint) ([]*model.Article, error) {
	rows, err := r.db.QueryContext(ctx, r.rebind(
		"SELECT id, ticket_id, sender, channel, customer_visible, subject, body, author_id, created_at "+
			"FROM articles WHERE ticket_id = ? ORDER BY created_at, id"), ticketID)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	defer func() { _ = rows.Close() }()

	articles := make([]*model.Article, 0)

	for rows.Next() {
		var (
			article         model.Article
			sender, channel string
			author          sql.NullInt64
			created         dateTime
		)

		err := rows.Scan(&article.ID, &article.TicketID, &sender, &channel, &article.CustomerVisible,
			&article.Subject, &article.Body, &author, &created)
		if err != nil {
			return nil, apperror.Internal(err)
		}

		article.Sender = model.Sender(sender)
		article.Channel = model.Channel(channel)
		article.AuthorID = optionalID(author)
		article.CreatedAt = created.Time

		articles = append(articles, &article)
	}

	if err := rows.Err(); err != nil {
		return nil, apperror.Internal(err)
	}

	return articles, nil
}

func (r *TicketRepository) History(ctx context.Context, ticketID uint) ([]model.HistoryEntry, error) {
	rows, err := r.db.QueryContext(ctx, r.rebind(
		"SELECT id, ticket_id, at, actor_kind, actor_id, event, old_value, new_value "+
			"FROM ticket_history WHERE ticket_id = ? ORDER BY at, id"), ticketID)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	defer func() { _ = rows.Close() }()

	history := make([]model.HistoryEntry, 0)

	for rows.Next() {
		var (
			entry        model.HistoryEntry
			at           dateTime
			actor, event string
			actorID      sql.NullInt64
		)

		err := rows.Scan(&entry.ID, &entry.TicketID, &at, &actor, &actorID, &event, &entry.OldValue, &entry.NewValue)
		if err != nil {
			return nil, apperror.Internal(err)
		}

		entry.At = at.Time
		entry.Actor = model.Sender(actor)
		entry.ActorID = optionalID(actorID)
		entry.Event = model.HistoryEvent(event)

		history = append(history, entry)
	}

	if err := rows.Err(); err != nil {
		return nil, apperror.Internal(err)
	}

	return history, nil
}

func (r *TicketRepository) AccountedMinutes(ctx context.Context, ticketID uint) (int, error) {
	var minutes int

	err := r.db.QueryRowContext(ctx,
		r.rebind("SELECT COALESCE(SUM(minutes), 0) FROM time_accounting WHERE ticket_id = ?"), ticketID).Scan(&minutes)
	if err != nil {
		return 0, apperror.Internal(err)
	}

	return minutes, nil
}

// Delete removes a ticket and everything written in it or about it, in the
// order the keys between them demand, in one transaction: a delete refused half
// way by a key must not leave a ticket without its articles.
func (r *TicketRepository) Delete(ctx context.Context, id uint) error {
	return r.withTx(ctx, func(tx base) error {
		for _, statement := range []string{
			"DELETE FROM time_accounting WHERE ticket_id = ?",
			"DELETE FROM attachments WHERE article_id IN (SELECT id FROM articles WHERE ticket_id = ?)",
			"DELETE FROM articles WHERE ticket_id = ?",
			"DELETE FROM ticket_history WHERE ticket_id = ?",
			"DELETE FROM ticket_watchers WHERE ticket_id = ?",
			"DELETE FROM notifications WHERE ticket_id = ?",
		} {
			if _, err := tx.exec(ctx, statement, id); err != nil {
				return apperror.Internal(err)
			}
		}

		if _, err := tx.exec(ctx, "DELETE FROM ticket_links WHERE ticket_id = ? OR linked_id = ?", id, id); err != nil {
			return apperror.Internal(err)
		}

		affected, err := tx.exec(ctx, "DELETE FROM tickets WHERE id = ?", id)
		if err != nil {
			return apperror.Internal(err)
		}

		if affected == 0 {
			return apperror.NotFound("ticket", strconv.FormatUint(uint64(id), 10))
		}

		return nil
	})
}

func (r *TicketRepository) SetWatching(ctx context.Context, ticketID, userID uint, watching bool) error {
	return r.withTx(ctx, func(tx base) error {
		if _, err := tx.exec(ctx, "DELETE FROM ticket_watchers WHERE ticket_id = ? AND user_id = ?",
			ticketID, userID); err != nil {
			return apperror.Internal(err)
		}

		if !watching {
			return nil
		}

		if _, err := tx.exec(ctx, "INSERT INTO ticket_watchers (ticket_id, user_id) VALUES (?, ?)",
			ticketID, userID); err != nil {
			return apperror.Internal(err)
		}

		return nil
	})
}

func (r *TicketRepository) Watching(ctx context.Context, ticketID, userID uint) (bool, error) {
	var count int

	err := r.db.QueryRowContext(ctx,
		r.rebind("SELECT COUNT(*) FROM ticket_watchers WHERE ticket_id = ? AND user_id = ?"),
		ticketID, userID).Scan(&count)
	if err != nil {
		return false, apperror.Internal(err)
	}

	return count > 0, nil
}

func (r *TicketRepository) Watchers(ctx context.Context, ticketID uint) ([]uint, error) {
	return r.ids(ctx, "SELECT user_id FROM ticket_watchers WHERE ticket_id = ? ORDER BY user_id", ticketID)
}

// insertArticle stores an article and the time noted with it, and answers the
// article's id.
func insertArticle(ctx context.Context, tx base, article *model.Article, minutes int) (uint, error) {
	id, err := tx.insert(ctx,
		"INSERT INTO articles (ticket_id, sender, channel, customer_visible, subject, body, author_id, created_at) "+
			"VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
		article.TicketID, string(article.Sender), string(article.Channel), article.CustomerVisible,
		article.Subject, article.Body, article.AuthorID, article.CreatedAt)
	if err != nil {
		return 0, apperror.Internal(err)
	}

	if minutes > 0 {
		_, err := tx.exec(ctx,
			"INSERT INTO time_accounting (ticket_id, article_id, minutes, created_by, created_at) VALUES (?, ?, ?, ?, ?)",
			article.TicketID, id, minutes, article.AuthorID, article.CreatedAt)
		if err != nil {
			return 0, apperror.Internal(err)
		}
	}

	return id, nil
}

func insertHistory(ctx context.Context, tx base, history []model.HistoryEntry) error {
	for _, entry := range history {
		_, err := tx.exec(ctx,
			"INSERT INTO ticket_history (ticket_id, at, actor_kind, actor_id, event, old_value, new_value) "+
				"VALUES (?, ?, ?, ?, ?, ?, ?)",
			entry.TicketID, entry.At, string(entry.Actor), entry.ActorID, string(entry.Event),
			cut(entry.OldValue), cut(entry.NewValue))
		if err != nil {
			return apperror.Internal(err)
		}
	}

	return nil
}

func scanTicket(row scanner) (*model.Ticket, error) {
	var (
		ticket                        model.Ticket
		number                        sql.NullString
		owner, responsible, createdBy sql.NullInt64
		contact, organisation, level  sql.NullInt64
		channel                       string
		lockedAt, created, updated    dateTime
		closed, pending               dateTime
		responseDue, respondedAt      dateTime
		solutionDue, updateDue        dateTime
	)

	err := row.Scan(&ticket.ID, &number, &ticket.Title, &ticket.QueueID, &ticket.StateID, &ticket.PriorityID,
		&ticket.TypeID, &owner, &responsible, &contact, &organisation, &ticket.Locked, &lockedAt, &channel,
		&createdBy,
		&created, &updated, &closed, &pending, &level, &responseDue, &respondedAt, &solutionDue, &updateDue)
	if err != nil {
		return nil, err
	}

	ticket.Number = number.String
	ticket.OwnerID = optionalID(owner)
	ticket.ResponsibleID = optionalID(responsible)
	ticket.ContactID = optionalID(contact)
	ticket.OrganisationID = optionalID(organisation)
	ticket.CreatedBy = optionalID(createdBy)
	ticket.Channel = model.Channel(channel)
	ticket.LockedAt = ptr(lockedAt)
	ticket.CreatedAt = created.Time
	ticket.UpdatedAt = updated.Time
	ticket.ClosedAt = ptr(closed)
	ticket.PendingUntil = ptr(pending)
	ticket.ServiceLevelID = optionalID(level)
	ticket.FirstResponseDue = ptr(responseDue)
	ticket.FirstResponseAt = ptr(respondedAt)
	ticket.SolutionDue = ptr(solutionDue)
	ticket.UpdateDue = ptr(updateDue)

	return &ticket, nil
}

// optionalID reads a key that may be absent.
func optionalID(id sql.NullInt64) *uint {
	if !id.Valid {
		return nil
	}

	value := uint(id.Int64)

	return &value
}

// cut keeps a history value within its column, VARCHAR(255), by characters
// rather than bytes so a word is not split in the middle of a letter.
func cut(value string) string {
	runes := []rune(value)
	if len(runes) <= 255 {
		return value
	}

	return string(runes[:255])
}

// likeEscaped makes what somebody searched for match itself: % and _ are LIKE's
// wildcards, and a search for "100%" is a search for that text. The escape is a
// character of its own, named in the statement with ESCAPE, because the three
// databases do not agree on a default one - SQLite has none at all.
func likeEscaped(search string) string {
	return strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(search)
}
