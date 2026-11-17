package model

import (
	"testing"
	"time"
)

// A job's criteria take a ticket that is in one of the values each names - a
// list naming none takes every one - and locked or not where that is said;
// criteria naming nothing at all are empty, because they would take every
// ticket there is.
func TestTicketCriteriaTakeWhatTheyName(t *testing.T) {
	locked, free := true, false
	ticket := &Ticket{QueueID: 1, StateID: 2, PriorityID: 3, TypeID: 4, Locked: true}

	for name, tc := range map[string]struct {
		criteria TicketCriteria
		takes    bool
	}{
		"its queue":            {TicketCriteria{QueueIDs: []uint{1, 9}}, true},
		"another queue":        {TicketCriteria{QueueIDs: []uint{9}}, false},
		"its state and type":   {TicketCriteria{StateIDs: []uint{2}, TypeIDs: []uint{4}}, true},
		"another priority":     {TicketCriteria{StateIDs: []uint{2}, PriorityIDs: []uint{5}}, false},
		"locked":               {TicketCriteria{Locked: &locked}, true},
		"free":                 {TicketCriteria{Locked: &free}, false},
		"nothing named at all": {TicketCriteria{}, true},
	} {
		if got := tc.criteria.Matches(ticket); got != tc.takes {
			t.Errorf("criteria naming %s take the ticket: %t, want %t", name, got, tc.takes)
		}
	}

	if !(TicketCriteria{}).Empty() || (TicketCriteria{Locked: &free}).Empty() || (TicketCriteria{TypeIDs: []uint{4}}).Empty() {
		t.Error("only criteria naming nothing at all are empty")
	}
}

// A time-based plan owes a job a run once one of its times has come on one of
// its weekdays and lies after the job's last run - a time missed while nothing
// looked is run when something does - and owes a job that has never run only a
// time that has just come.
func TestATimeBasedPlanOwesARunOnceItsTimeHasCome(t *testing.T) {
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatal(err)
	}

	mornings := ExecPlan{Type: PlanTimeBased, Valid: true, Weekdays: []time.Weekday{time.Monday}, Times: []int{8 * 60}}
	monday := func(hour, minute int) time.Time {
		return time.Date(2026, time.November, 16, hour, minute, 0, 0, berlin)
	}
	ranFriday := time.Date(2026, time.November, 13, 8, 0, 0, 0, berlin)

	for name, tc := range map[string]struct {
		job  Job
		plan ExecPlan
		now  time.Time
		owed bool
	}{
		"before its time":               {Job{LastRunAt: &ranFriday}, mornings, monday(7, 59), false},
		"its time come":                 {Job{LastRunAt: &ranFriday}, mornings, monday(8, 0), true},
		"its time missed while down":    {Job{LastRunAt: &ranFriday}, mornings, monday(11, 30), true},
		"run already since its time":    {Job{LastRunAt: ptrTo(monday(8, 0))}, mornings, monday(8, 5), false},
		"another weekday":               {Job{LastRunAt: &ranFriday}, mornings, monday(8, 0).AddDate(0, 0, 1), false},
		"never run, its time just come": {Job{}, mornings, monday(8, 0).Add(30 * time.Second), true},
		"never run, its time long past": {Job{}, mornings, monday(11, 30), false},
		"a retired plan":                {Job{LastRunAt: &ranFriday}, ExecPlan{Type: PlanTimeBased, Weekdays: mornings.Weekdays, Times: mornings.Times}, monday(8, 0), false},
		"an event plan":                 {Job{LastRunAt: &ranFriday}, ExecPlan{Type: PlanEventBased, Valid: true}, monday(8, 0), false},
	} {
		if got := tc.job.DueOn(tc.plan, tc.now, berlin); got != tc.owed {
			t.Errorf("%s: owed %t, want %t", name, got, tc.owed)
		}
	}
}

func ptrTo(at time.Time) *time.Time { return &at }

// A change raises the events its history lines say, each once and in the order
// a form offers them; the time noted raises none.
func TestAChangeRaisesTheEventsItsHistorySays(t *testing.T) {
	got := EventsOf([]HistoryEntry{
		{Event: HistoryTime}, {Event: HistoryArticle}, {Event: HistoryState}, {Event: HistoryContact},
		{Event: HistoryOrganisation}, {Event: HistoryState},
	})

	want := []TicketEvent{EventTicketStateUpdate, EventTicketCustomerUpdate, EventArticleCreate}
	if len(got) != len(want) {
		t.Fatalf("the change raised %v, want %v", got, want)
	}

	for i := range want {
		if got[i] != want[i] {
			t.Errorf("the change raised %v, want %v", got, want)
		}
	}
}
