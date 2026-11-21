package sqldb

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"

	"github.com/dennis-dko/qubitdesk/internal/domain/model"
	"github.com/dennis-dko/qubitdesk/internal/domain/repository"
	"github.com/dennis-dko/qubitdesk/internal/support/apperror"
)

// ChangeRepository keeps the changes in changes, the state machine in
// change_transitions and the matrix in change_cip.
type ChangeRepository struct{ base }

// NewChangeRepository creates the repository for the given dialect.
func NewChangeRepository(db DB, dialect string) *ChangeRepository {
	return &ChangeRepository{base{db: db, dialect: dialect}}
}

var _ repository.ChangeRepository = (*ChangeRepository)(nil)

const changeColumns = "id, number, title, description, justification, manager_id, builder_id, category_id, " +
	"impact_id, priority_id, state_id, requested_at, created_at, changed_at, created_by, changed_by"

func scanChange(row scanner) (*model.Change, error) {
	var (
		change    model.Change
		number    sql.NullString
		manager   sql.NullInt64
		builder   sql.NullInt64
		requested sql.NullTime
	)

	if err := row.Scan(&change.ID, &number, &change.Title, &change.Description, &change.Justification, &manager,
		&builder, &change.CategoryID, &change.ImpactID, &change.PriorityID, &change.StateID, &requested,
		&change.CreatedAt, &change.ChangedAt, &change.CreatedBy, &change.ChangedBy); err != nil {
		return nil, err
	}

	change.Number = number.String
	change.ManagerID = optionalID(manager)
	change.BuilderID = optionalID(builder)

	if requested.Valid {
		at := requested.Time
		change.RequestedAt = &at
	}

	return &change, nil
}

func (r *ChangeRepository) List(ctx context.Context, filter repository.ChangeFilter) ([]*model.Change, uint, error) {
	where := []string{"1 = 1"}
	args := make([]any, 0, 4)

	if len(filter.StateIDs) > 0 {
		where = append(where, "state_id IN (?"+strings.Repeat(", ?", len(filter.StateIDs)-1)+")")

		for _, state := range filter.StateIDs {
			args = append(args, state)
		}
	}

	if search := strings.TrimSpace(filter.Search); search != "" {
		where = append(where, "(LOWER(title) LIKE ? ESCAPE '!' OR number LIKE ? ESCAPE '!')")
		pattern := "%" + strings.ToLower(likeEscaped(search)) + "%"
		args = append(args, pattern, pattern)
	}

	condition := strings.Join(where, " AND ")

	var total uint

	if err := r.db.QueryRowContext(ctx, r.rebind("SELECT COUNT(*) FROM changes WHERE "+condition), args...).
		Scan(&total); err != nil {
		return nil, 0, apperror.Internal(err)
	}

	page := max(filter.Page, 1)
	args = append(args, filter.Limit, (page-1)*filter.Limit)

	rows, err := r.db.QueryContext(ctx, r.rebind("SELECT "+changeColumns+" FROM changes WHERE "+condition+
		" ORDER BY changed_at DESC, id DESC LIMIT ? OFFSET ?"), args...)
	if err != nil {
		return nil, 0, apperror.Internal(err)
	}
	defer func() { _ = rows.Close() }()

	changes := make([]*model.Change, 0, filter.Limit)

	for rows.Next() {
		change, scanErr := scanChange(rows)
		if scanErr != nil {
			return nil, 0, apperror.Internal(scanErr)
		}

		changes = append(changes, change)
	}

	if err := rows.Err(); err != nil {
		return nil, 0, apperror.Internal(err)
	}

	return changes, total, nil
}

func (r *ChangeRepository) GetByID(ctx context.Context, id uint) (*model.Change, error) {
	return r.change(ctx, r.base, id)
}

