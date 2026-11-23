package repository

import (
	"context"
	"time"

	"github.com/dennis-dko/qubitdesk/internal/domain/model"
)

// TicketRepository keeps the tickets and everything written in them. Every
// write that changes a ticket writes the history saying what changed in the same
// transaction, so the history cannot fall behind the ticket it describes.
type TicketRepository interface {
	// Create stores a new ticket, the article it was opened with, the time noted
	// with that article and the line of history saying it was opened, by the
	// article's sender - and its number, worked out by numberOf from the id the
	// row is given. One transaction, so nobody reads a ticket without a number or
	// without the article it was opened with.
	Create(ctx context.Context, ticket *model.Ticket, first *model.Article, minutes int,
		numberOf func(id uint) string) (*model.Ticket, error)

	GetByID(ctx context.Context, id uint) (*model.Ticket, error)

	// List answers one page of the tickets a filter allows, the most recently
	// changed first, and how many it allows altogether.
	List(ctx context.Context, filter TicketFilter) ([]*model.Ticket, uint, error)

	// Update stores what changed on a ticket, with the history saying so.
	Update(ctx context.Context, ticket *model.Ticket, history []model.HistoryEntry) (*model.Ticket, error)

	// Merge folds the ticket given into the one it names as MergedInto, in one
	// transaction, as OTRS's TicketMerge does: its articles, the time noted on
	// it, its watchers and its links go to the other, the two are linked as
	// parent and child by the account given, the ticket is written as given,
	// the other is noted as changed when the ticket was, and the history lines
	// of both are written.
	Merge(ctx context.Context, merged *model.Ticket, history []model.HistoryEntry, by *uint) error

	// AddArticle stores an article, the time noted with it and the history
	// saying so, and marks the ticket changed at the article's moment.
	// firstResponse is when the article answered the ticket for the first time
	// where it did; a ticket already answered keeps the moment it was.
	// renewsLock starts the time a locked ticket's lock runs for again, at the
	// article's moment; a ticket not locked has none to start. updateDue is when
	// the next answer is owed once the article is written, written with it.
	AddArticle(ctx context.Context, article *model.Article, minutes int,
		history []model.HistoryEntry, firstResponse *time.Time, renewsLock bool,
		updateDue *time.Time) (*model.Article, error)

	// AddFollowUp stores a customer's answer with the history saying so, marks
	// the ticket changed at its moment and, where reopen is given, moves the
	// ticket into the state it names - only while the ticket is still in the
	// state the answer found it in, because somebody may have changed it since,
	// and then their change stands and only the answer is written. updateDue is
	// when the next answer is owed in the state the answer leaves the ticket in,
	// written where that state is the one it meant. It answers whether it moved
	// the ticket.
	AddFollowUp(ctx context.Context, article *model.Article, history []model.HistoryEntry,
		reopen *model.Reopening, updateDue *time.Time) (*model.Article, bool, error)

	Articles(ctx context.Context, ticketID uint) ([]*model.Article, error)

	// AddAttachment stores a file with an article and the history saying so,
	// and marks the ticket changed at that moment.
	AddAttachment(ctx context.Context, attachment *model.Attachment, at time.Time,
		history []model.HistoryEntry) (*model.Attachment, error)

	// Attachments lists the files of a ticket's articles, without their content.
	Attachments(ctx context.Context, ticketID uint) ([]*model.Attachment, error)

	// Attachment reads one file with its content and the ticket it belongs to.
	Attachment(ctx context.Context, id uint) (*model.Attachment, error)
	History(ctx context.Context, ticketID uint) ([]model.HistoryEntry, error)

	// AccountedMinutes is the time noted on the ticket altogether.
	AccountedMinutes(ctx context.Context, ticketID uint) (int, error)

	// Delete removes a ticket and everything written in it or about it.
	Delete(ctx context.Context, id uint) error

	// PendingDue lists the tickets in one of the states whose waiting moment has
	// come by now.
	PendingDue(ctx context.Context, stateIDs []uint, now time.Time) ([]*model.Ticket, error)

	// LockedTickets lists the tickets that are locked.
	LockedTickets(ctx context.Context) ([]*model.Ticket, error)

	// Matching lists the ids of the tickets the criteria take, the oldest first
	// and at most limit of them.
	Matching(ctx context.Context, criteria model.TicketCriteria, limit int) ([]uint, error)

	// EndWait writes what the housekeeping changed on a ticket whose wait is
	// over - its state, when it finished, when its next answer is owed, that it
	// waits no longer and when it changed - and with release frees its lock as
	// well. It writes only while
	// the ticket still waits in the state it was found in, for a moment that
	// has come by now, and with release only while it is still locked: somebody
	// may have changed it since it was found, and then their change stands and
	// nothing is written. It answers whether it wrote.
	EndWait(ctx context.Context, ticket *model.Ticket, from uint, release bool, now time.Time,
		history []model.HistoryEntry) (bool, error)

	// FreeLock frees, as of now, the lock of a ticket locked no later than
	// lockedBy, provided it is still locked so and in one of the states named: a
	// lock taken again or answered under since it was found, or a ticket put to
	// wait, stays as it is. It answers whether it freed it.
	FreeLock(ctx context.Context, id uint, lockedBy time.Time, working []uint, now time.Time,
		history []model.HistoryEntry) (bool, error)

	// SetWatching records whether the account watches the ticket; watching twice
	// is watching once.
	SetWatching(ctx context.Context, ticketID, userID uint, watching bool) error

	// Watching reports whether the account watches the ticket.
	Watching(ctx context.Context, ticketID, userID uint) (bool, error)

	// Watchers are the accounts that watch the ticket.
	Watchers(ctx context.Context, ticketID uint) ([]uint, error)
}

