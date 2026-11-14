package repository

import (
	"context"

	"github.com/dennis-dko/go-ticket-system/internal/domain/model"
)

// TicketStateRepository keeps the states a ticket can be in.
type TicketStateRepository interface {
	GetAll(ctx context.Context) ([]*model.TicketState, error)
	GetByID(ctx context.Context, id uint) (*model.TicketState, error)

	// GetByName finds a state by its name, which is how the code finds the ones
	// it moves tickets into by itself.
	GetByName(ctx context.Context, name string) (*model.TicketState, error)
	Save(ctx context.Context, state *model.TicketState) (*model.TicketState, error)
	Update(ctx context.Context, state *model.TicketState) (*model.TicketState, error)
	Delete(ctx context.Context, id uint) error

	// CountTickets reports how many tickets are in the state, so that deleting
	// one in use is refused rather than leaving tickets in no state at all.
	CountTickets(ctx context.Context, id uint) (int, error)
}

// TicketPriorityRepository keeps how urgent a ticket can be, the most urgent
// first.
type TicketPriorityRepository interface {
	GetAll(ctx context.Context) ([]*model.TicketPriority, error)
	GetByID(ctx context.Context, id uint) (*model.TicketPriority, error)
	GetByName(ctx context.Context, name string) (*model.TicketPriority, error)
	Save(ctx context.Context, priority *model.TicketPriority) (*model.TicketPriority, error)
	Update(ctx context.Context, priority *model.TicketPriority) (*model.TicketPriority, error)
	Delete(ctx context.Context, id uint) error

	// CountTickets reports how many tickets have the priority, for the reason a
	// state's count exists.
	CountTickets(ctx context.Context, id uint) (int, error)
}

// TicketTypeRepository keeps the kinds of ticket an installation tells apart.
type TicketTypeRepository interface {
	GetAll(ctx context.Context) ([]*model.TicketType, error)
	GetByID(ctx context.Context, id uint) (*model.TicketType, error)
	GetByName(ctx context.Context, name string) (*model.TicketType, error)
	Save(ctx context.Context, kind *model.TicketType) (*model.TicketType, error)
	Update(ctx context.Context, kind *model.TicketType) (*model.TicketType, error)
	Delete(ctx context.Context, id uint) error

	// CountTickets reports how many tickets are of the type, for the reason a
	// state's count exists.
	CountTickets(ctx context.Context, id uint) (int, error)
}
