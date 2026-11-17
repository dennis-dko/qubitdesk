package sqldb

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/dennis-dko/go-ticket-system/internal/domain/model"
	"github.com/dennis-dko/go-ticket-system/internal/domain/repository"
	"github.com/dennis-dko/go-ticket-system/internal/support/apperror"
)

// JobRepository keeps the jobs in jobs, the macros each runs in job_macros and
// the plans that say when in job_plans.
type JobRepository struct{ base }

// NewJobRepository creates the repository for the given dialect.
func NewJobRepository(db DB, dialect string) *JobRepository {
	return &JobRepository{base{db: db, dialect: dialect}}
}

var _ repository.JobRepository = (*JobRepository)(nil)

const jobColumns = "id, name, description, valid, criteria, last_run_at"

// jobCriteria is how a job's criteria are kept in its criteria column.
type jobCriteria struct {
	QueueIDs    []uint `json:"queueIds,omitempty"`
	StateIDs    []uint `json:"stateIds,omitempty"`
	PriorityIDs []uint `json:"priorityIds,omitempty"`
	TypeIDs     []uint `json:"typeIds,omitempty"`
	Locked      *bool  `json:"locked,omitempty"`
}

func scanJob(row interface{ Scan(dest ...any) error }) (*model.Job, error) {
	var (
		job      model.Job
		criteria string
		lastRun  dateTime
	)

	if err := row.Scan(&job.ID, &job.Name, &job.Description, &job.Valid, &criteria, &lastRun); err != nil {
		return nil, err
	}

	var kept jobCriteria
	if err := json.Unmarshal([]byte(criteria), &kept); err != nil {
		return nil, err
	}

	job.Criteria = model.TicketCriteria{
		QueueIDs: kept.QueueIDs, StateIDs: kept.StateIDs, PriorityIDs: kept.PriorityIDs, TypeIDs: kept.TypeIDs,
		Locked: kept.Locked,
	}

	job.LastRunAt = ptr(lastRun)

	return &job, nil
}

func encodeCriteria(criteria model.TicketCriteria) (string, error) {
	encoded, err := json.Marshal(jobCriteria{
		QueueIDs: criteria.QueueIDs, StateIDs: criteria.StateIDs, PriorityIDs: criteria.PriorityIDs,
		TypeIDs: criteria.TypeIDs, Locked: criteria.Locked,
	})

	return string(encoded), err
}

func (r *JobRepository) GetAll(ctx context.Context) ([]*model.Job, error) {
	rows, err := r.db.QueryContext(ctx, "SELECT "+jobColumns+" FROM jobs ORDER BY name")
	if err != nil {
		return nil, apperror.Internal(err)
	}
	defer func() { _ = rows.Close() }()

	jobs := make([]*model.Job, 0)

	for rows.Next() {
		job, scanErr := scanJob(rows)
		if scanErr != nil {
			return nil, apperror.Internal(scanErr)
		}

		jobs = append(jobs, job)
	}

	if err := rows.Err(); err != nil {
		return nil, apperror.Internal(err)
	}

	for _, job := range jobs {
		if err := r.fill(ctx, job); err != nil {
			return nil, err
		}
	}

	return jobs, nil
}

