package sqldb

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/dennis-dko/go-ticket-system/internal/domain/model"
	"github.com/dennis-dko/go-ticket-system/internal/domain/repository"
	"github.com/dennis-dko/go-ticket-system/internal/support/apperror"
)

// ExecPlanRepository keeps the execution plans in exec_plans.
type ExecPlanRepository struct{ base }

// NewExecPlanRepository creates the repository for the given dialect.
func NewExecPlanRepository(db DB, dialect string) *ExecPlanRepository {
	return &ExecPlanRepository{base{db: db, dialect: dialect}}
}

var _ repository.ExecPlanRepository = (*ExecPlanRepository)(nil)

const execPlanColumns = "id, name, description, valid, type, parameters"

// planParameters is how a plan's weekdays, times and events are kept in its
// parameters column.
type planParameters struct {
	Weekdays []int    `json:"weekdays,omitempty"`
	Times    []int    `json:"times,omitempty"`
	Events   []string `json:"events,omitempty"`
}

func scanExecPlan(row interface{ Scan(dest ...any) error }) (*model.ExecPlan, error) {
	var (
		plan       model.ExecPlan
		kind       string
		parameters string
	)

	if err := row.Scan(&plan.ID, &plan.Name, &plan.Description, &plan.Valid, &kind, &parameters); err != nil {
		return nil, err
	}

	var kept planParameters
	if err := json.Unmarshal([]byte(parameters), &kept); err != nil {
		return nil, err
	}

	plan.Type = model.ExecPlanType(kind)
	plan.Weekdays = make([]time.Weekday, 0, len(kept.Weekdays))
	plan.Times = append(make([]int, 0, len(kept.Times)), kept.Times...)
	plan.Events = make([]model.TicketEvent, 0, len(kept.Events))

	for _, day := range kept.Weekdays {
		plan.Weekdays = append(plan.Weekdays, time.Weekday(day))
	}

	for _, event := range kept.Events {
		plan.Events = append(plan.Events, model.TicketEvent(event))
	}

	return &plan, nil
}

// encodePlan is a plan's parameters as they are kept.
func encodePlan(plan *model.ExecPlan) (string, error) {
	var kept planParameters

	for _, day := range plan.Weekdays {
		kept.Weekdays = append(kept.Weekdays, int(day))
	}

	kept.Times = plan.Times

	for _, event := range plan.Events {
		kept.Events = append(kept.Events, string(event))
	}

	encoded, err := json.Marshal(kept)

	return string(encoded), err
}

func (r *ExecPlanRepository) GetAll(ctx context.Context) ([]*model.ExecPlan, error) {
	rows, err := r.db.QueryContext(ctx, "SELECT "+execPlanColumns+" FROM exec_plans ORDER BY name")
	if err != nil {
		return nil, apperror.Internal(err)
	}
	defer func() { _ = rows.Close() }()

	plans := make([]*model.ExecPlan, 0)

	for rows.Next() {
		plan, scanErr := scanExecPlan(rows)
		if scanErr != nil {
			return nil, apperror.Internal(scanErr)
		}

		plans = append(plans, plan)
	}

	if err := rows.Err(); err != nil {
		return nil, apperror.Internal(err)
	}

	return plans, nil
}

func (r *ExecPlanRepository) GetByID(ctx context.Context, id uint) (*model.ExecPlan, error) {
	plan, err := scanExecPlan(r.db.QueryRowContext(ctx,
		r.rebind("SELECT "+execPlanColumns+" FROM exec_plans WHERE id = ?"), id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, apperror.NotFound("plan", strconv.FormatUint(uint64(id), 10))
	}

	if err != nil {
		return nil, apperror.Internal(err)
	}

	return plan, nil
}

func (r *ExecPlanRepository) Save(ctx context.Context, plan *model.ExecPlan) (*model.ExecPlan, error) {
	parameters, err := encodePlan(plan)
	if err != nil {
		return nil, apperror.Internal(err)
	}

	id, err := r.insert(ctx,
		"INSERT INTO exec_plans (name, description, valid, type, parameters) VALUES (?, ?, ?, ?, ?)",
		plan.Name, plan.Description, plan.Valid, string(plan.Type), parameters)
	if err != nil {
		return nil, translateExecPlanErr(err, plan.Name)
	}

	return r.GetByID(ctx, id)
}

func (r *ExecPlanRepository) Update(ctx context.Context, plan *model.ExecPlan) (*model.ExecPlan, error) {
	parameters, err := encodePlan(plan)
	if err != nil {
		return nil, apperror.Internal(err)
	}

	found, err := r.update(ctx, "exec_plans",
		"UPDATE exec_plans SET name = ?, description = ?, valid = ?, type = ?, parameters = ? WHERE id = ?",
		plan.ID, plan.Name, plan.Description, plan.Valid, string(plan.Type), parameters, plan.ID)
	if err != nil {
		return nil, translateExecPlanErr(err, plan.Name)
	}

	if !found {
		return nil, apperror.NotFound("plan", strconv.FormatUint(uint64(plan.ID), 10))
	}

	return r.GetByID(ctx, plan.ID)
}

func (r *ExecPlanRepository) Delete(ctx context.Context, id uint) error {
	affected, err := r.exec(ctx, "DELETE FROM exec_plans WHERE id = ?", id)
	if err != nil {
		return apperror.Internal(err)
	}

	if affected == 0 {
		return apperror.NotFound("plan", strconv.FormatUint(uint64(id), 10))
	}

	return nil
}

func translateExecPlanErr(err error, name string) error {
	if isUniqueViolation(err) {
		return apperror.Conflictf("an execution plan named %q already exists", name).WithCode("planNameTaken", name)
	}

	return apperror.Internal(err)
}

func (r *ExecPlanRepository) CountJobs(ctx context.Context, id uint) (int, error) {
	var count int

	if err := r.db.QueryRowContext(ctx, r.rebind("SELECT COUNT(DISTINCT job_id) FROM job_plans WHERE plan_id = ?"), id).
		Scan(&count); err != nil {
		return 0, apperror.Internal(err)
	}

	return count, nil
}
