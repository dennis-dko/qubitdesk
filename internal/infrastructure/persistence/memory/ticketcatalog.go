package memory

import (
	"context"
	"sync"

	"github.com/dennis-dko/go-ticket-system/internal/domain/model"
	"github.com/dennis-dko/go-ticket-system/internal/domain/repository"
	"github.com/dennis-dko/go-ticket-system/internal/support/apperror"
)

// catalog keeps one of the tables tickets are sorted by - states, priorities,
// types - with the database's one rule about it: no two values share a name.
//
// There are no tickets here, so how many use a value is whatever a test says
// with UseInTickets: that is the one input the rules about deleting a value
// depend on, and a ticket store would be a second implementation of something
// else to get right.
type catalog[T any] struct {
	items *store[T]
	id    func(*T) uint
	setID func(*T, uint)
	name  func(*T) string
	taken func(name string) error

	mu    sync.Mutex
	inUse map[uint]int
}

func newCatalog[T any](
	entity string,
	id func(*T) uint,
	setID func(*T, uint),
	name func(*T) string,
	taken func(string) error,
) *catalog[T] {
	return &catalog[T]{
		items: newStore[T](entity), id: id, setID: setID, name: name, taken: taken,
		inUse: map[uint]int{},
	}
}

// UseInTickets makes the value with this id look used by count tickets.
func (c *catalog[T]) UseInTickets(id uint, count int) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.inUse[id] = count
}

func (c *catalog[T]) GetAll(_ context.Context) ([]*T, error) {
	return c.items.all(), nil
}

func (c *catalog[T]) GetByID(_ context.Context, id uint) (*T, error) {
	return c.items.get(id)
}

func (c *catalog[T]) GetByName(_ context.Context, name string) (*T, error) {
	for _, item := range c.items.all() {
		if c.name(item) == name {
			return item, nil
		}
	}

	return nil, apperror.NotFound(c.items.entity, name)
}

func (c *catalog[T]) Save(_ context.Context, item *T) (*T, error) {
	if c.nameTaken(item) {
		return nil, c.taken(c.name(item))
	}

	return c.items.add(item, c.setID), nil
}

func (c *catalog[T]) Update(_ context.Context, item *T) (*T, error) {
	if c.nameTaken(item) {
		return nil, c.taken(c.name(item))
	}

	return c.items.update(c.id(item), item)
}

func (c *catalog[T]) Delete(_ context.Context, id uint) error {
	return c.items.delete(id)
}

func (c *catalog[T]) CountTickets(_ context.Context, id uint) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.inUse[id], nil
}

// nameTaken reports whether another value already has the name item carries.
func (c *catalog[T]) nameTaken(item *T) bool {
	for _, other := range c.items.all() {
		if c.id(other) != c.id(item) && c.name(other) == c.name(item) {
			return true
		}
	}

	return false
}

// TicketStateRepository keeps ticket states in memory.
type TicketStateRepository struct {
	*catalog[model.TicketState]
}

// NewTicketStateRepository creates an empty state repository.
func NewTicketStateRepository() *TicketStateRepository {
	return &TicketStateRepository{newCatalog("state",
		func(s *model.TicketState) uint { return s.ID },
		func(s *model.TicketState, id uint) { s.ID = id },
		func(s *model.TicketState) string { return s.Name },
		func(name string) error {
			return apperror.Conflictf("a state named %q already exists", name).WithCode("stateNameTaken", name)
		})}
}

var _ repository.TicketStateRepository = (*TicketStateRepository)(nil)

// TicketPriorityRepository keeps ticket priorities in memory.
type TicketPriorityRepository struct {
	*catalog[model.TicketPriority]
}

// NewTicketPriorityRepository creates an empty priority repository.
func NewTicketPriorityRepository() *TicketPriorityRepository {
	return &TicketPriorityRepository{newCatalog("priority",
		func(p *model.TicketPriority) uint { return p.ID },
		func(p *model.TicketPriority, id uint) { p.ID = id },
		func(p *model.TicketPriority) string { return p.Name },
		func(name string) error {
			return apperror.Conflictf("a priority named %q already exists", name).WithCode("priorityNameTaken", name)
		})}
}

var _ repository.TicketPriorityRepository = (*TicketPriorityRepository)(nil)

// TicketTypeRepository keeps ticket types in memory.
type TicketTypeRepository struct {
	*catalog[model.TicketType]
}

// NewTicketTypeRepository creates an empty type repository.
func NewTicketTypeRepository() *TicketTypeRepository {
	return &TicketTypeRepository{newCatalog("type",
		func(k *model.TicketType) uint { return k.ID },
		func(k *model.TicketType, id uint) { k.ID = id },
		func(k *model.TicketType) string { return k.Name },
		func(name string) error {
			return apperror.Conflictf("a type named %q already exists", name).WithCode("typeNameTaken", name)
		})}
}

var _ repository.TicketTypeRepository = (*TicketTypeRepository)(nil)
