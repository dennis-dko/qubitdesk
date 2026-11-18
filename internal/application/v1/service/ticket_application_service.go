package service

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/dennis-dko/go-ticket-system/internal/application/v1/command"
	"github.com/dennis-dko/go-ticket-system/internal/domain/model"
	"github.com/dennis-dko/go-ticket-system/internal/domain/repository"
	"github.com/dennis-dko/go-ticket-system/internal/support/apperror"
)

// TicketService works the tickets.
type TicketService interface {
	ListTickets(ctx context.Context, caller *Principal, filter TicketListFilter) ([]*model.Ticket, uint, error)
	GetTicket(ctx context.Context, caller *Principal, id uint) (*model.Ticket, error)
	CreateTicket(ctx context.Context, caller *Principal, cmd command.CreateTicketCommand) (*model.Ticket, error)
	UpdateTicket(ctx context.Context, caller *Principal, id uint, cmd command.UpdateTicketCommand) (*model.Ticket, error)
	DeleteTicket(ctx context.Context, caller *Principal, id uint) error

	AddArticle(ctx context.Context, caller *Principal, id uint, cmd command.AddArticleCommand) (*model.Article, error)
	Articles(ctx context.Context, caller *Principal, id uint) ([]*model.Article, error)
	History(ctx context.Context, caller *Principal, id uint) ([]model.HistoryEntry, error)

	AddAttachment(ctx context.Context, caller *Principal, ticketID, articleID uint,
		file AttachedFile) (*model.Attachment, error)
	Attachments(ctx context.Context, caller *Principal, ticketID uint) ([]*model.Attachment, error)
	Attachment(ctx context.Context, caller *Principal, ticketID, attachmentID uint) (*model.Attachment, error)
	AccountedMinutes(ctx context.Context, caller *Principal, id uint) (int, error)

	SetWatching(ctx context.Context, caller *Principal, id uint, watching bool) error
	Watching(ctx context.Context, caller *Principal, id uint) (bool, error)

	// Names are the names of the accounts with these ids, for a reader who may
	// not list the accounts and still has to see who owns a ticket. An account
	// that is gone has none.
	Names(ctx context.Context, ids []uint) (map[uint]string, error)

	// Assignees are the accounts a ticket in the queue can be given to: those
	// on the staff side whose role works tickets there.
	Assignees(ctx context.Context, caller *Principal, queueID uint) ([]*model.User, error)

	// CustomerNames are the names of the contacts and the organisations the
	// tickets name, by id.
	CustomerNames(ctx context.Context, tickets []*model.Ticket) (map[uint]string, map[uint]string, error)
}

// TicketListFilter is what a listing asks for: a page, and what narrows it.
type TicketListFilter struct {
	QueueID  uint
	StateIDs []uint

	// Unfinished narrows the listing to tickets somebody still has to do
	// something about.
	Unfinished bool

	OwnerID        uint
	ContactID      uint
	OrganisationID uint
	Search         string
	Limit          int
	Page           int
}

// DefaultTicketPage and MaxTicketPage bound a page of tickets. A limit above the
// maximum is refused rather than quietly lowered: a list that does not match what
// was asked for is worse than a plain no.
const (
	DefaultTicketPage = 50
	MaxTicketPage     = 200
)

