package service

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/dennis-dko/go-ticket-system/internal/domain/model"
	"github.com/dennis-dko/go-ticket-system/internal/domain/repository"
	"github.com/dennis-dko/go-ticket-system/internal/support/apperror"
	"github.com/dennis-dko/go-ticket-system/internal/support/i18n"
	"github.com/dennis-dko/go-ticket-system/internal/support/webpush"
)

// NotificationService sends the notifications: when something happens to a
// ticket it works out, for every notification set up for that event and that
// ticket, who is told - as KIX18's notification event works it out - and keeps
// for each of them what the notification says, in their language.
//
// It is told of every change, the application's own included, because a ticket
// a job moved on is news to the agents around it as much as one a person moved;
// the automation is told of a person's changes only, for the reason
// TicketApplicationService.WithEvents gives.
type NotificationService struct {
	definitions repository.NotificationDefinitionRepository
	sent        repository.NotificationRepository
	tickets     repository.TicketRepository
	roles       repository.RoleRepository
	refs        MacroReferences
	zone        func(ctx context.Context) string
	now         func() time.Time
	push        Pusher
}

// Pusher pushes messages to the browsers an account subscribed, where the
// installation pushes at all; PushService is the one there is.
type Pusher interface {
	Push(ctx context.Context, userID uint, messages ...webpush.Message) error
}

// pushedAtOnce bounds how many notifications one look sends, the rest waiting
// for the next. Chosen rather than derived: a minute's worth on a busy desk.
const pushedAtOnce = 500

// notificationTag starts the tag a pushed notification carries, which the
// service worker reads to know whom to ask whether it still holds.
const notificationTag = "gts-notification-"

// NotificationSources are what a notification is worked out from: the
// notifications set up, the ones sent, the ticket and the roles that decide who
// reaches it, and the records its placeholders name.
type NotificationSources struct {
	Definitions repository.NotificationDefinitionRepository
	Sent        repository.NotificationRepository
	Tickets     repository.TicketRepository
	Roles       repository.RoleRepository
	Refs        MacroReferences
}

// NewNotificationService counts a day, for a notification sent once a day, in
// the zone zone names.
func NewNotificationService(sources NotificationSources, zone func(ctx context.Context) string) *NotificationService {
	return &NotificationService{
		definitions: sources.Definitions, sent: sources.Sent, tickets: sources.Tickets, roles: sources.Roles,
		refs: sources.Refs, zone: zone, now: time.Now,
	}
}

// WithPush pushes what the agents are told to the browsers they subscribed.
// Optional: without it a notification is kept for the screen and pushed
// nowhere.
func (s *NotificationService) WithPush(push Pusher) *NotificationService {
	s.push = push

	return s
}

// SendUnsent pushes the notifications that have not left yet, each as a
// message of its own, and says how many it sent. Each leaves once, pushed or
// not: a push is a courtesy beside the screen, and one that failed is not tried
// again a minute later about a moment that has passed. What failed is said.
func (s *NotificationService) SendUnsent(ctx context.Context) (int, error) {
	unsent, err := s.sent.Unsent(ctx, pushedAtOnce)
	if err != nil || len(unsent) == 0 {
		return 0, err
	}

	byUser := map[uint][]webpush.Message{}
	order := make([]uint, 0)
	ids := make([]uint, 0, len(unsent))

	for _, n := range unsent {
		if _, seen := byUser[n.UserID]; !seen {
			order = append(order, n.UserID)
		}

		byUser[n.UserID] = append(byUser[n.UserID], webpush.Message{
			Title: n.Subject, Body: n.Body, Tag: notificationTag + strconv.FormatUint(uint64(n.ID), 10),
			At: n.CreatedAt.UTC().Format(time.RFC3339), About: strconv.FormatUint(uint64(n.ID), 10),
		})
		ids = append(ids, n.ID)
	}

	var failed []error

	if s.push != nil {
		for _, userID := range order {
			if err := s.push.Push(ctx, userID, byUser[userID]...); err != nil {
				failed = append(failed, err)
			}
		}
	}

	if err := s.sent.MarkSent(ctx, ids); err != nil {
		return 0, err
	}

	return len(ids), errors.Join(failed...)
}

