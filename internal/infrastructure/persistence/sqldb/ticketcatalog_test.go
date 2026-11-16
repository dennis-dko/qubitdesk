package sqldb_test

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"testing"

	"github.com/dennis-dko/go-ticket-system/internal/domain/model"
	"github.com/dennis-dko/go-ticket-system/internal/infrastructure/persistence/sqldb"
	"github.com/dennis-dko/go-ticket-system/internal/support/apperror"
)

// The states, the priority and the type the code moves tickets into by itself are
// found by name, so each has to be shipped under that name and marked as the
// code's own - which is what stops it being renamed or deleted from under the
// code. A name in the model the migration does not seed, or seeds as an ordinary
// row, is a ticket the code cannot create or close.
func TestTheValuesTheCodeNamesShipAsItsOwn(t *testing.T) {
	db := newTestDB(t)

	for _, want := range []struct{ table, name string }{
		{"ticket_states", model.StateNew},
		{"ticket_states", model.StateOpen},
		{"ticket_states", model.StateClosed},
		{"ticket_states", model.StateMerged},
		{"ticket_priorities", model.PriorityNormal},
		{"ticket_types", model.TypeUnclassified},
	} {
		var system bool

		err := db.QueryRow("SELECT is_system FROM "+want.table+" WHERE name = ?", want.name).Scan(&system)

		switch {
		case errors.Is(err, sql.ErrNoRows):
			t.Errorf("%s ships no row named %q, which the code looks for", want.table, want.name)
		case err != nil:
			t.Fatalf("read %s %q: %v", want.table, want.name, err)
		case !system:
			t.Errorf("%s ships %q as an ordinary row, so it can be renamed or deleted from under the code",
				want.table, want.name)
		}
	}
}

// Every shipped state has a type the code knows, and every type has a state: a
// state of an unknown type is a ticket nothing knows what to do with, and a type
// with no state is a step of a ticket's life an installation cannot take until it
// makes one itself.
func TestEveryShippedStateHasATypeTheCodeKnows(t *testing.T) {
	db := newTestDB(t)

	rows, err := db.Query("SELECT name, state_type FROM ticket_states")
	if err != nil {
		t.Fatalf("read the states: %v", err)
	}
	defer func() { _ = rows.Close() }()

	var seen []model.StateType

	for rows.Next() {
		var name, kind string
		if err := rows.Scan(&name, &kind); err != nil {
			t.Fatalf("scan a state: %v", err)
		}

		if !slices.Contains(model.StateTypes(), model.StateType(kind)) {
			t.Errorf("the shipped state %q is of type %q, which the code does not know", name, kind)
		}

		seen = append(seen, model.StateType(kind))
	}

	if err := rows.Err(); err != nil {
		t.Fatalf("read the states: %v", err)
	}

	for _, kind := range model.StateTypes() {
		if !slices.Contains(seen, kind) {
			t.Errorf("no shipped state is of type %q", kind)
		}
	}
}

// The repositories over the real tables: a ticket carrying a state is counted, a
// name another state has is refused with the code the interface translates, an
// id nobody has is a "not found" naming the kind of record, and a state made
// through the application is the installation's own even when the caller says
// otherwise - is_system is the seeding migration's to write.
func TestTheStateRepositoryKeepsTheRulesOfTheTable(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	states := sqldb.NewTicketStateRepository(db, sqldb.DialectSQLite)

	all, err := states.GetAll(ctx)
	if err != nil || len(all) != len(model.StateTypes()) {
		t.Fatalf("the shipped states read as %d, %v; want one per type", len(all), err)
	}

	var open *model.TicketState

	for _, state := range all {
		if state.Name == model.StateOpen {
			open = state
		}
	}

	ticket(t, db, "2026111400000001", model.StateOpen, nil, nil)
	ticket(t, db, "2026111400000002", model.StateOpen, nil, nil)

	if count, err := states.CountTickets(ctx, open.ID); err != nil || count != 2 {
		t.Errorf("two tickets are open and the count says %d, %v", count, err)
	}

	made, err := states.Save(ctx, &model.TicketState{
		Name: "on hold", Type: model.StateTypePendingReminder, Valid: true, IsSystem: true,
	})
	if err != nil {
		t.Fatalf("save a state: %v", err)
	}

	if made.IsSystem {
		t.Error("a state made through the application was stored as one the code relies on")
	}

	_, err = states.Save(ctx, &model.TicketState{Name: "on hold", Type: model.StateTypeOpen})
	if detail, ok := apperror.Detail(err); !ok || detail.Code != "stateNameTaken" {
		t.Errorf("a second state named on hold answered %v, want stateNameTaken", err)
	}

	_, err = states.GetByID(ctx, 9999)
	if detail, ok := apperror.Detail(err); !ok || detail.Kind != apperror.KindNotFound || detail.Entity != "state" {
		t.Errorf("reading a state nobody has answered %v, want a state not found", err)
	}

	made.Description = "waits"

	if _, err := states.Update(ctx, made); err != nil {
		t.Fatalf("change the state: %v", err)
	}

	if _, err := states.Update(ctx, made); err != nil {
		t.Errorf("saving the state unchanged answered %v; MySQL counts no changed row there", err)
	}

	if err := states.Delete(ctx, made.ID); err != nil {
		t.Fatalf("delete the state: %v", err)
	}

	if err := states.Delete(ctx, made.ID); apperror.KindOf(err) != apperror.KindNotFound {
		t.Errorf("deleting it twice answered %v, want not found", err)
	}
}

// The shipped waiting state moves a ticket on to closed, as KIX18 ships it, and
// a waiting state's next state reads back by id and is counted for the state it
// names - so the catalog can keep that state able to end the wait.
func TestTheShippedWaitingStateMovesOnToClosed(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	states := sqldb.NewTicketStateRepository(db, sqldb.DialectSQLite)

	waiting, err := states.GetByName(ctx, "pending-auto-close")
	if err != nil {
		t.Fatal(err)
	}

	closed, err := states.GetByName(ctx, "closed")
	if err != nil {
		t.Fatal(err)
	}

	if waiting.NextStateID == nil || *waiting.NextStateID != closed.ID {
		t.Fatalf("the shipped waiting state moves on to %v, want closed (%d)", waiting.NextStateID, closed.ID)
	}

	open, err := states.GetByName(ctx, "open")
	if err != nil {
		t.Fatal(err)
	}

	reopening, err := states.Save(ctx, &model.TicketState{
		Name: "pending-auto-open", Type: model.StateTypePendingAuto, Valid: true, NextStateID: &open.ID,
	})
	if err != nil || reopening.NextStateID == nil || *reopening.NextStateID != open.ID {
		t.Fatalf("a waiting state moving on to open reads %+v, %v", reopening, err)
	}

	for name, want := range map[string]int{"closed": 1, "open": 1, "merged": 0} {
		state, _ := states.GetByName(ctx, name)

		if count, err := states.CountWaitingFor(ctx, state.ID); err != nil || count != want {
			t.Errorf("%d waiting states move on to %s, %v; want %d", count, name, err, want)
		}
	}
}
