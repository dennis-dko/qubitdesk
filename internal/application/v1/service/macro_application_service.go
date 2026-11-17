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

// MacroReferences are the tables the parameters of a macro's steps name records
// in.
type MacroReferences struct {
	Catalog   TicketCatalogRepositories
	Customers TicketCustomerRepositories
	Queues    repository.QueueRepository
	Users     repository.UserRepository
}

// MacroApplicationService administers the macros: what the automation does to a
// ticket, step by step. Every step is checked as it is saved against what it
// names - a record that is there and still offered, a value its parameter can
// hold - so that a macro which saves is one a job can run. What a ticket allows
// when the step runs is still the ticket's to say.
type MacroApplicationService struct {
	macros repository.MacroRepository
	refs   MacroReferences
}

// NewMacroApplicationService reads the records a step may name.
func NewMacroApplicationService(macros repository.MacroRepository, refs MacroReferences) *MacroApplicationService {
	return &MacroApplicationService{macros: macros, refs: refs}
}

func (s *MacroApplicationService) List(ctx context.Context) ([]*model.Macro, error) {
	return s.macros.GetAll(ctx)
}

func (s *MacroApplicationService) Get(ctx context.Context, id uint) (*model.Macro, error) {
	if id == 0 {
		return nil, apperror.InvalidFields("id")
	}

	return s.macros.GetByID(ctx, id)
}

func (s *MacroApplicationService) Create(ctx context.Context, cmd command.MacroCommand) (*model.Macro, error) {
	if cmd.Name == nil {
		return nil, apperror.InvalidFields("name")
	}

	macro := &model.Macro{Valid: true, Actions: []model.MacroAction{}}
	if err := s.apply(ctx, macro, cmd); err != nil {
		return nil, err
	}

	return s.macros.Save(ctx, macro)
}

func (s *MacroApplicationService) Update(ctx context.Context, id uint, cmd command.MacroCommand) (*model.Macro, error) {
	macro, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}

	if err := s.apply(ctx, macro, cmd); err != nil {
		return nil, err
	}

	return s.macros.Update(ctx, macro)
}

// Delete deletes a macro no job runs. One a job runs is refused, with how many,
// and is retired instead - made invalid - so that the jobs keep it and skip it.
func (s *MacroApplicationService) Delete(ctx context.Context, id uint) error {
	macro, err := s.Get(ctx, id)
	if err != nil {
		return err
	}

	jobs, err := s.macros.CountJobs(ctx, id)
	if err != nil {
		return err
	}

	if jobs > 0 {
		return apperror.Conflictf("%d job(s) run the macro %q", jobs, macro.Name).WithCode("macroInUse", macro.Name, jobs)
	}

	return s.macros.Delete(ctx, id)
}

// apply writes a command into a macro, refusing the fields it cannot hold at
// once and then the first step that cannot be taken.
func (s *MacroApplicationService) apply(ctx context.Context, macro *model.Macro, cmd command.MacroCommand) error {
	var invalid []string

	name := strings.TrimSpace(valueOf(cmd.Name))
	if cmd.Name != nil && (name == "" || model.TooLong(name, model.MaxCatalogNameLength)) {
		invalid = append(invalid, "name")
	}

	description := strings.TrimSpace(valueOf(cmd.Description))
	if model.TooLong(description, model.MaxDescriptionLength) {
		invalid = append(invalid, "description")
	}

	if cmd.Actions != nil && len(*cmd.Actions) > model.MaxMacroActions {
		invalid = append(invalid, "actions")
	}

	if len(invalid) > 0 {
		return apperror.InvalidFields(invalid...)
	}

	if cmd.Name != nil {
		macro.Name = name
	}

	if cmd.Description != nil {
		macro.Description = description
	}

	if cmd.Valid != nil {
		macro.Valid = *cmd.Valid
	}

	if cmd.Actions == nil {
		return nil
	}

	actions := make([]model.MacroAction, 0, len(*cmd.Actions))

	for i, sent := range *cmd.Actions {
		action, err := s.step(ctx, i+1, sent)
		if err != nil {
			return err
		}

		actions = append(actions, action)
	}

	macro.Actions = actions

	return nil
}

