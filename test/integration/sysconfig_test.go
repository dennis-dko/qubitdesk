//go:build integration

package integration

import (
	"net/http"
	"strconv"
	"testing"
)

type configList struct {
	Items []struct {
		Name     string `json:"name"`
		Access   string `json:"access"`
		Default  any    `json:"default"`
		Value    any    `json:"value"`
		Modified bool   `json:"modified"`
	} `json:"items"`
}

// valueOf is what the listing says an option is set to, and whether it lists it.
func (l configList) valueOf(name string) (any, bool, bool) {
	for _, item := range l.Items {
		if item.Name == name {
			return item.Value, item.Modified, true
		}
	}

	return nil, false, false
}

// An option is changed, read back by an agent, refused a value it cannot hold,
// and put back to its default, over HTTP with its name in the path - the colons
// written plainly and escaped - and the administration log names the option.
func TestTheSystemConfigurationIsChangedAndPutBackOverHTTP(t *testing.T) {
	t.Parallel()

	_, admin, worker := startWithWorker(t)

	var before configList

	admin.must(admin.api(http.MethodGet, "/settings/sysconfig", nil), http.StatusOK).Data(t, &before)

	if value, modified, listed := before.valueOf("Ticket::Hook"); !listed || value != "Ticket#" || modified {
		t.Fatalf("Ticket::Hook reads as %v, modified %t, listed %t; want its default", value, modified, listed)
	}

	admin.must(admin.api(http.MethodPut, "/settings/sysconfig/Ticket::Hook", map[string]any{"value": "Call#"}),
		http.StatusOK)

	for _, refused := range []struct {
		path  string
		value any
		want  int
	}{
		{"/settings/sysconfig/Ticket::Hook", 5, http.StatusBadRequest},
		{"/settings/sysconfig/Ticket::Hook", "a hook far longer than twenty", http.StatusBadRequest},
		{"/settings/sysconfig/Ticket::Nothing", "x", http.StatusNotFound},
	} {
		if got := admin.api(http.MethodPut, refused.path, map[string]any{"value": refused.value}); got.Status != refused.want {
			t.Errorf("%s with %v was answered %d, want %d", refused.path, refused.value, got.Status, refused.want)
		}
	}

	var seen configList

	worker.must(worker.api(http.MethodGet, "/settings/sysconfig", nil), http.StatusOK).Data(t, &seen)

	if value, modified, _ := seen.valueOf("Ticket::Hook"); value != "Call#" || !modified {
		t.Errorf("an agent reads Ticket::Hook as %v, modified %t; want the value the administrator chose", value, modified)
	}

	for _, item := range seen.Items {
		if item.Access != "external" {
			t.Errorf("an agent was handed %s, which is %s", item.Name, item.Access)
		}
	}

	if refused := worker.api(http.MethodPut, "/settings/sysconfig/Ticket::Hook", map[string]any{"value": "Mine#"}); refused.Status != http.StatusForbidden {
		t.Errorf("an agent changing an option was answered %d, want 403", refused.Status)
	}

	admin.must(admin.api(http.MethodDelete, "/settings/sysconfig/Ticket%3A%3AHook", nil), http.StatusNoContent, http.StatusOK)

	var after configList

	admin.must(admin.api(http.MethodGet, "/settings/sysconfig", nil), http.StatusOK).Data(t, &after)

	if value, modified, _ := after.valueOf("Ticket::Hook"); value != "Ticket#" || modified {
		t.Errorf("after the reset Ticket::Hook reads as %v, modified %t; want its default", value, modified)
	}

	var log adminChanges

	admin.must(admin.api(http.MethodGet, "/admin/changes", nil), http.StatusOK).Data(t, &log)

	if len(log.Entries) < 2 || log.Entries[0].Action != "sysconfig.reset" || log.Entries[0].Detail != "Ticket::Hook" ||
		log.Entries[1].Action != "sysconfig.set" || log.Entries[1].Detail != "Ticket::Hook" {
		t.Errorf("the administration log reads %+v; want the reset and the change, each naming Ticket::Hook", log.Entries)
	}
}

