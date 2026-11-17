package service

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/dennis-dko/go-ticket-system/internal/application/v1/command"
	"github.com/dennis-dko/go-ticket-system/internal/domain/model"
	"github.com/dennis-dko/go-ticket-system/internal/support/apperror"
)

// workable is a queue a caller may put a ticket into: one they work in, and one
// still offered. A queue they do not reach is not found, as for a ticket.
func (s *TicketApplicationService) workable(ctx context.Context, caller *Principal, id uint) (*model.Queue, error) {
	queue, err := s.queues.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	level, ok := Reach(caller, queue)
	if !ok {
		return nil, apperror.NotFound("queue", strconv.FormatUint(uint64(id), 10))
	}

	if level != model.AccessWrite {
		return nil, apperror.Conflictf("the queue %q may be read here and not worked in", queue.Name).
			WithCode("queueReadOnly", queue.Name)
	}

	if !queue.Valid {
		return nil, apperror.Conflictf("the queue %q is no longer offered for tickets", queue.Name).
			WithCode("queueRetired", queue.Name)
	}

	return queue, nil
}

// article is the article a request describes, the time noted with it, and the
// fields it cannot be stored with.
func (s *TicketApplicationService) article(
	channel *string,
	customerVisible bool,
	subject, body *string,
	minutes int,
) (*model.Article, int, []string) {
	var invalid []string

	article := &model.Article{
		Sender:          model.SenderAgent,
		Channel:         model.Channel(strings.TrimSpace(valueOf(channel))),
		CustomerVisible: customerVisible,
		Subject:         strings.TrimSpace(valueOf(subject)),
		Body:            strings.TrimSpace(valueOf(body)),
	}

	if channel == nil {
		article.Channel = model.ChannelNote
	}

	if !slices.Contains(model.AgentChannels(), article.Channel) {
		invalid = append(invalid, "channel")
	}

	if model.TooLong(article.Subject, model.MaxTicketTitleLength) {
		invalid = append(invalid, "subject")
	}

	if article.Body == "" {
		invalid = append(invalid, "body")
	}

	if minutes < 0 || minutes > model.MaxArticleMinutes {
		invalid = append(invalid, "minutes")
	}

	return article, minutes, invalid
}

// chosenState is the state a request names, or the shipped one by name where it
// names none. Merged is reached by merging and never chosen; a retired state is
// not offered.
func (s *TicketApplicationService) chosenState(
	ctx context.Context,
	id *uint,
	fallback string,
) (*model.TicketState, error) {
	if id == nil {
		return s.stateNamed(ctx, fallback)
	}

	state, err := s.states.GetByID(ctx, *id)
	if apperror.KindOf(err) == apperror.KindNotFound {
		return nil, apperror.InvalidFields("stateId")
	}

	if err != nil {
		return nil, err
	}

	if state.Type == model.StateTypeMerged {
		return nil, apperror.InvalidFields("stateId")
	}

	if !state.Valid {
		return nil, apperror.Conflictf("the state %q is no longer offered for tickets", state.Name).
			WithCode("stateRetired", state.Name)
	}

	return state, nil
}

func (s *TicketApplicationService) stateNamed(ctx context.Context, name string) (*model.TicketState, error) {
	state, err := s.states.GetByName(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("the shipped state %q: %w", name, err)
	}

	return state, nil
}

// placeInState puts a ticket into a state, with what the state's type asks: a
// pending one a moment still to come, and a finished one the moment it finished.
// A moment for a state that waits for none is refused rather than dropped, so a
// request that meant to wait does not quietly stop waiting.
func (s *TicketApplicationService) placeInState(
	ticket *model.Ticket,
	state *model.TicketState,
	pendingUntil *time.Time,
	now time.Time,
) error {
	pending := state.Type == model.StateTypePendingReminder || state.Type == model.StateTypePendingAuto

	switch {
	case pending && pendingUntil != nil:
		if !pendingUntil.After(now) {
			return apperror.InvalidFields("pendingUntil")
		}

		until := pendingUntil.UTC()
		ticket.PendingUntil = &until
	case pending:
		// Already waiting for a moment still to come, and the request said no
		// other: it keeps waiting for that one. Not waiting yet, it has to say.
		if ticket.PendingUntil == nil || !ticket.PendingUntil.After(now) {
			return apperror.InvalidFields("pendingUntil")
		}
	case pendingUntil != nil:
		return apperror.InvalidFields("pendingUntil")
	default:
		ticket.PendingUntil = nil
	}

	finished := slices.Contains(model.FinishedStateTypes(), state.Type)

	switch {
	case finished && ticket.ClosedAt == nil:
		ticket.ClosedAt = &now
	case !finished:
		ticket.ClosedAt = nil
	}

	ticket.StateID = state.ID

	return nil
}

