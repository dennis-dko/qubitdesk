package service

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/dennis-dko/qubitdesk/internal/application/v1/command"
	"github.com/dennis-dko/qubitdesk/internal/domain/model"
	"github.com/dennis-dko/qubitdesk/internal/domain/repository"
	"github.com/dennis-dko/qubitdesk/internal/support/apperror"
)

// PortalService is the customer's side of the tickets: the ones the caller
// follows as a contact, what of them a customer is shown, and opening and
// answering one.
//
// It is reached with portal:use, and what it reaches is decided by the contact
// the caller signs in as and by nothing else - not by any queue, and not by
// any right of the staff side. An account that signs in as no contact follows
// no ticket, and a ticket it does not follow is not found, as a ticket in a
// queue an agent does not reach is not.
type PortalService interface {
	PortalTickets(ctx context.Context, caller *Principal, filter PortalTicketFilter) ([]PortalTicket, uint, error)
	PortalTicketOf(ctx context.Context, caller *Principal, id uint) (*PortalTicket, error)
	PortalArticles(ctx context.Context, caller *Principal, id uint) ([]PortalArticle, error)
	PortalAttachment(ctx context.Context, caller *Principal, ticketID, attachmentID uint) (*model.Attachment, error)
	PortalQueues(ctx context.Context, caller *Principal) ([]*model.Queue, error)
	OpenFromPortal(ctx context.Context, caller *Principal, cmd command.PortalTicketCommand) (*model.Ticket, error)
	AnswerFromPortal(ctx context.Context, caller *Principal, id uint, cmd command.PortalAnswerCommand) (*model.Article, error)
	AttachFromPortal(ctx context.Context, caller *Principal, ticketID, articleID uint,
		file AttachedFile) (*model.Attachment, error)
}

var _ PortalService = (*TicketApplicationService)(nil)

// PortalTicketFilter is what a customer's listing asks for: the tickets still
// being worked on, or the finished ones, a page at a time.
type PortalTicketFilter struct {
	Finished bool
	Limit    int
	Page     int
}

// PortalTicket is a ticket as its customer is shown it: what it is about and
// where it stands, and nothing of how the service desk works it - not its
// queue, its owner, its lock or what it is owed.
type PortalTicket struct {
	ID        uint
	Number    string
	Title     string
	State     string
	StateType model.StateType

	// ContactName is who the ticket is for, which is somebody else on a ticket
	// of the contact's organisation.
	ContactName string

	CreatedAt time.Time
	UpdatedAt time.Time
	ClosedAt  *time.Time
}

// PortalArticle is an article a customer is shown, with the files that came
// with it. AuthorName is the account that wrote it, empty for the application.
type PortalArticle struct {
	ID          uint
	Sender      model.Sender
	AuthorName  string
	Subject     string
	Body        string
	CreatedAt   time.Time
	Attachments []*model.Attachment
}

// PortalTickets lists the tickets the caller follows: those for their contact,
// and their organisation's where the contact sees them, the most recently
// changed first.
func (s *TicketApplicationService) PortalTickets(
	ctx context.Context,
	caller *Principal,
	filter PortalTicketFilter,
) ([]PortalTicket, uint, error) {
	if filter.Limit == 0 {
		filter.Limit = DefaultTicketPage
	}

	if filter.Limit < 0 || filter.Limit > MaxTicketPage {
		return nil, 0, apperror.InvalidFields("limit")
	}

	if filter.Page < 0 {
		return nil, 0, apperror.InvalidFields("page")
	}

	contact, err := s.contactOf(ctx, caller)
	if err != nil || contact == nil {
		return []PortalTicket{}, 0, err
	}

	queues, err := s.queues.GetAll(ctx)
	if err != nil {
		return nil, 0, err
	}

	all := make([]uint, 0, len(queues))
	for _, queue := range queues {
		all = append(all, queue.ID)
	}

	states, err := s.states.GetAll(ctx)
	if err != nil {
		return nil, 0, err
	}

	var chosen []uint

	for _, state := range states {
		if slices.Contains(model.FinishedStateTypes(), state.Type) == filter.Finished {
			chosen = append(chosen, state.ID)
		}
	}

	if len(chosen) == 0 {
		return []PortalTicket{}, 0, nil
	}

	follower := &repository.Follower{ContactID: contact.ID}
	if contact.SeesOrganisation && contact.OrganisationID != nil {
		organisation := *contact.OrganisationID
		follower.OrganisationID = &organisation
	}

	tickets, total, err := s.tickets.List(ctx, repository.TicketFilter{
		QueueIDs: all, StateIDs: chosen, Follower: follower, Limit: filter.Limit, Page: filter.Page,
	})
	if err != nil {
		return nil, 0, err
	}

	shown, err := s.asPortalTickets(ctx, tickets, states)
	if err != nil {
		return nil, 0, err
	}

	return shown, total, nil
}

