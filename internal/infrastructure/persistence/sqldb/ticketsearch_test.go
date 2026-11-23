package sqldb_test

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/dennis-dko/qubitdesk/internal/domain/model"
	"github.com/dennis-dko/qubitdesk/internal/domain/repository"
)

// searchTables is a migrated database holding five tickets that differ in one
// thing each, so that every criterion of the search takes a known few of them.
type searchTables struct {
	ticketTables
	byTitle map[string]*model.Ticket
}

func newSearchTables(t *testing.T) searchTables {
	t.Helper()

	ctx := context.Background()
	tables := searchTables{ticketTables: newTicketTables(t), byTitle: map[string]*model.Ticket{}}

	var high, incident, level uint

	for _, read := range []struct {
		into  *uint
		query string
	}{
		{&high, "SELECT id FROM ticket_priorities WHERE name = 'very-high'"},
		{&incident, "SELECT id FROM ticket_types WHERE name = 'incident'"},
	} {
		if err := tables.db.QueryRow(read.query).Scan(read.into); err != nil {
			t.Fatalf("%s: %v", read.query, err)
		}
	}

	if err := tables.db.QueryRow("INSERT INTO service_levels (name) VALUES ('Gold') RETURNING id").
		Scan(&level); err != nil {
		t.Fatalf("make a service level: %v", err)
	}

	agent := uint(7)
	past := time.Date(2026, 11, 10, 8, 0, 0, 0, time.UTC)
	ahead := time.Date(2026, 11, 30, 8, 0, 0, 0, time.UTC)

	for _, seed := range []struct {
		title string
		at    time.Time
		shape func(*model.Ticket)
		says  string
	}{
		{"Plain", time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC), func(*model.Ticket) {}, "Nothing to add."},
		{"Urgent incident", time.Date(2026, 11, 15, 9, 0, 0, 0, time.UTC), func(ticket *model.Ticket) {
			ticket.PriorityID, ticket.TypeID = high, incident
		}, "The server room is flooded."},
		{"Held", time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC), func(ticket *model.Ticket) {
			ticket.ResponsibleID, ticket.Locked, ticket.LockedAt = &agent, true, &past
		}, "Nothing to add."},
		{"Late", time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC), func(ticket *model.Ticket) {
			ticket.ServiceLevelID, ticket.FirstResponseDue = &level, &past
		}, "Nothing to add."},
		{"Promised", time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC), func(ticket *model.Ticket) {
			ticket.ServiceLevelID, ticket.FirstResponseDue, ticket.SolutionDue = &level, &ahead, &past
			ticket.ClosedAt = &past
		}, "Nothing to add."},
	} {
		ticket := &model.Ticket{
			Title: seed.title, QueueID: tables.desk, StateID: tables.open, PriorityID: tables.normal,
			TypeID: tables.unsorted, Channel: model.ChannelPhone, CreatedAt: seed.at, UpdatedAt: seed.at,
		}
		seed.shape(ticket)

		created, err := tables.tickets.Create(ctx, ticket, &model.Article{
			Sender: model.SenderAgent, Channel: model.ChannelPhone, Body: seed.says, CreatedAt: seed.at,
			CustomerVisible: true,
		}, 0, func(id uint) string { return fmt.Sprintf("20261114%08d", id) })
		if err != nil {
			t.Fatalf("create %q: %v", seed.title, err)
		}

		tables.byTitle[seed.title] = created
	}

	if _, err := tables.tickets.AddArticle(ctx, &model.Article{
		TicketID: tables.byTitle["Held"].ID, Sender: model.SenderAgent, Channel: model.ChannelNote,
		Subject: "Between us", Body: "The supplier owes us a refund.",
		CreatedAt: time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC),
	}, 0, nil, nil, false, nil); err != nil {
		t.Fatalf("note the supplier: %v", err)
	}

	if err := tables.tickets.SetWatching(ctx, tables.byTitle["Plain"].ID, agent, true); err != nil {
		t.Fatalf("watch the plain ticket: %v", err)
	}

	return tables
}

// listed is the titles a filter lists, in the order it lists them.
func (tables searchTables) listed(t *testing.T, filter repository.TicketFilter) []string {
	t.Helper()

	filter.QueueIDs, filter.Limit = []uint{tables.desk}, 50

	tickets, total, err := tables.tickets.List(context.Background(), filter)
	if err != nil {
		t.Fatalf("list %+v: %v", filter, err)
	}

	titles := make([]string, 0, len(tickets))
	for _, ticket := range tickets {
		titles = append(titles, ticket.Title)
	}

	if total != uint(len(titles)) {
		t.Errorf("the listing counts %d tickets and lists %d", total, len(titles))
	}

	return titles
}

