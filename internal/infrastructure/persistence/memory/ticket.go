package memory

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dennis-dko/go-ticket-system/internal/domain/model"
	"github.com/dennis-dko/go-ticket-system/internal/domain/repository"
	"github.com/dennis-dko/go-ticket-system/internal/support/apperror"
)

// TicketRepository keeps tickets, their articles, the time noted on them, their
// history and who watches them in memory, for the service tests.
type TicketRepository struct {
	mu sync.Mutex

	tickets  map[uint]model.Ticket
	articles []model.Article
	history  []model.HistoryEntry
	time     []model.TimeEntry
	watchers map[uint]map[uint]bool

	attachments []model.Attachment

	nextTicket, nextArticle, nextHistory, nextAttachment uint
}

// NewTicketRepository creates an empty ticket repository.
func NewTicketRepository() *TicketRepository {
	return &TicketRepository{
		tickets: map[uint]model.Ticket{}, watchers: map[uint]map[uint]bool{},
		nextTicket: 1, nextArticle: 1, nextHistory: 1, nextAttachment: 1,
	}
}

var _ repository.TicketRepository = (*TicketRepository)(nil)

func (r *TicketRepository) Create(
	_ context.Context,
	ticket *model.Ticket,
	first *model.Article,
	minutes int,
	numberOf func(id uint) string,
) (*model.Ticket, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	stored := *ticket
	stored.ID = r.nextTicket
	stored.Number = numberOf(stored.ID)
	r.nextTicket++
	r.tickets[stored.ID] = stored

	article := *first
	article.TicketID = stored.ID
	r.addArticle(&article, minutes)

	r.addHistory([]model.HistoryEntry{{
		TicketID: stored.ID, At: stored.CreatedAt, Actor: first.Sender, ActorID: stored.CreatedBy,
		Event: model.HistoryCreated, NewValue: stored.Title,
	}})

	out := stored

	return &out, nil
}

func (r *TicketRepository) GetByID(_ context.Context, id uint) (*model.Ticket, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	ticket, ok := r.tickets[id]
	if !ok {
		return nil, apperror.NotFound("ticket", strconv.FormatUint(uint64(id), 10))
	}

	return &ticket, nil
}

func (r *TicketRepository) List(_ context.Context, filter repository.TicketFilter) ([]*model.Ticket, uint, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	search := strings.ToLower(strings.TrimSpace(filter.Search))
	matching := make([]*model.Ticket, 0)

	for _, ticket := range r.tickets {
		switch {
		case !slices.Contains(filter.QueueIDs, ticket.QueueID):
		case len(filter.StateIDs) > 0 && !slices.Contains(filter.StateIDs, ticket.StateID):
		case filter.OwnerID != nil && (ticket.OwnerID == nil || *ticket.OwnerID != *filter.OwnerID):
		case filter.ContactID != nil && (ticket.ContactID == nil || *ticket.ContactID != *filter.ContactID):
		case filter.OrganisationID != nil &&
			(ticket.OrganisationID == nil || *ticket.OrganisationID != *filter.OrganisationID):
		case filter.Follower != nil && !follows(filter.Follower, &ticket):
		case search != "" && !strings.Contains(strings.ToLower(ticket.Title), search) &&
			!strings.Contains(ticket.Number, search):
		default:
			found := ticket
			matching = append(matching, &found)
		}
	}

	slices.SortFunc(matching, func(a, b *model.Ticket) int {
		if c := b.UpdatedAt.Compare(a.UpdatedAt); c != 0 {
			return c
		}

		return int(b.ID) - int(a.ID)
	})

	total := uint(len(matching))
	start := (max(filter.Page, 1) - 1) * filter.Limit

	if start >= len(matching) {
		return []*model.Ticket{}, total, nil
	}

	return matching[start:min(start+filter.Limit, len(matching))], total, nil
}

func (r *TicketRepository) Update(
	_ context.Context,
	ticket *model.Ticket,
	history []model.HistoryEntry,
) (*model.Ticket, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.tickets[ticket.ID]; !ok {
		return nil, apperror.NotFound("ticket", strconv.FormatUint(uint64(ticket.ID), 10))
	}

	r.tickets[ticket.ID] = *ticket
	r.addHistory(history)

	out := *ticket

	return &out, nil
}

func (r *TicketRepository) AddArticle(
	_ context.Context,
	article *model.Article,
	minutes int,
	history []model.HistoryEntry,
	firstResponse *time.Time,
	renewsLock bool,
	updateDue *time.Time,
) (*model.Article, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	ticket, ok := r.tickets[article.TicketID]
	if !ok {
		return nil, apperror.NotFound("ticket", strconv.FormatUint(uint64(article.TicketID), 10))
	}

	ticket.UpdatedAt = article.CreatedAt
	if ticket.FirstResponseAt == nil && firstResponse != nil {
		answered := *firstResponse
		ticket.FirstResponseAt = &answered
	}

	if renewsLock && ticket.Locked {
		renewed := article.CreatedAt
		ticket.LockedAt = &renewed
	}

	ticket.UpdateDue = updateDue
	r.tickets[ticket.ID] = ticket

	stored := *article
	r.addArticle(&stored, minutes)
	r.addHistory(history)

	return &stored, nil
}