// PortalTicketOf is one ticket the caller follows.
func (s *TicketApplicationService) PortalTicketOf(ctx context.Context, caller *Principal, id uint) (*PortalTicket, error) {
	ticket, _, err := s.followed(ctx, caller, id)
	if err != nil {
		return nil, err
	}

	states, err := s.states.GetAll(ctx)
	if err != nil {
		return nil, err
	}

	shown, err := s.asPortalTickets(ctx, []*model.Ticket{ticket}, states)
	if err != nil {
		return nil, err
	}

	return &shown[0], nil
}

// PortalArticles are the articles of a ticket the caller follows that the
// customer is shown, the oldest first, each with its files.
func (s *TicketApplicationService) PortalArticles(ctx context.Context, caller *Principal, id uint) ([]PortalArticle, error) {
	ticket, _, err := s.followed(ctx, caller, id)
	if err != nil {
		return nil, err
	}

	articles, err := s.tickets.Articles(ctx, ticket.ID)
	if err != nil {
		return nil, err
	}

	files, err := s.tickets.Attachments(ctx, ticket.ID)
	if err != nil {
		return nil, err
	}

	var authors []uint

	for _, article := range articles {
		if article.CustomerVisible && article.AuthorID != nil {
			authors = append(authors, *article.AuthorID)
		}
	}

	names, err := s.Names(ctx, authors)
	if err != nil {
		return nil, err
	}

	shown := make([]PortalArticle, 0, len(articles))

	for _, article := range articles {
		if !article.CustomerVisible {
			continue
		}

		entry := PortalArticle{
			ID: article.ID, Sender: article.Sender, Subject: article.Subject, Body: article.Body,
			CreatedAt: article.CreatedAt, Attachments: []*model.Attachment{},
		}

		if article.AuthorID != nil {
			entry.AuthorName = names[*article.AuthorID]
		}

		for _, file := range files {
			if file.ArticleID == article.ID {
				entry.Attachments = append(entry.Attachments, file)
			}
		}

		shown = append(shown, entry)
	}

	return shown, nil
}

// PortalAttachment is one file of a ticket the caller follows, with its
// content, where the article it came with is one the customer is shown; any
// other is not found, so that a ticket a customer follows is no window onto an
// internal note.
func (s *TicketApplicationService) PortalAttachment(
	ctx context.Context,
	caller *Principal,
	ticketID, attachmentID uint,
) (*model.Attachment, error) {
	ticket, _, err := s.followed(ctx, caller, ticketID)
	if err != nil {
		return nil, err
	}

	notFound := apperror.NotFound("attachment", strconv.FormatUint(uint64(attachmentID), 10))

	attachment, err := s.tickets.Attachment(ctx, attachmentID)
	if err != nil {
		return nil, err
	}

	if attachment.TicketID != ticket.ID {
		return nil, notFound
	}

	articles, err := s.tickets.Articles(ctx, ticket.ID)
	if err != nil {
		return nil, err
	}

	if !slices.ContainsFunc(articles, func(article *model.Article) bool {
		return article.ID == attachment.ArticleID && article.CustomerVisible
	}) {
		return nil, notFound
	}

	return attachment, nil
}

// PortalQueues are the queues a customer opens a ticket in from the portal:
// those offered there that still take tickets, by name.
func (s *TicketApplicationService) PortalQueues(ctx context.Context, caller *Principal) ([]*model.Queue, error) {
	if _, err := s.portalContact(ctx, caller); err != nil {
		return nil, err
	}

	return s.portalQueues(ctx)
}

func (s *TicketApplicationService) portalQueues(ctx context.Context) ([]*model.Queue, error) {
	queues, err := s.queues.GetAll(ctx)
	if err != nil {
		return nil, err
	}

	return slices.DeleteFunc(queues, func(queue *model.Queue) bool { return !queue.Portal || !queue.Valid }), nil
}

