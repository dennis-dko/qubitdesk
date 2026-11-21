//go:build integration

package integration

import (
	"net/http"
	"strconv"
	"testing"
)

// attributeValue is one occurrence of an attribute as the API writes it.
type attributeValue struct {
	Value string                      `json:"value,omitempty"`
	Sub   map[string][]attributeValue `json:"sub,omitempty"`
}

type configItem struct {
	ID                uint   `json:"id"`
	Number            string `json:"number"`
	ClassID           uint   `json:"classId"`
	Name              string `json:"name"`
	DeploymentStateID uint   `json:"deploymentStateId"`
	IncidentStateID   uint   `json:"incidentStateId"`
	VersionCount      int    `json:"versionCount"`
	CreatedBy         uint   `json:"createdBy"`
	ChangedBy         uint   `json:"changedBy"`
	Version           struct {
		ID           uint                        `json:"id"`
		DefinitionID uint                        `json:"definitionId"`
		Name         string                      `json:"name"`
		Data         map[string][]attributeValue `json:"data"`
	} `json:"version"`
}

// leastData is what an item of a definition holds at the least: every
// attribute as often as it has to occur, empty where it may be and otherwise
// with a value of its type. A required reference, contact or organisation that
// has to occur is beyond it, and no shipped definition has one.
func leastData(t *testing.T, attributes []classAttribute, listed func(class string) uint) map[string][]attributeValue {
	t.Helper()

	data := map[string][]attributeValue{}

	for _, attribute := range attributes {
		for range attribute.CountMin {
			var occurrence attributeValue

			if attribute.Required {
				switch attribute.Type {
				case "Text", "TextArea":
					occurrence.Value = "kept"
				case "Date":
					occurrence.Value = "2026-11-21"
				case "DateTime":
					occurrence.Value = "2026-11-21T10:00:00Z"
				case "GeneralCatalog":
					occurrence.Value = strconv.FormatUint(uint64(listed(attribute.Catalog)), 10)
				default:
					t.Fatalf("%s is a required %s that has to occur, which leastData cannot fill", attribute.Key,
						attribute.Type)
				}
			}

			if len(attribute.Sub) > 0 {
				occurrence.Sub = leastData(t, attribute.Sub, listed)
			}

			data[attribute.Key] = append(data[attribute.Key], occurrence)
		}
	}

	return data
}

