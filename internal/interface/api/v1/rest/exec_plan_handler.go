package rest

import (
	"gofr.dev/pkg/gofr"

	"github.com/dennis-dko/qubitdesk/internal/application/v1/command"
	"github.com/dennis-dko/qubitdesk/internal/application/v1/service"
	"github.com/dennis-dko/qubitdesk/internal/domain/model"
	"github.com/dennis-dko/qubitdesk/internal/support/apperror"
)

// ExecPlanHandler serves the execution plans, read and administered by whoever
// sets up the automation.
type ExecPlanHandler struct {
	plans *service.ExecPlanApplicationService
	authz *Authorizer
}

// NewExecPlanHandler wires the handler.
func NewExecPlanHandler(plans *service.ExecPlanApplicationService, authz *Authorizer) *ExecPlanHandler {
	return &ExecPlanHandler{plans: plans, authz: authz}
}

// ExecPlanResponse is a plan: a time-based one with its weekdays - 0 is Sunday,
// as the calendars count them - and its times as the wall reads them in the
// installation's zone, an event-based one with its events.
type ExecPlanResponse struct {
	ID          uint     `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Valid       bool     `json:"valid"`
	Type        string   `json:"type"`
	Weekdays    []int    `json:"weekdays"`
	Times       []string `json:"times"`
	Events      []string `json:"events"`
}

// ExecPlanListResponse is the plans, and every event a plan can run on, so that
// a form offers exactly those.
type ExecPlanListResponse struct {
	Items      []ExecPlanResponse `json:"items"`
	TotalCount uint               `json:"totalCount"`
	Events     []string           `json:"events"`
}

// ExecPlanRequest creates or changes a plan; on a change an absent field stays
// as it was.
type ExecPlanRequest struct {
	Name        *string   `json:"name"`
	Description *string   `json:"description"`
	Valid       *bool     `json:"valid"`
	Type        *string   `json:"type"`
	Weekdays    *[]int    `json:"weekdays"`
	Times       *[]string `json:"times"`
	Events      *[]string `json:"events"`
}

func newExecPlanResponse(plan *model.ExecPlan) ExecPlanResponse {
	response := ExecPlanResponse{
		ID: plan.ID, Name: plan.Name, Description: plan.Description, Valid: plan.Valid, Type: string(plan.Type),
		Weekdays: make([]int, 0, len(plan.Weekdays)), Times: make([]string, 0, len(plan.Times)),
		Events: make([]string, 0, len(plan.Events)),
	}

	for _, day := range plan.Weekdays {
		response.Weekdays = append(response.Weekdays, int(day))
	}

	for _, minute := range plan.Times {
		response.Times = append(response.Times, wallTime(minute))
	}

	for _, event := range plan.Events {
		response.Events = append(response.Events, string(event))
	}

	return response
}

// command reads the times as minutes after midnight; one that is not a time of
// day is refused here, where its spelling is still known.
func (r ExecPlanRequest) command() (command.ExecPlanCommand, error) {
	cmd := command.ExecPlanCommand{
		Name: r.Name, Description: r.Description, Valid: r.Valid, Type: r.Type, Weekdays: r.Weekdays, Events: r.Events,
	}

	if r.Times != nil {
		times := make([]int, 0, len(*r.Times))

		for _, clock := range *r.Times {
			minute, ok := minutesOf(clock)
			if !ok {
				return cmd, apperror.InvalidFields("times")
			}

			times = append(times, minute)
		}

		cmd.Times = &times
	}

	return cmd, nil
}

func ticketEvents() []string {
	events := make([]string, 0)
	for _, event := range model.TicketEvents() {
		events = append(events, string(event))
	}

	return events
}

// List handles GET /api/v1/exec-plans.
func (h *ExecPlanHandler) List(c *gofr.Context) (any, error) {
	if _, err := h.authz.Require(c, model.PermAutomationWrite); err != nil {
		return nil, err
	}

	plans, err := h.plans.List(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	page := listOf(plans, newExecPlanResponse)

	return ExecPlanListResponse{Items: page.Items, TotalCount: page.TotalCount, Events: ticketEvents()}, nil
}

// Get handles GET /api/v1/exec-plans/{id}.
func (h *ExecPlanHandler) Get(c *gofr.Context) (any, error) {
	if _, err := h.authz.Require(c, model.PermAutomationWrite); err != nil {
		return nil, err
	}

	id, err := pathID(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	plan, err := h.plans.Get(c, id)
	if err != nil {
		return nil, toHTTPError(err)
	}

	return newExecPlanResponse(plan), nil
}

// Create handles POST /api/v1/exec-plans.
func (h *ExecPlanHandler) Create(c *gofr.Context) (any, error) {
	if _, err := h.authz.Require(c, model.PermAutomationWrite); err != nil {
		return nil, err
	}

	var req ExecPlanRequest
	if err := bind(c, &req); err != nil {
		return nil, toHTTPError(err)
	}

	cmd, err := req.command()
	if err != nil {
		return nil, toHTTPError(err)
	}

	plan, err := h.plans.Create(c, cmd)
	if err != nil {
		return nil, toHTTPError(err)
	}

	return newExecPlanResponse(plan), nil
}

// Update handles PUT /api/v1/exec-plans/{id}.
func (h *ExecPlanHandler) Update(c *gofr.Context) (any, error) {
	if _, err := h.authz.Require(c, model.PermAutomationWrite); err != nil {
		return nil, err
	}

	id, err := pathID(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	var req ExecPlanRequest
	if err := bind(c, &req); err != nil {
		return nil, toHTTPError(err)
	}

	cmd, err := req.command()
	if err != nil {
		return nil, toHTTPError(err)
	}

	plan, err := h.plans.Update(c, id, cmd)
	if err != nil {
		return nil, toHTTPError(err)
	}

	return newExecPlanResponse(plan), nil
}

// Delete handles DELETE /api/v1/exec-plans/{id}.
func (h *ExecPlanHandler) Delete(c *gofr.Context) (any, error) {
	if _, err := h.authz.Require(c, model.PermAutomationWrite); err != nil {
		return nil, err
	}

	id, err := pathID(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	if err := h.plans.Delete(c, id); err != nil {
		return nil, toHTTPError(err)
	}

	return map[string]string{"status": "deleted"}, nil
}
