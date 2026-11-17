package model

import (
	"slices"
	"time"
)

// Job is what the automation does by itself, as KIX18's ticket jobs are: the
// tickets its criteria find, and the macros it runs on each of them in order,
// whenever one of its plans says so - or somebody runs it by hand. A retired
// job runs on no plan.
type Job struct {
	ID          uint
	Name        string
	Description string
	Valid       bool
	Criteria    TicketCriteria
	MacroIDs    []uint
	PlanIDs     []uint

	// LastRunAt is when the job last ran, which a time-based plan counts its
	// next run from; nil for a job that never has.
	LastRunAt *time.Time
}

// TicketCriteria are what a ticket has to be for a job to take it: in one of
// the queues, states, priorities and types named - a list naming none takes
// every one - and locked or not where that is said. KIX18 lets a filter name
// any field; these are the ones this ticket model sorts by.
type TicketCriteria struct {
	QueueIDs    []uint
	StateIDs    []uint
	PriorityIDs []uint
	TypeIDs     []uint
	Locked      *bool
}

// Empty says whether the criteria name nothing, and so would take every ticket
// there is - which a job is refused, because a step deleting tickets would
// then delete every one.
func (c TicketCriteria) Empty() bool {
	return len(c.QueueIDs) == 0 && len(c.StateIDs) == 0 && len(c.PriorityIDs) == 0 && len(c.TypeIDs) == 0 &&
		c.Locked == nil
}

// Matches says whether a ticket is one the criteria take.
func (c TicketCriteria) Matches(ticket *Ticket) bool {
	in := func(named []uint, id uint) bool { return len(named) == 0 || slices.Contains(named, id) }

	return in(c.QueueIDs, ticket.QueueID) && in(c.StateIDs, ticket.StateID) &&
		in(c.PriorityIDs, ticket.PriorityID) && in(c.TypeIDs, ticket.TypeID) &&
		(c.Locked == nil || *c.Locked == ticket.Locked)
}

// MaxJobMacros bounds the macros one job runs. Chosen rather than derived: a
// job running more macros than a form shows at once is several jobs.
const MaxJobMacros = 20
