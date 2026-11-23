package service_test

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dennis-dko/qubitdesk/internal/application/v1/service"
	"github.com/dennis-dko/qubitdesk/internal/domain/model"
	"github.com/dennis-dko/qubitdesk/internal/support/apperror"
)

// titlesOf is the titles of tickets in the order they are listed.
func titlesOf(tickets []*model.Ticket) []string {
	titles := make([]string, 0, len(tickets))
	for _, ticket := range tickets {
		titles = append(titles, ticket.Title)
	}

	return titles
}

// A search's days are read on the caller's own calendar - the zone they chose,
// or the installation's while they chose none - and the last day is included
// whole. A ticket opened at half past eleven in the morning UTC on the 20th of
// November is on the 20th in Berlin and already on the 21st in Auckland.
func TestASearchReadsItsDaysOnTheCallersCalendar(t *testing.T) {
	ctx := context.Background()
	f := newTicketFixture(t)
	opened := time.Date(2026, time.November, 20, 11, 30, 0, 0, time.UTC)

	f.tickets.WithClock(func() time.Time { return opened })
	f.open(t, f.agent, f.desk, "Opened at half past eleven")

	auckland := *f.agentUser
	auckland.Timezone = "Pacific/Auckland"
	inAuckland := &service.Principal{User: &auckland, Role: f.agent.Role, Permissions: f.agent.Permissions}
	day := func(d int) *time.Time {
		at := model.CalendarDay(time.Date(2026, time.November, d, 0, 0, 0, 0, time.UTC))
		return &at
	}

	for name, tc := range map[string]struct {
		caller *service.Principal
		days   service.Days
		found  bool
	}{
		"the 20th in Berlin":                 {f.agent, service.Days{First: day(20), Last: day(20)}, true},
		"the 21st in Berlin":                 {f.agent, service.Days{First: day(21), Last: day(21)}, false},
		"the 21st in Auckland":               {inAuckland, service.Days{First: day(21), Last: day(21)}, true},
		"the 20th in Auckland":               {inAuckland, service.Days{First: day(20), Last: day(20)}, false},
		"up to the 20th in Berlin":           {f.agent, service.Days{Last: day(20)}, true},
		"from the 21st on in Auckland":       {inAuckland, service.Days{First: day(21)}, true},
		"from the 21st on in Berlin":         {f.agent, service.Days{First: day(21)}, false},
		"up to the 20th, in Auckland, ended": {inAuckland, service.Days{Last: day(20)}, false},
	} {
		listed, _, err := f.tickets.ListTickets(ctx, tc.caller, service.TicketListFilter{Created: tc.days})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}

		if found := len(listed) == 1; found != tc.found {
			t.Errorf("opened on %s: found %t, want %t", name, found, tc.found)
		}
	}
}

// A ticket is overdue once a promise its service level made has run out
// unkept, judged at the moment the listing is read.
func TestASearchForOverdueTicketsJudgesThemNow(t *testing.T) {
	ctx := context.Background()
	f := newTicketFixture(t)
	opened := time.Date(2026, time.November, 20, 9, 0, 0, 0, time.UTC)

	gold, err := f.levels.Save(ctx, &model.ServiceLevel{Name: "Gold", FirstResponseMinutes: 60, Valid: true})
	if err != nil {
		t.Fatal(err)
	}

	f.tickets.WithClock(func() time.Time { return opened })
	f.open(t, f.agent, f.desk, "Not promised anything")

	f.desk.ServiceLevelID = &gold.ID
	if _, err := f.queues.Update(ctx, f.desk); err != nil {
		t.Fatal(err)
	}

	f.open(t, f.agent, f.desk, "Owed an answer within the hour")

	for _, tc := range []struct {
		after time.Duration
		want  []string
	}{
		{59 * time.Minute, nil},
		{61 * time.Minute, []string{"Owed an answer within the hour"}},
	} {
		f.tickets.WithClock(func() time.Time { return opened.Add(tc.after) })

		listed, _, err := f.tickets.ListTickets(ctx, f.agent, service.TicketListFilter{Overdue: true})
		if err != nil {
			t.Fatal(err)
		}

		if got := titlesOf(listed); !slices.Equal(got, tc.want) && len(got)+len(tc.want) > 0 {
			t.Errorf("%v after the opening, the overdue tickets are %q, want %q", tc.after, got, tc.want)
		}
	}
}

// What a search cannot answer is refused, field by field, rather than answered
// with some other list: a span that ends before it starts, more words than a
// search looks for, and an order nobody offers.
func TestASearchItCannotAnswerIsRefused(t *testing.T) {
	ctx := context.Background()
	f := newTicketFixture(t)
	day := func(d int) *time.Time {
		at := model.CalendarDay(time.Date(2026, time.November, d, 0, 0, 0, 0, time.UTC))
		return &at
	}

	for field, filter := range map[string]service.TicketListFilter{
		"createdTo": {Created: service.Days{First: day(21), Last: day(20)}},
		"changedTo": {Changed: service.Days{First: day(21), Last: day(20)}},
		"text":      {Text: strings.Repeat("word ", service.MaxTicketTextWords+1)},
		"sort":      {Sort: service.TicketListFilter{}.Sort},
	} {
		if field == "sort" {
			filter.Sort.By = "size"
		}

		_, _, err := f.tickets.ListTickets(ctx, f.agent, filter)
		if detail, ok := apperror.Detail(err); !ok || detail.Kind != apperror.KindInvalid ||
			!slices.Equal(detail.Fields, []string{field}) {
			t.Errorf("searching with a wrong %s answered %v, want it refused", field, err)
		}
	}

	words := strings.TrimSpace(strings.Repeat("word ", service.MaxTicketTextWords))
	if _, _, err := f.tickets.ListTickets(ctx, f.agent, service.TicketListFilter{Text: words}); err != nil {
		t.Errorf("searching for %d words was refused: %v", service.MaxTicketTextWords, err)
	}
}