// TicketApplicationService is the TicketService the application runs with.
//
// A ticket is reached through its queue: reading it takes the caller's role
// reaching the queue at all, and creating, changing and deleting it take the role
// working in it - or queues:write, which reaches every queue. A ticket in a queue
// the caller does not reach is not found rather than refused, as the queue itself
// is not. The right to do the thing at all - tickets:read, tickets:create,
// tickets:write, tickets:delete - is asked before any ticket is read, by the
// handler, and is refused as itself: it says something about the caller and
// nothing about the ticket.
type TicketApplicationService struct {
	tickets    repository.TicketRepository
	queues     repository.QueueRepository
	states     repository.TicketStateRepository
	priorities repository.TicketPriorityRepository
	types      repository.TicketTypeRepository
	users      repository.UserRepository
	roles      repository.RoleRepository

	contacts      repository.ContactRepository
	organisations repository.OrganisationRepository

	// zone names the installation's zone, which a ticket's number is dated in.
	zone func(ctx context.Context) string
	now  func() time.Time

	// config is the system configuration, read where a rule can be switched
	// off; nil keeps every option at its default.
	config ConfigReader

	// levels, calendars and holidays decide what a ticket is owed; without
	// them a ticket is opened owed nothing - see WithServiceLevels.
	levels    repository.ServiceLevelRepository
	calendars repository.CalendarRepository
	holidays  func(ctx context.Context) string

	// unlockTypes are the state types a queue's unlock time frees a lock in.
	unlockTypes repository.UnlockStateTypeRepository

	// raised hears what a person's change did to a ticket, for the jobs whose
	// event-based plans wait for it; nil hears nothing - see WithEvents.
	raised func(ctx context.Context, ticketID uint, events []model.TicketEvent)

	// told hears what any change did to a ticket and who made it, for the
	// notifications; nil tells nobody - see WithNotifications.
	told func(ctx context.Context, ticketID uint, events []model.TicketEvent, by uint)

	metrics
}

// NewTicketApplicationService wires the ticket service. zone answers the
// installation's zone per request, so changing it takes effect at once.
func NewTicketApplicationService(
	tickets repository.TicketRepository,
	queues repository.QueueRepository,
	catalog TicketCatalogRepositories,
	customers TicketCustomerRepositories,
	users repository.UserRepository,
	roles repository.RoleRepository,
	zone func(ctx context.Context) string,
) *TicketApplicationService {
	return &TicketApplicationService{
		tickets: tickets, queues: queues,
		states: catalog.States, priorities: catalog.Priorities, types: catalog.Types, unlockTypes: catalog.UnlockTypes,
		contacts: customers.Contacts, organisations: customers.Organisations,
		users: users, roles: roles, zone: zone, now: time.Now,
	}
}

// TicketCatalogRepositories are the tables tickets are sorted by, handed over
// together because a ticket is sorted by all of them at once - and the state
// types a lock runs out in, which belong with the states.
type TicketCatalogRepositories struct {
	States      repository.TicketStateRepository
	Priorities  repository.TicketPriorityRepository
	Types       repository.TicketTypeRepository
	UnlockTypes repository.UnlockStateTypeRepository
}

var _ TicketService = (*TicketApplicationService)(nil)

// ConfigReader reads the options of the system configuration a rule obeys.
type ConfigReader interface {
	Bool(ctx context.Context, name string) (bool, error)
	String(ctx context.Context, name string) (string, error)
}

// WithEvents has the events a person's change raised told to raised, after the
// change is written. The application's own changes - the automation's - raise
// none, so that a job cannot start itself, or two jobs one another, for ever.
func (s *TicketApplicationService) WithEvents(
	raised func(ctx context.Context, ticketID uint, events []model.TicketEvent),
) *TicketApplicationService {
	s.raised = raised

	return s
}

// WithNotifications attaches what sends the notifications, told of every
// change's events and of the account that made it - nought for the application,
// whose changes are news to the agents around a ticket as much as a person's -
// once the change is written. Optional: without it nobody is told anything.
func (s *TicketApplicationService) WithNotifications(
	told func(ctx context.Context, ticketID uint, events []model.TicketEvent, by uint),
) *TicketApplicationService {
	s.told = told

	return s
}

// raise tells the events a change raised to whoever listens: the notifications
// whoever made it, and the jobs where a person made it.
func (s *TicketApplicationService) raise(ctx context.Context, caller *Principal, ticketID uint, events []model.TicketEvent) {
	if len(events) == 0 {
		return
	}

	if s.told != nil {
		var by uint
		if id := callerID(caller); id != nil {
			by = *id
		}

		s.told(ctx, ticketID, events, by)
	}

	if s.raised == nil || caller == nil || caller.System {
		return
	}

	s.raised(ctx, ticketID, events)
}

// WithClock reads the time from now instead of the wall.
func (s *TicketApplicationService) WithClock(now func() time.Time) *TicketApplicationService {
	s.now = now

	return s
}

