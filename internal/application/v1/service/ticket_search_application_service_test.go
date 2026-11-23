package service_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/dennis-dko/qubitdesk/internal/application/v1/service"
	"github.com/dennis-dko/qubitdesk/internal/domain/model"
	"github.com/dennis-dko/qubitdesk/internal/infrastructure/persistence/memory"
	"github.com/dennis-dko/qubitdesk/internal/support/apperror"
)

// A saved search is its agent's own: kept under a name they have not used,
// listed for them alone, deleted by them alone, and no more of them than an
// account keeps.
func TestASavedSearchIsItsAgentsOwn(t *testing.T) {
	ctx := context.Background()
	searches := service.NewTicketSearchApplicationService(memory.NewTicketSearchRepository())
	agnes := &service.Principal{User: &model.User{ID: 1, Name: "Agnes"}}
	bert := &service.Principal{User: &model.User{ID: 2, Name: "Bert"}}

	printers, err := searches.Save(ctx, agnes, "  Printers  ", "text=printer")
	if err != nil {
		t.Fatal(err)
	}

	if printers.Name != "Printers" || printers.Query != "text=printer" || printers.UserID != 1 {
		t.Errorf("the search was kept as %+v", printers)
	}

	if _, err := searches.Save(ctx, agnes, "PRINTERS", "text=toner"); errorCode(err) != "ticketSearchNameTaken" {
		t.Errorf("a second search called the same answered %v, want the name refused", err)
	}

	if _, err := searches.Save(ctx, bert, "Printers", "text=printer"); err != nil {
		t.Errorf("another agent could not use the same name: %v", err)
	}

	for name, query := range map[string]string{"": "text=x", strings.Repeat("n", model.MaxTicketSearchName+1): "text=x"} {
		_, err := searches.Save(ctx, agnes, name, query)
		if detail, ok := apperror.Detail(err); !ok || detail.Kind != apperror.KindInvalid {
			t.Errorf("a search named %d characters answered %v, want it refused", len(name), err)
		}
	}

	if mine, _ := searches.Mine(ctx, agnes); len(mine) != 1 || mine[0].ID != printers.ID {
		t.Errorf("Agnes keeps %+v, want her printers alone", mine)
	}

	if err := searches.Delete(ctx, bert, printers.ID); apperror.KindOf(err) != apperror.KindNotFound {
		t.Errorf("Bert deleting Agnes's search answered %v, want it not found", err)
	}

	if err := searches.Delete(ctx, agnes, printers.ID); err != nil {
		t.Errorf("Agnes could not delete her own search: %v", err)
	}

	for i := 0; i < model.MaxTicketSearches; i++ {
		if _, err := searches.Save(ctx, agnes, fmt.Sprintf("Search %d", i), "text=x"); err != nil {
			t.Fatalf("search %d was refused: %v", i, err)
		}
	}

	if _, err := searches.Save(ctx, agnes, "One too many", "text=x"); errorCode(err) != "ticketSearchLimit" {
		t.Errorf("a search past the most an account keeps answered %v, want it refused", err)
	}

	if mine, err := searches.Mine(ctx, &service.Principal{System: true}); err != nil || len(mine) != 0 {
		t.Errorf("the application keeps %+v, %v; want no searches, being no account", mine, err)
	}
}

// errorCode is the code a refusal carries, empty where it carries none.
func errorCode(err error) string {
	if detail, ok := apperror.Detail(err); ok {
		return detail.Code
	}

	return ""
}
