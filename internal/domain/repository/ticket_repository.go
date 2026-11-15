package repository

import (
	"context"
	"time"

	"github.com/dennis-dko/go-ticket-system/internal/domain/model"
)

// TicketRepository keeps the tickets and everything written in them. Every
// write that changes a ticket writes the history saying what changed in the same
// transaction, so the history cannot fall behind the ticket it describes.
type TicketRepository interface {
	// Create stores a new ticket, the article it was opened with, the time noted
	// with that article and the line of history saying it was opened - and its
	// number, worked out by numberOf from the id the row is given. One
	// transaction, so nobody reads a ticket without a number or without the
	// article it was opened with.
	Create(ctx context.Context, ticket *model.Ticket, first *model.Article, minutes int,
		numberOf func(id uint) string) (*model.Ticket, error)

	GetByID(ctx context.Context, id uint) (*model.Ticket, error)

	// List answers one page of the tickets a filter allows, the most recently
	// changed first, and how many it allows altogether.
	List(ctx context.Context, filter TicketFilter) ([]*model.Ticket, uint, error)

	// Update stores what changed on a ticket, with the history saying so.
	Update(ctx context.Context, ticket *model.Ticket, history []model.HistoryEntry) (*model.Ticket, error)

	// AddArticle stores an article, the time noted with it and the history
	// saying so, and marks the ticket changed at the article's moment.
	AddArticle(ctx context.Context, article *model.Article, minutes int,
		history []model.HistoryEntry) (*model.Article, error)

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

	// SetWatching records whether the account watches the ticket; watching twice
	// is watching once.
	SetWatching(ctx context.Context, ticketID, userID uint, watching bool) error

	// Watching reports whether the account watches the ticket.
	Watching(ctx context.Context, ticketID, userID uint) (bool, error)
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

	// Search narrows it to tickets whose title or number holds the words.
	Search string

	// Limit and Page choose the page: Limit tickets, the first page being 1.
	Limit int
	Page  int
}
