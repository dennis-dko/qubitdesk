package sqldb_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/dennis-dko/qubitdesk/internal/domain/model"
	"github.com/dennis-dko/qubitdesk/internal/infrastructure/persistence/sqldb"
)

// A fresh installation lets locks run out in new and open, as KIX18 ships it,
// and keeps both as its own whatever replaces the others - in the order the
// types are listed, not the order they were added.
func TestNewAndOpenShipAsTheStateTypesALockRunsOutIn(t *testing.T) {
	ctx := context.Background()
	unlock := sqldb.NewUnlockStateTypeRepository(newTestDB(t), sqldb.DialectSQLite)

	read := func() string {
		kept, err := unlock.GetAll(ctx)
		if err != nil {
			t.Fatal(err)
		}

		var named []string
		for _, entry := range kept {
			named = append(named, fmt.Sprintf("%s/%t", entry.Type, entry.IsSystem))
		}

		return strings.Join(named, " ")
	}

	if got := read(); got != "new/true open/true" {
		t.Fatalf("a fresh installation lets locks run out in %q, want new and open as its own", got)
	}

	if err := unlock.Replace(ctx, []model.StateType{model.StateTypeClosed, model.StateTypePendingReminder}); err != nil {
		t.Fatal(err)
	}

	if got := read(); got != "new/true open/true pending-reminder/false closed/false" {
		t.Errorf("after adding two the types read %q", got)
	}

	if err := unlock.Replace(ctx, nil); err != nil {
		t.Fatal(err)
	}

	if got := read(); got != "new/true open/true" {
		t.Errorf("after taking the others away the types read %q, want new and open kept", got)
	}
}
