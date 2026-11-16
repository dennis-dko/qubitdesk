package sqldb_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/dennis-dko/go-ticket-system/internal/domain/model"
	"github.com/dennis-dko/go-ticket-system/internal/domain/repository"
	"github.com/dennis-dko/go-ticket-system/internal/infrastructure/persistence/sqldb"
	"github.com/dennis-dko/go-ticket-system/internal/support/apperror"
)

// ticketTables is a migrated database with the ticket repository over it, and
// the ids of what a ticket is sorted by as the migrations seed them.
type ticketTables struct {
	db       *sql.DB
	tickets  *sqldb.TicketRepository
	desk     uint
	open     uint
	closed   uint
	normal   uint
	unsorted uint
}

func newTicketTables(t *testing.T) ticketTables {
	t.Helper()

	db := newTestDB(t)
	tables := ticketTables{db: db, tickets: sqldb.NewTicketRepository(db, sqldb.DialectSQLite)}

	for _, read := range []struct {
		into  *uint
		query string
	}{
		{&tables.desk, "SELECT id FROM queues WHERE name = 'Service Desk'"},
		{&tables.open, "SELECT id FROM ticket_states WHERE name = 'open'"},
		{&tables.closed, "SELECT id FROM ticket_states WHERE name = 'closed'"},
		{&tables.normal, "SELECT id FROM ticket_priorities WHERE name = 'normal'"},
		{&tables.unsorted, "SELECT id FROM ticket_types WHERE name = 'unclassified'"},
	} {
		if err := db.QueryRow(read.query).Scan(read.into); err != nil {
			t.Fatalf("%s: %v", read.query, err)
		}
	}

	return tables
}

func (tables ticketTables) create(t *testing.T, title string, at time.Time, minutes int) *model.Ticket {
	t.Helper()

	ticket, err := tables.tickets.Create(context.Background(), &model.Ticket{
		Title: title, QueueID: tables.desk, StateID: tables.open, PriorityID: tables.normal,
		TypeID: tables.unsorted, Channel: model.ChannelPhone, CreatedAt: at, UpdatedAt: at,
	}, &model.Article{
		Sender: model.SenderAgent, Channel: model.ChannelPhone, Body: "What was said.", CreatedAt: at,
	}, minutes, func(id uint) string { return fmt.Sprintf("20261114%08d", id) })
	if err != nil {
		t.Fatalf("create %q: %v", title, err)
	}

	return ticket
}

// A ticket is created with its number, the article it was opened with, the time
// noted with that article and the line saying it was opened - all in one go - and
// reads back as it was stored.
func TestATicketIsCreatedWithItsNumberArticleTimeAndHistory(t *testing.T) {
	ctx := context.Background()
	tables := newTicketTables(t)
	at := time.Date(2026, 11, 14, 9, 30, 0, 0, time.UTC)

	ticket := tables.create(t, "Printer jams", at, 15)

	if ticket.Number != fmt.Sprintf("20261114%08d", ticket.ID) || ticket.Title != "Printer jams" ||
		!ticket.CreatedAt.Equal(at) || ticket.Channel != model.ChannelPhone {
		t.Errorf("the ticket reads back as %+v", ticket)
	}

	articles, err := tables.tickets.Articles(ctx, ticket.ID)
	if err != nil || len(articles) != 1 || articles[0].Body != "What was said." {
		t.Errorf("the ticket's articles read as %+v, %v", articles, err)
	}

	if minutes, err := tables.tickets.AccountedMinutes(ctx, ticket.ID); err != nil || minutes != 15 {
		t.Errorf("the ticket has %d minutes noted, %v; want 15", minutes, err)
	}

	history, err := tables.tickets.History(ctx, ticket.ID)
	if err != nil || len(history) != 1 || history[0].Event != model.HistoryCreated {
		t.Errorf("the ticket's history reads as %+v, %v", history, err)
	}
}

