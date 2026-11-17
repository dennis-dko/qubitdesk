package memory

import (
	"context"
	"slices"
	"sync"

	"github.com/dennis-dko/go-ticket-system/internal/domain/model"
	"github.com/dennis-dko/go-ticket-system/internal/domain/repository"
	"github.com/dennis-dko/go-ticket-system/internal/support/apperror"
)

// QueueRepository keeps queues in memory, with the database's one rule about
// them: no two share a name. How many tickets a queue holds is whatever a test
// says with UseInTickets, for the reason the catalog's counts are.
type QueueRepository struct {
	items *store[model.Queue]

	mu        sync.Mutex
	tickets   map[uint]int
	followers map[uint][]uint
}

// NewQueueRepository creates an empty queue repository.
func NewQueueRepository() *QueueRepository {
	return &QueueRepository{items: newStore[model.Queue]("queue"), tickets: map[uint]int{}, followers: map[uint][]uint{}}
}

// Follow makes the agent with userID one who chose the queue as theirs.
func (r *QueueRepository) Follow(queueID, userID uint) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.followers[queueID] = append(r.followers[queueID], userID)
}

func (r *QueueRepository) Followers(_ context.Context, id uint) ([]uint, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	return slices.Clone(r.followers[id]), nil
}

var _ repository.QueueRepository = (*QueueRepository)(nil)

// UseInTickets makes the queue with this id look as if it held count tickets.
func (r *QueueRepository) UseInTickets(id uint, count int) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.tickets[id] = count
}

func (r *QueueRepository) GetAll(_ context.Context) ([]*model.Queue, error) {
	return r.items.all(), nil
}

func (r *QueueRepository) GetByID(_ context.Context, id uint) (*model.Queue, error) {
	return r.items.get(id)
}

func (r *QueueRepository) Save(_ context.Context, queue *model.Queue) (*model.Queue, error) {
	if r.nameTaken(queue) {
		return nil, queueNameTaken(queue.Name)
	}

	stored := *queue
	stored.Access = slices.Clone(queue.Access)

	return r.items.add(&stored, func(q *model.Queue, id uint) { q.ID = id }), nil
}

func (r *QueueRepository) Update(_ context.Context, queue *model.Queue) (*model.Queue, error) {
	if r.nameTaken(queue) {
		return nil, queueNameTaken(queue.Name)
	}

	stored := *queue
	stored.Access = slices.Clone(queue.Access)

	return r.items.update(queue.ID, &stored)
}

func (r *QueueRepository) Delete(_ context.Context, id uint) error {
	return r.items.delete(id)
}

func (r *QueueRepository) CountTickets(_ context.Context, id uint) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.tickets[id], nil
}

func (r *QueueRepository) CountChildren(_ context.Context, id uint) (int, error) {
	children := 0

	for _, queue := range r.items.all() {
		if queue.ParentID != nil && *queue.ParentID == id {
			children++
		}
	}

	return children, nil
}

func (r *QueueRepository) nameTaken(queue *model.Queue) bool {
	for _, other := range r.items.all() {
		if other.ID != queue.ID && other.Name == queue.Name {
			return true
		}
	}

	return false
}

func queueNameTaken(name string) error {
	return apperror.Conflictf("a queue named %q already exists", name).WithCode("queueNameTaken", name)
}
