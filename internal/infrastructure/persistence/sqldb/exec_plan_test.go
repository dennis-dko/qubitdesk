package sqldb_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/dennis-dko/qubitdesk/internal/domain/model"
	"github.com/dennis-dko/qubitdesk/internal/infrastructure/persistence/sqldb"
	"github.com/dennis-dko/qubitdesk/internal/support/apperror"
)

// A plan reads back with the weekdays, times and events it was kept with, and a
// change of kind keeps nothing of the other.
func TestAPlanReadsBackAsItWasKept(t *testing.T) {
	ctx := context.Background()
	plans := sqldb.NewExecPlanRepository(newTestDB(t), sqldb.DialectSQLite)

	made, err := plans.Save(ctx, &model.ExecPlan{
		Name: "Weekday mornings", Valid: true, Type: model.PlanTimeBased,
		Weekdays: []time.Weekday{time.Monday, time.Friday}, Times: []int{8 * 60, 17*60 + 30},
	})
	if err != nil {
		t.Fatal(err)
	}

	read, err := plans.GetByID(ctx, made.ID)
	if err != nil || read.Type != model.PlanTimeBased || !slices.Equal(read.Weekdays, []time.Weekday{time.Monday, time.Friday}) ||
		!slices.Equal(read.Times, []int{8 * 60, 17*60 + 30}) || len(read.Events) != 0 {
		t.Fatalf("the plan reads %+v, %v", read, err)
	}

	read.Type, read.Weekdays, read.Times = model.PlanEventBased, nil, nil
	read.Events = []model.TicketEvent{model.EventTicketCreate, model.EventArticleCreate}

	if changed, err := plans.Update(ctx, read); err != nil || len(changed.Weekdays) != 0 || len(changed.Times) != 0 ||
		!slices.Equal(changed.Events, []model.TicketEvent{model.EventTicketCreate, model.EventArticleCreate}) {
		t.Fatalf("the plan turned event-based reads %+v, %v", changed, err)
	}

	if err := plans.Delete(ctx, made.ID); err != nil {
		t.Fatal(err)
	}

	if _, err := plans.GetByID(ctx, made.ID); apperror.KindOf(err) != apperror.KindNotFound {
		t.Errorf("the deleted plan reads as %v, want it not found", err)
	}
}
