package memory

import (
	"context"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/dennis-dko/go-ticket-system/internal/domain/model"
	"github.com/dennis-dko/go-ticket-system/internal/domain/repository"
	"github.com/dennis-dko/go-ticket-system/internal/support/apperror"
)

// NotificationRepository keeps what each agent was told in memory, for the
// service tests.
type NotificationRepository struct {
	mu     sync.Mutex
	sent   []model.Notification
	pushed map[uint]bool
	off    map[uint][]uint
	next   uint
}

// NewNotificationRepository creates an empty notification repository.
func NewNotificationRepository() *NotificationRepository {
	return &NotificationRepository{pushed: map[uint]bool{}, off: map[uint][]uint{}, next: 1}
}

func (r *NotificationRepository) SwitchedOff(_ context.Context, userID uint) ([]uint, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	return slices.Clone(r.off[userID]), nil
}

func (r *NotificationRepository) SetSwitchedOff(_ context.Context, userID uint, definitionIDs []uint) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.off[userID] = slices.Clone(definitionIDs)

	return nil
}

func (r *NotificationRepository) SwitchedOffBy(_ context.Context, definitionID uint) ([]uint, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	users := make([]uint, 0)

	for userID, off := range r.off {
		if slices.Contains(off, definitionID) {
			users = append(users, userID)
		}
	}

	slices.Sort(users)

	return users, nil
}

var _ repository.NotificationRepository = (*NotificationRepository)(nil)

func (r *NotificationRepository) Add(_ context.Context, notifications []model.Notification) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, n := range notifications {
		n.ID = r.next
		r.next++
		r.sent = append(r.sent, n)
	}

	return nil
}

func (r *NotificationRepository) ForUser(_ context.Context, userID uint, limit int) ([]model.Notification, int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	listed := make([]model.Notification, 0)
	unread := 0

	for _, n := range slices.Backward(r.sent) {
		if n.UserID != userID {
			continue
		}

		if n.ReadAt == nil {
			unread++
		}

		if len(listed) < limit {
			listed = append(listed, n)
		}
	}

	return listed, unread, nil
}

func (r *NotificationRepository) MarkRead(_ context.Context, userID, id uint, at time.Time) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for i, n := range r.sent {
		if n.ID == id && n.UserID == userID {
			if n.ReadAt == nil {
				r.sent[i].ReadAt = &at
			}

			return true, nil
		}
	}

	return false, nil
}

func (r *NotificationRepository) MarkAllRead(_ context.Context, userID uint, at time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	for i, n := range r.sent {
		if n.UserID == userID && n.ReadAt == nil {
			r.sent[i].ReadAt = &at
		}
	}

	return nil
}

func (r *NotificationRepository) Get(_ context.Context, userID, id uint) (*model.Notification, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, n := range r.sent {
		if n.ID == id && n.UserID == userID {
			return &n, nil
		}
	}

	return nil, apperror.NotFound("notification", strconv.FormatUint(uint64(id), 10))
}

func (r *NotificationRepository) Unsent(_ context.Context, limit int) ([]model.Notification, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	unsent := make([]model.Notification, 0)

	for _, n := range r.sent {
		if !r.pushed[n.ID] && len(unsent) < limit {
			unsent = append(unsent, n)
		}
	}

	return unsent, nil
}

func (r *NotificationRepository) MarkSent(_ context.Context, ids []uint) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, id := range ids {
		r.pushed[id] = true
	}

	return nil
}

func (r *NotificationRepository) SentSince(
	_ context.Context,
	definitionID, ticketID, userID uint,
	since time.Time,
) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	return slices.ContainsFunc(r.sent, func(n model.Notification) bool {
		return n.DefinitionID == definitionID && n.TicketID == ticketID && n.UserID == userID &&
			!n.CreatedAt.Before(since)
	}), nil
}