func (r *JobRepository) GetByID(ctx context.Context, id uint) (*model.Job, error) {
	job, err := scanJob(r.db.QueryRowContext(ctx, r.rebind("SELECT "+jobColumns+" FROM jobs WHERE id = ?"), id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, apperror.NotFound("job", strconv.FormatUint(uint64(id), 10))
	}

	if err != nil {
		return nil, apperror.Internal(err)
	}

	if err := r.fill(ctx, job); err != nil {
		return nil, err
	}

	return job, nil
}

func (r *JobRepository) Save(ctx context.Context, job *model.Job) (*model.Job, error) {
	criteria, err := encodeCriteria(job.Criteria)
	if err != nil {
		return nil, apperror.Internal(err)
	}

	var id uint

	err = r.withTx(ctx, func(tx base) error {
		newID, err := tx.insert(ctx, "INSERT INTO jobs (name, description, valid, criteria) VALUES (?, ?, ?, ?)",
			job.Name, job.Description, job.Valid, criteria)
		if err != nil {
			return translateJobErr(err, job.Name)
		}

		id = newID

		return replaceJobParts(ctx, tx, newID, job)
	})
	if err != nil {
		return nil, err
	}

	return r.GetByID(ctx, id)
}

func (r *JobRepository) Update(ctx context.Context, job *model.Job) (*model.Job, error) {
	criteria, err := encodeCriteria(job.Criteria)
	if err != nil {
		return nil, apperror.Internal(err)
	}

	err = r.withTx(ctx, func(tx base) error {
		found, err := tx.update(ctx, "jobs",
			"UPDATE jobs SET name = ?, description = ?, valid = ?, criteria = ? WHERE id = ?",
			job.ID, job.Name, job.Description, job.Valid, criteria, job.ID)
		if err != nil {
			return translateJobErr(err, job.Name)
		}

		if !found {
			return apperror.NotFound("job", strconv.FormatUint(uint64(job.ID), 10))
		}

		return replaceJobParts(ctx, tx, job.ID, job)
	})
	if err != nil {
		return nil, err
	}

	return r.GetByID(ctx, job.ID)
}

func (r *JobRepository) Delete(ctx context.Context, id uint) error {
	return r.withTx(ctx, func(tx base) error {
		for _, query := range []string{
			"DELETE FROM job_macros WHERE job_id = ?",
			"DELETE FROM job_plans WHERE job_id = ?",
		} {
			if _, err := tx.exec(ctx, query, id); err != nil {
				return apperror.Internal(err)
			}
		}

		affected, err := tx.exec(ctx, "DELETE FROM jobs WHERE id = ?", id)
		if err != nil {
			return apperror.Internal(err)
		}

		if affected == 0 {
			return apperror.NotFound("job", strconv.FormatUint(uint64(id), 10))
		}

		return nil
	})
}

// fill reads the macros a job runs, in order, and its plans.
func (r *JobRepository) fill(ctx context.Context, job *model.Job) error {
	macros, err := r.ids(ctx, "SELECT macro_id FROM job_macros WHERE job_id = ? ORDER BY position", job.ID)
	if err != nil {
		return err
	}

	plans, err := r.ids(ctx, "SELECT plan_id FROM job_plans WHERE job_id = ? ORDER BY plan_id", job.ID)
	if err != nil {
		return err
	}

	job.MacroIDs, job.PlanIDs = macros, plans

	return nil
}

func (r *JobRepository) ids(ctx context.Context, query string, id uint) ([]uint, error) {
	rows, err := r.db.QueryContext(ctx, r.rebind(query), id)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	defer func() { _ = rows.Close() }()

	ids := make([]uint, 0)

	for rows.Next() {
		var found uint
		if err := rows.Scan(&found); err != nil {
			return nil, apperror.Internal(err)
		}

		ids = append(ids, found)
	}

	if err := rows.Err(); err != nil {
		return nil, apperror.Internal(err)
	}

	return ids, nil
}

// replaceJobParts writes a job's macros and plans in place of the ones it had.
func replaceJobParts(ctx context.Context, tx base, id uint, job *model.Job) error {
	for _, query := range []string{
		"DELETE FROM job_macros WHERE job_id = ?",
		"DELETE FROM job_plans WHERE job_id = ?",
	} {
		if _, err := tx.exec(ctx, query, id); err != nil {
			return apperror.Internal(err)
		}
	}

	for position, macro := range job.MacroIDs {
		if _, err := tx.exec(ctx, "INSERT INTO job_macros (job_id, macro_id, position) VALUES (?, ?, ?)",
			id, macro, position); err != nil {
			return apperror.Internal(err)
		}
	}

	for _, plan := range job.PlanIDs {
		if _, err := tx.exec(ctx, "INSERT INTO job_plans (job_id, plan_id) VALUES (?, ?)", id, plan); err != nil {
			return apperror.Internal(err)
		}
	}

	return nil
}

func translateJobErr(err error, name string) error {
	if isUniqueViolation(err) {
		return apperror.Conflictf("a job named %q already exists", name).WithCode("jobNameTaken", name)
	}

	return apperror.Internal(err)
}
