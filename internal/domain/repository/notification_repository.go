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

	// Get is one of an agent's notifications.
	Get(ctx context.Context, userID, id uint) (*model.Notification, error)

	// Unsent are the notifications that have not left for the browsers yet, the
	// oldest first and at most limit of them.
	Unsent(ctx context.Context, limit int) ([]model.Notification, error)

	// MarkSent marks notifications as having left.
	MarkSent(ctx context.Context, ids []uint) error

	// SwitchedOff are the notifications the agent switched off for themselves.
	SwitchedOff(ctx context.Context, userID uint) ([]uint, error)

	// SetSwitchedOff puts the notifications the agent switched off in place of
	// the ones they had.
	SetSwitchedOff(ctx context.Context, userID uint, definitionIDs []uint) error

	// SwitchedOffBy are the agents who switched the notification off.
	SwitchedOffBy(ctx context.Context, definitionID uint) ([]uint, error)
}
