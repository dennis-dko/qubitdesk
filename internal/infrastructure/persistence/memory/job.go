package memory

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dennis-dko/go-ticket-system/internal/domain/model"
	"github.com/dennis-dko/go-ticket-system/internal/domain/repository"
	"github.com/dennis-dko/go-ticket-system/internal/support/apperror"
)

// JobRepository keeps jobs in memory, for the service tests.
type JobRepository struct {
	mu   sync.Mutex
	jobs map[uint]model.Job
	next uint
}

// NewJobRepository creates an empty job repository.
func NewJobRepository() *JobRepository {
	return &JobRepository{jobs: map[uint]model.Job{}, next: 1}
}

var _ repository.JobRepository = (*JobRepository)(nil)

func (r *JobRepository) GetAll(_ context.Context) ([]*model.Job, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	jobs := make([]*model.Job, 0, len(r.jobs))
	for _, job := range r.jobs {
		jobs = append(jobs, copyJob(job))
	}

	slices.SortFunc(jobs, func(a, b *model.Job) int { return strings.Compare(a.Name, b.Name) })

	return jobs, nil
}

func (r *JobRepository) GetByID(_ context.Context, id uint) (*model.Job, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	job, ok := r.jobs[id]
	if !ok {
		return nil, apperror.NotFound("job", strconv.FormatUint(uint64(id), 10))
	}

	return copyJob(job), nil
}

func (r *JobRepository) Save(_ context.Context, job *model.Job) (*model.Job, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.taken(job.Name, 0) {
		return nil, apperror.Conflictf("a job named %q already exists", job.Name).WithCode("jobNameTaken", job.Name)
	}

	stored := *copyJob(*job)
	stored.ID, stored.LastRunAt = r.next, nil
	r.next++
	r.jobs[stored.ID] = stored

	return copyJob(stored), nil
}

func (r *JobRepository) Update(_ context.Context, job *model.Job) (*model.Job, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	kept, ok := r.jobs[job.ID]
	if !ok {
		return nil, apperror.NotFound("job", strconv.FormatUint(uint64(job.ID), 10))
	}

	if r.taken(job.Name, job.ID) {
		return nil, apperror.Conflictf("a job named %q already exists", job.Name).WithCode("jobNameTaken", job.Name)
	}

	stored := *copyJob(*job)
	stored.LastRunAt = kept.LastRunAt
	r.jobs[job.ID] = stored

	return copyJob(stored), nil
}

func (r *JobRepository) RanAt(_ context.Context, id uint, at time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	job, ok := r.jobs[id]
	if !ok {
		return apperror.NotFound("job", strconv.FormatUint(uint64(id), 10))
	}

	ran := at
	job.LastRunAt = &ran
	r.jobs[id] = job

	return nil
}

func (r *JobRepository) Delete(_ context.Context, id uint) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.jobs[id]; !ok {
		return apperror.NotFound("job", strconv.FormatUint(uint64(id), 10))
	}

	delete(r.jobs, id)

	return nil
}

func (r *JobRepository) taken(name string, except uint) bool {
	for id, job := range r.jobs {
		if id != except && job.Name == name {
			return true
		}
	}

	return false
}

// copyJob copies a job with lists of its own, which the caller and the table
// would otherwise share.
func copyJob(job model.Job) *model.Job {
	copied := job
	copied.MacroIDs = slices.Clone(job.MacroIDs)
	copied.PlanIDs = slices.Clone(job.PlanIDs)
	copied.Criteria.QueueIDs = slices.Clone(job.Criteria.QueueIDs)
	copied.Criteria.StateIDs = slices.Clone(job.Criteria.StateIDs)
	copied.Criteria.PriorityIDs = slices.Clone(job.Criteria.PriorityIDs)
	copied.Criteria.TypeIDs = slices.Clone(job.Criteria.TypeIDs)

	return &copied
}
