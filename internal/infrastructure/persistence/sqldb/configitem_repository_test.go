package sqldb_test

import (
	"context"
	"testing"
	"time"

	"github.com/dennis-dko/qubitdesk/internal/domain/model"
	"github.com/dennis-dko/qubitdesk/internal/infrastructure/persistence/sqldb"
)

// A deleted item takes its versions and its links with it, in one
// transaction, and leaves every other link alone. A link names its ends by
// kind and id, so no key does that for it - and the service would hide a link
// whose end is gone, so nothing above the repository could tell it was left.
func TestADeletedItemTakesItsVersionsAndLinksWithIt(t *testing.T) {
	ctx := context.Background()
	db := newTestDBWithKeys(t)
	catalog := sqldb.NewGeneralCatalogRepository(db, sqldb.DialectSQLite)
	items := sqldb.NewConfigItemRepository(db, sqldb.DialectSQLite)
	links := sqldb.NewLinkRepository(db, sqldb.DialectSQLite)

	first := func(class string) uint {
		entries, err := catalog.Items(ctx, class)
		if err != nil || len(entries) == 0 {
			t.Fatalf("the catalog ships no %s: %v", class, err)
		}

		return entries[0].ID
	}

	class := first(model.CatalogCIClass)

	definition, err := sqldb.NewClassDefinitionRepository(db, sqldb.DialectSQLite).Current(ctx, class)
	if err != nil {
		t.Fatalf("the shipped class has no definition: %v", err)
	}

	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	made := func(name string) uint {
		item, err := items.Create(ctx, &model.ConfigItem{ClassID: class, CreatedAt: now, CreatedBy: 1},
			&model.ConfigItemVersion{DefinitionID: definition.ID, Name: name,
				DeploymentStateID: first(model.CatalogDeploymentState), IncidentStateID: first(model.CatalogIncidentState),
				Data: model.AttributeData{}, CreatedAt: now, CreatedBy: 1},
			func(id uint) string { return name })
		if err != nil {
			t.Fatalf("making %s: %v", name, err)
		}

		return item.ID
	}

	office, depot, spare := made("office"), made("depot"), made("spare")

	for _, link := range []*model.Link{
		{SourceObject: model.LinkConfigItem, SourceID: depot, TargetObject: model.LinkConfigItem, TargetID: office},
		{SourceObject: model.LinkTicket, SourceID: 5, TargetObject: model.LinkConfigItem, TargetID: depot},
		{SourceObject: model.LinkConfigItem, SourceID: office, TargetObject: model.LinkConfigItem, TargetID: spare},
	} {
		link.Type, link.CreatedAt = model.LinkNormal, now
		if _, err := links.Create(ctx, link); err != nil {
			t.Fatalf("linking: %v", err)
		}
	}

	if err := items.Delete(ctx, depot); err != nil {
		t.Fatalf("deleting the depot: %v", err)
	}

	if left := count(t, db, "SELECT COUNT(*) FROM config_item_versions WHERE item_id = ?", depot); left != 0 {
		t.Errorf("%d version(s) of the deleted depot are left", left)
	}

	if left := count(t, db, "SELECT COUNT(*) FROM links WHERE id > ?", 0); left != 1 {
		t.Errorf("%d link(s) are left after the depot was deleted, want only the office's to the spare", left)
	}
}
