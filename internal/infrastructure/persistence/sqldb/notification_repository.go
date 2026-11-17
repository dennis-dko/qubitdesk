package sqldb

import (
	"context"
	"time"

	"github.com/dennis-dko/go-ticket-system/internal/domain/model"
	"github.com/dennis-dko/go-ticket-system/internal/domain/repository"
	"github.com/dennis-dko/go-ticket-system/internal/support/apperror"
)

// NotificationRepository keeps what each agent was told in notifications.
type NotificationRepository struct{ base }

// NewNotificationRepository creates the repository for the given dialect.
func NewNotificationRepository(db DB, dialect string) *NotificationRepository {
	return &NotificationRepository{base{db: db, dialect: dialect}}
}

var _ repository.NotificationRepository = (*NotificationRepository)(nil)

func (r *NotificationRepository) Add(ctx context.Context, notifications []model.Notification) error {
	if len(notifications) == 0 {
		return nil
	}

	return r.withTx(ctx, func(tx base) error {
		for _, n := range notifications {
			if _, err := tx.exec(ctx,
				"INSERT INTO notifications (user_id, ticket_id, definition_id, subject, body, created_at) "+
					"VALUES (?, ?, ?, ?, ?, ?)",
				n.UserID, n.TicketID, n.DefinitionID, n.Subject, n.Body, n.CreatedAt.UTC()); err != nil {
				return apperror.Internal(err)
			}
		}

		return nil
	})
}

func (r *NotificationRepository) ForUser(ctx context.Context, userID uint, limit int) ([]model.Notification, int, error) {
	rows, err := r.db.QueryContext(ctx, r.rebind(
		"SELECT id, user_id, ticket_id, definition_id, subject, body, created_at, read_at FROM notifications "+
			"WHERE user_id = ? ORDER BY id DESC LIMIT ?"), userID, limit)
	if err != nil {
		return nil, 0, apperror.Internal(err)
	}
	defer func() { _ = rows.Close() }()

	listed := make([]model.Notification, 0)

	for rows.Next() {
		var (
			n       model.Notification
			created dateTime
			read    dateTime
		)

		if err := rows.Scan(&n.ID, &n.UserID, &n.TicketID, &n.DefinitionID, &n.Subject, &n.Body, &created,
			&read); err != nil {
			return nil, 0, apperror.Internal(err)
		}

		n.CreatedAt = created.Time
		n.ReadAt = ptr(read)
		listed = append(listed, n)
	}

	if err := rows.Err(); err != nil {
		return nil, 0, apperror.Internal(err)
	}

	var unread int

	if err := r.db.QueryRowContext(ctx,
		r.rebind("SELECT COUNT(*) FROM notifications WHERE user_id = ? AND read_at IS NULL"), userID).
		Scan(&unread); err != nil {
		return nil, 0, apperror.Internal(err)
	}

	return listed, unread, nil
}

func (r *NotificationRepository) MarkRead(ctx context.Context, userID, id uint, at time.Time) (bool, error) {
	if _, err := r.exec(ctx, "UPDATE notifications SET read_at = ? WHERE id = ? AND user_id = ? AND read_at IS NULL",
		at.UTC(), id, userID); err != nil {
		return false, apperror.Internal(err)
	}

	var count int

	if err := r.db.QueryRowContext(ctx,
		r.rebind("SELECT COUNT(*) FROM notifications WHERE id = ? AND user_id = ?"), id, userID).
		Scan(&count); err != nil {
		return false, apperror.Internal(err)
	}

	return count > 0, nil
}

func (r *NotificationRepository) MarkAllRead(ctx context.Context, userID uint, at time.Time) error {
	if _, err := r.exec(ctx, "UPDATE notifications SET read_at = ? WHERE user_id = ? AND read_at IS NULL",
		at.UTC(), userID); err != nil {
		return apperror.Internal(err)
	}

	return nil
}

func (r *NotificationRepository) SentSince(
	ctx context.Context,
	definitionID, ticketID, userID uint,
	since time.Time,
) (bool, error) {
	var count int

	if err := r.db.QueryRowContext(ctx, r.rebind(
		"SELECT COUNT(*) FROM notifications WHERE user_id = ? AND ticket_id = ? AND definition_id = ? "+
			"AND created_at >= ?"), userID, ticketID, definitionID, since.UTC()).Scan(&count); err != nil {
		return false, apperror.Internal(err)
	}

	return count > 0, nil
}
