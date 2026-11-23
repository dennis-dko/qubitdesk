package repository

import (
	"context"

	"github.com/dennis-dko/qubitdesk/internal/domain/model"
)

// NotificationDefinitionRepository keeps the notifications the installation
// sends, each with what it says in every language it speaks.
type NotificationDefinitionRepository interface {
	GetAll(ctx context.Context) ([]*model.NotificationDefinition, error)
	GetByID(ctx context.Context, id uint) (*model.NotificationDefinition, error)
	Save(ctx context.Context, definition *model.NotificationDefinition) (*model.NotificationDefinition, error)
	Update(ctx context.Context, definition *model.NotificationDefinition) (*model.NotificationDefinition, error)
	Delete(ctx context.Context, id uint) error
}
