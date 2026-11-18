package rest

import (
	"time"

	"gofr.dev/pkg/gofr"
	"gofr.dev/pkg/gofr/http/response"

	"github.com/dennis-dko/go-ticket-system/internal/application/v1/command"
	"github.com/dennis-dko/go-ticket-system/internal/application/v1/service"
	"github.com/dennis-dko/go-ticket-system/internal/domain/model"
)

// PortalHandler serves the customer's side of the tickets under /portal, to an
// account holding portal:use. Which tickets it reaches is the contact's the
// account signs in as, and nothing about any queue - see service.PortalService.
type PortalHandler struct {
	portal service.PortalService
	authz  *Authorizer
}

// NewPortalHandler creates the portal handler.
func NewPortalHandler(portal service.PortalService, authz *Authorizer) *PortalHandler {
	return &PortalHandler{portal: portal, authz: authz}
}

// PortalTicketResponse is a ticket as its customer is shown it: what it is about
// and where it stands. The state is named as it is stored, with its type, so
// that the screen writes a shipped one in the reader's words.
type PortalTicketResponse struct {
	ID          uint       `json:"id"`
	Number      string     `json:"number"`
	Title       string     `json:"title"`
	State       string     `json:"state"`
	StateType   string     `json:"stateType"`
	ContactName string     `json:"contactName"`
	CreatedAt   time.Time  `json:"createdAt"`
	UpdatedAt   time.Time  `json:"updatedAt"`
	ClosedAt    *time.Time `json:"closedAt"`
}

// PortalArticleResponse is an article a customer is shown, with its files.
type PortalArticleResponse struct {
	ID          uint                 `json:"id"`
	TicketID    uint                 `json:"ticketId"`
	Sender      string               `json:"sender"`
	AuthorName  string               `json:"authorName"`
	Subject     string               `json:"subject"`
	Body        string               `json:"body"`
	CreatedAt   time.Time            `json:"createdAt"`
	Attachments []AttachmentResponse `json:"attachments"`
}

// PortalQueueResponse is a queue a customer opens a ticket in.
type PortalQueueResponse struct {
	ID   uint   `json:"id"`
	Name string `json:"name"`
}

// PortalTicketRequest opens a ticket from the portal.
type PortalTicketRequest struct {
	Title   *string `json:"title"`
	Body    *string `json:"body"`
	QueueID *uint   `json:"queueId"`
}

// PortalAnswerRequest answers a ticket from the portal.
type PortalAnswerRequest struct {
	Subject *string `json:"subject"`
	Body    *string `json:"body"`
}

func newPortalTicketResponse(ticket service.PortalTicket) PortalTicketResponse {
	return PortalTicketResponse{
		ID: ticket.ID, Number: ticket.Number, Title: ticket.Title, State: ticket.State,
		StateType: string(ticket.StateType), ContactName: ticket.ContactName,
		CreatedAt: ticket.CreatedAt, UpdatedAt: ticket.UpdatedAt, ClosedAt: ticket.ClosedAt,
	}
}

// Tickets handles GET /api/v1/portal/tickets: a page of the tickets the caller
// follows, those still being worked on unless ?finished=true asks for the
// finished ones.
func (h *PortalHandler) Tickets(c *gofr.Context) (any, error) {
	caller, err := h.authz.Require(c, model.PermPortalUse)
	if err != nil {
		return nil, err
	}

	limit, err := strictInt(c.Param("limit"), "limit")
	if err != nil {
		return nil, toHTTPError(err)
	}

	page, err := strictInt(c.Param("page"), "page")
	if err != nil {
		return nil, toHTTPError(err)
	}

	tickets, total, err := h.portal.PortalTickets(c, caller, service.PortalTicketFilter{
		Finished: boolParam(c, "finished"), Limit: limit, Page: page,
	})
	if err != nil {
		return nil, toHTTPError(err)
	}

	items := make([]PortalTicketResponse, 0, len(tickets))
	for _, ticket := range tickets {
		items = append(items, newPortalTicketResponse(ticket))
	}

	return listResponse[PortalTicketResponse]{Items: items, TotalCount: total}, nil
}

