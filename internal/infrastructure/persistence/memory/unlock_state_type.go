package memory

import (
	"context"
	"slices"
	"sync"

	"github.com/dennis-dko/qubitdesk/internal/domain/model"
	"github.com/dennis-dko/qubitdesk/internal/domain/repository"
)

// UnlockStateTypeRepository keeps the state types a lock runs out in, in
// memory, for the service tests - seeded as the migration seeds a database.
type UnlockStateTypeRepository struct {
	mu   sync.Mutex
	kept []model.UnlockStateType
}

// NewUnlockStateTypeRepository ships new and open, as a fresh installation does.
func NewUnlockStateTypeRepository() *UnlockStateTypeRepository {
	return &UnlockStateTypeRepository{kept: []model.UnlockStateType{
		{Type: model.StateTypeNew, IsSystem: true},
		{Type: model.StateTypeOpen, IsSystem: true},
	}}
}

var _ repository.UnlockStateTypeRepository = (*UnlockStateTypeRepository)(nil)

func (r *UnlockStateTypeRepository) GetAll(_ context.Context) ([]model.UnlockStateType, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	kept := slices.Clone(r.kept)
	slices.SortFunc(kept, func(a, b model.UnlockStateType) int {
		return slices.Index(model.StateTypes(), a.Type) - slices.Index(model.StateTypes(), b.Type)
	})

	return kept, nil
}

func (r *UnlockStateTypeRepository) Replace(_ context.Context, types []model.StateType) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	kept := make([]model.UnlockStateType, 0, len(types))
	for _, entry := range r.kept {
		if entry.IsSystem {
			kept = append(kept, entry)
		}
	}

	for _, kind := range types {
		if !slices.ContainsFunc(kept, func(entry model.UnlockStateType) bool { return entry.Type == kind }) {
			kept = append(kept, model.UnlockStateType{Type: kind})
		}
	}

	r.kept = kept

	return nil
}
