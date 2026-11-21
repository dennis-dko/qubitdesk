package sqldb

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/dennis-dko/go-ticket-system/internal/domain/model"
	"github.com/dennis-dko/go-ticket-system/internal/domain/repository"
	"github.com/dennis-dko/go-ticket-system/internal/support/apperror"
)

// ConfigItemRepository keeps the configuration items in config_items and their
// versions in config_item_versions, a version's values as JSON.
type ConfigItemRepository struct{ base }

// NewConfigItemRepository creates the repository for the given dialect.
func NewConfigItemRepository(db DB, dialect string) *ConfigItemRepository {
	return &ConfigItemRepository{base{db: db, dialect: dialect}}
}

var _ repository.ConfigItemRepository = (*ConfigItemRepository)(nil)

const configItemColumns = "id, number, class_id, name, deployment_state_id, incident_state_id, version_id, " +
	"version_count, created_at, changed_at, created_by, changed_by"

const configItemVersionColumns = "id, item_id, definition_id, name, deployment_state_id, incident_state_id, data, " +
	"created_at, created_by"

func scanConfigItem(row scanner) (*model.ConfigItem, error) {
	var (
		item   model.ConfigItem
		number sql.NullString
	)

	if err := row.Scan(&item.ID, &number, &item.ClassID, &item.Name, &item.DeploymentStateID,
		&item.IncidentStateID, &item.VersionID, &item.VersionCount, &item.CreatedAt, &item.ChangedAt,
		&item.CreatedBy, &item.ChangedBy); err != nil {
		return nil, err
	}

	item.Number = number.String

	return &item, nil
}

func scanConfigItemVersion(row scanner) (*model.ConfigItemVersion, error) {
	var (
		version model.ConfigItemVersion
		data    string
	)

	if err := row.Scan(&version.ID, &version.ItemID, &version.DefinitionID, &version.Name,
		&version.DeploymentStateID, &version.IncidentStateID, &data, &version.CreatedAt,
		&version.CreatedBy); err != nil {
		return nil, err
	}

	if err := json.Unmarshal([]byte(data), &version.Data); err != nil {
		return nil, err
	}

	return &version, nil
}

func (r *ConfigItemRepository) List(
	ctx context.Context,
	filter repository.ConfigItemFilter,
) ([]*model.ConfigItem, uint, error) {
	where := []string{"1 = 1"}
	args := make([]any, 0, 4)

	if filter.ClassID != nil {
		where = append(where, "class_id = ?")
		args = append(args, *filter.ClassID)
	}

	if search := strings.TrimSpace(filter.Search); search != "" {
		where = append(where, "(LOWER(name) LIKE ? ESCAPE '!' OR number LIKE ? ESCAPE '!')")
		pattern := "%" + strings.ToLower(likeEscaped(search)) + "%"
		args = append(args, pattern, pattern)
	}

	condition := strings.Join(where, " AND ")

	var total uint

	if err := r.db.QueryRowContext(ctx,
		r.rebind("SELECT COUNT(*) FROM config_items WHERE "+condition), args...).Scan(&total); err != nil {
		return nil, 0, apperror.Internal(err)
	}

	page := max(filter.Page, 1)
	args = append(args, filter.Limit, (page-1)*filter.Limit)

	rows, err := r.db.QueryContext(ctx, r.rebind(
		"SELECT "+configItemColumns+" FROM config_items WHERE "+condition+" ORDER BY name, id LIMIT ? OFFSET ?"),
		args...)
	if err != nil {
		return nil, 0, apperror.Internal(err)
	}
	defer func() { _ = rows.Close() }()

	items := make([]*model.ConfigItem, 0, filter.Limit)

	for rows.Next() {
		item, scanErr := scanConfigItem(rows)
		if scanErr != nil {
			return nil, 0, apperror.Internal(scanErr)
		}

		items = append(items, item)
	}

	if err := rows.Err(); err != nil {
		return nil, 0, apperror.Internal(err)
	}

	return items, total, nil
}

func (r *ConfigItemRepository) GetByID(ctx context.Context, id uint) (*model.ConfigItem, error) {
	return r.item(ctx, r.base, id)
}

// item reads one item through the connection or the transaction given.
func (r *ConfigItemRepository) item(ctx context.Context, through base, id uint) (*model.ConfigItem, error) {
	row := through.db.QueryRowContext(ctx, through.rebind("SELECT "+configItemColumns+" FROM config_items WHERE id = ?"), id)

	item, err := scanConfigItem(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, apperror.NotFound("configitem", strconv.FormatUint(uint64(id), 10))
	}

	if err != nil {
		return nil, apperror.Internal(err)
	}

	return item, nil
}

