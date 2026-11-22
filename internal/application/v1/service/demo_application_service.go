package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/dennis-dko/qubitdesk/internal/application/v1/command"
	"github.com/dennis-dko/qubitdesk/internal/domain/model"
	"github.com/dennis-dko/qubitdesk/internal/domain/repository"
	"github.com/dennis-dko/qubitdesk/internal/support/i18n"
)

// DemoService fills a new installation with demo data, where whoever answered
// the installer asked for it, so that every screen has something to show from
// the first sign-in: customers and their tickets, conversations with time noted
// on them, a service level some of them have already missed, a knowledge base
// and answer templates.
//
// Everything goes through the services the interface uses, so the demo obeys
// every rule somebody's own work does and cannot make a state the application
// could not have made itself. In the built-in administrator's name, because that
// is the account the browser which answered the installer is signed into, and as
// shipped it may do everything on the staff side, tickets included.
//
// No second account is made for it, because an account with a password somebody
// could read would outlive the demo. That also decides whose words the tickets
// hold: what an agent records of a conversation - a call taken down, a call
// returned, an answer the customer reads in the portal, a note for colleagues.
// A customer's own words come through the portal or by mail, and both need the
// customer to have an account.
//
// The tickets are opened over the last five days rather than in the second the
// demo runs, because a list of tickets all opened just now shows neither the
// order a help desk works in nor a promise it has missed. Each moment is written
// through a copy of the ticket service reading the demo's clock, and every one
// of them has passed: nothing is dated in the future but the moment a waiting
// ticket waits for.
type DemoService struct {
	users     repository.UserRepository
	auth      *AuthService
	tickets   *TicketApplicationService
	catalog   TicketCatalogRepositories
	queues    *QueueApplicationService
	levels    *SLAApplicationService
	customers *CustomerApplicationService
	faq       *FAQApplicationService
	templates *ResponseTemplateApplicationService
	inventory *demoInventory
	changes   *demoChangeServices
	now       func() time.Time
}

// NewDemoService is handed the services the interface works through, so the
// demo is held to what they hold anybody to.
func NewDemoService(
	users repository.UserRepository,
	auth *AuthService,
	tickets *TicketApplicationService,
	catalog TicketCatalogRepositories,
	queues *QueueApplicationService,
	levels *SLAApplicationService,
	customers *CustomerApplicationService,
	faq *FAQApplicationService,
	templates *ResponseTemplateApplicationService,
) *DemoService {
	return &DemoService{
		users: users, auth: auth, tickets: tickets, catalog: catalog, queues: queues, levels: levels,
		customers: customers, faq: faq, templates: templates, now: time.Now,
	}
}

// WithInventory attaches what the demo's configuration items are written
// through and linked with. Optional: without it the demo adds none.
func (s *DemoService) WithInventory(
	items *ConfigItemApplicationService,
	catalog repository.GeneralCatalogRepository,
	links *LinkApplicationService,
) *DemoService {
	s.inventory = &demoInventory{items: items, catalog: catalog, links: links}

	return s
}

// WithChanges attaches what the demo's changes and their work orders are
// written through and linked with. Optional: without it the demo adds none.
func (s *DemoService) WithChanges(
	changes *ChangeApplicationService,
	workOrders *WorkOrderApplicationService,
	catalog repository.GeneralCatalogRepository,
	links *LinkApplicationService,
) *DemoService {
	s.changes = &demoChangeServices{changes: changes, workOrders: workOrders, catalog: catalog, links: links}

	return s
}

// WithClock reads the time from now instead of the wall.
func (s *DemoService) WithClock(now func() time.Time) *DemoService {
	s.now = now

	return s
}

// DemoSummary is what a seed added, for the log.
type DemoSummary struct {
	Organisations, Contacts, Tickets, Articles, FAQArticles, Templates, ConfigItems, Changes, WorkOrders int

	// Skipped says why nothing was added, and is empty when the demo went in.
	Skipped string
}

