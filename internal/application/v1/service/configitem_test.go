package service_test

import (
	"context"
	"strconv"
	"testing"

	"github.com/dennis-dko/qubitdesk/internal/application/v1/service"
	"github.com/dennis-dko/qubitdesk/internal/domain/model"
	"github.com/dennis-dko/qubitdesk/internal/infrastructure/persistence/memory"
	"github.com/dennis-dko/qubitdesk/internal/support/apperror"
)

// itemWorld is the service over a catalog holding two classes - a computer
// with a definition worth checking against, a location with a short one - the
// states, a list to pick from, and a contact.
type itemWorld struct {
	items                        *service.ConfigItemApplicationService
	catalog                      *memory.GeneralCatalogRepository
	contacts                     *memory.ContactRepository
	computer, location           uint
	planned, production          uint
	operational, laptop, desktop uint
	contact                      uint
}

func newItemWorld(t *testing.T) itemWorld {
	t.Helper()

	ctx := context.Background()
	catalog := memory.NewGeneralCatalogRepository()
	definitions := memory.NewClassDefinitionRepository()
	contacts := memory.NewContactRepository()

	add := func(class, name, functionality string) uint {
		item, err := catalog.Save(ctx, &model.CatalogItem{Class: class, Name: name, Functionality: functionality,
			Valid: true})
		if err != nil {
			t.Fatalf("seeding %s %s: %v", class, name, err)
		}

		return item.ID
	}

	world := itemWorld{
		catalog:     catalog,
		contacts:    contacts,
		computer:    add(model.CatalogCIClass, "computer", ""),
		location:    add(model.CatalogCIClass, "location", ""),
		planned:     add(model.CatalogDeploymentState, "planned", "preproductive"),
		production:  add(model.CatalogDeploymentState, "production", "productive"),
		operational: add(model.CatalogIncidentState, "operational", "operational"),
		laptop:      add("ITSM::ConfigItem::Computer::Type", "laptop", ""),
		desktop:     add("ITSM::ConfigItem::Computer::Type", "desktop", ""),
	}

	contact, err := contacts.Save(ctx, &model.Contact{Name: "Erika", Valid: true})
	if err != nil {
		t.Fatalf("seeding a contact: %v", err)
	}

	world.contact = contact.ID

	attr := func(key string, kind model.AttributeType, least, most, begin int) model.ClassAttribute {
		return model.ClassAttribute{Key: key, Name: key, Type: kind, CountMin: least, CountMax: most, CountDefault: begin}
	}

	kind := attr("Type", model.AttributeGeneralCatalog, 1, 1, 1)
	kind.Catalog = "ITSM::ConfigItem::Computer::Type"
	vendor := attr("Vendor", model.AttributeText, 0, 1, 1)
	vendor.MaxLength = 10
	place := attr("Location", model.AttributeCIClassReference, 0, 1, 0)
	place.Classes = []string{"location"}
	nic := attr("NIC", model.AttributeText, 0, 2, 0)
	nic.Required = true
	nic.Sub = []model.ClassAttribute{attr("IPAddress", model.AttributeText, 1, 3, 1)}

	for class, attributes := range map[uint][]model.ClassAttribute{
		world.computer: {kind, vendor, place, attr("Bought", model.AttributeDate, 0, 1, 0),
			attr("Owner", model.AttributeContact, 0, 1, 0), nic},
		world.location: {attr("Note", model.AttributeTextArea, 0, 1, 0)},
	} {
		if _, err := definitions.Add(ctx, &model.ClassDefinition{ClassID: class, Attributes: attributes}); err != nil {
			t.Fatalf("defining class %d: %v", class, err)
		}
	}

	world.items = service.NewConfigItemApplicationService(memory.NewConfigItemRepository(), service.ConfigItemSources{
		Definitions: definitions, Catalog: catalog, Contacts: contacts,
		Organisations: memory.NewOrganisationRepository(),
	})

	return world
}

// laptopData is the values of a laptop every check passes.
func (w itemWorld) laptopData() model.AttributeData {
	return model.AttributeData{
		"Type":   {{Value: strconv.FormatUint(uint64(w.laptop), 10)}},
		"Vendor": {{Value: "Acme"}},
		"Bought": {{Value: "2026-11-21"}},
		"Owner":  {{Value: strconv.FormatUint(uint64(w.contact), 10)}},
		"NIC": {{Value: "eth0", Sub: model.AttributeData{
			"IPAddress": {{Value: "192.0.2.10"}, {Value: "192.0.2.11"}},
		}}},
	}
}

func (w itemWorld) version(name string, data model.AttributeData) service.ConfigItemVersionCommand {
	return service.ConfigItemVersionCommand{Name: name, DeploymentStateID: w.planned, IncidentStateID: w.operational,
		Data: data}
}

