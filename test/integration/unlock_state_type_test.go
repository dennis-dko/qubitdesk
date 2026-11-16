//go:build integration

package integration

import (
	"net/http"
	"testing"
)

// The state types a lock runs out in are read and named over HTTP and through
// the database: new and open as shipped, another added beside them, a list
// leaving one of them out refused, an agent not let in, and the change in the
// administration log.
func TestTheStateTypesALockRunsOutInAreNamedOverHTTP(t *testing.T) {
	t.Parallel()

	_, admin, worker := startWithWorker(t)

	type types struct {
		Items []struct {
			Type     string `json:"type"`
			IsSystem bool   `json:"isSystem"`
		} `json:"items"`
	}

	named := func(list types) string {
		var out string
		for _, entry := range list.Items {
			out += entry.Type
			if entry.IsSystem {
				out += "!"
			}

			out += " "
		}

		return out
	}

	var shipped types

	admin.must(admin.api(http.MethodGet, "/unlock-state-types", nil), http.StatusOK).Data(t, &shipped)

	if got := named(shipped); got != "new! open! " {
		t.Fatalf("a fresh installation lets locks run out in %q, want new and open as shipped", got)
	}

	var added types

	admin.must(admin.api(http.MethodPut, "/unlock-state-types", map[string]any{
		"types": []string{"new", "open", "pending-reminder"},
	}), http.StatusOK).Data(t, &added)

	if got := named(added); got != "new! open! pending-reminder " {
		t.Errorf("after adding the waiting type the list reads %q", got)
	}

	refused := admin.api(http.MethodPut, "/unlock-state-types", map[string]any{"types": []string{"open"}})
	if refused.Status != http.StatusConflict || errorCode(t, refused) != "unlockStateTypeShipped" {
		t.Errorf("a list leaving new out was answered %d %s", refused.Status, refused.Body)
	}

	if got := admin.api(http.MethodPut, "/unlock-state-types", map[string]any{"types": []string{"new", "open", "asleep"}}); got.Status != http.StatusBadRequest {
		t.Errorf("a type there is not was answered %d, want 400", got.Status)
	}

	if got := worker.api(http.MethodGet, "/unlock-state-types", nil); got.Status != http.StatusForbidden {
		t.Errorf("an agent reading the state types was answered %d, want 403", got.Status)
	}

	var log adminChanges

	admin.must(admin.api(http.MethodGet, "/admin/changes", nil), http.StatusOK).Data(t, &log)

	if len(log.Entries) == 0 || log.Entries[0].Action != "unlockStateTypes.changed" {
		t.Errorf("the administration log reads %+v; want the change", log.Entries)
	}
}
