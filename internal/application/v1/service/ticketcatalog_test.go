package service_test

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/dennis-dko/go-ticket-system/internal/application/v1/command"
	"github.com/dennis-dko/go-ticket-system/internal/application/v1/service"
	"github.com/dennis-dko/go-ticket-system/internal/domain/model"
	"github.com/dennis-dko/go-ticket-system/internal/infrastructure/persistence/memory"
	"github.com/dennis-dko/go-ticket-system/internal/support/apperror"
)

// catalogFixture is the catalog service over empty in-memory tables, each of
// which a case fills with what it needs.
type catalogFixture struct {
	catalog    *service.TicketCatalogApplicationService
	states     *memory.TicketStateRepository
	priorities *memory.TicketPriorityRepository
	types      *memory.TicketTypeRepository
}

func newCatalogFixture() catalogFixture {
	f := catalogFixture{
		states:     memory.NewTicketStateRepository(),
		priorities: memory.NewTicketPriorityRepository(),
		types:      memory.NewTicketTypeRepository(),
	}

	f.catalog = service.NewTicketCatalogApplicationService(service.TicketCatalogRepositories{
		States: f.states, Priorities: f.priorities, Types: f.types, UnlockTypes: memory.NewUnlockStateTypeRepository(),
	})

	return f
}

func ptr[T any](v T) *T { return &v }

// refusal fails the case unless err carries code, and hands over the refusal.
func refusal(t *testing.T, err error, code string) *apperror.Error {
	t.Helper()

	detail, ok := apperror.Detail(err)
	if !ok || detail.Code != code {
		t.Fatalf("answered %v, want the refusal %s", err, code)
	}

	return detail
}

// invalidFields fails the case unless err refuses exactly these fields.
func invalidFields(t *testing.T, err error, want ...string) {
	t.Helper()

	detail, ok := apperror.Detail(err)
	if !ok || detail.Kind != apperror.KindInvalid || !slices.Equal(detail.Fields, want) {
		t.Fatalf("answered %v, want the fields %v refused", err, want)
	}
}

// What the code moves tickets into by itself is found by its name, so a shipped
// value is neither changed nor deleted - in any of the three tables - however
// harmless the change looks.
func TestAShippedValueStaysAsItShipped(t *testing.T) {
	ctx := context.Background()
	f := newCatalogFixture()

	state, _ := f.states.Save(ctx, &model.TicketState{
		Name: model.StateClosed, Type: model.StateTypeClosed, Valid: true, IsSystem: true,
	})
	priority, _ := f.priorities.Save(ctx, &model.TicketPriority{
		Name: model.PriorityNormal, Level: 3, Valid: true, IsSystem: true,
	})
	kind, _ := f.types.Save(ctx, &model.TicketType{Name: model.TypeUnclassified, Valid: true, IsSystem: true})

	_, err := f.catalog.UpdateState(ctx, state.ID, command.TicketStateCommand{Description: ptr("mine now")})
	refusedWith(t, err, "stateShipped")
	refusedWith(t, f.catalog.DeleteState(ctx, state.ID), "stateShipped")

	_, err = f.catalog.UpdatePriority(ctx, priority.ID, command.TicketPriorityCommand{Valid: ptr(false)})
	refusedWith(t, err, "priorityShipped")
	refusedWith(t, f.catalog.DeletePriority(ctx, priority.ID), "priorityShipped")

	_, err = f.catalog.UpdateType(ctx, kind.ID, command.TicketTypeCommand{Name: ptr("other")})
	refusedWith(t, err, "typeShipped")
	refusedWith(t, f.catalog.DeleteType(ctx, kind.ID), "typeShipped")

	if kept, _ := f.states.GetByID(ctx, state.ID); kept.Description != "" {
		t.Errorf("a refused change was stored anyway: %+v", kept)
	}
}

