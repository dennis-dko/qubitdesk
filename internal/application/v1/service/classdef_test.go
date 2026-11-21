package service_test

import (
	"context"
	"testing"

	"github.com/dennis-dko/go-ticket-system/internal/application/v1/service"
	"github.com/dennis-dko/go-ticket-system/internal/domain/model"
	"github.com/dennis-dko/go-ticket-system/internal/infrastructure/persistence/memory"
	"github.com/dennis-dko/go-ticket-system/internal/support/apperror"
)

// classDefinitions is the service over a general catalog holding two classes
// of configuration items and one list to pick from.
func classDefinitions(t *testing.T) (*service.ClassDefinitionApplicationService, uint, uint) {
	t.Helper()

	ctx := context.Background()
	catalog := memory.NewGeneralCatalogRepository()

	add := func(class, name string) uint {
		item, err := catalog.Save(ctx, &model.CatalogItem{Class: class, Name: name, Valid: true})
		if err != nil {
			t.Fatalf("seeding %s %s: %v", class, name, err)
		}

		return item.ID
	}

	computer := add(model.CatalogCIClass, "computer")
	add(model.CatalogCIClass, "location")
	add("ITSM::ConfigItem::Computer::Type", "laptop")
	yes := add("ITSM::ConfigItem::YesNo", "yes")

	return service.NewClassDefinitionApplicationService(catalog, memory.NewClassDefinitionRepository()), computer, yes
}

// attribute is a text attribute occurring once, which every check passes.
func attribute(key string) model.ClassAttribute {
	return model.ClassAttribute{Key: key, Name: key, Type: model.AttributeText, CountMin: 1, CountMax: 1,
		CountDefault: 1, MaxLength: 50}
}

// A definition is kept as its class's next version, numbered on from the last,
// by the account that wrote it - and one saying nothing the standing version
// does not is refused rather than kept as a version nobody can tell apart.
func TestAClassDefinitionIsKeptAsTheNextVersion(t *testing.T) {
	ctx := context.Background()
	definitions, computer, _ := classDefinitions(t)

	first, err := definitions.Add(ctx, 7, computer, []model.ClassAttribute{attribute("Vendor")})
	if err != nil || first.Version != 1 || first.CreatedBy != 7 {
		t.Fatalf("the first definition was kept as %+v (%v), want version 1 by account 7", first, err)
	}

	second, err := definitions.Add(ctx, 7, computer, []model.ClassAttribute{attribute("Vendor"), attribute("Model")})
	if err != nil || second.Version != 2 {
		t.Fatalf("the second definition was kept as %+v (%v), want version 2", second, err)
	}

	_, err = definitions.Add(ctx, 7, computer, []model.ClassAttribute{attribute("Vendor"), attribute("Model")})
	if detail, ok := apperror.Detail(err); !ok || detail.Code != "classDefinitionUnchanged" {
		t.Errorf("a definition saying what version 2 says answered %v, want classDefinitionUnchanged", err)
	}

	versions, err := definitions.Versions(ctx, computer)
	if err != nil || len(versions) != 2 || versions[0].Version != 2 {
		t.Errorf("the versions read as %+v (%v), want two, the newest first", versions, err)
	}

	classes, err := definitions.Classes(ctx)
	if err != nil || len(classes) != 2 {
		t.Fatalf("the classes read as %+v (%v), want both", classes, err)
	}

	for _, class := range classes {
		want := 0
		if class.Class.ID == computer {
			want = 2
		}

		if class.Version != want {
			t.Errorf("%s stands at version %d, want %d", class.Class.Name, class.Version, want)
		}
	}
}

