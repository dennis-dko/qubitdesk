package sqldb_test

import (
	"context"
	"slices"
	"testing"

	"github.com/dennis-dko/qubitdesk/internal/domain/model"
	"github.com/dennis-dko/qubitdesk/internal/infrastructure/persistence/sqldb"
	"github.com/dennis-dko/qubitdesk/internal/support/apperror"
)

// A job reads back with its criteria, its macros in order - one twice - and its
// plans; the macros and plans count the jobs that use them, a change writes the
// lists in place of the old ones, and a job deleted takes them with it.
func TestAJobReadsBackWithWhatItNames(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	macros := sqldb.NewMacroRepository(db, sqldb.DialectSQLite)
	plans := sqldb.NewExecPlanRepository(db, sqldb.DialectSQLite)
	jobs := sqldb.NewJobRepository(db, sqldb.DialectSQLite)

	escalate, err := macros.Save(ctx, &model.Macro{Name: "Escalate", Valid: true})
	if err != nil {
		t.Fatal(err)
	}

	note, err := macros.Save(ctx, &model.Macro{Name: "Note", Valid: true})
	if err != nil {
		t.Fatal(err)
	}

	arrivals, err := plans.Save(ctx, &model.ExecPlan{Name: "Arrivals", Valid: true, Type: model.PlanEventBased,
		Events: []model.TicketEvent{model.EventTicketCreate}})
	if err != nil {
		t.Fatal(err)
	}

	locked := true

	made, err := jobs.Save(ctx, &model.Job{
		Name: "Escalate the locked", Valid: true,
		Criteria: model.TicketCriteria{StateIDs: []uint{1, 2}, Locked: &locked},
		MacroIDs: []uint{note.ID, escalate.ID, note.ID}, PlanIDs: []uint{arrivals.ID},
	})
	if err != nil {
		t.Fatal(err)
	}

	read, err := jobs.GetByID(ctx, made.ID)
	if err != nil || !slices.Equal(read.Criteria.StateIDs, []uint{1, 2}) || read.Criteria.Locked == nil || !*read.Criteria.Locked ||
		len(read.Criteria.QueueIDs) != 0 || !slices.Equal(read.MacroIDs, []uint{note.ID, escalate.ID, note.ID}) ||
		!slices.Equal(read.PlanIDs, []uint{arrivals.ID}) || read.LastRunAt != nil {
		t.Fatalf("the job reads %+v, %v", read, err)
	}

	for name, counted := range map[string]func() (int, error){
		"note":     func() (int, error) { return macros.CountJobs(ctx, note.ID) },
		"escalate": func() (int, error) { return macros.CountJobs(ctx, escalate.ID) },
		"arrivals": func() (int, error) { return plans.CountJobs(ctx, arrivals.ID) },
	} {
		if count, err := counted(); err != nil || count != 1 {
			t.Errorf("%s is used by %d jobs, %v; want one", name, count, err)
		}
	}

	read.MacroIDs, read.PlanIDs = []uint{escalate.ID}, nil

	if changed, err := jobs.Update(ctx, read); err != nil || !slices.Equal(changed.MacroIDs, []uint{escalate.ID}) || len(changed.PlanIDs) != 0 {
		t.Fatalf("the change reads %+v, %v", changed, err)
	}

	if err := jobs.Delete(ctx, made.ID); err != nil {
		t.Fatal(err)
	}

	if count, err := macros.CountJobs(ctx, escalate.ID); err != nil || count != 0 {
		t.Errorf("after the job was deleted escalate is used by %d jobs, %v", count, err)
	}

	if _, err := jobs.GetByID(ctx, made.ID); apperror.KindOf(err) != apperror.KindNotFound {
		t.Errorf("the deleted job reads as %v, want it not found", err)
	}
}
