package service

import (
	"context"
	"slices"
	"strings"

	"github.com/dennis-dko/go-ticket-system/internal/application/v1/command"
	"github.com/dennis-dko/go-ticket-system/internal/domain/model"
	"github.com/dennis-dko/go-ticket-system/internal/domain/repository"
	"github.com/dennis-dko/go-ticket-system/internal/support/apperror"
)

// TicketCatalogService administers what an installation sorts its tickets by:
// the states, the priorities and the types.
type TicketCatalogService interface {
	ListStates(ctx context.Context) ([]*model.TicketState, error)
	GetState(ctx context.Context, id uint) (*model.TicketState, error)
	CreateState(ctx context.Context, cmd command.TicketStateCommand) (*model.TicketState, error)
	UpdateState(ctx context.Context, id uint, cmd command.TicketStateCommand) (*model.TicketState, error)
	DeleteState(ctx context.Context, id uint) error

	ListPriorities(ctx context.Context) ([]*model.TicketPriority, error)
	GetPriority(ctx context.Context, id uint) (*model.TicketPriority, error)
	CreatePriority(ctx context.Context, cmd command.TicketPriorityCommand) (*model.TicketPriority, error)
	UpdatePriority(ctx context.Context, id uint, cmd command.TicketPriorityCommand) (*model.TicketPriority, error)
	DeletePriority(ctx context.Context, id uint) error

	ListTypes(ctx context.Context) ([]*model.TicketType, error)
	GetType(ctx context.Context, id uint) (*model.TicketType, error)
	CreateType(ctx context.Context, cmd command.TicketTypeCommand) (*model.TicketType, error)
	UpdateType(ctx context.Context, id uint, cmd command.TicketTypeCommand) (*model.TicketType, error)
	DeleteType(ctx context.Context, id uint) error

	ListUnlockStateTypes(ctx context.Context) ([]model.UnlockStateType, error)
	SetUnlockStateTypes(ctx context.Context, types []string) ([]model.UnlockStateType, error)
}

// TicketCatalogApplicationService is the TicketCatalogService the application
// runs with. Three rules hold for all three tables, as KIX18 has them with one
// addition Dennis allowed.
//
// A value the code moves tickets into by itself (IsSystem) stays exactly as it
// shipped - neither renamed, retyped, retired nor deleted - because the code
// finds it by its name. That is the addition: KIX18 lets an installation break
// its own defaults.
//
// A value a ticket uses is retired rather than deleted: made invalid, it is no
// longer offered, and the tickets already carrying it keep it. A ticket in no
// state at all would have nothing to say what it is.
//
// And no two values share a name, which the database decides and this passes on.
type TicketCatalogApplicationService struct {
	states      repository.TicketStateRepository
	priorities  repository.TicketPriorityRepository
	types       repository.TicketTypeRepository
	unlockTypes repository.UnlockStateTypeRepository
}

// NewTicketCatalogApplicationService works on the catalog's tables alone; that
// a ticket may only be put into a valid value is the ticket's rule, not theirs.
func NewTicketCatalogApplicationService(catalog TicketCatalogRepositories) *TicketCatalogApplicationService {
	return &TicketCatalogApplicationService{
		states: catalog.States, priorities: catalog.Priorities, types: catalog.Types, unlockTypes: catalog.UnlockTypes,
	}
}

var _ TicketCatalogService = (*TicketCatalogApplicationService)(nil)

func (s *TicketCatalogApplicationService) ListStates(ctx context.Context) ([]*model.TicketState, error) {
	return s.states.GetAll(ctx)
}

func (s *TicketCatalogApplicationService) GetState(ctx context.Context, id uint) (*model.TicketState, error) {
	if id == 0 {
		return nil, apperror.InvalidFields("id")
	}

	return s.states.GetByID(ctx, id)
}

func (s *TicketCatalogApplicationService) CreateState(
	ctx context.Context,
	cmd command.TicketStateCommand,
) (*model.TicketState, error) {
	if cmd.Name == nil || cmd.Type == nil {
		return nil, apperror.InvalidFields(absent(cmd.Name == nil, "name", cmd.Type == nil, "type")...)
	}

	state := &model.TicketState{Valid: true}
	if err := applyState(state, cmd); err != nil {
		return nil, err
	}

	return s.states.Save(ctx, state)
}

func (s *TicketCatalogApplicationService) UpdateState(
	ctx context.Context,
	id uint,
	cmd command.TicketStateCommand,
) (*model.TicketState, error) {
	state, err := s.GetState(ctx, id)
	if err != nil {
		return nil, err
	}

	if state.IsSystem {
		return nil, shipped("state", state.Name).WithCode("stateShipped", state.Name)
	}

	if err := applyState(state, cmd); err != nil {
		return nil, err
	}

	return s.states.Update(ctx, state)
}

