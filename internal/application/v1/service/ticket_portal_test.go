package service_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/dennis-dko/qubitdesk/internal/application/v1/command"
	"github.com/dennis-dko/qubitdesk/internal/application/v1/service"
	"github.com/dennis-dko/qubitdesk/internal/domain/model"
	"github.com/dennis-dko/qubitdesk/internal/support/apperror"
)

// portalTickets is the ticket fixture with a customer who signs in to the portal
// as a contact of an organisation, a colleague of theirs at it, and the service
// desk offered in the portal.
type portalTickets struct {
	ticketFixture

	acme     *model.Organisation
	carl     *model.Contact
	cleo     *model.Contact
	customer *service.Principal
	stranger *service.Principal
	events   *[]model.TicketEvent
}

func newPortalTickets(t *testing.T) portalTickets {
	t.Helper()

	ctx := context.Background()
	f := portalTickets{ticketFixture: newTicketFixture(t), events: &[]model.TicketEvent{}}

	f.acme, _ = f.organisations.Save(ctx, &model.Organisation{Name: "Acme", Valid: true})
	f.carl, _ = f.contacts.Save(ctx, &model.Contact{Name: "Carl", Email: "carl@example.com", Valid: true,
		OrganisationID: &f.acme.ID})
	f.cleo, _ = f.contacts.Save(ctx, &model.Contact{Name: "Cleo", Email: "cleo@example.com", Valid: true,
		OrganisationID: &f.acme.ID})

	if linked, err := f.contacts.LinkAccount(ctx, f.carl.ID, f.ticketFixture.customer.ID); !linked || err != nil {
		t.Fatalf("link Carl's account: %v, %v", linked, err)
	}

	customerRole, _ := f.roles.GetByName(ctx, model.RoleCustomer)
	f.customer = &service.Principal{User: f.ticketFixture.customer, Role: customerRole,
		Permissions: model.CustomerPermissions()}

	unlinked, _ := f.users.Save(ctx, &model.User{Name: "Nobody's", Email: "nobody@example.com", RoleID: customerRole.ID})
	f.stranger = &service.Principal{User: unlinked, Role: customerRole, Permissions: model.CustomerPermissions()}

	f.desk.Portal = true
	if _, err := f.queues.Update(ctx, f.desk); err != nil {
		t.Fatal(err)
	}

	events := f.events
	f.tickets.WithEvents(func(_ context.Context, _ uint, raised []model.TicketEvent) {
		*events = append(*events, raised...)
	})

	return f
}

// forContact opens a ticket for the contact as the agent, with a first article
// the customer is not shown.
func (f portalTickets) forContact(t *testing.T, contact *model.Contact, title string) *model.Ticket {
	t.Helper()

	ticket, err := f.tickets.CreateTicket(context.Background(), f.agent, command.CreateTicketCommand{
		Title: ptr(title), QueueID: &f.desk.ID, Body: ptr("Noted on the phone."), ContactID: &contact.ID,
	})
	if err != nil {
		t.Fatalf("open %q: %v", title, err)
	}

	return ticket
}

func (f portalTickets) titles(t *testing.T, caller *service.Principal, finished bool) []string {
	t.Helper()

	tickets, _, err := f.tickets.PortalTickets(context.Background(), caller, service.PortalTicketFilter{Finished: finished})
	if err != nil {
		t.Fatalf("list the portal's tickets: %v", err)
	}

	titles := make([]string, 0, len(tickets))
	for _, ticket := range tickets {
		titles = append(titles, ticket.Title)
	}

	slices.Sort(titles)

	return titles
}

func (f portalTickets) setState(t *testing.T, ticket *model.Ticket, state string, pending *time.Time) {
	t.Helper()

	if _, err := f.tickets.UpdateTicket(context.Background(), f.agent, ticket.ID, command.UpdateTicketCommand{
		StateID: &f.states[state].ID, PendingUntil: pending,
	}); err != nil {
		t.Fatalf("put %q to %s: %v", ticket.Title, state, err)
	}
}