// WithConfig attaches the system configuration. Optional: without it every
// option the service obeys keeps its default.
func (s *TicketApplicationService) WithConfig(config ConfigReader) *TicketApplicationService {
	s.config = config

	return s
}

// WithMetrics attaches the recorder. Optional: without it tickets are opened and
// nothing is counted.
func (s *TicketApplicationService) WithMetrics(recorder Recorder) *TicketApplicationService {
	s.recorder = recorder

	return s
}

func (s *TicketApplicationService) ListTickets(
	ctx context.Context,
	caller *Principal,
	filter TicketListFilter,
) ([]*model.Ticket, uint, error) {
	if filter.Limit == 0 {
		filter.Limit = DefaultTicketPage
	}

	if filter.Limit < 0 || filter.Limit > MaxTicketPage {
		return nil, 0, apperror.InvalidFields("limit")
	}

	if filter.Page < 0 {
		return nil, 0, apperror.InvalidFields("page")
	}

	queues, err := s.queues.GetAll(ctx)
	if err != nil {
		return nil, 0, err
	}

	var reached []uint

	for _, queue := range queues {
		if _, ok := Reach(caller, queue); ok && (filter.QueueID == 0 || filter.QueueID == queue.ID) {
			reached = append(reached, queue.ID)
		}
	}

	states := filter.StateIDs

	if filter.Unfinished {
		all, err := s.states.GetAll(ctx)
		if err != nil {
			return nil, 0, err
		}

		states = nil

		for _, state := range all {
			if !slices.Contains(model.FinishedStateTypes(), state.Type) &&
				(len(filter.StateIDs) == 0 || slices.Contains(filter.StateIDs, state.ID)) {
				states = append(states, state.ID)
			}
		}

		if len(states) == 0 {
			return []*model.Ticket{}, 0, nil
		}
	}

	return s.tickets.List(ctx, repository.TicketFilter{
		QueueIDs: reached, StateIDs: states, OwnerID: given(filter.OwnerID), ContactID: given(filter.ContactID),
		OrganisationID: given(filter.OrganisationID), Search: filter.Search, Limit: filter.Limit, Page: filter.Page,
	})
}

func (s *TicketApplicationService) GetTicket(ctx context.Context, caller *Principal, id uint) (*model.Ticket, error) {
	ticket, _, err := s.reached(ctx, caller, id, false)

	return ticket, err
}

func (s *TicketApplicationService) CreateTicket(
	ctx context.Context,
	caller *Principal,
	cmd command.CreateTicketCommand,
) (*model.Ticket, error) {
	var invalid []string

	title := strings.TrimSpace(valueOf(cmd.Title))
	if title == "" || model.TooLong(title, model.MaxTicketTitleLength) {
		invalid = append(invalid, "title")
	}

	if cmd.QueueID == nil {
		invalid = append(invalid, "queueId")
	}

	article, minutes, articleInvalid := s.article(cmd.Channel, cmd.CustomerVisible, cmd.Subject, cmd.Body, cmd.Minutes)
	invalid = append(invalid, articleInvalid...)

	if len(invalid) > 0 {
		return nil, apperror.InvalidFields(invalid...)
	}

	queue, err := s.workable(ctx, caller, *cmd.QueueID)
	if err != nil {
		return nil, err
	}

	now := s.now().UTC()
	ticket := &model.Ticket{
		Title: title, QueueID: queue.ID, Channel: article.Channel,
		CreatedBy: callerID(caller), CreatedAt: now, UpdatedAt: now,
	}

	state, err := s.chosenState(ctx, cmd.StateID, model.StateNew)
	if err != nil {
		return nil, err
	}

	if err := s.placeInState(ticket, state, cmd.PendingUntil, now); err != nil {
		return nil, err
	}

	if ticket.PriorityID, err = s.chosenPriority(ctx, cmd.PriorityID); err != nil {
		return nil, err
	}

	if ticket.TypeID, err = s.chosenType(ctx, cmd.TypeID); err != nil {
		return nil, err
	}

	if ticket.OwnerID, err = s.staff(ctx, cmd.OwnerID); errors.Is(err, errNotStaff) {
		return nil, apperror.InvalidFields("ownerId")
	} else if err != nil {
		return nil, err
	}

	if ticket.ResponsibleID, err = s.staff(ctx, cmd.ResponsibleID); errors.Is(err, errNotStaff) {
		return nil, apperror.InvalidFields("responsibleId")
	} else if err != nil {
		return nil, err
	}

	if err := s.chooseCustomer(ctx, ticket, customerChoice{
		contactID: cmd.ContactID, organisationID: cmd.OrganisationID,
	}, nil); err != nil {
		return nil, err
	}

	if err := s.openOwed(ctx, ticket, cmd.ServiceLevelID, queue); err != nil {
		return nil, err
	}

	article.AuthorID = callerID(caller)
	article.CreatedAt = now

	if ticket.UpdateDue, err = s.updateOwed(ctx, ticket, []*model.Article{article}, state.Type); err != nil {
		return nil, err
	}

	opened, err := s.tickets.Create(ctx, ticket, article, minutes, s.numbered(ctx, now))
	if err != nil {
		return nil, err
	}

	s.count(ctx, MetricTicketsOpened, "channel", string(opened.Channel))
	s.raise(ctx, caller, opened.ID, []model.TicketEvent{model.EventTicketCreate, model.EventArticleCreate})

	return opened, nil
}

