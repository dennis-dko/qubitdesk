package service

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/dennis-dko/qubitdesk/internal/domain/model"
	"github.com/dennis-dko/qubitdesk/internal/domain/repository"
	"github.com/dennis-dko/qubitdesk/internal/support/apperror"
)

// WorkOrderApplicationService keeps the work orders a change is carried out in,
// as OTRS::ITSM keeps them. A work order starts in the state OTRS's work order
// state machine starts with and moves only as the machine allows, and the
// moments it began and ended are given to it as OTRS's WorkOrderActualTimesSet
// gives them, wherever nobody has said them. Its planned moments are both given
// or neither, the start before the end, and its actual end needs its start, as
// OTRS's _CheckTimestamps has it. Its agent is an account that may see changes,
// since a work order is read through its change.
//
// The agent reports on their work order - its report, its state, the moments it
// began and ended, the time they accounted - without changes:write, as OTRS's
// WorkOrderAgentCheck grants a work order's agent the work order: the account
// carrying a step out is seldom the one planning the change, and an agent who
// cannot say that their step is done leaves the change waiting on somebody who
// can. Everything else about a work order takes changes:write.
type WorkOrderApplicationService struct {
	orders  repository.WorkOrderRepository
	changes repository.ChangeRepository
	catalog repository.GeneralCatalogRepository
	users   repository.UserRepository
	roles   repository.RoleRepository
	now     func() time.Time
}

// NewWorkOrderApplicationService keeps the work orders of the changes given.
func NewWorkOrderApplicationService(
	orders repository.WorkOrderRepository,
	changes repository.ChangeRepository,
	catalog repository.GeneralCatalogRepository,
	users repository.UserRepository,
	roles repository.RoleRepository,
) *WorkOrderApplicationService {
	return &WorkOrderApplicationService{orders: orders, changes: changes, catalog: catalog, users: users,
		roles: roles, now: time.Now}
}

// WithClock reads the time from now instead of the wall.
func (s *WorkOrderApplicationService) WithClock(now func() time.Time) *WorkOrderApplicationService {
	s.now = now

	return s
}

// WorkOrderCommand is a work order as somebody writes it, a partial one where
// it corrects a work order: nil leaves a field as it is. ClearAgent takes the
// agent away, ClearPlanned both planned moments and ClearActual both actual
// ones, which nil cannot say. Minutes is time accounted on top of what the work
// order has accounted, as a report adds it in OTRS.
type WorkOrderCommand struct {
	Title, Instruction, Report *string
	TypeID, StateID            *uint

	AgentID    *uint
	ClearAgent bool

	PlannedStartAt, PlannedEndAt *time.Time
	ClearPlanned                 bool
	ActualStartAt, ActualEndAt   *time.Time
	ClearActual                  bool

	PlannedMinutes *int
	Minutes        int
}

// plans reports whether a command says more than a report does: what the work
// order is and is for, who carries it out, and what is planned.
func (c WorkOrderCommand) plans() bool {
	return c.Title != nil || c.Instruction != nil || c.TypeID != nil || c.AgentID != nil || c.ClearAgent ||
		c.PlannedStartAt != nil || c.PlannedEndAt != nil || c.ClearPlanned || c.PlannedMinutes != nil
}

// Of is a change's work orders in the order of its plan, numbered; not found
// for a change there is not.
func (s *WorkOrderApplicationService) Of(ctx context.Context, changeID uint) ([]*model.WorkOrder, error) {
	if _, err := s.changes.GetByID(ctx, changeID); err != nil {
		return nil, err
	}

	return s.numbered(ctx, changeID)
}

// Get is one work order, numbered among its change's.
func (s *WorkOrderApplicationService) Get(ctx context.Context, id uint) (*model.WorkOrder, error) {
	order, err := s.orders.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	return s.placed(ctx, order)
}

// Schedules are what the work orders of each change given say of it.
func (s *WorkOrderApplicationService) Schedules(ctx context.Context, changeIDs []uint) (map[uint]model.Schedule, error) {
	orders, err := s.orders.OfChanges(ctx, changeIDs)
	if err != nil {
		return nil, err
	}

	byChange := make(map[uint][]*model.WorkOrder, len(changeIDs))
	for _, order := range orders {
		byChange[order.ChangeID] = append(byChange[order.ChangeID], order)
	}

	schedules := make(map[uint]model.Schedule, len(changeIDs))
	for _, id := range changeIDs {
		schedules[id] = model.ScheduleOf(byChange[id])
	}

	return schedules, nil
}