// One page of the tickets a filter allows, the most recently changed first, and
// how many it allows altogether; a search finds a title holding the words, and a
// search for a wildcard finds that character and not everything.
func TestAListingFiltersPagesAndSearchesTickets(t *testing.T) {
	ctx := context.Background()
	tables := newTicketTables(t)
	start := time.Date(2026, 11, 14, 8, 0, 0, 0, time.UTC)

	first := tables.create(t, "Printer jams", start, 0)
	tables.create(t, "Disk 100% full", start.Add(time.Hour), 0)
	last := tables.create(t, "VPN drops", start.Add(2*time.Hour), 0)

	list := func(filter repository.TicketFilter) ([]*model.Ticket, uint) {
		t.Helper()

		tickets, total, err := tables.tickets.List(ctx, filter)
		if err != nil {
			t.Fatalf("list %+v: %v", filter, err)
		}

		return tickets, total
	}

	page, total := list(repository.TicketFilter{QueueIDs: []uint{tables.desk}, Limit: 2, Page: 1})
	if total != 3 || len(page) != 2 || page[0].ID != last.ID {
		t.Errorf("the first page holds %d of %d, newest %v; want two of three, the newest first", len(page), total, page)
	}

	if rest, _ := list(repository.TicketFilter{QueueIDs: []uint{tables.desk}, Limit: 2, Page: 2}); len(rest) != 1 ||
		rest[0].ID != first.ID {
		t.Errorf("the second page holds %v, want the oldest", rest)
	}

	if none, total := list(repository.TicketFilter{Limit: 10}); len(none) != 0 || total != 0 {
		t.Errorf("a listing reaching no queue holds %d of %d; no queue is no ticket", len(none), total)
	}

	if found, _ := list(repository.TicketFilter{QueueIDs: []uint{tables.desk}, Search: "printer", Limit: 10}); len(found) != 1 ||
		found[0].ID != first.ID {
		t.Errorf("searching for printer found %v", found)
	}

	if found, _ := list(repository.TicketFilter{QueueIDs: []uint{tables.desk}, Search: "%", Limit: 10}); len(found) != 1 {
		t.Errorf("searching for %% found %d ticket(s), want only the one whose title holds it", len(found))
	}

	if found, _ := list(repository.TicketFilter{QueueIDs: []uint{tables.desk}, StateIDs: []uint{tables.closed}, Limit: 10}); len(found) != 0 {
		t.Errorf("no ticket is closed and the state filter found %d", len(found))
	}
}

// A change is stored with the history saying so; an article marks the ticket
// changed at its moment; and deleting a ticket takes everything written in it or
// about it along.
func TestATicketChangesAndGoesWithEverythingWrittenInIt(t *testing.T) {
	ctx := context.Background()
	tables := newTicketTables(t)
	at := time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)

	ticket := tables.create(t, "Slow network", at, 5)
	later := at.Add(time.Hour)

	ticket.Title = "Slow network upstairs"
	ticket.UpdatedAt = later

	changed, err := tables.tickets.Update(ctx, ticket, []model.HistoryEntry{{
		TicketID: ticket.ID, At: later, Actor: model.SenderAgent, Event: model.HistoryTitle,
		OldValue: "Slow network", NewValue: "Slow network upstairs",
	}})
	if err != nil || changed.Title != "Slow network upstairs" {
		t.Fatalf("change the title: %+v, %v", changed, err)
	}

	latest := later.Add(time.Hour)

	if _, err := tables.tickets.AddArticle(ctx, &model.Article{
		TicketID: ticket.ID, Sender: model.SenderAgent, Channel: model.ChannelNote, Body: "Rebooted the switch.",
		CreatedAt: latest,
	}, 20, nil, nil); err != nil {
		t.Fatalf("add an article: %v", err)
	}

	read, _ := tables.tickets.GetByID(ctx, ticket.ID)
	if !read.UpdatedAt.Equal(latest) {
		t.Errorf("the ticket was last changed at %v, want the article's moment %v", read.UpdatedAt, latest)
	}

	if minutes, _ := tables.tickets.AccountedMinutes(ctx, ticket.ID); minutes != 25 {
		t.Errorf("the ticket has %d minutes noted, want 25", minutes)
	}

	leaving, _ := twoAccounts(t, tables.db)

	if err := tables.tickets.SetWatching(ctx, ticket.ID, leaving.ID, true); err != nil {
		t.Fatalf("watch the ticket: %v", err)
	}

	if err := tables.tickets.SetWatching(ctx, ticket.ID, leaving.ID, true); err != nil {
		t.Fatalf("watch it twice: %v", err)
	}

	if watching, _ := tables.tickets.Watching(ctx, ticket.ID, leaving.ID); !watching {
		t.Error("the account does not watch the ticket it asked to watch")
	}

	if err := tables.tickets.Delete(ctx, ticket.ID); err != nil {
		t.Fatalf("delete the ticket: %v", err)
	}

	for _, table := range []string{"articles", "time_accounting", "ticket_history", "ticket_watchers"} {
		if left := count(t, tables.db, "SELECT COUNT(*) FROM "+table+" WHERE ticket_id = ?", ticket.ID); left != 0 {
			t.Errorf("%d row(s) of %s still belong to the deleted ticket", left, table)
		}
	}

	if _, err := tables.tickets.GetByID(ctx, ticket.ID); apperror.KindOf(err) != apperror.KindNotFound {
		t.Errorf("reading the deleted ticket answered %v, want not found", err)
	}
}
