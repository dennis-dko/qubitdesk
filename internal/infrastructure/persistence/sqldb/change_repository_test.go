package sqldb_test

import (
	"context"
	"testing"
	"time"

	"github.com/dennis-dko/qubitdesk/internal/domain/model"
	"github.com/dennis-dko/qubitdesk/internal/infrastructure/persistence/sqldb"
)

// The migration ships OTRS's state machine and matrix, and a deleted change
// takes its links with it - which nothing above the repository could see, as
// a link whose end is gone is left out like one its reader may not see.
func TestAChangeIsKeptWithTheMachineAndMatrixOTRSShips(t *testing.T) {
	ctx := context.Background()
	db := newTestDBWithKeys(t)
	catalog := sqldb.NewGeneralCatalogRepository(db, sqldb.DialectSQLite)
	changes := sqldb.NewChangeRepository(db, sqldb.DialectSQLite)
	links := sqldb.NewLinkRepository(db, sqldb.DialectSQLite)

	value := func(class, name string) uint {
		entries, err := catalog.Items(ctx, class)
		if err != nil {
			t.Fatal(err)
		}

		for _, entry := range entries {
			if entry.Name == name {
				return entry.ID
			}
		}

		t.Fatalf("the catalog ships no %s %s", class, name)

		return 0
	}

	transitions, err := changes.Transitions(ctx)
	if err != nil || len(transitions) != 17 {
		t.Errorf("the state machine has %d steps (%v), want OTRS's seventeen", len(transitions), err)
	}

	priority, err := changes.Priority(ctx, value(model.CatalogChangeCategory, "5-very-high"),
		value(model.CatalogChangeImpact, "4-high"))
	if err != nil || priority != value(model.CatalogChangePriority, "5-very-high") {
		t.Errorf("the matrix gives a very high category of high impact %d (%v), want very high", priority, err)
	}

	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	normal := func(class string) uint { return value(class, "3-normal") }

	change, err := changes.Create(ctx, &model.Change{Title: "Replace the core switch",
		CategoryID: normal(model.CatalogChangeCategory), ImpactID: normal(model.CatalogChangeImpact),
		PriorityID: normal(model.CatalogChangePriority), StateID: value(model.CatalogChangeState, "requested"),
		CreatedAt: now, ChangedAt: now}, func(id uint) string { return "C-1" })
	if err != nil || change.Number != "C-1" {
		t.Fatalf("making a change: %+v (%v)", change, err)
	}

	for _, link := range []*model.Link{
		{SourceObject: model.LinkTicket, SourceID: 3, TargetObject: model.LinkChange, TargetID: change.ID},
		{SourceObject: model.LinkChange, SourceID: change.ID, TargetObject: model.LinkConfigItem, TargetID: 4},
		{SourceObject: model.LinkTicket, SourceID: 3, TargetObject: model.LinkConfigItem, TargetID: 4},
	} {
		link.Type, link.CreatedAt = model.LinkNormal, now
		if _, err := links.Create(ctx, link); err != nil {
			t.Fatal(err)
		}
	}

	if err := changes.Delete(ctx, change.ID); err != nil {
		t.Fatalf("deleting the change: %v", err)
	}

	if left := count(t, db, "SELECT COUNT(*) FROM links WHERE id > ?", 0); left != 1 {
		t.Errorf("%d link(s) are left after the change was deleted, want only the ticket's to the item", left)
	}
}