// NextStates are the states the machine allows a work order in its state to
// move to; none for a work order whose state ends it.
func (s *WorkOrderApplicationService) NextStates(ctx context.Context, order *model.WorkOrder) ([]uint, error) {
	transitions, err := s.orders.Transitions(ctx)
	if err != nil {
		return nil, err
	}

	return nextStates(transitions, order.StateID), nil
}

// NextStatesOf are the states the machine allows each work order given to move
// to, by its id, reading the machine once for them all.
func (s *WorkOrderApplicationService) NextStatesOf(
	ctx context.Context,
	orders []*model.WorkOrder,
) (map[uint][]uint, error) {
	transitions, err := s.orders.Transitions(ctx)
	if err != nil {
		return nil, err
	}

	next := make(map[uint][]uint, len(orders))
	for _, order := range orders {
		next[order.ID] = nextStates(transitions, order.StateID)
	}

	return next, nil
}

// Create adds a work order to a change, by the caller, in the state the
// machine starts with and of OTRS's default type where it is given none.
func (s *WorkOrderApplicationService) Create(
	ctx context.Context,
	caller *Principal,
	changeID uint,
	cmd WorkOrderCommand,
) (*model.WorkOrder, error) {
	if _, err := s.changes.GetByID(ctx, changeID); err != nil {
		return nil, err
	}

	transitions, err := s.orders.Transitions(ctx)
	if err != nil {
		return nil, err
	}

	start, err := startState(transitions, "work order")
	if err != nil {
		return nil, err
	}

	if cmd.StateID != nil && *cmd.StateID != start {
		return nil, apperror.InvalidFields("stateId")
	}

	if cmd.TypeID == nil {
		id, err := catalogNamed(ctx, s.catalog, model.CatalogWorkOrderType, model.WorkOrderDefaultType)
		if err != nil {
			return nil, err
		}

		cmd.TypeID = &id
	}

	now, author := s.now().UTC(), caller.User.ID
	order := &model.WorkOrder{ChangeID: changeID, StateID: start, CreatedAt: now, ChangedAt: now, CreatedBy: author,
		ChangedBy: author}

	if err := s.apply(ctx, order, &model.WorkOrder{}, cmd, true); err != nil {
		return nil, err
	}

	created, err := s.orders.Create(ctx, order)
	if err != nil {
		return nil, err
	}

	return s.placed(ctx, created)
}

// Update corrects a work order, by the caller: what the command names, the
// state only as the machine allows from where it stands. Without changes:write
// it takes only a report, and only from the work order's agent.
func (s *WorkOrderApplicationService) Update(
	ctx context.Context,
	caller *Principal,
	id uint,
	cmd WorkOrderCommand,
) (*model.WorkOrder, error) {
	order, err := s.orders.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	if !caller.Can(model.PermChangeWrite) &&
		(cmd.plans() || order.AgentID == nil || caller.User == nil || *order.AgentID != caller.User.ID) {
		return nil, apperror.Conflictf("without the right to work on changes a work order takes only a report, " +
			"from its agent").WithCode("workOrderReportOnly")
	}

	stood := *order

	if err := s.apply(ctx, order, &stood, cmd, false); err != nil {
		return nil, err
	}

	order.ChangedAt, order.ChangedBy = s.now().UTC(), caller.User.ID

	updated, err := s.orders.Update(ctx, order)
	if err != nil {
		return nil, err
	}

	return s.placed(ctx, updated)
}

// Delete removes a work order.
func (s *WorkOrderApplicationService) Delete(ctx context.Context, id uint) error {
	return s.orders.Delete(ctx, id)
}

// numbered is a change's work orders in the order of its plan, numbered.
func (s *WorkOrderApplicationService) numbered(ctx context.Context, changeID uint) ([]*model.WorkOrder, error) {
	orders, err := s.orders.OfChanges(ctx, []uint{changeID})
	if err != nil {
		return nil, err
	}

	model.NumberWorkOrders(orders)

	return orders, nil
}

