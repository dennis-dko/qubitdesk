package repository

import (
	"context"

	"github.com/dennis-dko/qubitdesk/internal/domain/model"
)

// ChangeFilter narrows a listing of changes.
type ChangeFilter struct {
	// StateIDs narrows it to changes in these states, when given.
	StateIDs []uint

	// Search narrows it to changes whose title or number holds the words.
	Search string

	// Limit and Page choose the page: Limit changes, the first page being 1.
	Limit int
	Page  int
}

// ChangeRepository keeps the changes, OTRS's state machine for them and its
// matrix of category and impact.
type ChangeRepository interface {
	// List is one page of the changes the filter allows, the most recently
	// changed first, and how many it allows altogether.
	List(ctx context.Context, filter ChangeFilter) ([]*model.Change, uint, error)

	GetByID(ctx context.Context, id uint) (*model.Change, error)

	// Create writes a change and numbers it by numberOf from its id, in one
	// transaction.
	Create(ctx context.Context, change *model.Change, numberOf func(id uint) string) (*model.Change, error)

	Update(ctx context.Context, change *model.Change) (*model.Change, error)
	Delete(ctx context.Context, id uint) error

	// Transitions is every step the state machine allows.
	Transitions(ctx context.Context) ([]model.StateTransition, error)

	// Priority is the priority the matrix gives a category and an impact, or
	// not found where it gives none.
	Priority(ctx context.Context, categoryID, impactID uint) (uint, error)
}