// An item is made with its first version and changed by its next, each by the
// account that wrote it, the item carrying what its newest version says - and a
// version saying what the item says now is refused rather than kept.
func TestAConfigItemIsKeptInVersions(t *testing.T) {
	ctx := context.Background()
	w := newItemWorld(t)

	made, err := w.items.Create(ctx, 3, w.computer, w.version("Erika's laptop", w.laptopData()))
	if err != nil {
		t.Fatalf("making the laptop: %v", err)
	}

	if made.Item.Number == "" || made.Item.VersionCount != 1 || made.Item.Name != "Erika's laptop" ||
		made.Version.CreatedBy != 3 || made.Version.Data["NIC"][0].Sub["IPAddress"][1].Value != "192.0.2.11" {
		t.Fatalf("the laptop was made as %+v with %+v", made.Item, made.Version)
	}

	data := w.laptopData()
	data["Vendor"] = []model.AttributeValue{{Value: "Globex"}}

	changed, err := w.items.AddVersion(ctx, 4, made.Item.ID, w.version("Erika's laptop", data))
	if err != nil || changed.Item.VersionCount != 2 || changed.Version.Data["Vendor"][0].Value != "Globex" ||
		changed.Item.ChangedBy != 4 {
		t.Fatalf("the laptop's next version reads %+v, %v", changed, err)
	}

	_, err = w.items.AddVersion(ctx, 4, made.Item.ID, w.version("Erika's laptop", data))
	if detail, ok := apperror.Detail(err); !ok || detail.Code != "configItemUnchanged" {
		t.Errorf("a version saying what the item says answered %v, want configItemUnchanged", err)
	}

	versions, err := w.items.Versions(ctx, made.Item.ID)
	if err != nil || len(versions) != 2 || versions[0].Data["Vendor"][0].Value != "Globex" ||
		versions[1].Data["Vendor"][0].Value != "Acme" {
		t.Errorf("the versions read %+v, %v; want both, the newest first", versions, err)
	}

	listed, total, err := w.items.List(ctx, service.ConfigItemListFilter{ClassID: w.computer, Search: "erika"})
	if err != nil || total != 1 || listed[0].ID != made.Item.ID {
		t.Errorf("a search for the laptop found %+v (%d, %v)", listed, total, err)
	}

	if err := w.items.Delete(ctx, made.Item.ID); err != nil {
		t.Fatalf("deleting the laptop: %v", err)
	}

	if _, err := w.items.Get(ctx, made.Item.ID); apperror.KindOf(err) != apperror.KindNotFound {
		t.Errorf("the deleted laptop reads %v, want not found", err)
	}
}

// A version the definition does not allow is refused whole, naming the value
// at fault by the path of its keys.
func TestAConfigItemVersionTheDefinitionDoesNotAllowIsRefused(t *testing.T) {
	ctx := context.Background()
	w := newItemWorld(t)

	place, err := w.items.Create(ctx, 1, w.location, w.version("Head office", model.AttributeData{}))
	if err != nil {
		t.Fatalf("making a location: %v", err)
	}

	other, err := w.items.Create(ctx, 1, w.computer, w.version("Another laptop", w.laptopData()))
	if err != nil {
		t.Fatalf("making a computer: %v", err)
	}

	with := func(change func(model.AttributeData)) model.AttributeData {
		data := w.laptopData()
		change(data)

		return data
	}

	for code, data := range map[string]model.AttributeData{
		"configItemAttributeUnknown": with(func(d model.AttributeData) { d["Colour"] = []model.AttributeValue{{Value: "red"}} }),
		"configItemCountInvalid":     with(func(d model.AttributeData) { delete(d, "Type") }),
		"configItemValueRequired":    with(func(d model.AttributeData) { d["NIC"][0].Value = "  " }),
		"configItemValueTooLong":     with(func(d model.AttributeData) { d["Vendor"][0].Value = "Acme Corporation" }),
		"configItemValueInvalid":     with(func(d model.AttributeData) { d["Bought"][0].Value = "21.11.2026" }),
	} {
		_, err := w.items.Create(ctx, 1, w.computer, w.version("Refused", data))
		if detail, ok := apperror.Detail(err); !ok || detail.Code != code {
			t.Errorf("a version that should be refused with %s answered %v", code, err)
		}
	}

	// What a reference, a list or a contact names has to be there, and of the
	// kind the attribute says.
	for name, change := range map[string]func(model.AttributeData){
		"a location that is a computer": func(d model.AttributeData) {
			d["Location"] = []model.AttributeValue{{Value: strconv.FormatUint(uint64(other.Item.ID), 10)}}
		},
		"a type of another list": func(d model.AttributeData) {
			d["Type"][0].Value = strconv.FormatUint(uint64(w.planned), 10)
		},
		"a contact nobody is": func(d model.AttributeData) { d["Owner"][0].Value = "999" },
		"a type written with a leading zero": func(d model.AttributeData) {
			d["Type"][0].Value = "0" + strconv.FormatUint(uint64(w.laptop), 10)
		},
		"an IP beneath a section of none": func(d model.AttributeData) {
			d["NIC"][0].Sub["Gateway"] = []model.AttributeValue{{Value: "192.0.2.1"}}
		},
	} {
		_, err := w.items.Create(ctx, 1, w.computer, w.version("Refused", with(change)))
		if detail, ok := apperror.Detail(err); !ok || (detail.Code != "configItemValueInvalid" &&
			detail.Code != "configItemAttributeUnknown") {
			t.Errorf("%s answered %v, want it refused", name, err)
		}
	}

	right := with(func(d model.AttributeData) {
		d["Location"] = []model.AttributeValue{{Value: strconv.FormatUint(uint64(place.Item.ID), 10)}}
	})
	if _, err := w.items.Create(ctx, 1, w.computer, w.version("At the head office", right)); err != nil {
		t.Errorf("a laptop at the head office was refused: %v", err)
	}

	if _, err := w.items.Create(ctx, 1, w.computer, service.ConfigItemVersionCommand{Name: " ", Data: right}); apperror.KindOf(
		err) != apperror.KindInvalid {
		t.Errorf("a version with no name and no states answered %v, want it refused", err)
	}
}