// A value of another list of the general catalog is no class, and has no
// definition to read or write.
func TestOnlyAClassHasADefinition(t *testing.T) {
	ctx := context.Background()
	definitions, _, yes := classDefinitions(t)

	if _, err := definitions.Add(ctx, 1, yes, []model.ClassAttribute{attribute("Vendor")}); apperror.KindOf(err) !=
		apperror.KindNotFound {
		t.Errorf("writing a definition for a value of another list answered %v, want not found", err)
	}

	if _, err := definitions.Versions(ctx, yes); apperror.KindOf(err) != apperror.KindNotFound {
		t.Errorf("reading the definitions of a value of another list answered %v, want not found", err)
	}
}

// A definition an item could not be written under is refused whole, naming the
// attribute at fault by the path of its keys.
func TestAClassDefinitionAnItemCannotBeWrittenUnderIsRefused(t *testing.T) {
	ctx := context.Background()
	definitions, computer, _ := classDefinitions(t)

	section := func(sub ...model.ClassAttribute) model.ClassAttribute {
		return model.ClassAttribute{Key: "SectionGeneral", Name: "General", Type: model.AttributeSection,
			CountMin: 1, CountMax: 1, CountDefault: 1, Sub: sub}
	}

	with := func(change func(*model.ClassAttribute)) model.ClassAttribute {
		a := attribute("Vendor")
		change(&a)

		return a
	}

	nest := func(levels int) model.ClassAttribute {
		a := attribute("Level")
		for range levels - 1 {
			outer := attribute("Level")
			outer.Sub = []model.ClassAttribute{a}
			a = outer
		}

		return a
	}

	many := make([]model.ClassAttribute, 0, model.MaxClassAttributes+1)
	for i := range model.MaxClassAttributes + 1 {
		many = append(many, attribute("Attribute"+string(rune('A'+i%26))+string(rune('A'+i/26%26))))
	}

	for code, attributes := range map[string][]model.ClassAttribute{
		"classDefinitionEmpty":    nil,
		"attributeKeyInvalid":     {with(func(a *model.ClassAttribute) { a.Key = "2nd" })},
		"attributeKeyTwice":       {attribute("Vendor"), attribute("Vendor")},
		"attributeNameInvalid":    {with(func(a *model.ClassAttribute) { a.Name = " " })},
		"attributeTypeUnknown":    {with(func(a *model.ClassAttribute) { a.Type = "Attachment" })},
		"attributeCountInvalid":   {with(func(a *model.ClassAttribute) { a.CountDefault = 2 })},
		"attributeSectionInvalid": {section()},
		"attributeLengthInvalid":  {with(func(a *model.ClassAttribute) { a.MaxLength = model.MaxAttributeTextLen + 1 })},
		"attributeCatalogUnknown": {with(func(a *model.ClassAttribute) {
			a.Type, a.MaxLength, a.Catalog = model.AttributeGeneralCatalog, 0, "ITSM::ConfigItem::Nothing"
		})},
		"attributeClassUnknown": {section(with(func(a *model.ClassAttribute) {
			a.Type, a.MaxLength, a.Classes = model.AttributeCIClassReference, 0, []string{"spaceship"}
		}))},
		"attributeTooDeep":        {nest(model.MaxAttributeDepth + 1)},
		"classDefinitionTooLarge": many,
	} {
		_, err := definitions.Add(ctx, 1, computer, attributes)
		if detail, ok := apperror.Detail(err); !ok || detail.Code != code {
			t.Errorf("a definition that should be refused with %s answered %v", code, err)
		}
	}

	// And the definitions that pass: a list of the catalog, a class to point at,
	// and a section around them.
	if _, err := definitions.Add(ctx, 1, computer, []model.ClassAttribute{section(
		with(func(a *model.ClassAttribute) {
			a.Key, a.Type, a.MaxLength, a.Catalog = "Type", model.AttributeGeneralCatalog, 0,
				"ITSM::ConfigItem::Computer::Type"
		}),
		with(func(a *model.ClassAttribute) {
			a.Key, a.Type, a.MaxLength, a.Classes = "Location", model.AttributeCIClassReference, 0, []string{"location"}
		}),
	)}); err != nil {
		t.Errorf("a definition every check passes was refused: %v", err)
	}
}