// Seed fills the database with demo data written in language, and says what it
// added.
//
// Only into a database that holds no tickets: one that does is being worked in,
// and demo tickets among somebody's real ones could never be told apart again.
func (s *DemoService) Seed(ctx context.Context, language string) (DemoSummary, error) {
	admin, err := s.users.GetByEmail(ctx, SystemUserEmail)
	if err != nil {
		return DemoSummary{}, fmt.Errorf("finding the built-in administrator: %w", err)
	}

	caller, err := s.auth.PrincipalByID(ctx, admin.ID)
	if err != nil {
		return DemoSummary{}, fmt.Errorf("reading what the built-in administrator may do: %w", err)
	}

	_, held, err := s.tickets.ListTickets(ctx, caller, TicketListFilter{Limit: 1})
	if err != nil {
		return DemoSummary{}, fmt.Errorf("asking whether the database holds tickets: %w", err)
	}

	if held > 0 {
		return DemoSummary{Skipped: "the database already holds tickets"}, nil
	}

	t := i18n.For(language)

	var summary DemoSummary

	desk, err := s.desk(ctx, caller)
	if err != nil {
		return summary, err
	}

	level, err := s.levels.Create(ctx, command.ServiceLevelCommand{
		Name: ptr(demoLevel.name.in(t)), Description: ptr(demoLevel.description.in(t)),
		FirstResponseMinutes: ptr(demoLevel.first), UpdateMinutes: ptr(demoLevel.update),
		SolutionMinutes: ptr(demoLevel.solution),
	})
	if err != nil {
		return summary, fmt.Errorf("adding the service level: %w", err)
	}

	if _, err := s.queues.UpdateQueue(ctx, desk.ID, command.QueueCommand{ServiceLevelID: &level.ID}); err != nil {
		return summary, fmt.Errorf("giving %s the service level: %w", desk.Name, err)
	}

	contacts, organisations, err := s.seedCustomers(ctx, &summary)
	if err != nil {
		return summary, err
	}

	items, err := s.seedInventory(ctx, caller, t, contacts, organisations, &summary)
	if err != nil {
		return summary, err
	}

	if err := s.seedKnowledge(ctx, caller, t, language, &summary); err != nil {
		return summary, err
	}

	if err := s.seedTemplates(ctx, t, &summary); err != nil {
		return summary, err
	}

	tickets, err := s.seedTickets(ctx, caller, t, desk, contacts, items, &summary)
	if err != nil {
		return summary, err
	}

	return summary, s.seedChanges(ctx, caller, t, tickets, items, &summary)
}

// desk is the queue the migration ships, which every demo ticket is opened in.
func (s *DemoService) desk(ctx context.Context, caller *Principal) (*model.Queue, error) {
	queues, err := s.queues.ListQueues(ctx, caller)
	if err != nil {
		return nil, fmt.Errorf("listing the queues: %w", err)
	}

	for _, queue := range queues {
		if queue.Name == "Service Desk" {
			return queue, nil
		}
	}

	return nil, fmt.Errorf("the shipped queue Service Desk is not there")
}

// seedCustomers adds the organisations and their contacts, and answers both by
// name.
func (s *DemoService) seedCustomers(
	ctx context.Context,
	summary *DemoSummary,
) (map[string]*model.Contact, map[string]uint, error) {
	contacts := map[string]*model.Contact{}
	organisations := map[string]uint{}

	for _, o := range demoOrganisations {
		website := "https://www." + o.domain

		organisation, err := s.customers.CreateOrganisation(ctx, command.OrganisationCommand{
			Name: ptr(o.name), Number: ptr(o.number), URL: &website,
		})
		if err != nil {
			return nil, nil, fmt.Errorf("adding the organisation %s: %w", o.name, err)
		}

		organisations[o.name] = organisation.ID
		summary.Organisations++

		for _, name := range o.contacts {
			email := strings.ToLower(strings.ReplaceAll(name, " ", ".")) + "@" + o.domain

			contact, err := s.customers.CreateContact(ctx, command.ContactCommand{
				Name: ptr(name), Email: &email, OrganisationID: &organisation.ID,
			})
			if err != nil {
				return nil, nil, fmt.Errorf("adding the contact %s: %w", name, err)
			}

			contacts[name] = contact
			summary.Contacts++
		}
	}

	return contacts, organisations, nil
}

// seedKnowledge adds the knowledge base's categories and articles, the articles
// marked as written in language.
func (s *DemoService) seedKnowledge(
	ctx context.Context,
	caller *Principal,
	t i18n.Translator,
	language string,
	summary *DemoSummary,
) error {
	categories := make([]*model.FAQCategory, 0, len(demoCategories))

	for _, c := range demoCategories {
		category, err := s.faq.CreateCategory(ctx, command.FAQCategoryCommand{
			Name: ptr(c.name.in(t)), Description: ptr(c.description.in(t)),
		})
		if err != nil {
			return fmt.Errorf("adding the category %s: %w", c.name.key, err)
		}

		categories = append(categories, category)
	}

	for _, a := range demoArticles {
		visibility := string(a.visibility)

		if _, err := s.faq.CreateArticle(ctx, caller, command.FAQArticleCommand{
			CategoryID: &categories[a.category].ID, Title: ptr(a.title.in(t)), Body: ptr(a.body.in(t)),
			Keywords: ptr(a.keywords.in(t)), Visibility: &visibility, Language: &language,
		}); err != nil {
			return fmt.Errorf("adding the article %s: %w", a.title.key, err)
		}

		summary.FAQArticles++
	}

	return nil
}

