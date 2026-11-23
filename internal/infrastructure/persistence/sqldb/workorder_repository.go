package sqldb

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/dennis-dko/qubitdesk/internal/domain/model"
	"github.com/dennis-dko/qubitdesk/internal/domain/repository"
	"github.com/dennis-dko/qubitdesk/internal/support/apperror"
)

// WorkOrderRepository keeps the work orders in work_orders and their state
// machine in work_order_transitions.
type WorkOrderRepository struct{ base }

// NewWorkOrderRepository creates the repository for the given dialect.
func NewWorkOrderRepository(db DB, dialect string) *WorkOrderRepository {
	return &WorkOrderRepository{base{db: db, dialect: dialect}}
}

var _ repository.WorkOrderRepository = (*WorkOrderRepository)(nil)

const workOrderColumns = "id, change_id, title, instruction, report, state_id, type_id, agent_id, " +
	"planned_start_at, planned_end_at, actual_start_at, actual_end_at, planned_minutes, accounted_minutes, " +
	"created_at, changed_at, created_by, changed_by"

func scanWorkOrder(row scanner) (*model.WorkOrder, error) {
	var (
		order                    model.WorkOrder
		agent                    sql.NullInt64
		plannedStart, plannedEnd sql.NullTime
		actualStart, actualEnd   sql.NullTime
	)

	if err := row.Scan(&order.ID, &order.ChangeID, &order.Title, &order.Instruction, &order.Report,
		&order.StateID, &order.TypeID, &agent, &plannedStart, &plannedEnd, &actualStart, &actualEnd,
		&order.PlannedMinutes, &order.AccountedMinutes, &order.CreatedAt, &order.ChangedAt, &order.CreatedBy,
		&order.ChangedBy); err != nil {
		return nil, err
	}

	order.AgentID = optionalID(agent)
	order.PlannedStartAt, order.PlannedEndAt = moment(plannedStart), moment(plannedEnd)
	order.ActualStartAt, order.ActualEndAt = moment(actualStart), moment(actualEnd)

	return &order, nil
}

// moment is a moment that may not have been given.
func moment(at sql.NullTime) *time.Time {
	if !at.Valid {
		return nil
	}

	return &at.Time
}

func (r *WorkOrderRepository) OfChanges(ctx context.Context, changeIDs []uint) ([]*model.WorkOrder, error) {
	if len(changeIDs) == 0 {
		return []*model.WorkOrder{}, nil
	}

	args := make([]any, 0, len(changeIDs))
	for _, id := range changeIDs {
		args = append(args, id)
	}

	rows, err := r.db.QueryContext(ctx, r.rebind("SELECT "+workOrderColumns+" FROM work_orders WHERE change_id IN (?"+
		strings.Repeat(", ?", len(changeIDs)-1)+")"), args...)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	defer func() { _ = rows.Close() }()

	orders := []*model.WorkOrder{}

	for rows.Next() {
		order, scanErr := scanWorkOrder(rows)
		if scanErr != nil {
			return nil, apperror.Internal(scanErr)
		}

		orders = append(orders, order)
	}

	if err := rows.Err(); err != nil {
		return nil, apperror.Internal(err)
	}

	return orders, nil
}

func (r *WorkOrderRepository) GetByID(ctx context.Context, id uint) (*model.WorkOrder, error) {
	order, err := scanWorkOrder(r.db.QueryRowContext(ctx,
		r.rebind("SELECT "+workOrderColumns+" FROM work_orders WHERE id = ?"), id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, apperror.NotFound("workorder", strconv.FormatUint(uint64(id), 10))
	}

	if err != nil {
		return nil, apperror.Internal(err)
	}

	return order, nil
}

func (r *WorkOrderRepository) Create(ctx context.Context, order *model.WorkOrder) (*model.WorkOrder, error) {
	id, err := r.insert(ctx, "INSERT INTO work_orders (change_id, title, instruction, report, state_id, type_id, "+
		"agent_id, planned_start_at, planned_end_at, actual_start_at, actual_end_at, planned_minutes, "+
		"accounted_minutes, created_at, changed_at, created_by, changed_by) "+
		"VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		order.ChangeID, order.Title, order.Instruction, order.Report, order.StateID, order.TypeID, order.AgentID,
		order.PlannedStartAt, order.PlannedEndAt, order.ActualStartAt, order.ActualEndAt, order.PlannedMinutes,
		order.AccountedMinutes, order.CreatedAt, order.ChangedAt, order.CreatedBy, order.ChangedBy)
	if err != nil {
		return nil, apperror.Internal(err)
	}

	return r.GetByID(ctx, id)
}

func (r *WorkOrderRepository) Update(ctx context.Context, order *model.WorkOrder) (*model.WorkOrder, error) {
	affected, err := r.exec(ctx, "UPDATE work_orders SET title = ?, instruction = ?, report = ?, state_id = ?, "+
		"type_id = ?, agent_id = ?, planned_start_at = ?, planned_end_at = ?, actual_start_at = ?, "+
		"actual_end_at = ?, planned_minutes = ?, accounted_minutes = ?, changed_at = ?, changed_by = ? WHERE id = ?",
		order.Title, order.Instruction, order.Report, order.StateID, order.TypeID, order.AgentID,
		order.PlannedStartAt, order.PlannedEndAt, order.ActualStartAt, order.ActualEndAt, order.PlannedMinutes,
		order.AccountedMinutes, order.ChangedAt, order.ChangedBy, order.ID)
	if err != nil {
		return nil, apperror.Internal(err)
	}

	if affected == 0 {
		return nil, apperror.NotFound("workorder", strconv.FormatUint(uint64(order.ID), 10))
	}

	return r.GetByID(ctx, order.ID)
}

func (r *WorkOrderRepository) Delete(ctx context.Context, id uint) error {
	affected, err := r.exec(ctx, "DELETE FROM work_orders WHERE id = ?", id)
	if err != nil {
		return apperror.Internal(err)
	}

	if affected == 0 {
		return apperror.NotFound("workorder", strconv.FormatUint(uint64(id), 10))
	}

	return nil
}

func (r *WorkOrderRepository) Transitions(ctx context.Context) ([]model.StateTransition, error) {
	rows, err := r.db.QueryContext(ctx, "SELECT from_state_id, to_state_id FROM work_order_transitions")
	if err != nil {
		return nil, apperror.Internal(err)
	}
	defer func() { _ = rows.Close() }()

	var transitions []model.StateTransition

	for rows.Next() {
		var (
			from sql.NullInt64
			to   uint
		)

		if err := rows.Scan(&from, &to); err != nil {
			return nil, apperror.Internal(err)
		}

		transitions = append(transitions, model.StateTransition{FromID: optionalID(from), ToID: to})
	}

	if err := rows.Err(); err != nil {
		return nil, apperror.Internal(err)
	}

	return transitions, nil
}
