package repository

import (
	"context"

	"github.com/dennis-dko/go-ticket-system/internal/domain/model"
)

// ConfigItemFilter narrows a listing of configuration items.
type ConfigItemFilter struct {
	// ClassID narrows it to the items of one class, when given.
	ClassID *uint

	// Search narrows it to items whose name or number holds the words.
	Search string

	// Limit and Page choose the page: Limit items, the first page being 1.
	Limit int
	Page  int
}

// ConfigItemRepository keeps the configuration items and their versions. A
// version is written once and never changed: a change to an item is its next
// version.
type ConfigItemRepository interface {
	// List is one page of the items the filter allows, by name, and how many it
	// allows altogether.
	List(ctx context.Context, filter ConfigItemFilter) ([]*model.ConfigItem, uint, error)

	GetByID(ctx context.Context, id uint) (*model.ConfigItem, error)

	// Versions is every version of an item, the newest first.
	Versions(ctx context.Context, itemID uint) ([]*model.ConfigItemVersion, error)

	// Version is one version by its own id.
	Version(ctx context.Context, id uint) (*model.ConfigItemVersion, error)

	// Create writes an item and its first version in one transaction, the item's
	// number given by numberOf from its id, and answers the item as stored.
	Create(
		ctx context.Context,
		item *model.ConfigItem,
		first *model.ConfigItemVersion,
		numberOf func(id uint) string,
	) (*model.ConfigItem, error)

	// AddVersion writes an item's next version and carries what it says - its
	// name and its states - onto the item, in one transaction.
	AddVersion(ctx context.Context, version *model.ConfigItemVersion) (*model.ConfigItem, error)

	// Delete removes an item with every version of it.
	Delete(ctx context.Context, id uint) error
}
