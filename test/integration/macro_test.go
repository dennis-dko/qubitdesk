//go:build integration

package integration

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
)

type macroResponse struct {
	ID      uint   `json:"id"`
	Name    string `json:"name"`
	Valid   bool   `json:"valid"`
	Actions []struct {
		Type       string            `json:"type"`
		Parameters map[string]string `json:"parameters"`
	} `json:"actions"`
}

// A macro is made, read back with its steps in order, refused a step that could
// not be taken - naming the step and the parameter - changed, and deleted, over
// HTTP and through the database; reading the macros tells a form every step
// there is; an agent is not let in; and the administration log names each
// change by the macro's name.
func TestAMacroIsSetUpOverHTTP(t *testing.T) {
	t.Parallel()

	_, admin, worker := startWithWorker(t)

	var states catalogList

	admin.must(admin.api(http.MethodGet, "/ticket-states", nil), http.StatusOK).Data(t, &states)

	named := map[string]string{}
	for _, state := range states.Items {
		named[state.Name] = strconv.FormatUint(uint64(state.ID), 10)
	}

	steps := []map[string]any{
		{"type": "StateSet", "parameters": map[string]string{"stateId": named["pending-reminder"], "pendingMinutes": "120"}},
		{"type": "TeamSet", "parameters": map[string]string{"queueId": strconv.FormatUint(uint64(theDesk(t, admin)), 10)}},
		{"type": "ArticleCreate", "parameters": map[string]string{"channel": "note", "body": "Waiting for the supplier."}},
	}

	var made macroResponse

	admin.must(admin.api(http.MethodPost, "/macros", map[string]any{"name": "Wait for the supplier", "actions": steps}),
		http.StatusCreated).Data(t, &made)

	if len(made.Actions) != 3 || made.Actions[0].Type != "StateSet" || made.Actions[2].Parameters["body"] != "Waiting for the supplier." ||
		made.Actions[0].Parameters["pendingMinutes"] != "120" {
		t.Fatalf("the macro was made as %+v; want its three steps as sent", made)
	}

	var list struct {
		Items []macroResponse `json:"items"`
		Steps []struct {
			Type       string `json:"type"`
			Parameters []struct {
				Name     string `json:"name"`
				Kind     string `json:"kind"`
				Required bool   `json:"required"`
			} `json:"parameters"`
		} `json:"steps"`
	}

	admin.must(admin.api(http.MethodGet, "/macros", nil), http.StatusOK).Data(t, &list)

	if len(list.Items) != 1 || len(list.Steps) == 0 || list.Steps[0].Type != "StateSet" ||
		list.Steps[0].Parameters[0].Name != "stateId" || list.Steps[0].Parameters[0].Kind != "state" ||
		!list.Steps[0].Parameters[0].Required {
		t.Errorf("the list reads %+v; want the macro and every step with what it reads", list)
	}

	refused := admin.api(http.MethodPost, "/macros", map[string]any{"name": "Merge it", "actions": []map[string]any{
		{"type": "LockSet", "parameters": map[string]string{"locked": "true"}},
		{"type": "StateSet", "parameters": map[string]string{"stateId": named["merged"]}},
	}})

	var refusal struct {
		Error struct {
			Code   string `json:"code"`
			Values []any  `json:"values"`
		} `json:"error"`
	}

	if err := json.Unmarshal(refused.Body, &refusal); err != nil || refused.Status != http.StatusBadRequest ||
		refusal.Error.Code != "macroStepInvalid" || len(refusal.Error.Values) != 2 ||
		refusal.Error.Values[0] != float64(2) || refusal.Error.Values[1] != "stateId" {
		t.Errorf("a macro merging tickets was answered %d %s; want step 2 refused for stateId", refused.Status, refused.Body)
	}

	path := "/macros/" + strconv.FormatUint(uint64(made.ID), 10)

	var changed macroResponse

	admin.must(admin.api(http.MethodPut, path, map[string]any{"actions": []map[string]any{
		{"type": "LockSet", "parameters": map[string]string{"locked": "false"}},
	}, "valid": false}), http.StatusOK).Data(t, &changed)

	if len(changed.Actions) != 1 || changed.Actions[0].Type != "LockSet" || changed.Valid {
		t.Errorf("the change reads %+v; want the one step in place of three, retired", changed)
	}

	if got := worker.api(http.MethodGet, "/macros", nil); got.Status != http.StatusForbidden {
		t.Errorf("an agent reading the macros was answered %d, want 403", got.Status)
	}

	admin.must(admin.api(http.MethodDelete, path, nil), http.StatusNoContent, http.StatusOK)
	admin.must(admin.api(http.MethodGet, path, nil), http.StatusNotFound)

	var log adminChanges

	admin.must(admin.api(http.MethodGet, "/admin/changes", nil), http.StatusOK).Data(t, &log)

	if len(log.Entries) < 3 || log.Entries[0].Action != "macro.deleted" || log.Entries[1].Action != "macro.updated" ||
		log.Entries[2].Action != "macro.created" || log.Entries[2].Detail != "Wait for the supplier" {
		t.Errorf("the administration log reads %+v; want the macro made, changed and deleted", log.Entries)
	}
}