// A value tickets carry is refused deletion with how many carry it, and can be
// retired instead; once nothing carries it, it can go.
func TestAValueTicketsCarryIsRetiredRatherThanDeleted(t *testing.T) {
	ctx := context.Background()
	f := newCatalogFixture()

	state, err := f.catalog.CreateState(ctx, command.TicketStateCommand{
		Name: ptr("waiting for the customer"), Type: ptr(string(model.StateTypePendingReminder)),
	})
	if err != nil {
		t.Fatalf("create a state: %v", err)
	}

	priority, err := f.catalog.CreatePriority(ctx, command.TicketPriorityCommand{Name: ptr("urgent"), Level: ptr(1)})
	if err != nil {
		t.Fatalf("create a priority: %v", err)
	}

	kind, err := f.catalog.CreateType(ctx, command.TicketTypeCommand{Name: ptr("change request")})
	if err != nil {
		t.Fatalf("create a type: %v", err)
	}

	f.states.UseInTickets(state.ID, 3)
	f.priorities.UseInTickets(priority.ID, 1)
	f.types.UseInTickets(kind.ID, 2)

	detail := refusal(t, f.catalog.DeleteState(ctx, state.ID), "stateInUse")
	if !slices.Equal(detail.Values, []any{"waiting for the customer", 3}) {
		t.Errorf("the refusal says %v, want the state's name and how many tickets carry it", detail.Values)
	}

	refusedWith(t, f.catalog.DeletePriority(ctx, priority.ID), "priorityInUse")
	refusedWith(t, f.catalog.DeleteType(ctx, kind.ID), "typeInUse")

	retired, err := f.catalog.UpdateState(ctx, state.ID, command.TicketStateCommand{Valid: ptr(false)})
	if err != nil || retired.Valid {
		t.Fatalf("retiring a state tickets carry answered %+v, %v", retired, err)
	}

	f.states.UseInTickets(state.ID, 0)

	if err := f.catalog.DeleteState(ctx, state.ID); err != nil {
		t.Fatalf("deleting a state nothing carries: %v", err)
	}
}

// A state is a name and a type the code knows; the type is what the code does
// with a ticket in it, so one it does not know is refused rather than stored.
func TestAStateNeedsATypeTheCodeKnows(t *testing.T) {
	ctx := context.Background()
	f := newCatalogFixture()

	_, err := f.catalog.CreateState(ctx, command.TicketStateCommand{Name: ptr("waiting")})
	invalidFields(t, err, "type")

	_, err = f.catalog.CreateState(ctx, command.TicketStateCommand{Name: ptr("waiting"), Type: ptr("waiting")})
	invalidFields(t, err, "type")

	for _, kind := range model.StateTypes() {
		name := "a state of type " + string(kind)

		state, err := f.catalog.CreateState(ctx, command.TicketStateCommand{Name: ptr(name), Type: ptr(string(kind))})
		if err != nil {
			t.Fatalf("a state of type %s was refused: %v", kind, err)
		}

		if !state.Valid || state.IsSystem {
			t.Errorf("a new state starts valid and as the installation's own, got %+v", state)
		}
	}
}

// A priority's level orders it, 1 the most urgent, and is bounded at both ends.
func TestAPriorityLevelIsBounded(t *testing.T) {
	ctx := context.Background()
	f := newCatalogFixture()

	for _, level := range []int{0, -1, model.MaxPriorityLevel + 1} {
		_, err := f.catalog.CreatePriority(ctx, command.TicketPriorityCommand{Name: ptr("p"), Level: ptr(level)})
		invalidFields(t, err, "level")
	}

	for _, level := range []int{1, model.MaxPriorityLevel} {
		name := "level " + strconv.Itoa(level)
		if _, err := f.catalog.CreatePriority(ctx, command.TicketPriorityCommand{
			Name: ptr(name), Level: ptr(level),
		}); err != nil {
			t.Errorf("level %d was refused: %v", level, err)
		}
	}

	_, err := f.catalog.CreatePriority(ctx, command.TicketPriorityCommand{Name: ptr("no level")})
	invalidFields(t, err, "level")
}