// OpenFromPortal opens a ticket for the contact the caller signs in as, in the
// queue they name or, where the portal offers one queue only, in that one -
// new, normal and unclassified, as any ticket starts, and owed what its queue
// promises. What they write is its first article, from the customer and shown
// to them.
func (s *TicketApplicationService) OpenFromPortal(
	ctx context.Context,
	caller *Principal,
	cmd command.PortalTicketCommand,
) (*model.Ticket, error) {
	contact, err := s.portalContact(ctx, caller)
	if err != nil {
		return nil, err
	}

	title := strings.TrimSpace(valueOf(cmd.Title))
	body := strings.TrimSpace(valueOf(cmd.Body))

	var invalid []string

	if title == "" || model.TooLong(title, model.MaxTicketTitleLength) {
		invalid = append(invalid, "title")
	}

	if body == "" {
		invalid = append(invalid, "body")
	}

	if len(invalid) > 0 {
		return nil, apperror.InvalidFields(invalid...)
	}

	queue, err := s.portalQueue(ctx, cmd.QueueID)
	if err != nil {
		return nil, err
	}

	now := s.now().UTC()

	return s.openForCustomer(ctx, caller, contact, queue, &model.Article{
		Sender: model.SenderCustomer, Channel: model.ChannelWeb, CustomerVisible: true,
		Subject: title, Body: body, AuthorID: callerID(caller), CreatedAt: now,
	}, title)
}

// portalQueue is the queue a ticket from the portal is opened in: the one named,
// where the portal offers it, or the only one it offers. A queue it does not
// offer is refused as a field, not found, so that the portal tells nobody which
// other queues there are.
func (s *TicketApplicationService) portalQueue(ctx context.Context, id *uint) (*model.Queue, error) {
	offered, err := s.portalQueues(ctx)
	if err != nil {
		return nil, err
	}

	if id == nil {
		switch len(offered) {
		case 0:
			return nil, apperror.Conflictf("the portal offers no queue to open a ticket in").
				WithCode("portalOffersNoQueue")
		case 1:
			return offered[0], nil
		default:
			return nil, apperror.InvalidFields("queueId")
		}
	}

	for _, queue := range offered {
		if queue.ID == *id {
			return queue, nil
		}
	}

	return nil, apperror.InvalidFields("queueId")
}

// openForCustomer opens a ticket in the queue for the contact, with the article
// as its first.
func (s *TicketApplicationService) openForCustomer(
	ctx context.Context,
	caller *Principal,
	contact *model.Contact,
	queue *model.Queue,
	article *model.Article,
	title string,
) (*model.Ticket, error) {
	now := article.CreatedAt
	ticket := &model.Ticket{
		Title: title, QueueID: queue.ID, Channel: model.ChannelWeb,
		CreatedBy: callerID(caller), CreatedAt: now, UpdatedAt: now,
	}

	state, err := s.stateNamed(ctx, model.StateNew)
	if err != nil {
		return nil, err
	}

	if err := s.placeInState(ticket, state, nil, now); err != nil {
		return nil, err
	}

	if ticket.PriorityID, err = s.chosenPriority(ctx, nil); err != nil {
		return nil, err
	}

	if ticket.TypeID, err = s.chosenType(ctx, nil); err != nil {
		return nil, err
	}

	if err := s.chooseCustomer(ctx, ticket, customerChoice{contactID: &contact.ID}, nil); err != nil {
		return nil, err
	}

	if err := s.openOwed(ctx, ticket, nil, queue); err != nil {
		return nil, err
	}

	if ticket.UpdateDue, err = s.updateOwed(ctx, ticket, []*model.Article{article}, state.Type); err != nil {
		return nil, err
	}

	opened, err := s.tickets.Create(ctx, ticket, article, 0, s.numbered(ctx, now))
	if err != nil {
		return nil, err
	}

	s.count(ctx, MetricTicketsOpened, "channel", string(opened.Channel))
	s.raise(ctx, caller, opened.ID, []model.TicketEvent{model.EventTicketCreate, model.EventArticleCreate})

	return opened, nil
}

// numbered numbers a ticket opened at the moment: the day in the installation's
// zone, then the id.
func (s *TicketApplicationService) numbered(ctx context.Context, now time.Time) func(id uint) string {
	zone, err := time.LoadLocation(s.zone(ctx))
	if err != nil {
		zone = time.UTC
	}

	day := now.In(zone).Format("20060102")

	return func(id uint) string { return fmt.Sprintf("%s%08d", day, id) }
}

