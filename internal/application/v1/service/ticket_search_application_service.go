package service

import (
	"context"
	"strings"
	"time"

	"github.com/dennis-dko/qubitdesk/internal/domain/model"
	"github.com/dennis-dko/qubitdesk/internal/domain/repository"
	"github.com/dennis-dko/qubitdesk/internal/support/apperror"
)

// TicketSearchApplicationService keeps the searches an agent saved. A search is
// the agent's own: another account's is not found, and the application, which
// is no account, has none.
type TicketSearchApplicationService struct {
	searches repository.TicketSearchRepository
	now      func() time.Time
}

// NewTicketSearchApplicationService wires the saved searches.
func NewTicketSearchApplicationService(searches repository.TicketSearchRepository) *TicketSearchApplicationService {
	return &TicketSearchApplicationService{searches: searches, now: time.Now}
}

// Mine lists the caller's saved searches, by name.
func (s *TicketSearchApplicationService) Mine(ctx context.Context, caller *Principal) ([]*model.TicketSearch, error) {
	if caller == nil || caller.User == nil {
		return []*model.TicketSearch{}, nil
	}

	return s.searches.List(ctx, caller.User.ID)
}

// Save keeps a search under a name of the caller's, refusing a name they gave
// another search already and a search past the most one account keeps. The
// query is the caller's handler's to have read the listing's way first.
func (s *TicketSearchApplicationService) Save(
	ctx context.Context,
	caller *Principal,
	name, query string,
) (*model.TicketSearch, error) {
	if caller == nil || caller.User == nil {
		return nil, apperror.Conflictf("only an account keeps searches")
	}

	name = strings.TrimSpace(name)

	var invalid []string

	if name == "" || model.TooLong(name, model.MaxTicketSearchName) {
		invalid = append(invalid, "name")
	}

	if model.TooLong(query, model.MaxTicketSearchQuery) {
		invalid = append(invalid, "query")
	}

	if len(invalid) > 0 {
		return nil, apperror.InvalidFields(invalid...)
	}

	mine, err := s.searches.List(ctx, caller.User.ID)
	if err != nil {
		return nil, err
	}

	if len(mine) >= model.MaxTicketSearches {
		return nil, apperror.Conflictf("an account keeps at most %d searches", model.MaxTicketSearches).
			WithCode("ticketSearchLimit", model.MaxTicketSearches)
	}

	for _, search := range mine {
		if strings.EqualFold(search.Name, name) {
			return nil, apperror.Conflictf("a search called %q is kept already", name).
				WithCode("ticketSearchNameTaken", name)
		}
	}

	return s.searches.Save(ctx, &model.TicketSearch{
		UserID: caller.User.ID, Name: name, Query: query, CreatedAt: s.now().UTC(),
	})
}

// Delete removes one of the caller's searches; another account's is not found.
func (s *TicketSearchApplicationService) Delete(ctx context.Context, caller *Principal, id uint) error {
	if caller == nil || caller.User == nil {
		return apperror.NotFound("search", "")
	}

	return s.searches.Delete(ctx, id, caller.User.ID)
}
