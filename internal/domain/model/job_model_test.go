package model

import "testing"

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
