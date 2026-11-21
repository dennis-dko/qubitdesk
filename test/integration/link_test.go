//go:build integration

package integration

import (
	"net/http"
	"testing"
)

type linkedRecord struct {
	ID       uint   `json:"id"`
	Type     string `json:"type"`
	Outward  bool   `json:"outward"`
	Object   string `json:"object"`
	ObjectID uint   `json:"objectId"`
	Label    string `json:"label"`
}

// A ticket and an item are linked by an agent who works the ticket and reads
// the item, and the link is read from either end, on every database. The
// rights asked are the ends': linking from an item, or undoing a link through
// one, takes assets:write. A deleted ticket or item takes its links with it.
func TestRecordsAreLinkedOverHTTP(t *testing.T) {
	t.Parallel()

	_, admin, agent := startWithWorker(t)
	desk := theDesk(t, admin)

	var ticket ticketResponse

	admin.must(admin.api(http.MethodPost, "/tickets", map[string]any{
		"title": "Printer", "queueId": desk, "body": "It jams.",
	}), http.StatusCreated).Data(t, &ticket)

	item := func(name string) uint {
		var classes struct {
			Items []ciClass `json:"items"`
		}

		admin.must(admin.api(http.MethodGet, "/cmdb/classes", nil), http.StatusOK).Data(t, &classes)

		var place ciClass

		for _, class := range classes.Items {
			if class.Name == "location" {
				place = class
			}
		}

		var definitions struct {
			Items []classDefinition `json:"items"`
		}

		admin.must(admin.api(http.MethodGet, path("/cmdb/classes/", place.ID)+"/definitions", nil),
			http.StatusOK).Data(t, &definitions)

		listed := func(class string) uint {
			var values struct {
				Items []catalogItem `json:"items"`
			}

			admin.must(admin.api(http.MethodGet, "/general-catalog?class="+class, nil), http.StatusOK).Data(t, &values)

			return values.Items[0].ID
		}

		var made configItem

		admin.must(admin.api(http.MethodPost, "/cmdb/items", map[string]any{
			"classId": place.ID, "name": name, "deploymentStateId": listed("ITSM::ConfigItem::DeploymentState"),
			"incidentStateId": listed("ITSM::Core::IncidentState"),
			"data":            leastData(t, definitions.Items[0].Attributes, listed),
		}), http.StatusCreated, http.StatusOK).Data(t, &made)

		return made.ID
	}

	office, depot := item("Head office"), item("Depot")

	var link linkedRecord

	agent.must(agent.api(http.MethodPost, "/links", map[string]any{
		"sourceObject": "ticket", "sourceId": ticket.ID, "targetObject": "configitem", "targetId": office,
		"type": "Normal",
	}), http.StatusCreated, http.StatusOK).Data(t, &link)

	if link.ObjectID != office || !link.Outward || link.Label == "" {
		t.Errorf("the link was answered as %+v", link)
	}

	read := func(object string, record uint) []linkedRecord {
		var links struct {
			Items []linkedRecord `json:"items"`
		}

		agent.must(agent.api(http.MethodGet, "/links?object="+object+"&record="+path(record), nil),
			http.StatusOK).Data(t, &links)

		return links.Items
	}

	if from := read("configitem", office); len(from) != 1 || from[0].ObjectID != ticket.ID || from[0].Outward {
		t.Errorf("from the office the agent reads %+v, want the ticket, inward", from)
	}

	if refused := agent.api(http.MethodPost, "/links", map[string]any{
		"sourceObject": "configitem", "sourceId": office, "targetObject": "configitem", "targetId": depot,
		"type": "Normal",
	}); refused.Status != http.StatusForbidden {
		t.Errorf("an agent linking from an item was answered %d, want 403", refused.Status)
	}

	if refused := agent.api(http.MethodDelete, path("/links/", link.ID)+"?object=configitem&record="+path(office),
		nil); refused.Status != http.StatusForbidden {
		t.Errorf("an agent undoing a link through an item was answered %d, want 403", refused.Status)
	}

	admin.must(admin.api(http.MethodPost, "/links", map[string]any{
		"sourceObject": "configitem", "sourceId": office, "targetObject": "configitem", "targetId": depot,
		"type": "ParentChild",
	}), http.StatusCreated, http.StatusOK)

	admin.must(admin.api(http.MethodDelete, path("/tickets/", ticket.ID), nil), http.StatusNoContent, http.StatusOK)

	if left := read("configitem", office); len(left) != 1 || left[0].Object != "configitem" {
		t.Errorf("after the ticket was deleted the office reads %+v, want only the depot", left)
	}

	admin.must(admin.api(http.MethodDelete, path("/cmdb/items/", depot), nil), http.StatusNoContent, http.StatusOK)

	if left := read("configitem", office); len(left) != 0 {
		t.Errorf("after the depot was deleted the office reads %+v, want nothing", left)
	}
}
