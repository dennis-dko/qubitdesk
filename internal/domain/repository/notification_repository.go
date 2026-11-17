package repository

import (
	"context"
	"time"

	"github.com/dennis-dko/go-ticket-system/internal/domain/model"
)

// NotificationRepository keeps what each agent was told about a ticket.
type NotificationRepository interface {
	// Add keeps the notifications one event sent, all of them or none.
	Add(ctx context.Context, notifications []model.Notification) error

	// ForUser is an agent's latest notifications, the latest first and at most
	// limit of them, and how many of all of theirs they have not read.
	ForUser(ctx context.Context, userID uint, limit int) ([]model.Notification, int, error)

	// MarkRead marks one of an agent's notifications read as of at, where it is
	// not read already, and says whether it is one of theirs.
	MarkRead(ctx context.Context, userID, id uint, at time.Time) (bool, error)

	// MarkAllRead marks every notification of an agent's not read yet as read.
	MarkAllRead(ctx context.Context, userID uint, at time.Time) error

	// SentSince says whether a notification has told the agent about the ticket
	// since the moment given.
	SentSince(ctx context.Context, definitionID, ticketID, userID uint, since time.Time) (bool, error)
}