// Get is one of the caller's notifications; somebody else's is not found. The
// service worker asks for one this way to know whether a push that arrived late
// still holds.
func (s *NotificationService) Get(ctx context.Context, caller *Principal, id uint) (*model.Notification, error) {
	if caller == nil || caller.User == nil {
		return nil, apperror.NotFound("notification", strconv.FormatUint(uint64(id), 10))
	}

	return s.sent.Get(ctx, caller.User.ID, id)
}

// WithClock reads the time from now instead of the wall.
func (s *NotificationService) WithClock(now func() time.Time) *NotificationService {
	s.now = now

	return s
}

// OnTicketEvents sends what the events a change raised on a ticket call for. by
// is the account that made the change, which is not told about its own doing,
// or nought for the application.
func (s *NotificationService) OnTicketEvents(ctx context.Context, ticketID uint, events []model.TicketEvent, by uint) error {
	definitions, err := s.definitions.GetAll(ctx)
	if err != nil {
		return err
	}

	definitions = slices.DeleteFunc(definitions, func(d *model.NotificationDefinition) bool {
		return !d.Valid || !slices.ContainsFunc(events, func(event model.TicketEvent) bool {
			return slices.Contains(d.Events, event)
		})
	})
	if len(definitions) == 0 {
		return nil
	}

	ticket, err := s.tickets.GetByID(ctx, ticketID)
	if err != nil {
		return err
	}

	around, err := s.around(ctx, ticket)
	if err != nil {
		return err
	}

	now := s.now().UTC()
	notifications := make([]model.Notification, 0)

	for _, definition := range definitions {
		if !definition.Criteria.Matches(ticket) {
			continue
		}

		told, err := s.toldBy(ctx, definition, ticket, around, by, now)
		if err != nil {
			return err
		}

		for _, user := range told {
			message := definition.MessageFor(user.Language)
			words := around.words(user.Language)

			notifications = append(notifications, model.Notification{
				UserID: user.ID, TicketID: ticket.ID, DefinitionID: definition.ID,
				Subject: shortened(fillTags(message.Subject, words), model.MaxTicketTitleLength),
				Body:    fillTags(message.Body, words), CreatedAt: now,
			})
		}
	}

	return s.sent.Add(ctx, notifications)
}

// Mine is the caller's latest notifications, the latest first, and how many of
// all of theirs they have not read. A request with no account behind it - where
// authentication is off - has been told nothing.
func (s *NotificationService) Mine(ctx context.Context, caller *Principal) ([]model.Notification, int, error) {
	if caller == nil || caller.User == nil {
		return []model.Notification{}, 0, nil
	}

	return s.sent.ForUser(ctx, caller.User.ID, model.MaxNotificationsListed)
}

// MarkRead marks one of the caller's notifications read. Somebody else's is
// not found, as any record the caller may not see.
func (s *NotificationService) MarkRead(ctx context.Context, caller *Principal, id uint) error {
	theirs := false

	if caller != nil && caller.User != nil {
		var err error
		if theirs, err = s.sent.MarkRead(ctx, caller.User.ID, id, s.now().UTC()); err != nil {
			return err
		}
	}

	if !theirs {
		return apperror.NotFound("notification", strconv.FormatUint(uint64(id), 10))
	}

	return nil
}

// MarkAllRead marks every notification of the caller's read.
func (s *NotificationService) MarkAllRead(ctx context.Context, caller *Principal) error {
	if caller == nil || caller.User == nil {
		return nil
	}

	return s.sent.MarkAllRead(ctx, caller.User.ID, s.now().UTC())
}

