package service

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/dennis-dko/qubitdesk/internal/domain/model"
	"github.com/dennis-dko/qubitdesk/internal/domain/repository"
	"github.com/dennis-dko/qubitdesk/internal/support/apperror"
)

// ChangeApplicationService keeps the changes, as OTRS::ITSM's change management
// keeps them. A change is ranked by category, impact and priority, and its
// priority follows OTRS's matrix of the other two unless it is given one; it
// starts in the state OTRS's state machine starts with and moves only as the
// machine allows, which is data, because the states are the installation's. Its
// manager and its builder are accounts that may work on changes. A retired
// value is not chosen anew and stays where the change already names it, the
// rule a ticket keeps for its state.
type ChangeApplicationService struct {
	changes repository.ChangeRepository
	catalog repository.GeneralCatalogRepository
	users   repository.UserRepository
	roles   repository.RoleRepository
	zone    func(ctx context.Context) string
	now     func() time.Time
}

// NewChangeApplicationService keeps the changes. zone answers the installation's
// zone, which a change's number is dated in.
func NewChangeApplicationService(
	changes repository.ChangeRepository,
	catalog repository.GeneralCatalogRepository,
	users repository.UserRepository,
	roles repository.RoleRepository,
	zone func(ctx context.Context) string,
) *ChangeApplicationService {
	return &ChangeApplicationService{changes: changes, catalog: catalog, users: users, roles: roles, zone: zone,
		now: time.Now}
}

// WithClock reads the time from now instead of the wall.
func (s *ChangeApplicationService) WithClock(now func() time.Time) *ChangeApplicationService {
	s.now = now

	return s
}

// ChangeCommand is a change as somebody writes it, a partial one where it
// corrects a change: nil leaves a field as it is, and the Clear fields take a
// manager, a builder or the requested moment away, which nil cannot say.
type ChangeCommand struct {
	Title, Description, Justification *string

	ManagerID    *uint
	ClearManager bool
	BuilderID    *uint
	ClearBuilder bool

	CategoryID, ImpactID, PriorityID *uint
	StateID                          *uint

	RequestedAt      *time.Time
	ClearRequestedAt bool
}

// ChangeListFilter is what a listing of changes asks for.
type ChangeListFilter struct {
	StateIDs []uint
	Search   string
	Limit    int
	Page     int
}

// normal is the middle of the scale, which a change is ranked at where nobody
// says otherwise - OTRS's default for category and impact.
const normal = "3-normal"

// List is one page of the changes the filter allows, the most recently changed
// first, and how many it allows altogether.
func (s *ChangeApplicationService) List(ctx context.Context, filter ChangeListFilter) ([]*model.Change, uint, error) {
	if filter.Limit == 0 {
		filter.Limit = DefaultTicketPage
	}

	if filter.Limit < 0 || filter.Limit > MaxTicketPage {
		return nil, 0, apperror.InvalidFields("limit")
	}

	if filter.Page < 0 {
		return nil, 0, apperror.InvalidFields("page")
	}

	return s.changes.List(ctx, repository.ChangeFilter{
		StateIDs: filter.StateIDs, Search: filter.Search, Limit: filter.Limit, Page: filter.Page,
	})
}

func (s *ChangeApplicationService) Get(ctx context.Context, id uint) (*model.Change, error) {
	return s.changes.GetByID(ctx, id)
}

// NextStates are the states the machine allows a change in its state to move
// to; none for a change whose state ends it.
func (s *ChangeApplicationService) NextStates(ctx context.Context, change *model.Change) ([]uint, error) {
	transitions, err := s.changes.Transitions(ctx)
	if err != nil {
		return nil, err
	}

	return nextStates(transitions, change.StateID), nil
}

