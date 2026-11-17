package memory

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/dennis-dko/go-ticket-system/internal/domain/model"
	"github.com/dennis-dko/go-ticket-system/internal/domain/repository"
)

// JobRunRepository keeps the runs of the jobs in memory, for the service tests.
type JobRunRepository struct {
	mu   sync.Mutex
	runs []model.JobRun
	next uint
}

// NewJobRunRepository creates an empty run repository.
func NewJobRunRepository() *JobRunRepository {
	return &JobRunRepository{next: 1}
}

var _ repository.JobRunRepository = (*JobRunRepository)(nil)

func (r *JobRunRepository) Record(_ context.Context, run *model.JobRun) (*model.JobRun, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	stored := *run
	stored.ID = r.next
	stored.Lines = slices.Clone(run.Lines)
	r.next++
	r.runs = append(r.runs, stored)

	copied := stored

	return &copied, nil
}

func (r *JobRunRepository) ForJob(_ context.Context, jobID uint, limit int) ([]*model.JobRun, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	runs := make([]*model.JobRun, 0)

	for i := len(r.runs) - 1; i >= 0 && len(runs) < limit; i-- {
		if r.runs[i].JobID == jobID {
			copied := r.runs[i]
			copied.Lines = slices.Clone(copied.Lines)
			runs = append(runs, &copied)
		}
	}

	return runs, nil
}

func (r *JobRunRepository) PruneBefore(_ context.Context, before time.Time) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	kept := r.runs[:0]
	removed := 0

	for _, run := range r.runs {
		if run.StartedAt.Before(before) {
			removed++

			continue
		}

		kept = append(kept, run)
	}

	r.runs = kept

	return removed, nil
}
