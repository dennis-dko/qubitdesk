package service

import (
	"context"
	"slices"
	"strconv"
	"strings"

	"github.com/dennis-dko/go-ticket-system/internal/domain/model"
	"github.com/dennis-dko/go-ticket-system/internal/support/apperror"
)

func (s *TicketApplicationService) Articles(ctx context.Context, caller *Principal, id uint) ([]*model.Article, error) {
	if _, _, err := s.reached(ctx, caller, id, false); err != nil {
		return nil, err
	}

	return s.tickets.Articles(ctx, id)
}

func (s *TicketApplicationService) History(ctx context.Context, caller *Principal, id uint) ([]model.HistoryEntry, error) {
	if _, _, err := s.reached(ctx, caller, id, false); err != nil {
		return nil, err
	}

	return s.tickets.History(ctx, id)
}

func (s *TicketApplicationService) AccountedMinutes(ctx context.Context, caller *Principal, id uint) (int, error) {
	if _, _, err := s.reached(ctx, caller, id, false); err != nil {
		return 0, err
	}

	return s.tickets.AccountedMinutes(ctx, id)
}

func (s *TicketApplicationService) SetWatching(ctx context.Context, caller *Principal, id uint, watching bool) error {
	if _, _, err := s.reached(ctx, caller, id, false); err != nil {
		return err
	}

	user := callerID(caller)
	if user == nil {
		return apperror.Invalidf("watching a ticket needs somebody signed in").WithCode("watchNeedsAccount")
	}

	return s.tickets.SetWatching(ctx, id, *user, watching)
}

func (s *TicketApplicationService) Watching(ctx context.Context, caller *Principal, id uint) (bool, error) {
	if _, _, err := s.reached(ctx, caller, id, false); err != nil {
		return false, err
	}

	user := callerID(caller)
	if user == nil {
		return false, nil
	}

	return s.tickets.Watching(ctx, id, *user)
}

func (s *TicketApplicationService) Names(ctx context.Context, ids []uint) (map[uint]string, error) {
	names := map[uint]string{}
	if len(ids) == 0 {
		return names, nil
	}

	users, err := s.users.GetAll(ctx)
	if err != nil {
		return nil, err
	}

	for _, user := range users {
		if slices.Contains(ids, user.ID) {
			names[user.ID] = user.Name
		}
	}

	return names, nil
}

// Assignees answers for a queue the caller reaches at all, and is not found for
// one they do not, as the queue itself is not.
func (s *TicketApplicationService) Assignees(
	ctx context.Context,
	caller *Principal,
	queueID uint,
) ([]*model.User, error) {
	queue, err := s.queues.GetByID(ctx, queueID)
	if err != nil {
		return nil, err
	}

	if _, ok := Reach(caller, queue); !ok {
		return nil, apperror.NotFound("queue", strconv.FormatUint(uint64(queueID), 10))
	}

	roles, err := s.roles.GetAll(ctx)
	if err != nil {
		return nil, err
	}

	working := map[uint]bool{}

	for _, role := range roles {
		level, reached := Reach(&Principal{Role: role, Permissions: role.Permissions}, queue)
		working[role.ID] = role.Has(model.PermTicketWrite) && reached && level == model.AccessWrite
	}

	users, err := s.users.GetAll(ctx)
	if err != nil {
		return nil, err
	}

	assignees := make([]*model.User, 0)

	for _, user := range users {
		if working[user.RoleID] {
			assignees = append(assignees, user)
		}
	}

	slices.SortFunc(assignees, func(a, b *model.User) int { return strings.Compare(a.Name, b.Name) })

	return assignees, nil
}
