package service_test

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dennis-dko/go-ticket-system/internal/application/v1/command"
	"github.com/dennis-dko/go-ticket-system/internal/application/v1/service"
	"github.com/dennis-dko/go-ticket-system/internal/domain/model"
	"github.com/dennis-dko/go-ticket-system/internal/infrastructure/persistence/memory"
	"github.com/dennis-dko/go-ticket-system/internal/support/apperror"
)

// ticketFixture is the ticket service over in-memory tables seeded as the
// migrations seed a real database, with a queue the agents work in, one they
// only read and one they do not reach - and a caller on each side of
// queues:write.
type ticketFixture struct {
	tickets    *service.TicketApplicationService
	repo       *memory.TicketRepository
	priorities *memory.TicketPriorityRepository
	states     map[string]*model.TicketState
	priority   map[string]*model.TicketPriority
	desk       *model.Queue
	readOnly   *model.Queue
	hidden     *model.Queue
	admin      *service.Principal
	agent      *service.Principal
	agentUser  *model.User
	customer   *model.User
}

func newTicketFixture(t *testing.T) ticketFixture {
	t.Helper()

	ctx := context.Background()
	users := memory.NewUserRepository()
	roles := memory.NewRoleRepository(users)

	agentRole, _ := roles.GetByName(ctx, model.RoleAgent)
	adminRole, _ := roles.GetByName(ctx, model.RoleAdmin)
	customerRole, _ := roles.GetByName(ctx, model.RoleCustomer)

	agentUser, _ := users.Save(ctx, &model.User{Name: "Agnes Agent", Email: "agent@example.com", RoleID: agentRole.ID})
	adminUser, _ := users.Save(ctx, &model.User{Name: "Ada Admin", Email: "admin@example.com", RoleID: adminRole.ID})
	customer, _ := users.Save(ctx, &model.User{Name: "Carl Customer", Email: "carl@example.com", RoleID: customerRole.ID})

	states := memory.NewTicketStateRepository()
	priorities := memory.NewTicketPriorityRepository()
	types := memory.NewTicketTypeRepository()

	f := ticketFixture{
		repo:       memory.NewTicketRepository(),
		priorities: priorities,
		states:     map[string]*model.TicketState{},
		priority:   map[string]*model.TicketPriority{},
		admin:      &service.Principal{User: adminUser, Role: adminRole, Permissions: model.AdminPermissions()},
		agent:      &service.Principal{User: agentUser, Role: agentRole, Permissions: model.AgentPermissions()},
		agentUser:  agentUser,
		customer:   customer,
	}

	for _, seed := range []model.TicketState{
		{Name: "new", Type: model.StateTypeNew, IsSystem: true},
		{Name: "open", Type: model.StateTypeOpen, IsSystem: true},
		{Name: "pending-reminder", Type: model.StateTypePendingReminder},
		{Name: "closed", Type: model.StateTypeClosed, IsSystem: true},
		{Name: "merged", Type: model.StateTypeMerged, IsSystem: true},
	} {
		seed.Valid = true
		f.states[seed.Name], _ = states.Save(ctx, &seed)
	}

	for level, name := range []string{"high", "normal", "low"} {
		f.priority[name], _ = priorities.Save(ctx, &model.TicketPriority{
			Name: name, Level: level + 1, Valid: true, IsSystem: name == "normal",
		})
	}

	_, _ = types.Save(ctx, &model.TicketType{Name: model.TypeUnclassified, Valid: true, IsSystem: true})

	queues := memory.NewQueueRepository()
	f.desk, _ = queues.Save(ctx, &model.Queue{
		Name: "Service Desk", Valid: true, FollowUp: model.FollowUpReopen,
		Access: []model.QueueAccess{{RoleID: agentRole.ID, Level: model.AccessWrite}},
	})
	f.readOnly, _ = queues.Save(ctx, &model.Queue{
		Name: "Finance", Valid: true, FollowUp: model.FollowUpReopen,
		Access: []model.QueueAccess{{RoleID: agentRole.ID, Level: model.AccessRead}},
	})
	f.hidden, _ = queues.Save(ctx, &model.Queue{Name: "Board", Valid: true, FollowUp: model.FollowUpReopen})

	f.tickets = service.NewTicketApplicationService(f.repo, queues,
		service.TicketCatalogRepositories{States: states, Priorities: priorities, Types: types},
		users, roles, func(context.Context) string { return "Europe/Berlin" })

	return f
}

// open opens a ticket as the caller in the queue, and fails the case if it cannot.
func (f ticketFixture) open(t *testing.T, caller *service.Principal, queue *model.Queue, title string) *model.Ticket {
	t.Helper()

	ticket, err := f.tickets.CreateTicket(context.Background(), caller, command.CreateTicketCommand{
		Title: ptr(title), QueueID: &queue.ID, Body: ptr("What happened, in the caller's words."),
	})
	if err != nil {
		t.Fatalf("open %q: %v", title, err)
	}

	return ticket
}

