package sqldb

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"time"

	"github.com/dennis-dko/go-ticket-system/internal/domain/model"
	"github.com/dennis-dko/go-ticket-system/internal/support/apperror"
)

// AddAttachment stores a file with its article, the history saying so and the
// moment the ticket changed, in one transaction.
func (r *TicketRepository) AddAttachment(
	ctx context.Context,
	attachment *model.Attachment,
	at time.Time,
	history []model.HistoryEntry,
) (*model.Attachment, error) {
	var id uint

	err := r.withTx(ctx, func(tx base) error {
		found, err := tx.update(ctx, "tickets", "UPDATE tickets SET updated_at = ? WHERE id = ?",
			attachment.TicketID, at, attachment.TicketID)
		if err != nil {
			return apperror.Internal(err)
		}

		if !found {
			return apperror.NotFound("ticket", strconv.FormatUint(uint64(attachment.TicketID), 10))
		}

		id, err = tx.insert(ctx,
			"INSERT INTO attachments (article_id, filename, content_type, size, content) VALUES (?, ?, ?, ?, ?)",
			attachment.ArticleID, attachment.Filename, attachment.ContentType, attachment.Size, attachment.Content)
		if err != nil {
			return apperror.Internal(err)
		}

		return insertHistory(ctx, tx, history)
	})
	if err != nil {
		return nil, err
	}

	stored := *attachment
	stored.ID = id
	stored.Content = nil

	return &stored, nil
}

// Attachments lists the files of a ticket's articles in the order they came,
// without their content, which a listing has no use for.
func (r *TicketRepository) Attachments(ctx context.Context, ticketID uint) ([]*model.Attachment, error) {
	rows, err := r.db.QueryContext(ctx, r.rebind(
		"SELECT a.id, a.article_id, ar.ticket_id, a.filename, a.content_type, a.size "+
			"FROM attachments a JOIN articles ar ON ar.id = a.article_id WHERE ar.ticket_id = ? ORDER BY a.id"),
		ticketID)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	defer func() { _ = rows.Close() }()

	attachments := make([]*model.Attachment, 0)

	for rows.Next() {
		var a model.Attachment
		if err := rows.Scan(&a.ID, &a.ArticleID, &a.TicketID, &a.Filename, &a.ContentType, &a.Size); err != nil {
			return nil, apperror.Internal(err)
		}

		attachments = append(attachments, &a)
	}

	if err := rows.Err(); err != nil {
		return nil, apperror.Internal(err)
	}

	return attachments, nil
}

// Attachment reads one file with its content and the ticket its article is in.
func (r *TicketRepository) Attachment(ctx context.Context, id uint) (*model.Attachment, error) {
	var a model.Attachment

	err := r.db.QueryRowContext(ctx, r.rebind(
		"SELECT a.id, a.article_id, ar.ticket_id, a.filename, a.content_type, a.size, a.content "+
			"FROM attachments a JOIN articles ar ON ar.id = a.article_id WHERE a.id = ?"), id).
		Scan(&a.ID, &a.ArticleID, &a.TicketID, &a.Filename, &a.ContentType, &a.Size, &a.Content)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, apperror.NotFound("attachment", strconv.FormatUint(uint64(id), 10))
	}

	if err != nil {
		return nil, apperror.Internal(err)
	}

	return &a, nil
}
