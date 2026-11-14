package sqldb_test

import (
	"database/sql"
	"errors"
	"slices"
	"testing"

	"github.com/dennis-dko/go-ticket-system/internal/domain/model"
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
