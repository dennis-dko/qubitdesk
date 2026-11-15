package memory

import (
	"context"
	"strconv"
	"time"

	"github.com/dennis-dko/go-ticket-system/internal/domain/model"
	"github.com/dennis-dko/go-ticket-system/internal/support/apperror"
)

func (r *TicketRepository) AddAttachment(
	_ context.Context,
	attachment *model.Attachment,
	at time.Time,
	history []model.HistoryEntry,
) (*model.Attachment, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	ticket, ok := r.tickets[attachment.TicketID]
	if !ok {
		return nil, apperror.NotFound("ticket", strconv.FormatUint(uint64(attachment.TicketID), 10))
	}

	ticket.UpdatedAt = at
	r.tickets[ticket.ID] = ticket

	stored := *attachment
	stored.ID = r.nextAttachment
	stored.Content = append([]byte(nil), attachment.Content...)
	r.nextAttachment++
	r.attachments = append(r.attachments, stored)
	r.addHistory(history)

	listed := stored
	listed.Content = nil

	return &listed, nil
}

func (r *TicketRepository) Attachments(_ context.Context, ticketID uint) ([]*model.Attachment, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	attachments := make([]*model.Attachment, 0)

	for _, stored := range r.attachments {
		if stored.TicketID == ticketID {
			listed := stored
			listed.Content = nil
			attachments = append(attachments, &listed)
		}
	}

	return attachments, nil
}

func (r *TicketRepository) Attachment(_ context.Context, id uint) (*model.Attachment, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, stored := range r.attachments {
		if stored.ID == id {
			found := stored
			found.Content = append([]byte(nil), stored.Content...)

			return &found, nil
		}
	}

	return nil, apperror.NotFound("attachment", strconv.FormatUint(uint64(id), 10))
}
