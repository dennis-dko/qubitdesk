package sqldb

import (
	"context"
	"strconv"

	"github.com/dennis-dko/qubitdesk/internal/domain/model"
	"github.com/dennis-dko/qubitdesk/internal/domain/repository"
	"github.com/dennis-dko/qubitdesk/internal/support/apperror"
)

// TicketSearchRepository keeps saved searches in ticket_searches.
type TicketSearchRepository struct{ base }

// NewTicketSearchRepository creates the repository over db.
func NewTicketSearchRepository(db DB, dialect string) *TicketSearchRepository {
	return &TicketSearchRepository{base{db: db, dialect: dialect}}
}

var _ repository.TicketSearchRepository = (*TicketSearchRepository)(nil)

func (r *TicketSearchRepository) List(ctx context.Context, userID uint) ([]*model.TicketSearch, error) {
	rows, err := r.db.QueryContext(ctx, r.rebind(
		"SELECT id, user_id, name, query, created_at FROM ticket_searches WHERE user_id = ? ORDER BY name, id"), userID)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	defer func() { _ = rows.Close() }()

	searches := make([]*model.TicketSearch, 0)

	for rows.Next() {
		var search model.TicketSearch
		if err := rows.Scan(&search.ID, &search.UserID, &search.Name, &search.Query, &search.CreatedAt); err != nil {
			return nil, apperror.Internal(err)
		}

		searches = append(searches, &search)
	}

	if err := rows.Err(); err != nil {
		return nil, apperror.Internal(err)
	}

	return searches, nil
}

func (r *TicketSearchRepository) Save(ctx context.Context, search *model.TicketSearch) (*model.TicketSearch, error) {
	id, err := r.insert(ctx,
		"INSERT INTO ticket_searches (user_id, name, query, created_at) VALUES (?, ?, ?, ?)",
		search.UserID, search.Name, search.Query, search.CreatedAt.UTC())
	if err != nil {
		return nil, err
	}

	saved := *search
	saved.ID = id

	return &saved, nil
}

func (r *TicketSearchRepository) Delete(ctx context.Context, id, userID uint) error {
	removed, err := r.exec(ctx, "DELETE FROM ticket_searches WHERE id = ? AND user_id = ?", id, userID)
	if err != nil {
		return err
	}

	if removed == 0 {
		return apperror.NotFound("search", strconv.FormatUint(uint64(id), 10))
	}

	return nil
}
