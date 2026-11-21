package repository

import (
	"context"

	"github.com/dennis-dko/qubitdesk/internal/domain/model"
)

// ServiceLevelRepository keeps the service levels.
type ServiceLevelRepository interface {
	GetAll(ctx context.Context) ([]*model.ServiceLevel, error)
	GetByID(ctx context.Context, id uint) (*model.ServiceLevel, error)
	Save(ctx context.Context, level *model.ServiceLevel) (*model.ServiceLevel, error)
	Update(ctx context.Context, level *model.ServiceLevel) (*model.ServiceLevel, error)
	Delete(ctx context.Context, id uint) error

	// CountUses is how many queues start their tickets with the service level,
	// and how many tickets carry it.
	CountUses(ctx context.Context, id uint) (queues, tickets int, err error)
}
