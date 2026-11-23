//go:build integration

package integration

import (
	"net/http"
	"strconv"
	"testing"
)

type calendarResponse struct {
	ID           uint   `json:"id"`
	Name         string `json:"name"`
	Zone         string `json:"zone"`
	HolidayPlace string `json:"holidayPlace"`
	Hours        []struct {
		Weekday int    `json:"weekday"`
		From    string `json:"from"`
		To      string `json:"to"`
	} `json:"hours"`
	FreeDays []struct {
		Day       string `json:"day"`
		Recurring bool   `json:"recurring"`
		Name      string `json:"name"`
	} `json:"freeDays"`
}

// A calendar is made with its hours and free days, read back as it was sent,
// corrected - the lists replaced rather than added to - and deleted, over HTTP
// by whoever sets up queues; hours that overlap, a zone the runtime does not
// know, a time that cannot be read, a place that is none and a name already
// taken are refused, and an agent is refused it all.
func TestACalendarIsAdministeredOverHTTP(t *testing.T) {
	t.Parallel()

	_, admin, worker := startWithWorker(t)

	var calendar calendarResponse

	admin.must(admin.api(http.MethodPost, "/calendars", map[string]any{
		"name": "Service hours", "zone": "Europe/Berlin", "holidayPlace": "DE-BY",
		"hours": []map[string]any{
			{"weekday": 1, "from": "13:00", "to": "17:00"},
			{"weekday": 1, "from": "08:00", "to": "12:00"},
			{"weekday": 5, "from": "20:00", "to": "24:00"},
		},
		"freeDays": []map[string]any{{"day": "2026-12-24", "recurring": true, "name": "Christmas Eve"}},
	}), http.StatusCreated).Data(t, &calendar)

	if len(calendar.Hours) != 3 || calendar.Hours[0].From != "08:00" || calendar.Hours[2].To != "24:00" ||
		len(calendar.FreeDays) != 1 || calendar.FreeDays[0].Day != "2026-12-24" || !calendar.FreeDays[0].Recurring {
		t.Errorf("the calendar reads back as %+v", calendar)
	}

	path := "/calendars/" + strconv.FormatUint(uint64(calendar.ID), 10)

	admin.must(admin.api(http.MethodPut, path, map[string]any{
		"hours": []map[string]any{{"weekday": 2, "from": "09:00", "to": "15:00"}}, "freeDays": []map[string]any{},
	}), http.StatusOK).Data(t, &calendar)

	if len(calendar.Hours) != 1 || calendar.Hours[0].Weekday != 2 || len(calendar.FreeDays) != 0 ||
		calendar.Zone != "Europe/Berlin" {
		t.Errorf("after the correction the calendar reads %+v", calendar)
	}

	for _, refused := range []struct {
		what string
		body map[string]any
		want int
	}{
		{"overlapping hours", map[string]any{"name": "Overlap", "hours": []map[string]any{
			{"weekday": 1, "from": "08:00", "to": "12:00"}, {"weekday": 1, "from": "11:00", "to": "13:00"}}},
			http.StatusBadRequest},
		{"a zone nobody knows", map[string]any{"name": "Munich", "zone": "Europe/Munich"}, http.StatusBadRequest},
		{"a time that cannot be read", map[string]any{"name": "Clock", "hours": []map[string]any{
			{"weekday": 1, "from": "8:00", "to": "12:00"}}}, http.StatusBadRequest},
		{"a place that is none", map[string]any{"name": "Nowhere", "holidayPlace": "XX-YY"}, http.StatusBadRequest},
		{"a name already taken", map[string]any{"name": "Service hours"}, http.StatusConflict},
	} {
		if got := admin.api(http.MethodPost, "/calendars", refused.body); got.Status != refused.want {
			t.Errorf("%s was answered %d %s, want %d", refused.what, got.Status, got.Body, refused.want)
		}
	}

	if got := worker.api(http.MethodGet, "/calendars", nil); got.Status != http.StatusForbidden {
		t.Errorf("an agent listing the calendars was answered %d, want 403", got.Status)
	}

	admin.must(admin.api(http.MethodDelete, path, nil), http.StatusNoContent, http.StatusOK)

	if got := admin.api(http.MethodGet, path, nil); got.Status != http.StatusNotFound {
		t.Errorf("a deleted calendar was answered %d, want 404", got.Status)
	}
}
