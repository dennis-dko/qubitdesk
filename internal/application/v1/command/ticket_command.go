package command

import "time"

// CreateTicketCommand opens a ticket with the article it starts from.
//
// The queue, the title and the body are the caller's to give. What a ticket is
// sorted by may be left out, and is then what a ticket starts with: the state
// new, the priority normal, the type unclassified.
type CreateTicketCommand struct {
	Title      *string
	QueueID    *uint
	StateID    *uint
	PriorityID *uint
	TypeID     *uint

	// PendingUntil is needed when the state is a pending one.
	PendingUntil *time.Time

	OwnerID       *uint
	ResponsibleID *uint

	// The article the ticket starts from.
	Channel         *string
	CustomerVisible bool
	Subject         *string
	Body            *string
	Minutes         int
}

// UpdateTicketCommand changes a ticket; a nil field is left as it was.
//
// The owner and the responsible are emptiable, so their third state is said out
// loud, as the package says it has to be. The pending moment is not: it follows
// the state, kept while the ticket waits and dropped when it stops.
type UpdateTicketCommand struct {
	Title      *string
	QueueID    *uint
	StateID    *uint
	PriorityID *uint
	TypeID     *uint

	PendingUntil *time.Time

	OwnerID          *uint
	ClearOwner       bool
	ResponsibleID    *uint
	ClearResponsible bool

	// Locked locks the ticket to its owner, or frees it. Locking a ticket nobody
	// owns makes the caller its owner, as KIX18 does.
	Locked *bool
}

// AddArticleCommand writes an article in a ticket, with the time worked for it.
type AddArticleCommand struct {
	Channel         *string
	CustomerVisible bool
	Subject         *string
	Body            *string
	Minutes         int
}