// Create makes a change, by the caller, in the state the machine starts with.
func (s *ChangeApplicationService) Create(ctx context.Context, caller *Principal, cmd ChangeCommand) (*model.Change, error) {
	transitions, err := s.changes.Transitions(ctx)
	if err != nil {
		return nil, err
	}

	start, err := startState(transitions, "change")
	if err != nil {
		return nil, err
	}

	if cmd.StateID != nil && *cmd.StateID != start {
		return nil, apperror.InvalidFields("stateId")
	}

	now, author := s.now().UTC(), caller.User.ID
	change := &model.Change{StateID: start, CreatedAt: now, ChangedAt: now, CreatedBy: author, ChangedBy: author}

	for _, defaulted := range []struct {
		given **uint
		class string
	}{{&cmd.CategoryID, model.CatalogChangeCategory}, {&cmd.ImpactID, model.CatalogChangeImpact}} {
		if *defaulted.given != nil {
			continue
		}

		id, err := catalogNamed(ctx, s.catalog, defaulted.class, normal)
		if err != nil {
			return nil, err
		}

		*defaulted.given = &id
	}

	if err := s.apply(ctx, change, cmd, true); err != nil {
		return nil, err
	}

	if err := s.stillOfferedWhereChosen(ctx, &model.Change{}, change); err != nil {
		return nil, err
	}

	return s.changes.Create(ctx, change, s.numbered(ctx, now))
}

// Update corrects a change, by the caller: what the command names, the state
// only as the machine allows from where it stands.
func (s *ChangeApplicationService) Update(
	ctx context.Context,
	caller *Principal,
	id uint,
	cmd ChangeCommand,
) (*model.Change, error) {
	change, err := s.changes.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	stood := *change

	if cmd.StateID != nil && *cmd.StateID != change.StateID {
		if err := s.moving(ctx, &stood, *cmd.StateID); err != nil {
			return nil, err
		}

		change.StateID = *cmd.StateID
	}

	if err := s.apply(ctx, change, cmd, false); err != nil {
		return nil, err
	}

	if err := s.stillOfferedWhereChosen(ctx, &stood, change); err != nil {
		return nil, err
	}

	change.ChangedAt, change.ChangedBy = s.now().UTC(), caller.User.ID

	return s.changes.Update(ctx, change)
}

// Delete removes a change, with its work orders and its links.
func (s *ChangeApplicationService) Delete(ctx context.Context, id uint) error {
	return s.changes.Delete(ctx, id)
}

// Names are the accounts' names by id, for a change's manager, builder and
// authors. An account erased since is in none of them, and is shown by its
// number.
func (s *ChangeApplicationService) Names(ctx context.Context) (map[uint]string, error) {
	users, err := s.users.GetAll(ctx)
	if err != nil {
		return nil, err
	}

	names := make(map[uint]string, len(users))
	for _, user := range users {
		names[user.ID] = user.Name
	}

	return names, nil
}

// ChangePerson is an account whose role may see changes, and whether it may
// work on them.
type ChangePerson struct {
	ID    uint
	Name  string
	Works bool
}

// People are the accounts whose role may see changes, by name: those that may
// work on them are whom a change can be managed and built by, and any of them
// is whom a work order can be given to - what the screen offers for each, so
// that it never offers somebody the server would refuse.
func (s *ChangeApplicationService) People(ctx context.Context) ([]*ChangePerson, error) {
	roles, err := s.roles.GetAll(ctx)
	if err != nil {
		return nil, err
	}

	sees, works := map[uint]bool{}, map[uint]bool{}

	for _, role := range roles {
		works[role.ID] = role.Has(model.PermChangeWrite)
		sees[role.ID] = works[role.ID] || role.Has(model.PermChangeRead)
	}

	users, err := s.users.GetAll(ctx)
	if err != nil {
		return nil, err
	}

	people := []*ChangePerson{}

	for _, user := range users {
		if sees[user.RoleID] {
			people = append(people, &ChangePerson{ID: user.ID, Name: user.Name, Works: works[user.RoleID]})
		}
	}

	slices.SortFunc(people, func(a, b *ChangePerson) int { return strings.Compare(a.Name, b.Name) })

	return people, nil
}

// moving refuses a step the state machine does not allow.
func (s *ChangeApplicationService) moving(ctx context.Context, stood *model.Change, to uint) error {
	entry, err := catalogEntry(ctx, s.catalog, to, model.CatalogChangeState)
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

	return apperror.Conflictf("a change that is %s does not become %s", standing.Name, entry.Name).
		WithCode("changeStateNotAllowed", standing.Name, entry.Name)
}