// A customer follows the tickets for their contact and no others, until the
// contact is said to see the organisation's: then the colleague's are there as
// well, named for whom they are. Finished tickets are listed apart. An account
// that signs in as no contact follows nothing, and a ticket somebody does not
// follow is not found, as one in an unreached queue is not.
func TestACustomerFollowsTheirOwnTicketsAndTheirOrganisationsWhenSaid(t *testing.T) {
	ctx := context.Background()
	f := newPortalTickets(t)

	mine := f.forContact(t, f.carl, "Printer jammed")
	theirs := f.forContact(t, f.cleo, "Badge lost")
	f.open(t, f.agent, f.desk, "Nobody's")

	if got := f.titles(t, f.customer, false); !slices.Equal(got, []string{"Printer jammed"}) {
		t.Errorf("Carl follows %v; want his own ticket alone", got)
	}

	_, err := f.tickets.PortalTicketOf(ctx, f.customer, theirs.ID)
	requireKind(t, err, apperror.KindNotFound)

	f.carl.SeesOrganisation = true
	if _, err := f.contacts.Update(ctx, f.carl); err != nil {
		t.Fatal(err)
	}

	if got := f.titles(t, f.customer, false); !slices.Equal(got, []string{"Badge lost", "Printer jammed"}) {
		t.Errorf("Carl, seeing Acme's tickets, follows %v", got)
	}

	colleague, err := f.tickets.PortalTicketOf(ctx, f.customer, theirs.ID)
	if err != nil || colleague.ContactName != "Cleo" || colleague.State != "new" {
		t.Errorf("Cleo's ticket reads %+v, %v; want it new and named for Cleo", colleague, err)
	}

	f.setState(t, mine, "closed", nil)

	if got := f.titles(t, f.customer, false); !slices.Equal(got, []string{"Badge lost"}) {
		t.Errorf("the tickets being worked on are %v once Carl's is closed", got)
	}

	if got := f.titles(t, f.customer, true); !slices.Equal(got, []string{"Printer jammed"}) {
		t.Errorf("the finished tickets are %v", got)
	}

	if got := f.titles(t, f.stranger, false); len(got) != 0 {
		t.Errorf("an account that signs in as no contact follows %v", got)
	}

	_, err = f.tickets.PortalTicketOf(ctx, f.stranger, mine.ID)
	requireKind(t, err, apperror.KindNotFound)
}

