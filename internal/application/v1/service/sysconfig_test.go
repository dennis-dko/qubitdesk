package service_test

import (
	"context"
	"strings"
	"testing"

	"github.com/dennis-dko/go-ticket-system/internal/application/v1/service"
	"github.com/dennis-dko/go-ticket-system/internal/domain/model"
	"github.com/dennis-dko/go-ticket-system/internal/support/apperror"
)

// An option nobody changed reads as its default; a value is checked against its
// option; a value equal to the default is the default again rather than a
// stored copy of it; and resetting puts a changed option back.
func TestTheSystemConfigurationKeepsOnlyWhatWasChanged(t *testing.T) {
	ctx := context.Background()
	settings := newStubSettings()
	config := service.NewSysConfigService(settings)

	values, err := config.List(ctx, true)
	if err != nil {
		t.Fatal(err)
	}

	if len(values) != len(model.ConfigOptions()) {
		t.Fatalf("%d options listed, want every one of the %d", len(values), len(model.ConfigOptions()))
	}

	for _, value := range values {
		if value.Modified || value.Value != value.Option.Default {
			t.Errorf("%s reads as %q, modified %t, before anybody changed it", value.Option.Name, value.Value, value.Modified)
		}
	}

	changed, err := config.Set(ctx, model.ConfigTicketHook, "Call#")
	if err != nil || !changed.Modified || changed.Value != "Call#" {
		t.Fatalf("setting the hook answered %+v, %v", changed, err)
	}

	if stored, _ := settings.Get(ctx, model.SysConfigPrefix+model.ConfigTicketHook); stored != "Call#" {
		t.Errorf("the hook was stored as %q", stored)
	}

	if _, err := config.Set(ctx, model.ConfigTicketHook, strings.Repeat("x", 21)); apperror.KindOf(err) != apperror.KindInvalid {
		t.Errorf("a hook too long was answered %v, want it refused as invalid", err)
	}

	if _, err := config.Set(ctx, "Ticket::Nothing", "x"); apperror.KindOf(err) != apperror.KindNotFound {
		t.Errorf("an option that does not exist was answered %v, want not found", err)
	}

	back, err := config.Set(ctx, model.ConfigTicketHook, "Ticket#")
	if err != nil || back.Modified {
		t.Fatalf("setting the hook to its default answered %+v, %v", back, err)
	}

	if _, stored, _ := settings.Lookup(ctx, model.SysConfigPrefix+model.ConfigTicketHook); stored {
		t.Error("setting the hook to its default stored a copy of the default")
	}

	if _, err := config.Set(ctx, model.ConfigTicketHookDivider, ": "); err != nil {
		t.Fatal(err)
	}

	reset, err := config.Reset(ctx, model.ConfigTicketHookDivider)
	if err != nil || reset.Modified || reset.Value != "" {
		t.Fatalf("resetting the divider answered %+v, %v", reset, err)
	}

	if _, stored, _ := settings.Lookup(ctx, model.SysConfigPrefix+model.ConfigTicketHookDivider); stored {
		t.Error("resetting the divider left it stored")
	}
}
