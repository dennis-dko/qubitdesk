package rest

import (
	"strconv"
	"strings"
	"time"

	"gofr.dev/pkg/gofr"

	"github.com/dennis-dko/go-ticket-system/internal/application/v1/command"
	"github.com/dennis-dko/go-ticket-system/internal/application/v1/service"
	"github.com/dennis-dko/go-ticket-system/internal/domain/model"
	"github.com/dennis-dko/go-ticket-system/internal/support/apperror"
)

// TicketHandler serves the tickets under /tickets.
//
// Each route asks for its right first - tickets:read, tickets:create,
// tickets:write or tickets:delete - and refuses without it, because that says
// something about the caller and nothing about any ticket. Which tickets the
// caller reaches is the service's to decide, and a ticket out of reach is not
// found.
type TicketHandler struct {
	tickets service.TicketService
	authz   *Authorizer
}

// NewTicketHandler creates the ticket handler.
func NewTicketHandler(tickets service.TicketService, authz *Authorizer) *TicketHandler {
	return &TicketHandler{tickets: tickets, authz: authz}
}

// TicketResponse is a ticket as the API hands it out, with the names of the
// people it names - a reader who may not list the accounts still has to see who
// owns a ticket.
type TicketResponse struct {
	ID              uint       `json:"id"`
	Number          string     `json:"number"`
	Title           string     `json:"title"`
	QueueID         uint       `json:"queueId"`
	StateID         uint       `json:"stateId"`
	PriorityID      uint       `json:"priorityId"`
	TypeID          uint       `json:"typeId"`
	OwnerID         *uint      `json:"ownerId"`
	OwnerName       string     `json:"ownerName"`
	ResponsibleID   *uint      `json:"responsibleId"`
	ResponsibleName string     `json:"responsibleName"`
	Locked          bool       `json:"locked"`
	LockedAt        *time.Time `json:"lockedAt"`
	Channel         string     `json:"channel"`
	CreatedBy       *uint      `json:"createdBy"`
	CreatedByName   string     `json:"createdByName"`
	CreatedAt       time.Time  `json:"createdAt"`
	UpdatedAt       time.Time  `json:"updatedAt"`
	ClosedAt        *time.Time `json:"closedAt"`
	PendingUntil    *time.Time `json:"pendingUntil"`
}

// TicketDetailResponse is one ticket with what is read about it beside: the
// time noted on it and whether the caller watches it.
type TicketDetailResponse struct {
	TicketResponse

	AccountedMinutes int  `json:"accountedMinutes"`
	Watching         bool `json:"watching"`
}

// CreateTicketRequest opens a ticket with the article it starts from.
type CreateTicketRequest struct {
	Title           *string    `json:"title"`
	QueueID         *uint      `json:"queueId"`
	StateID         *uint      `json:"stateId"`
	PriorityID      *uint      `json:"priorityId"`
	TypeID          *uint      `json:"typeId"`
	PendingUntil    *time.Time `json:"pendingUntil"`
	OwnerID         *uint      `json:"ownerId"`
	ResponsibleID   *uint      `json:"responsibleId"`
	Channel         *string    `json:"channel"`
	CustomerVisible bool       `json:"customerVisible"`
	Subject         *string    `json:"subject"`
	Body            *string    `json:"body"`
	Minutes         int        `json:"minutes"`
}

// UpdateTicketRequest changes a ticket; an absent field is left as it was, and
// ClearOwner and ClearResponsible take the person off, because an id sent as
// null cannot be told apart from one that was not sent.
type UpdateTicketRequest struct {
	Title            *string    `json:"title"`
	QueueID          *uint      `json:"queueId"`
	StateID          *uint      `json:"stateId"`
	PriorityID       *uint      `json:"priorityId"`
	TypeID           *uint      `json:"typeId"`
	PendingUntil     *time.Time `json:"pendingUntil"`
	OwnerID          *uint      `json:"ownerId"`
	ClearOwner       bool       `json:"clearOwner"`
	ResponsibleID    *uint      `json:"responsibleId"`
	ClearResponsible bool       `json:"clearResponsible"`
	Locked           *bool      `json:"locked"`
}

// ArticleRequest writes an article in a ticket, with the time worked for it.
type ArticleRequest struct {
	Channel         *string `json:"channel"`
	CustomerVisible bool    `json:"customerVisible"`
	Subject         *string `json:"subject"`
	Body            *string `json:"body"`
	Minutes         int     `json:"minutes"`
}

