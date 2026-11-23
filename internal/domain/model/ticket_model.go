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
	// once. LockedAt is when it was locked, or last answered for the customer to
	// see while it was: what the queue's unlock time is counted from.
	Locked   bool
	LockedAt *time.Time

	// Channel is how the ticket arrived.
	Channel Channel

	// CreatedBy is the account that opened the ticket, an id without a key: it
	// names nobody once that account is gone.
	CreatedBy *uint

	// MergedInto is the ticket this one was merged into, once it was: its
	// articles, the time noted on it and its watchers went there. An id without
	// a key, like CreatedBy, because a merged ticket is the record of a merge and
	// outlives the ticket it names.
	MergedInto *uint

	CreatedAt time.Time
	UpdatedAt time.Time

	// ClosedAt is when the ticket last entered a finished state; empty while it
	// is not in one.
	ClosedAt *time.Time

	// PendingUntil is the moment a ticket in a pending state waits for; empty in
	// every other state.
	PendingUntil *time.Time

	// ServiceLevelID is the service level the ticket is owed, where it has one;
	// the moments below are counted from its opening in the level's calendar,
	// and are empty for a promise the level does not make.
	ServiceLevelID *uint

	// FirstResponseDue is when the first answer is owed, and FirstResponseAt
	// when one was given: the first article of an agent the customer sees.
	FirstResponseDue *time.Time
	FirstResponseAt  *time.Time

	// SolutionDue is when the ticket is owed a finished state.
	SolutionDue *time.Time

	// UpdateDue is when the next answer is owed - the time between answers its
	// service level promises, counted from UpdateFrom - and empty while the
	// ticket waits or is finished, which nobody owes an answer in.
	UpdateDue *time.Time
}

// Escalated says whether a promise the ticket was made has run out unkept: no
// first answer by its moment, no answer by the moment the next was owed, or not
// finished by its.
func (t Ticket) Escalated(now time.Time) bool {
	return (t.FirstResponseDue != nil && t.FirstResponseAt == nil && now.After(*t.FirstResponseDue)) ||
		(t.UpdateDue != nil && now.After(*t.UpdateDue)) ||
		(t.SolutionDue != nil && t.ClosedAt == nil && now.After(*t.SolutionDue))
}

// UpdateFrom is the moment the time between answers is counted from, as OTRS
// counts it, read from a ticket's articles oldest first: the customer's first
// article since the last answer they were shown, or that answer where the
// agent wrote last. A note nobody outside saw and what the application wrote
// count for neither, and a ticket with neither counts from its opening.
//
// From the customer's first article of the run rather than their last, because
// writing again while waiting is not a reason to be owed an answer later.
func UpdateFrom(articles []*Article, opened time.Time) time.Time {
	from, customerSince := opened, false

	for i := len(articles) - 1; i >= 0; i-- {
		article := articles[i]

		if !article.CustomerVisible {
			continue
		}

		switch article.Sender {
		case SenderCustomer:
			from, customerSince = article.CreatedAt, true
		case SenderAgent:
			if customerSince {
				return from
			}

			return article.CreatedAt
		}
	}

	return from
}

// Channel is how something reached the service desk: a ticket's first contact,
// or one article of it.
type Channel string

const (
	// ChannelNote is written by an agent and reached nobody outside.
	ChannelNote Channel = "note"

	// ChannelPhone is a call an agent wrote down.
	ChannelPhone Channel = "phone"

	// ChannelWeb is what a customer wrote in the portal.
	ChannelWeb Channel = "web"
)

// AgentChannels lists the channels an agent writes a ticket or an article in by
// hand. Mail arrives and leaves through the mailboxes, and the portal is the
// customer's, so neither is chosen on the staff side.
func AgentChannels() []Channel {
	return []Channel{ChannelNote, ChannelPhone}
}

// Sender is who an article came from, or who did something to a ticket.
type Sender string

// The senders so far: somebody on the staff side, the customer the ticket is
// for, writing in the portal, and the application itself - its scheduler moving
// a ticket whose wait is over, or freeing a lock that has run its time.
const (
	SenderAgent    Sender = "agent"
	SenderCustomer Sender = "customer"
	SenderSystem   Sender = "system"
)

// Reopening is what a customer's answer does to a ticket that waits or is
// finished: it moves it from the state the answer found it in to the one it is
// open in again, with the line of history saying so.
type Reopening struct {
	From    uint
	To      uint
	History HistoryEntry
}

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
	HistoryServiceLevel HistoryEvent = "sla"
	HistoryMerged       HistoryEvent = "merged"
)

// HistoryEvents lists every kind of thing that happens to a ticket, which is
// what the interface has a sentence for.
func HistoryEvents() []HistoryEvent {
	return []HistoryEvent{
		HistoryCreated, HistoryTitle, HistoryQueue, HistoryState, HistoryPriority, HistoryType,
		HistoryOwner, HistoryResponsible, HistoryContact, HistoryOrganisation, HistoryLock, HistoryPending,
		HistoryArticle, HistoryTime, HistoryAttachment, HistoryServiceLevel, HistoryMerged,
	}
}

// MaxTicketTitleLength covers tickets.title, VARCHAR(255).
const MaxTicketTitleLength = 255

// MaxArticleMinutes bounds the time noted with one article. Chosen rather than
// derived: a working day is the most one note can honestly say, and a typo of
// an extra digit is caught here rather than in somebody's invoice.
const MaxArticleMinutes = 24 * 60
