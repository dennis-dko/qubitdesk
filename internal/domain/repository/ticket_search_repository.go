package repository

import (
	"context"

	"github.com/dennis-dko/qubitdesk/internal/domain/model"
)

// TicketSearchRepository keeps the searches agents saved. Every method names the
// account whose searches it reads or changes, so that one account's search is
// not found by another.
type TicketSearchRepository interface {
	// List reads an account's searches, by name.
	List(ctx context.Context, userID uint) ([]*model.TicketSearch, error)

	// Save stores a new search.
	Save(ctx context.Context, search *model.TicketSearch) (*model.TicketSearch, error)

	// Delete removes one of an account's searches; another account's is not found.
	Delete(ctx context.Context, id, userID uint) error
}
