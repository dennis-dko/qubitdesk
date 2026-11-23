package service

import (
	"context"

	"github.com/dennis-dko/qubitdesk/internal/domain/model"
)

// AutomationChoices are what a macro's step and a job's criteria can name, read
// for whoever may set the automation up. A rule names records by id, and
// choosing one takes its name: KIX18 offers every value of every list to whoever
// may build a job, and so does every help desk whose administrators write rules.
// Reading them through the rights that read them for work - the tickets, the
// accounts, the customers - left a role given the automation and not those an
// empty screen, while the server took a step naming any record that is there.
// So the automation has a reading of its own, of what choosing takes and no
// more: ids, names, where a queue hangs, what a state is, and whether a value
// is still offered.
type AutomationChoices struct {
	Queues     []*model.Queue
	States     []*model.TicketState
	Priorities []*model.TicketPriority
	Types      []*model.TicketType
	Accounts   []*model.User
}

// Choices are every queue, state, priority and type, and the accounts a ticket
// can be given to: those whose role may work on tickets.
func (s *MacroApplicationService) Choices(ctx context.Context) (*AutomationChoices, error) {
	var (
		choices AutomationChoices
		err     error
	)

	if choices.Queues, err = s.refs.Queues.GetAll(ctx); err != nil {
		return nil, err
	}

	if choices.States, err = s.refs.Catalog.States.GetAll(ctx); err != nil {
		return nil, err
	}

	if choices.Priorities, err = s.refs.Catalog.Priorities.GetAll(ctx); err != nil {
		return nil, err
	}

	if choices.Types, err = s.refs.Catalog.Types.GetAll(ctx); err != nil {
		return nil, err
	}

	if choices.Accounts, err = s.workers(ctx); err != nil {
		return nil, err
	}

	return &choices, nil
}

// workers are the accounts whose role may work on tickets, or every account
// where the roles are not known.
func (s *MacroApplicationService) workers(ctx context.Context) ([]*model.User, error) {
	users, err := s.refs.Users.GetAll(ctx)
	if err != nil || s.refs.Roles == nil {
		return users, err
	}

	roles, err := s.refs.Roles.GetAll(ctx)
	if err != nil {
		return nil, err
	}

	works := map[uint]bool{}
	for _, role := range roles {
		works[role.ID] = role.Has(model.PermTicketWrite)
	}

	workers := []*model.User{}

	for _, user := range users {
		if works[user.RoleID] {
			workers = append(workers, user)
		}
	}

	return workers, nil
}

// FindContacts are a page of the contacts a step can name whose name or address
// holds the words - there are too many to list whole - bounded as the
// customers' own listing is.
func (s *MacroApplicationService) FindContacts(ctx context.Context, filter CustomerListFilter) ([]*model.Contact, error) {
	page, err := customerPage(filter)
	if err != nil {
		return nil, err
	}

	contacts, _, err := s.refs.Customers.Contacts.List(ctx, page)

	return contacts, err
}

// FindOrganisations are a page of the organisations a step can name whose name
// or number holds the words.
func (s *MacroApplicationService) FindOrganisations(
	ctx context.Context,
	filter CustomerListFilter,
) ([]*model.Organisation, error) {
	page, err := customerPage(filter)
	if err != nil {
		return nil, err
	}

	organisations, _, err := s.refs.Customers.Organisations.List(ctx, page)

	return organisations, err
}

// Contact is the contact a step names, for its name beside the choice.
func (s *MacroApplicationService) Contact(ctx context.Context, id uint) (*model.Contact, error) {
	return s.refs.Customers.Contacts.GetByID(ctx, id)
}

// Organisation is the organisation a step names, for its name beside the
// choice.
func (s *MacroApplicationService) Organisation(ctx context.Context, id uint) (*model.Organisation, error) {
	return s.refs.Customers.Organisations.GetByID(ctx, id)
}
