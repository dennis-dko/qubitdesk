package sqldb

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/dennis-dko/qubitdesk/internal/domain/model"
	"github.com/dennis-dko/qubitdesk/internal/domain/repository"
	"github.com/dennis-dko/qubitdesk/internal/support/apperror"
)

// JobRunRepository keeps the runs of the jobs in job_runs and what they said in
// job_run_lines.
type JobRunRepository struct{ base }

// NewJobRunRepository creates the repository for the given dialect.
func NewJobRunRepository(db DB, dialect string) *JobRunRepository {
	return &JobRunRepository{base{db: db, dialect: dialect}}
}

var _ repository.JobRunRepository = (*JobRunRepository)(nil)

func (r *JobRunRepository) Record(ctx context.Context, run *model.JobRun) (*model.JobRun, error) {
	stored := *run

	err := r.withTx(ctx, func(tx base) error {
		id, err := tx.insert(ctx,
			"INSERT INTO job_runs (job_id, run_trigger, started_at, finished_at, tickets, changed, failed) "+
				"VALUES (?, ?, ?, ?, ?, ?, ?)",
			run.JobID, string(run.Trigger), run.StartedAt, run.FinishedAt, run.Tickets, run.Changed, run.Failed)
		if err != nil {
			return apperror.Internal(err)
		}

		stored.ID = id

		for _, line := range run.Lines {
			values, err := json.Marshal(line.Values)
			if err != nil || len(values) > 1000 {
				values = []byte("[]")
			}

			if _, err := tx.exec(ctx,
				"INSERT INTO job_run_lines (run_id, ticket_id, macro_id, step, code, refusal_values, message, at) "+
					"VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
				id, line.TicketID, line.MacroID, line.Step, cutTo(line.Code, 64), string(values),
				cutTo(line.Message, 1000), line.At); err != nil {
				return apperror.Internal(err)
			}
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return &stored, nil
}

func (r *JobRunRepository) ForJob(ctx context.Context, jobID uint, limit int) ([]*model.JobRun, error) {
	rows, err := r.db.QueryContext(ctx, r.rebind(
		"SELECT id, job_id, run_trigger, started_at, finished_at, tickets, changed, failed FROM job_runs "+
			"WHERE job_id = ? ORDER BY started_at DESC, id DESC LIMIT ?"), jobID, limit)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	defer func() { _ = rows.Close() }()

	runs := make([]*model.JobRun, 0)

	for rows.Next() {
		var (
			run               model.JobRun
			trigger           string
			started, finished dateTime
		)

		if err := rows.Scan(&run.ID, &run.JobID, &trigger, &started, &finished, &run.Tickets, &run.Changed,
			&run.Failed); err != nil {
			return nil, apperror.Internal(err)
		}

		run.Trigger, run.StartedAt, run.FinishedAt = model.RunTrigger(trigger), started.Time, finished.Time
		runs = append(runs, &run)
	}

	if err := rows.Err(); err != nil {
		return nil, apperror.Internal(err)
	}

	for _, run := range runs {
		if err := r.fill(ctx, run); err != nil {
			return nil, err
		}
	}

	return runs, nil
}

func (r *JobRunRepository) PruneBefore(ctx context.Context, before time.Time) (int, error) {
	var removed int64

	err := r.withTx(ctx, func(tx base) error {
		if _, err := tx.exec(ctx,
			"DELETE FROM job_run_lines WHERE run_id IN (SELECT id FROM job_runs WHERE started_at < ?)", before); err != nil {
			return apperror.Internal(err)
		}

		affected, err := tx.exec(ctx, "DELETE FROM job_runs WHERE started_at < ?", before)
		if err != nil {
			return apperror.Internal(err)
		}

		removed = affected

		return nil
	})

	return int(removed), err
}

// fill reads what a run said, in the order it said it.
func (r *JobRunRepository) fill(ctx context.Context, run *model.JobRun) error {
	rows, err := r.db.QueryContext(ctx, r.rebind(
		"SELECT ticket_id, macro_id, step, code, refusal_values, message, at FROM job_run_lines WHERE run_id = ? "+
			"ORDER BY id"), run.ID)
	if err != nil {
		return apperror.Internal(err)
	}
	defer func() { _ = rows.Close() }()

	run.Lines = make([]model.RunLine, 0)

	for rows.Next() {
		var (
			line          model.RunLine
			ticket, macro sql.NullInt64
			values        string
			at            dateTime
		)

		if err := rows.Scan(&ticket, &macro, &line.Step, &line.Code, &values, &line.Message, &at); err != nil {
			return apperror.Internal(err)
		}

		if err := json.Unmarshal([]byte(values), &line.Values); err != nil {
			line.Values = nil
		}

		line.TicketID, line.MacroID, line.At = idOf(ticket), idOf(macro), at.Time
		run.Lines = append(run.Lines, line)
	}

	return rows.Err()
}

// idOf is a nullable id as the model keeps it.
func idOf(id sql.NullInt64) *uint {
	if !id.Valid {
		return nil
	}

	value := uint(id.Int64)

	return &value
}

// cutTo keeps a value within its column, by characters rather than bytes so a
// word is not split in the middle of a letter.
func cutTo(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}

	return string(runes[:limit])
}
