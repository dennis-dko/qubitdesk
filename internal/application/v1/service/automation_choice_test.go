package service_test

import (
	"context"
	"testing"

	"github.com/dennis-dko/qubitdesk/internal/application/v1/service"
	"github.com/dennis-dko/qubitdesk/internal/domain/model"
	"github.com/dennis-dko/qubitdesk/internal/infrastructure/persistence/memory"
	"github.com/dennis-dko/qubitdesk/internal/support/apperror"
)

// The automation's choices are every queue, state, priority and type, and the
// accounts a ticket can be given to - not a customer's - and its searches find
// contacts and organisations by their words, bounded as the customers' own
// listing is.
func TestTheAutomationsChoicesAreWhatAStepCanName(t *testing.T) {
	ctx := context.Background()
	users := memory.NewUserRepository()
	roles := memory.NewRoleRepository(users)
	agentRole, _ := roles.GetByName(ctx, model.RoleAgent)
	customerRole, _ := roles.GetByName(ctx, model.RoleCustomer)

	if _, err := users.Save(ctx, &model.User{Name: "Agnes Agent", Email: "agnes@example.com",
		RoleID: agentRole.ID}); err != nil {
		t.Fatal(err)
	}

	if _, err := users.Save(ctx, &model.User{Name: "Cora Customer", Email: "cora@example.com",
		RoleID: customerRole.ID}); err != nil {
		t.Fatal(err)
	}

	states := memory.NewTicketStateRepository()
	priorities := memory.NewTicketPriorityRepository()
	types := memory.NewTicketTypeRepository()
	queues := memory.NewQueueRepository()
	contacts := memory.NewContactRepository()
	organisations := memory.NewOrganisationRepository()

	_, _ = states.Save(ctx, &model.TicketState{Name: "open", Type: model.StateTypeOpen, Valid: true})
	_, _ = priorities.Save(ctx, &model.TicketPriority{Name: "high", Level: 1, Valid: true})
	_, _ = types.Save(ctx, &model.TicketType{Name: "incident", Valid: true})
	_, _ = queues.Save(ctx, &model.Queue{Name: "Service Desk", Valid: true, FollowUp: model.FollowUpReopen})

	for _, name := range []string{"Ann Archer", "Bob Baker"} {
		if _, err := contacts.Save(ctx, &model.Contact{Name: name, Valid: true}); err != nil {
			t.Fatal(err)
		}
	}

	_, _ = organisations.Save(ctx, &model.Organisation{Name: "Acme", Valid: true})

	macros := service.NewMacroApplicationService(memory.NewMacroRepository(), service.MacroReferences{
		Catalog:   service.TicketCatalogRepositories{States: states, Priorities: priorities, Types: types},
		Customers: service.TicketCustomerRepositories{Contacts: contacts, Organisations: organisations},
		Queues:    queues, Users: users, Roles: roles,
	})

	choices, err := macros.Choices(ctx)
	if err != nil {
		t.Fatal(err)
	}

	offered := map[string]bool{}
	for _, account := range choices.Accounts {
		offered[account.Name] = true
	}

	if !offered["Agnes Agent"] || offered["Cora Customer"] {
		t.Errorf("the automation offers a ticket to %v; want the agent and not the customer", offered)
	}

	if len(choices.Queues) != 1 || len(choices.States) != 1 || len(choices.Priorities) != 1 || len(choices.Types) != 1 {
		t.Errorf("the automation's choices read %d queues, %d states, %d priorities and %d types, want one of each",
			len(choices.Queues), len(choices.States), len(choices.Priorities), len(choices.Types))
	}

	found, err := macros.FindContacts(ctx, service.CustomerListFilter{Search: "ann"})
	if err != nil || len(found) != 1 || found[0].Name != "Ann Archer" {
		t.Errorf("a search for ann finds %+v (%v), want Ann alone", found, err)
	}

	if _, err := macros.FindOrganisations(ctx, service.CustomerListFilter{Limit: service.MaxTicketPage + 1}); apperror.
		KindOf(err) != apperror.KindInvalid {
		t.Errorf("a page larger than the customers' own was answered %v, want it refused", err)
	}
}