func (s *TicketApplicationService) chosenPriority(ctx context.Context, id *uint) (uint, error) {
	if id == nil {
		priority, err := s.priorities.GetByName(ctx, model.PriorityNormal)
		if err != nil {
			return 0, fmt.Errorf("the shipped priority %q: %w", model.PriorityNormal, err)
		}

		return priority.ID, nil
	}

	priority, err := s.priorities.GetByID(ctx, *id)
	if apperror.KindOf(err) == apperror.KindNotFound {
		return 0, apperror.InvalidFields("priorityId")
	}

	if err != nil {
		return 0, err
	}

	if !priority.Valid {
		return 0, apperror.Conflictf("the priority %q is no longer offered for tickets", priority.Name).
			WithCode("priorityRetired", priority.Name)
	}

	return priority.ID, nil
}

func (s *TicketApplicationService) chosenType(ctx context.Context, id *uint) (uint, error) {
	if id == nil {
		kind, err := s.types.GetByName(ctx, model.TypeUnclassified)
		if err != nil {
			return 0, fmt.Errorf("the shipped type %q: %w", model.TypeUnclassified, err)
		}

		return kind.ID, nil
	}

	kind, err := s.types.GetByID(ctx, *id)
	if apperror.KindOf(err) == apperror.KindNotFound {
		return 0, apperror.InvalidFields("typeId")
	}

	if err != nil {
		return 0, err
	}

	if !kind.Valid {
		return 0, apperror.Conflictf("the type %q is no longer offered for tickets", kind.Name).
			WithCode("typeRetired", kind.Name)
	}

	return kind.ID, nil
}

// errNotStaff is what staff answers for an account that cannot own a ticket,
// which each caller refuses as the field that named it.
var errNotStaff = errors.New("not an account on the staff side")

// staff is the account a request names as owner or responsible: one that exists
// and works on the staff side. A customer's account owns no ticket.
func (s *TicketApplicationService) staff(ctx context.Context, id *uint) (*uint, error) {
	if id == nil {
		return nil, nil
	}

	user, err := s.users.GetByID(ctx, *id)
	if apperror.KindOf(err) == apperror.KindNotFound {
		return nil, errNotStaff
	}

	if err != nil {
		return nil, err
	}

	if user.RoleName == model.RoleCustomer {
		return nil, errNotStaff
	}

	chosen := user.ID

	return &chosen, nil
}

// changeCatalog applies what a change says about the state, the priority and the
// type, writing a line of history for each that changed - by name, as it was
// called then.
func (s *TicketApplicationService) changeCatalog(
	ctx context.Context,
	ticket *model.Ticket,
	cmd command.UpdateTicketCommand,
	change *history,
	now time.Time,
) error {
	if (cmd.StateID != nil && *cmd.StateID != ticket.StateID) || cmd.PendingUntil != nil {
		was, err := s.states.GetByID(ctx, ticket.StateID)
		if err != nil {
			return err
		}

		state := was
		if cmd.StateID != nil && *cmd.StateID != ticket.StateID {
			if state, err = s.chosenState(ctx, cmd.StateID, ""); err != nil {
				return err
			}
		}

		waited := ticket.PendingUntil

		if err := s.placeInState(ticket, state, cmd.PendingUntil, now); err != nil {
			return err
		}

		if state.ID != was.ID {
			change.add(model.HistoryState, was.Name, state.Name)
		}

		if !sameMoment(waited, ticket.PendingUntil) {
			change.add(model.HistoryPending, moment(waited), moment(ticket.PendingUntil))
		}
	}

	if cmd.PriorityID != nil && *cmd.PriorityID != ticket.PriorityID {
		was, err := s.priorities.GetByID(ctx, ticket.PriorityID)
		if err != nil {
			return err
		}

		id, err := s.chosenPriority(ctx, cmd.PriorityID)
		if err != nil {
			return err
		}

		chosen, err := s.priorities.GetByID(ctx, id)
		if err != nil {
			return err
		}

		change.add(model.HistoryPriority, was.Name, chosen.Name)
		ticket.PriorityID = id
	}

	if cmd.TypeID != nil && *cmd.TypeID != ticket.TypeID {
		was, err := s.types.GetByID(ctx, ticket.TypeID)
		if err != nil {
			return err
		}

		id, err := s.chosenType(ctx, cmd.TypeID)
		if err != nil {
			return err
		}

		chosen, err := s.types.GetByID(ctx, id)
		if err != nil {
			return err
		}

		change.add(model.HistoryType, was.Name, chosen.Name)
		ticket.TypeID = id
	}

	return nil
}

