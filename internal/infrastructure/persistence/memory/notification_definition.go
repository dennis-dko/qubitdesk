package memory

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/dennis-dko/qubitdesk/internal/domain/model"
	"github.com/dennis-dko/qubitdesk/internal/domain/repository"
	"github.com/dennis-dko/qubitdesk/internal/support/apperror"
)

// NotificationDefinitionRepository keeps notifications in memory, for the
// service tests.
type NotificationDefinitionRepository struct {
	mu          sync.Mutex
	definitions map[uint]model.NotificationDefinition
	next        uint
}

// NewNotificationDefinitionRepository creates an empty notification repository.
func NewNotificationDefinitionRepository() *NotificationDefinitionRepository {
	return &NotificationDefinitionRepository{definitions: map[uint]model.NotificationDefinition{}, next: 1}
}

var _ repository.NotificationDefinitionRepository = (*NotificationDefinitionRepository)(nil)

func (r *NotificationDefinitionRepository) GetAll(_ context.Context) ([]*model.NotificationDefinition, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	definitions := make([]*model.NotificationDefinition, 0, len(r.definitions))
	for _, definition := range r.definitions {
		definitions = append(definitions, copyNotificationDefinition(definition))
	}

	slices.SortFunc(definitions, func(a, b *model.NotificationDefinition) int { return strings.Compare(a.Name, b.Name) })

	return definitions, nil
}

func (r *NotificationDefinitionRepository) GetByID(_ context.Context, id uint) (*model.NotificationDefinition, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	definition, ok := r.definitions[id]
	if !ok {
		return nil, apperror.NotFound("notification", strconv.FormatUint(uint64(id), 10))
	}

	return copyNotificationDefinition(definition), nil
}

func (r *NotificationDefinitionRepository) Save(
	_ context.Context,
	definition *model.NotificationDefinition,
) (*model.NotificationDefinition, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.taken(definition.Name, 0) {
		return nil, apperror.Conflictf("a notification named %q already exists", definition.Name).
			WithCode("notificationNameTaken", definition.Name)
	}

	stored := *copyNotificationDefinition(*definition)
	stored.ID = r.next
	r.next++
	r.definitions[stored.ID] = stored

	return copyNotificationDefinition(stored), nil
}

func (r *NotificationDefinitionRepository) Update(
	_ context.Context,
	definition *model.NotificationDefinition,
) (*model.NotificationDefinition, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.definitions[definition.ID]; !ok {
		return nil, apperror.NotFound("notification", strconv.FormatUint(uint64(definition.ID), 10))
	}

	if r.taken(definition.Name, definition.ID) {
		return nil, apperror.Conflictf("a notification named %q already exists", definition.Name).
			WithCode("notificationNameTaken", definition.Name)
	}

	r.definitions[definition.ID] = *copyNotificationDefinition(*definition)

	return copyNotificationDefinition(r.definitions[definition.ID]), nil
}

func (r *NotificationDefinitionRepository) Delete(_ context.Context, id uint) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.definitions[id]; !ok {
		return apperror.NotFound("notification", strconv.FormatUint(uint64(id), 10))
	}

	delete(r.definitions, id)

	return nil
}

func (r *NotificationDefinitionRepository) taken(name string, except uint) bool {
	for id, definition := range r.definitions {
		if id != except && definition.Name == name {
			return true
		}
	}

	return false
}

// copyNotificationDefinition copies a notification with lists of its own, which
// the caller and the table would otherwise share.
func copyNotificationDefinition(definition model.NotificationDefinition) *model.NotificationDefinition {
	copied := definition
	copied.Events = slices.Clone(definition.Events)
	copied.Criteria = copyCriteria(definition.Criteria)
	copied.Recipients = slices.Clone(definition.Recipients)
	copied.Messages = slices.Clone(definition.Messages)

	return &copied
}