// Every criterion of the search takes the tickets it names and no other: a
// priority, a type, the agent responsible, the agent watching, a lock, a
// service level, the days a ticket was opened and changed on, words it says -
// in an internal note for whoever reads those and not for whoever does not -
// and a promise run out unkept, judged as model.Ticket.Escalated judges one.
func TestASearchTakesTheTicketsEachCriterionNames(t *testing.T) {
	tables := newSearchTables(t)

	agent, locked, free := uint(7), true, false
	level := *tables.byTitle["Late"].ServiceLevelID
	day := func(d int) *time.Time { at := time.Date(2026, 11, d, 0, 0, 0, 0, time.UTC); return &at }
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)

	for name, tc := range map[string]struct {
		filter repository.TicketFilter
		want   []string
	}{
		"a priority":       {repository.TicketFilter{PriorityIDs: []uint{tables.byTitle["Urgent incident"].PriorityID}}, []string{"Urgent incident"}},
		"a type":           {repository.TicketFilter{TypeIDs: []uint{tables.byTitle["Urgent incident"].TypeID}}, []string{"Urgent incident"}},
		"the responsible":  {repository.TicketFilter{ResponsibleID: &agent}, []string{"Held"}},
		"the watcher":      {repository.TicketFilter{WatcherID: &agent}, []string{"Plain"}},
		"locked":           {repository.TicketFilter{Locked: &locked}, []string{"Held"}},
		"free":             {repository.TicketFilter{Locked: &free}, []string{"Promised", "Late", "Urgent incident", "Plain"}},
		"a service level":  {repository.TicketFilter{ServiceLevelID: &level}, []string{"Promised", "Late"}},
		"opened in a span": {repository.TicketFilter{Created: &repository.Span{From: day(15), Before: day(17)}}, []string{"Held", "Urgent incident"}},
		"opened since":     {repository.TicketFilter{Created: &repository.Span{From: day(18)}}, []string{"Promised"}},
		"changed before":   {repository.TicketFilter{Changed: &repository.Span{Before: day(15)}}, []string{"Plain"}},
		"what it says":     {repository.TicketFilter{Text: &repository.TicketText{Words: []string{"FLOODED", "server"}}}, []string{"Urgent incident"}},
		"words apart":      {repository.TicketFilter{Text: &repository.TicketText{Words: []string{"held", "refund"}}}, []string{"Held"}},
		"a word not said":  {repository.TicketFilter{Text: &repository.TicketText{Words: []string{"flooded", "refund"}}}, nil},
		"a note, to staff": {repository.TicketFilter{Text: &repository.TicketText{Words: []string{"supplier"}}}, []string{"Held"}},
		"a note, to customers": {repository.TicketFilter{Text: &repository.TicketText{
			Words: []string{"supplier"}, CustomerArticlesOnly: true}}, nil},
		"a word to customers": {repository.TicketFilter{Text: &repository.TicketText{
			Words: []string{"flooded"}, CustomerArticlesOnly: true}}, []string{"Urgent incident"}},
		"overdue": {repository.TicketFilter{OverdueAt: &now}, []string{"Late"}},
	} {
		got := tables.listed(t, tc.filter)
		if !slices.Equal(got, tc.want) && len(got)+len(tc.want) > 0 {
			t.Errorf("searching by %s lists %q, want %q", name, got, tc.want)
		}
	}
}

// A listing is ordered by when each ticket was changed, by when it was opened
// or by how urgent it is - the most urgent first unless ascending, the older of
// two equally urgent first either way - and a tie in a moment is broken by the
// id in the same direction.
func TestASearchOrdersByWhatItIsAskedTo(t *testing.T) {
	tables := newSearchTables(t)

	for name, tc := range map[string]struct {
		sort repository.TicketSort
		want []string
	}{
		"changed last first":  {repository.TicketSort{}, []string{"Held", "Promised", "Late", "Urgent incident", "Plain"}},
		"changed first first": {repository.TicketSort{Ascending: true}, []string{"Plain", "Urgent incident", "Late", "Promised", "Held"}},
		"opened last first":   {repository.TicketSort{By: repository.SortByCreated}, []string{"Promised", "Late", "Held", "Urgent incident", "Plain"}},
		"opened first first":  {repository.TicketSort{By: repository.SortByCreated, Ascending: true}, []string{"Plain", "Urgent incident", "Held", "Late", "Promised"}},
		"most urgent first":   {repository.TicketSort{By: repository.SortByPriority}, []string{"Urgent incident", "Plain", "Held", "Late", "Promised"}},
		"least urgent first":  {repository.TicketSort{By: repository.SortByPriority, Ascending: true}, []string{"Plain", "Held", "Late", "Promised", "Urgent incident"}},
	} {
		if got := tables.listed(t, repository.TicketFilter{Sort: tc.sort}); !slices.Equal(got, tc.want) {
			t.Errorf("ordered %s, the listing is %q, want %q", name, got, tc.want)
		}
	}
}