func (s *TicketApplicationService) UpdateTicket(
	ctx context.Context,
	caller *Principal,
	id uint,
	cmd command.UpdateTicketCommand,
) (*model.Ticket, error) {
	ticket, queue, err := s.reached(ctx, caller, id, true)
	if err != nil {
		return nil, err
	}

	var invalid []string

	title := strings.TrimSpace(valueOf(cmd.Title))
	if cmd.Title != nil && (title == "" || model.TooLong(title, model.MaxTicketTitleLength)) {
		invalid = append(invalid, "title")
	}

	if cmd.ClearOwner && cmd.OwnerID != nil {
		invalid = append(invalid, "ownerId")
	}

	if cmd.ClearResponsible && cmd.ResponsibleID != nil {
		invalid = append(invalid, "responsibleId")
	}

	if len(invalid) > 0 {
		return nil, apperror.InvalidFields(invalid...)
	}

	now := s.now().UTC()
	change := historyOf(ticket.ID, now, caller)

	if cmd.Title != nil {
		change.add(model.HistoryTitle, ticket.Title, title)
		ticket.Title = title
	}

	moved := cmd.QueueID != nil && *cmd.QueueID != ticket.QueueID
	if moved {
		target, err := s.workable(ctx, caller, *cmd.QueueID)
		if err != nil {
			return nil, err
		}

		change.add(model.HistoryQueue, queue.Name, target.Name)
		ticket.QueueID = target.ID
	}

	if err := s.changeCatalog(ctx, ticket, cmd, change, now); err != nil {
		return nil, err
	}

	if err := s.changePeople(ctx, caller, ticket, cmd, change, now); err != nil {
		return nil, err
	}

	// After the people, so that a request moving the ticket and asking for the
	// lock keeps the lock it asked for.
	if moved && ticket.Locked && cmd.Locked == nil {
		if err := s.releaseOnMove(ctx, ticket, change); err != nil {
			return nil, err
		}
	}

	if err := s.chooseCustomer(ctx, ticket, customerChoice{
		contactID: cmd.ContactID, clearContact: cmd.ClearContact,
		organisationID: cmd.OrganisationID, clearOrganisation: cmd.ClearOrganisation,
	}, change); err != nil {
		return nil, err
	}

	if err := s.changeOwed(ctx, ticket, cmd.ServiceLevelID, cmd.ClearServiceLevel, change); err != nil {
		return nil, err
	}

	if len(change.entries) == 0 {
		return ticket, nil
	}

	// After the state and the level, either of which decides it: a ticket put
	// to wait or finished owes no answer, and one opened again owes one.
	if ticket.UpdateDue, err = s.updateOwedNow(ctx, ticket); err != nil {
		return nil, err
	}

	ticket.UpdatedAt = now

	changed, err := s.tickets.Update(ctx, ticket, change.entries)
	if err != nil {
		return nil, err
	}

	s.raise(ctx, caller, changed.ID, model.EventsOf(change.entries))

	return changed, nil
}

