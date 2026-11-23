package sqldb_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/dennis-dko/qubitdesk/internal/domain/model"
	"github.com/dennis-dko/qubitdesk/internal/infrastructure/persistence/sqldb"
	"github.com/dennis-dko/qubitdesk/internal/support/apperror"
)

// A macro keeps its steps in the order they were given and their parameters as
// they were given, a change writes its steps in place of the old ones, and a
// macro deleted takes its steps with it.
func TestAMacroKeepsItsStepsInOrder(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	macros := sqldb.NewMacroRepository(db, sqldb.DialectSQLite)

	steps := func(macro *model.Macro) string {
		return fmt.Sprint(macro.Actions)
	}

	made, err := macros.Save(ctx, &model.Macro{Name: "Escalate", Valid: true, Actions: []model.MacroAction{
		{Type: model.ActionStateSet, Parameters: map[string]string{"stateId": "3", "pendingMinutes": "60"}},
		{Type: model.ActionOwnerSet, Parameters: map[string]string{}},
		{Type: model.ActionArticleCreate, Parameters: map[string]string{"channel": "note", "body": "Escalated – „überfällig“."}},
	}})
	if err != nil {
		t.Fatal(err)
	}

	want := fmt.Sprint([]model.MacroAction{
		{Type: model.ActionStateSet, Parameters: map[string]string{"stateId": "3", "pendingMinutes": "60"}},
		{Type: model.ActionOwnerSet, Parameters: map[string]string{}},
		{Type: model.ActionArticleCreate, Parameters: map[string]string{"channel": "note", "body": "Escalated – „überfällig“."}},
	})
	if read, err := macros.GetByID(ctx, made.ID); err != nil || steps(read) != want || read.Name != "Escalate" || !read.Valid {
		t.Fatalf("the macro reads %+v, %v; want its three steps as given", read, err)
	}

	made.Valid = false
	made.Actions = []model.MacroAction{{Type: model.ActionLockSet, Parameters: map[string]string{"locked": "false"}}}

	if changed, err := macros.Update(ctx, made); err != nil || len(changed.Actions) != 1 || changed.Valid {
		t.Fatalf("the change reads %+v, %v; want the one step in place of three, retired", changed, err)
	}

	if _, err := macros.Save(ctx, &model.Macro{Name: "Escalate"}); err == nil {
		t.Error("a second macro of the same name was kept")
	} else if detail, ok := apperror.Detail(err); !ok || detail.Code != "macroNameTaken" {
		t.Errorf("a second macro of the same name was answered %v, want macroNameTaken", err)
	}

	if err := macros.Delete(ctx, made.ID); err != nil {
		t.Fatal(err)
	}

	var left int
	if err := db.QueryRow("SELECT COUNT(*) FROM macro_actions").Scan(&left); err != nil || left != 0 {
		t.Errorf("deleting the macro left %d of its steps, %v", left, err)
	}

	if _, err := macros.GetByID(ctx, made.ID); apperror.KindOf(err) != apperror.KindNotFound {
		t.Errorf("the deleted macro reads as %v, want it not found", err)
	}
}
