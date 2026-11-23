//go:build integration

package integration

import (
	"net/http"
	"strconv"
	"testing"
)

// A service level is made in a calendar, read by an agent, corrected, and kept
// from deletion with its calendar while it counts in it; deleted, it frees the
// calendar to go too.
func TestAServiceLevelIsAdministeredOverHTTP(t *testing.T) {
	t.Parallel()

	_, admin, worker := startWithWorker(t)

	var calendar, level struct {
		ID                   uint   `json:"id"`
		Name                 string `json:"name"`
		CalendarID           *uint  `json:"calendarId"`
		FirstResponseMinutes int    `json:"firstResponseMinutes"`
		SolutionMinutes      int    `json:"solutionMinutes"`
	}

	admin.must(admin.api(http.MethodPost, "/calendars", map[string]any{
		"name": "Office", "hours": []map[string]any{{"weekday": 1, "from": "08:00", "to": "17:00"}},
	}), http.StatusCreated).Data(t, &calendar)

	admin.must(admin.api(http.MethodPost, "/service-levels", map[string]any{
		"name": "Gold", "calendarId": calendar.ID, "firstResponseMinutes": 60, "solutionMinutes": 480,
	}), http.StatusCreated).Data(t, &level)

	if level.CalendarID == nil || *level.CalendarID != calendar.ID || level.SolutionMinutes != 480 {
		t.Errorf("Gold reads back as %+v", level)
	}

	levelPath := "/service-levels/" + strconv.FormatUint(uint64(level.ID), 10)
	calendarPath := "/calendars/" + strconv.FormatUint(uint64(calendar.ID), 10)

	worker.must(worker.api(http.MethodGet, levelPath, nil), http.StatusOK)

	if refused := worker.api(http.MethodPut, levelPath, map[string]any{"solutionMinutes": 1}); refused.Status != http.StatusForbidden {
		t.Errorf("an agent changing a service level was answered %d, want 403", refused.Status)
	}

	admin.must(admin.api(http.MethodPut, levelPath, map[string]any{"firstResponseMinutes": 30}), http.StatusOK).
		Data(t, &level)

	if level.FirstResponseMinutes != 30 || level.SolutionMinutes != 480 {
		t.Errorf("after the correction Gold reads %+v", level)
	}

	if bad := admin.api(http.MethodPost, "/service-levels", map[string]any{"name": "Lead", "solutionMinutes": -1}); bad.Status != http.StatusBadRequest {
		t.Errorf("a promise of minus a minute was answered %d, want 400", bad.Status)
	}

	inUse := admin.api(http.MethodDelete, calendarPath, nil)
	if inUse.Status != http.StatusConflict || errorCode(t, inUse) != "calendarInUse" {
		t.Errorf("deleting the calendar Gold counts in was answered %d %s, want 409 calendarInUse", inUse.Status, inUse.Body)
	}

	admin.must(admin.api(http.MethodDelete, levelPath, nil), http.StatusNoContent, http.StatusOK)
	admin.must(admin.api(http.MethodDelete, calendarPath, nil), http.StatusNoContent, http.StatusOK)
}
