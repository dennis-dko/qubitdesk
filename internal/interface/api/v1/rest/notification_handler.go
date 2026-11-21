package rest

import (
	"time"

	"gofr.dev/pkg/gofr"

	"github.com/dennis-dko/qubitdesk/internal/application/v1/service"
	"github.com/dennis-dko/qubitdesk/internal/domain/model"
)

// NotificationHandler serves what an agent was told about their tickets, to the
// agent: notifications are about tickets, so reading them takes reading tickets.
type NotificationHandler struct {
	notifications *service.NotificationService
	authz         *Authorizer
}

// NewNotificationHandler wires the handler.
func NewNotificationHandler(notifications *service.NotificationService, authz *Authorizer) *NotificationHandler {
	return &NotificationHandler{notifications: notifications, authz: authz}
}

// NotificationResponse is one thing an agent was told, as it was worded for
// them when it was sent.
type NotificationResponse struct {
	ID        uint       `json:"id"`
	TicketID  uint       `json:"ticketId"`
	Subject   string     `json:"subject"`
	Body      string     `json:"body"`
	CreatedAt time.Time  `json:"createdAt"`
	ReadAt    *time.Time `json:"readAt"`
}

// NotificationListResponse is an agent's latest notifications, the latest
// first, and how many of all of theirs they have not read.
type NotificationListResponse struct {
	Items  []NotificationResponse `json:"items"`
	Unread int                    `json:"unread"`
}

// Mine handles GET /api/v1/me/notifications.
func (h *NotificationHandler) Mine(c *gofr.Context) (any, error) {
	caller, err := h.authz.Require(c, model.PermTicketRead)
	if err != nil {
		return nil, err
	}

	listed, unread, err := h.notifications.Mine(c, caller)
	if err != nil {
		return nil, toHTTPError(err)
	}

	items := make([]NotificationResponse, 0, len(listed))
	for _, n := range listed {
		items = append(items, newNotificationResponse(n))
	}

	return NotificationListResponse{Items: items, Unread: unread}, nil
}

func newNotificationResponse(n model.Notification) NotificationResponse {
	return NotificationResponse{
		ID: n.ID, TicketID: n.TicketID, Subject: n.Subject, Body: n.Body, CreatedAt: n.CreatedAt, ReadAt: n.ReadAt,
	}
}

// Get handles GET /api/v1/me/notifications/{id}.
func (h *NotificationHandler) Get(c *gofr.Context) (any, error) {
	caller, err := h.authz.Require(c, model.PermTicketRead)
	if err != nil {
		return nil, err
	}

	id, err := pathID(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	n, err := h.notifications.Get(c, caller, id)
	if err != nil {
		return nil, toHTTPError(err)
	}

	return newNotificationResponse(*n), nil
}

// MarkRead handles PUT /api/v1/me/notifications/{id}/read.
func (h *NotificationHandler) MarkRead(c *gofr.Context) (any, error) {
	caller, err := h.authz.Require(c, model.PermTicketRead)
	if err != nil {
		return nil, err
	}

	id, err := pathID(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	if err := h.notifications.MarkRead(c, caller, id); err != nil {
		return nil, toHTTPError(err)
	}

	return map[string]string{"status": "read"}, nil
}

// NotificationChoiceResponse is what an agent may choose about what they are
// told, and what they chose.
type NotificationChoiceResponse struct {
	Optional    []NotificationChoiceItem `json:"optional"`
	SwitchedOff []uint                   `json:"switchedOff"`
	Queues      []NotificationChoiceItem `json:"queues"`
	Followed    []uint                   `json:"followed"`
}

// NotificationChoiceItem is a notification or a queue an agent may choose.
type NotificationChoiceItem struct {
	ID          uint   `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	ParentID    *uint  `json:"parentId,omitempty"`
}

// NotificationChoiceRequest is what an agent chose: the notifications switched
// off and the queues followed, each in place of what they had.
type NotificationChoiceRequest struct {
	SwitchedOff []uint `json:"switchedOff"`
	Followed    []uint `json:"followed"`
}

// Choices handles GET /api/v1/me/notification-choices.
func (h *NotificationHandler) Choices(c *gofr.Context) (any, error) {
	caller, err := h.authz.Require(c, model.PermTicketRead)
	if err != nil {
		return nil, err
	}

	choices, err := h.notifications.Choices(c, caller)
	if err != nil {
		return nil, toHTTPError(err)
	}

	response := NotificationChoiceResponse{
		Optional: make([]NotificationChoiceItem, 0, len(choices.Optional)), SwitchedOff: choices.SwitchedOff,
		Queues: make([]NotificationChoiceItem, 0, len(choices.Queues)), Followed: choices.Followed,
	}

	for _, d := range choices.Optional {
		response.Optional = append(response.Optional, NotificationChoiceItem{ID: d.ID, Name: d.Name, Description: d.Description})
	}

	for _, q := range choices.Queues {
		response.Queues = append(response.Queues, NotificationChoiceItem{ID: q.ID, Name: q.Name, ParentID: q.ParentID})
	}

	return response, nil
}

// Choose handles PUT /api/v1/me/notification-choices.
func (h *NotificationHandler) Choose(c *gofr.Context) (any, error) {
	caller, err := h.authz.Require(c, model.PermTicketRead)
	if err != nil {
		return nil, err
	}

	var req NotificationChoiceRequest
	if err := bind(c, &req); err != nil {
		return nil, toHTTPError(err)
	}

	if err := h.notifications.Choose(c, caller, req.SwitchedOff, req.Followed); err != nil {
		return nil, toHTTPError(err)
	}

	return map[string]string{"status": "chosen"}, nil
}

// MarkAllRead handles PUT /api/v1/me/notifications/read.
func (h *NotificationHandler) MarkAllRead(c *gofr.Context) (any, error) {
	caller, err := h.authz.Require(c, model.PermTicketRead)
	if err != nil {
		return nil, err
	}

	if err := h.notifications.MarkAllRead(c, caller); err != nil {
		return nil, toHTTPError(err)
	}

	return map[string]string{"status": "read"}, nil
}
