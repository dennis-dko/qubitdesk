package memory

import (
	"context"
	"slices"

	"github.com/dennis-dko/qubitdesk/internal/domain/model"
	"github.com/dennis-dko/qubitdesk/internal/domain/repository"
)

// WorkOrderRepository keeps the work orders in memory, with the state machine a
// test gives it.
type WorkOrderRepository struct {
	orders      *store[model.WorkOrder]
	transitions []model.StateTransition
}

// NewWorkOrderRepository creates a store holding no work order, following the
// state machine given.
func NewWorkOrderRepository(transitions []model.StateTransition) *WorkOrderRepository {
	return &WorkOrderRepository{orders: newStore[model.WorkOrder]("workorder"), transitions: transitions}
}

var _ repository.WorkOrderRepository = (*WorkOrderRepository)(nil)

func (r *WorkOrderRepository) OfChanges(_ context.Context, changeIDs []uint) ([]*model.WorkOrder, error) {
	return slices.DeleteFunc(r.orders.all(), func(order *model.WorkOrder) bool {
		return !slices.Contains(changeIDs, order.ChangeID)
	}), nil
}

func (r *WorkOrderRepository) GetByID(_ context.Context, id uint) (*model.WorkOrder, error) {
	return r.orders.get(id)
}

func (r *WorkOrderRepository) Create(_ context.Context, order *model.WorkOrder) (*model.WorkOrder, error) {
	kept := *order

	return r.orders.add(&kept, func(o *model.WorkOrder, id uint) { o.ID = id }), nil
}

func (r *WorkOrderRepository) Update(_ context.Context, order *model.WorkOrder) (*model.WorkOrder, error) {
	kept := *order

	return r.orders.update(order.ID, &kept)
}

func (r *WorkOrderRepository) Delete(_ context.Context, id uint) error {
	return r.orders.delete(id)
}

func (r *WorkOrderRepository) Transitions(_ context.Context) ([]model.StateTransition, error) {
	return slices.Clone(r.transitions), nil
}
