package service

import (
	"context"
	"slices"
	"strconv"
	"strings"

	"github.com/dennis-dko/go-ticket-system/internal/application/v1/command"
	"github.com/dennis-dko/go-ticket-system/internal/domain/model"
	"github.com/dennis-dko/go-ticket-system/internal/domain/repository"
	"github.com/dennis-dko/go-ticket-system/internal/support/apperror"
)

// QueueService administers the queues tickets are sorted to, and answers which
// of them a caller reaches.
type QueueService interface {
	ListQueues(ctx context.Context, caller *Principal) ([]*model.Queue, error)
	GetQueue(ctx context.Context, caller *Principal, id uint) (*model.Queue, error)
	CreateQueue(ctx context.Context, cmd command.QueueCommand) (*model.Queue, error)
	UpdateQueue(ctx context.Context, id uint, cmd command.QueueCommand) (*model.Queue, error)
	DeleteQueue(ctx context.Context, id uint) error
}

// QueueApplicationService is the QueueService the application runs with.
//
// A caller holding queues:write reaches every queue and sees who else does;
// anybody else sees the queues their role reaches and nothing of the others - not
// refused, not there, because the difference between the two is itself what the
// caller may not learn. What a caller is shown of a queue's access is the line
// that is theirs.
type QueueApplicationService struct {
	queues repository.QueueRepository
	roles  repository.RoleRepository
}

// NewQueueApplicationService needs the roles, to refuse access for one that
// does not exist.
func NewQueueApplicationService(queues repository.QueueRepository, roles repository.RoleRepository) *QueueApplicationService {
	return &QueueApplicationService{queues: queues, roles: roles}
}

var _ QueueService = (*QueueApplicationService)(nil)

// Reach is how far a caller reaches into a queue.
func Reach(caller *Principal, queue *model.Queue) (model.AccessLevel, bool) {
	if caller.Can(model.PermQueueWrite) {
		return model.AccessWrite, true
	}

	if caller.Role == nil {
		return "", false
	}

	for _, entry := range queue.Access {
		if entry.RoleID == caller.Role.ID {
			return entry.Level, true
		}
	}

	return "", false
}

func (s *QueueApplicationService) ListQueues(ctx context.Context, caller *Principal) ([]*model.Queue, error) {
	all, err := s.queues.GetAll(ctx)
	if err != nil {
		return nil, err
	}

	seen := make([]*model.Queue, 0, len(all))

	for _, queue := range all {
		if visible, ok := asSeenBy(caller, queue); ok {
			seen = append(seen, visible)
		}
	}

	return seen, nil
}

func (s *QueueApplicationService) GetQueue(ctx context.Context, caller *Principal, id uint) (*model.Queue, error) {
	if id == 0 {
		return nil, apperror.InvalidFields("id")
	}

	queue, err := s.queues.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	visible, ok := asSeenBy(caller, queue)
	if !ok {
		return nil, apperror.NotFound("queue", strconv.FormatUint(uint64(id), 10))
	}

	return visible, nil
}

func (s *QueueApplicationService) CreateQueue(ctx context.Context, cmd command.QueueCommand) (*model.Queue, error) {
	if cmd.Name == nil {
		return nil, apperror.InvalidFields("name")
	}

	queue := &model.Queue{FollowUp: model.FollowUpReopen, Valid: true}
	if err := s.apply(ctx, queue, cmd); err != nil {
		return nil, err
	}

	return s.queues.Save(ctx, queue)
}

func (s *QueueApplicationService) UpdateQueue(ctx context.Context, id uint, cmd command.QueueCommand) (*model.Queue, error) {
	if id == 0 {
		return nil, apperror.InvalidFields("id")
	}

	queue, err := s.queues.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	if err := s.apply(ctx, queue, cmd); err != nil {
		return nil, err
	}

	return s.queues.Update(ctx, queue)
}

// DeleteQueue removes a queue that holds no tickets and no other queue. A queue
// that holds tickets is made invalid instead - its tickets keep it, and nothing
// new goes in - and the refusal says how many it holds, because that number is
// the only moment anybody learns it.
func (s *QueueApplicationService) DeleteQueue(ctx context.Context, id uint) error {
	if id == 0 {
		return apperror.InvalidFields("id")
	}

	queue, err := s.queues.GetByID(ctx, id)
	if err != nil {
		return err
	}

	tickets, err := s.queues.CountTickets(ctx, id)
	if err != nil {
		return err
	}

	if tickets > 0 {
		return apperror.Conflictf("%d ticket(s) are in the queue %q; make it invalid instead of deleting it",
			tickets, queue.Name).WithCode("queueInUse", queue.Name, tickets)
	}

	children, err := s.queues.CountChildren(ctx, id)
	if err != nil {
		return err
	}

	if children > 0 {
		return apperror.Conflictf("%d queue(s) sit under %q; move or delete them first", children, queue.Name).
			WithCode("queueHasChildren", queue.Name, children)
	}

	return s.queues.Delete(ctx, id)
}