// ArticleResponse is an article as the API hands it out.
type ArticleResponse struct {
	ID              uint      `json:"id"`
	Sender          string    `json:"sender"`
	Channel         string    `json:"channel"`
	CustomerVisible bool      `json:"customerVisible"`
	Subject         string    `json:"subject"`
	Body            string    `json:"body"`
	AuthorID        *uint     `json:"authorId"`
	AuthorName      string    `json:"authorName"`
	CreatedAt       time.Time `json:"createdAt"`
}

// HistoryResponse is one line of a ticket's history.
type HistoryResponse struct {
	At        time.Time `json:"at"`
	Actor     string    `json:"actor"`
	ActorID   *uint     `json:"actorId"`
	ActorName string    `json:"actorName"`
	Event     string    `json:"event"`
	OldValue  string    `json:"oldValue"`
	NewValue  string    `json:"newValue"`
}

// AssigneeResponse is somebody a ticket in a queue can be given to.
type AssigneeResponse struct {
	ID   uint   `json:"id"`
	Name string `json:"name"`
}

// List handles GET /api/v1/tickets.
func (h *TicketHandler) List(c *gofr.Context) (any, error) {
	caller, err := h.authz.Require(c, model.PermTicketRead)
	if err != nil {
		return nil, err
	}

	filter, err := ticketListFilter(c, caller)
	if err != nil {
		return nil, toHTTPError(err)
	}

	tickets, total, err := h.tickets.ListTickets(c, caller, filter)
	if err != nil {
		return nil, toHTTPError(err)
	}

	names, err := h.tickets.Names(c, peopleOf(tickets...))
	if err != nil {
		return nil, toHTTPError(err)
	}

	items := make([]TicketResponse, 0, len(tickets))
	for _, ticket := range tickets {
		items = append(items, newTicketResponse(ticket, names))
	}

	return listResponse[TicketResponse]{Items: items, TotalCount: total}, nil
}

