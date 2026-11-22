package rest

import (
	"strings"
	"time"

	"gofr.dev/pkg/gofr"

	"github.com/dennis-dko/qubitdesk/internal/application/v1/service"
	"github.com/dennis-dko/qubitdesk/internal/domain/model"
)

// ChangeHandler serves the changes and the work orders they are carried out in:
// read with changes:read and written with changes:write, every change - a
// change is the organisation's, not narrowed by queue - except the report a
// work order's agent writes, which the work order service lets through.
type ChangeHandler struct {
	changes    *service.ChangeApplicationService
	workOrders *service.WorkOrderApplicationService
	authz      *Authorizer
}

// NewChangeHandler wires the handler.
func NewChangeHandler(
	changes *service.ChangeApplicationService,
	workOrders *service.WorkOrderApplicationService,
	authz *Authorizer,
) *ChangeHandler {
	return &ChangeHandler{changes: changes, workOrders: workOrders, authz: authz}
}

// ChangeResponse is a change, with the names of the accounts it names, the
// states the machine lets it move to from where it stands, and what its work
// orders say of it: when it is planned and when it ran, the effort planned and
// the time accounted, and how many there are.
type ChangeResponse struct {
	ID            uint       `json:"id"`
	Number        string     `json:"number"`
	Title         string     `json:"title"`
	Description   string     `json:"description"`
	Justification string     `json:"justification"`
	ManagerID     *uint      `json:"managerId"`
	ManagerName   string     `json:"managerName"`
	BuilderID     *uint      `json:"builderId"`
	BuilderName   string     `json:"builderName"`
	CategoryID    uint       `json:"categoryId"`
	ImpactID      uint       `json:"impactId"`
	PriorityID    uint       `json:"priorityId"`
	StateID       uint       `json:"stateId"`
	NextStateIDs  []uint     `json:"nextStateIds"`
	RequestedAt   *time.Time `json:"requestedAt"`
	CreatedAt     time.Time  `json:"createdAt"`
	ChangedAt     time.Time  `json:"changedAt"`
	CreatedBy     uint       `json:"createdBy"`
	CreatedByName string     `json:"createdByName"`
	ChangedBy     uint       `json:"changedBy"`
	ChangedByName string     `json:"changedByName"`

	PlannedStartAt   *time.Time `json:"plannedStartAt"`
	PlannedEndAt     *time.Time `json:"plannedEndAt"`
	ActualStartAt    *time.Time `json:"actualStartAt"`
	ActualEndAt      *time.Time `json:"actualEndAt"`
	PlannedMinutes   int        `json:"plannedMinutes"`
	AccountedMinutes int        `json:"accountedMinutes"`
	WorkOrders       int        `json:"workOrders"`
}

// ChangeRequest makes or corrects a change. A correction names only what it
// changes; the clear fields take a manager, a builder or the requested moment
// away.
type ChangeRequest struct {
	Title            *string    `json:"title"`
	Description      *string    `json:"description"`
	Justification    *string    `json:"justification"`
	ManagerID        *uint      `json:"managerId"`
	ClearManager     bool       `json:"clearManager"`
	BuilderID        *uint      `json:"builderId"`
	ClearBuilder     bool       `json:"clearBuilder"`
	CategoryID       *uint      `json:"categoryId"`
	ImpactID         *uint      `json:"impactId"`
	PriorityID       *uint      `json:"priorityId"`
	StateID          *uint      `json:"stateId"`
	RequestedAt      *time.Time `json:"requestedAt"`
	ClearRequestedAt bool       `json:"clearRequestedAt"`
}

func (r ChangeRequest) command() service.ChangeCommand {
	return service.ChangeCommand{
		Title: r.Title, Description: r.Description, Justification: r.Justification, ManagerID: r.ManagerID,
		ClearManager: r.ClearManager, BuilderID: r.BuilderID, ClearBuilder: r.ClearBuilder, CategoryID: r.CategoryID,
		ImpactID: r.ImpactID, PriorityID: r.PriorityID, StateID: r.StateID, RequestedAt: r.RequestedAt,
		ClearRequestedAt: r.ClearRequestedAt,
	}
}

