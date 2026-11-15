package sqldb_test

import (
	"context"
	"testing"
	"time"

	"github.com/dennis-dko/go-ticket-system/internal/domain/model"
	"github.com/dennis-dko/go-ticket-system/internal/infrastructure/persistence/sqldb"
	"github.com/dennis-dko/go-ticket-system/internal/support/apperror"
)

// A calendar is stored with its hours and free days and read back as it was;
// correcting it replaces both rather than adding to them; a second calendar of
// the same name is refused, and deleting one takes its hours and days with it.
func TestACalendarIsStoredWithItsHoursAndFreeDays(t *testing.T) {
	ctx := context.Background()
	calendars := sqldb.NewCalendarRepository(newTestDB(t), sqldb.DialectSQLite)

	christmasEve := time.Date(2026, time.December, 24, 0, 0, 0, 0, time.UTC)

	saved, err := calendars.Save(ctx, &model.Calendar{
		Name: "Service hours", Zone: "Europe/Berlin", HolidayPlace: "DE-BY", Valid: true,
		Hours: []model.WorkingHours{
			{Weekday: time.Monday, From: 13 * 60, To: 17 * 60},
			{Weekday: time.Monday, From: 8 * 60, To: 12 * 60},
		},
		FreeDays: []model.FreeDay{{Day: christmasEve, Recurring: true, Name: "Christmas Eve"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(saved.Hours) != 2 || saved.Hours[0].From != 8*60 || saved.Hours[0].Weekday != time.Monday {
		t.Errorf("the hours read back as %+v; want both, the morning first", saved.Hours)
	}

	if len(saved.FreeDays) != 1 || !saved.FreeDays[0].Day.Equal(christmasEve) || !saved.FreeDays[0].Recurring {
		t.Errorf("the free days read back as %+v", saved.FreeDays)
	}

	saved.Hours = []model.WorkingHours{{Weekday: time.Tuesday, From: 9 * 60, To: 15 * 60}}
	saved.FreeDays = nil

	corrected, err := calendars.Update(ctx, saved)
	if err != nil {
		t.Fatal(err)
	}

	if len(corrected.Hours) != 1 || corrected.Hours[0].Weekday != time.Tuesday || len(corrected.FreeDays) != 0 {
		t.Errorf("after the correction the calendar reads %+v", corrected)
	}

	if _, err := calendars.Save(ctx, &model.Calendar{Name: "Service hours"}); apperror.KindOf(err) != apperror.KindConflict {
		t.Errorf("a second calendar of the same name was answered %v, want a conflict", err)
	}

	if err := calendars.Delete(ctx, saved.ID); err != nil {
		t.Fatal(err)
	}

	if _, err := calendars.GetByID(ctx, saved.ID); apperror.KindOf(err) != apperror.KindNotFound {
		t.Errorf("a deleted calendar was answered %v, want not found", err)
	}
}
