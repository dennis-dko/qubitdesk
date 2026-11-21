package memory

import (
	"context"
	"slices"
	"strconv"
	"sync"

	"github.com/dennis-dko/go-ticket-system/internal/domain/model"
	"github.com/dennis-dko/go-ticket-system/internal/domain/repository"
	"github.com/dennis-dko/go-ticket-system/internal/support/apperror"
)

// ClassDefinitionRepository keeps the classes' definitions in memory, numbering
// each class's versions as the database does.
type ClassDefinitionRepository struct {
	mu          sync.Mutex
	definitions *store[model.ClassDefinition]
}

// NewClassDefinitionRepository creates a store holding no definition.
func NewClassDefinitionRepository() *ClassDefinitionRepository {
	return &ClassDefinitionRepository{definitions: newStore[model.ClassDefinition]("classdefinition")}
}

var _ repository.ClassDefinitionRepository = (*ClassDefinitionRepository)(nil)

func (r *ClassDefinitionRepository) Current(ctx context.Context, classID uint) (*model.ClassDefinition, error) {
	versions, _ := r.Versions(ctx, classID)
	if len(versions) == 0 {
		return nil, apperror.NotFound("classdefinition", strconv.FormatUint(uint64(classID), 10))
	}

	return versions[0], nil
}

func (r *ClassDefinitionRepository) Versions(_ context.Context, classID uint) ([]*model.ClassDefinition, error) {
	versions := slices.DeleteFunc(r.definitions.all(), func(definition *model.ClassDefinition) bool {
		return definition.ClassID != classID
	})

	slices.SortFunc(versions, func(a, b *model.ClassDefinition) int { return b.Version - a.Version })

	return versions, nil
}

func (r *ClassDefinitionRepository) Add(
	ctx context.Context,
	definition *model.ClassDefinition,
) (*model.ClassDefinition, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	kept := *definition
	kept.Version = 1

	if versions, _ := r.Versions(ctx, definition.ClassID); len(versions) > 0 {
		kept.Version = versions[0].Version + 1
	}

	return r.definitions.add(&kept, func(d *model.ClassDefinition, id uint) { d.ID = id }), nil
}
