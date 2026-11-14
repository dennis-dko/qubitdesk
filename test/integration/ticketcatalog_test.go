//go:build integration

package integration

import (
	"net/http"
	"slices"
	"strconv"
	"testing"
)

type catalogEntry struct {
	ID       uint   `json:"id"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	Level    int    `json:"level"`
	Valid    bool   `json:"valid"`
	IsSystem bool   `json:"isSystem"`
}

type catalogList struct {
	Items []catalogEntry `json:"items"`
}

// A fresh installation sorts its tickets by what KIX18 ships: seven states, one
// of each type, five priorities with the most urgent first, and three types -
// and only what the code moves tickets into by itself is marked as its own.
func TestTheTicketCatalogShipsWhatKIX18Ships(t *testing.T) {
	t.Parallel()

	a := start(t)
	admin := a.signInAsAdmin("a-much-better-password")

	read := func(path string) []catalogEntry {
		var list catalogList

		admin.must(admin.api(http.MethodGet, path, nil), http.StatusOK).Data(t, &list)

		return list.Items
	}

	var states, systemStates []string

	for _, state := range read("/ticket-states") {
		states = append(states, state.Name+"/"+state.Type)

		if state.IsSystem {
			systemStates = append(systemStates, state.Name)
		}
	}

	if want := []string{
		"new/new", "open/open", "pending-reminder/pending-reminder", "pending-auto-close/pending-auto",
		"closed/closed", "removed/removed", "merged/merged",
	}; !slices.Equal(states, want) {
		t.Errorf("the shipped states are %v, want %v", states, want)
	}

	if want := []string{"new", "open", "closed", "merged"}; !slices.Equal(systemStates, want) {
		t.Errorf("the states marked as the code's own are %v, want %v", systemStates, want)
	}

	var priorities []string

	for _, priority := range read("/ticket-priorities") {
		priorities = append(priorities, strconv.Itoa(priority.Level)+" "+priority.Name+
			map[bool]string{true: " (system)"}[priority.IsSystem])
	}

	if want := []string{"1 very-high", "2 high", "3 normal (system)", "4 low", "5 very-low"}; !slices.Equal(priorities, want) {
		t.Errorf("the shipped priorities are %v, want %v", priorities, want)
	}

	var types []string

	for _, kind := range read("/ticket-types") {
		types = append(types, kind.Name+map[bool]string{true: " (system)"}[kind.IsSystem])
	}

	if want := []string{"unclassified (system)", "incident", "service-request"}; !slices.Equal(types, want) {
		t.Errorf("the shipped types are %v, want %v", types, want)
	}
}

// Somebody who works tickets reads the lists, because putting a ticket into a
// state needs them, and changes nothing about them: that takes queues:write.
func TestAnAgentReadsTheTicketCatalogAndChangesNothing(t *testing.T) {
	t.Parallel()

	a := start(t)
	admin := a.signInAsAdmin("a-much-better-password")
	agent := a.signInAsAgent(admin, "Agent", "agent@example.com")

	for _, path := range []string{"/ticket-states", "/ticket-priorities", "/ticket-types"} {
		agent.must(agent.api(http.MethodGet, path, nil), http.StatusOK)
	}

	refused := agent.api(http.MethodPost, "/ticket-types", map[string]any{"name": "mine"})
	if refused.Status != http.StatusForbidden || errorCode(t, refused) != "missingPermission" {
		t.Errorf("an agent creating a type was answered %d %s, want 403 missingPermission",
			refused.Status, refused.Body)
	}
}

// What an installation adds is its own to change and delete, and the change is in
// the administration log; what it shipped with that the code relies on is not.
func TestTheTicketCatalogIsAdministeredAndLogged(t *testing.T) {
	t.Parallel()

	a := start(t)
	admin := a.signInAsAdmin("a-much-better-password")

	var made catalogEntry

	admin.must(admin.api(http.MethodPost, "/ticket-states", map[string]any{
		"name": "waiting for the customer", "type": "pending-reminder",
	}), http.StatusCreated).Data(t, &made)

	if !made.Valid || made.IsSystem || made.Type != "pending-reminder" {
		t.Errorf("a new state came back as %+v", made)
	}

	path := "/ticket-states/" + strconv.FormatUint(uint64(made.ID), 10)

	var retired catalogEntry

	admin.must(admin.api(http.MethodPut, path, map[string]any{
		"name": "waiting for the customer", "valid": false,
	}), http.StatusOK).Data(t, &retired)

	if retired.Valid || retired.Type != "pending-reminder" {
		t.Errorf("retiring the state left %+v", retired)
	}

	unknown := admin.api(http.MethodPost, "/ticket-states", map[string]any{"name": "x", "type": "waiting"})
	if unknown.Status != http.StatusBadRequest {
		t.Errorf("a state of an unknown type was answered %d, want 400", unknown.Status)
	}

	var states catalogList

	admin.must(admin.api(http.MethodGet, "/ticket-states", nil), http.StatusOK).Data(t, &states)

	for _, state := range states.Items {
		if state.Name != "closed" {
			continue
		}

		closed := "/ticket-states/" + strconv.FormatUint(uint64(state.ID), 10)

		for _, refused := range []response{
			admin.api(http.MethodPut, closed, map[string]any{"description": "mine now"}),
			admin.api(http.MethodDelete, closed, nil),
		} {
			if refused.Status != http.StatusConflict || errorCode(t, refused) != "stateShipped" {
				t.Errorf("changing the shipped closed state was answered %d %s, want 409 stateShipped",
					refused.Status, refused.Body)
			}
		}
	}

	var log adminChanges

	admin.must(admin.api(http.MethodGet, "/admin/changes", nil), http.StatusOK).Data(t, &log)

	logged := false

	for _, entry := range log.Entries {
		logged = logged || entry.Action == "state.created"
	}

	if !logged {
		t.Error("creating a state is not in the administration log")
	}

	admin.must(admin.api(http.MethodDelete, path, nil), http.StatusNoContent)

	if gone := admin.api(http.MethodGet, path, nil); gone.Status != http.StatusNotFound {
		t.Errorf("the deleted state was answered %d, want 404", gone.Status)
	}
}
