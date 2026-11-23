package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/dennis-dko/qubitdesk/internal/application/v1/service"
	"github.com/dennis-dko/qubitdesk/internal/domain/model"
	"github.com/dennis-dko/qubitdesk/internal/infrastructure/persistence/memory"
)

// demoWorld is an installation as the migration leaves it - the built-in
// administrator, the shipped states, priorities, types and Service Desk - with
// the services the demo writes through, and the clock it runs at.
type demoWorld struct {
	demo      *service.DemoService
	tickets   *service.TicketApplicationService
	customers *service.CustomerApplicationService
	faq       *service.FAQApplicationService
	templates *service.ResponseTemplateApplicationService
	levels    *service.SLAApplicationService
	queues    *memory.QueueRepository
	admin     *service.Principal
	now       time.Time
}

func newDemoWorld(t *testing.T) demoWorld {
	t.Helper()

	ctx := context.Background()
	users := memory.NewUserRepository()
	roles := memory.NewRoleRepository(users)
	adminRole, _ := roles.GetByName(ctx, model.RoleAdmin)
	agentRole, _ := roles.GetByName(ctx, model.RoleAgent)

	admin, err := users.Save(ctx, &model.User{Name: "Administrator", Email: service.SystemUserEmail,
		RoleID: adminRole.ID})
	if err != nil {
		t.Fatal(err)
	}

	states := memory.NewTicketStateRepository()

	for _, seed := range []model.TicketState{
		{Name: "new", Type: model.StateTypeNew, IsSystem: true},
		{Name: "open", Type: model.StateTypeOpen, IsSystem: true},
		{Name: "pending-reminder", Type: model.StateTypePendingReminder},
		{Name: "closed", Type: model.StateTypeClosed, IsSystem: true},
		{Name: "merged", Type: model.StateTypeMerged, IsSystem: true},
	} {
		seed.Valid = true
		_, _ = states.Save(ctx, &seed)
	}

	priorities := memory.NewTicketPriorityRepository()

	for level, name := range []string{"very-high", "high", "normal", "low", "very-low"} {
		_, _ = priorities.Save(ctx, &model.TicketPriority{Name: name, Level: level + 1, Valid: true,
			IsSystem: name == model.PriorityNormal})
	}

	types := memory.NewTicketTypeRepository()

	for _, name := range []string{model.TypeUnclassified, "incident", "service-request"} {
		_, _ = types.Save(ctx, &model.TicketType{Name: name, Valid: true, IsSystem: name == model.TypeUnclassified})
	}

	queues := memory.NewQueueRepository()
	_, _ = queues.Save(ctx, &model.Queue{Name: "Service Desk", Valid: true, FollowUp: model.FollowUpReopen,
		Access: []model.QueueAccess{{RoleID: agentRole.ID, Level: model.AccessWrite}}})

	levels, calendars := memory.NewServiceLevelRepository(), memory.NewCalendarRepository()
	catalog := service.TicketCatalogRepositories{States: states, Priorities: priorities, Types: types,
		UnlockTypes: memory.NewUnlockStateTypeRepository()}
	customerRepos := service.TicketCustomerRepositories{Contacts: memory.NewContactRepository(),
		Organisations: memory.NewOrganisationRepository()}
	tickets := service.NewTicketApplicationService(memory.NewTicketRepository(), queues, catalog, customerRepos,
		users, roles, func(context.Context) string { return "Europe/Berlin" }).
		WithServiceLevels(levels, calendars, func(context.Context) string { return "" })
	templateRepo := memory.NewResponseTemplateRepository()
	faqRepo := memory.NewFAQRepository()

	w := demoWorld{
		tickets:   tickets,
		customers: service.NewCustomerApplicationService(customerRepos.Organisations, customerRepos.Contacts),
		faq:       service.NewFAQApplicationService(faqRepo.Categories(), faqRepo.Articles(), users),
		templates: service.NewResponseTemplateApplicationService(templateRepo, tickets, service.MacroReferences{
			Catalog: catalog, Customers: customerRepos, Queues: queues, Users: users,
		}),
		levels: service.NewSLAApplicationService(levels, calendars),
		queues: queues,
		admin:  &service.Principal{User: admin, Role: adminRole, Permissions: model.AdminPermissions()},
		now:    time.Date(2026, 11, 21, 15, 0, 0, 0, time.UTC),
	}

	w.demo = service.NewDemoService(users, service.NewAuthService(users, roles), tickets, catalog,
		service.NewQueueApplicationService(queues, roles, levels).WithTemplates(templateRepo), w.levels,
		w.customers, w.faq, w.templates).WithClock(func() time.Time { return w.now })

	return w
}

