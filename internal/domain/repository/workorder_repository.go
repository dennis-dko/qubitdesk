package repository

import (
	"context"

	"github.com/dennis-dko/qubitdesk/internal/domain/model"
)

// WorkOrderRepository keeps the work orders the changes are carried out in, and
// OTRS's state machine for them.
type WorkOrderRepository interface {
	// OfChanges is every work order of the changes given, in no order.
	OfChanges(ctx context.Context, changeIDs []uint) ([]*model.WorkOrder, error)

	GetByID(ctx context.Context, id uint) (*model.WorkOrder, error)
	Create(ctx context.Context, order *model.WorkOrder) (*model.WorkOrder, error)
	Update(ctx context.Context, order *model.WorkOrder) (*model.WorkOrder, error)
	Delete(ctx context.Context, id uint) error

	// Transitions is every step the work order state machine allows.
	Transitions(ctx context.Context) ([]model.StateTransition, error)
}
