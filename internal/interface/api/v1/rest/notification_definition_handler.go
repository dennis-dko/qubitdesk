package rest

import (
	"gofr.dev/pkg/gofr"

	"github.com/dennis-dko/go-ticket-system/internal/application/v1/command"
	"github.com/dennis-dko/go-ticket-system/internal/application/v1/service"
	"github.com/dennis-dko/go-ticket-system/internal/domain/model"
)

// NotificationDefinitionHandler serves the notifications the installation
// sends, read and administered by whoever sets up how tickets are sorted and
// answered.
type NotificationDefinitionHandler struct {
	definitions *service.NotificationDefinitionApplicationService
	authz       *Authorizer
}

// NewNotificationDefinitionHandler wires the handler.
func NewNotificationDefinitionHandler(
	definitions *service.NotificationDefinitionApplicationService,
	authz *Authorizer,
) *NotificationDefinitionHandler {
	return &NotificationDefinitionHandler{definitions: definitions, authz: authz}
}

// NotificationMessageBody is what a notification says in one language.
type NotificationMessageBody struct {
	Language string `json:"language"`
	Subject  string `json:"subject"`
	Body     string `json:"body"`
}

// NotificationDefinitionResponse is a notification as the API hands it out.
type NotificationDefinitionResponse struct {
	ID          uint                      `json:"id"`
	Name        string                    `json:"name"`
	Description string                    `json:"description"`
	Valid       bool                      `json:"valid"`
	Events      []string                  `json:"events"`
	Criteria    JobCriteriaBody           `json:"criteria"`
	Recipients  []string                  `json:"recipients"`
	OncePerDay  bool                      `json:"oncePerDay"`
	Optional    bool                      `json:"optional"`
	Messages    []NotificationMessageBody `json:"messages"`
}

// NotificationDefinitionListResponse is every notification, with the events
// and the recipients one can name and the languages one can speak, so a form
// offers what the server takes.
type NotificationDefinitionListResponse struct {
	Items      []NotificationDefinitionResponse `json:"items"`
	TotalCount uint                             `json:"totalCount"`
	Events     []string                         `json:"events"`
	Recipients []string                         `json:"recipients"`
	Languages  []string                         `json:"languages"`
}

// NotificationDefinitionRequest creates or changes a notification. On a change
// an absent field stays as it was, and a list sent takes the place of the one
// the notification had.
type NotificationDefinitionRequest struct {
	Name        *string                    `json:"name"`
	Description *string                    `json:"description"`
	Valid       *bool                      `json:"valid"`
	Events      *[]string                  `json:"events"`
	Criteria    *JobCriteriaBody           `json:"criteria"`
	Recipients  *[]string                  `json:"recipients"`
	OncePerDay  *bool                      `json:"oncePerDay"`
	Optional    *bool                      `json:"optional"`
	Messages    *[]NotificationMessageBody `json:"messages"`
}

func (r NotificationDefinitionRequest) command() command.NotificationDefinitionCommand {
	cmd := command.NotificationDefinitionCommand{
		Name: r.Name, Description: r.Description, Valid: r.Valid, Events: r.Events, Recipients: r.Recipients,
		OncePerDay: r.OncePerDay, Optional: r.Optional,
	}

	if r.Criteria != nil {
		cmd.Criteria = r.Criteria.command()
	}

	if r.Messages != nil {
		messages := make([]command.NotificationMessageCommand, 0, len(*r.Messages))
		for _, message := range *r.Messages {
			messages = append(messages, command.NotificationMessageCommand{
				Language: message.Language, Subject: message.Subject, Body: message.Body,
			})
		}

		cmd.Messages = &messages
	}

	return cmd
}

func newNotificationDefinitionResponse(definition *model.NotificationDefinition) NotificationDefinitionResponse {
	response := NotificationDefinitionResponse{
		ID: definition.ID, Name: definition.Name, Description: definition.Description, Valid: definition.Valid,
		Events: make([]string, 0, len(definition.Events)), Criteria: newJobCriteriaBody(definition.Criteria),
		Recipients: make([]string, 0, len(definition.Recipients)), OncePerDay: definition.OncePerDay,
		Optional: definition.Optional, Messages: make([]NotificationMessageBody, 0, len(definition.Messages)),
	}

	for _, event := range definition.Events {
		response.Events = append(response.Events, string(event))
	}

	for _, recipient := range definition.Recipients {
		response.Recipients = append(response.Recipients, string(recipient))
	}

	for _, message := range definition.Messages {
		response.Messages = append(response.Messages, NotificationMessageBody{
			Language: message.Language, Subject: message.Subject, Body: message.Body,
		})
	}

	return response
}

// List handles GET /api/v1/notification-definitions.
func (h *NotificationDefinitionHandler) List(c *gofr.Context) (any, error) {
	if _, err := h.authz.Require(c, model.PermQueueWrite); err != nil {
		return nil, err
	}

	definitions, err := h.definitions.List(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	page := listOf(definitions, newNotificationDefinitionResponse)
	recipients := make([]string, 0)

	for _, recipient := range model.NotificationRecipients() {
		recipients = append(recipients, string(recipient))
	}

	return NotificationDefinitionListResponse{
		Items: page.Items, TotalCount: page.TotalCount, Events: ticketEvents(), Recipients: recipients,
		Languages: model.SupportedLanguages(),
	}, nil
}

// Get handles GET /api/v1/notification-definitions/{id}.
func (h *NotificationDefinitionHandler) Get(c *gofr.Context) (any, error) {
	if _, err := h.authz.Require(c, model.PermQueueWrite); err != nil {
		return nil, err
	}

	id, err := pathID(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	definition, err := h.definitions.Get(c, id)
	if err != nil {
		return nil, toHTTPError(err)
	}

	return newNotificationDefinitionResponse(definition), nil
}

// Create handles POST /api/v1/notification-definitions.
func (h *NotificationDefinitionHandler) Create(c *gofr.Context) (any, error) {
	if _, err := h.authz.Require(c, model.PermQueueWrite); err != nil {
		return nil, err
	}

	var req NotificationDefinitionRequest
	if err := bind(c, &req); err != nil {
		return nil, toHTTPError(err)
	}

	definition, err := h.definitions.Create(c, req.command())
	if err != nil {
		return nil, toHTTPError(err)
	}

	return newNotificationDefinitionResponse(definition), nil
}

// Update handles PUT /api/v1/notification-definitions/{id}.
func (h *NotificationDefinitionHandler) Update(c *gofr.Context) (any, error) {
	if _, err := h.authz.Require(c, model.PermQueueWrite); err != nil {
		return nil, err
	}

	id, err := pathID(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	var req NotificationDefinitionRequest
	if err := bind(c, &req); err != nil {
		return nil, toHTTPError(err)
	}

	definition, err := h.definitions.Update(c, id, req.command())
	if err != nil {
		return nil, toHTTPError(err)
	}

	return newNotificationDefinitionResponse(definition), nil
}

// Delete handles DELETE /api/v1/notification-definitions/{id}.
func (h *NotificationDefinitionHandler) Delete(c *gofr.Context) (any, error) {
	if _, err := h.authz.Require(c, model.PermQueueWrite); err != nil {
		return nil, err
	}

	id, err := pathID(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	if err := h.definitions.Delete(c, id); err != nil {
		return nil, toHTTPError(err)
	}

	return map[string]string{"status": "deleted"}, nil
}