// placed is a work order with the number its place in its change's plan gives
// it.
func (s *WorkOrderApplicationService) placed(ctx context.Context, order *model.WorkOrder) (*model.WorkOrder, error) {
	orders, err := s.numbered(ctx, order.ChangeID)
	if err != nil {
		return nil, err
	}

	if at := slices.IndexFunc(orders, func(o *model.WorkOrder) bool { return o.ID == order.ID }); at >= 0 {
		return orders[at], nil
	}

	return order, nil
}

// apply writes what a command says into a work order and checks it against
// what the work order stood at; fresh is a work order still to be made, whose
// title has to be said.
func (s *WorkOrderApplicationService) apply(
	ctx context.Context,
	order, stood *model.WorkOrder,
	cmd WorkOrderCommand,
	fresh bool,
) error {
	if invalid := written(order, cmd, fresh); len(invalid) > 0 {
		return apperror.InvalidFields(invalid...)
	}

	if err := s.typed(ctx, order, stood, cmd.TypeID); err != nil {
		return err
	}

	if err := s.assigned(ctx, order, cmd); err != nil {
		return err
	}

	if cmd.StateID != nil && *cmd.StateID != stood.StateID {
		if err := s.moving(ctx, stood, *cmd.StateID); err != nil {
			return err
		}

		order.StateID = *cmd.StateID

		if err := s.timed(ctx, order); err != nil {
			return err
		}
	}

	return checkedMoments(order)
}

// written writes a command's words, moments and minutes into a work order, and
// names the fields it says wrongly.
func written(order *model.WorkOrder, cmd WorkOrderCommand, fresh bool) []string {
	var invalid []string

	if cmd.Title != nil || fresh {
		order.Title = strings.TrimSpace(valueOf(cmd.Title))
		if order.Title == "" || model.TooLong(order.Title, model.MaxChangeTitleLength) {
			invalid = append(invalid, "title")
		}
	}

	if cmd.Instruction != nil {
		order.Instruction = strings.TrimSpace(*cmd.Instruction)
		if model.TooLong(order.Instruction, model.MaxChangeTextLength) {
			invalid = append(invalid, "instruction")
		}
	}

	if cmd.Report != nil {
		order.Report = strings.TrimSpace(*cmd.Report)
		if model.TooLong(order.Report, model.MaxChangeTextLength) {
			invalid = append(invalid, "report")
		}
	}

	switch planned := cmd.PlannedMinutes; {
	case planned == nil:
	case *planned < 0 || *planned > model.MaxWorkOrderPlannedMinutes:
		invalid = append(invalid, "plannedMinutes")
	default:
		order.PlannedMinutes = *planned
	}

	// Bounded as the time noted with an article is: a report accounts what one
	// sitting took.
	if cmd.Minutes < 0 || cmd.Minutes > model.MaxArticleMinutes {
		invalid = append(invalid, "minutes")
	} else {
		order.AccountedMinutes += cmd.Minutes
	}

	if cmd.ClearAgent && cmd.AgentID != nil {
		invalid = append(invalid, "agentId")
	}

	if cmd.ClearPlanned && (cmd.PlannedStartAt != nil || cmd.PlannedEndAt != nil) {
		invalid = append(invalid, "plannedStartAt")
	}

	if cmd.ClearActual && (cmd.ActualStartAt != nil || cmd.ActualEndAt != nil) {
		invalid = append(invalid, "actualStartAt")
	}

	order.PlannedStartAt = momentWritten(order.PlannedStartAt, cmd.PlannedStartAt, cmd.ClearPlanned)
	order.PlannedEndAt = momentWritten(order.PlannedEndAt, cmd.PlannedEndAt, cmd.ClearPlanned)
	order.ActualStartAt = momentWritten(order.ActualStartAt, cmd.ActualStartAt, cmd.ClearActual)
	order.ActualEndAt = momentWritten(order.ActualEndAt, cmd.ActualEndAt, cmd.ClearActual)

	return invalid
}

// momentWritten is a moment as a command leaves it: taken away, given anew, or
// as it stood.
func momentWritten(standing, given *time.Time, cleared bool) *time.Time {
	switch {
	case cleared:
		return nil
	case given != nil:
		at := given.UTC()

		return &at
	}

	return standing
}

