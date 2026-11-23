package rest

import (
	"gofr.dev/pkg/gofr"

	"github.com/dennis-dko/qubitdesk/internal/application/v1/command"
	"github.com/dennis-dko/qubitdesk/internal/application/v1/service"
	"github.com/dennis-dko/qubitdesk/internal/domain/model"
)

// ResponseTemplateHandler serves the answer templates: administered by whoever
// sets up how tickets are sorted and answered, and handed, filled, to whoever
// writes in a ticket.
type ResponseTemplateHandler struct {
	templates *service.ResponseTemplateApplicationService
	authz     *Authorizer
}

// NewResponseTemplateHandler wires the handler.
func NewResponseTemplateHandler(
	templates *service.ResponseTemplateApplicationService,
	authz *Authorizer,
) *ResponseTemplateHandler {
	return &ResponseTemplateHandler{templates: templates, authz: authz}
}

// ResponseTemplateResponse is an answer template as the API hands it out; a
// template with no queue is offered in every queue.
type ResponseTemplateResponse struct {
	ID      uint   `json:"id"`
	Name    string `json:"name"`
	Subject string `json:"subject"`
	Body    string `json:"body"`
	QueueID *uint  `json:"queueId"`
	Valid   bool   `json:"valid"`
}

// ResponseTemplateRequest creates or changes a template. On a change an absent
// field stays as it was; ClearQueue offers it in every queue again, which an
// absent QueueID cannot say.
type ResponseTemplateRequest struct {
	Name       *string `json:"name"`
	Subject    *string `json:"subject"`
	Body       *string `json:"body"`
	QueueID    *uint   `json:"queueId"`
	ClearQueue bool    `json:"clearQueue"`
	Valid      *bool   `json:"valid"`
}

func (r ResponseTemplateRequest) command() command.ResponseTemplateCommand {
	return command.ResponseTemplateCommand{
		Name: r.Name, Subject: r.Subject, Body: r.Body, QueueID: r.QueueID, ClearQueue: r.ClearQueue, Valid: r.Valid,
	}
}

func newResponseTemplateResponse(template model.ResponseTemplate) ResponseTemplateResponse {
	return ResponseTemplateResponse{
		ID: template.ID, Name: template.Name, Subject: template.Subject, Body: template.Body,
		QueueID: template.QueueID, Valid: template.Valid,
	}
}

// List handles GET /api/v1/response-templates.
func (h *ResponseTemplateHandler) List(c *gofr.Context) (any, error) {
	if _, err := h.authz.Require(c, model.PermCommunicationWrite); err != nil {
		return nil, err
	}

	templates, err := h.templates.List(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	return listOf(templates, func(t *model.ResponseTemplate) ResponseTemplateResponse {
		return newResponseTemplateResponse(*t)
	}), nil
}

// Get handles GET /api/v1/response-templates/{id}.
func (h *ResponseTemplateHandler) Get(c *gofr.Context) (any, error) {
	if _, err := h.authz.Require(c, model.PermCommunicationWrite); err != nil {
		return nil, err
	}

	id, err := pathID(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	template, err := h.templates.Get(c, id)
	if err != nil {
		return nil, toHTTPError(err)
	}

	return newResponseTemplateResponse(*template), nil
}

// Create handles POST /api/v1/response-templates.
func (h *ResponseTemplateHandler) Create(c *gofr.Context) (any, error) {
	if _, err := h.authz.Require(c, model.PermCommunicationWrite); err != nil {
		return nil, err
	}

	var req ResponseTemplateRequest
	if err := bind(c, &req); err != nil {
		return nil, toHTTPError(err)
	}

	template, err := h.templates.Create(c, req.command())
	if err != nil {
		return nil, toHTTPError(err)
	}

	return newResponseTemplateResponse(*template), nil
}

// Update handles PUT /api/v1/response-templates/{id}.
func (h *ResponseTemplateHandler) Update(c *gofr.Context) (any, error) {
	if _, err := h.authz.Require(c, model.PermCommunicationWrite); err != nil {
		return nil, err
	}

	id, err := pathID(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	var req ResponseTemplateRequest
	if err := bind(c, &req); err != nil {
		return nil, toHTTPError(err)
	}

	template, err := h.templates.Update(c, id, req.command())
	if err != nil {
		return nil, toHTTPError(err)
	}

	return newResponseTemplateResponse(*template), nil
}

// Delete handles DELETE /api/v1/response-templates/{id}.
func (h *ResponseTemplateHandler) Delete(c *gofr.Context) (any, error) {
	if _, err := h.authz.Require(c, model.PermCommunicationWrite); err != nil {
		return nil, err
	}

	id, err := pathID(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	if err := h.templates.Delete(c, id); err != nil {
		return nil, toHTTPError(err)
	}

	return map[string]string{"status": "deleted"}, nil
}

// ForTicket handles GET /api/v1/tickets/{id}/templates: the templates offered for
// the ticket, filled for it, to whoever may write in tickets.
func (h *ResponseTemplateHandler) ForTicket(c *gofr.Context) (any, error) {
	caller, err := h.authz.Require(c, model.PermTicketWrite)
	if err != nil {
		return nil, err
	}

	id, err := pathID(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	templates, err := h.templates.ForTicket(c, caller, id)
	if err != nil {
		return nil, toHTTPError(err)
	}

	items := make([]ResponseTemplateResponse, 0, len(templates))
	for _, template := range templates {
		items = append(items, newResponseTemplateResponse(template))
	}

	return listResponse[ResponseTemplateResponse]{Items: items, TotalCount: uint(len(items))}, nil
}