// AnswerFromPortal is a customer's answer to a ticket they follow, and what it
// does to the ticket is what an answer by mail does in KIX18 and OTRS: a ticket
// that waits is open again, because the customer's answer is what it waited
// for; one that is finished follows its queue - opened again, refused, or left
// as it is while the answer opens a new ticket - and the answer says which
// ticket it landed in. A new ticket stays new.
//
// The answer does not answer the ticket for its service level and does not
// start a lock's time again: both count what the customer was told, not what
// they said.
func (s *TicketApplicationService) AnswerFromPortal(
	ctx context.Context,
	caller *Principal,
	id uint,
	cmd command.PortalAnswerCommand,
) (*model.Article, error) {
	ticket, contact, err := s.followed(ctx, caller, id)
	if err != nil {
		return nil, err
	}

	if !contact.Valid {
		return nil, apperror.Conflictf("the contact %q is no longer offered", contact.Name).
			WithCode("contactRetired", contact.Name)
	}

	subject := strings.TrimSpace(valueOf(cmd.Subject))
	if subject == "" {
		subject = ticket.Title
	}

	body := strings.TrimSpace(valueOf(cmd.Body))

	var invalid []string

	if model.TooLong(subject, model.MaxTicketTitleLength) {
		invalid = append(invalid, "subject")
	}

	if body == "" {
		invalid = append(invalid, "body")
	}

	if len(invalid) > 0 {
		return nil, apperror.InvalidFields(invalid...)
	}

	state, err := s.states.GetByID(ctx, ticket.StateID)
	if err != nil {
		return nil, err
	}

	now := s.now().UTC()
	article := &model.Article{
		TicketID: ticket.ID, Sender: model.SenderCustomer, Channel: model.ChannelWeb, CustomerVisible: true,
		Subject: subject, Body: body, AuthorID: callerID(caller), CreatedAt: now,
	}

	reopens := state.Type == model.StateTypePendingReminder || state.Type == model.StateTypePendingAuto

	if slices.Contains(model.FinishedStateTypes(), state.Type) {
		queue, err := s.queues.GetByID(ctx, ticket.QueueID)
		if err != nil {
			return nil, err
		}

		switch queue.FollowUp {
		case model.FollowUpReject:
			return nil, apperror.Conflictf("ticket %s is finished, and its queue takes no answer to it", ticket.Number).
				WithCode("followUpRejected", ticket.Number)
		case model.FollowUpNewTicket:
			if !queue.Valid {
				return nil, apperror.Conflictf("the queue %q takes no tickets any more", queue.Name).
					WithCode("queueRetired", queue.Name)
			}

			article.TicketID = 0

			opened, err := s.openForCustomer(ctx, caller, contact, queue, article, ticket.Title)
			if err != nil {
				return nil, err
			}

			return s.firstArticleOf(ctx, opened.ID)
		default:
			reopens = true
		}
	}

	change := historyByCustomer(ticket.ID, now, caller)
	change.add(model.HistoryArticle, "", string(article.Channel))

	var reopen *model.Reopening

	leftIn := state.Type

	if reopens {
		open, err := s.stateNamed(ctx, model.StateOpen)
		if err != nil {
			return nil, err
		}

		moved := historyByCustomer(ticket.ID, now, caller)
		moved.add(model.HistoryState, state.Name, open.Name)
		reopen = &model.Reopening{From: state.ID, To: open.ID, History: moved.entries[0]}
		leftIn = open.Type
	}

	articles, err := s.tickets.Articles(ctx, ticket.ID)
	if err != nil {
		return nil, err
	}

	updateDue, err := s.updateOwed(ctx, ticket, append(articles, article), leftIn)
	if err != nil {
		return nil, err
	}

	written, moved, err := s.tickets.AddFollowUp(ctx, article, change.entries, reopen, updateDue)
	if err != nil {
		return nil, err
	}

	entries := change.entries
	if moved {
		entries = append(entries, reopen.History)
	}

	s.raise(ctx, caller, ticket.ID, model.EventsOf(entries))

	return written, nil
}

// firstArticleOf is the article a ticket was opened with.
func (s *TicketApplicationService) firstArticleOf(ctx context.Context, ticketID uint) (*model.Article, error) {
	articles, err := s.tickets.Articles(ctx, ticketID)
	if err != nil {
		return nil, err
	}

	if len(articles) == 0 {
		return nil, apperror.NotFound("article", strconv.FormatUint(uint64(ticketID), 10))
	}

	return articles[0], nil
}