// checkedMoments refuses the moments OTRS's _CheckTimestamps refuses: a planned
// start without a planned end or the other way round, an actual end without an
// actual start, and a start that is not before its end.
func checkedMoments(order *model.WorkOrder) error {
	var invalid []string

	switch planned := [2]*time.Time{order.PlannedStartAt, order.PlannedEndAt}; {
	case planned[0] == nil && planned[1] != nil:
		invalid = append(invalid, "plannedStartAt")
	case planned[0] != nil && (planned[1] == nil || !planned[0].Before(*planned[1])):
		invalid = append(invalid, "plannedEndAt")
	}

	switch actual := [2]*time.Time{order.ActualStartAt, order.ActualEndAt}; {
	case actual[1] != nil && actual[0] == nil:
		invalid = append(invalid, "actualStartAt")
	case actual[1] != nil && !actual[0].Before(*actual[1]):
		invalid = append(invalid, "actualEndAt")
	}

	if len(invalid) > 0 {
		return apperror.InvalidFields(invalid...)
	}

	return nil
}

// typed sets a work order's type: a value of its list, and not a retired one
// where it is newly chosen.
func (s *WorkOrderApplicationService) typed(ctx context.Context, order, stood *model.WorkOrder, given *uint) error {
	if given == nil {
		return nil
	}

	entry, err := catalogEntry(ctx, s.catalog, *given, model.CatalogWorkOrderType)
	if err != nil {
		return err
	}

	if entry == nil {
		return apperror.InvalidFields("workOrderTypeId")
	}

	if err := stillOffered(entry, stood.TypeID == entry.ID); err != nil {
		return err
	}

	order.TypeID = entry.ID

	return nil
}

// assigned sets or takes away a work order's agent: an account whose role may
// see changes.
func (s *WorkOrderApplicationService) assigned(ctx context.Context, order *model.WorkOrder, cmd WorkOrderCommand) error {
	switch {
	case cmd.ClearAgent:
		order.AgentID = nil

		return nil
	case cmd.AgentID == nil || (order.AgentID != nil && *order.AgentID == *cmd.AgentID):
		return nil
	}

	user, role, err := accountWithRole(ctx, s.users, s.roles, *cmd.AgentID)
	if err != nil {
		return err
	}

	if user == nil {
		return apperror.InvalidFields("agentId")
	}

	if role == nil || (!role.Has(model.PermChangeRead) && !role.Has(model.PermChangeWrite)) {
		return apperror.Conflictf("%s may not see changes", user.Name).WithCode("workOrderAgentCannotSee", user.Name)
	}

	order.AgentID = &user.ID

	return nil
}

// moving refuses a step the work order state machine does not allow, and a
// state no longer offered.
func (s *WorkOrderApplicationService) moving(ctx context.Context, stood *model.WorkOrder, to uint) error {
	entry, err := catalogEntry(ctx, s.catalog, to, model.CatalogWorkOrderState)
	if err != nil {
		return err
	}

	if entry == nil {
		return apperror.InvalidFields("stateId")
	}

	next, err := s.NextStates(ctx, stood)
	if err != nil {
		return err
	}

	if slices.Contains(next, to) {
		return stillOffered(entry, false)
	}

	standing, err := s.catalog.GetByID(ctx, stood.StateID)
	if err != nil {
		return err
	}

	return apperror.Conflictf("a work order that is %s does not become %s", standing.Name, entry.Name).
		WithCode("workOrderStateNotAllowed", standing.Name, entry.Name)
}

// timed gives a work order that has just moved the moments OTRS's
// WorkOrderActualTimesSet gives it, wherever nobody has said them: the moment
// it began once it is in progress, and once its state ends it the moment it
// ended as well - a second after now, as OTRS has it, so that a work order
// begun and ended at once still begins before it ends.
func (s *WorkOrderApplicationService) timed(ctx context.Context, order *model.WorkOrder) error {
	state, err := s.catalog.GetByID(ctx, order.StateID)
	if err != nil {
		return err
	}

	next, err := s.NextStates(ctx, order)
	if err != nil {
		return err
	}

	now := s.now().UTC()
	ends := len(next) == 0

	if order.ActualStartAt == nil && (state.Name == model.WorkOrderStartedState || ends) {
		order.ActualStartAt = &now
	}

	if order.ActualEndAt == nil && ends {
		end := now.Add(time.Second)
		order.ActualEndAt = &end
	}

	return nil
}
