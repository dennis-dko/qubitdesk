package repository

import (
	"context"

	"github.com/dennis-dko/go-ticket-system/internal/domain/model"
)

// ExecPlanRepository keeps the execution plans.
type ExecPlanRepository interface {
	GetAll(ctx context.Context) ([]*model.ExecPlan, error)
	GetByID(ctx context.Context, id uint) (*model.ExecPlan, error)
	Save(ctx context.Context, plan *model.ExecPlan) (*model.ExecPlan, error)
	Update(ctx context.Context, plan *model.ExecPlan) (*model.ExecPlan, error)
	Delete(ctx context.Context, id uint) error

	// CountJobs reports how many jobs the plan says when to run, so that one in
	// use is retired rather than deleted from under them.
	CountJobs(ctx context.Context, id uint) (int, error)
}
