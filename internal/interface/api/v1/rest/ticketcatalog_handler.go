package rest

import (
	"context"

	"gofr.dev/pkg/gofr"

	"github.com/dennis-dko/go-ticket-system/internal/application/v1/command"
	"github.com/dennis-dko/go-ticket-system/internal/application/v1/service"
	"github.com/dennis-dko/go-ticket-system/internal/domain/model"
)

// TicketCatalogHandler serves what tickets are sorted by - the states, the
// priorities and the types - under /ticket-states, /ticket-priorities and
// /ticket-types.
//
// Reading takes tickets:read or queues:write: whoever works tickets needs the
// lists to put a ticket into a state, and whoever sets them up needs to see what
// is there. Changing takes queues:write, the right that sets up how tickets are
// sorted.
type TicketCatalogHandler struct {
	catalog service.TicketCatalogService
	authz   *Authorizer
}

// NewTicketCatalogHandler creates the handler for the three tables.
func NewTicketCatalogHandler(catalog service.TicketCatalogService, authz *Authorizer) *TicketCatalogHandler {
	return &TicketCatalogHandler{catalog: catalog, authz: authz}
}

// TicketStateResponse is a state as the API hands it out.
type TicketStateResponse struct {
	ID          uint   `json:"id"`
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description"`
	Valid       bool   `json:"valid"`
	IsSystem    bool   `json:"isSystem"`
}

// TicketStateRequest creates or changes a state; on a change an absent field is
// left as it was.
type TicketStateRequest struct {
	Name        *string `json:"name"`
	Type        *string `json:"type"`
	Description *string `json:"description"`
	Valid       *bool   `json:"valid"`
}

// TicketPriorityResponse is a priority as the API hands it out.
type TicketPriorityResponse struct {
	ID          uint   `json:"id"`
	Name        string `json:"name"`
	Level       int    `json:"level"`
	Description string `json:"description"`
	Valid       bool   `json:"valid"`
	IsSystem    bool   `json:"isSystem"`
}

// TicketPriorityRequest creates or changes a priority; on a change an absent
// field is left as it was.
type TicketPriorityRequest struct {
	Name        *string `json:"name"`
	Level       *int    `json:"level"`
	Description *string `json:"description"`
	Valid       *bool   `json:"valid"`
}