// toldBy is who a notification tells about a ticket: the recipients it names,
// each once, less the account that made the change, the built-in administrator,
// whoever cannot read the ticket, and - for one sent once a day - whoever it has
// told about the ticket today, as KIX18 leaves each of them out.
func (s *NotificationService) toldBy(
	ctx context.Context,
	definition *model.NotificationDefinition,
	ticket *model.Ticket,
	around ticketSurroundings,
	by uint,
	now time.Time,
) ([]*model.User, error) {
	named := map[uint]bool{}

	for _, recipient := range definition.Recipients {
		for _, id := range around.recipients[recipient] {
			named[id] = true
		}
	}

	told := make([]*model.User, 0, len(named))

	for _, user := range around.users {
		if !named[user.ID] || user.ID == by || user.IsSystem || !around.reads[user.ID] {
			continue
		}

		if definition.OncePerDay {
			already, err := s.sent.SentSince(ctx, definition.ID, ticket.ID, user.ID, s.dayBegun(ctx, now))
			if err != nil {
				return nil, err
			}

			if already {
				continue
			}
		}

		told = append(told, user)
	}

	return told, nil
}

// dayBegun is the midnight the day of now began at, in the installation's zone.
func (s *NotificationService) dayBegun(ctx context.Context, now time.Time) time.Time {
	zone, err := time.LoadLocation(s.zone(ctx))
	if err != nil {
		zone = time.UTC
	}

	wall := now.In(zone)

	return time.Date(wall.Year(), wall.Month(), wall.Day(), 0, 0, 0, 0, zone)
}

// ticketSurroundings is everything a ticket's notifications are worked out
// from, read once for all of them: every account, who of them each recipient
// names, who may read the ticket, and the records its placeholders name.
type ticketSurroundings struct {
	ticket     *model.Ticket
	users      []*model.User
	recipients map[model.NotificationRecipient][]uint
	reads      map[uint]bool
	queue      string
	state      string
	priority   string
	kind       string
	contact    string
	org        string
}

// around reads a ticket's surroundings.
func (s *NotificationService) around(ctx context.Context, ticket *model.Ticket) (ticketSurroundings, error) {
	around := ticketSurroundings{
		ticket: ticket, recipients: map[model.NotificationRecipient][]uint{}, reads: map[uint]bool{},
	}

	queue, err := s.refs.Queues.GetByID(ctx, ticket.QueueID)
	if err != nil {
		return around, err
	}

	around.queue = queue.Name

	roles, err := s.roles.GetAll(ctx)
	if err != nil {
		return around, err
	}

	if around.users, err = s.refs.Users.GetAll(ctx); err != nil {
		return around, err
	}

	for _, user := range around.users {
		at := slices.IndexFunc(roles, func(role *model.Role) bool { return role.ID == user.RoleID })
		if at < 0 {
			continue
		}

		role := roles[at]
		level, reached := Reach(&Principal{User: user, Role: role, Permissions: role.Permissions}, queue)
		around.reads[user.ID] = reached && role.Has(model.PermTicketRead)

		if around.reads[user.ID] {
			around.recipients[model.RecipientReaders] = append(around.recipients[model.RecipientReaders], user.ID)
		}

		if around.reads[user.ID] && level == model.AccessWrite && role.Has(model.PermTicketWrite) {
			around.recipients[model.RecipientWriters] = append(around.recipients[model.RecipientWriters], user.ID)
		}
	}

	if ticket.OwnerID != nil {
		around.recipients[model.RecipientOwner] = []uint{*ticket.OwnerID}
	}

	if ticket.ResponsibleID != nil {
		around.recipients[model.RecipientResponsible] = []uint{*ticket.ResponsibleID}
	}

	if around.recipients[model.RecipientWatcher], err = s.tickets.Watchers(ctx, ticket.ID); err != nil {
		return around, err
	}

	if around.recipients[model.RecipientMyQueues], err = s.refs.Queues.Followers(ctx, ticket.QueueID); err != nil {
		return around, err
	}

	return around, s.names(ctx, &around)
}

