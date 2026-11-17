package service_test

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dennis-dko/go-ticket-system/internal/application/v1/command"
	"github.com/dennis-dko/go-ticket-system/internal/application/v1/service"
	"github.com/dennis-dko/go-ticket-system/internal/domain/model"
	"github.com/dennis-dko/go-ticket-system/internal/infrastructure/persistence/memory"
	"github.com/dennis-dko/go-ticket-system/internal/support/apperror"
	"github.com/dennis-dko/go-ticket-system/internal/support/webpush"
)

// notifying is the ticket fixture with the notifications wired in as main wires
// them, a second agent who works the desk and reads German, and the built-in
// administrator.
type notifying struct {
	ticketFixture
	notifications *service.NotificationService
	definitions   *service.NotificationDefinitionApplicationService
	bob           *model.User
	builtIn       *model.User
	clock         *time.Time
}

func newNotifying(t *testing.T) notifying {
	t.Helper()

	ctx := context.Background()
	f := newTicketFixture(t)

	agentRole, _ := f.roles.GetByName(ctx, model.RoleAgent)
	adminRole, _ := f.roles.GetByName(ctx, model.RoleAdmin)

	bob, err := f.users.Save(ctx, &model.User{
		Name: "Bob Agent", Email: "bob@example.com", RoleID: agentRole.ID, Language: model.LanguageGerman,
	})
	if err != nil {
		t.Fatal(err)
	}

	builtIn, err := f.users.Save(ctx, &model.User{
		Name: "Administrator", Email: "admin@local", RoleID: adminRole.ID, IsSystem: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	clock := time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)
	definitions := memory.NewNotificationDefinitionRepository()
	refs := service.MacroReferences{Catalog: f.catalog, Customers: f.customers, Queues: f.queues, Users: f.users}
	notifications := service.NewNotificationService(service.NotificationSources{
		Definitions: definitions, Sent: memory.NewNotificationRepository(), Tickets: f.repo, Roles: f.roles, Refs: refs,
	}, func(context.Context) string { return "Europe/Berlin" }).WithClock(func() time.Time { return clock })

	f.tickets.WithNotifications(func(ctx context.Context, ticketID uint, events []model.TicketEvent, by uint) {
		if err := notifications.OnTicketEvents(ctx, ticketID, events, by); err != nil {
			t.Errorf("notifying about ticket %d: %v", ticketID, err)
		}
	})

	return notifying{
		ticketFixture: f, notifications: notifications,
		definitions: service.NewNotificationDefinitionApplicationService(definitions, refs),
		bob:         bob, builtIn: builtIn, clock: &clock,
	}
}

// define sets a notification up that tells the recipients named on the events
// named, and fails the case if it cannot.
func (n notifying) define(t *testing.T, cmd command.NotificationDefinitionCommand) {
	t.Helper()

	if cmd.Messages == nil {
		cmd.Messages = &[]command.NotificationMessageCommand{{Language: "en", Subject: *cmd.Name, Body: *cmd.Name}}
	}

	if _, err := n.definitions.Create(context.Background(), cmd); err != nil {
		t.Fatalf("set up the notification %s: %v", *cmd.Name, err)
	}
}

// told is who was told what, as "name: subject", in the order sent.
func (n notifying) told(t *testing.T) []string {
	t.Helper()

	var told []string

	for _, user := range []*model.User{n.agentUser, n.bob, n.admin.User, n.builtIn, n.customer} {
		mine, _, err := n.notifications.Mine(context.Background(), &service.Principal{User: user})
		if err != nil {
			t.Fatal(err)
		}

		for _, notification := range slices.Backward(mine) {
			told = append(told, user.Name+": "+notification.Subject)
		}
	}

	return told
}

// A ticket opened in a queue tells the agents whose role works in it, each in
// their language and with the ticket's placeholders filled in their words - a
// shipped state as the screen names it, a placeholder naming nothing as a
// dash. Not the agent who opened it, not the built-in administrator, and not an
// account that cannot read the ticket.
func TestAnOpenedTicketTellsTheAgentsWhoWorkItsQueue(t *testing.T) {
	n := newNotifying(t)

	n.define(t, command.NotificationDefinitionCommand{
		Name: ptr("Opened"), Events: &[]string{"TicketCreate"}, Recipients: &[]string{"AgentWritePermissions"},
		Messages: &[]command.NotificationMessageCommand{
			{Language: "en", Subject: "New: <KIX_TICKET_Title>",
				Body: "In <KIX_TICKET_Queue>, <kix_ticket_state>, <KIX_TICKET_Nonsense>"},
			{Language: "de", Subject: "Neu: <KIX_TICKET_Title>", Body: "&lt;KIX_TICKET_State&gt;"},
		},
	})

	n.open(t, n.agent, n.desk, "Printer")

	if told := n.told(t); !slices.Equal(told, []string{"Bob Agent: Neu: Printer", "Ada Admin: New: Printer"}) {
		t.Fatalf("opening a ticket told %v; want the two others who work the desk", told)
	}

	bob, _, _ := n.notifications.Mine(context.Background(), &service.Principal{User: n.bob})
	ada, _, _ := n.notifications.Mine(context.Background(), n.admin)

	if bob[0].Body != "neu" || ada[0].Body != "In Service Desk, new, -" {
		t.Errorf("the notifications read %q and %q", bob[0].Body, ada[0].Body)
	}
}

// A notification about a ticket it is not set up for tells nobody - another
// event, a ticket its criteria do not take, a notification retired - and one
// sent once a day tells each recipient about a ticket once that day, and again
// the next.
func TestANotificationTellsOnlyWhatItIsSetUpFor(t *testing.T) {
	ctx := context.Background()
	n := newNotifying(t)
	printer := n.open(t, n.agent, n.desk, "Printer")
	writers := &[]string{"AgentWritePermissions"}
	titled := &[]string{"TicketTitleUpdate"}

	n.define(t, command.NotificationDefinitionCommand{Name: ptr("Daily"), Events: titled, Recipients: writers,
		OncePerDay: ptr(true)})
	n.define(t, command.NotificationDefinitionCommand{Name: ptr("Finance"), Events: titled, Recipients: writers,
		Criteria: &command.JobCriteriaCommand{QueueIDs: []uint{n.readOnly.ID}}})
	n.define(t, command.NotificationDefinitionCommand{Name: ptr("Retired"), Events: titled, Recipients: writers,
		Valid: ptr(false)})
	n.define(t, command.NotificationDefinitionCommand{Name: ptr("Priority"), Events: &[]string{"TicketPriorityUpdate"},
		Recipients: writers})

	retitle := func(title string) {
		if _, err := n.tickets.UpdateTicket(ctx, n.agent, printer.ID, command.UpdateTicketCommand{Title: ptr(title)}); err != nil {
			t.Fatal(err)
		}
	}

	retitle("Printer jams")
	retitle("Printer jams again")

	if told := n.told(t); !slices.Equal(told, []string{"Bob Agent: Daily", "Ada Admin: Daily"}) {
		t.Fatalf("two new titles told %v; want the daily notification once to each", told)
	}

	*n.clock = n.clock.Add(24 * time.Hour)
	retitle("Printer jams for days")

	if told := n.told(t); len(told) != 4 {
		t.Errorf("a new title the next day left the told as %v; want the daily notification again", told)
	}
}

// The owner, the responsible agent, those watching the ticket and those who
// chose its queue are each told by the notification that names them - a
// watcher who cannot read the ticket is not.
func TestANotificationTellsWhomItNamesAroundTheTicket(t *testing.T) {
	ctx := context.Background()
	n := newNotifying(t)
	printer := n.open(t, n.admin, n.desk, "Printer")

	if _, err := n.tickets.UpdateTicket(ctx, n.admin, printer.ID, command.UpdateTicketCommand{
		OwnerID: &n.bob.ID, ResponsibleID: &n.agentUser.ID,
	}); err != nil {
		t.Fatal(err)
	}

	for _, watcher := range []uint{n.bob.ID, n.customer.ID} {
		if err := n.repo.SetWatching(ctx, printer.ID, watcher, true); err != nil {
			t.Fatal(err)
		}
	}

	n.queues.Follow(n.desk.ID, n.agentUser.ID)

	for name, recipient := range map[string]string{
		"Owner": "AgentOwner", "Responsible": "AgentResponsible", "Watcher": "AgentWatcher", "Queue": "AgentMyQueues",
	} {
		n.define(t, command.NotificationDefinitionCommand{
			Name: ptr(name), Events: &[]string{"ArticleCreate"}, Recipients: &[]string{recipient},
		})
	}

	if _, err := n.tickets.AddArticle(ctx, n.admin, printer.ID, command.AddArticleCommand{
		Body: ptr("Toner ordered."),
	}); err != nil {
		t.Fatal(err)
	}

	told := n.told(t)
	slices.Sort(told)

	want := []string{"Agnes Agent: Queue", "Agnes Agent: Responsible", "Bob Agent: Owner", "Bob Agent: Watcher"}
	if !slices.Equal(told, want) {
		t.Errorf("an answer told %v, want %v", told, want)
	}
}

// What the application changes is news too: a job's step tells the agents
// around the ticket, none of them left out as the one who made the change.
func TestAChangeTheApplicationMakesIsSentToo(t *testing.T) {
	ctx := context.Background()
	n := newNotifying(t)
	printer := n.open(t, n.admin, n.desk, "Printer")

	n.define(t, command.NotificationDefinitionCommand{
		Name: ptr("Raised"), Events: &[]string{"TicketPriorityUpdate"}, Recipients: &[]string{"AgentWritePermissions"},
	})

	macros, jobs := memory.NewMacroRepository(), memory.NewJobRepository()
	automation := service.NewAutomationService(service.AutomationRepositories{
		Jobs: jobs, Macros: macros, Plans: memory.NewExecPlanRepository(), Runs: memory.NewJobRunRepository(),
	}, n.repo, n.tickets, func(context.Context) string { return "Europe/Berlin" })

	raise, _ := macros.Save(ctx, &model.Macro{Name: "Raise", Valid: true, Actions: []model.MacroAction{
		{Type: model.ActionPrioritySet, Parameters: map[string]string{"priorityId": idText(n.priority["high"].ID)}},
	}})
	job, _ := jobs.Save(ctx, &model.Job{Name: "Raise the desk", Valid: true,
		Criteria: model.TicketCriteria{QueueIDs: []uint{n.desk.ID}}, MacroIDs: []uint{raise.ID}})

	if _, err := automation.Run(ctx, job.ID, model.RunManual); err != nil {
		t.Fatal(err)
	}

	told := n.told(t)
	slices.Sort(told)

	if want := []string{"Ada Admin: Raised", "Agnes Agent: Raised", "Bob Agent: Raised"}; !slices.Equal(told, want) {
		t.Errorf("a job raising %s told %v, want %v", printer.Title, told, want)
	}
}

// An agent reads what they were told and marks it read, one or all at once;
// somebody else's notification is not found rather than refused.
func TestAnAgentReadsWhatTheyWereTold(t *testing.T) {
	ctx := context.Background()
	n := newNotifying(t)

	n.define(t, command.NotificationDefinitionCommand{
		Name: ptr("Opened"), Events: &[]string{"TicketCreate"}, Recipients: &[]string{"AgentWritePermissions"},
	})

	n.open(t, n.agent, n.desk, "Printer")
	n.open(t, n.agent, n.desk, "Scanner")

	bob := &service.Principal{User: n.bob}

	mine, unread, err := n.notifications.Mine(ctx, bob)
	if err != nil || len(mine) != 2 || unread != 2 {
		t.Fatalf("Bob reads %d notifications, %d unread, %v; want two of each", len(mine), unread, err)
	}

	if err := n.notifications.MarkRead(ctx, bob, mine[0].ID); err != nil {
		t.Fatal(err)
	}

	if _, unread, _ = n.notifications.Mine(ctx, bob); unread != 1 {
		t.Errorf("one marked read leaves %d unread", unread)
	}

	adas, _, _ := n.notifications.Mine(ctx, n.admin)
	if err := n.notifications.MarkRead(ctx, bob, adas[0].ID); apperror.KindOf(err) != apperror.KindNotFound {
		t.Errorf("Bob marking Ada's notification read was answered %v; want not found", err)
	}

	if err := n.notifications.MarkAllRead(ctx, bob); err != nil {
		t.Fatal(err)
	}

	if _, unread, _ = n.notifications.Mine(ctx, bob); unread != 0 {
		t.Errorf("all marked read leaves %d unread", unread)
	}

	if _, unread, _ = n.notifications.Mine(ctx, n.admin); unread != 2 || !strings.HasPrefix(n.admin.User.Name, "Ada") {
		t.Errorf("Bob marking his own read left Ada %d unread", unread)
	}
}

// fakePusher keeps what it is given to push, and fails where it is told to.
type fakePusher struct {
	pushed map[uint][]webpush.Message
	fail   error
}

func (f *fakePusher) Push(_ context.Context, userID uint, messages ...webpush.Message) error {
	f.pushed[userID] = append(f.pushed[userID], messages...)

	return f.fail
}

// What an agent is told is pushed to them once, as a message of its own with the
// notification's words, when it was sent, and a tag the service worker asks
// about; a push that fails is said and not tried again a minute later.
func TestWhatAnAgentIsToldIsPushedOnce(t *testing.T) {
	ctx := context.Background()
	n := newNotifying(t)
	pusher := &fakePusher{pushed: map[uint][]webpush.Message{}}
	n.notifications.WithPush(pusher)

	n.define(t, command.NotificationDefinitionCommand{
		Name: ptr("Opened"), Events: &[]string{"TicketCreate"}, Recipients: &[]string{"AgentWritePermissions"},
		Messages: &[]command.NotificationMessageCommand{
			{Language: "en", Subject: "New: <KIX_TICKET_Title>", Body: "Opened."},
		},
	})

	n.open(t, n.agent, n.desk, "Printer")

	if sent, err := n.notifications.SendUnsent(ctx); sent != 2 || err != nil {
		t.Fatalf("sending sent %d, %v; want the two told", sent, err)
	}

	mine, _, _ := n.notifications.Mine(ctx, &service.Principal{User: n.bob})
	id := strconv.FormatUint(uint64(mine[0].ID), 10)
	want := webpush.Message{Title: "New: Printer", Body: "Opened.", Tag: "gts-notification-" + id,
		At: n.clock.Format(time.RFC3339), About: id}

	if got := pusher.pushed[n.bob.ID]; len(got) != 1 || got[0] != want {
		t.Errorf("Bob was pushed %+v, want %+v", got, want)
	}

	if sent, _ := n.notifications.SendUnsent(ctx); sent != 0 || len(pusher.pushed[n.bob.ID]) != 1 {
		t.Errorf("sending again sent %d and pushed Bob %d", sent, len(pusher.pushed[n.bob.ID]))
	}

	pusher.fail = errors.New("the push service is down")
	n.open(t, n.agent, n.desk, "Scanner")

	if sent, err := n.notifications.SendUnsent(ctx); sent != 2 || err == nil {
		t.Errorf("sending through a push service that is down answered %d, %v; want both sent and the failure said",
			sent, err)
	}

	if sent, _ := n.notifications.SendUnsent(ctx); sent != 0 {
		t.Errorf("a push that failed was tried again: %d sent", sent)
	}
}