// follows says whether the ticket is one the follower is shown: for them, or
// for the organisation they follow.
func follows(follower *repository.Follower, ticket *model.Ticket) bool {
	if ticket.ContactID != nil && *ticket.ContactID == follower.ContactID {
		return true
	}

	return follower.OrganisationID != nil && ticket.OrganisationID != nil &&
		*ticket.OrganisationID == *follower.OrganisationID
}

func (r *TicketRepository) AddFollowUp(
	_ context.Context,
	article *model.Article,
	history []model.HistoryEntry,
	reopen *model.Reopening,
	updateDue *time.Time,
) (*model.Article, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	ticket, ok := r.tickets[article.TicketID]
	if !ok {
		return nil, false, apperror.NotFound("ticket", strconv.FormatUint(uint64(article.TicketID), 10))
	}

	ticket.UpdatedAt = article.CreatedAt
	entries := history
	moved := reopen != nil && ticket.StateID == reopen.From

	if moved {
		ticket.StateID = reopen.To
		ticket.PendingUntil = nil
		ticket.ClosedAt = nil
		entries = append(slices.Clone(history), reopen.History)
	}

	if reopen == nil || moved {
		ticket.UpdateDue = updateDue
	}

	r.tickets[ticket.ID] = ticket

	stored := *article
	r.addArticle(&stored, 0)
	r.addHistory(entries)

	return &stored, moved, nil
}

func (r *TicketRepository) Articles(_ context.Context, ticketID uint) ([]*model.Article, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	articles := make([]*model.Article, 0)

	for _, article := range r.articles {
		if article.TicketID == ticketID {
			found := article
			articles = append(articles, &found)
		}
	}

	return articles, nil
}

func (r *TicketRepository) History(_ context.Context, ticketID uint) ([]model.HistoryEntry, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	history := make([]model.HistoryEntry, 0)

	for _, entry := range r.history {
		if entry.TicketID == ticketID {
			history = append(history, entry)
		}
	}

	return history, nil
}

func (r *TicketRepository) AccountedMinutes(_ context.Context, ticketID uint) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	minutes := 0

	for _, entry := range r.time {
		if entry.TicketID == ticketID {
			minutes += entry.Minutes
		}
	}

	return minutes, nil
}

func (r *TicketRepository) Delete(_ context.Context, id uint) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.tickets[id]; !ok {
		return apperror.NotFound("ticket", strconv.FormatUint(uint64(id), 10))
	}

	delete(r.tickets, id)
	delete(r.watchers, id)

	r.articles = slices.DeleteFunc(r.articles, func(a model.Article) bool { return a.TicketID == id })
	r.history = slices.DeleteFunc(r.history, func(h model.HistoryEntry) bool { return h.TicketID == id })
	r.time = slices.DeleteFunc(r.time, func(e model.TimeEntry) bool { return e.TicketID == id })
	r.attachments = slices.DeleteFunc(r.attachments, func(a model.Attachment) bool { return a.TicketID == id })

	return nil
}

func (r *TicketRepository) SetWatching(_ context.Context, ticketID, userID uint, watching bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.watchers[ticketID] == nil {
		r.watchers[ticketID] = map[uint]bool{}
	}

	if watching {
		r.watchers[ticketID][userID] = true
	} else {
		delete(r.watchers[ticketID], userID)
	}

	return nil
}

func (r *TicketRepository) Watching(_ context.Context, ticketID, userID uint) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.watchers[ticketID][userID], nil
}

func (r *TicketRepository) Watchers(_ context.Context, ticketID uint) ([]uint, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	watchers := make([]uint, 0, len(r.watchers[ticketID]))
	for id := range r.watchers[ticketID] {
		watchers = append(watchers, id)
	}

	slices.Sort(watchers)

	return watchers, nil
}

// addArticle stores an article and the time noted with it; the caller holds the
// lock.
func (r *TicketRepository) addArticle(article *model.Article, minutes int) {
	article.ID = r.nextArticle
	r.nextArticle++
	r.articles = append(r.articles, *article)

	if minutes > 0 {
		id := article.ID
		r.time = append(r.time, model.TimeEntry{
			TicketID: article.TicketID, ArticleID: &id, Minutes: minutes,
			CreatedBy: article.AuthorID, CreatedAt: article.CreatedAt,
		})
	}
}

// addHistory stores history lines; the caller holds the lock.
func (r *TicketRepository) addHistory(history []model.HistoryEntry) {
	for _, entry := range history {
		entry.ID = r.nextHistory
		r.nextHistory++
		r.history = append(r.history, entry)
	}
}
