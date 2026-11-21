//go:build integration

package integration

import (
	"net/http"
	"testing"
)

type catalogItem struct {
	ID            uint   `json:"id"`
	Class         string `json:"class"`
	Name          string `json:"name"`
	Functionality string `json:"functionality"`
	Valid         bool   `json:"valid"`
}

// The general catalog ships KIX18's, on every database - the deployment states
// with the functionality KIX18 gives each, the incident states, and the lists
// the classes' details pick from - and is read by anybody signed in and changed
// by whoever holds catalog:write alone: a value added in a class of its own, a
// name a class holds refused a second time, a value retired rather than
// deleted, and every change in the administration log.
func TestTheGeneralCatalogIsKeptOverHTTP(t *testing.T) {
	t.Parallel()

	_, admin, agent := startWithWorker(t)

	var states struct {
		Items []catalogItem `json:"items"`
	}

	agent.must(agent.api(http.MethodGet, "/general-catalog?class=ITSM::ConfigItem::DeploymentState", nil),
		http.StatusOK).Data(t, &states)

	functionality := map[string]string{}
	for _, state := range states.Items {
		functionality[state.Name] = state.Functionality
	}

	if len(states.Items) != 10 || functionality["planned"] != "preproductive" ||
		functionality["production"] != "productive" || functionality["retired"] != "postproductive" {
		t.Errorf("the deployment states shipped are %+v; want KIX18's ten with their functionality", states.Items)
	}

	var classes struct {
		Items []struct {
			Name            string   `json:"name"`
			Functionalities []string `json:"functionalities"`
		} `json:"items"`
	}

	agent.must(agent.api(http.MethodGet, "/general-catalog/classes", nil), http.StatusOK).Data(t, &classes)

	if len(classes.Items) != 16 {
		t.Errorf("the catalog ships the classes %+v; want ten", classes.Items)
	}

	brand := map[string]any{"class": "ITSM::ConfigItem::Laptop::Brand", "name": "Acme"}

	if refused := agent.api(http.MethodPost, "/general-catalog", brand); refused.Status != http.StatusForbidden {
		t.Errorf("an agent adding to the catalog was answered %d, want 403", refused.Status)
	}

	var acme catalogItem

	admin.must(admin.api(http.MethodPost, "/general-catalog", brand), http.StatusCreated, http.StatusOK).Data(t, &acme)

	if twice := admin.api(http.MethodPost, "/general-catalog", brand); twice.Status != http.StatusConflict ||
		refusalOf(t, twice).Code != "catalogItemTaken" {
		t.Errorf("a name its class holds was answered %d: %s", twice.Status, twice.Body)
	}

	admin.must(admin.api(http.MethodPut, path("/general-catalog/", acme.ID), map[string]any{"valid": false}),
		http.StatusOK).Data(t, &acme)

	if acme.Valid {
		t.Error("the value is still offered after it was retired")
	}

	if moved := admin.api(http.MethodPut, path("/general-catalog/", acme.ID),
		map[string]any{"class": "ITSM::ConfigItem::YesNo"}); moved.Status != http.StatusBadRequest {
		t.Errorf("moving a value to another class was answered %d, want 400", moved.Status)
	}

	var log adminChanges

	admin.must(admin.api(http.MethodGet, "/admin/changes", nil), http.StatusOK).Data(t, &log)

	recorded := map[string]bool{}
	for _, entry := range log.Entries {
		if entry.Detail == "Acme" {
			recorded[entry.Action] = true
		}
	}

	if !recorded["catalog.created"] {
		t.Errorf("the administration log does not keep the value added: %+v", log.Entries)
	}
}
