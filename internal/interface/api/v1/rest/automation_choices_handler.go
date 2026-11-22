package rest

import (
	"gofr.dev/pkg/gofr"

	"github.com/dennis-dko/qubitdesk/internal/domain/model"
)

// AutomationChoice is one value a step or a job's criteria can name: what
// choosing it takes, and no more.
type AutomationChoice struct {
	ID    uint   `json:"id"`
	Name  string `json:"name"`
	Valid bool   `json:"valid"`
}

// AutomationQueueChoice is a queue as a choice, with where it hangs.
type AutomationQueueChoice struct {
	AutomationChoice
	ParentID *uint `json:"parentId"`
}

// AutomationStateChoice is a state as a choice, with what it is.
type AutomationStateChoice struct {
	AutomationChoice
	Type string `json:"type"`
}

// AutomationContactChoice is a contact as a choice: the address tells two of
// one name apart.
type AutomationContactChoice struct {
	AutomationChoice
	Email string `json:"email"`
}

// AutomationOrganisationChoice is an organisation as a choice, with its number.
type AutomationOrganisationChoice struct {
	AutomationChoice
	Number string `json:"number"`
}

// AutomationChoicesResponse is what a step and a job's criteria can name, but
// the contacts and the organisations, which are searched.
type AutomationChoicesResponse struct {
	Queues     []AutomationQueueChoice `json:"queues"`
	States     []AutomationStateChoice `json:"states"`
	Priorities []AutomationChoice      `json:"priorities"`
	Types      []AutomationChoice      `json:"types"`
	Accounts   []AutomationChoice      `json:"accounts"`
}

// Choices handles GET /api/v1/automation/choices.
func (h *MacroHandler) Choices(c *gofr.Context) (any, error) {
	if _, err := h.authz.Require(c, model.PermAutomationWrite); err != nil {
		return nil, err
	}

	choices, err := h.macros.Choices(c)
	if err != nil {
		return nil, toHTTPError(err)
	}

	answer := AutomationChoicesResponse{
		Queues: []AutomationQueueChoice{}, States: []AutomationStateChoice{}, Priorities: []AutomationChoice{},
		Types: []AutomationChoice{}, Accounts: []AutomationChoice{},
	}

	for _, queue := range choices.Queues {
		answer.Queues = append(answer.Queues,
			AutomationQueueChoice{AutomationChoice{queue.ID, queue.Name, queue.Valid}, queue.ParentID})
	}

	for _, state := range choices.States {
		answer.States = append(answer.States,
			AutomationStateChoice{AutomationChoice{state.ID, state.Name, state.Valid}, string(state.Type)})
	}

	for _, priority := range choices.Priorities {
		answer.Priorities = append(answer.Priorities, AutomationChoice{priority.ID, priority.Name, priority.Valid})
	}

	for _, kind := range choices.Types {
		answer.Types = append(answer.Types, AutomationChoice{kind.ID, kind.Name, kind.Valid})
	}

	for _, account := range choices.Accounts {
		answer.Accounts = append(answer.Accounts, AutomationChoice{account.ID, account.Name, true})
	}

	return answer, nil
}

// Contacts handles GET /api/v1/automation/contacts: a page of the contacts a
// step can name, narrowed by ?q= and ?valid=true, or with ?id= the one a step
// names.
func (h *MacroHandler) Contacts(c *gofr.Context) (any, error) {
	if _, err := h.authz.Require(c, model.PermAutomationWrite); err != nil {
		return nil, err
	}

	id, err := queryUint(c, "id")
	if err != nil {
		return nil, toHTTPError(err)
	}

	var contacts []*model.Contact

	if id != 0 {
		contact, err := h.macros.Contact(c, id)
		if err != nil {
			return nil, toHTTPError(err)
		}

		contacts = []*model.Contact{contact}
	} else {
		filter, err := customerListFilter(c)
		if err != nil {
			return nil, toHTTPError(err)
		}

		if contacts, err = h.macros.FindContacts(c, filter); err != nil {
			return nil, toHTTPError(err)
		}
	}

	return listOf(contacts, func(contact *model.Contact) AutomationContactChoice {
		return AutomationContactChoice{AutomationChoice{contact.ID, contact.Name, contact.Valid}, contact.Email}
	}), nil
}

// Organisations handles GET /api/v1/automation/organisations: a page of the
// organisations a step can name, narrowed by ?q= and ?valid=true, or with ?id=
// the one a step names.
func (h *MacroHandler) Organisations(c *gofr.Context) (any, error) {
	if _, err := h.authz.Require(c, model.PermAutomationWrite); err != nil {
		return nil, err
	}

	id, err := queryUint(c, "id")
	if err != nil {
		return nil, toHTTPError(err)
	}

	var organisations []*model.Organisation

	if id != 0 {
		organisation, err := h.macros.Organisation(c, id)
		if err != nil {
			return nil, toHTTPError(err)
		}

		organisations = []*model.Organisation{organisation}
	} else {
		filter, err := customerListFilter(c)
		if err != nil {
			return nil, toHTTPError(err)
		}

		if organisations, err = h.macros.FindOrganisations(c, filter); err != nil {
			return nil, toHTTPError(err)
		}
	}

	return listOf(organisations, func(organisation *model.Organisation) AutomationOrganisationChoice {
		return AutomationOrganisationChoice{
			AutomationChoice{organisation.ID, organisation.Name, organisation.Valid}, organisation.Number,
		}
	}), nil
}
