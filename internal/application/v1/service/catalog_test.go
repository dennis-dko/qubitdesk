package service_test

import (
	"context"
	"slices"
	"testing"

	"github.com/dennis-dko/go-ticket-system/internal/application/v1/command"
	"github.com/dennis-dko/go-ticket-system/internal/application/v1/service"
	"github.com/dennis-dko/go-ticket-system/internal/domain/model"
	"github.com/dennis-dko/go-ticket-system/internal/infrastructure/persistence/memory"
	"github.com/dennis-dko/go-ticket-system/internal/support/apperror"
)

// A value of the general catalog is kept with the functionality its class asks
// for - one of the three a deployment state or an incident state may have, and
// none in a class whose values mean nothing to the code. A class holds a name
// once, a value stays in the class it was added to, and one no longer offered
// is retired rather than deleted. A class is made by adding its first value.
func TestTheGeneralCatalogKeepsWhatEachClassAllows(t *testing.T) {
	ctx := context.Background()
	catalog := service.NewGeneralCatalogApplicationService(memory.NewGeneralCatalogRepository())

	add := func(class, name, functionality string) (*model.CatalogItem, error) {
		return catalog.Create(ctx, command.CatalogItemCommand{
			Class: ptr(class), Name: ptr(name), Functionality: ptr(functionality),
		})
	}

	staging, err := add(model.CatalogDeploymentState, "staging", "preproductive")
	if err != nil || !staging.Valid {
		t.Fatalf("add a deployment state: %+v, %v", staging, err)
	}

	for _, refused := range []struct{ class, name, functionality string }{
		{model.CatalogDeploymentState, "limbo", ""},
		{model.CatalogDeploymentState, "limbo", "operational"},
		{model.CatalogIncidentState, "smoking", "productive"},
		{"ITSM::ConfigItem::Laptop::Brand", "Acme", "productive"},
		{"", "Acme", ""},
		{"ITSM::ConfigItem::Laptop::Brand", " ", ""},
	} {
		_, err := add(refused.class, refused.name, refused.functionality)
		requireKind(t, err, apperror.KindInvalid)
	}

	if _, err := add("ITSM::ConfigItem::Laptop::Brand", "Acme", ""); err != nil {
		t.Fatalf("add the first value of a new class: %v", err)
	}

	_, err = add(model.CatalogDeploymentState, "staging", "productive")
	refusedWith(t, err, "catalogItemTaken")

	_, err = catalog.Update(ctx, staging.ID, command.CatalogItemCommand{Class: ptr(model.CatalogIncidentState)})
	requireKind(t, err, apperror.KindInvalid)

	retired, err := catalog.Update(ctx, staging.ID, command.CatalogItemCommand{
		Valid: ptr(false), Functionality: ptr("postproductive"),
	})
	if err != nil || retired.Valid || retired.Functionality != "postproductive" || retired.Name != "staging" {
		t.Errorf("retiring the value answered %+v, %v", retired, err)
	}

	classes, err := catalog.Classes(ctx)
	if err != nil || !slices.Equal(classes, []string{"ITSM::ConfigItem::DeploymentState", "ITSM::ConfigItem::Laptop::Brand"}) {
		t.Errorf("the classes are %v, %v", classes, err)
	}
}