// seedTemplates adds the answer templates.
func (s *DemoService) seedTemplates(ctx context.Context, t i18n.Translator, summary *DemoSummary) error {
	for _, template := range demoTemplates {
		if _, err := s.templates.Create(ctx, command.ResponseTemplateCommand{
			Name: ptr(template.name.in(t)), Subject: ptr(template.subject.in(t)), Body: ptr(template.body.in(t)),
		}); err != nil {
			return fmt.Errorf("adding the template %s: %w", template.name.key, err)
		}

		summary.Templates++
	}

	return nil
}

// seedTickets opens the tickets and writes what happened to them, each at its
// moment before now.
func (s *DemoService) seedTickets(
	ctx context.Context,
	caller *Principal,
	t i18n.Translator,
	desk *model.Queue,
	contacts map[string]*model.Contact,
	items map[string]uint,
	summary *DemoSummary,
) (map[string]uint, error) {
	now := s.now()
	moment := now
	back := s.tickets.At(func() time.Time { return moment })
	opened := map[string]uint{}

	for _, ticket := range demoTickets {
		kind, err := s.catalog.Types.GetByName(ctx, ticket.kind)
		if err != nil {
			return nil, fmt.Errorf("finding the shipped type %s: %w", ticket.kind, err)
		}

		priority, err := s.catalog.Priorities.GetByName(ctx, ticket.priority)
		if err != nil {
			return nil, fmt.Errorf("finding the shipped priority %s: %w", ticket.priority, err)
		}

		state, err := s.catalog.States.GetByName(ctx, ticket.state)
		if err != nil {
			return nil, fmt.Errorf("finding the shipped state %s: %w", ticket.state, err)
		}

		title := ticket.title.in(t)
		phone := string(model.ChannelPhone)
		open := command.CreateTicketCommand{
			Title: &title, QueueID: &desk.ID, StateID: &state.ID, PriorityID: &priority.ID, TypeID: &kind.ID,
			ContactID: &contacts[ticket.contact].ID, Channel: &phone, CustomerVisible: true, Subject: &title,
			Body: ptr(ticket.opening.in(t)), Minutes: ticket.minutes,
		}

		if ticket.owned {
			open.OwnerID = &caller.User.ID
		}

		moment = now.Add(-ticket.opened)

		made, err := back.CreateTicket(ctx, caller, open)
		if err != nil {
			return nil, fmt.Errorf("opening the ticket %s: %w", ticket.title.key, err)
		}

		opened[ticket.title.key] = made.ID

		summary.Tickets++
		summary.Articles++

		for _, step := range ticket.steps {
			moment = now.Add(-step.ago)

			if err := s.takeStep(ctx, back, caller, t, made.ID, step, now, summary); err != nil {
				return nil, fmt.Errorf("writing what happened to the ticket %s: %w", ticket.title.key, err)
			}
		}

		if err := s.linkAbout(ctx, caller, made.ID, items[ticket.about]); err != nil {
			return nil, fmt.Errorf("linking the ticket %s to what it is about: %w", ticket.title.key, err)
		}

		if ticket.watched {
			if err := s.tickets.SetWatching(ctx, caller, made.ID, true); err != nil {
				return nil, fmt.Errorf("watching the ticket %s: %w", ticket.title.key, err)
			}
		}
	}

	return opened, nil
}

// takeStep writes one step's article and puts the ticket into its state, through
// the service reading the step's moment.
func (s *DemoService) takeStep(
	ctx context.Context,
	back *TicketApplicationService,
	caller *Principal,
	t i18n.Translator,
	ticketID uint,
	step demoStep,
	now time.Time,
	summary *DemoSummary,
) error {
	if step.body.key != "" {
		channel := string(step.channel)

		if _, err := back.AddArticle(ctx, caller, ticketID, command.AddArticleCommand{
			Channel: &channel, CustomerVisible: step.visible, Subject: ptr(step.subject.in(t)),
			Body: ptr(step.body.in(t)), Minutes: step.minutes,
		}); err != nil {
			return err
		}

		summary.Articles++
	}

	if step.state == "" {
		return nil
	}

	state, err := s.catalog.States.GetByName(ctx, step.state)
	if err != nil {
		return fmt.Errorf("finding the shipped state %s: %w", step.state, err)
	}

	change := command.UpdateTicketCommand{StateID: &state.ID}

	if step.waitFor > 0 {
		until := now.Add(step.waitFor)
		change.PendingUntil = &until
	}

	_, err = back.UpdateTicket(ctx, caller, ticketID, change)

	return err
}

// ptr is a pointer to a copy of v, for a command's optional fields.
func ptr[T any](v T) *T { return &v }
