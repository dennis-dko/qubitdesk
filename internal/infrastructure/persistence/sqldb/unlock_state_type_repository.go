package sqldb

import (
	"context"
	"slices"

	"github.com/dennis-dko/go-ticket-system/internal/domain/model"
	"github.com/dennis-dko/go-ticket-system/internal/domain/repository"
	"github.com/dennis-dko/go-ticket-system/internal/support/apperror"
)

// UnlockStateTypeRepository keeps the state types a lock runs out in, in
// unlock_state_types.
type UnlockStateTypeRepository struct{ base }

// NewUnlockStateTypeRepository creates the repository for the given dialect.
func NewUnlockStateTypeRepository(db DB, dialect string) *UnlockStateTypeRepository {
	return &UnlockStateTypeRepository{base{db: db, dialect: dialect}}
}

var _ repository.UnlockStateTypeRepository = (*UnlockStateTypeRepository)(nil)

func (r *UnlockStateTypeRepository) GetAll(ctx context.Context) ([]model.UnlockStateType, error) {
	rows, err := r.db.QueryContext(ctx, "SELECT state_type, is_system FROM unlock_state_types")
	if err != nil {
		return nil, apperror.Internal(err)
	}
	defer func() { _ = rows.Close() }()

	kept := make([]model.UnlockStateType, 0)

	for rows.Next() {
		var (
			kind   string
			system bool
		)

		if err := rows.Scan(&kind, &system); err != nil {
			return nil, apperror.Internal(err)
		}

		kept = append(kept, model.UnlockStateType{Type: model.StateType(kind), IsSystem: system})
	}

	if err := rows.Err(); err != nil {
		return nil, apperror.Internal(err)
	}

	return inTypeOrder(kept), nil
}

func (r *UnlockStateTypeRepository) Replace(ctx context.Context, types []model.StateType) error {
	return r.withTx(ctx, func(tx base) error {
		if _, err := tx.exec(ctx, "DELETE FROM unlock_state_types WHERE is_system = ?", false); err != nil {
			return apperror.Internal(err)
		}

		var shipped []string

		rows, err := tx.db.QueryContext(ctx, tx.rebind("SELECT state_type FROM unlock_state_types WHERE is_system = ?"), true)
		if err != nil {
			return apperror.Internal(err)
		}

		for rows.Next() {
			var kind string
			if err := rows.Scan(&kind); err != nil {
				_ = rows.Close()

				return apperror.Internal(err)
			}

			shipped = append(shipped, kind)
		}

		if err := rows.Close(); err != nil {
			return apperror.Internal(err)
		}

		for _, kind := range types {
			if slices.Contains(shipped, string(kind)) {
				continue
			}

			if _, err := tx.exec(ctx, "INSERT INTO unlock_state_types (state_type, is_system) VALUES (?, ?)",
				string(kind), false); err != nil {
				return apperror.Internal(err)
			}
		}

		return nil
	})
}

// inTypeOrder sorts state types the way model.StateTypes lists them, the order
// a ticket usually passes through them.
func inTypeOrder(kept []model.UnlockStateType) []model.UnlockStateType {
	slices.SortFunc(kept, func(a, b model.UnlockStateType) int {
		return slices.Index(model.StateTypes(), a.Type) - slices.Index(model.StateTypes(), b.Type)
	})

	return kept
}
