package memory

import (
	"context"
	"slices"
	"strings"
	"sync"

	"github.com/dennis-dko/qubitdesk/internal/domain/model"
	"github.com/dennis-dko/qubitdesk/internal/domain/repository"
)

// ConfigItemRepository keeps the configuration items and their versions in
// memory, carrying the newest version onto its item as the database does.
type ConfigItemRepository struct {
	mu       sync.Mutex
	items    *store[model.ConfigItem]
	versions *store[model.ConfigItemVersion]
}

// NewConfigItemRepository creates a store holding no item.
func NewConfigItemRepository() *ConfigItemRepository {
	return &ConfigItemRepository{
		items:    newStore[model.ConfigItem]("configitem"),
		versions: newStore[model.ConfigItemVersion]("configitemversion"),
	}
}

var _ repository.ConfigItemRepository = (*ConfigItemRepository)(nil)

func (r *ConfigItemRepository) List(
	_ context.Context,
	filter repository.ConfigItemFilter,
) ([]*model.ConfigItem, uint, error) {
	search := strings.ToLower(strings.TrimSpace(filter.Search))

	items := slices.DeleteFunc(r.items.all(), func(item *model.ConfigItem) bool {
		if filter.ClassID != nil && item.ClassID != *filter.ClassID {
			return true
		}

		return search != "" && !strings.Contains(strings.ToLower(item.Name), search) &&
			!strings.Contains(item.Number, search)
	})

	slices.SortStableFunc(items, func(a, b *model.ConfigItem) int { return strings.Compare(a.Name, b.Name) })

	total := uint(len(items))
	page := max(filter.Page, 1)
	from := min((page-1)*filter.Limit, len(items))
	to := min(from+filter.Limit, len(items))

	return items[from:to], total, nil
}

func (r *ConfigItemRepository) GetByID(_ context.Context, id uint) (*model.ConfigItem, error) {
	return r.items.get(id)
}

func (r *ConfigItemRepository) Versions(_ context.Context, itemID uint) ([]*model.ConfigItemVersion, error) {
	versions := slices.DeleteFunc(r.versions.all(), func(version *model.ConfigItemVersion) bool {
		return version.ItemID != itemID
	})

	slices.Reverse(versions)

	return versions, nil
}

func (r *ConfigItemRepository) Version(_ context.Context, id uint) (*model.ConfigItemVersion, error) {
	return r.versions.get(id)
}

func (r *ConfigItemRepository) Create(
	ctx context.Context,
	item *model.ConfigItem,
	first *model.ConfigItemVersion,
	numberOf func(id uint) string,
) (*model.ConfigItem, error) {
	r.mu.Lock()

	kept := *item
	kept.ChangedAt, kept.ChangedBy = item.CreatedAt, item.CreatedBy

	stored := r.items.add(&kept, func(i *model.ConfigItem, id uint) { i.ID = id })
	stored.Number = numberOf(stored.ID)

	if _, err := r.items.update(stored.ID, stored); err != nil {
		r.mu.Unlock()

		return nil, err
	}

	r.mu.Unlock()

	version := *first
	version.ItemID = stored.ID

	return r.AddVersion(ctx, &version)
}

func (r *ConfigItemRepository) AddVersion(
	_ context.Context,
	version *model.ConfigItemVersion,
) (*model.ConfigItem, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	item, err := r.items.get(version.ItemID)
	if err != nil {
		return nil, err
	}

	stored := r.versions.add(version, func(v *model.ConfigItemVersion, id uint) { v.ID = id })

	item.Name, item.DeploymentStateID, item.IncidentStateID = version.Name, version.DeploymentStateID,
		version.IncidentStateID
	item.VersionID, item.VersionCount = stored.ID, item.VersionCount+1
	item.ChangedAt, item.ChangedBy = version.CreatedAt, version.CreatedBy

	return r.items.update(item.ID, item)
}

func (r *ConfigItemRepository) Delete(_ context.Context, id uint) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, err := r.items.get(id); err != nil {
		return err
	}

	for _, version := range r.versions.all() {
		if version.ItemID == id {
			_ = r.versions.delete(version.ID)
		}
	}

	return r.items.delete(id)
}