// Ticket handles GET /api/v1/portal/tickets/{id}.
func (h *PortalHandler) Ticket(c *gofr.Context) (any, error) {
	caller, err := h.authz.Require(c, model.PermPortalUse)
	if err != nil {
		return nil, err
	}

	id, err := pathID(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	ticket, err := h.portal.PortalTicketOf(c, caller, id)
	if err != nil {
		return nil, toHTTPError(err)
	}

	return newPortalTicketResponse(*ticket), nil
}

// Articles handles GET /api/v1/portal/tickets/{id}/articles: what the customer
// is shown of the ticket, the oldest first.
func (h *PortalHandler) Articles(c *gofr.Context) (any, error) {
	caller, err := h.authz.Require(c, model.PermPortalUse)
	if err != nil {
		return nil, err
	}

	id, err := pathID(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	articles, err := h.portal.PortalArticles(c, caller, id)
	if err != nil {
		return nil, toHTTPError(err)
	}

	items := make([]PortalArticleResponse, 0, len(articles))

	for _, article := range articles {
		files := make([]AttachmentResponse, 0, len(article.Attachments))
		for _, file := range article.Attachments {
			files = append(files, newAttachmentResponse(file))
		}

		items = append(items, PortalArticleResponse{
			ID: article.ID, TicketID: id, Sender: string(article.Sender), AuthorName: article.AuthorName,
			Subject: article.Subject, Body: article.Body, CreatedAt: article.CreatedAt, Attachments: files,
		})
	}

	return listResponse[PortalArticleResponse]{Items: items, TotalCount: uint(len(items))}, nil
}

// Queues handles GET /api/v1/portal/queues: the queues a ticket is opened in
// from the portal.
func (h *PortalHandler) Queues(c *gofr.Context) (any, error) {
	caller, err := h.authz.Require(c, model.PermPortalUse)
	if err != nil {
		return nil, err
	}

	queues, err := h.portal.PortalQueues(c, caller)
	if err != nil {
		return nil, toHTTPError(err)
	}

	return listOf(queues, func(queue *model.Queue) PortalQueueResponse {
		return PortalQueueResponse{ID: queue.ID, Name: queue.Name}
	}), nil
}

// Open handles POST /api/v1/portal/tickets: a ticket opened for the contact the
// caller signs in as.
func (h *PortalHandler) Open(c *gofr.Context) (any, error) {
	caller, err := h.authz.Require(c, model.PermPortalUse)
	if err != nil {
		return nil, err
	}

	var req PortalTicketRequest
	if err := bind(c, &req); err != nil {
		return nil, toHTTPError(err)
	}

	opened, err := h.portal.OpenFromPortal(c, caller, command.PortalTicketCommand{
		Title: req.Title, Body: req.Body, QueueID: req.QueueID,
	})
	if err != nil {
		return nil, toHTTPError(err)
	}

	ticket, err := h.portal.PortalTicketOf(c, caller, opened.ID)
	if err != nil {
		return nil, toHTTPError(err)
	}

	return newPortalTicketResponse(*ticket), nil
}

// Answer handles POST /api/v1/portal/tickets/{id}/articles: the customer's
// answer, whose ticketId says which ticket it landed in - a new one, where the
// ticket was finished and its queue opens one for an answer.
func (h *PortalHandler) Answer(c *gofr.Context) (any, error) {
	caller, err := h.authz.Require(c, model.PermPortalUse)
	if err != nil {
		return nil, err
	}

	id, err := pathID(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	var req PortalAnswerRequest
	if err := bind(c, &req); err != nil {
		return nil, toHTTPError(err)
	}

	article, err := h.portal.AnswerFromPortal(c, caller, id, command.PortalAnswerCommand{
		Subject: req.Subject, Body: req.Body,
	})
	if err != nil {
		return nil, toHTTPError(err)
	}

	return PortalArticleResponse{
		ID: article.ID, TicketID: article.TicketID, Sender: string(article.Sender), Subject: article.Subject,
		Body: article.Body, CreatedAt: article.CreatedAt, Attachments: []AttachmentResponse{},
	}, nil
}

// Upload handles POST /api/v1/portal/tickets/{id}/articles/{article}/attachments:
// a file kept with an article the caller wrote.
func (h *PortalHandler) Upload(c *gofr.Context) (any, error) {
	caller, err := h.authz.Require(c, model.PermPortalUse)
	if err != nil {
		return nil, err
	}

	id, err := pathID(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	articleID, err := parseUint(c.PathParam("article"), "article")
	if err != nil {
		return nil, toHTTPError(err)
	}

	file, err := readUpload(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	attachment, err := h.portal.AttachFromPortal(c, caller, id, articleID, file)
	if err != nil {
		return nil, toHTTPError(err)
	}

	return newAttachmentResponse(attachment), nil
}

// Download handles GET /api/v1/portal/tickets/{id}/attachments/{attachment},
// always as bytes for the reason DownloadAttachment gives.
func (h *PortalHandler) Download(c *gofr.Context) (any, error) {
	caller, err := h.authz.Require(c, model.PermPortalUse)
	if err != nil {
		return nil, err
	}

	id, err := pathID(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	attachmentID, err := parseUint(c.PathParam("attachment"), "attachment")
	if err != nil {
		return nil, toHTTPError(err)
	}

	attachment, err := h.portal.PortalAttachment(c, caller, id, attachmentID)
	if err != nil {
		return nil, toHTTPError(err)
	}

	return response.File{Content: attachment.Content, ContentType: "application/octet-stream"}, nil
}