// A ticket opened with no more than its queue, title and body starts as the
// shipped values say - new, normal, unclassified - is numbered by its day and
// id, and carries the article it was opened with and the line saying so.
func TestATicketStartsWithWhatShips(t *testing.T) {
	ctx := context.Background()
	f := newTicketFixture(t)

	ticket, err := f.tickets.CreateTicket(ctx, f.agent, command.CreateTicketCommand{
		Title: ptr("  Printer jams  "), QueueID: &f.desk.ID, Body: ptr("It jams on every page."),
		Channel: ptr("phone"), CustomerVisible: true, Minutes: 12,
	})
	if err != nil {
		t.Fatalf("open a ticket: %v", err)
	}

	if ticket.Title != "Printer jams" || ticket.StateID != f.states["new"].ID ||
		ticket.PriorityID != f.priority["normal"].ID || ticket.Channel != model.ChannelPhone {
		t.Errorf("the ticket starts as %+v", ticket)
	}

	if len(ticket.Number) != 16 || !strings.HasSuffix(ticket.Number, "00000001") {
		t.Errorf("the ticket is numbered %q; want its day and its id, sixteen digits", ticket.Number)
	}

	articles, _ := f.tickets.Articles(ctx, f.agent, ticket.ID)
	if len(articles) != 1 || articles[0].Body != "It jams on every page." || !articles[0].CustomerVisible ||
		articles[0].AuthorID == nil || *articles[0].AuthorID != f.agentUser.ID {
		t.Errorf("the ticket carries %+v; want the article it was opened with", articles)
	}

	if minutes, _ := f.tickets.AccountedMinutes(ctx, f.agent, ticket.ID); minutes != 12 {
		t.Errorf("the ticket has %d minutes noted, want the 12 it was opened with", minutes)
	}

	history, _ := f.tickets.History(ctx, f.agent, ticket.ID)
	if len(history) != 1 || history[0].Event != model.HistoryCreated {
		t.Errorf("the history is %+v; want the line saying it was opened", history)
	}
}

// A ticket is reached through its queue. Out of reach it is not found - read,
// changed, written in or watched - exactly as one that is not there; in a queue
// the role only reads, it is read and not changed.
func TestATicketOutOfReachIsNotFound(t *testing.T) {
	ctx := context.Background()
	f := newTicketFixture(t)

	hidden := f.open(t, f.admin, f.hidden, "Board business")
	readable := f.open(t, f.admin, f.readOnly, "An invoice")

	notFound := func(what string, err error) {
		t.Helper()

		if detail, ok := apperror.Detail(err); !ok || detail.Kind != apperror.KindNotFound {
			t.Errorf("%s answered %v, want not found", what, err)
		}
	}

	_, err := f.tickets.GetTicket(ctx, f.agent, hidden.ID)
	notFound("reading a ticket in a queue the agents do not reach", err)

	_, err = f.tickets.UpdateTicket(ctx, f.agent, hidden.ID, command.UpdateTicketCommand{Title: ptr("Mine")})
	notFound("changing it", err)

	notFound("watching it", f.tickets.SetWatching(ctx, f.agent, hidden.ID, true))

	if _, err := f.tickets.GetTicket(ctx, f.agent, readable.ID); err != nil {
		t.Errorf("reading a ticket in a queue the agents read answered %v", err)
	}

	_, err = f.tickets.AddArticle(ctx, f.agent, readable.ID, command.AddArticleCommand{Body: ptr("Paid.")})
	notFound("writing in a ticket of a queue the agents only read", err)

	_, err = f.tickets.CreateTicket(ctx, f.agent, command.CreateTicketCommand{
		Title: ptr("x"), QueueID: &f.readOnly.ID, Body: ptr("y"),
	})
	refusedWith(t, err, "queueReadOnly")

	_, err = f.tickets.CreateTicket(ctx, f.agent, command.CreateTicketCommand{
		Title: ptr("x"), QueueID: &f.hidden.ID, Body: ptr("y"),
	})
	notFound("opening a ticket in a queue the agents do not reach", err)

	listed, total, err := f.tickets.ListTickets(ctx, f.agent, service.TicketListFilter{})
	if err != nil || total != 1 || len(listed) != 1 || listed[0].ID != readable.ID {
		t.Errorf("an agent lists %d of %d ticket(s), %v; want only the one they reach", len(listed), total, err)
	}
}