// The name every value has is there and fits its column, and so does the
// description; both are refused together, so a form learns everything at once.
func TestAValueIsNamedAndDescribedWithinItsColumns(t *testing.T) {
	ctx := context.Background()
	f := newCatalogFixture()

	_, err := f.catalog.CreateType(ctx, command.TicketTypeCommand{
		Name:        ptr("   "),
		Description: ptr(strings.Repeat("d", model.MaxDescriptionLength+1)),
	})
	invalidFields(t, err, "name", "description")

	_, err = f.catalog.CreateType(ctx, command.TicketTypeCommand{
		Name: ptr(strings.Repeat("n", model.MaxCatalogNameLength+1)),
	})
	invalidFields(t, err, "name")

	_, err = f.catalog.CreateType(ctx, command.TicketTypeCommand{})
	invalidFields(t, err, "name")

	kind, err := f.catalog.CreateType(ctx, command.TicketTypeCommand{
		Name: ptr("  " + strings.Repeat("n", model.MaxCatalogNameLength) + "  "),
	})
	if err != nil {
		t.Fatalf("a name as long as its column, with spaces around it, was refused: %v", err)
	}

	if len(kind.Name) != model.MaxCatalogNameLength {
		t.Errorf("the name was stored as %d characters, want it trimmed to %d", len(kind.Name), model.MaxCatalogNameLength)
	}

	if _, err := f.catalog.CreateType(ctx, command.TicketTypeCommand{Name: ptr(kind.Name)}); err == nil {
		t.Fatal("a second type with the same name was stored")
	} else {
		refusedWith(t, err, "typeNameTaken")
	}
}

// A change says what it changes; a field it leaves out stays as it was.
func TestAChangeLeavesWhatItDoesNotMention(t *testing.T) {
	ctx := context.Background()
	f := newCatalogFixture()

	state, err := f.catalog.CreateState(ctx, command.TicketStateCommand{
		Name: ptr("on hold"), Type: ptr(string(model.StateTypePendingReminder)), Description: ptr("waits"),
	})
	if err != nil {
		t.Fatalf("create a state: %v", err)
	}

	changed, err := f.catalog.UpdateState(ctx, state.ID, command.TicketStateCommand{Valid: ptr(false)})
	if err != nil {
		t.Fatalf("retire the state: %v", err)
	}

	if changed.Name != "on hold" || changed.Type != model.StateTypePendingReminder || changed.Description != "waits" {
		t.Errorf("retiring the state changed what the request did not mention: %+v", changed)
	}

	if _, err := f.catalog.GetState(ctx, 0); err == nil {
		t.Error("the state with id 0 was looked up")
	}
}

// The state types a lock runs out in are the installation's to add to: the ones
// sent are kept, each once, beside new and open, which ship with it and stay -
// a list leaving one of them out is refused rather than quietly put back, and
// so is a type there is not.
func TestTheStateTypesALockRunsOutInAreAddedToButKeepWhatShips(t *testing.T) {
	ctx := context.Background()
	f := newCatalogFixture()

	kept, err := f.catalog.SetUnlockStateTypes(ctx, []string{"open", "pending-reminder", "new", "pending-reminder"})
	if err != nil {
		t.Fatal(err)
	}

	var named []string
	for _, entry := range kept {
		named = append(named, fmt.Sprintf("%s/%t", entry.Type, entry.IsSystem))
	}

	if want := "new/true open/true pending-reminder/false"; strings.Join(named, " ") != want {
		t.Errorf("the state types read %q, want %q", strings.Join(named, " "), want)
	}

	_, err = f.catalog.SetUnlockStateTypes(ctx, []string{"open", "pending-reminder"})
	if detail := refusal(t, err, "unlockStateTypeShipped"); len(detail.Values) != 0 {
		t.Errorf("the refusal carries %v, and needs nothing to say what it says", detail.Values)
	}

	if _, err := f.catalog.SetUnlockStateTypes(ctx, []string{"new", "open", "asleep"}); apperror.KindOf(err) != apperror.KindInvalid {
		t.Errorf("a type there is not was answered %v, want it refused", err)
	}

	if back, err := f.catalog.SetUnlockStateTypes(ctx, []string{"new", "open"}); err != nil || len(back) != 2 {
		t.Errorf("taking the added type away again answered %v, %v", back, err)
	}
}