// A customer is shown only the articles meant for them, with who wrote each, and
// only the files that came with those: an internal note and its file are not
// there, and asking for the file by its number finds nothing.
func TestACustomerIsShownOnlyWhatIsMeantForThem(t *testing.T) {
	ctx := context.Background()
	f := newPortalTickets(t)
	ticket := f.forContact(t, f.carl, "Printer jammed")

	note, err := f.tickets.AddArticle(ctx, f.agent, ticket.ID, command.AddArticleCommand{Body: ptr("Toner is empty.")})
	if err != nil {
		t.Fatal(err)
	}

	answer, err := f.tickets.AddArticle(ctx, f.agent, ticket.ID, command.AddArticleCommand{
		Body: ptr("A new toner is on its way."), CustomerVisible: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	internal, err := f.tickets.AddAttachment(ctx, f.agent, ticket.ID, note.ID,
		service.AttachedFile{Name: "inventory.txt", Content: []byte("stock")})
	if err != nil {
		t.Fatal(err)
	}

	shown, err := f.tickets.AddAttachment(ctx, f.agent, ticket.ID, answer.ID,
		service.AttachedFile{Name: "tracking.txt", Content: []byte("parcel")})
	if err != nil {
		t.Fatal(err)
	}

	articles, err := f.tickets.PortalArticles(ctx, f.customer, ticket.ID)
	if err != nil {
		t.Fatalf("read the ticket's articles: %v", err)
	}

	if len(articles) != 1 || articles[0].ID != answer.ID || articles[0].AuthorName != "Agnes Agent" ||
		len(articles[0].Attachments) != 1 || articles[0].Attachments[0].ID != shown.ID {
		t.Fatalf("Carl is shown %+v; want the agent's answer alone, named, with its one file", articles)
	}

	if _, err := f.tickets.PortalAttachment(ctx, f.customer, ticket.ID, shown.ID); err != nil {
		t.Errorf("the file of the answer: %v", err)
	}

	_, err = f.tickets.PortalAttachment(ctx, f.customer, ticket.ID, internal.ID)
	requireKind(t, err, apperror.KindNotFound)
}

// A customer opens a ticket in the queue the portal offers: for their contact
// and organisation, from the portal, new, with what they wrote as its first
// article, from them and shown to them - and the events a person's change
// raises. Where the portal offers no queue it says so; where it offers two the
// customer names one, and a queue it does not offer is refused as a field.
func TestACustomerOpensATicketInTheQueueThePortalOffers(t *testing.T) {
	ctx := context.Background()
	f := newPortalTickets(t)

	opened, err := f.tickets.OpenFromPortal(ctx, f.customer, command.PortalTicketCommand{
		Title: ptr("Screen flickers"), Body: ptr("Since this morning."),
	})
	if err != nil {
		t.Fatalf("open a ticket: %v", err)
	}

	if opened.QueueID != f.desk.ID || opened.ContactID == nil || *opened.ContactID != f.carl.ID ||
		opened.OrganisationID == nil || *opened.OrganisationID != f.acme.ID ||
		opened.Channel != model.ChannelWeb || opened.StateID != f.states["new"].ID {
		t.Errorf("the ticket opened is %+v; want it new, in the desk, for Carl at Acme, from the portal", opened)
	}

	articles, _ := f.repo.Articles(ctx, opened.ID)
	if len(articles) != 1 || articles[0].Sender != model.SenderCustomer || !articles[0].CustomerVisible ||
		articles[0].AuthorID == nil || *articles[0].AuthorID != f.customer.User.ID {
		t.Errorf("its first article is %+v; want Carl's words, from the customer and shown to him", articles)
	}

	history, _ := f.repo.History(ctx, opened.ID)
	if len(history) == 0 || history[0].Actor != model.SenderCustomer {
		t.Errorf("the ticket's history begins %+v; want it opened by the customer", history)
	}

	if !slices.Contains(*f.events, model.EventTicketCreate) {
		t.Errorf("opening raised %v", *f.events)
	}

	second, _ := f.queues.Save(ctx, &model.Queue{Name: "Facilities", Valid: true, Portal: true,
		FollowUp: model.FollowUpReopen})

	_, err = f.tickets.OpenFromPortal(ctx, f.customer, command.PortalTicketCommand{
		Title: ptr("Door"), Body: ptr("It squeaks."),
	})
	requireKind(t, err, apperror.KindInvalid)

	_, err = f.tickets.OpenFromPortal(ctx, f.customer, command.PortalTicketCommand{
		Title: ptr("Door"), Body: ptr("It squeaks."), QueueID: &f.readOnly.ID,
	})
	requireKind(t, err, apperror.KindInvalid)

	if door, err := f.tickets.OpenFromPortal(ctx, f.customer, command.PortalTicketCommand{
		Title: ptr("Door"), Body: ptr("It squeaks."), QueueID: &second.ID,
	}); err != nil || door.QueueID != second.ID {
		t.Errorf("opening in the queue named answered %+v, %v", door, err)
	}

	for _, queue := range []*model.Queue{f.desk, second} {
		queue.Portal = false
		if _, err := f.queues.Update(ctx, queue); err != nil {
			t.Fatal(err)
		}
	}

	_, err = f.tickets.OpenFromPortal(ctx, f.customer, command.PortalTicketCommand{
		Title: ptr("Chair"), Body: ptr("Broken."),
	})
	refusedWith(t, err, "portalOffersNoQueue")

	_, err = f.tickets.OpenFromPortal(ctx, f.stranger, command.PortalTicketCommand{
		Title: ptr("Chair"), Body: ptr("Broken."),
	})
	refusedWith(t, err, "portalNeedsAContact")
}

// A customer's answer opens again a ticket that waits, leaves one being worked
// on as it is, and answers a finished one as its queue says: opened again,
// refused, or left as it is while the answer opens a new ticket for the same
// contact. It does not answer the ticket for its service level, and does not
// start a lock's time again.
func TestACustomersAnswerOpensWhatWaitsAndAFinishedTicketFollowsItsQueue(t *testing.T) {
	ctx := context.Background()
	f := newPortalTickets(t)
	answer := func(ticket *model.Ticket) (*model.Article, error) {
		return f.tickets.AnswerFromPortal(ctx, f.customer, ticket.ID, command.PortalAnswerCommand{Body: ptr("Any news?")})
	}

	waiting := f.forContact(t, f.carl, "Waiting")
	later := time.Now().Add(48 * time.Hour)
	f.setState(t, waiting, "pending-reminder", &later)

	if _, err := answer(waiting); err != nil {
		t.Fatalf("answer the waiting ticket: %v", err)
	}

	if now, _ := f.repo.GetByID(ctx, waiting.ID); now.StateID != f.states["open"].ID || now.PendingUntil != nil {
		t.Errorf("the waiting ticket answered is in state %d waiting until %v; want it open", now.StateID, now.PendingUntil)
	}

	if !slices.Contains(*f.events, model.EventArticleCreate) || !slices.Contains(*f.events, model.EventTicketStateUpdate) {
		t.Errorf("the answer raised %v; want the article and the state", *f.events)
	}

	fresh := f.forContact(t, f.carl, "Fresh")
	if _, err := answer(fresh); err != nil {
		t.Fatal(err)
	}

	if now, _ := f.repo.GetByID(ctx, fresh.ID); now.StateID != f.states["new"].ID || now.FirstResponseAt != nil {
		t.Errorf("a new ticket answered by its customer is in state %d, answered at %v; want it new and unanswered",
			now.StateID, now.FirstResponseAt)
	}

	closed := f.forContact(t, f.carl, "Closed")
	f.setState(t, closed, "closed", nil)

	if _, err := answer(closed); err != nil {
		t.Fatal(err)
	}

	if now, _ := f.repo.GetByID(ctx, closed.ID); now.StateID != f.states["open"].ID || now.ClosedAt != nil {
		t.Errorf("a closed ticket answered in a queue that reopens is in state %d, closed at %v", now.StateID, now.ClosedAt)
	}

	f.desk.FollowUp = model.FollowUpReject
	if _, err := f.queues.Update(ctx, f.desk); err != nil {
		t.Fatal(err)
	}

	f.setState(t, closed, "closed", nil)
	before, _ := f.repo.Articles(ctx, closed.ID)

	_, err := answer(closed)
	refusedWith(t, err, "followUpRejected")

	if after, _ := f.repo.Articles(ctx, closed.ID); len(after) != len(before) {
		t.Errorf("a refused answer was written: %d articles where there were %d", len(after), len(before))
	}

	f.desk.FollowUp = model.FollowUpNewTicket
	if _, err := f.queues.Update(ctx, f.desk); err != nil {
		t.Fatal(err)
	}

	landed, err := answer(closed)
	if err != nil {
		t.Fatalf("answer a closed ticket in a queue that opens a new one: %v", err)
	}

	opened, _ := f.repo.GetByID(ctx, landed.TicketID)
	if landed.TicketID == closed.ID || opened.ContactID == nil || *opened.ContactID != f.carl.ID ||
		opened.Title != "Closed" {
		t.Errorf("the answer landed in %+v; want a new ticket for Carl under the old title", opened)
	}

	if now, _ := f.repo.GetByID(ctx, closed.ID); now.StateID != f.states["closed"].ID {
		t.Errorf("the closed ticket moved to state %d when its answer opened a new one", now.StateID)
	}
}

// A customer attaches a file to their own words and to nobody else's: an
// agent's article in the same ticket is not found to them.
func TestACustomerAttachesFilesOnlyToTheirOwnWords(t *testing.T) {
	ctx := context.Background()
	f := newPortalTickets(t)
	ticket := f.forContact(t, f.carl, "Printer jammed")

	reply, err := f.tickets.AnswerFromPortal(ctx, f.customer, ticket.ID, command.PortalAnswerCommand{
		Body: ptr("A photo of the display."),
	})
	if err != nil {
		t.Fatal(err)
	}

	file := service.AttachedFile{Name: "display.jpg", Type: "image/jpeg", Content: []byte("jpeg")}

	if _, err := f.tickets.AttachFromPortal(ctx, f.customer, ticket.ID, reply.ID, file); err != nil {
		t.Errorf("attach to his own answer: %v", err)
	}

	agents, _ := f.repo.Articles(ctx, ticket.ID)

	_, err = f.tickets.AttachFromPortal(ctx, f.customer, ticket.ID, agents[0].ID, file)
	requireKind(t, err, apperror.KindNotFound)
}
