package repository

import (
	"context"

	"github.com/dennis-dko/go-ticket-system/internal/domain/model"
)

// JobRepository keeps the jobs, each with the macros it runs in order and the
// plans that say when; saving a job writes both in place of the ones it had,
// and leaves when it last ran as it was.
type JobRepository interface {
	GetAll(ctx context.Context) ([]*model.Job, error)
	GetByID(ctx context.Context, id uint) (*model.Job, error)
	Save(ctx context.Context, job *model.Job) (*model.Job, error)
	Update(ctx context.Context, job *model.Job) (*model.Job, error)
	Delete(ctx context.Context, id uint) error
}