// names reads the names a ticket's placeholders write.
func (s *NotificationService) names(ctx context.Context, around *ticketSurroundings) error {
	ticket := around.ticket

	state, err := s.refs.Catalog.States.GetByID(ctx, ticket.StateID)
	if err != nil {
		return err
	}

	priority, err := s.refs.Catalog.Priorities.GetByID(ctx, ticket.PriorityID)
	if err != nil {
		return err
	}

	kind, err := s.refs.Catalog.Types.GetByID(ctx, ticket.TypeID)
	if err != nil {
		return err
	}

	around.state, around.priority, around.kind = state.Name, priority.Name, kind.Name

	if ticket.ContactID != nil {
		contact, err := s.refs.Customers.Contacts.GetByID(ctx, *ticket.ContactID)
		if err != nil && apperror.KindOf(err) != apperror.KindNotFound {
			return err
		}

		if contact != nil {
			around.contact = contact.Name
		}
	}

	if ticket.OrganisationID != nil {
		org, err := s.refs.Customers.Organisations.GetByID(ctx, *ticket.OrganisationID)
		if err != nil && apperror.KindOf(err) != apperror.KindNotFound {
			return err
		}

		if org != nil {
			around.org = org.Name
		}
	}

	return nil
}

// words are what a ticket's placeholders write for a reader of language: a
// state, a priority and a type in the words the screen has for them, a shipped
// one translated as the page translates it, and a person by their name.
func (a ticketSurroundings) words(language string) map[string]string {
	person := func(id *uint) string {
		if id == nil {
			return ""
		}

		if at := slices.IndexFunc(a.users, func(user *model.User) bool { return user.ID == *id }); at >= 0 {
			return a.users[at].Name
		}

		return ""
	}

	return map[string]string{
		"id": strconv.FormatUint(uint64(a.ticket.ID), 10), "number": a.ticket.Number,
		"ticketnumber": a.ticket.Number, "title": a.ticket.Title, "queue": a.queue,
		"state": catalogWord(language, "status.", a.state), "priority": catalogWord(language, "priority.", a.priority),
		"type": catalogWord(language, "tickettype.", a.kind), "owner": person(a.ticket.OwnerID),
		"responsible": person(a.ticket.ResponsibleID), "contact": a.contact, "organisation": a.org,
	}
}

// catalogWord is a state's, a priority's or a type's name in a reader's
// language: the dictionary's word for a value that ships, and otherwise the
// name as it was given. A value that ships is stored under an identifier whose
// hyphens the English writes as spaces - "pending-reminder" is "pending
// reminder" - and English has no dictionary, so that is its word there.
func catalogWord(language, prefix, name string) string {
	if word, found := i18n.Dictionary(language)[prefix+name]; found {
		return word
	}

	if _, ships := i18n.Dictionary(model.LanguageGerman)[prefix+name]; ships {
		return strings.ReplaceAll(name, "-", " ")
	}

	return name
}

// shortened is a text cut to the characters a column holds, counted as
// model.TooLong counts them: a subject a long title was put into still fits.
func shortened(text string, limit int) string {
	runes := []rune(strings.TrimSpace(text))
	if len(runes) <= limit {
		return string(runes)
	}

	return string(runes[:limit])
}

// kixTag is one of KIX18's ticket placeholders, <KIX_TICKET_Title> and the like,
// written in any case, and as an escaped one where a text came from a page.
var kixTag = regexp.MustCompile(`(?i)(?:<|&lt;)KIX_TICKET_([A-Z]+)(?:>|&gt;)`)

// fillTags puts the ticket's words in for its placeholders. One that names
// nothing this ticket has is written as a dash, as KIX18 writes it.
func fillTags(text string, words map[string]string) string {
	return kixTag.ReplaceAllStringFunc(text, func(tag string) string {
		word, known := words[strings.ToLower(kixTag.FindStringSubmatch(tag)[1])]
		if !known || word == "" {
			return "-"
		}

		return word
	})
}
