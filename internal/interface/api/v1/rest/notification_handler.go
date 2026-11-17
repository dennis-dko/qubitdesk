package rest

import (
	"time"

	"gofr.dev/pkg/gofr"

	"github.com/dennis-dko/go-ticket-system/internal/application/v1/service"
	"github.com/dennis-dko/go-ticket-system/internal/domain/model"
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
		items = append(items, NotificationResponse{
			ID: n.ID, TicketID: n.TicketID, Subject: n.Subject, Body: n.Body, CreatedAt: n.CreatedAt, ReadAt: n.ReadAt,
		})
	}

	return NotificationListResponse{Items: items, Unread: unread}, nil
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