// AttachFromPortal keeps a file with an article the caller wrote in a ticket
// they follow - their own words, never an agent's - bounded as every file is.
func (s *TicketApplicationService) AttachFromPortal(
	ctx context.Context,
	caller *Principal,
	ticketID, articleID uint,
	file AttachedFile,
) (*model.Attachment, error) {
	ticket, contact, err := s.followed(ctx, caller, ticketID)
	if err != nil {
		return nil, err
	}

	if !contact.Valid {
		return nil, apperror.Conflictf("the contact %q is no longer offered", contact.Name).
			WithCode("contactRetired", contact.Name)
	}

	articles, err := s.tickets.Articles(ctx, ticket.ID)
	if err != nil {
		return nil, err
	}

	author := callerID(caller)

	if author == nil || !slices.ContainsFunc(articles, func(article *model.Article) bool {
		return article.ID == articleID && article.AuthorID != nil && *article.AuthorID == *author
	}) {
		return nil, apperror.NotFound("article", strconv.FormatUint(uint64(articleID), 10))
	}

	now := s.now().UTC()

	attachment, err := boundedAttachment(ticket.ID, articleID, file)
	if err != nil {
		return nil, err
	}

	change := historyByCustomer(ticket.ID, now, caller)
	change.add(model.HistoryAttachment, "", attachment.Filename)

	return s.tickets.AddAttachment(ctx, attachment, now, change.entries)
}

// contactOf is the contact the caller signs in to the portal as, nil for an
// account that signs in as none.
func (s *TicketApplicationService) contactOf(ctx context.Context, caller *Principal) (*model.Contact, error) {
	id := callerID(caller)
	if id == nil {
		return nil, nil
	}

	contact, err := s.contacts.GetByUserID(ctx, *id)
	if apperror.KindOf(err) == apperror.KindNotFound {
		return nil, nil
	}

	return contact, err
}

// portalContact is the contact the caller opens tickets as, refused where there
// is none, or where it is no longer offered: reading what it followed goes on,
// adding to it does not.
func (s *TicketApplicationService) portalContact(ctx context.Context, caller *Principal) (*model.Contact, error) {
	contact, err := s.contactOf(ctx, caller)
	if err != nil {
		return nil, err
	}

	if contact == nil {
		return nil, apperror.Conflictf("this account signs in to the portal as no contact").
			WithCode("portalNeedsAContact")
	}

	if !contact.Valid {
		return nil, apperror.Conflictf("the contact %q is no longer offered", contact.Name).
			WithCode("contactRetired", contact.Name)
	}

	return contact, nil
}

// followed reads a ticket the caller follows as a contact, with the contact,
// and answers "not found" for any other - as a ticket in a queue an agent does
// not reach is answered.
func (s *TicketApplicationService) followed(
	ctx context.Context,
	caller *Principal,
	id uint,
) (*model.Ticket, *model.Contact, error) {
	if id == 0 {
		return nil, nil, apperror.InvalidFields("id")
	}

	notFound := apperror.NotFound("ticket", strconv.FormatUint(uint64(id), 10))

	contact, err := s.contactOf(ctx, caller)
	if err != nil {
		return nil, nil, err
	}

	if contact == nil {
		return nil, nil, notFound
	}

	ticket, err := s.tickets.GetByID(ctx, id)
	if err != nil {
		return nil, nil, err
	}

	if !contact.Follows(ticket) {
		return nil, nil, notFound
	}

	return ticket, contact, nil
}

// asPortalTickets is the tickets as their customer is shown them.
func (s *TicketApplicationService) asPortalTickets(
	ctx context.Context,
	tickets []*model.Ticket,
	states []*model.TicketState,
) ([]PortalTicket, error) {
	contacts, _, err := s.CustomerNames(ctx, tickets)
	if err != nil {
		return nil, err
	}

	shown := make([]PortalTicket, 0, len(tickets))

	for _, ticket := range tickets {
		entry := PortalTicket{
			ID: ticket.ID, Number: ticket.Number, Title: ticket.Title,
			CreatedAt: ticket.CreatedAt, UpdatedAt: ticket.UpdatedAt, ClosedAt: ticket.ClosedAt,
		}

		for _, state := range states {
			if state.ID == ticket.StateID {
				entry.State, entry.StateType = state.Name, state.Type
			}
		}

		if ticket.ContactID != nil {
			entry.ContactName = contacts[*ticket.ContactID]
		}

		shown = append(shown, entry)
	}

	return shown, nil
}