func (s *TicketCatalogApplicationService) DeleteState(ctx context.Context, id uint) error {
	state, err := s.GetState(ctx, id)
	if err != nil {
		return err
	}

	if state.IsSystem {
		return shipped("state", state.Name).WithCode("stateShipped", state.Name)
	}

	tickets, err := s.states.CountTickets(ctx, id)
	if err != nil {
		return err
	}

	if tickets > 0 {
		return carried("state", state.Name, tickets).WithCode("stateInUse", state.Name, tickets)
	}

	return s.states.Delete(ctx, id)
}

func (s *TicketCatalogApplicationService) ListUnlockStateTypes(ctx context.Context) ([]model.UnlockStateType, error) {
	return s.unlockTypes.GetAll(ctx)
}

// SetUnlockStateTypes makes the types given the ones a queue's unlock time
// frees a lock in. The shipped ones have to be among them: they stay, and a
// list leaving one out is refused rather than quietly put back, so that what
// was sent is what holds.
func (s *TicketCatalogApplicationService) SetUnlockStateTypes(
	ctx context.Context,
	types []string,
) ([]model.UnlockStateType, error) {
	kinds := make([]model.StateType, 0, len(types))

	for _, sent := range types {
		kind := model.StateType(strings.TrimSpace(sent))
		if !slices.Contains(model.StateTypes(), kind) {
			return nil, apperror.InvalidFields("types")
		}

		if !slices.Contains(kinds, kind) {
			kinds = append(kinds, kind)
		}
	}

	kept, err := s.unlockTypes.GetAll(ctx)
	if err != nil {
		return nil, err
	}

	for _, entry := range kept {
		if entry.IsSystem && !slices.Contains(kinds, entry.Type) {
			return nil, apperror.Conflictf("new and open ship with the application, and a lock runs out in them").
				WithCode("unlockStateTypeShipped")
		}
	}

	if err := s.unlockTypes.Replace(ctx, kinds); err != nil {
		return nil, err
	}

	return s.unlockTypes.GetAll(ctx)
}

func (s *TicketCatalogApplicationService) ListPriorities(ctx context.Context) ([]*model.TicketPriority, error) {
	return s.priorities.GetAll(ctx)
}

func (s *TicketCatalogApplicationService) GetPriority(ctx context.Context, id uint) (*model.TicketPriority, error) {
	if id == 0 {
		return nil, apperror.InvalidFields("id")
	}

	return s.priorities.GetByID(ctx, id)
}

func (s *TicketCatalogApplicationService) CreatePriority(
	ctx context.Context,
	cmd command.TicketPriorityCommand,
) (*model.TicketPriority, error) {
	if cmd.Name == nil || cmd.Level == nil {
		return nil, apperror.InvalidFields(absent(cmd.Name == nil, "name", cmd.Level == nil, "level")...)
	}

	priority := &model.TicketPriority{Valid: true}
	if err := applyPriority(priority, cmd); err != nil {
		return nil, err
	}

	return s.priorities.Save(ctx, priority)
}

func (s *TicketCatalogApplicationService) UpdatePriority(
	ctx context.Context,
	id uint,
	cmd command.TicketPriorityCommand,
) (*model.TicketPriority, error) {
	priority, err := s.GetPriority(ctx, id)
	if err != nil {
		return nil, err
	}

	if priority.IsSystem {
		return nil, shipped("priority", priority.Name).WithCode("priorityShipped", priority.Name)
	}

	if err := applyPriority(priority, cmd); err != nil {
		return nil, err
	}

	return s.priorities.Update(ctx, priority)
}

func (s *TicketCatalogApplicationService) DeletePriority(ctx context.Context, id uint) error {
	priority, err := s.GetPriority(ctx, id)
	if err != nil {
		return err
	}

	if priority.IsSystem {
		return shipped("priority", priority.Name).WithCode("priorityShipped", priority.Name)
	}

	tickets, err := s.priorities.CountTickets(ctx, id)
	if err != nil {
		return err
	}

	if tickets > 0 {
		return carried("priority", priority.Name, tickets).WithCode("priorityInUse", priority.Name, tickets)
	}

	return s.priorities.Delete(ctx, id)
}

func (s *TicketCatalogApplicationService) ListTypes(ctx context.Context) ([]*model.TicketType, error) {
	return s.types.GetAll(ctx)
}

func (s *TicketCatalogApplicationService) GetType(ctx context.Context, id uint) (*model.TicketType, error) {
	if id == 0 {
		return nil, apperror.InvalidFields("id")
	}

	return s.types.GetByID(ctx, id)
}

func (s *TicketCatalogApplicationService) CreateType(
	ctx context.Context,
	cmd command.TicketTypeCommand,
) (*model.TicketType, error) {
	if cmd.Name == nil {
		return nil, apperror.InvalidFields("name")
	}

	kind := &model.TicketType{Valid: true}
	if err := applyType(kind, cmd); err != nil {
		return nil, err
	}

	return s.types.Save(ctx, kind)
}

