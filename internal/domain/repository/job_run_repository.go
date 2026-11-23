package repository

import (
	"context"
	"time"

	"github.com/dennis-dko/qubitdesk/internal/domain/model"
)

// JobRunRepository keeps what the runs of the jobs did.
type JobRunRepository interface {
	// Record keeps a finished run with its lines.
	Record(ctx context.Context, run *model.JobRun) (*model.JobRun, error)

	// ForJob lists a job's latest runs, the latest first, each with its lines.
	ForJob(ctx context.Context, jobID uint, limit int) ([]*model.JobRun, error)

	// PruneBefore removes the runs started before the moment given, and says
	// how many.
	PruneBefore(ctx context.Context, before time.Time) (int, error)
}
