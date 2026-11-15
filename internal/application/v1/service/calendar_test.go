package service_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/dennis-dko/go-ticket-system/internal/application/v1/command"
	"github.com/dennis-dko/go-ticket-system/internal/application/v1/service"
	"github.com/dennis-dko/go-ticket-system/internal/domain/model"
	"github.com/dennis-dko/go-ticket-system/internal/infrastructure/persistence/memory"
	"github.com/dennis-dko/go-ticket-system/internal/support/apperror"
)

// A calendar has every field it cannot hold refused at once, is kept trimmed and
// with its free days as calendar days, and is corrected field by field: what a
// correction leaves out stays as it was, and hours or free days that are sent
// replace the old ones.
func TestACalendarIsCheckedAndCorrectedFieldByField(t *testing.T) {
	ctx := context.Background()
	calendars := service.NewCalendarApplicationService(memory.NewCalendarRepository())

	_, err := calendars.Create(ctx, command.CalendarCommand{
		Name: ptr(" "), Zone: ptr("Europe/Munich"), HolidayPlace: ptr("XX-YY"),
		Hours:    &[]model.WorkingHours{{Weekday: time.Monday, From: 600, To: 480}},
		FreeDays: &[]model.FreeDay{{Name: "a day with no date"}},
	})

	detail, ok := apperror.Detail(err)
	if !ok {
		t.Fatalf("a calendar holding nothing it can was answered %v", err)
	}

	for _, field := range []string{"name", "zone", "holidayPlace", "hours", "freeDays"} {
		if !slices.Contains(detail.Fields, field) {
			t.Errorf("the refusal names %v, and not %s", detail.Fields, field)
		}
	}

	made, err := calendars.Create(ctx, command.CalendarCommand{
		Name: ptr(" Service hours "), Zone: ptr("Europe/Berlin"),
		Hours: &[]model.WorkingHours{{Weekday: time.Monday, From: 480, To: 720}},
		FreeDays: &[]model.FreeDay{{
			Day: time.Date(2026, time.December, 24, 15, 0, 0, 0, time.UTC), Name: " Christmas Eve ",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}

	if made.Name != "Service hours" || !made.Valid || len(made.FreeDays) != 1 ||
		!made.FreeDays[0].Day.Equal(time.Date(2026, time.December, 24, 0, 0, 0, 0, time.UTC)) ||
		made.FreeDays[0].Name != "Christmas Eve" {
		t.Errorf("the calendar was kept as %+v", made)
	}

	corrected, err := calendars.Update(ctx, made.ID, command.CalendarCommand{Description: ptr("Weekdays")})
	if err != nil || corrected.Zone != "Europe/Berlin" || len(corrected.Hours) != 1 || len(corrected.FreeDays) != 1 {
		t.Fatalf("a correction of the description left the calendar as %+v, %v", corrected, err)
	}

	corrected, err = calendars.Update(ctx, made.ID, command.CalendarCommand{Hours: &[]model.WorkingHours{}})
	if err != nil || len(corrected.Hours) != 0 || len(corrected.FreeDays) != 1 {
		t.Errorf("sending no hours left the calendar as %+v, %v; want the hours gone and the free day kept", corrected, err)
	}
}
