package sqldb_test

import (
	"context"
	"testing"

	"github.com/dennis-dko/go-ticket-system/internal/domain/model"
	"github.com/dennis-dko/go-ticket-system/internal/infrastructure/persistence/sqldb"
	"github.com/dennis-dko/go-ticket-system/internal/support/apperror"
)

// A service level is stored with the calendar it counts in and read back as it
// was; the calendar knows a level counts in it, nothing uses a new level, a
// second level of the same name is refused, and one with no calendar counts
// around the clock.
func TestAServiceLevelIsStoredWithItsCalendar(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	calendars := sqldb.NewCalendarRepository(db, sqldb.DialectSQLite)
	levels := sqldb.NewServiceLevelRepository(db, sqldb.DialectSQLite)

	office, err := calendars.Save(ctx, &model.Calendar{Name: "Office", Valid: true})
	if err != nil {
		t.Fatal(err)
	}

	gold, err := levels.Save(ctx, &model.ServiceLevel{
		Name: "Gold", CalendarID: &office.ID, FirstResponseMinutes: 60, UpdateMinutes: 120, SolutionMinutes: 480,
		Valid: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	if gold.CalendarID == nil || *gold.CalendarID != office.ID || gold.SolutionMinutes != 480 || !gold.Valid {
		t.Errorf("Gold reads back as %+v", gold)
	}

	if counted, err := calendars.CountServiceLevels(ctx, office.ID); err != nil || counted != 1 {
		t.Errorf("the office calendar counts %d levels, %v; want Gold", counted, err)
	}

	if queues, tickets, err := levels.CountUses(ctx, gold.ID); err != nil || queues != 0 || tickets != 0 {
		t.Errorf("a new level is used by %d queues and %d tickets, %v", queues, tickets, err)
	}

	if _, err := levels.Save(ctx, &model.ServiceLevel{Name: "Gold"}); apperror.KindOf(err) != apperror.KindConflict {
		t.Errorf("a second Gold was answered %v, want a conflict", err)
	}

	gold.CalendarID = nil

	around, err := levels.Update(ctx, gold)
	if err != nil || around.CalendarID != nil {
		t.Errorf("taking Gold's calendar off read back as %+v, %v", around, err)
	}
}