// apply writes what a command says into a change and checks it; fresh is a
// change still to be made, whose title has to be said.
func (s *ChangeApplicationService) apply(ctx context.Context, change *model.Change, cmd ChangeCommand, fresh bool) error {
	var invalid []string

	if cmd.Title != nil || fresh {
		change.Title = strings.TrimSpace(valueOf(cmd.Title))
		if change.Title == "" || model.TooLong(change.Title, model.MaxChangeTitleLength) {
			invalid = append(invalid, "title")
		}
	}

	if cmd.Description != nil {
		change.Description = strings.TrimSpace(*cmd.Description)
		if model.TooLong(change.Description, model.MaxChangeTextLength) {
			invalid = append(invalid, "description")
		}
	}

	if cmd.Justification != nil {
		change.Justification = strings.TrimSpace(*cmd.Justification)
		if model.TooLong(change.Justification, model.MaxChangeTextLength) {
			invalid = append(invalid, "justification")
		}
	}

	if cmd.ClearRequestedAt && cmd.RequestedAt != nil {
		invalid = append(invalid, "requestedAt")
	}

	if len(invalid) > 0 {
		return apperror.InvalidFields(invalid...)
	}

	switch {
	case cmd.ClearRequestedAt:
		change.RequestedAt = nil
	case cmd.RequestedAt != nil:
		at := cmd.RequestedAt.UTC()
		change.RequestedAt = &at
	}

	if (cmd.ClearManager && cmd.ManagerID != nil) || (cmd.ClearBuilder && cmd.BuilderID != nil) {
		return apperror.InvalidFields("managerId", "builderId")
	}

	switch manager, err := s.worker(ctx, change.ManagerID, cmd.ManagerID); {
	case cmd.ClearManager:
		change.ManagerID = nil
	case err != nil:
		return err
	case manager == nil && cmd.ManagerID != nil:
		return apperror.InvalidFields("managerId")
	case manager != nil && !manager.works:
		return apperror.Conflictf("%s may not work on changes", manager.name).
			WithCode("changeManagerCannotWork", manager.name)
	case manager != nil:
		change.ManagerID = &manager.id
	}

	switch builder, err := s.worker(ctx, change.BuilderID, cmd.BuilderID); {
	case cmd.ClearBuilder:
		change.BuilderID = nil
	case err != nil:
		return err
	case builder == nil && cmd.BuilderID != nil:
		return apperror.InvalidFields("builderId")
	case builder != nil && !builder.works:
		return apperror.Conflictf("%s may not work on changes", builder.name).
			WithCode("changeBuilderCannotWork", builder.name)
	case builder != nil:
		change.BuilderID = &builder.id
	}

	return s.rank(ctx, change, cmd)
}

// changeWorker is an account named as a change's manager or builder: who it
// is, and whether its role may work on changes.
type changeWorker struct {
	id    uint
	name  string
	works bool
}

// worker is the account a command names as a manager or builder, or nil where
// it names nobody new - none, or the one already named - or an account there
// is not.
func (s *ChangeApplicationService) worker(ctx context.Context, standing, given *uint) (*changeWorker, error) {
	if given == nil || (standing != nil && *standing == *given) {
		return nil, nil
	}

	user, role, err := accountWithRole(ctx, s.users, s.roles, *given)
	if user == nil || err != nil {
		return nil, err
	}

	return &changeWorker{id: user.ID, name: user.Name, works: role != nil && role.Has(model.PermChangeWrite)}, nil
}

// accountWithRole is the account an id names and the role it holds: no account
// where the id names none, and no role where the account holds none there is.
func accountWithRole(
	ctx context.Context,
	users repository.UserRepository,
	roles repository.RoleRepository,
	id uint,
) (*model.User, *model.Role, error) {
	user, err := users.GetByID(ctx, id)
	if apperror.KindOf(err) == apperror.KindNotFound {
		return nil, nil, nil
	}

	if err != nil {
		return nil, nil, err
	}

	role, err := roles.GetByID(ctx, user.RoleID)
	if apperror.KindOf(err) == apperror.KindNotFound {
		return user, nil, nil
	}

	if err != nil {
		return nil, nil, err
	}

	return user, role, nil
}

// nextStates are the states a state machine lets one move to from the state
// given; none from a state that ends it.
func nextStates(transitions []model.StateTransition, from uint) []uint {
	next := []uint{}

	for _, step := range transitions {
		if step.FromID != nil && *step.FromID == from {
			next = append(next, step.ToID)
		}
	}

	return next
}

// startState is the state a state machine starts in: the one no state leads to.
func startState(transitions []model.StateTransition, machine string) (uint, error) {
	start := slices.IndexFunc(transitions, func(step model.StateTransition) bool { return step.FromID == nil })
	if start < 0 {
		return 0, apperror.Internal(fmt.Errorf("the %s state machine has no state to start in", machine))
	}

	return transitions[start].ToID, nil
}