// Every shipped class takes an item under the definition it ships with, on
// every database. An item is read with assets:read and written with
// assets:write alone; a change is its next version beside the ones before it,
// refused whole where the definition does not allow it or where it says nothing
// new; a value retired since stays where the item names it and is chosen by no
// new item; and what an account wrote is in its export.
func TestConfigItemsAreKeptInVersionsOverHTTP(t *testing.T) {
	t.Parallel()

	_, admin, agent := startWithWorker(t)

	listed := func(class string) uint {
		var values struct {
			Items []catalogItem `json:"items"`
		}

		agent.must(agent.api(http.MethodGet, "/general-catalog?class="+class, nil), http.StatusOK).Data(t, &values)

		for _, value := range values.Items {
			if value.Valid {
				return value.ID
			}
		}

		t.Fatalf("the catalog offers no value of %s", class)

		return 0
	}

	var classes struct {
		Items []ciClass `json:"items"`
	}

	agent.must(agent.api(http.MethodGet, "/cmdb/classes", nil), http.StatusOK).Data(t, &classes)

	planned := map[string]any{"class": "ITSM::ConfigItem::DeploymentState", "name": "Ordered",
		"functionality": "preproductive"}

	var ordered catalogItem

	admin.must(admin.api(http.MethodPost, "/general-catalog", planned), http.StatusCreated, http.StatusOK).
		Data(t, &ordered)

	operational := listed("ITSM::Core::IncidentState")
	made := map[string]configItem{}

	for _, class := range classes.Items {
		var definitions struct {
			Items []classDefinition `json:"items"`
		}

		agent.must(agent.api(http.MethodGet, path("/cmdb/classes/", class.ID)+"/definitions", nil),
			http.StatusOK).Data(t, &definitions)

		item := map[string]any{"classId": class.ID, "name": "The first " + class.Name,
			"deploymentStateId": ordered.ID, "incidentStateId": operational,
			"data": leastData(t, definitions.Items[0].Attributes, listed)}

		if refused := agent.api(http.MethodPost, "/cmdb/items", item); refused.Status != http.StatusForbidden {
			t.Fatalf("an agent making an item was answered %d, want 403", refused.Status)
		}

		var first configItem

		answer := admin.api(http.MethodPost, "/cmdb/items", item)
		if answer.Status != http.StatusCreated && answer.Status != http.StatusOK {
			t.Fatalf("the least item of the shipped %s definition was answered %d: %s", class.Name, answer.Status,
				answer.Body)
		}

		answer.Data(t, &first)

		if first.Number == "" || first.VersionCount != 1 || first.Version.Name != "The first "+class.Name {
			t.Errorf("the first %s was made as %+v", class.Name, first)
		}

		made[class.Name] = first
	}

	computer := made["computer"]
	if computer.ID == 0 {
		t.Fatalf("no computer was made among %+v", classes.Items)
	}

	route := path("/cmdb/items/", computer.ID)
	data := computer.Version.Data
	data["SectionGeneral"][0].Sub["Vendor"] = []attributeValue{{Value: "Acme"}}
	next := map[string]any{"name": "Erika's laptop", "deploymentStateId": ordered.ID,
		"incidentStateId": operational, "data": data}

	if refused := agent.api(http.MethodPost, route+"/versions", next); refused.Status != http.StatusForbidden {
		t.Errorf("an agent writing a version was answered %d, want 403", refused.Status)
	}

	var second configItem

	admin.must(admin.api(http.MethodPost, route+"/versions", next), http.StatusCreated, http.StatusOK).Data(t, &second)

	if second.VersionCount != 2 || second.Name != "Erika's laptop" ||
		second.Version.Data["SectionGeneral"][0].Sub["Vendor"][0].Value != "Acme" {
		t.Errorf("the computer's second version reads %+v", second)
	}

	if again := admin.api(http.MethodPost, route+"/versions", next); again.Status != http.StatusBadRequest ||
		refusalOf(t, again).Code != "configItemUnchanged" {
		t.Errorf("the same version again was answered %d: %s", again.Status, again.Body)
	}

	for code, change := range map[string]func(map[string][]attributeValue){
		"configItemAttributeUnknown": func(d map[string][]attributeValue) {
			d["Colour"] = []attributeValue{{Value: "red"}}
		},
		"configItemCountInvalid": func(d map[string][]attributeValue) { delete(d, "SectionGeneral") },
		"configItemValueInvalid": func(d map[string][]attributeValue) {
			d["SectionWarranty"][0].Sub["FirstUsageDate"] = []attributeValue{{Value: "21.11.2026"}}
		},
	} {
		var broken configItem

		admin.must(admin.api(http.MethodGet, route, nil), http.StatusOK).Data(t, &broken)
		change(broken.Version.Data)

		refused := admin.api(http.MethodPost, route+"/versions", map[string]any{"name": "Broken",
			"deploymentStateId": ordered.ID, "incidentStateId": operational, "data": broken.Version.Data})
		if refused.Status != http.StatusBadRequest || refusalOf(t, refused).Code != code {
			t.Errorf("a version that should be refused with %s was answered %d: %s", code, refused.Status,
				refused.Body)
		}
	}

	admin.must(admin.api(http.MethodPut, path("/general-catalog/", ordered.ID), map[string]any{"valid": false}),
		http.StatusOK)

	data["SectionGeneral"][0].Sub["Vendor"] = []attributeValue{{Value: "Globex"}}
	next["data"] = data

	admin.must(admin.api(http.MethodPost, route+"/versions", next), http.StatusCreated, http.StatusOK)

	var definitions struct {
		Items []classDefinition `json:"items"`
	}

	agent.must(agent.api(http.MethodGet, path("/cmdb/classes/", computer.ClassID)+"/definitions", nil),
		http.StatusOK).Data(t, &definitions)

	another := map[string]any{"classId": computer.ClassID, "name": "Another computer",
		"deploymentStateId": ordered.ID, "incidentStateId": operational,
		"data": leastData(t, definitions.Items[0].Attributes, listed)}

	if refused := admin.api(http.MethodPost, "/cmdb/items", another); refused.Status != http.StatusConflict ||
		refusalOf(t, refused).Code != "catalogValueRetired" {
		t.Errorf("a new item in a state retired since was answered %d: %s", refused.Status, refused.Body)
	}

	var versions struct {
		Items []struct {
			Name          string `json:"name"`
			CreatedByName string `json:"createdByName"`
		} `json:"items"`
	}

	agent.must(agent.api(http.MethodGet, route+"/versions", nil), http.StatusOK).Data(t, &versions)

	if len(versions.Items) != 3 || versions.Items[2].Name != "The first computer" ||
		versions.Items[0].CreatedByName == "" {
		t.Errorf("the computer's versions read %+v; want three, the newest first, each saying who wrote it",
			versions.Items)
	}

	// A reference may point at items of several classes, so a listing takes them
	// at once.
	var two struct {
		TotalCount uint `json:"totalCount"`
	}

	agent.must(agent.api(http.MethodGet, "/cmdb/items?class="+
		strconv.FormatUint(uint64(computer.ClassID), 10)+","+strconv.FormatUint(uint64(made["location"].ClassID), 10),
		nil), http.StatusOK).Data(t, &two)

	if two.TotalCount != 2 {
		t.Errorf("the computers and the locations together are %d items, want the one of each", two.TotalCount)
	}

	var found struct {
		Items      []configItem `json:"items"`
		TotalCount uint         `json:"totalCount"`
	}

	agent.must(agent.api(http.MethodGet, "/cmdb/items?class="+strconv.FormatUint(uint64(computer.ClassID), 10)+
		"&q=erika", nil), http.StatusOK).Data(t, &found)

	if found.TotalCount != 1 || len(found.Items) != 1 || found.Items[0].ID != computer.ID {
		t.Errorf("a search for Erika's laptop found %+v", found)
	}

	parts, _ := exportOf(t, admin)
	if written := parts["config-item-versions-written"]; len(written) != len(classes.Items)+2 {
		t.Errorf("the administrator's export holds %d versions written, want %d", len(written),
			len(classes.Items)+2)
	}

	if refused := agent.api(http.MethodDelete, route, nil); refused.Status != http.StatusForbidden {
		t.Errorf("an agent deleting an item was answered %d, want 403", refused.Status)
	}

	admin.must(admin.api(http.MethodDelete, route, nil), http.StatusNoContent, http.StatusOK)

	if gone := agent.api(http.MethodGet, route, nil); gone.Status != http.StatusNotFound {
		t.Errorf("the deleted computer was answered %d, want 404", gone.Status)
	}
}