// The demo fills an empty installation in the language asked for, through the
// services anybody works through: customers and their contacts, tickets opened
// over the last days with what was said in them and the time it took, a service
// level the Service Desk starts its tickets with - which the ticket nobody has
// answered yet has already missed - a knowledge base and answer templates.
func TestTheDemoFillsAnEmptyInstallationInTheLanguageAsked(t *testing.T) {
	ctx := context.Background()
	w := newDemoWorld(t)

	summary, err := w.demo.Seed(ctx, "de")
	if err != nil {
		t.Fatalf("seeding the demo: %v", err)
	}

	if summary.Skipped != "" || summary.Organisations != 3 || summary.Contacts != 6 || summary.Tickets != 8 ||
		summary.Articles < 15 || summary.FAQArticles != 4 || summary.Templates != 2 {
		t.Fatalf("the demo added %+v", summary)
	}

	listed, total, err := w.tickets.ListTickets(ctx, w.admin, service.TicketListFilter{Limit: 50})
	if err != nil || total != 8 {
		t.Fatalf("the administrator lists %d tickets (%v), want the demo's eight", total, err)
	}

	byTitle := map[string]*model.Ticket{}

	for _, ticket := range listed {
		byTitle[ticket.Title] = ticket

		if !ticket.CreatedAt.Before(w.now) {
			t.Errorf("%q was opened at %v, not before the demo ran at %v", ticket.Title, ticket.CreatedAt, w.now)
		}

		if ticket.ContactID == nil || ticket.OrganisationID == nil {
			t.Errorf("%q is for nobody: contact %v, organisation %v", ticket.Title, ticket.ContactID,
				ticket.OrganisationID)
		}
	}

	printer := byTitle["Der Drucker im zweiten Stock druckt leere Seiten"]
	if printer == nil {
		t.Fatalf("no ticket carries the printer's German title among %v", byTitle)
	}

	if want := w.now.Add(-28 * time.Hour); !printer.CreatedAt.Equal(want) || printer.FirstResponseAt == nil {
		t.Errorf("the printer's ticket was opened at %v and answered at %v, want opened at %v and answered",
			printer.CreatedAt, printer.FirstResponseAt, want)
	}

	if minutes, err := w.tickets.AccountedMinutes(ctx, w.admin, printer.ID); err != nil || minutes != 30 {
		t.Errorf("the printer's ticket carries %d minutes (%v), want the 5, 10 and 15 noted on it", minutes, err)
	}

	certificate := byTitle["Das Kundenportal warnt vor einem abgelaufenen Zertifikat"]
	if certificate == nil || certificate.FirstResponseDue == nil || !certificate.Escalated(w.now) {
		t.Errorf("the certificate's ticket, opened six hours ago and not answered, is not overdue: %+v", certificate)
	}

	if watching, err := w.tickets.Watching(ctx, w.admin, certificate.ID); err != nil || !watching {
		t.Errorf("the administrator does not watch the most urgent ticket (%v)", err)
	}

	waiting := byTitle["Die VPN-Verbindung bricht alle paar Minuten ab"]
	if waiting == nil || waiting.PendingUntil == nil || !waiting.PendingUntil.After(w.now) {
		t.Errorf("the VPN ticket does not wait for a moment still to come: %+v", waiting)
	}

	if closed := byTitle["Der zweite Bildschirm flackert"]; closed == nil || closed.ClosedAt == nil ||
		!closed.ClosedAt.Equal(w.now.Add(-118*time.Hour)) {
		t.Errorf("the monitor's ticket was not closed when its answer was written: %+v", closed)
	}

	desk, _ := w.queues.GetAll(ctx)
	if desk[0].ServiceLevelID == nil {
		t.Error("the Service Desk starts its tickets with no service level")
	}

	articles, err := w.faq.Search(ctx, w.admin, "", 0)
	if err != nil || len(articles) != 4 {
		t.Fatalf("the knowledge base holds %d articles (%v), want the demo's four", len(articles), err)
	}

	for _, article := range articles {
		if article.Language != "de" {
			t.Errorf("the article %q is marked as written in %q", article.Title, article.Language)
		}
	}

	if templates, err := w.templates.List(ctx); err != nil || len(templates) != 2 ||
		templates[0].Name != "Eingang bestätigen" && templates[1].Name != "Eingang bestätigen" {
		t.Errorf("the templates are %v (%v), want the demo's two in German", templates, err)
	}
}

// A second seed, or one into an installation somebody has started working in,
// adds nothing: demo tickets among real ones could never be told apart again.
func TestTheDemoLeavesAnInstallationWithTicketsAlone(t *testing.T) {
	ctx := context.Background()
	w := newDemoWorld(t)

	if _, err := w.demo.Seed(ctx, "en"); err != nil {
		t.Fatalf("seeding the demo: %v", err)
	}

	again, err := w.demo.Seed(ctx, "en")
	if err != nil || again.Skipped == "" || again.Tickets != 0 {
		t.Errorf("a second seed answered %+v (%v), want it skipped", again, err)
	}

	if levels, _ := w.levels.List(ctx); len(levels) != 1 {
		t.Errorf("after a second seed there are %d service levels, want the first seed's one", len(levels))
	}
}
