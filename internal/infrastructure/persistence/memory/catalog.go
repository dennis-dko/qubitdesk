package memory

import (
	"context"
	"slices"
	"strings"

	"github.com/dennis-dko/go-ticket-system/internal/domain/model"
	"github.com/dennis-dko/go-ticket-system/internal/domain/repository"
	"github.com/dennis-dko/go-ticket-system/internal/support/apperror"
)

// GeneralCatalogRepository keeps the general catalog in memory, with the
// database's rule that a class holds a name once.
type GeneralCatalogRepository struct {
	items *store[model.CatalogItem]
}

// NewGeneralCatalogRepository creates an empty general catalog.
func NewGeneralCatalogRepository() *GeneralCatalogRepository {
	return &GeneralCatalogRepository{items: newStore[model.CatalogItem]("catalogitem")}
}

var _ repository.GeneralCatalogRepository = (*GeneralCatalogRepository)(nil)

func (r *GeneralCatalogRepository) Items(_ context.Context, class string) ([]*model.CatalogItem, error) {
	items := slices.DeleteFunc(r.items.all(), func(item *model.CatalogItem) bool {
		return class != "" && item.Class != class
	})

	slices.SortFunc(items, func(a, b *model.CatalogItem) int {
		if c := strings.Compare(a.Class, b.Class); c != 0 {
			return c
		}

		return strings.Compare(a.Name, b.Name)
	})

	return items, nil
}

func (r *GeneralCatalogRepository) Classes(ctx context.Context) ([]string, error) {
	items, _ := r.Items(ctx, "")

	classes := make([]string, 0)
	for _, item := range items {
		if !slices.Contains(classes, item.Class) {
			classes = append(classes, item.Class)
		}
	}

	return classes, nil
}

func (r *GeneralCatalogRepository) GetByID(_ context.Context, id uint) (*model.CatalogItem, error) {
	return r.items.get(id)
}

func (r *GeneralCatalogRepository) Save(_ context.Context, item *model.CatalogItem) (*model.CatalogItem, error) {
	if r.taken(item) {
		return nil, catalogItemTaken(item)
	}

	return r.items.add(item, func(i *model.CatalogItem, id uint) { i.ID = id }), nil
}

func (r *GeneralCatalogRepository) Update(_ context.Context, item *model.CatalogItem) (*model.CatalogItem, error) {
	if r.taken(item) {
		return nil, catalogItemTaken(item)
	}

	return r.items.update(item.ID, item)
}

func (r *GeneralCatalogRepository) taken(item *model.CatalogItem) bool {
	return slices.ContainsFunc(r.items.all(), func(other *model.CatalogItem) bool {
		return other.ID != item.ID && other.Class == item.Class && other.Name == item.Name
	})
}

func catalogItemTaken(item *model.CatalogItem) error {
	return apperror.Conflictf("%q already holds %q", item.Class, item.Name).
		WithCode("catalogItemTaken", item.Class, item.Name)
}
