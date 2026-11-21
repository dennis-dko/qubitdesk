//go:build integration

package integration

import (
	"net/http"
	"slices"
	"testing"
)

type ciClass struct {
	ID      uint   `json:"id"`
	Name    string `json:"name"`
	Version int    `json:"version"`
}

// classAttribute is an attribute as the API writes it, every field of it: a
// definition read and sent back has to say exactly what it said, or it is a
// new version.
type classAttribute struct {
	Key        string           `json:"key"`
	Name       string           `json:"name"`
	Type       string           `json:"type"`
	CountMin   int              `json:"countMin"`
	CountMax   int              `json:"countMax"`
	Default    int              `json:"countDefault"`
	Required   bool             `json:"required,omitempty"`
	Searchable bool             `json:"searchable,omitempty"`
	MaxLength  int              `json:"maxLength,omitempty"`
	Catalog    string           `json:"catalog,omitempty"`
	Classes    []string         `json:"classes,omitempty"`
	Sub        []classAttribute `json:"sub,omitempty"`
}

type classDefinition struct {
	Version    int              `json:"version"`
	Attributes []classAttribute `json:"attributes"`
	CreatedBy  uint             `json:"createdBy"`
}

// Every shipped class comes with KIX18's definition as its first version, on
// every database, and is read by anybody signed in; a change is written by
// whoever holds catalog:write alone, kept as the next version beside the ones
// before it, refused whole where no item could be written under it or where it
// says nothing new, and recorded in the administration log under the class's
// name.
func TestClassDefinitionsAreKeptInVersionsOverHTTP(t *testing.T) {
	t.Parallel()

	_, admin, agent := startWithWorker(t)

	var classes struct {
		Items []ciClass `json:"items"`
	}

	agent.must(agent.api(http.MethodGet, "/cmdb/classes", nil), http.StatusOK).Data(t, &classes)

	var computer ciClass

	for _, class := range classes.Items {
		if class.Version != 1 {
			t.Errorf("the shipped class %s stands at version %d, want KIX18's as version 1", class.Name, class.Version)
		}

		if class.Name == "computer" {
			computer = class
		}
	}

	if len(classes.Items) != 7 || computer.ID == 0 {
		t.Fatalf("the classes shipped are %+v; want KIX18's seven, the computer among them", classes.Items)
	}

	var versions struct {
		Items []classDefinition `json:"items"`
	}

	// Every shipped definition is one the checks pass: sent again as it is, it
	// is refused only for saying nothing new.
	for _, class := range classes.Items {
		route := path("/cmdb/classes/", class.ID) + "/definitions"

		// Read into a fresh value each time: decoding into the last one keeps
		// whatever this answer leaves out, which is every empty field.
		var shipped struct {
			Items []classDefinition `json:"items"`
		}

		admin.must(admin.api(http.MethodGet, route, nil), http.StatusOK).Data(t, &shipped)

		again := admin.api(http.MethodPost, route, map[string]any{"attributes": shipped.Items[0].Attributes})
		if again.Status != http.StatusBadRequest || refusalOf(t, again).Code != "classDefinitionUnchanged" {
			t.Errorf("the shipped %s definition sent again was answered %d: %s", class.Name, again.Status, again.Body)
		}
	}

	agent.must(agent.api(http.MethodGet, path("/cmdb/classes/", computer.ID)+"/definitions", nil),
		http.StatusOK).Data(t, &versions)

	if len(versions.Items) != 1 || len(versions.Items[0].Attributes) == 0 ||
		versions.Items[0].Attributes[0].Key != "SectionGeneral" || versions.Items[0].CreatedBy != 0 {
		t.Fatalf("the computer's definition reads %+v; want KIX18's, shipped, its general section first", versions.Items)
	}

	shipped := versions.Items[0].Attributes
	changed := append(slices.Clone(shipped), classAttribute{Key: "AssetTag", Name: "Asset tag", Type: "Text",
		CountMin: 0, CountMax: 1, Default: 1})
	route := path("/cmdb/classes/", computer.ID) + "/definitions"

	if refused := agent.api(http.MethodPost, route, map[string]any{"attributes": changed}); refused.Status !=
		http.StatusForbidden {
		t.Errorf("an agent writing a definition was answered %d, want 403", refused.Status)
	}

	var second classDefinition

	admin.must(admin.api(http.MethodPost, route, map[string]any{"attributes": changed}),
		http.StatusCreated, http.StatusOK).Data(t, &second)

	if second.Version != 2 || len(second.Attributes) != len(shipped)+1 {
		t.Errorf("the changed definition was kept as %+v; want version 2 with the asset tag", second)
	}

	if again := admin.api(http.MethodPost, route, map[string]any{"attributes": changed}); again.Status !=
		http.StatusBadRequest || refusalOf(t, again).Code != "classDefinitionUnchanged" {
		t.Errorf("the same definition again was answered %d: %s", again.Status, again.Body)
	}

	broken := append(slices.Clone(shipped), classAttribute{Key: "Owner", Name: "Owner", Type: "CIClassReference",
		CountMin: 0, CountMax: 1, Default: 1, Classes: []string{"spaceship"}})

	if refused := admin.api(http.MethodPost, route, map[string]any{"attributes": broken}); refused.Status !=
		http.StatusBadRequest || refusalOf(t, refused).Code != "attributeClassUnknown" {
		t.Errorf("a reference to a class there is not was answered %d: %s", refused.Status, refused.Body)
	}

	var after struct {
		Items []classDefinition `json:"items"`
	}

	agent.must(agent.api(http.MethodGet, route, nil), http.StatusOK).Data(t, &after)

	versions = after

	if len(versions.Items) != 2 || versions.Items[0].Version != 2 || versions.Items[1].Version != 1 {
		t.Errorf("the versions read %+v; want 2 and the shipped 1 beside it, the newest first", versions.Items)
	}

	var page struct {
		Entries []struct {
			Action string `json:"action"`
			Target *struct {
				Name string `json:"name"`
			} `json:"target"`
		} `json:"entries"`
	}

	admin.must(admin.api(http.MethodGet, "/admin/changes", nil), http.StatusOK).Data(t, &page)

	if len(page.Entries) == 0 || page.Entries[0].Action != "classDefinition.created" ||
		page.Entries[0].Target == nil || page.Entries[0].Target.Name != "computer" {
		t.Errorf("the administration log's newest entry is %+v; want the computer's new definition", page.Entries)
	}
}