// Get handles GET /api/v1/tickets/{id}.
func (h *TicketHandler) Get(c *gofr.Context) (any, error) {
	caller, err := h.authz.Require(c, model.PermTicketRead)
	if err != nil {
		return nil, err
	}

	id, err := pathID(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	return h.detail(c, caller, id)
}

// Create handles POST /api/v1/tickets.
func (h *TicketHandler) Create(c *gofr.Context) (any, error) {
	caller, err := h.authz.Require(c, model.PermTicketCreate)
	if err != nil {
		return nil, err
	}

	var req CreateTicketRequest
	if err := bind(c, &req); err != nil {
		return nil, toHTTPError(err)
	}

	ticket, err := h.tickets.CreateTicket(c, caller, command.CreateTicketCommand{
		Title: req.Title, QueueID: req.QueueID, StateID: req.StateID, PriorityID: req.PriorityID,
		TypeID: req.TypeID, PendingUntil: req.PendingUntil, OwnerID: req.OwnerID, ResponsibleID: req.ResponsibleID,
		Channel: req.Channel, CustomerVisible: req.CustomerVisible, Subject: req.Subject, Body: req.Body,
		Minutes: req.Minutes,
	})
	if err != nil {
		return nil, toHTTPError(err)
	}

	return h.detail(c, caller, ticket.ID)
}

// Update handles PUT /api/v1/tickets/{id}.
func (h *TicketHandler) Update(c *gofr.Context) (any, error) {
	caller, err := h.authz.Require(c, model.PermTicketWrite)
	if err != nil {
		return nil, err
	}

	id, err := pathID(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	var req UpdateTicketRequest
	if err := bind(c, &req); err != nil {
		return nil, toHTTPError(err)
	}

	if _, err := h.tickets.UpdateTicket(c, caller, id, command.UpdateTicketCommand{
		Title: req.Title, QueueID: req.QueueID, StateID: req.StateID, PriorityID: req.PriorityID,
		TypeID: req.TypeID, PendingUntil: req.PendingUntil, OwnerID: req.OwnerID, ClearOwner: req.ClearOwner,
		ResponsibleID: req.ResponsibleID, ClearResponsible: req.ClearResponsible, Locked: req.Locked,
	}); err != nil {
		return nil, toHTTPError(err)
	}

	return h.detail(c, caller, id)
}

// Delete handles DELETE /api/v1/tickets/{id}.
func (h *TicketHandler) Delete(c *gofr.Context) (any, error) {
	caller, err := h.authz.Require(c, model.PermTicketDelete)
	if err != nil {
		return nil, err
	}

	id, err := pathID(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	if err := h.tickets.DeleteTicket(c, caller, id); err != nil {
		return nil, toHTTPError(err)
	}

	return map[string]string{"status": "deleted"}, nil
}

// Articles handles GET /api/v1/tickets/{id}/articles.
func (h *TicketHandler) Articles(c *gofr.Context) (any, error) {
	caller, err := h.authz.Require(c, model.PermTicketRead)
	if err != nil {
		return nil, err
	}

	id, err := pathID(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	articles, err := h.tickets.Articles(c, caller, id)
	if err != nil {
		return nil, toHTTPError(err)
	}

	authors := make([]uint, 0, len(articles))
	for _, article := range articles {
		if article.AuthorID != nil {
			authors = append(authors, *article.AuthorID)
		}
	}

	names, err := h.tickets.Names(c, authors)
	if err != nil {
		return nil, toHTTPError(err)
	}

	return listOf(articles, func(article *model.Article) ArticleResponse {
		return newArticleResponse(article, names)
	}), nil
}

// AddArticle handles POST /api/v1/tickets/{id}/articles.
func (h *TicketHandler) AddArticle(c *gofr.Context) (any, error) {
	caller, err := h.authz.Require(c, model.PermTicketWrite)
	if err != nil {
		return nil, err
	}

	id, err := pathID(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	var req ArticleRequest
	if err := bind(c, &req); err != nil {
		return nil, toHTTPError(err)
	}

	article, err := h.tickets.AddArticle(c, caller, id, command.AddArticleCommand{
		Channel: req.Channel, CustomerVisible: req.CustomerVisible, Subject: req.Subject, Body: req.Body,
		Minutes: req.Minutes,
	})
	if err != nil {
		return nil, toHTTPError(err)
	}

	names, err := h.tickets.Names(c, peopleIDs(article.AuthorID))
	if err != nil {
		return nil, toHTTPError(err)
	}

	return newArticleResponse(article, names), nil
}

// History handles GET /api/v1/tickets/{id}/history.
func (h *TicketHandler) History(c *gofr.Context) (any, error) {
	caller, err := h.authz.Require(c, model.PermTicketRead)
	if err != nil {
		return nil, err
	}

	id, err := pathID(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	history, err := h.tickets.History(c, caller, id)
	if err != nil {
		return nil, toHTTPError(err)
	}

	actors := make([]uint, 0, len(history))
	for _, entry := range history {
		if entry.ActorID != nil {
			actors = append(actors, *entry.ActorID)
		}
	}

	names, err := h.tickets.Names(c, actors)
	if err != nil {
		return nil, toHTTPError(err)
	}

	items := make([]HistoryResponse, 0, len(history))
	for _, entry := range history {
		items = append(items, HistoryResponse{
			At: entry.At, Actor: string(entry.Actor), ActorID: entry.ActorID, ActorName: nameIn(names, entry.ActorID),
			Event: string(entry.Event), OldValue: entry.OldValue, NewValue: entry.NewValue,
		})
	}

	return listResponse[HistoryResponse]{Items: items, TotalCount: uint(len(items))}, nil
}

// Watch handles PUT /api/v1/tickets/{id}/watch.
func (h *TicketHandler) Watch(c *gofr.Context) (any, error) {
	return h.watch(c, true)
}

// Unwatch handles DELETE /api/v1/tickets/{id}/watch.
func (h *TicketHandler) Unwatch(c *gofr.Context) (any, error) {
	return h.watch(c, false)
}

// Assignees handles GET /api/v1/tickets/assignees?queue=: who a ticket in the
// queue can be given to - everybody whose role works in it, whatever else they
// may do.
func (h *TicketHandler) Assignees(c *gofr.Context) (any, error) {
	caller, err := h.authz.Require(c, model.PermTicketRead)
	if err != nil {
		return nil, err
	}

	queue, err := queryUint(c, "queue")
	if err != nil {
		return nil, toHTTPError(err)
	}

	if queue == 0 {
		return nil, toHTTPError(apperror.InvalidFields("queue"))
	}

	people, err := h.tickets.Assignees(c, caller, queue)
	if err != nil {
		return nil, toHTTPError(err)
	}

	return listOf(people, func(user *model.User) AssigneeResponse {
		return AssigneeResponse{ID: user.ID, Name: user.Name}
	}), nil
}

func (h *TicketHandler) watch(c *gofr.Context, watching bool) (any, error) {
	caller, err := h.authz.Require(c, model.PermTicketRead)
	if err != nil {
		return nil, err
	}

	id, err := pathID(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	if err := h.tickets.SetWatching(c, caller, id, watching); err != nil {
		return nil, toHTTPError(err)
	}

	return map[string]bool{"watching": watching}, nil
}

// detail reads a ticket the way a reader opens it: the ticket, the time noted
// on it and whether the caller watches it.
func (h *TicketHandler) detail(c *gofr.Context, caller *service.Principal, id uint) (any, error) {
	ticket, err := h.tickets.GetTicket(c, caller, id)
	if err != nil {
		return nil, toHTTPError(err)
	}

	minutes, err := h.tickets.AccountedMinutes(c, caller, id)
	if err != nil {
		return nil, toHTTPError(err)
	}

	watching, err := h.tickets.Watching(c, caller, id)
	if err != nil {
		return nil, toHTTPError(err)
	}

	names, err := h.tickets.Names(c, peopleOf(ticket))
	if err != nil {
		return nil, toHTTPError(err)
	}

	return TicketDetailResponse{
		TicketResponse: newTicketResponse(ticket, names), AccountedMinutes: minutes, Watching: watching,
	}, nil
}

// ticketListFilter reads what a listing asks for. owner=me is the caller, which
// is what a screen offering "my tickets" means and cannot name by id without
// first asking who it is.
func ticketListFilter(c *gofr.Context, caller *service.Principal) (service.TicketListFilter, error) {
	queue, err := queryUint(c, "queue")
	if err != nil {
		return service.TicketListFilter{}, err
	}

	var states []uint

	for _, raw := range strings.Split(c.Param("state"), ",") {
		if raw = strings.TrimSpace(raw); raw == "" {
			continue
		}

		state, err := parseUint(raw, "state")
		if err != nil {
			return service.TicketListFilter{}, err
		}

		states = append(states, state)
	}

	var owner uint

	switch raw := strings.TrimSpace(c.Param("owner")); raw {
	case "":
	case "me":
		if caller.User != nil {
			owner = caller.User.ID
		}
	default:
		if owner, err = parseUint(raw, "owner"); err != nil {
			return service.TicketListFilter{}, err
		}
	}

	limit, err := strictInt(c.Param("limit"), "limit")
	if err != nil {
		return service.TicketListFilter{}, err
	}

	page, err := strictInt(c.Param("page"), "page")
	if err != nil {
		return service.TicketListFilter{}, err
	}

	return service.TicketListFilter{
		QueueID: queue, StateIDs: states, Unfinished: c.Param("unfinished") == "true", OwnerID: owner,
		Search: c.Param("q"), Limit: limit, Page: page,
	}, nil
}

// strictInt reads a number a listing was given, refusing one that is not a
// number rather than reading it as none: a page that is not what was asked for
// is worse than a plain no.
func strictInt(raw, field string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}

	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, apperror.InvalidFields(field)
	}

	return value, nil
}

func newTicketResponse(ticket *model.Ticket, names map[uint]string) TicketResponse {
	return TicketResponse{
		ID: ticket.ID, Number: ticket.Number, Title: ticket.Title, QueueID: ticket.QueueID,
		StateID: ticket.StateID, PriorityID: ticket.PriorityID, TypeID: ticket.TypeID,
		OwnerID: ticket.OwnerID, OwnerName: nameIn(names, ticket.OwnerID),
		ResponsibleID: ticket.ResponsibleID, ResponsibleName: nameIn(names, ticket.ResponsibleID),
		Locked: ticket.Locked, LockedAt: ticket.LockedAt, Channel: string(ticket.Channel),
		CreatedBy: ticket.CreatedBy, CreatedByName: nameIn(names, ticket.CreatedBy),
		CreatedAt: ticket.CreatedAt, UpdatedAt: ticket.UpdatedAt, ClosedAt: ticket.ClosedAt,
		PendingUntil: ticket.PendingUntil,
	}
}

func newArticleResponse(article *model.Article, names map[uint]string) ArticleResponse {
	return ArticleResponse{
		ID: article.ID, Sender: string(article.Sender), Channel: string(article.Channel),
		CustomerVisible: article.CustomerVisible, Subject: article.Subject, Body: article.Body,
		AuthorID: article.AuthorID, AuthorName: nameIn(names, article.AuthorID), CreatedAt: article.CreatedAt,
	}
}

// peopleOf is every account a set of tickets names.
func peopleOf(tickets ...*model.Ticket) []uint {
	ids := make([]uint, 0, 3*len(tickets))
	for _, ticket := range tickets {
		ids = append(ids, peopleIDs(ticket.OwnerID, ticket.ResponsibleID, ticket.CreatedBy)...)
	}

	return ids
}

func peopleIDs(ids ...*uint) []uint {
	out := make([]uint, 0, len(ids))

	for _, id := range ids {
		if id != nil {
			out = append(out, *id)
		}
	}

	return out
}

// nameIn is an account's name, or nothing for nobody and for one that is gone -
// which is what an erased account leaves a ticket naming.
func nameIn(names map[uint]string, id *uint) string {
	if id == nil {
		return ""
	}

	return names[*id]
}