// apply writes what cmd says onto queue and refuses the result if any of it is
// not a queue. Every field is checked before anything is refused, so a form
// learns everything wrong with it at once; only a parent that would put the
// queue under itself is a refusal of its own, because it is about the tree
// rather than a field.
func (s *QueueApplicationService) apply(ctx context.Context, queue *model.Queue, cmd command.QueueCommand) error {
	var invalid []string

	if cmd.Name != nil {
		queue.Name = strings.TrimSpace(*cmd.Name)
	}

	if cmd.Description != nil {
		queue.Description = strings.TrimSpace(*cmd.Description)
	}

	if cmd.FollowUp != nil {
		queue.FollowUp = model.FollowUp(strings.TrimSpace(*cmd.FollowUp))
	}

	if cmd.UnlockMinutes != nil {
		queue.UnlockMinutes = *cmd.UnlockMinutes
	}

	if cmd.Valid != nil {
		queue.Valid = *cmd.Valid
	}

	switch {
	case cmd.ClearParent && cmd.ParentID != nil:
		invalid = append(invalid, "parentId")
	case cmd.ClearParent:
		queue.ParentID = nil
	case cmd.ParentID != nil:
		parent := *cmd.ParentID
		queue.ParentID = &parent
	}

	if queue.Name == "" || model.TooLong(queue.Name, model.MaxNameLength) {
		invalid = append(invalid, "name")
	}

	if model.TooLong(queue.Description, model.MaxDescriptionLength) {
		invalid = append(invalid, "description")
	}

	if !slices.Contains(model.FollowUps(), queue.FollowUp) {
		invalid = append(invalid, "followUp")
	}

	if queue.UnlockMinutes < 0 || queue.UnlockMinutes > model.MaxUnlockMinutes {
		invalid = append(invalid, "unlockMinutes")
	}

	if cmd.Access != nil {
		access, ok, err := s.access(ctx, *cmd.Access)
		if err != nil {
			return err
		}

		if ok {
			queue.Access = access
		} else {
			invalid = append(invalid, "access")
		}
	}

	if len(invalid) > 0 {
		return apperror.InvalidFields(invalid...)
	}

	return s.placeable(ctx, queue)
}

// access turns what a request says about roles into a queue's access, and says
// whether it can be stored: every role there, every level one the code knows,
// and no role twice.
func (s *QueueApplicationService) access(
	ctx context.Context,
	entries []command.QueueAccessCommand,
) ([]model.QueueAccess, bool, error) {
	access := make([]model.QueueAccess, 0, len(entries))

	for _, entry := range entries {
		level := model.AccessLevel(strings.TrimSpace(entry.Level))

		if entry.RoleID == 0 || !slices.Contains(model.AccessLevels(), level) ||
			slices.ContainsFunc(access, func(given model.QueueAccess) bool { return given.RoleID == entry.RoleID }) {
			return nil, false, nil
		}

		if _, err := s.roles.GetByID(ctx, entry.RoleID); err != nil {
			if apperror.KindOf(err) == apperror.KindNotFound {
				return nil, false, nil
			}

			return nil, false, err
		}

		access = append(access, model.QueueAccess{RoleID: entry.RoleID, Level: level})
	}

	return access, true, nil
}

// placeable refuses a parent that is not there, and one that would put the queue
// under itself: walking up from the parent must end at the top without passing
// the queue.
//
// The walk also stops at a queue it has already passed. The API never stores a
// loop, but a tree broken by hand in the database would otherwise turn this
// request into one that never ends - measured, by a test that left one behind.
func (s *QueueApplicationService) placeable(ctx context.Context, queue *model.Queue) error {
	passed := map[uint]bool{}

	for at := queue.ParentID; at != nil; {
		if (queue.ID != 0 && *at == queue.ID) || passed[*at] {
			return apperror.Invalidf("%q cannot sit under itself or under a queue below it", queue.Name).
				WithCode("queueUnderItself", queue.Name)
		}

		passed[*at] = true

		parent, err := s.queues.GetByID(ctx, *at)
		if err != nil {
			if apperror.KindOf(err) == apperror.KindNotFound {
				return apperror.InvalidFields("parentId")
			}

			return err
		}

		at = parent.ParentID
	}

	return nil
}

// asSeenBy is the queue as a caller is shown it, and whether they are shown it
// at all: everything to whoever holds queues:write, the caller's own line of its
// access to anybody it reaches, and nothing to the rest.
func asSeenBy(caller *Principal, queue *model.Queue) (*model.Queue, bool) {
	if caller.Can(model.PermQueueWrite) {
		return queue, true
	}

	level, reached := Reach(caller, queue)
	if !reached {
		return nil, false
	}

	visible := *queue
	visible.Access = []model.QueueAccess{{RoleID: caller.Role.ID, Level: level}}

	return &visible, true
}