// change reads one change through the connection or the transaction given.
func (r *ChangeRepository) change(ctx context.Context, through base, id uint) (*model.Change, error) {
	change, err := scanChange(through.db.QueryRowContext(ctx,
		through.rebind("SELECT "+changeColumns+" FROM changes WHERE id = ?"), id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, apperror.NotFound("change", strconv.FormatUint(uint64(id), 10))
	}

	if err != nil {
		return nil, apperror.Internal(err)
	}

	return change, nil
}

func (r *ChangeRepository) Create(
	ctx context.Context,
	change *model.Change,
	numberOf func(id uint) string,
) (*model.Change, error) {
	var stored *model.Change

	err := r.withTx(ctx, func(tx base) error {
		id, err := tx.insert(ctx, "INSERT INTO changes (title, description, justification, manager_id, builder_id, "+
			"category_id, impact_id, priority_id, state_id, requested_at, created_at, changed_at, created_by, "+
			"changed_by) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
			change.Title, change.Description, change.Justification, change.ManagerID, change.BuilderID,
			change.CategoryID, change.ImpactID, change.PriorityID, change.StateID, change.RequestedAt,
			change.CreatedAt, change.ChangedAt, change.CreatedBy, change.ChangedBy)
		if err != nil {
			return apperror.Internal(err)
		}

		if _, err := tx.exec(ctx, "UPDATE changes SET number = ? WHERE id = ?", numberOf(id), id); err != nil {
			return apperror.Internal(err)
		}

		stored, err = r.change(ctx, tx, id)

		return err
	})
	if err != nil {
		return nil, err
	}

	return stored, nil
}

func (r *ChangeRepository) Update(ctx context.Context, change *model.Change) (*model.Change, error) {
	affected, err := r.exec(ctx, "UPDATE changes SET title = ?, description = ?, justification = ?, manager_id = ?, "+
		"builder_id = ?, category_id = ?, impact_id = ?, priority_id = ?, state_id = ?, requested_at = ?, "+
		"changed_at = ?, changed_by = ? WHERE id = ?",
		change.Title, change.Description, change.Justification, change.ManagerID, change.BuilderID,
		change.CategoryID, change.ImpactID, change.PriorityID, change.StateID, change.RequestedAt, change.ChangedAt,
		change.ChangedBy, change.ID)
	if err != nil {
		return nil, apperror.Internal(err)
	}

	if affected == 0 {
		return nil, apperror.NotFound("change", strconv.FormatUint(uint64(change.ID), 10))
	}

	return r.GetByID(ctx, change.ID)
}

func (r *ChangeRepository) Delete(ctx context.Context, id uint) error {
	return r.withTx(ctx, func(tx base) error {
		if _, err := r.change(ctx, tx, id); err != nil {
			return err
		}

		if err := deleteLinksOf(ctx, tx, model.LinkChange, id); err != nil {
			return err
		}

		if _, err := tx.exec(ctx, "DELETE FROM changes WHERE id = ?", id); err != nil {
			return apperror.Internal(err)
		}

		return nil
	})
}

func (r *ChangeRepository) Transitions(ctx context.Context) ([]model.ChangeTransition, error) {
	rows, err := r.db.QueryContext(ctx, "SELECT from_state_id, to_state_id FROM change_transitions")
	if err != nil {
		return nil, apperror.Internal(err)
	}
	defer func() { _ = rows.Close() }()

	var transitions []model.ChangeTransition

	for rows.Next() {
		var (
			from sql.NullInt64
			to   uint
		)

		if err := rows.Scan(&from, &to); err != nil {
			return nil, apperror.Internal(err)
		}

		transitions = append(transitions, model.ChangeTransition{FromID: optionalID(from), ToID: to})
	}

	if err := rows.Err(); err != nil {
		return nil, apperror.Internal(err)
	}

	return transitions, nil
}

func (r *ChangeRepository) Priority(ctx context.Context, categoryID, impactID uint) (uint, error) {
	var priority uint

	err := r.db.QueryRowContext(ctx, r.rebind("SELECT priority_id FROM change_cip WHERE category_id = ? AND "+
		"impact_id = ?"), categoryID, impactID).Scan(&priority)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, apperror.NotFound("change", "matrix")
	}

	if err != nil {
		return 0, apperror.Internal(err)
	}

	return priority, nil
}
