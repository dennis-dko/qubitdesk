package sqldb

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/dennis-dko/qubitdesk/internal/domain/model"
	"github.com/dennis-dko/qubitdesk/internal/domain/repository"
	"github.com/dennis-dko/qubitdesk/internal/support/apperror"
)

// MacroRepository keeps the macros in macros and their steps in macro_actions.
type MacroRepository struct{ base }

// NewMacroRepository creates the repository for the given dialect.
func NewMacroRepository(db DB, dialect string) *MacroRepository {
	return &MacroRepository{base{db: db, dialect: dialect}}
}

var _ repository.MacroRepository = (*MacroRepository)(nil)

const macroColumns = "id, name, description, valid"

func scanMacro(row interface{ Scan(dest ...any) error }) (*model.Macro, error) {
	var macro model.Macro

	if err := row.Scan(&macro.ID, &macro.Name, &macro.Description, &macro.Valid); err != nil {
		return nil, err
	}

	return &macro, nil
}

func (r *MacroRepository) GetAll(ctx context.Context) ([]*model.Macro, error) {
	rows, err := r.db.QueryContext(ctx, "SELECT "+macroColumns+" FROM macros ORDER BY name")
	if err != nil {
		return nil, apperror.Internal(err)
	}
	defer func() { _ = rows.Close() }()

	macros := make([]*model.Macro, 0)

	for rows.Next() {
		macro, scanErr := scanMacro(rows)
		if scanErr != nil {
			return nil, apperror.Internal(scanErr)
		}

		macros = append(macros, macro)
	}

	if err := rows.Err(); err != nil {
		return nil, apperror.Internal(err)
	}

	for _, macro := range macros {
		if err := r.fill(ctx, macro); err != nil {
			return nil, err
		}
	}

	return macros, nil
}

func (r *MacroRepository) GetByID(ctx context.Context, id uint) (*model.Macro, error) {
	macro, err := scanMacro(r.db.QueryRowContext(ctx,
		r.rebind("SELECT "+macroColumns+" FROM macros WHERE id = ?"), id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, apperror.NotFound("macro", strconv.FormatUint(uint64(id), 10))
	}

	if err != nil {
		return nil, apperror.Internal(err)
	}

	if err := r.fill(ctx, macro); err != nil {
		return nil, err
	}

	return macro, nil
}

func (r *MacroRepository) Save(ctx context.Context, macro *model.Macro) (*model.Macro, error) {
	var id uint

	err := r.withTx(ctx, func(tx base) error {
		newID, err := tx.insert(ctx, "INSERT INTO macros (name, description, valid) VALUES (?, ?, ?)",
			macro.Name, macro.Description, macro.Valid)
		if err != nil {
			return translateMacroErr(err, macro.Name)
		}

		id = newID

		return replaceMacroActions(ctx, tx, newID, macro.Actions)
	})
	if err != nil {
		return nil, err
	}

	return r.GetByID(ctx, id)
}

func (r *MacroRepository) Update(ctx context.Context, macro *model.Macro) (*model.Macro, error) {
	err := r.withTx(ctx, func(tx base) error {
		found, err := tx.update(ctx, "macros", "UPDATE macros SET name = ?, description = ?, valid = ? WHERE id = ?",
			macro.ID, macro.Name, macro.Description, macro.Valid, macro.ID)
		if err != nil {
			return translateMacroErr(err, macro.Name)
		}

		if !found {
			return apperror.NotFound("macro", strconv.FormatUint(uint64(macro.ID), 10))
		}

		return replaceMacroActions(ctx, tx, macro.ID, macro.Actions)
	})
	if err != nil {
		return nil, err
	}

	return r.GetByID(ctx, macro.ID)
}

func (r *MacroRepository) Delete(ctx context.Context, id uint) error {
	return r.withTx(ctx, func(tx base) error {
		if _, err := tx.exec(ctx, "DELETE FROM macro_actions WHERE macro_id = ?", id); err != nil {
			return apperror.Internal(err)
		}

		affected, err := tx.exec(ctx, "DELETE FROM macros WHERE id = ?", id)
		if err != nil {
			return apperror.Internal(err)
		}

		if affected == 0 {
			return apperror.NotFound("macro", strconv.FormatUint(uint64(id), 10))
		}

		return nil
	})
}

// fill reads a macro's steps in the order they are taken.
func (r *MacroRepository) fill(ctx context.Context, macro *model.Macro) error {
	rows, err := r.db.QueryContext(ctx, r.rebind(
		"SELECT type, parameters FROM macro_actions WHERE macro_id = ? ORDER BY position"), macro.ID)
	if err != nil {
		return apperror.Internal(err)
	}
	defer func() { _ = rows.Close() }()

	macro.Actions = make([]model.MacroAction, 0)

	for rows.Next() {
		var (
			action     model.MacroAction
			kind       string
			parameters string
		)

		if err := rows.Scan(&kind, &parameters); err != nil {
			return apperror.Internal(err)
		}

		action.Type = model.MacroActionType(kind)
		if err := json.Unmarshal([]byte(parameters), &action.Parameters); err != nil {
			return apperror.Internal(err)
		}

		if action.Parameters == nil {
			action.Parameters = map[string]string{}
		}

		macro.Actions = append(macro.Actions, action)
	}

	if err := rows.Err(); err != nil {
		return apperror.Internal(err)
	}

	return nil
}

// replaceMacroActions writes a macro's steps in place of the ones it had.
func replaceMacroActions(ctx context.Context, tx base, id uint, actions []model.MacroAction) error {
	if _, err := tx.exec(ctx, "DELETE FROM macro_actions WHERE macro_id = ?", id); err != nil {
		return apperror.Internal(err)
	}

	for position, action := range actions {
		parameters := action.Parameters
		if parameters == nil {
			parameters = map[string]string{}
		}

		encoded, err := json.Marshal(parameters)
		if err != nil {
			return apperror.Internal(err)
		}

		if _, err := tx.exec(ctx,
			"INSERT INTO macro_actions (macro_id, position, type, parameters) VALUES (?, ?, ?, ?)",
			id, position, string(action.Type), string(encoded)); err != nil {
			return apperror.Internal(err)
		}
	}

	return nil
}

func translateMacroErr(err error, name string) error {
	if isUniqueViolation(err) {
		return apperror.Conflictf("a macro named %q already exists", name).WithCode("macroNameTaken", name)
	}

	return apperror.Internal(err)
}

func (r *MacroRepository) CountJobs(ctx context.Context, id uint) (int, error) {
	var count int

	if err := r.db.QueryRowContext(ctx, r.rebind("SELECT COUNT(DISTINCT job_id) FROM job_macros WHERE macro_id = ?"), id).
		Scan(&count); err != nil {
		return 0, apperror.Internal(err)
	}

	return count, nil
}
