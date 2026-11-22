package sqldb_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/dennis-dko/qubitdesk/internal/domain/model"
	"github.com/dennis-dko/qubitdesk/internal/infrastructure/persistence/sqldb"
)

// A merge moves the merged ticket's articles, its time and its watchers to the
// other - a watcher of both once - hands it its links, but neither one to the
// other ticket nor one to a record the other is linked to already, links the
// two as parent and child, and writes the merged ticket and the other's
// change, with the history lines of both.
func TestAMergeMovesWhatTheMergedTicketHeld(t *testing.T) {
	ctx := context.Background()
	tables := newTicketTables(t)
	links := sqldb.NewLinkRepository(tables.db, sqldb.DialectSQLite)
	at := time.Date(2026, 11, 22, 9, 0, 0, 0, time.UTC)

	into := tables.create(t, "Printer jams", at, 5)
	merged := tables.create(t, "Printer jams again", at.Add(time.Hour), 10)
	toner := tables.create(t, "Toner", at, 0)

	for _, link := range []*model.Link{
		{SourceObject: model.LinkTicket, SourceID: merged.ID, TargetObject: model.LinkTicket, TargetID: into.ID},
		{SourceObject: model.LinkTicket, SourceID: toner.ID, TargetObject: model.LinkTicket, TargetID: merged.ID},
		{SourceObject: model.LinkTicket, SourceID: merged.ID, TargetObject: model.LinkConfigItem, TargetID: 7},
		{SourceObject: model.LinkTicket, SourceID: into.ID, TargetObject: model.LinkConfigItem, TargetID: 7},
		{SourceObject: model.LinkTicket, SourceID: merged.ID, TargetObject: model.LinkConfigItem, TargetID: 8},
	} {
		link.Type, link.CreatedAt = model.LinkNormal, at
		if _, err := links.Create(ctx, link); err != nil {
			t.Fatal(err)
		}
	}

	for _, watch := range []struct{ ticket, user uint }{{merged.ID, 1}, {merged.ID, 2}, {into.ID, 1}} {
		if err := tables.tickets.SetWatching(ctx, watch.ticket, watch.user, true); err != nil {
			t.Fatal(err)
		}
	}

	var mergedState uint
	if err := tables.db.QueryRow("SELECT id FROM ticket_states WHERE name = 'merged'").Scan(&mergedState); err != nil {
		t.Fatal(err)
	}

	by := uint(3)
	then := at.Add(2 * time.Hour)
	merged.StateID, merged.MergedInto, merged.UpdatedAt = mergedState, &into.ID, then

	if err := tables.tickets.Merge(ctx, merged, []model.HistoryEntry{
		{TicketID: merged.ID, At: then, Actor: model.SenderAgent, ActorID: &by, Event: model.HistoryMerged,
			OldValue: merged.Number, NewValue: into.Number},
		{TicketID: into.ID, At: then, Actor: model.SenderAgent, ActorID: &by, Event: model.HistoryMerged,
			OldValue: merged.Number, NewValue: into.Number},
	}, &by); err != nil {
		t.Fatalf("merging: %v", err)
	}

	for what, want := range map[string]struct {
		got, want int
	}{
		"articles of the ticket merged into": {tally(t, tables.db, "SELECT COUNT(*) FROM articles WHERE ticket_id = ?",
			into.ID), 2},
		"articles left with the merged ticket": {tally(t, tables.db,
			"SELECT COUNT(*) FROM articles WHERE ticket_id = ?", merged.ID), 0},
		"minutes of the ticket merged into": {tally(t, tables.db,
			"SELECT COALESCE(SUM(minutes), 0) FROM time_accounting WHERE ticket_id = ?", into.ID), 15},
		"watchers of the ticket merged into": {tally(t, tables.db,
			"SELECT COUNT(*) FROM ticket_watchers WHERE ticket_id = ?", into.ID), 2},
		"watchers left with the merged ticket": {tally(t, tables.db,
			"SELECT COUNT(*) FROM ticket_watchers WHERE ticket_id = ?", merged.ID), 0},
		"links between the two, as parent and child by whoever merged": {tally(t, tables.db,
			"SELECT COUNT(*) FROM links WHERE source_object = 'ticket' AND source_id = ? AND target_object = 'ticket' "+
				"AND target_id = ? AND type = 'ParentChild' AND created_by = ?", into.ID, merged.ID, by), 1},
		"links of the merged ticket but that one": {tally(t, tables.db,
			"SELECT COUNT(*) FROM links WHERE (source_object = 'ticket' AND source_id = ?) OR "+
				"(target_object = 'ticket' AND target_id = ?)", merged.ID, merged.ID), 1},
		"links of the ticket merged into to the item both were linked to": {tally(t, tables.db,
			"SELECT COUNT(*) FROM links WHERE source_id = ? AND target_object = 'configitem' AND target_id = 7",
			into.ID), 1},
		"links of the ticket merged into to the item it took over": {tally(t, tables.db,
			"SELECT COUNT(*) FROM links WHERE source_id = ? AND target_object = 'configitem' AND target_id = 8",
			into.ID), 1},
		"links joining a record to itself": {tally(t, tables.db,
			"SELECT COUNT(*) FROM links WHERE source_object = target_object AND source_id = target_id"), 0},
		"links to the ticket merged into from the toner's": {tally(t, tables.db,
			"SELECT COUNT(*) FROM links WHERE source_id = ? AND target_object = 'ticket' AND target_id = ?",
			toner.ID, into.ID), 1},
	} {
		if want.got != want.want {
			t.Errorf("%s: %d, want %d", what, want.got, want.want)
		}
	}

	stored, err := tables.tickets.GetByID(ctx, merged.ID)
	if err != nil || stored.MergedInto == nil || *stored.MergedInto != into.ID || stored.StateID != mergedState {
		t.Errorf("the merged ticket reads back as %+v (%v)", stored, err)
	}

	if touched, err := tables.tickets.GetByID(ctx, into.ID); err != nil || !touched.UpdatedAt.Equal(then) {
		t.Errorf("the ticket merged into was last changed %v (%v), want when the merge was", touched.UpdatedAt, err)
	}

	for _, id := range []uint{merged.ID, into.ID} {
		history, err := tables.tickets.History(ctx, id)
		if err != nil || history[len(history)-1].Event != model.HistoryMerged {
			t.Errorf("ticket %d's history ends %+v (%v), want the merge", id, history, err)
		}
	}
}

// tally is the count a query answers, with as many values as it takes.
func tally(t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()

	var n int
	if err := db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}

	return n
}