// Follower is a contact as the portal shows them tickets: those for them, and
// those for their organisation where OrganisationID names it.
type Follower struct {
	ContactID      uint
	OrganisationID *uint
}

// TicketFilter says which tickets a listing allows, and which page of them.
type TicketFilter struct {
	// QueueIDs are the queues the caller reaches. No queue is no ticket: the
	// filter never widens to "every queue" by being left empty.
	QueueIDs []uint

	// StateIDs narrows the listing to tickets in these states, when given.
	StateIDs []uint

	// OwnerID narrows it to one agent's tickets, when given.
	OwnerID *uint

	// ContactID and OrganisationID narrow it to one customer's tickets, when
	// given.
	ContactID      *uint
	OrganisationID *uint

	// Follower narrows it to the tickets a contact follows in the portal, when
	// given.
	Follower *Follower

	// Search narrows it to tickets whose title or number holds the words.
	Search string

	// PriorityIDs and TypeIDs narrow it to tickets of these priorities and of
	// these types, when given.
	PriorityIDs []uint
	TypeIDs     []uint

	// ResponsibleID narrows it to the tickets one agent is responsible for, and
	// WatcherID to the tickets one agent watches, when given.
	ResponsibleID *uint
	WatcherID     *uint

	// Locked narrows it to locked or to free tickets, when given.
	Locked *bool

	// ServiceLevelID narrows it to the tickets owed what one service level
	// promises, when given.
	ServiceLevelID *uint

	// Created and Changed narrow it to the tickets opened, and last changed, in
	// a span, when given.
	Created *Span
	Changed *Span

	// Text narrows it to the tickets that say every one of its words, when
	// given.
	Text *TicketText

	// OverdueAt narrows it to the tickets a promise has run out on unkept at that
	// moment, as model.Ticket.Escalated judges one, when given.
	OverdueAt *time.Time

	// Sort orders the listing. Its zero value puts the ticket changed last
	// first.
	Sort TicketSort

	// Limit and Page choose the page: Limit tickets, the first page being 1.
	Limit int
	Page  int
}

// Span is the moments from From up to, and not including, Before; either end
// may be open.
type Span struct {
	From   *time.Time
	Before *time.Time
}

// TicketText is a search of what tickets say. Each of Words is found, case
// aside, in the ticket's title or number or in the subject or the text of one
// of its articles - not necessarily all in the same one.
//
// CustomerArticlesOnly keeps the search to the articles a customer is shown.
// Whoever is shown only those must be searched only in those, or a word from an
// internal note would put a ticket among their results and so say what the note
// says.
type TicketText struct {
	Words                []string
	CustomerArticlesOnly bool
}

// TicketSort orders a listing of tickets; the zero value is the ticket changed
// last first. Ascending runs from the earliest moment, or from the least urgent
// ticket, and its absence the other way.
type TicketSort struct {
	By        TicketSortKey
	Ascending bool
}

// TicketSortKey is what a listing of tickets is ordered by.
type TicketSortKey string

// What a listing of tickets can be ordered by: when each was last changed,
// when it was opened, and how urgent it is by its priority's level - the most
// urgent first unless ascending, level 1 being the most urgent. A tie in
// either moment is broken by the ticket's id in the same direction, so that a
// page never shuffles between two reads; of two tickets equally urgent the one
// opened first comes first whichever way the levels run, because it has waited
// longer.
const (
	SortByChanged  TicketSortKey = ""
	SortByCreated  TicketSortKey = "created"
	SortByPriority TicketSortKey = "priority"
)
