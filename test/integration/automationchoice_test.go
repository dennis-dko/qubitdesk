//go:build integration

package integration

import (
	"net/http"
	"testing"
)

// Whoever may set the automation up reads what a step and a job's criteria can
// name through the automation's own choices, on every database, without any
// right to the tickets, the accounts or the customers themselves - and nobody
// without that right reads them.
func TestTheAutomationsChoicesTakeTheRightToSetItUpAlone(t *testing.T) {
	t.Parallel()

	a, admin, agent := startWithWorker(t)

	admin.must(admin.api(http.MethodPost, "/roles", map[string]any{"name": "automator",
		"permissions": []string{"automation:write"}}), http.StatusCreated, http.StatusOK)
	admin.must(admin.api(http.MethodPost, "/users", map[string]any{"name": "Otto", "email": "otto@example.com",
		"role": "automator", "password": "otto-password-1"}), http.StatusCreated, http.StatusOK)
	admin.must(admin.api(http.MethodPost, "/organisations", map[string]any{"name": "Acme"}),
		http.StatusCreated, http.StatusOK)
	admin.must(admin.api(http.MethodPost, "/contacts", map[string]any{"name": "Ann Archer"}),
		http.StatusCreated, http.StatusOK)

	otto := a.newClient()
	otto.signIn("otto@example.com", "otto-password-1")

	type named struct {
		Name string `json:"name"`
	}

	var choices struct {
		Queues   []named `json:"queues"`
		States   []named `json:"states"`
		Accounts []named `json:"accounts"`
	}

	otto.must(otto.api(http.MethodGet, "/automation/choices", nil), http.StatusOK).Data(t, &choices)

	has := func(list []named, name string) bool {
		for _, entry := range list {
			if entry.Name == name {
				return true
			}
		}

		return false
	}

	if !has(choices.Queues, "Service Desk") || !has(choices.States, "open") || !has(choices.Accounts, "Wera") ||
		has(choices.Accounts, "Otto") {
		t.Errorf("the automator is offered %+v; want the desk, the open state and the agent, and not itself, "+
			"which may not work on tickets", choices)
	}

	for path, want := range map[string]string{
		"/automation/contacts?q=ann":       "Ann Archer",
		"/automation/organisations?q=acme": "Acme",
	} {
		var found listOf[named]

		otto.must(otto.api(http.MethodGet, path, nil), http.StatusOK).Data(t, &found)

		if len(found.Items) != 1 || found.Items[0].Name != want {
			t.Errorf("%s found %+v, want %s alone", path, found.Items, want)
		}
	}

	if refused := otto.api(http.MethodGet, "/contacts", nil); refused.Status != http.StatusForbidden {
		t.Errorf("the automator read the customers themselves with %d, want 403", refused.Status)
	}

	if refused := agent.api(http.MethodGet, "/automation/choices", nil); refused.Status != http.StatusForbidden {
		t.Errorf("an agent read the automation's choices with %d, want 403", refused.Status)
	}
}