func newChangeResponse(
	change *model.Change,
	names map[uint]string,
	next []uint,
	schedule model.Schedule,
) ChangeResponse {
	name := func(id *uint) string {
		if id == nil {
			return ""
		}

		return names[*id]
	}

	return ChangeResponse{
		ID: change.ID, Number: change.Number, Title: change.Title, Description: change.Description,
		Justification: change.Justification, ManagerID: change.ManagerID, ManagerName: name(change.ManagerID),
		BuilderID: change.BuilderID, BuilderName: name(change.BuilderID), CategoryID: change.CategoryID,
		ImpactID: change.ImpactID, PriorityID: change.PriorityID, StateID: change.StateID, NextStateIDs: next,
		RequestedAt: change.RequestedAt, CreatedAt: change.CreatedAt, ChangedAt: change.ChangedAt,
		CreatedBy: change.CreatedBy, CreatedByName: names[change.CreatedBy], ChangedBy: change.ChangedBy,
		ChangedByName: names[change.ChangedBy], PlannedStartAt: schedule.PlannedStartAt,
		PlannedEndAt: schedule.PlannedEndAt, ActualStartAt: schedule.ActualStartAt, ActualEndAt: schedule.ActualEndAt,
		PlannedMinutes: schedule.PlannedMinutes, AccountedMinutes: schedule.AccountedMinutes,
		WorkOrders: schedule.WorkOrders,
	}
}

// respond is a change as the API answers it.
func (h *ChangeHandler) respond(c *gofr.Context, change *model.Change) (any, error) {
	names, err := h.changes.Names(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	next, err := h.changes.NextStates(c, change)
	if err != nil {
		return nil, toHTTPError(err)
	}

	schedules, err := h.workOrders.Schedules(c, []uint{change.ID})
	if err != nil {
		return nil, toHTTPError(err)
	}

	return newChangeResponse(change, names, next, schedules[change.ID]), nil
}

// List handles GET /api/v1/changes: a page of the changes, narrowed by ?state=
// - one state or several, comma-separated - and ?q=.
func (h *ChangeHandler) List(c *gofr.Context) (any, error) {
	if _, err := h.authz.Require(c, model.PermChangeRead); err != nil {
		return nil, err
	}

	var states []uint

	for _, raw := range strings.Split(c.Param("state"), ",") {
		if raw = strings.TrimSpace(raw); raw == "" {
			continue
		}

		state, err := parseUint(raw, "state")
		if err != nil {
			return nil, toHTTPError(err)
		}

		states = append(states, state)
	}

	limit, err := strictInt(c.Param("limit"), "limit")
	if err != nil {
		return nil, toHTTPError(err)
	}

	page, err := strictInt(c.Param("page"), "page")
	if err != nil {
		return nil, toHTTPError(err)
	}

	changes, total, err := h.changes.List(c, service.ChangeListFilter{
		StateIDs: states, Search: c.Param("q"), Limit: limit, Page: page,
	})
	if err != nil {
		return nil, toHTTPError(err)
	}

	names, err := h.changes.Names(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	ids := make([]uint, 0, len(changes))
	for _, change := range changes {
		ids = append(ids, change.ID)
	}

	schedules, err := h.workOrders.Schedules(c, ids)
	if err != nil {
		return nil, toHTTPError(err)
	}

	listed := listOf(changes, func(change *model.Change) ChangeResponse {
		return newChangeResponse(change, names, nil, schedules[change.ID])
	})
	listed.TotalCount = total

	return listed, nil
}

// Get handles GET /api/v1/changes/{id}.
func (h *ChangeHandler) Get(c *gofr.Context) (any, error) {
	if _, err := h.authz.Require(c, model.PermChangeRead); err != nil {
		return nil, err
	}

	id, err := pathID(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	change, err := h.changes.Get(c, id)
	if err != nil {
		return nil, toHTTPError(err)
	}

	return h.respond(c, change)
}

// Create handles POST /api/v1/changes.
func (h *ChangeHandler) Create(c *gofr.Context) (any, error) {
	principal, err := h.authz.Require(c, model.PermChangeWrite)
	if err != nil {
		return nil, err
	}

	var req ChangeRequest
	if err := bind(c, &req); err != nil {
		return nil, toHTTPError(err)
	}

	change, err := h.changes.Create(c, principal, req.command())
	if err != nil {
		return nil, toHTTPError(err)
	}

	return h.respond(c, change)
}

// Update handles PUT /api/v1/changes/{id}.
func (h *ChangeHandler) Update(c *gofr.Context) (any, error) {
	principal, err := h.authz.Require(c, model.PermChangeWrite)
	if err != nil {
		return nil, err
	}

	id, err := pathID(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	var req ChangeRequest
	if err := bind(c, &req); err != nil {
		return nil, toHTTPError(err)
	}

	change, err := h.changes.Update(c, principal, id, req.command())
	if err != nil {
		return nil, toHTTPError(err)
	}

	return h.respond(c, change)
}

// Delete handles DELETE /api/v1/changes/{id}.
func (h *ChangeHandler) Delete(c *gofr.Context) (any, error) {
	if _, err := h.authz.Require(c, model.PermChangeWrite); err != nil {
		return nil, err
	}

	id, err := pathID(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	if err := h.changes.Delete(c, id); err != nil {
		return nil, toHTTPError(err)
	}

	return nil, nil
}