// A class that is retired, or has no definition, makes no new item.
func TestAConfigItemNeedsAnOfferedClassWithADefinition(t *testing.T) {
	ctx := context.Background()
	w := newItemWorld(t)

	bare, err := w.catalog.Save(ctx, &model.CatalogItem{Class: model.CatalogCIClass, Name: "software", Valid: true})
	if err != nil {
		t.Fatal(err)
	}

	_, err = w.items.Create(ctx, 1, bare.ID, w.version("An editor", model.AttributeData{}))
	if detail, ok := apperror.Detail(err); !ok || detail.Code != "configItemNoDefinition" {
		t.Errorf("an item of a class with no definition answered %v, want configItemNoDefinition", err)
	}

	computer, _ := w.catalog.GetByID(ctx, w.computer)
	computer.Valid = false

	if _, err := w.catalog.Update(ctx, computer); err != nil {
		t.Fatal(err)
	}

	_, err = w.items.Create(ctx, 1, w.computer, w.version("A laptop", w.laptopData()))
	if detail, ok := apperror.Detail(err); !ok || detail.Code != "configItemClassRetired" {
		t.Errorf("an item of a retired class answered %v, want configItemClassRetired", err)
	}
}

// A state, a list value or a contact retired since stays where the version the
// item stands at names it, and is not chosen anew - by a new item, or by an item
// that had moved away from it.
func TestARetiredValueStaysWhereItIsButIsNotChosenAgain(t *testing.T) {
	ctx := context.Background()
	w := newItemWorld(t)

	made, err := w.items.Create(ctx, 1, w.computer, w.version("Erika's laptop", w.laptopData()))
	if err != nil {
		t.Fatalf("making the laptop: %v", err)
	}

	for _, id := range []uint{w.planned, w.laptop} {
		entry, _ := w.catalog.GetByID(ctx, id)
		entry.Valid = false

		if _, err := w.catalog.Update(ctx, entry); err != nil {
			t.Fatal(err)
		}
	}

	contact, _ := w.contacts.GetByID(ctx, w.contact)
	contact.Valid = false

	if _, err := w.contacts.Update(ctx, contact); err != nil {
		t.Fatal(err)
	}

	data := w.laptopData()
	data["Vendor"] = []model.AttributeValue{{Value: "Globex"}}

	if _, err := w.items.AddVersion(ctx, 1, made.Item.ID, w.version("Erika's laptop", data)); err != nil {
		t.Fatalf("a version keeping what was retired since was refused: %v", err)
	}

	elsewhere := service.ConfigItemVersionCommand{Name: "Another laptop", DeploymentStateID: w.production,
		IncidentStateID: w.operational, Data: w.laptopData()}

	for code, change := range map[string]func(*service.ConfigItemVersionCommand){
		"catalogValueRetired": func(*service.ConfigItemVersionCommand) {},
		"contactRetired": func(cmd *service.ConfigItemVersionCommand) {
			cmd.Data["Type"][0].Value = strconv.FormatUint(uint64(w.desktop), 10)
		},
	} {
		cmd := elsewhere
		cmd.Data = w.laptopData()
		change(&cmd)

		_, err := w.items.Create(ctx, 1, w.computer, cmd)
		if detail, ok := apperror.Detail(err); !ok || detail.Code != code {
			t.Errorf("a new item naming what was retired answered %v, want %s", err, code)
		}
	}

	moved := w.version("Erika's laptop", data)
	moved.DeploymentStateID = w.production

	if _, err := w.items.AddVersion(ctx, 1, made.Item.ID, moved); err != nil {
		t.Fatalf("moving the laptop into production: %v", err)
	}

	_, err = w.items.AddVersion(ctx, 1, made.Item.ID, w.version("Erika's laptop", data))
	if detail, ok := apperror.Detail(err); !ok || detail.Code != "catalogValueRetired" {
		t.Errorf("moving the laptop back to a state retired since answered %v, want catalogValueRetired", err)
	}
}
