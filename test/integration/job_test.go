//go:build integration

package integration

import (
	"net/http"
	"slices"
	"strconv"
	"testing"
)

type jobResponse struct {
	ID       uint `json:"id"`
	Criteria struct {
		QueueIDs []uint `json:"queueIds"`
		Locked   *bool  `json:"locked"`
	} `json:"criteria"`
	MacroIDs  []uint  `json:"macroIds"`
	PlanIDs   []uint  `json:"planIds"`
	LastRunAt *string `json:"lastRunAt"`
}

// A job is made with what it names, refused criteria that would take every
// ticket, changed and deleted over HTTP and through the database; the macro it
// runs is not deleted from under it, an agent is not let in, and the
// administration log names each change.
func TestAJobIsSetUpOverHTTP(t *testing.T) {
	t.Parallel()

	_, admin, worker := startWithWorker(t)
	desk := theDesk(t, admin)

	var macro, plan struct {
		ID uint `json:"id"`
	}

	admin.must(admin.api(http.MethodPost, "/macros", map[string]any{"name": "Escalate", "actions": []map[string]any{
		{"type": "LockSet", "parameters": map[string]string{"locked": "false"}},
	}}), http.StatusCreated).Data(t, &macro)
	admin.must(admin.api(http.MethodPost, "/exec-plans", map[string]any{
		"name": "Arrivals", "type": "EventBased", "events": []string{"TicketCreate"},
	}), http.StatusCreated).Data(t, &plan)

	var made jobResponse

	admin.must(admin.api(http.MethodPost, "/jobs", map[string]any{
		"name": "Escalate the desk", "criteria": map[string]any{"queueIds": []uint{desk}, "locked": true},
		"macroIds": []uint{macro.ID, macro.ID}, "planIds": []uint{plan.ID},
	}), http.StatusCreated).Data(t, &made)

	if !slices.Equal(made.Criteria.QueueIDs, []uint{desk}) || made.Criteria.Locked == nil || !*made.Criteria.Locked ||
		!slices.Equal(made.MacroIDs, []uint{macro.ID, macro.ID}) || !slices.Equal(made.PlanIDs, []uint{plan.ID}) ||
		made.LastRunAt != nil {
		t.Fatalf("the job was made as %+v", made)
	}

	if got := admin.api(http.MethodPost, "/jobs", map[string]any{
		"name": "Everything", "criteria": map[string]any{}, "macroIds": []uint{macro.ID},
	}); got.Status != http.StatusBadRequest {
		t.Errorf("a job taking every ticket there is was answered %d, want 400", got.Status)
	}

	macroPath := "/macros/" + strconv.FormatUint(uint64(macro.ID), 10)
	if got := admin.api(http.MethodDelete, macroPath, nil); got.Status != http.StatusConflict || errorCode(t, got) != "macroInUse" {
		t.Errorf("deleting the macro a job runs was answered %d %s", got.Status, got.Body)
	}

	path := "/jobs/" + strconv.FormatUint(uint64(made.ID), 10)

	var changed jobResponse

	admin.must(admin.api(http.MethodPut, path, map[string]any{"planIds": []uint{}}), http.StatusOK).Data(t, &changed)

	if len(changed.PlanIDs) != 0 || len(changed.MacroIDs) != 2 {
		t.Errorf("the job without its plan reads %+v", changed)
	}

	if got := worker.api(http.MethodGet, "/jobs", nil); got.Status != http.StatusForbidden {
		t.Errorf("an agent reading the jobs was answered %d, want 403", got.Status)
	}

	admin.must(admin.api(http.MethodDelete, path, nil), http.StatusNoContent, http.StatusOK)
	admin.must(admin.api(http.MethodDelete, macroPath, nil), http.StatusNoContent, http.StatusOK)

	var log adminChanges

	admin.must(admin.api(http.MethodGet, "/admin/changes", nil), http.StatusOK).Data(t, &log)

	if len(log.Entries) < 3 || log.Entries[0].Action != "macro.deleted" || log.Entries[1].Action != "job.deleted" ||
		log.Entries[2].Action != "job.updated" {
		t.Errorf("the administration log reads %+v; want the job changed and deleted, then the macro", log.Entries)
	}
}