// rank sets a change's category, impact and priority: each a value of its
// list, and the priority the matrix gives the other two wherever they change
// and the priority is not given.
func (s *ChangeApplicationService) rank(ctx context.Context, change *model.Change, cmd ChangeCommand) error {
	var invalid []string

	category, err := s.ranked(ctx, &change.CategoryID, cmd.CategoryID, model.CatalogChangeCategory)
	if err != nil {
		return err
	}

	if !category {
		invalid = append(invalid, "categoryId")
	}

	impact, err := s.ranked(ctx, &change.ImpactID, cmd.ImpactID, model.CatalogChangeImpact)
	if err != nil {
		return err
	}

	if !impact {
		invalid = append(invalid, "impactId")
	}

	priority, err := s.ranked(ctx, &change.PriorityID, cmd.PriorityID, model.CatalogChangePriority)
	if err != nil {
		return err
	}

	if !priority {
		invalid = append(invalid, "priorityId")
	}

	if len(invalid) > 0 {
		return apperror.InvalidFields(invalid...)
	}

	if cmd.PriorityID == nil && (cmd.CategoryID != nil || cmd.ImpactID != nil) {
		given, err := s.changes.Priority(ctx, change.CategoryID, change.ImpactID)
		if apperror.KindOf(err) == apperror.KindNotFound {
			return apperror.InvalidFields("priorityId")
		}

		if err != nil {
			return err
		}

		change.PriorityID = given
	}

	return nil
}

// ranked writes a value of a list into a field where one is given, and reports
// whether the given one is a value of that list; nothing given is fine.
func (s *ChangeApplicationService) ranked(ctx context.Context, field, given *uint, class string) (bool, error) {
	if given == nil {
		return true, nil
	}

	entry, err := catalogEntry(ctx, s.catalog, *given, class)
	if err != nil || entry == nil {
		return false, err
	}

	*field = entry.ID

	return true, nil
}

// stillOfferedWhereChosen refuses a retired category, impact or priority newly
// chosen, and leaves one the change already names. A change still to be made
// names nothing yet.
func (s *ChangeApplicationService) stillOfferedWhereChosen(ctx context.Context, stood, change *model.Change) error {
	for _, scale := range []struct{ was, is uint }{
		{stood.CategoryID, change.CategoryID}, {stood.ImpactID, change.ImpactID},
		{stood.PriorityID, change.PriorityID},
	} {
		entry, err := s.catalog.GetByID(ctx, scale.is)
		if err != nil {
			return err
		}

		if err := stillOffered(entry, scale.was == scale.is); err != nil {
			return err
		}
	}

	return nil
}

// catalogNamed is the id of the value a list of the general catalog ships under
// the name given.
func catalogNamed(ctx context.Context, catalog repository.GeneralCatalogRepository, class, name string) (uint, error) {
	entries, err := catalog.Items(ctx, class)
	if err != nil {
		return 0, err
	}

	for _, entry := range entries {
		if entry.Name == name {
			return entry.ID, nil
		}
	}

	return 0, apperror.Internal(fmt.Errorf("the catalog ships no %s %s", class, name))
}

// numbered numbers a change by the day it is made in the installation's zone
// and its id, as a ticket is numbered.
func (s *ChangeApplicationService) numbered(ctx context.Context, now time.Time) func(id uint) string {
	zone, err := time.LoadLocation(s.zone(ctx))
	if err != nil {
		zone = time.UTC
	}

	day := now.In(zone).Format("20060102")

	return func(id uint) string { return fmt.Sprintf("%s%08d", day, id) }
}

// catalogEntry is the value of the general catalog's class an id names, or nil
// where it names no value of that class. An error is one of reading, never of
// the id.
func catalogEntry(
	ctx context.Context,
	catalog repository.GeneralCatalogRepository,
	id uint,
	class string,
) (*model.CatalogItem, error) {
	if id == 0 {
		return nil, nil
	}

	entry, err := catalog.GetByID(ctx, id)
	if apperror.KindOf(err) == apperror.KindNotFound {
		return nil, nil
	}

	if err != nil {
		return nil, err
	}

	if entry.Class != class {
		return nil, nil
	}

	return entry, nil
}
