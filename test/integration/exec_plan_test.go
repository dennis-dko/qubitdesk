//go:build integration

package integration

import (
	"net/http"
	"slices"
	"strconv"
	"testing"
)

type execPlanResponse struct {
	ID       uint     `json:"id"`
	Name     string   `json:"name"`
	Type     string   `json:"type"`
	Weekdays []int    `json:"weekdays"`
	Times    []string `json:"times"`
	Events   []string `json:"events"`
}

// An execution plan is made with its times as the wall reads them, read back so,
// refused a time of day there is not, turned from times to events, and deleted,
// over HTTP and through the database; the list tells a form every event there
// is, and the administration log names each change.
func TestAnExecutionPlanIsSetUpOverHTTP(t *testing.T) {
	t.Parallel()

	a := start(t)
	admin := a.signInAsAdmin("a-much-better-password")

	var made execPlanResponse

	admin.must(admin.api(http.MethodPost, "/exec-plans", map[string]any{
		"name": "Weekday mornings", "type": "TimeBased", "weekdays": []int{1, 2, 3, 4, 5}, "times": []string{"17:30", "08:00"},
	}), http.StatusCreated).Data(t, &made)

	if made.Type != "TimeBased" || !slices.Equal(made.Weekdays, []int{1, 2, 3, 4, 5}) ||
		!slices.Equal(made.Times, []string{"08:00", "17:30"}) || len(made.Events) != 0 {
		t.Fatalf("the plan was made as %+v", made)
	}

	for _, refused := range [][]string{{"25:00"}, {"24:00"}, {"8am"}} {
		if got := admin.api(http.MethodPost, "/exec-plans", map[string]any{
			"name": "Never", "type": "TimeBased", "weekdays": []int{1}, "times": refused,
		}); got.Status != http.StatusBadRequest {
			t.Errorf("a plan at %v was answered %d, want 400", refused, got.Status)
		}
	}

	var list struct {
		Items  []execPlanResponse `json:"items"`
		Events []string           `json:"events"`
	}

	admin.must(admin.api(http.MethodGet, "/exec-plans", nil), http.StatusOK).Data(t, &list)

	if len(list.Items) != 1 || !slices.Contains(list.Events, "TicketCreate") || !slices.Contains(list.Events, "ArticleCreate") {
		t.Errorf("the list reads %+v; want the plan and the events there are", list)
	}

	path := "/exec-plans/" + strconv.FormatUint(uint64(made.ID), 10)

	var turned execPlanResponse

	admin.must(admin.api(http.MethodPut, path, map[string]any{"type": "EventBased", "events": []string{"TicketCreate"}}),
		http.StatusOK).Data(t, &turned)

	if turned.Type != "EventBased" || len(turned.Weekdays) != 0 || len(turned.Times) != 0 ||
		!slices.Equal(turned.Events, []string{"TicketCreate"}) {
		t.Errorf("the plan turned to events reads %+v", turned)
	}

	admin.must(admin.api(http.MethodDelete, path, nil), http.StatusNoContent, http.StatusOK)
	admin.must(admin.api(http.MethodGet, path, nil), http.StatusNotFound)

	var log adminChanges

	admin.must(admin.api(http.MethodGet, "/admin/changes", nil), http.StatusOK).Data(t, &log)

	if len(log.Entries) < 3 || log.Entries[0].Action != "execPlan.deleted" || log.Entries[1].Action != "execPlan.updated" ||
		log.Entries[2].Action != "execPlan.created" || log.Entries[2].Detail != "Weekday mornings" {
		t.Errorf("the administration log reads %+v; want the plan made, changed and deleted", log.Entries)
	}
}
