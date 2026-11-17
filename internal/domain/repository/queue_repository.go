package repository

import (
	"context"

	"github.com/dennis-dko/go-ticket-system/internal/domain/model"
)

// QueueRepository keeps the queues and which role reaches each. A queue and its
// access are one thing to whoever set them up, so every write takes both.
type QueueRepository interface {
	GetAll(ctx context.Context) ([]*model.Queue, error)
	GetByID(ctx context.Context, id uint) (*model.Queue, error)
	Save(ctx context.Context, queue *model.Queue) (*model.Queue, error)
	Update(ctx context.Context, queue *model.Queue) (*model.Queue, error)

	// Delete removes the queue with its access and the agents' wish to be told
	// about it. That it holds no tickets and no other queue is the caller's to
	// have asked first.
	Delete(ctx context.Context, id uint) error

	// CountTickets reports how many tickets are in the queue.
	CountTickets(ctx context.Context, id uint) (int, error)

	// CountChildren reports how many queues sit directly under this one.
	CountChildren(ctx context.Context, id uint) (int, error)

	// Followers are the agents who chose the queue as one of theirs, to be told
	// about what happens in it.
	Followers(ctx context.Context, id uint) ([]uint, error)

	// FollowedBy are the queues the agent chose as theirs.
	FollowedBy(ctx context.Context, userID uint) ([]uint, error)

	// SetFollowed puts the queues the agent chose in place of the ones they had.
	SetFollowed(ctx context.Context, userID uint, queueIDs []uint) error
}
