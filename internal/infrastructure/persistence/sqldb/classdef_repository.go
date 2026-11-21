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

// ClassDefinitionRepository keeps the classes' definitions in
// ci_class_definitions, a row a version, the attributes as JSON.
type ClassDefinitionRepository struct{ base }

// NewClassDefinitionRepository creates the repository for the given dialect.
func NewClassDefinitionRepository(db DB, dialect string) *ClassDefinitionRepository {
	return &ClassDefinitionRepository{base{db: db, dialect: dialect}}
}

var _ repository.ClassDefinitionRepository = (*ClassDefinitionRepository)(nil)

const classDefinitionColumns = "id, class_id, version, attributes, created_at, created_by"

func scanClassDefinition(row scanner) (*model.ClassDefinition, error) {
	var (
		definition model.ClassDefinition
		attributes string
	)

	if err := row.Scan(&definition.ID, &definition.ClassID, &definition.Version, &attributes,
		&definition.CreatedAt, &definition.CreatedBy); err != nil {
		return nil, err
	}

	if err := json.Unmarshal([]byte(attributes), &definition.Attributes); err != nil {
		return nil, err
	}

	return &definition, nil
}

func (r *ClassDefinitionRepository) Current(ctx context.Context, classID uint) (*model.ClassDefinition, error) {
	row := r.db.QueryRowContext(ctx, r.rebind("SELECT "+classDefinitionColumns+
		" FROM ci_class_definitions WHERE class_id = ? ORDER BY version DESC LIMIT 1"), classID)

	definition, err := scanClassDefinition(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, apperror.NotFound("classdefinition", strconv.FormatUint(uint64(classID), 10))
	}

	if err != nil {
		return nil, apperror.Internal(err)
	}

	return definition, nil
}

func (r *ClassDefinitionRepository) Versions(ctx context.Context, classID uint) ([]*model.ClassDefinition, error) {
	rows, err := r.db.QueryContext(ctx, r.rebind("SELECT "+classDefinitionColumns+
		" FROM ci_class_definitions WHERE class_id = ? ORDER BY version DESC"), classID)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	defer func() { _ = rows.Close() }()

	versions := make([]*model.ClassDefinition, 0)

	for rows.Next() {
		definition, scanErr := scanClassDefinition(rows)
		if scanErr != nil {
			return nil, apperror.Internal(scanErr)
		}

		versions = append(versions, definition)
	}

	if err := rows.Err(); err != nil {
		return nil, apperror.Internal(err)
	}

	return versions, nil
}

func (r *ClassDefinitionRepository) Add(
	ctx context.Context,
	definition *model.ClassDefinition,
) (*model.ClassDefinition, error) {
	attributes, err := json.Marshal(definition.Attributes)
	if err != nil {
		return nil, apperror.Internal(err)
	}

	var id uint

	err = r.withTx(ctx, func(tx base) error {
		var last int

		if err := tx.db.QueryRowContext(ctx, tx.rebind(
			"SELECT COALESCE(MAX(version), 0) FROM ci_class_definitions WHERE class_id = ?"),
			definition.ClassID).Scan(&last); err != nil {
			return apperror.Internal(err)
		}

		newID, err := tx.insert(ctx, "INSERT INTO ci_class_definitions "+
			"(class_id, version, attributes, created_at, created_by) VALUES (?, ?, ?, ?, ?)",
			definition.ClassID, last+1, string(attributes), definition.CreatedAt, definition.CreatedBy)
		if err != nil {
			return apperror.Internal(err)
		}

		id = newID

		return nil
	})
	if err != nil {
		return nil, err
	}

	row := r.db.QueryRowContext(ctx, r.rebind("SELECT "+classDefinitionColumns+
		" FROM ci_class_definitions WHERE id = ?"), id)

	stored, err := scanClassDefinition(row)
	if err != nil {
		return nil, apperror.Internal(err)
	}

	return stored, nil
}