// releaseOnMove takes a moved ticket's lock off, as KIX18 does unless the
// installation switched it off (model.ConfigTicketUnlockOnMove): the lock says
// somebody in the old queue is on the ticket, and the agents of the queue it
// arrives in could otherwise not take it up.
func (s *TicketApplicationService) releaseOnMove(ctx context.Context, ticket *model.Ticket, change *history) error {
	releases, err := s.switchOn(ctx, model.ConfigTicketUnlockOnMove)
	if err != nil || !releases {
		return err
	}

	change.add(model.HistoryLock, strconv.FormatBool(true), strconv.FormatBool(false))
	ticket.Locked = false
	ticket.LockedAt = nil

	return nil
}

// switchOn reads a switch of the system configuration, or its default where the
// service was given none.
func (s *TicketApplicationService) switchOn(ctx context.Context, name string) (bool, error) {
	if s.config == nil {
		option, _ := model.ConfigOptionNamed(name)

		return option.Default == "true", nil
	}

	return s.config.Bool(ctx, name)
}

func (s *TicketApplicationService) DeleteTicket(ctx context.Context, caller *Principal, id uint) error {
	if _, _, err := s.reached(ctx, caller, id, true); err != nil {
		return err
	}

	return s.tickets.Delete(ctx, id)
}

func (s *TicketApplicationService) AddArticle(
	ctx context.Context,
	caller *Principal,
	id uint,
	cmd command.AddArticleCommand,
) (*model.Article, error) {
	ticket, _, err := s.reached(ctx, caller, id, true)
	if err != nil {
		return nil, err
	}

	article, minutes, invalid := s.article(cmd.Channel, cmd.CustomerVisible, cmd.Subject, cmd.Body, cmd.Minutes)
	if len(invalid) > 0 {
		return nil, apperror.InvalidFields(invalid...)
	}

	now := s.now().UTC()
	article.TicketID = ticket.ID
	article.AuthorID = callerID(caller)
	article.CreatedAt = now

	if caller.System {
		article.Sender = model.SenderSystem
	}

	change := historyOf(ticket.ID, now, caller)
	change.add(model.HistoryArticle, "", string(article.Channel))

	if minutes > 0 {
		change.add(model.HistoryTime, "", strconv.Itoa(minutes))
	}

	// The first article of an agent the customer sees answers the ticket, and
	// every one starts the time its lock runs for again, as KIX18 counts it.
	var answered *time.Time
	if article.CustomerVisible && ticket.FirstResponseAt == nil {
		answered = &now
	}

	updateDue, err := s.updateOwedNow(ctx, ticket, article)
	if err != nil {
		return nil, err
	}

	written, err := s.tickets.AddArticle(ctx, article, minutes, change.entries, answered, article.CustomerVisible,
		updateDue)
	if err != nil {
		return nil, err
	}

	s.raise(ctx, caller, ticket.ID, model.EventsOf(change.entries))

	return written, nil
}

// reached reads a ticket and its queue for a caller, and answers "not found"
// where the caller's role does not reach the queue - or, when work is asked for,
// does not work in it - exactly as for a ticket that is not there.
func (s *TicketApplicationService) reached(
	ctx context.Context,
	caller *Principal,
	id uint,
	work bool,
) (*model.Ticket, *model.Queue, error) {
	if id == 0 {
		return nil, nil, apperror.InvalidFields("id")
	}

	ticket, err := s.tickets.GetByID(ctx, id)
	if err != nil {
		return nil, nil, err
	}

	queue, err := s.queues.GetByID(ctx, ticket.QueueID)
	if err != nil {
		return nil, nil, err
	}

	level, ok := Reach(caller, queue)
	if !ok || (work && level != model.AccessWrite) {
		return nil, nil, apperror.NotFound("ticket", strconv.FormatUint(uint64(id), 10))
	}

	return ticket, queue, nil
}
