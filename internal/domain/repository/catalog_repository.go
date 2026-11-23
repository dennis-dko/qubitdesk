package repository

import (
	"context"

	"github.com/dennis-dko/qubitdesk/internal/domain/model"
)

// GeneralCatalogRepository keeps the general catalog. Nothing is deleted from
// it: a value no longer offered is made invalid.
type GeneralCatalogRepository interface {
	// Items is the values of a class by name, or of every class by class and
	// name where class is empty.
	Items(ctx context.Context, class string) ([]*model.CatalogItem, error)

	// Classes is every class a value is kept under, by name.
	Classes(ctx context.Context) ([]string, error)

	GetByID(ctx context.Context, id uint) (*model.CatalogItem, error)
	Save(ctx context.Context, item *model.CatalogItem) (*model.CatalogItem, error)
	Update(ctx context.Context, item *model.CatalogItem) (*model.CatalogItem, error)
}
