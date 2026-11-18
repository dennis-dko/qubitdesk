package sqldb

import (
	"context"
	"database/sql"
	"errors"
	"strconv"

	"github.com/dennis-dko/go-ticket-system/internal/domain/model"
	"github.com/dennis-dko/go-ticket-system/internal/domain/repository"
	"github.com/dennis-dko/go-ticket-system/internal/support/apperror"
)

// GeneralCatalogRepository keeps the general catalog in general_catalog.
type GeneralCatalogRepository struct{ base }

// NewGeneralCatalogRepository creates the repository for the given dialect.
func NewGeneralCatalogRepository(db DB, dialect string) *GeneralCatalogRepository {
	return &GeneralCatalogRepository{base{db: db, dialect: dialect}}
}

var _ repository.GeneralCatalogRepository = (*GeneralCatalogRepository)(nil)

const catalogColumns = "id, catalog_class, name, functionality, comment, valid"

func scanCatalogItem(row scanner) (*model.CatalogItem, error) {
	var item model.CatalogItem

	if err := row.Scan(&item.ID, &item.Class, &item.Name, &item.Functionality, &item.Comment, &item.Valid); err != nil {
		return nil, err
	}

	return &item, nil
}

func (r *GeneralCatalogRepository) Items(ctx context.Context, class string) ([]*model.CatalogItem, error) {
	query, args := "SELECT "+catalogColumns+" FROM general_catalog ORDER BY catalog_class, name", []any{}
	if class != "" {
		query = "SELECT " + catalogColumns + " FROM general_catalog WHERE catalog_class = ? ORDER BY name"
		args = append(args, class)
	}

	rows, err := r.db.QueryContext(ctx, r.rebind(query), args...)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	defer func() { _ = rows.Close() }()

	items := make([]*model.CatalogItem, 0)

	for rows.Next() {
		item, scanErr := scanCatalogItem(rows)
		if scanErr != nil {
			return nil, apperror.Internal(scanErr)
		}

		items = append(items, item)
	}

	if err := rows.Err(); err != nil {
		return nil, apperror.Internal(err)
	}

	return items, nil
}

func (r *GeneralCatalogRepository) Classes(ctx context.Context) ([]string, error) {
	rows, err := r.db.QueryContext(ctx, "SELECT DISTINCT catalog_class FROM general_catalog ORDER BY catalog_class")
	if err != nil {
		return nil, apperror.Internal(err)
	}
	defer func() { _ = rows.Close() }()

	classes := make([]string, 0)

	for rows.Next() {
		var class string
		if err := rows.Scan(&class); err != nil {
			return nil, apperror.Internal(err)
		}

		classes = append(classes, class)
	}

	if err := rows.Err(); err != nil {
		return nil, apperror.Internal(err)
	}

	return classes, nil
}

func (r *GeneralCatalogRepository) GetByID(ctx context.Context, id uint) (*model.CatalogItem, error) {
	item, err := scanCatalogItem(r.db.QueryRowContext(ctx,
		r.rebind("SELECT "+catalogColumns+" FROM general_catalog WHERE id = ?"), id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, apperror.NotFound("catalogitem", strconv.FormatUint(uint64(id), 10))
	}

	if err != nil {
		return nil, apperror.Internal(err)
	}

	return item, nil
}

func (r *GeneralCatalogRepository) Save(ctx context.Context, item *model.CatalogItem) (*model.CatalogItem, error) {
	id, err := r.insert(ctx,
		"INSERT INTO general_catalog (catalog_class, name, functionality, comment, valid) VALUES (?, ?, ?, ?, ?)",
		item.Class, item.Name, item.Functionality, item.Comment, item.Valid)
	if err != nil {
		return nil, translateCatalogErr(err, item)
	}

	return r.GetByID(ctx, id)
}

func (r *GeneralCatalogRepository) Update(ctx context.Context, item *model.CatalogItem) (*model.CatalogItem, error) {
	found, err := r.update(ctx, "general_catalog",
		"UPDATE general_catalog SET name = ?, functionality = ?, comment = ?, valid = ? WHERE id = ?",
		item.ID, item.Name, item.Functionality, item.Comment, item.Valid, item.ID)
	if err != nil {
		return nil, translateCatalogErr(err, item)
	}

	if !found {
		return nil, apperror.NotFound("catalogitem", strconv.FormatUint(uint64(item.ID), 10))
	}

	return r.GetByID(ctx, item.ID)
}

// translateCatalogErr names a value its class already holds, which is what the
// uniqueness of a class's names refuses.
func translateCatalogErr(err error, item *model.CatalogItem) error {
	if isUniqueViolation(err) {
		return apperror.Conflictf("%q already holds %q", item.Class, item.Name).
			WithCode("catalogItemTaken", item.Class, item.Name)
	}

	return apperror.Internal(err)
}