// step is a step as it is kept: of a type there is, with the parameters its
// type defines and no other, each holding what its kind allows. A value left
// empty is left out, which for a person, a contact or an organisation is what
// clears the ticket's.
func (s *MacroApplicationService) step(
	ctx context.Context,
	position int,
	sent command.MacroActionCommand,
) (model.MacroAction, error) {
	definition, ok := model.MacroActionDefinitionOf(model.MacroActionType(strings.TrimSpace(sent.Type)))
	if !ok {
		return model.MacroAction{}, refusedStep(position, "type")
	}

	for name := range sent.Parameters {
		if !slices.ContainsFunc(definition.Parameters, func(p model.MacroParameter) bool { return p.Name == name }) {
			return model.MacroAction{}, refusedStep(position, name)
		}
	}

	parameters := map[string]string{}

	for _, parameter := range definition.Parameters {
		value := strings.TrimSpace(sent.Parameters[parameter.Name])
		if value == "" {
			if parameter.Required {
				return model.MacroAction{}, refusedStep(position, parameter.Name)
			}

			continue
		}

		holds, err := s.holds(ctx, parameter, value)
		if err != nil {
			return model.MacroAction{}, err
		}

		if !holds {
			return model.MacroAction{}, refusedStep(position, parameter.Name)
		}

		parameters[parameter.Name] = value
	}

	if definition.Type == model.ActionStateSet {
		if err := s.waitsAsItShould(ctx, position, parameters); err != nil {
			return model.MacroAction{}, err
		}
	}

	return model.MacroAction{Type: definition.Type, Parameters: parameters}, nil
}

// waitsAsItShould holds a step setting a state to the moment it names: one that
// waits wants to be told how long, from the moment the step runs, and one that
// waits for nothing refuses a time it would only drop.
func (s *MacroApplicationService) waitsAsItShould(ctx context.Context, position int, parameters map[string]string) error {
	id, _ := strconv.ParseUint(parameters["stateId"], 10, 0)

	state, err := s.refs.Catalog.States.GetByID(ctx, uint(id))
	if err != nil {
		return err
	}

	waits := state.Type == model.StateTypePendingAuto || state.Type == model.StateTypePendingReminder
	minutes, given := parameters["pendingMinutes"]

	switch {
	case waits && (!given || minutes == "0"):
		return refusedStep(position, "pendingMinutes")
	case !waits && given:
		return refusedStep(position, "pendingMinutes")
	}

	return nil
}

// holds says whether a value is one its parameter can hold: a record that is
// there and still offered, a text within its bound, a switch, a number of
// minutes, a channel an agent writes through.
func (s *MacroApplicationService) holds(ctx context.Context, parameter model.MacroParameter, value string) (bool, error) {
	switch parameter.Kind {
	case model.ParameterText, model.ParameterLongText:
		return !model.TooLong(value, parameter.Max), nil
	case model.ParameterSwitch:
		return value == "true" || value == "false", nil
	case model.ParameterMinutes:
		minutes, err := strconv.Atoi(value)

		return err == nil && minutes >= 0 && minutes <= parameter.Max, nil
	case model.ParameterChannel:
		return slices.Contains(model.AgentChannels(), model.Channel(value)), nil
	}

	id, err := strconv.ParseUint(value, 10, 0)
	if err != nil || id == 0 {
		return false, nil
	}

	offered, err := s.offered(ctx, parameter.Kind, uint(id))
	if apperror.KindOf(err) == apperror.KindNotFound {
		return false, nil
	}

	return offered, err
}

// offered says whether the record a parameter names is still offered: merged is
// never chosen, and a retired value is not chosen anew.
func (s *MacroApplicationService) offered(ctx context.Context, kind model.MacroParameterKind, id uint) (bool, error) {
	switch kind {
	case model.ParameterState:
		state, err := s.refs.Catalog.States.GetByID(ctx, id)
		if err != nil {
			return false, err
		}

		return state.Valid && state.Type != model.StateTypeMerged, nil
	case model.ParameterPriority:
		priority, err := s.refs.Catalog.Priorities.GetByID(ctx, id)
		if err != nil {
			return false, err
		}

		return priority.Valid, nil
	case model.ParameterType:
		ticketType, err := s.refs.Catalog.Types.GetByID(ctx, id)
		if err != nil {
			return false, err
		}

		return ticketType.Valid, nil
	case model.ParameterQueue:
		queue, err := s.refs.Queues.GetByID(ctx, id)
		if err != nil {
			return false, err
		}

		return queue.Valid, nil
	case model.ParameterContact:
		contact, err := s.refs.Customers.Contacts.GetByID(ctx, id)
		if err != nil {
			return false, err
		}

		return contact.Valid, nil
	case model.ParameterOrganisation:
		organisation, err := s.refs.Customers.Organisations.GetByID(ctx, id)
		if err != nil {
			return false, err
		}

		return organisation.Valid, nil
	case model.ParameterUser:
		_, err := s.refs.Users.GetByID(ctx, id)

		return err == nil, err
	}

	return false, nil
}

// refusedStep says which step of a macro cannot be taken, and which of its
// parameters is why - its type, where the step names one there is not.
func refusedStep(position int, parameter string) error {
	return apperror.Invalidf("step %d of the macro cannot hold %s", position, parameter).
		WithCode("macroStepInvalid", position, parameter)
}
