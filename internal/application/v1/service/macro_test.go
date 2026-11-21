package service_test

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/dennis-dko/qubitdesk/internal/application/v1/command"
	"github.com/dennis-dko/qubitdesk/internal/application/v1/service"
	"github.com/dennis-dko/qubitdesk/internal/domain/model"
	"github.com/dennis-dko/qubitdesk/internal/infrastructure/persistence/memory"
	"github.com/dennis-dko/qubitdesk/internal/support/apperror"
)

// macroFixture is the macro service over in-memory tables holding one record
// of each kind a step can name, offered or retired.
type macroFixture struct {
	macros *service.MacroApplicationService
	ids    map[string]string
}

func newMacroFixture(t *testing.T) macroFixture {
	t.Helper()

	ctx := context.Background()
	states := memory.NewTicketStateRepository()
	priorities := memory.NewTicketPriorityRepository()
	types := memory.NewTicketTypeRepository()
	queues := memory.NewQueueRepository()
	users := memory.NewUserRepository()
	contacts := memory.NewContactRepository()
	organisations := memory.NewOrganisationRepository()

	ids := map[string]string{}
	id := func(name string, value uint) { ids[name] = strconv.FormatUint(uint64(value), 10) }

	for _, seed := range []model.TicketState{
		{Name: "open", Type: model.StateTypeOpen, Valid: true},
		{Name: "pending-reminder", Type: model.StateTypePendingReminder, Valid: true},
		{Name: "merged", Type: model.StateTypeMerged, Valid: true},
		{Name: "resolved", Type: model.StateTypeClosed},
	} {
		state, err := states.Save(ctx, &seed)
		if err != nil {
			t.Fatal(err)
		}

		id(seed.Name, state.ID)
	}

	high, _ := priorities.Save(ctx, &model.TicketPriority{Name: "high", Level: 1, Valid: true})
	low, _ := priorities.Save(ctx, &model.TicketPriority{Name: "low", Level: 5})
	incident, _ := types.Save(ctx, &model.TicketType{Name: "incident", Valid: true})
	desk, _ := queues.Save(ctx, &model.Queue{Name: "Service Desk", Valid: true, FollowUp: model.FollowUpReopen})
	agent, _ := users.Save(ctx, &model.User{Name: "Agnes Agent", Email: "agent@example.com"})
	ann, _ := contacts.Save(ctx, &model.Contact{Name: "Ann", Valid: true})
	acme, _ := organisations.Save(ctx, &model.Organisation{Name: "Acme", Valid: true})

	id("high", high.ID)
	id("low", low.ID)
	id("incident", incident.ID)
	id("desk", desk.ID)
	id("agent", agent.ID)
	id("ann", ann.ID)
	id("acme", acme.ID)

	return macroFixture{
		macros: service.NewMacroApplicationService(memory.NewMacroRepository(), service.MacroReferences{
			Catalog:   service.TicketCatalogRepositories{States: states, Priorities: priorities, Types: types},
			Customers: service.TicketCustomerRepositories{Contacts: contacts, Organisations: organisations},
			Queues:    queues,
			Users:     users,
		}),
		ids: ids,
	}
}

func step(kind model.MacroActionType, parameters ...string) command.MacroActionCommand {
	sent := command.MacroActionCommand{Type: string(kind), Parameters: map[string]string{}}
	for i := 0; i+1 < len(parameters); i += 2 {
		sent.Parameters[parameters[i]] = parameters[i+1]
	}

	return sent
}

// A macro is kept with its steps in the order they were given and the values
// each was given, and a change that sends steps takes the place of all of them
// while one that sends none leaves them as they were.
func TestAMacroIsKeptWithItsStepsInOrder(t *testing.T) {
	ctx := context.Background()
	f := newMacroFixture(t)

	made, err := f.macros.Create(ctx, command.MacroCommand{Name: ptr("  Escalate  "), Actions: &[]command.MacroActionCommand{
		step(model.ActionStateSet, "stateId", f.ids["pending-reminder"], "pendingMinutes", "60"),
		step(model.ActionPrioritySet, "priorityId", f.ids["high"]),
		step(model.ActionOwnerSet),
		step(model.ActionArticleCreate, "channel", "note", "body", "Escalated by the night shift."),
	}})
	if err != nil {
		t.Fatalf("making a macro answered %v", err)
	}

	var kept []string
	for _, action := range made.Actions {
		kept = append(kept, fmt.Sprintf("%s%v", action.Type, action.Parameters))
	}

	want := []string{
		"StateSet" + fmt.Sprint(map[string]string{"stateId": f.ids["pending-reminder"], "pendingMinutes": "60"}),
		"PrioritySet" + fmt.Sprint(map[string]string{"priorityId": f.ids["high"]}),
		"OwnerSet" + fmt.Sprint(map[string]string{}),
		"ArticleCreate" + fmt.Sprint(map[string]string{"channel": "note", "body": "Escalated by the night shift."}),
	}
	if made.Name != "Escalate" || !made.Valid || strings.Join(kept, " ") != strings.Join(want, " ") {
		t.Fatalf("the macro was kept as %q, valid %t, with %q; want %q", made.Name, made.Valid, kept, want)
	}

	renamed, err := f.macros.Update(ctx, made.ID, command.MacroCommand{Name: ptr("Escalate at night")})
	if err != nil || len(renamed.Actions) != 4 {
		t.Fatalf("a change sending no steps answered %+v, %v; want the four steps kept", renamed, err)
	}

	replaced, err := f.macros.Update(ctx, made.ID, command.MacroCommand{Actions: &[]command.MacroActionCommand{
		step(model.ActionLockSet, "locked", "false"),
	}})
	if err != nil || len(replaced.Actions) != 1 || replaced.Actions[0].Type != model.ActionLockSet {
		t.Fatalf("a change sending one step answered %+v, %v; want that step alone", replaced, err)
	}

	if _, err := f.macros.Create(ctx, command.MacroCommand{Name: ptr("Escalate at night")}); err == nil {
		t.Error("a second macro of the same name was kept")
	} else if detail, ok := apperror.Detail(err); !ok || detail.Code != "macroNameTaken" {
		t.Errorf("a second macro of the same name was answered %v, want macroNameTaken", err)
	}
}

