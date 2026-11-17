package sqldb

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
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

const notificationColumns = "id, user_id, ticket_id, definition_id, subject, body, created_at, read_at"

func scanNotification(row interface{ Scan(dest ...any) error }) (model.Notification, error) {
	var (
		n       model.Notification
		created dateTime
		read    dateTime
	)

	if err := row.Scan(&n.ID, &n.UserID, &n.TicketID, &n.DefinitionID, &n.Subject, &n.Body, &created, &read); err != nil {
		return n, err
	}

	n.CreatedAt = created.Time
	n.ReadAt = ptr(read)

	return n, nil
}

// list is the notifications a query answers, in its order.
func (r *NotificationRepository) list(ctx context.Context, query string, args ...any) ([]model.Notification, error) {
	rows, err := r.db.QueryContext(ctx, r.rebind(query), args...)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	defer func() { _ = rows.Close() }()

	listed := make([]model.Notification, 0)

	for rows.Next() {
		n, err := scanNotification(rows)
		if err != nil {
			return nil, apperror.Internal(err)
		}

		listed = append(listed, n)
	}

	if err := rows.Err(); err != nil {
		return nil, apperror.Internal(err)
	}

	return listed, nil
}

func (r *NotificationRepository) ForUser(ctx context.Context, userID uint, limit int) ([]model.Notification, int, error) {
	listed, err := r.list(ctx,
		"SELECT "+notificationColumns+" FROM notifications WHERE user_id = ? ORDER BY id DESC LIMIT ?", userID, limit)
	if err != nil {
		return nil, 0, err
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

func (r *NotificationRepository) Get(ctx context.Context, userID, id uint) (*model.Notification, error) {
	n, err := scanNotification(r.db.QueryRowContext(ctx,
		r.rebind("SELECT "+notificationColumns+" FROM notifications WHERE id = ? AND user_id = ?"), id, userID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, apperror.NotFound("notification", strconv.FormatUint(uint64(id), 10))
	}

	if err != nil {
		return nil, apperror.Internal(err)
	}

	return &n, nil
}

func (r *NotificationRepository) Unsent(ctx context.Context, limit int) ([]model.Notification, error) {
	return r.list(ctx, "SELECT "+notificationColumns+" FROM notifications WHERE sent = ? ORDER BY id LIMIT ?",
		false, limit)
}

func (r *NotificationRepository) MarkSent(ctx context.Context, ids []uint) error {
	if len(ids) == 0 {
		return nil
	}

	args := make([]any, 0, len(ids)+1)
	args = append(args, true)

	for _, id := range ids {
		args = append(args, id)
	}

	if _, err := r.exec(ctx, "UPDATE notifications SET sent = ? WHERE id IN "+marks(len(ids)), args...); err != nil {
		return apperror.Internal(err)
	}

	return nil
}

func (r *NotificationRepository) SwitchedOff(ctx context.Context, userID uint) ([]uint, error) {
	return r.ids(ctx, "SELECT definition_id FROM notification_opt_outs WHERE user_id = ? ORDER BY definition_id", userID)
}

func (r *NotificationRepository) SetSwitchedOff(ctx context.Context, userID uint, definitionIDs []uint) error {
	return r.withTx(ctx, func(tx base) error {
		if _, err := tx.exec(ctx, "DELETE FROM notification_opt_outs WHERE user_id = ?", userID); err != nil {
			return apperror.Internal(err)
		}

		for _, id := range definitionIDs {
			if _, err := tx.exec(ctx, "INSERT INTO notification_opt_outs (user_id, definition_id) VALUES (?, ?)",
				userID, id); err != nil {
				return apperror.Internal(err)
			}
		}

		return nil
	})
}

func (r *NotificationRepository) SwitchedOffBy(ctx context.Context, definitionID uint) ([]uint, error) {
	return r.ids(ctx, "SELECT user_id FROM notification_opt_outs WHERE definition_id = ? ORDER BY user_id", definitionID)
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
