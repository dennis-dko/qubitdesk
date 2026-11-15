package sqldb_test

import (
	"context"
	"testing"

	"github.com/dennis-dko/go-ticket-system/internal/infrastructure/persistence/sqldb"
)

// A key stored as nothing is told apart from a key not stored, and deleting a
// key - twice, or one never stored - leaves it not stored.
func TestASettingStoredAsNothingIsStillStored(t *testing.T) {
	ctx := context.Background()
	settings := sqldb.NewSettingsRepository(newTestDB(t), sqldb.DialectSQLite)

	if _, stored, err := settings.Lookup(ctx, "sysconfig.Ticket::HookDivider"); err != nil || stored {
		t.Fatalf("a key never written reads as stored %t, %v", stored, err)
	}

	if err := settings.Set(ctx, "sysconfig.Ticket::HookDivider", ""); err != nil {
		t.Fatal(err)
	}

	if value, stored, err := settings.Lookup(ctx, "sysconfig.Ticket::HookDivider"); err != nil || !stored || value != "" {
		t.Fatalf("a key written as nothing reads as %q, stored %t, %v", value, stored, err)
	}

	for range 2 {
		if err := settings.Delete(ctx, "sysconfig.Ticket::HookDivider"); err != nil {
			t.Fatalf("deleting the key: %v", err)
		}
	}

	if _, stored, err := settings.Lookup(ctx, "sysconfig.Ticket::HookDivider"); err != nil || stored {
		t.Fatalf("a deleted key reads as stored %t, %v", stored, err)
	}
}
