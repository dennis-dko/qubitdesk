package service_test

import (
	"context"
	"slices"
	"testing"

	"github.com/dennis-dko/qubitdesk/internal/application/v1/command"
	"github.com/dennis-dko/qubitdesk/internal/application/v1/service"
	"github.com/dennis-dko/qubitdesk/internal/domain/model"
	"github.com/dennis-dko/qubitdesk/internal/infrastructure/persistence/memory"
	"github.com/dennis-dko/qubitdesk/internal/support/apperror"
)

// A service level has every field it cannot hold refused at once; a calendar no
// longer offered is not chosen anew and stays where it was chosen; and neither a
// level queues or tickets use nor a calendar a level counts in is deleted -
// each refusal says how many use it.
func TestAServiceLevelIsCheckedAndKeptWhileInUse(t *testing.T) {
	ctx := context.Background()
	calendars := memory.NewCalendarRepository()
	levels := memory.NewServiceLevelRepository()
	slas := service.NewSLAApplicationService(levels, calendars)
	calendarService := service.NewCalendarApplicationService(calendars)

	office, err := calendars.Save(ctx, &model.Calendar{Name: "Office", Valid: true})
	if err != nil {
		t.Fatal(err)
	}

	_, err = slas.Create(ctx, command.ServiceLevelCommand{
		Name: ptr(""), CalendarID: ptr(uint(99)), FirstResponseMinutes: ptr(-1),
		UpdateMinutes: ptr(model.MaxServiceMinutes + 1), SolutionMinutes: ptr(-5),
	})

	detail, ok := apperror.Detail(err)
	if !ok {
		t.Fatalf("a service level holding nothing it can was answered %v", err)
	}

	for _, field := range []string{"name", "calendarId", "firstResponseMinutes", "updateMinutes", "solutionMinutes"} {
		if !slices.Contains(detail.Fields, field) {
			t.Errorf("the refusal names %v, and not %s", detail.Fields, field)
		}
	}

	gold, err := slas.Create(ctx, command.ServiceLevelCommand{
		Name: ptr("Gold"), CalendarID: &office.ID, FirstResponseMinutes: ptr(60), SolutionMinutes: ptr(480),
	})
	if err != nil || gold.CalendarID == nil || *gold.CalendarID != office.ID || !gold.Valid {
		t.Fatalf("making Gold answered %+v, %v", gold, err)
	}

	office.Valid = false
	if _, err := calendars.Update(ctx, office); err != nil {
		t.Fatal(err)
	}

	if _, err := slas.Create(ctx, command.ServiceLevelCommand{Name: ptr("Silver"), CalendarID: &office.ID}); apperror.KindOf(err) != apperror.KindInvalid {
		t.Errorf("a retired calendar chosen for a new level was answered %v, want it refused", err)
	}

	if kept, err := slas.Update(ctx, gold.ID, command.ServiceLevelCommand{CalendarID: &office.ID, UpdateMinutes: ptr(30)}); err != nil || kept.UpdateMinutes != 30 {
		t.Errorf("Gold keeping its retired calendar answered %+v, %v", kept, err)
	}

	if cleared, err := slas.Update(ctx, gold.ID, command.ServiceLevelCommand{ClearCalendar: true}); err != nil || cleared.CalendarID != nil {
		t.Errorf("taking Gold's calendar off answered %+v, %v", cleared, err)
	}

	levels.UseIn(gold.ID, 1, 3)

	if detail, ok := apperror.Detail(slas.Delete(ctx, gold.ID)); !ok || detail.Code != "serviceLevelInUse" ||
		!slices.Equal(detail.Values, []any{"Gold", 1, 3}) {
		t.Errorf("deleting Gold while in use was answered %+v", detail)
	}

	calendars.CountIn(office.ID, 2)

	if detail, ok := apperror.Detail(calendarService.Delete(ctx, office.ID)); !ok || detail.Code != "calendarInUse" ||
		!slices.Equal(detail.Values, []any{"Office", 2}) {
		t.Errorf("deleting the office calendar while in use was answered %+v", detail)
	}
}
