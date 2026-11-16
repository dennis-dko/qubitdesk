package rest

import (
	"gofr.dev/pkg/gofr"

	"github.com/dennis-dko/go-ticket-system/internal/application/v1/command"
	"github.com/dennis-dko/go-ticket-system/internal/application/v1/service"
	"github.com/dennis-dko/go-ticket-system/internal/domain/model"
)

// ServiceLevelHandler serves the service levels: read by whoever works tickets,
// which will carry one, and administered by whoever sets up queues.
type ServiceLevelHandler struct {
	levels *service.SLAApplicationService
	authz  *Authorizer
}

// NewServiceLevelHandler wires the handler.
func NewServiceLevelHandler(levels *service.SLAApplicationService, authz *Authorizer) *ServiceLevelHandler {
	return &ServiceLevelHandler{levels: levels, authz: authz}
}

// ServiceLevelResponse is a service level. A calendarId of null counts around
// the clock; a minute count of zero makes no promise.
type ServiceLevelResponse struct {
	ID                   uint   `json:"id"`
	Name                 string `json:"name"`
	Description          string `json:"description"`
	CalendarID           *uint  `json:"calendarId"`
	FirstResponseMinutes int    `json:"firstResponseMinutes"`
	UpdateMinutes        int    `json:"updateMinutes"`
	SolutionMinutes      int    `json:"solutionMinutes"`
	Valid                bool   `json:"valid"`
}

// ServiceLevelRequest creates or changes a service level; on a change an absent
// field stays as it was, and ClearCalendar takes the calendar off.
type ServiceLevelRequest struct {
	Name                 *string `json:"name"`
	Description          *string `json:"description"`
	CalendarID           *uint   `json:"calendarId"`
	ClearCalendar        bool    `json:"clearCalendar"`
	FirstResponseMinutes *int    `json:"firstResponseMinutes"`
	UpdateMinutes        *int    `json:"updateMinutes"`
	SolutionMinutes      *int    `json:"solutionMinutes"`
	Valid                *bool   `json:"valid"`
}

func newServiceLevelResponse(level *model.ServiceLevel) ServiceLevelResponse {
	return ServiceLevelResponse{
		ID: level.ID, Name: level.Name, Description: level.Description, CalendarID: level.CalendarID,
		FirstResponseMinutes: level.FirstResponseMinutes, UpdateMinutes: level.UpdateMinutes,
		SolutionMinutes: level.SolutionMinutes, Valid: level.Valid,
	}
}

func (r ServiceLevelRequest) command() command.ServiceLevelCommand {
	return command.ServiceLevelCommand{
		Name: r.Name, Description: r.Description, CalendarID: r.CalendarID, ClearCalendar: r.ClearCalendar,
		FirstResponseMinutes: r.FirstResponseMinutes, UpdateMinutes: r.UpdateMinutes,
		SolutionMinutes: r.SolutionMinutes, Valid: r.Valid,
	}
}

// List handles GET /api/v1/service-levels.
func (h *ServiceLevelHandler) List(c *gofr.Context) (any, error) {
	if _, err := h.authz.RequireAny(c, model.PermTicketRead, model.PermQueueWrite); err != nil {
		return nil, err
	}

	levels, err := h.levels.List(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	return listOf(levels, newServiceLevelResponse), nil
}

// Get handles GET /api/v1/service-levels/{id}.
func (h *ServiceLevelHandler) Get(c *gofr.Context) (any, error) {
	if _, err := h.authz.RequireAny(c, model.PermTicketRead, model.PermQueueWrite); err != nil {
		return nil, err
	}

	id, err := pathID(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	level, err := h.levels.Get(c, id)
	if err != nil {
		return nil, toHTTPError(err)
	}

	return newServiceLevelResponse(level), nil
}

// Create handles POST /api/v1/service-levels.
func (h *ServiceLevelHandler) Create(c *gofr.Context) (any, error) {
	if _, err := h.authz.Require(c, model.PermQueueWrite); err != nil {
		return nil, err
	}

	var req ServiceLevelRequest
	if err := bind(c, &req); err != nil {
		return nil, toHTTPError(err)
	}

	level, err := h.levels.Create(c, req.command())
	if err != nil {
		return nil, toHTTPError(err)
	}

	return newServiceLevelResponse(level), nil
}

// Update handles PUT /api/v1/service-levels/{id}.
func (h *ServiceLevelHandler) Update(c *gofr.Context) (any, error) {
	if _, err := h.authz.Require(c, model.PermQueueWrite); err != nil {
		return nil, err
	}

	id, err := pathID(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	var req ServiceLevelRequest
	if err := bind(c, &req); err != nil {
		return nil, toHTTPError(err)
	}

	level, err := h.levels.Update(c, id, req.command())
	if err != nil {
		return nil, toHTTPError(err)
	}

	return newServiceLevelResponse(level), nil
}

// Delete handles DELETE /api/v1/service-levels/{id}.
func (h *ServiceLevelHandler) Delete(c *gofr.Context) (any, error) {
	if _, err := h.authz.Require(c, model.PermQueueWrite); err != nil {
		return nil, err
	}

	id, err := pathID(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	if err := h.levels.Delete(c, id); err != nil {
		return nil, toHTTPError(err)
	}

	return map[string]string{"status": "deleted"}, nil
}
