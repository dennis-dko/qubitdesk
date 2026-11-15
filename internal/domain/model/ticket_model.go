package model

import "time"

// Ticket is one request somebody made of the service desk, and what has become
// of it. What it is sorted by - its queue, state, priority and type - are rows an
// installation keeps, referred to here by id.
type Ticket struct {
	ID uint

	// Number is what people quote: the day the ticket was opened, in the
	// installation's zone, and its id, so it is unique and sorts by age.
	Number string

	Title      string
	QueueID    uint
	StateID    uint
	PriorityID uint
	TypeID     uint

	// OwnerID is the agent working the ticket, ResponsibleID the one answerable
	// for it; either may be nobody, and an erased account leaves both empty
	// rather than taking the ticket with it.
	OwnerID       *uint
	ResponsibleID *uint

	// ContactID is who the ticket is for, and OrganisationID the company it is
	// for - the contact's own unless the ticket says otherwise. Either may be
	// nobody, and a deleted contact leaves the ticket naming nobody.
	ContactID      *uint
	OrganisationID *uint

	// Locked keeps the ticket to its owner, so two agents do not answer it at
	// once; LockedAt is when, which the queue's unlock time is counted from.
	Locked   bool
	LockedAt *time.Time

	// Channel is how the ticket arrived.
	Channel Channel

	// CreatedBy is the account that opened the ticket, an id without a key: it
	// names nobody once that account is gone.
	CreatedBy *uint

	CreatedAt time.Time
	UpdatedAt time.Time

	// ClosedAt is when the ticket last entered a finished state; empty while it
	// is not in one.
	ClosedAt *time.Time

	// PendingUntil is the moment a ticket in a pending state waits for; empty in
	// every other state.
	PendingUntil *time.Time
}

// Channel is how something reached the service desk: a ticket's first contact,
// or one article of it.
type Channel string

const (
	// ChannelNote is written by an agent and reached nobody outside.
	ChannelNote Channel = "note"

	// ChannelPhone is a call an agent wrote down.
	ChannelPhone Channel = "phone"
)

// AgentChannels lists the channels an agent writes a ticket or an article in by
// hand. Mail arrives and leaves through the mailboxes, and the portal is the
// customer's, so neither is chosen on the staff side.
func AgentChannels() []Channel {
	return []Channel{ChannelNote, ChannelPhone}
}

// Sender is who an article came from, or who did something to a ticket. The
// customer and the application itself arrive with the portal and the mailboxes.
type Sender string

// SenderAgent is somebody on the staff side.
const SenderAgent Sender = "agent"

// Article is one thing written in a ticket: a note, a call, a message.
type Article struct {
	ID       uint
	TicketID uint
	Sender   Sender
	Channel  Channel

	// CustomerVisible says whether the customer is shown the article in the
	// portal; an internal note is not.
	CustomerVisible bool

	Subject string
	Body    string

	// AuthorID is the account that wrote the article, an id without a key, for
	// the reason a ticket's creator is.
	AuthorID *uint

	CreatedAt time.Time
}

// TimeEntry is time somebody worked on a ticket, noted once, with the article it
// was noted with where there was one. A ticket's total is the sum of these.
type TimeEntry struct {
	ID        uint
	TicketID  uint
	ArticleID *uint
	Minutes   int
	CreatedBy *uint
	CreatedAt time.Time
}

// HistoryEntry is one thing that happened to a ticket: who did it, what changed,
// and from what to what - each written as it was called at the time, because the
// history describes that moment and a value renamed since did not have its new
// name then.
type HistoryEntry struct {
	ID       uint
	TicketID uint
	At       time.Time

	// Actor is who did it, ActorID which account where it was somebody on the
	// staff side - an id without a key, for the reason a ticket's creator is.
	Actor   Sender
	ActorID *uint

	Event    HistoryEvent
	OldValue string
	NewValue string
}

// HistoryEvent is the kind of thing that happened to a ticket.
type HistoryEvent string

// The kinds of thing that happen to a ticket, each stored as its value.
const (
	HistoryCreated      HistoryEvent = "created"
	HistoryTitle        HistoryEvent = "title"
	HistoryQueue        HistoryEvent = "queue"
	HistoryState        HistoryEvent = "state"
	HistoryPriority     HistoryEvent = "priority"
	HistoryType         HistoryEvent = "type"
	HistoryOwner        HistoryEvent = "owner"
	HistoryResponsible  HistoryEvent = "responsible"
	HistoryContact      HistoryEvent = "contact"
	HistoryOrganisation HistoryEvent = "organisation"
	HistoryLock         HistoryEvent = "lock"
	HistoryPending      HistoryEvent = "pending"
	HistoryArticle      HistoryEvent = "article"
	HistoryTime         HistoryEvent = "time"
	HistoryAttachment   HistoryEvent = "attachment"
)

// HistoryEvents lists every kind of thing that happens to a ticket, which is
// what the interface has a sentence for.
func HistoryEvents() []HistoryEvent {
	return []HistoryEvent{
		HistoryCreated, HistoryTitle, HistoryQueue, HistoryState, HistoryPriority, HistoryType,
		HistoryOwner, HistoryResponsible, HistoryContact, HistoryOrganisation, HistoryLock, HistoryPending,
		HistoryArticle, HistoryTime, HistoryAttachment,
	}
}

// MaxTicketTitleLength covers tickets.title, VARCHAR(255).
const MaxTicketTitleLength = 255

// MaxArticleMinutes bounds the time noted with one article. Chosen rather than
// derived: a working day is the most one note can honestly say, and a typo of
// an extra digit is caught here rather than in somebody's invoice.
const MaxArticleMinutes = 24 * 60
