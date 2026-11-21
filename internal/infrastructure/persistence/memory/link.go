package memory

import (
	"context"
	"slices"
	"sync"

	"github.com/dennis-dko/qubitdesk/internal/domain/model"
	"github.com/dennis-dko/qubitdesk/internal/domain/repository"
	"github.com/dennis-dko/qubitdesk/internal/support/apperror"
)

// LinkRepository keeps the links in memory. Nothing takes a record's links with
// it here, so the service it serves has to leave out an end that is gone - as
// it does for one the caller may not see.
type LinkRepository struct {
	mu    sync.Mutex
	links *store[model.Link]
}

// NewLinkRepository creates a store holding no link.
func NewLinkRepository() *LinkRepository {
	return &LinkRepository{links: newStore[model.Link]("link")}
}

var _ repository.LinkRepository = (*LinkRepository)(nil)

func (r *LinkRepository) Of(_ context.Context, object model.LinkObject, id uint) ([]*model.Link, error) {
	return slices.DeleteFunc(r.links.all(), func(link *model.Link) bool { return !link.Joins(object, id) }), nil
}

func (r *LinkRepository) GetByID(_ context.Context, id uint) (*model.Link, error) {
	return r.links.get(id)
}

func (r *LinkRepository) Create(_ context.Context, link *model.Link) (*model.Link, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, kept := range r.links.all() {
		if kept.SourceObject == link.SourceObject && kept.SourceID == link.SourceID &&
			kept.TargetObject == link.TargetObject && kept.TargetID == link.TargetID {
			return nil, apperror.Conflictf("the two are joined already").WithCode("linkTaken")
		}
	}

	stored := *link

	return r.links.add(&stored, func(l *model.Link, id uint) { l.ID = id }), nil
}

func (r *LinkRepository) Delete(_ context.Context, id uint) error {
	return r.links.delete(id)
}