// A pending state waits for a moment still to come: without one it is refused,
// a moment past is refused, leaving the state drops the moment, and a moment for
// a state that waits for none is refused rather than dropped.
func TestAPendingTicketWaitsForAMomentStillToCome(t *testing.T) {
	ctx := context.Background()
	f := newTicketFixture(t)
	ticket := f.open(t, f.agent, f.desk, "Waiting for a part")

	pending := f.states["pending-reminder"].ID
	past := time.Now().Add(-time.Hour)
	future := time.Now().Add(48 * time.Hour)

	for name, cmd := range map[string]command.UpdateTicketCommand{
		"a pending state with no moment":           {StateID: &pending},
		"a pending state with a moment past":       {StateID: &pending, PendingUntil: &past},
		"a moment for a state that waits for none": {PendingUntil: &future},
	} {
		_, err := f.tickets.UpdateTicket(ctx, f.agent, ticket.ID, cmd)
		if detail, ok := apperror.Detail(err); !ok || !slices.Equal(detail.Fields, []string{"pendingUntil"}) {
			t.Errorf("%s answered %v, want the moment refused", name, err)
		}
	}

	waiting, err := f.tickets.UpdateTicket(ctx, f.agent, ticket.ID,
		command.UpdateTicketCommand{StateID: &pending, PendingUntil: &future})
	if err != nil || waiting.PendingUntil == nil {
		t.Fatalf("putting the ticket on hold until the day after tomorrow answered %+v, %v", waiting, err)
	}

	open := f.states["open"].ID

	working, err := f.tickets.UpdateTicket(ctx, f.agent, ticket.ID, command.UpdateTicketCommand{StateID: &open})
	if err != nil || working.PendingUntil != nil {
		t.Fatalf("opening the ticket again left it waiting: %+v, %v", working, err)
	}
}

// A ticket in a finished state says when it finished, and stops saying so when
// it is opened again; merged is reached by merging and never chosen.
func TestAFinishedTicketKnowsWhenItFinished(t *testing.T) {
	ctx := context.Background()
	f := newTicketFixture(t)
	ticket := f.open(t, f.agent, f.desk, "Done soon")

	closed, open, merged := f.states["closed"].ID, f.states["open"].ID, f.states["merged"].ID

	done, err := f.tickets.UpdateTicket(ctx, f.agent, ticket.ID, command.UpdateTicketCommand{StateID: &closed})
	if err != nil || done.ClosedAt == nil {
		t.Fatalf("closing the ticket answered %+v, %v", done, err)
	}

	again, err := f.tickets.UpdateTicket(ctx, f.agent, ticket.ID, command.UpdateTicketCommand{StateID: &open})
	if err != nil || again.ClosedAt != nil {
		t.Fatalf("opening it again answered %+v, %v", again, err)
	}

	_, err = f.tickets.UpdateTicket(ctx, f.agent, ticket.ID, command.UpdateTicketCommand{StateID: &merged})
	invalidFields(t, err, "stateId")
}

// Every change is in the history, by the names things had then; locking a ticket
// nobody owns makes the caller its owner; and a customer's account owns nothing.
func TestEveryChangeIsInTheHistoryByTheNamesThingsHadThen(t *testing.T) {
	ctx := context.Background()
	f := newTicketFixture(t)
	ticket := f.open(t, f.agent, f.desk, "Slow network")

	high := f.priority["high"].ID
	locked := true

	changed, err := f.tickets.UpdateTicket(ctx, f.agent, ticket.ID, command.UpdateTicketCommand{
		Title: ptr("Slow network on the second floor"), PriorityID: &high, Locked: &locked,
	})
	if err != nil {
		t.Fatalf("change the ticket: %v", err)
	}

	if changed.OwnerID == nil || *changed.OwnerID != f.agentUser.ID || !changed.Locked || changed.LockedAt == nil {
		t.Errorf("locking a ticket nobody owned left it as %+v; want it the agent's and locked", changed)
	}

	history, _ := f.tickets.History(ctx, f.agent, ticket.ID)

	var lines []string
	for _, entry := range history {
		lines = append(lines, string(entry.Event)+" "+entry.OldValue+" > "+entry.NewValue)
	}

	for _, want := range []string{
		"title Slow network > Slow network on the second floor",
		"priority normal > high",
		"owner  > Agnes Agent",
		"lock false > true",
	} {
		if !slices.Contains(lines, want) {
			t.Errorf("the history does not say %q: %v", want, lines)
		}
	}

	_, err = f.tickets.UpdateTicket(ctx, f.agent, ticket.ID, command.UpdateTicketCommand{OwnerID: &f.customer.ID})
	invalidFields(t, err, "ownerId")
}