// A step that could not be taken is refused as the macro is saved, naming the
// step and the parameter that is why: a type there is not, a parameter its type
// does not read, a value missing that it needs, a record that is not there or
// no longer offered, merged, a waiting state without its time and a state that
// waits for none with one, and values their parameter cannot hold.
func TestAStepThatCouldNotBeTakenIsRefusedAsTheMacroIsSaved(t *testing.T) {
	ctx := context.Background()
	f := newMacroFixture(t)

	for name, refused := range map[string]struct {
		sent      command.MacroActionCommand
		parameter string
	}{
		"a type there is not":        {step("TicketExplode"), "type"},
		"a parameter not read":       {step(model.ActionPrioritySet, "priorityId", f.ids["high"], "colour", "red"), "colour"},
		"a value missing":            {step(model.ActionPrioritySet), "priorityId"},
		"a record that is not there": {step(model.ActionTeamSet, "queueId", "99"), "queueId"},
		"an id that is no number":    {step(model.ActionOwnerSet, "ownerId", "Agnes"), "ownerId"},
		"a retired priority":         {step(model.ActionPrioritySet, "priorityId", f.ids["low"]), "priorityId"},
		"a retired state":            {step(model.ActionStateSet, "stateId", f.ids["resolved"]), "stateId"},
		"merged":                     {step(model.ActionStateSet, "stateId", f.ids["merged"]), "stateId"},
		"a wait without its time":    {step(model.ActionStateSet, "stateId", f.ids["pending-reminder"]), "pendingMinutes"},
		"a wait of no time":          {step(model.ActionStateSet, "stateId", f.ids["pending-reminder"], "pendingMinutes", "0"), "pendingMinutes"},
		"a time for no wait":         {step(model.ActionStateSet, "stateId", f.ids["open"], "pendingMinutes", "30"), "pendingMinutes"},
		"a title too long":           {step(model.ActionTitleSet, "title", strings.Repeat("x", model.MaxTicketTitleLength+1)), "title"},
		"a switch that is neither":   {step(model.ActionLockSet, "locked", "maybe"), "locked"},
		"minutes beyond the bound":   {step(model.ActionArticleCreate, "channel", "note", "body", "x", "minutes", "1441"), "minutes"},
		"a channel nobody writes in": {step(model.ActionArticleCreate, "channel", "pigeon", "body", "x"), "channel"},
		"an article with nothing":    {step(model.ActionArticleCreate, "channel", "note"), "body"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := f.macros.Create(ctx, command.MacroCommand{Name: ptr(name), Actions: &[]command.MacroActionCommand{
				step(model.ActionLockSet, "locked", "true"), refused.sent,
			}})

			detail, ok := apperror.Detail(err)
			if !ok || detail.Code != "macroStepInvalid" || len(detail.Values) != 2 ||
				detail.Values[0] != 2 || detail.Values[1] != refused.parameter {
				t.Errorf("the macro was answered %v; want step 2 refused for %s", err, refused.parameter)
			}
		})
	}
}

// A macro has its own fields checked all at once before its steps: a name, and
// no more steps than one form shows.
func TestAMacroWithoutANameOrWithTooManyStepsIsRefused(t *testing.T) {
	ctx := context.Background()
	f := newMacroFixture(t)

	steps := make([]command.MacroActionCommand, model.MaxMacroActions+1)
	for i := range steps {
		steps[i] = step(model.ActionLockSet, "locked", "true")
	}

	_, err := f.macros.Create(ctx, command.MacroCommand{Name: ptr(" "), Actions: &steps})

	detail, ok := apperror.Detail(err)
	if !ok || strings.Join(detail.Fields, ",") != "name,actions" {
		t.Errorf("a macro without a name and with too many steps was answered %v", err)
	}

	if _, err := f.macros.Create(ctx, command.MacroCommand{}); apperror.KindOf(err) != apperror.KindInvalid {
		t.Errorf("a macro sent without a name was answered %v", err)
	}
}
