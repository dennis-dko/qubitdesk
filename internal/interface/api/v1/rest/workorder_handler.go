package rest

import (
	"time"

	"gofr.dev/pkg/gofr"

	"github.com/dennis-dko/qubitdesk/internal/application/v1/service"
	"github.com/dennis-dko/qubitdesk/internal/domain/model"
)

// WorkOrderResponse is a work order, with the names of the accounts it names
// and the states the machine lets it move to from where it stands. Its number
// is its place in its change's plan.
type WorkOrderResponse struct {
	ID               uint       `json:"id"`
	ChangeID         uint       `json:"changeId"`
	Number           int        `json:"number"`
	Title            string     `json:"title"`
	Instruction      string     `json:"instruction"`
	Report           string     `json:"report"`
	StateID          uint       `json:"stateId"`
	NextStateIDs     []uint     `json:"nextStateIds"`
	WorkOrderTypeID  uint       `json:"workOrderTypeId"`
	AgentID          *uint      `json:"agentId"`
	AgentName        string     `json:"agentName"`
	PlannedStartAt   *time.Time `json:"plannedStartAt"`
	PlannedEndAt     *time.Time `json:"plannedEndAt"`
	ActualStartAt    *time.Time `json:"actualStartAt"`
	ActualEndAt      *time.Time `json:"actualEndAt"`
	PlannedMinutes   int        `json:"plannedMinutes"`
	AccountedMinutes int        `json:"accountedMinutes"`
	CreatedAt        time.Time  `json:"createdAt"`
	ChangedAt        time.Time  `json:"changedAt"`
	CreatedBy        uint       `json:"createdBy"`
	CreatedByName    string     `json:"createdByName"`
	ChangedBy        uint       `json:"changedBy"`
	ChangedByName    string     `json:"changedByName"`
}

// WorkOrderRequest adds or corrects a work order. A correction names only what
// it changes; the clear fields take the agent, both planned moments or both
// actual ones away, and minutes are accounted on top of what is accounted. Its
// type travels under the work order's own name, because the plain one names a
// ticket's type wherever a refusal names a field.
type WorkOrderRequest struct {
	Title           *string    `json:"title"`
	Instruction     *string    `json:"instruction"`
	Report          *string    `json:"report"`
	WorkOrderTypeID *uint      `json:"workOrderTypeId"`
	StateID         *uint      `json:"stateId"`
	AgentID         *uint      `json:"agentId"`
	ClearAgent      bool       `json:"clearAgent"`
	PlannedStartAt  *time.Time `json:"plannedStartAt"`
	PlannedEndAt    *time.Time `json:"plannedEndAt"`
	ClearPlanned    bool       `json:"clearPlanned"`
	ActualStartAt   *time.Time `json:"actualStartAt"`
	ActualEndAt     *time.Time `json:"actualEndAt"`
	ClearActual     bool       `json:"clearActual"`
	PlannedMinutes  *int       `json:"plannedMinutes"`
	Minutes         int        `json:"minutes"`
}

func (r WorkOrderRequest) command() service.WorkOrderCommand {
	return service.WorkOrderCommand{
		Title: r.Title, Instruction: r.Instruction, Report: r.Report, TypeID: r.WorkOrderTypeID, StateID: r.StateID,
		AgentID: r.AgentID, ClearAgent: r.ClearAgent, PlannedStartAt: r.PlannedStartAt, PlannedEndAt: r.PlannedEndAt,
		ClearPlanned: r.ClearPlanned, ActualStartAt: r.ActualStartAt, ActualEndAt: r.ActualEndAt,
		ClearActual: r.ClearActual, PlannedMinutes: r.PlannedMinutes, Minutes: r.Minutes,
	}
}

