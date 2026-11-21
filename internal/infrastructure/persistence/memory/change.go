package memory

import (
	"cmp"
	"context"
	"slices"
	"strings"
	"sync"

	"github.com/dennis-dko/qubitdesk/internal/domain/model"
	"github.com/dennis-dko/qubitdesk/internal/domain/repository"
	"github.com/dennis-dko/qubitdesk/internal/support/apperror"
)

// ChangeRepository keeps the changes in memory, with the state machine and the
// matrix a test gives it.
type ChangeRepository struct {
	mu          sync.Mutex
	changes     *store[model.Change]
	transitions []model.ChangeTransition
	matrix      []model.CIPEntry
}

// NewChangeRepository creates a store holding no change, following the state
// machine and the matrix given.
func NewChangeRepository(transitions []model.ChangeTransition, matrix []model.CIPEntry) *ChangeRepository {
	return &ChangeRepository{changes: newStore[model.Change]("change"), transitions: transitions, matrix: matrix}
}

var _ repository.ChangeRepository = (*ChangeRepository)(nil)

func (r *ChangeRepository) List(_ context.Context, filter repository.ChangeFilter) ([]*model.Change, uint, error) {
	search := strings.ToLower(strings.TrimSpace(filter.Search))

	changes := slices.DeleteFunc(r.changes.all(), func(change *model.Change) bool {
		if len(filter.StateIDs) > 0 && !slices.Contains(filter.StateIDs, change.StateID) {
			return true
		}

		return search != "" && !strings.Contains(strings.ToLower(change.Title), search) &&
			!strings.Contains(change.Number, search)
	})

	slices.SortStableFunc(changes, func(a, b *model.Change) int {
		return cmp.Or(b.ChangedAt.Compare(a.ChangedAt), cmp.Compare(b.ID, a.ID))
	})

	total := uint(len(changes))
	page := max(filter.Page, 1)
	from := min((page-1)*filter.Limit, len(changes))
	to := min(from+filter.Limit, len(changes))

	return changes[from:to], total, nil
}

func (r *ChangeRepository) GetByID(_ context.Context, id uint) (*model.Change, error) {
	return r.changes.get(id)
}

func (r *ChangeRepository) Create(
	_ context.Context,
	change *model.Change,
	numberOf func(id uint) string,
) (*model.Change, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	kept := *change
	stored := r.changes.add(&kept, func(c *model.Change, id uint) { c.ID = id })
	stored.Number = numberOf(stored.ID)

	return r.changes.update(stored.ID, stored)
}

func (r *ChangeRepository) Update(_ context.Context, change *model.Change) (*model.Change, error) {
	kept := *change

	return r.changes.update(change.ID, &kept)
}

func (r *ChangeRepository) Delete(_ context.Context, id uint) error {
	return r.changes.delete(id)
}

func (r *ChangeRepository) Transitions(_ context.Context) ([]model.ChangeTransition, error) {
	return slices.Clone(r.transitions), nil
}

func (r *ChangeRepository) Priority(_ context.Context, categoryID, impactID uint) (uint, error) {
	for _, entry := range r.matrix {
		if entry.CategoryID == categoryID && entry.ImpactID == impactID {
			return entry.PriorityID, nil
		}
	}

	return 0, apperror.NotFound("change", "matrix")
}