// A value no longer offered is not chosen for a ticket, and a ticket that already
// carries it keeps it while something else about it changes.
func TestARetiredValueIsNotChosenButKept(t *testing.T) {
	ctx := context.Background()
	f := newTicketFixture(t)

	low := f.priority["low"]
	ticket, err := f.tickets.CreateTicket(ctx, f.agent, command.CreateTicketCommand{
		Title: ptr("Someday"), QueueID: &f.desk.ID, Body: ptr("No hurry."), PriorityID: &low.ID,
	})
	if err != nil {
		t.Fatalf("open a ticket of low priority: %v", err)
	}

	retired := *low
	retired.Valid = false

	if _, err := f.priorities.Update(ctx, &retired); err != nil {
		t.Fatalf("retire the priority: %v", err)
	}

	_, err = f.tickets.CreateTicket(ctx, f.agent, command.CreateTicketCommand{
		Title: ptr("Later"), QueueID: &f.desk.ID, Body: ptr("Also no hurry."), PriorityID: &low.ID,
	})
	refusedWith(t, err, "priorityRetired")

	kept, err := f.tickets.UpdateTicket(ctx, f.agent, ticket.ID, command.UpdateTicketCommand{Title: ptr("Someday, maybe")})
	if err != nil || kept.PriorityID != low.ID {
		t.Fatalf("changing the title of a ticket with a retired priority answered %+v, %v", kept, err)
	}
}

// One page of tickets and the true total; a limit above the maximum is refused
// rather than quietly lowered; and the unfinished filter leaves out what is done.
func TestAListingIsOnePageAndTheTrueTotal(t *testing.T) {
	ctx := context.Background()
	f := newTicketFixture(t)

	for _, title := range []string{"one", "two", "three"} {
		f.open(t, f.agent, f.desk, title)
	}

	page, total, err := f.tickets.ListTickets(ctx, f.agent, service.TicketListFilter{Limit: 2})
	if err != nil || total != 3 || len(page) != 2 {
		t.Fatalf("the first page holds %d of %d, %v; want two of three", len(page), total, err)
	}

	second, _, _ := f.tickets.ListTickets(ctx, f.agent, service.TicketListFilter{Limit: 2, Page: 2})
	if len(second) != 1 {
		t.Errorf("the second page holds %d, want the one left", len(second))
	}

	_, _, err = f.tickets.ListTickets(ctx, f.agent, service.TicketListFilter{Limit: service.MaxTicketPage + 1})
	invalidFields(t, err, "limit")

	closed := f.states["closed"].ID
	if _, err := f.tickets.UpdateTicket(ctx, f.agent, page[0].ID, command.UpdateTicketCommand{StateID: &closed}); err != nil {
		t.Fatalf("close a ticket: %v", err)
	}

	_, unfinished, _ := f.tickets.ListTickets(ctx, f.agent, service.TicketListFilter{Unfinished: true})
	if unfinished != 2 {
		t.Errorf("%d ticket(s) are listed as unfinished, want the two still open", unfinished)
	}
}

// Opening a ticket is counted by how it arrived, and only once it is open: a
// refused one counts nothing.
func TestOpeningATicketIsCountedByHowItArrived(t *testing.T) {
	ctx := context.Background()
	f := newTicketFixture(t)
	spy := &counterSpy{counted: map[string][]string{}}
	f.tickets.WithMetrics(spy)

	f.open(t, f.agent, f.desk, "Counted")

	if _, err := f.tickets.CreateTicket(ctx, f.agent, command.CreateTicketCommand{
		Title: ptr("Refused"), QueueID: &f.hidden.ID, Body: ptr("Nowhere to go."),
	}); err == nil {
		t.Fatal("a ticket was opened in a queue the agent does not reach")
	}

	if got := spy.counted[service.MetricTicketsOpened]; !slices.Equal(got, []string{"channel=note"}) {
		t.Errorf("opening one ticket and being refused another counted %v, want one note", got)
	}
}

// A ticket can be given to whoever's role works in its queue, and to nobody else.
func TestATicketCanBeGivenToWhoeverWorksInItsQueue(t *testing.T) {
	ctx := context.Background()
	f := newTicketFixture(t)

	people, err := f.tickets.Assignees(ctx, f.agent, f.desk.ID)
	if err != nil {
		t.Fatalf("who works in the desk: %v", err)
	}

	var names []string
	for _, person := range people {
		names = append(names, person.Name)
	}

	if !slices.Equal(names, []string{"Ada Admin", "Agnes Agent"}) {
		t.Errorf("the desk's tickets can be given to %v; want the administrator and the agent", names)
	}

	_, err = f.tickets.Assignees(ctx, f.agent, f.hidden.ID)
	if apperror.KindOf(err) != apperror.KindNotFound {
		t.Errorf("asking who works in a queue the agent does not reach answered %v, want not found", err)
	}
}