func newWorkOrderResponse(order *model.WorkOrder, names map[uint]string, next []uint) WorkOrderResponse {
	var agent string
	if order.AgentID != nil {
		agent = names[*order.AgentID]
	}

	return WorkOrderResponse{
		ID: order.ID, ChangeID: order.ChangeID, Number: order.Number, Title: order.Title,
		Instruction: order.Instruction, Report: order.Report, StateID: order.StateID, NextStateIDs: next,
		WorkOrderTypeID: order.TypeID, AgentID: order.AgentID, AgentName: agent, PlannedStartAt: order.PlannedStartAt,
		PlannedEndAt: order.PlannedEndAt, ActualStartAt: order.ActualStartAt, ActualEndAt: order.ActualEndAt,
		PlannedMinutes: order.PlannedMinutes, AccountedMinutes: order.AccountedMinutes, CreatedAt: order.CreatedAt,
		ChangedAt: order.ChangedAt, CreatedBy: order.CreatedBy, CreatedByName: names[order.CreatedBy],
		ChangedBy: order.ChangedBy, ChangedByName: names[order.ChangedBy],
	}
}

// respondWorkOrder is a work order as the API answers it.
func (h *ChangeHandler) respondWorkOrder(c *gofr.Context, order *model.WorkOrder) (any, error) {
	names, err := h.changes.Names(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	next, err := h.workOrders.NextStates(c, order)
	if err != nil {
		return nil, toHTTPError(err)
	}

	return newWorkOrderResponse(order, names, next), nil
}

// WorkOrders handles GET /api/v1/changes/{id}/work-orders: a change's work
// orders in the order of its plan.
func (h *ChangeHandler) WorkOrders(c *gofr.Context) (any, error) {
	if _, err := h.authz.Require(c, model.PermChangeRead); err != nil {
		return nil, err
	}

	id, err := pathID(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	orders, err := h.workOrders.Of(c, id)
	if err != nil {
		return nil, toHTTPError(err)
	}

	names, err := h.changes.Names(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	next, err := h.workOrders.NextStatesOf(c, orders)
	if err != nil {
		return nil, toHTTPError(err)
	}

	return listOf(orders, func(order *model.WorkOrder) WorkOrderResponse {
		return newWorkOrderResponse(order, names, next[order.ID])
	}), nil
}

// CreateWorkOrder handles POST /api/v1/changes/{id}/work-orders.
func (h *ChangeHandler) CreateWorkOrder(c *gofr.Context) (any, error) {
	principal, err := h.authz.Require(c, model.PermChangeWrite)
	if err != nil {
		return nil, err
	}

	id, err := pathID(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	var req WorkOrderRequest
	if err := bind(c, &req); err != nil {
		return nil, toHTTPError(err)
	}

	order, err := h.workOrders.Create(c, principal, id, req.command())
	if err != nil {
		return nil, toHTTPError(err)
	}

	return h.respondWorkOrder(c, order)
}

// WorkOrder handles GET /api/v1/work-orders/{id}.
func (h *ChangeHandler) WorkOrder(c *gofr.Context) (any, error) {
	if _, err := h.authz.Require(c, model.PermChangeRead); err != nil {
		return nil, err
	}

	id, err := pathID(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	order, err := h.workOrders.Get(c, id)
	if err != nil {
		return nil, toHTTPError(err)
	}

	return h.respondWorkOrder(c, order)
}

// UpdateWorkOrder handles PUT /api/v1/work-orders/{id}: anything about it by
// whoever may work on changes, and a report by its agent, who may only see
// them - which of the two a request is, the work order service decides.
func (h *ChangeHandler) UpdateWorkOrder(c *gofr.Context) (any, error) {
	principal, err := h.authz.RequireAny(c, model.PermChangeWrite, model.PermChangeRead)
	if err != nil {
		return nil, err
	}

	id, err := pathID(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	var req WorkOrderRequest
	if err := bind(c, &req); err != nil {
		return nil, toHTTPError(err)
	}

	order, err := h.workOrders.Update(c, principal, id, req.command())
	if err != nil {
		return nil, toHTTPError(err)
	}

	return h.respondWorkOrder(c, order)
}

// DeleteWorkOrder handles DELETE /api/v1/work-orders/{id}.
func (h *ChangeHandler) DeleteWorkOrder(c *gofr.Context) (any, error) {
	if _, err := h.authz.Require(c, model.PermChangeWrite); err != nil {
		return nil, err
	}

	id, err := pathID(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	if err := h.workOrders.Delete(c, id); err != nil {
		return nil, toHTTPError(err)
	}

	return nil, nil
}