// TicketTypeResponse is a type as the API hands it out.
type TicketTypeResponse struct {
	ID          uint   `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Valid       bool   `json:"valid"`
	IsSystem    bool   `json:"isSystem"`
}

// TicketTypeRequest creates or changes a type; on a change an absent field is
// left as it was.
type TicketTypeRequest struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
	Valid       *bool   `json:"valid"`
}

func newTicketStateResponse(s *model.TicketState) TicketStateResponse {
	return TicketStateResponse{
		ID: s.ID, Name: s.Name, Type: string(s.Type), Description: s.Description,
		Valid: s.Valid, IsSystem: s.IsSystem,
	}
}

func newTicketPriorityResponse(p *model.TicketPriority) TicketPriorityResponse {
	return TicketPriorityResponse{
		ID: p.ID, Name: p.Name, Level: p.Level, Description: p.Description,
		Valid: p.Valid, IsSystem: p.IsSystem,
	}
}

func newTicketTypeResponse(k *model.TicketType) TicketTypeResponse {
	return TicketTypeResponse{
		ID: k.ID, Name: k.Name, Description: k.Description, Valid: k.Valid, IsSystem: k.IsSystem,
	}
}

// ListStates handles GET /api/v1/ticket-states.
func (h *TicketCatalogHandler) ListStates(c *gofr.Context) (any, error) {
	if _, err := h.authz.RequireAny(c, model.PermTicketRead, model.PermQueueWrite); err != nil {
		return nil, err
	}

	states, err := h.catalog.ListStates(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	return listOf(states, newTicketStateResponse), nil
}

// GetState handles GET /api/v1/ticket-states/{id}.
func (h *TicketCatalogHandler) GetState(c *gofr.Context) (any, error) {
	if _, err := h.authz.RequireAny(c, model.PermTicketRead, model.PermQueueWrite); err != nil {
		return nil, err
	}

	id, err := pathID(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	state, err := h.catalog.GetState(c, id)
	if err != nil {
		return nil, toHTTPError(err)
	}

	return newTicketStateResponse(state), nil
}

// CreateState handles POST /api/v1/ticket-states.
func (h *TicketCatalogHandler) CreateState(c *gofr.Context) (any, error) {
	if _, err := h.authz.Require(c, model.PermQueueWrite); err != nil {
		return nil, err
	}

	var req TicketStateRequest
	if err := bind(c, &req); err != nil {
		return nil, toHTTPError(err)
	}

	state, err := h.catalog.CreateState(c, req.command())
	if err != nil {
		return nil, toHTTPError(err)
	}

	return newTicketStateResponse(state), nil
}

// UpdateState handles PUT /api/v1/ticket-states/{id}.
func (h *TicketCatalogHandler) UpdateState(c *gofr.Context) (any, error) {
	if _, err := h.authz.Require(c, model.PermQueueWrite); err != nil {
		return nil, err
	}

	id, err := pathID(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	var req TicketStateRequest
	if err := bind(c, &req); err != nil {
		return nil, toHTTPError(err)
	}

	state, err := h.catalog.UpdateState(c, id, req.command())
	if err != nil {
		return nil, toHTTPError(err)
	}

	return newTicketStateResponse(state), nil
}

// DeleteState handles DELETE /api/v1/ticket-states/{id}.
func (h *TicketCatalogHandler) DeleteState(c *gofr.Context) (any, error) {
	return h.deleteOne(c, h.catalog.DeleteState)
}

// ListPriorities handles GET /api/v1/ticket-priorities.
func (h *TicketCatalogHandler) ListPriorities(c *gofr.Context) (any, error) {
	if _, err := h.authz.RequireAny(c, model.PermTicketRead, model.PermQueueWrite); err != nil {
		return nil, err
	}

	priorities, err := h.catalog.ListPriorities(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	return listOf(priorities, newTicketPriorityResponse), nil
}

// GetPriority handles GET /api/v1/ticket-priorities/{id}.
func (h *TicketCatalogHandler) GetPriority(c *gofr.Context) (any, error) {
	if _, err := h.authz.RequireAny(c, model.PermTicketRead, model.PermQueueWrite); err != nil {
		return nil, err
	}

	id, err := pathID(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	priority, err := h.catalog.GetPriority(c, id)
	if err != nil {
		return nil, toHTTPError(err)
	}

	return newTicketPriorityResponse(priority), nil
}

// CreatePriority handles POST /api/v1/ticket-priorities.
func (h *TicketCatalogHandler) CreatePriority(c *gofr.Context) (any, error) {
	if _, err := h.authz.Require(c, model.PermQueueWrite); err != nil {
		return nil, err
	}

	var req TicketPriorityRequest
	if err := bind(c, &req); err != nil {
		return nil, toHTTPError(err)
	}

	priority, err := h.catalog.CreatePriority(c, req.command())
	if err != nil {
		return nil, toHTTPError(err)
	}

	return newTicketPriorityResponse(priority), nil
}

// UpdatePriority handles PUT /api/v1/ticket-priorities/{id}.
func (h *TicketCatalogHandler) UpdatePriority(c *gofr.Context) (any, error) {
	if _, err := h.authz.Require(c, model.PermQueueWrite); err != nil {
		return nil, err
	}

	id, err := pathID(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	var req TicketPriorityRequest
	if err := bind(c, &req); err != nil {
		return nil, toHTTPError(err)
	}

	priority, err := h.catalog.UpdatePriority(c, id, req.command())
	if err != nil {
		return nil, toHTTPError(err)
	}

	return newTicketPriorityResponse(priority), nil
}

// DeletePriority handles DELETE /api/v1/ticket-priorities/{id}.
func (h *TicketCatalogHandler) DeletePriority(c *gofr.Context) (any, error) {
	return h.deleteOne(c, h.catalog.DeletePriority)
}

// ListTypes handles GET /api/v1/ticket-types.
func (h *TicketCatalogHandler) ListTypes(c *gofr.Context) (any, error) {
	if _, err := h.authz.RequireAny(c, model.PermTicketRead, model.PermQueueWrite); err != nil {
		return nil, err
	}

	types, err := h.catalog.ListTypes(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	return listOf(types, newTicketTypeResponse), nil
}

// GetType handles GET /api/v1/ticket-types/{id}.
func (h *TicketCatalogHandler) GetType(c *gofr.Context) (any, error) {
	if _, err := h.authz.RequireAny(c, model.PermTicketRead, model.PermQueueWrite); err != nil {
		return nil, err
	}

	id, err := pathID(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	kind, err := h.catalog.GetType(c, id)
	if err != nil {
		return nil, toHTTPError(err)
	}

	return newTicketTypeResponse(kind), nil
}

// CreateType handles POST /api/v1/ticket-types.
func (h *TicketCatalogHandler) CreateType(c *gofr.Context) (any, error) {
	if _, err := h.authz.Require(c, model.PermQueueWrite); err != nil {
		return nil, err
	}

	var req TicketTypeRequest
	if err := bind(c, &req); err != nil {
		return nil, toHTTPError(err)
	}

	kind, err := h.catalog.CreateType(c, req.command())
	if err != nil {
		return nil, toHTTPError(err)
	}

	return newTicketTypeResponse(kind), nil
}

// UpdateType handles PUT /api/v1/ticket-types/{id}.
func (h *TicketCatalogHandler) UpdateType(c *gofr.Context) (any, error) {
	if _, err := h.authz.Require(c, model.PermQueueWrite); err != nil {
		return nil, err
	}

	id, err := pathID(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	var req TicketTypeRequest
	if err := bind(c, &req); err != nil {
		return nil, toHTTPError(err)
	}

	kind, err := h.catalog.UpdateType(c, id, req.command())
	if err != nil {
		return nil, toHTTPError(err)
	}

	return newTicketTypeResponse(kind), nil
}

// DeleteType handles DELETE /api/v1/ticket-types/{id}.
func (h *TicketCatalogHandler) DeleteType(c *gofr.Context) (any, error) {
	return h.deleteOne(c, h.catalog.DeleteType)
}

// deleteOne is what deleting a value takes in all three tables: the right that
// sets them up, the id from the path, and the service's own refusals.
func (h *TicketCatalogHandler) deleteOne(c *gofr.Context, remove func(context.Context, uint) error) (any, error) {
	if _, err := h.authz.Require(c, model.PermQueueWrite); err != nil {
		return nil, err
	}

	id, err := pathID(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	if err := remove(c, id); err != nil {
		return nil, toHTTPError(err)
	}

	return map[string]string{"status": "deleted"}, nil
}

func (r TicketStateRequest) command() command.TicketStateCommand {
	return command.TicketStateCommand{Name: r.Name, Type: r.Type, Description: r.Description, Valid: r.Valid}
}

func (r TicketPriorityRequest) command() command.TicketPriorityCommand {
	return command.TicketPriorityCommand{Name: r.Name, Level: r.Level, Description: r.Description, Valid: r.Valid}
}

func (r TicketTypeRequest) command() command.TicketTypeCommand {
	return command.TicketTypeCommand{Name: r.Name, Description: r.Description, Valid: r.Valid}
}

// listOf turns what a service listed into the list the API hands out.
func listOf[M, R any](items []*M, respond func(*M) R) listResponse[R] {
	out := make([]R, 0, len(items))
	for _, item := range items {
		out = append(out, respond(item))
	}

	return listResponse[R]{Items: out, TotalCount: uint(len(out))}
}