// The tickets obey a switch of the system configuration through the whole stack:
// with Ticket::UnlockOnMove off a moved ticket keeps its lock, and put back to
// its default the switch releases it again, as KIX18 does.
func TestTheTicketsObeyTheSystemConfiguration(t *testing.T) {
	t.Parallel()

	a := start(t)
	admin := a.signInAsAdmin("a-much-better-password")
	desk := theDesk(t, admin)

	var board struct {
		ID uint `json:"id"`
	}

	admin.must(admin.api(http.MethodPost, "/queues", map[string]any{"name": "Board"}), http.StatusCreated).
		Data(t, &board)

	movedLocked := func(title string) ticketResponse {
		var ticket ticketResponse

		admin.must(admin.api(http.MethodPost, "/tickets", map[string]any{
			"title": title, "queueId": desk, "body": "It is broken.",
		}), http.StatusCreated).Data(t, &ticket)
		admin.must(admin.api(http.MethodPut, ticketPath(ticket.ID), map[string]any{"locked": true}), http.StatusOK)
		admin.must(admin.api(http.MethodPut, ticketPath(ticket.ID), map[string]any{"queueId": board.ID}),
			http.StatusOK).Data(t, &ticket)

		return ticket
	}

	admin.must(admin.api(http.MethodPut, "/settings/sysconfig/Ticket::UnlockOnMove", map[string]any{"value": false}),
		http.StatusOK)

	if kept := movedLocked("Printer"); !kept.Locked {
		t.Error("with Ticket::UnlockOnMove off a moved ticket lost its lock")
	}

	admin.must(admin.api(http.MethodDelete, "/settings/sysconfig/Ticket::UnlockOnMove", nil),
		http.StatusNoContent, http.StatusOK)

	if released := movedLocked("Scanner"); released.Locked {
		t.Error("with Ticket::UnlockOnMove at its default a moved ticket kept its lock")
	}
}

// The installation's calendar is named over HTTP and through the database: a
// calendar there is is taken and handed out as its id, one that is not is
// refused, the calendar is not deleted while it is named, and once the option
// is put back to none it is.
func TestTheInstallationsCalendarIsNamedOverHTTP(t *testing.T) {
	t.Parallel()

	a := start(t)
	admin := a.signInAsAdmin("a-much-better-password")

	var office struct {
		ID uint `json:"id"`
	}

	admin.must(admin.api(http.MethodPost, "/calendars", map[string]any{
		"name": "Office", "hours": []map[string]any{{"weekday": 1, "from": "08:00", "to": "17:00"}},
	}), http.StatusCreated).Data(t, &office)

	admin.must(admin.api(http.MethodPut, "/settings/sysconfig/TimeWorkingHours", map[string]any{"value": office.ID}),
		http.StatusOK)

	if got := admin.api(http.MethodPut, "/settings/sysconfig/TimeWorkingHours", map[string]any{"value": 999}); got.Status != http.StatusBadRequest {
		t.Errorf("naming a calendar there is not was answered %d, want 400", got.Status)
	}

	var listed configList

	admin.must(admin.api(http.MethodGet, "/settings/sysconfig", nil), http.StatusOK).Data(t, &listed)

	if value, modified, _ := listed.valueOf("TimeWorkingHours"); value != float64(office.ID) || !modified {
		t.Errorf("the option reads %v, modified %t; want the office calendar's id", value, modified)
	}

	path := "/calendars/" + strconv.FormatUint(uint64(office.ID), 10)

	if got := admin.api(http.MethodDelete, path, nil); got.Status != http.StatusConflict ||
		errorCode(t, got) != "calendarCountsTheInstallation" {
		t.Errorf("deleting the installation's calendar was answered %d %s", got.Status, got.Body)
	}

	admin.must(admin.api(http.MethodDelete, "/settings/sysconfig/TimeWorkingHours", nil), http.StatusNoContent, http.StatusOK)
	admin.must(admin.api(http.MethodDelete, path, nil), http.StatusNoContent, http.StatusOK)
}
