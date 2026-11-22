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

	// ContactID is who the ticket is for; OrganisationID is the contact's own
	// organisation unless it is given.
	ContactID      *uint
	OrganisationID *uint

	// ServiceLevelID is the service level the ticket is owed, where it is not
	// the one its queue starts tickets with.
	ServiceLevelID *uint

	// The article the ticket starts from.
	Channel         *string
	CustomerVisible bool
	Subject         *string
	Body            *string
	Minutes         int
}

// MergeTicketCommand folds a ticket into another: IntoID is the ticket it goes
// into, in a command of its own so that the two ids a merge takes cannot be
// handed over the wrong way round and merge the other ticket instead.
type MergeTicketCommand struct {
	IntoID uint
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

	// A contact newly given brings its organisation along unless one is given
	// too.
	ContactID         *uint
	ClearContact      bool
	OrganisationID    *uint
	ClearOrganisation bool

	// ServiceLevelID puts the ticket under another service level, counting what
	// it is owed afresh from its opening; ClearServiceLevel takes it off.
	ServiceLevelID    *uint
	ClearServiceLevel bool

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
