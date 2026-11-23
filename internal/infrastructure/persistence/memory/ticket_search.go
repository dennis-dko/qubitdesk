package memory

import (
	"cmp"
	"context"
	"slices"
	"strconv"
	"sync"

	"github.com/dennis-dko/qubitdesk/internal/domain/model"
	"github.com/dennis-dko/qubitdesk/internal/domain/repository"
	"github.com/dennis-dko/qubitdesk/internal/support/apperror"
)

// TicketSearchRepository keeps saved searches in memory.
type TicketSearchRepository struct {
	mu       sync.Mutex
	searches map[uint]model.TicketSearch
	next     uint
}

// NewTicketSearchRepository creates an empty repository.
func NewTicketSearchRepository() *TicketSearchRepository {
	return &TicketSearchRepository{searches: map[uint]model.TicketSearch{}, next: 1}
}

var _ repository.TicketSearchRepository = (*TicketSearchRepository)(nil)

func (r *TicketSearchRepository) List(_ context.Context, userID uint) ([]*model.TicketSearch, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	searches := make([]*model.TicketSearch, 0)

	for _, search := range r.searches {
		if search.UserID == userID {
			found := search
			searches = append(searches, &found)
		}
	}

	slices.SortFunc(searches, func(a, b *model.TicketSearch) int {
		if c := cmp.Compare(a.Name, b.Name); c != 0 {
			return c
		}

		return cmp.Compare(a.ID, b.ID)
	})

	return searches, nil
}

func (r *TicketSearchRepository) Save(_ context.Context, search *model.TicketSearch) (*model.TicketSearch, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	saved := *search
	saved.ID = r.next
	r.next++
	r.searches[saved.ID] = saved

	return &saved, nil
}

func (r *TicketSearchRepository) Delete(_ context.Context, id, userID uint) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if search, ok := r.searches[id]; !ok || search.UserID != userID {
		return apperror.NotFound("search", strconv.FormatUint(uint64(id), 10))
	}

	delete(r.searches, id)

	return nil
}