// changePeople applies what a change says about the owner, the responsible and
// the lock. Locking a ticket nobody owns makes the caller its owner.
func (s *TicketApplicationService) changePeople(
	ctx context.Context,
	caller *Principal,
	ticket *model.Ticket,
	cmd command.UpdateTicketCommand,
	change *history,
	now time.Time,
) error {
	owner, err := s.person(ctx, ticket.OwnerID, cmd.OwnerID, cmd.ClearOwner)
	if errors.Is(err, errNotStaff) {
		return apperror.InvalidFields("ownerId")
	}

	if err != nil {
		return err
	}

	if cmd.Locked != nil && *cmd.Locked && owner == nil {
		owner = callerID(caller)
	}

	if !sameID(owner, ticket.OwnerID) {
		change.add(model.HistoryOwner, s.nameOf(ctx, ticket.OwnerID), s.nameOf(ctx, owner))
		ticket.OwnerID = owner
	}

	responsible, err := s.person(ctx, ticket.ResponsibleID, cmd.ResponsibleID, cmd.ClearResponsible)
	if errors.Is(err, errNotStaff) {
		return apperror.InvalidFields("responsibleId")
	}

	if err != nil {
		return err
	}

	if !sameID(responsible, ticket.ResponsibleID) {
		change.add(model.HistoryResponsible, s.nameOf(ctx, ticket.ResponsibleID), s.nameOf(ctx, responsible))
		ticket.ResponsibleID = responsible
	}

	if cmd.Locked != nil && *cmd.Locked != ticket.Locked {
		change.add(model.HistoryLock, strconv.FormatBool(ticket.Locked), strconv.FormatBool(*cmd.Locked))
		ticket.Locked = *cmd.Locked

		if ticket.Locked {
			ticket.LockedAt = &now
		} else {
			ticket.LockedAt = nil
		}
	}

	return nil
}

// person is who a ticket names after a change: the one it named, somebody new,
// or nobody where the change clears it.
func (s *TicketApplicationService) person(ctx context.Context, current, given *uint, clear bool) (*uint, error) {
	switch {
	case clear:
		return nil, nil
	case given != nil:
		return s.staff(ctx, given)
	default:
		return current, nil
	}
}

// nameOf is an account's name for the history, or nothing for nobody and for an
// account that is gone.
func (s *TicketApplicationService) nameOf(ctx context.Context, id *uint) string {
	if id == nil {
		return ""
	}

	user, err := s.users.GetByID(ctx, *id)
	if err != nil {
		return "#" + strconv.FormatUint(uint64(*id), 10)
	}

	return user.Name
}

// history collects the lines a change writes.
type history struct {
	ticketID uint
	at       time.Time
	by       model.Sender
	actor    *uint
	entries  []model.HistoryEntry
}

func historyOf(ticketID uint, at time.Time, caller *Principal) *history {
	if caller != nil && caller.System {
		return historyBySystem(ticketID, at)
	}

	return &history{ticketID: ticketID, at: at, by: model.SenderAgent, actor: callerID(caller)}
}

// historyBySystem is the history of what the application did by itself, which
// no account did.
func historyBySystem(ticketID uint, at time.Time) *history {
	return &history{ticketID: ticketID, at: at, by: model.SenderSystem}
}

func (h *history) add(event model.HistoryEvent, was, now string) {
	if was == now {
		return
	}

	h.entries = append(h.entries, model.HistoryEntry{
		TicketID: h.ticketID, At: h.at, Actor: h.by, ActorID: h.actor,
		Event: event, OldValue: was, NewValue: now,
	})
}

// callerID is the caller's account, or nobody where authentication is off and
// there is no account behind the request.
func callerID(caller *Principal) *uint {
	if caller == nil || caller.User == nil || caller.User.ID == 0 {
		return nil
	}

	id := caller.User.ID

	return &id
}

// given is an id a filter was given, or nothing for zero.
func given(id uint) *uint {
	if id == 0 {
		return nil
	}

	return &id
}

func sameID(a, b *uint) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}

func sameMoment(a, b *time.Time) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && a.Equal(*b))
}

func moment(at *time.Time) string {
	if at == nil {
		return ""
	}

	return at.UTC().Format(time.RFC3339)
}

func valueOf(s *string) string {
	if s == nil {
		return ""
	}

	return *s
}