func (s *TicketCatalogApplicationService) UpdateType(
	ctx context.Context,
	id uint,
	cmd command.TicketTypeCommand,
) (*model.TicketType, error) {
	kind, err := s.GetType(ctx, id)
	if err != nil {
		return nil, err
	}

	if kind.IsSystem {
		return nil, shipped("type", kind.Name).WithCode("typeShipped", kind.Name)
	}

	if err := applyType(kind, cmd); err != nil {
		return nil, err
	}

	return s.types.Update(ctx, kind)
}

func (s *TicketCatalogApplicationService) DeleteType(ctx context.Context, id uint) error {
	kind, err := s.GetType(ctx, id)
	if err != nil {
		return err
	}

	if kind.IsSystem {
		return shipped("type", kind.Name).WithCode("typeShipped", kind.Name)
	}

	tickets, err := s.types.CountTickets(ctx, id)
	if err != nil {
		return err
	}

	if tickets > 0 {
		return carried("type", kind.Name, tickets).WithCode("typeInUse", kind.Name, tickets)
	}

	return s.types.Delete(ctx, id)
}

// applyState writes what cmd says onto state and refuses the result if any of
// it is not a state: no name or one longer than its column, a type the code does
// not know, a description longer than a description is allowed to be.
func applyState(state *model.TicketState, cmd command.TicketStateCommand) error {
	if cmd.Type != nil {
		state.Type = model.StateType(strings.TrimSpace(*cmd.Type))
	}

	applyCatalogEntry(&state.Name, &state.Description, &state.Valid, cmd.Name, cmd.Description, cmd.Valid)

	invalid := catalogEntryProblems(state.Name, state.Description)
	if !slices.Contains(model.StateTypes(), state.Type) {
		invalid = append(invalid, "type")
	}

	if len(invalid) > 0 {
		return apperror.InvalidFields(invalid...)
	}

	return nil
}

// applyPriority is applyState for a priority, whose level orders it among the
// others and is bounded by model.MaxPriorityLevel.
func applyPriority(priority *model.TicketPriority, cmd command.TicketPriorityCommand) error {
	if cmd.Level != nil {
		priority.Level = *cmd.Level
	}

	applyCatalogEntry(&priority.Name, &priority.Description, &priority.Valid, cmd.Name, cmd.Description, cmd.Valid)

	invalid := catalogEntryProblems(priority.Name, priority.Description)
	if priority.Level < 1 || priority.Level > model.MaxPriorityLevel {
		invalid = append(invalid, "level")
	}

	if len(invalid) > 0 {
		return apperror.InvalidFields(invalid...)
	}

	return nil
}

// applyType is applyState for a type, which is a name and nothing that behaves.
func applyType(kind *model.TicketType, cmd command.TicketTypeCommand) error {
	applyCatalogEntry(&kind.Name, &kind.Description, &kind.Valid, cmd.Name, cmd.Description, cmd.Valid)

	if invalid := catalogEntryProblems(kind.Name, kind.Description); len(invalid) > 0 {
		return apperror.InvalidFields(invalid...)
	}

	return nil
}

// applyCatalogEntry writes the three fields every value has, leaving each one
// the command is silent about as it was.
func applyCatalogEntry(name, description *string, valid *bool, newName, newDescription *string, newValid *bool) {
	if newName != nil {
		*name = strings.TrimSpace(*newName)
	}

	if newDescription != nil {
		*description = strings.TrimSpace(*newDescription)
	}

	if newValid != nil {
		*valid = *newValid
	}
}

// catalogEntryProblems names the fields every value has that it cannot be
// stored with.
func catalogEntryProblems(name, description string) []string {
	var invalid []string

	if name == "" || model.TooLong(name, model.MaxCatalogNameLength) {
		invalid = append(invalid, "name")
	}

	if model.TooLong(description, model.MaxDescriptionLength) {
		invalid = append(invalid, "description")
	}

	return invalid
}

// absent names which of a creation's two required fields it is missing.
func absent(firstMissing bool, first string, secondMissing bool, second string) []string {
	var missing []string

	if firstMissing {
		missing = append(missing, first)
	}

	if secondMissing {
		missing = append(missing, second)
	}

	return missing
}

// shipped is the refusal to change or delete a value the code moves tickets into
// by itself. The caller adds the code, written out where it is sent.
func shipped(entity, name string) *apperror.Error {
	return apperror.Conflictf("the %s %q ships with the application and the code relies on it, "+
		"so it stays as it is", entity, name)
}

// carried is the refusal to delete a value tickets still carry, and says how many:
// that number is the only moment anybody learns it, and retiring the value is
// what to do instead. The caller adds the code, as for shipped.
func carried(entity, name string, tickets int) *apperror.Error {
	return apperror.Conflictf("%d ticket(s) carry the %s %q; make it invalid instead of deleting it",
		tickets, entity, name)
}