func (r *ConfigItemRepository) Versions(ctx context.Context, itemID uint) ([]*model.ConfigItemVersion, error) {
	rows, err := r.db.QueryContext(ctx, r.rebind("SELECT "+configItemVersionColumns+
		" FROM config_item_versions WHERE item_id = ? ORDER BY id DESC"), itemID)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	defer func() { _ = rows.Close() }()

	versions := make([]*model.ConfigItemVersion, 0)

	for rows.Next() {
		version, scanErr := scanConfigItemVersion(rows)
		if scanErr != nil {
			return nil, apperror.Internal(scanErr)
		}

		versions = append(versions, version)
	}

	if err := rows.Err(); err != nil {
		return nil, apperror.Internal(err)
	}

	return versions, nil
}

func (r *ConfigItemRepository) Version(ctx context.Context, id uint) (*model.ConfigItemVersion, error) {
	row := r.db.QueryRowContext(ctx, r.rebind("SELECT "+configItemVersionColumns+
		" FROM config_item_versions WHERE id = ?"), id)

	version, err := scanConfigItemVersion(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, apperror.NotFound("configitemversion", strconv.FormatUint(uint64(id), 10))
	}

	if err != nil {
		return nil, apperror.Internal(err)
	}

	return version, nil
}

func (r *ConfigItemRepository) Create(
	ctx context.Context,
	item *model.ConfigItem,
	first *model.ConfigItemVersion,
	numberOf func(id uint) string,
) (*model.ConfigItem, error) {
	var stored *model.ConfigItem

	err := r.withTx(ctx, func(tx base) error {
		id, err := tx.insert(ctx, "INSERT INTO config_items (class_id, name, deployment_state_id, incident_state_id, "+
			"version_id, version_count, created_at, changed_at, created_by, changed_by) "+
			"VALUES (?, ?, ?, ?, 0, 0, ?, ?, ?, ?)",
			item.ClassID, first.Name, first.DeploymentStateID, first.IncidentStateID, item.CreatedAt, item.CreatedAt,
			item.CreatedBy, item.CreatedBy)
		if err != nil {
			return apperror.Internal(err)
		}

		if _, err := tx.exec(ctx, "UPDATE config_items SET number = ? WHERE id = ?", numberOf(id), id); err != nil {
			return apperror.Internal(err)
		}

		first.ItemID = id

		if err := addConfigItemVersion(ctx, tx, first); err != nil {
			return err
		}

		stored, err = r.item(ctx, tx, id)

		return err
	})
	if err != nil {
		return nil, err
	}

	return stored, nil
}

func (r *ConfigItemRepository) AddVersion(
	ctx context.Context,
	version *model.ConfigItemVersion,
) (*model.ConfigItem, error) {
	var stored *model.ConfigItem

	err := r.withTx(ctx, func(tx base) error {
		if _, err := r.item(ctx, tx, version.ItemID); err != nil {
			return err
		}

		if err := addConfigItemVersion(ctx, tx, version); err != nil {
			return err
		}

		var err error

		stored, err = r.item(ctx, tx, version.ItemID)

		return err
	})
	if err != nil {
		return nil, err
	}

	return stored, nil
}

// addConfigItemVersion writes a version and carries what it says onto its item.
func addConfigItemVersion(ctx context.Context, tx base, version *model.ConfigItemVersion) error {
	data, err := json.Marshal(version.Data)
	if err != nil {
		return apperror.Internal(err)
	}

	id, err := tx.insert(ctx, "INSERT INTO config_item_versions (item_id, definition_id, name, deployment_state_id, "+
		"incident_state_id, data, created_at, created_by) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
		version.ItemID, version.DefinitionID, version.Name, version.DeploymentStateID, version.IncidentStateID,
		string(data), version.CreatedAt, version.CreatedBy)
	if err != nil {
		return apperror.Internal(err)
	}

	if _, err := tx.exec(ctx, "UPDATE config_items SET name = ?, deployment_state_id = ?, incident_state_id = ?, "+
		"version_id = ?, version_count = version_count + 1, changed_at = ?, changed_by = ? WHERE id = ?",
		version.Name, version.DeploymentStateID, version.IncidentStateID, id, version.CreatedAt,
		version.CreatedBy, version.ItemID); err != nil {
		return apperror.Internal(err)
	}

	return nil
}

func (r *ConfigItemRepository) Delete(ctx context.Context, id uint) error {
	return r.withTx(ctx, func(tx base) error {
		if _, err := r.item(ctx, tx, id); err != nil {
			return err
		}

		if _, err := tx.exec(ctx, "DELETE FROM config_item_versions WHERE item_id = ?", id); err != nil {
			return apperror.Internal(err)
		}

		if _, err := tx.exec(ctx, "DELETE FROM config_items WHERE id = ?", id); err != nil {
			return apperror.Internal(err)
		}

		return nil
	})
}
