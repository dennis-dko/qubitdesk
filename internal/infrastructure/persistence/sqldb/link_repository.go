package sqldb

import (
	"context"
	"database/sql"
	"errors"
	"strconv"

	"github.com/dennis-dko/qubitdesk/internal/domain/model"
	"github.com/dennis-dko/qubitdesk/internal/domain/repository"
	"github.com/dennis-dko/qubitdesk/internal/support/apperror"
)

// LinkRepository keeps the links in links, each end named by its kind and id.
type LinkRepository struct{ base }

// NewLinkRepository creates the repository for the given dialect.
func NewLinkRepository(db DB, dialect string) *LinkRepository {
	return &LinkRepository{base{db: db, dialect: dialect}}
}

var _ repository.LinkRepository = (*LinkRepository)(nil)

const linkColumns = "id, source_object, source_id, target_object, target_id, type, created_at, created_by"

func scanLink(row scanner) (*model.Link, error) {
	var link model.Link

	if err := row.Scan(&link.ID, &link.SourceObject, &link.SourceID, &link.TargetObject, &link.TargetID, &link.Type,
		&link.CreatedAt, &link.CreatedBy); err != nil {
		return nil, err
	}

	return &link, nil
}

func (r *LinkRepository) Of(ctx context.Context, object model.LinkObject, id uint) ([]*model.Link, error) {
	rows, err := r.db.QueryContext(ctx, r.rebind("SELECT "+linkColumns+" FROM links "+
		"WHERE (source_object = ? AND source_id = ?) OR (target_object = ? AND target_id = ?) ORDER BY id"),
		object, id, object, id)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	defer func() { _ = rows.Close() }()

	links := make([]*model.Link, 0)

	for rows.Next() {
		link, scanErr := scanLink(rows)
		if scanErr != nil {
			return nil, apperror.Internal(scanErr)
		}

		links = append(links, link)
	}

	if err := rows.Err(); err != nil {
		return nil, apperror.Internal(err)
	}

	return links, nil
}

func (r *LinkRepository) GetByID(ctx context.Context, id uint) (*model.Link, error) {
	link, err := scanLink(r.db.QueryRowContext(ctx, r.rebind("SELECT "+linkColumns+" FROM links WHERE id = ?"), id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, apperror.NotFound("link", strconv.FormatUint(uint64(id), 10))
	}

	if err != nil {
		return nil, apperror.Internal(err)
	}

	return link, nil
}

func (r *LinkRepository) Create(ctx context.Context, link *model.Link) (*model.Link, error) {
	id, err := r.insert(ctx, "INSERT INTO links (source_object, source_id, target_object, target_id, type, "+
		"created_at, created_by) VALUES (?, ?, ?, ?, ?, ?, ?)", link.SourceObject, link.SourceID, link.TargetObject,
		link.TargetID, link.Type, link.CreatedAt, link.CreatedBy)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, apperror.Conflictf("the two are joined already").WithCode("linkTaken")
		}

		return nil, apperror.Internal(err)
	}

	stored := *link
	stored.ID = id

	return &stored, nil
}

func (r *LinkRepository) Delete(ctx context.Context, id uint) error {
	affected, err := r.exec(ctx, "DELETE FROM links WHERE id = ?", id)
	if err != nil {
		return apperror.Internal(err)
	}

	if affected == 0 {
		return apperror.NotFound("link", strconv.FormatUint(uint64(id), 10))
	}

	return nil
}

// deleteLinksOf removes every link with the record at either end, inside the
// transaction that deletes the record.
func deleteLinksOf(ctx context.Context, tx base, object model.LinkObject, id uint) error {
	if _, err := tx.exec(ctx, "DELETE FROM links WHERE (source_object = ? AND source_id = ?) "+
		"OR (target_object = ? AND target_id = ?)", object, id, object, id); err != nil {
		return apperror.Internal(err)
	}

	return nil
}
