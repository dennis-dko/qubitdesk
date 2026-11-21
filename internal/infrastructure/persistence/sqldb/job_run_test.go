package sqldb_test

import (
	"context"
	"testing"
	"time"

	"github.com/dennis-dko/qubitdesk/internal/domain/model"
	"github.com/dennis-dko/qubitdesk/internal/infrastructure/persistence/sqldb"
)

// A run is kept with its lines - a refusal's code and values among them - and
// read back the latest first; a job deleted takes its runs with it, and runs
// started before a moment are pruned.
func TestARunIsKeptWithWhatItSaid(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	macros := sqldb.NewMacroRepository(db, sqldb.DialectSQLite)
	jobs := sqldb.NewJobRepository(db, sqldb.DialectSQLite)
	runs := sqldb.NewJobRunRepository(db, sqldb.DialectSQLite)
	at := time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)

	macro, err := macros.Save(ctx, &model.Macro{Name: "Escalate", Valid: true})
	if err != nil {
		t.Fatal(err)
	}

	job, err := jobs.Save(ctx, &model.Job{Name: "Escalate the desk", Valid: true,
		Criteria: model.TicketCriteria{QueueIDs: []uint{1}}, MacroIDs: []uint{macro.ID}})
	if err != nil {
		t.Fatal(err)
	}

	ticket := uint(7)

	for i, started := range []time.Time{at.Add(-40 * 24 * time.Hour), at} {
		if _, err := runs.Record(ctx, &model.JobRun{
			JobID: job.ID, Trigger: model.RunManual, StartedAt: started, FinishedAt: started.Add(time.Second),
			Tickets: 2, Changed: 1, Failed: i,
			Lines: []model.RunLine{{TicketID: &ticket, MacroID: &macro.ID, Step: 1, Code: "stateRetired",
				Values: []any{"resolved"}, Message: "the state is retired", At: started}},
		}); err != nil {
			t.Fatal(err)
		}
	}

	kept, err := runs.ForJob(ctx, job.ID, 20)
	if err != nil || len(kept) != 2 || !kept[0].StartedAt.Equal(at) || kept[0].Failed != 1 || len(kept[0].Lines) != 1 {
		t.Fatalf("the job's runs read %+v, %v", kept, err)
	}

	if line := kept[0].Lines[0]; line.Code != "stateRetired" || len(line.Values) != 1 || line.Values[0] != "resolved" ||
		line.TicketID == nil || *line.TicketID != 7 || line.Step != 1 {
		t.Errorf("the run's line reads %+v", line)
	}

	if err := jobs.RanAt(ctx, job.ID, at); err != nil {
		t.Fatal(err)
	}

	if ran, _ := jobs.GetByID(ctx, job.ID); ran.LastRunAt == nil || !ran.LastRunAt.Equal(at) {
		t.Errorf("the job last ran at %v, want %v", ran.LastRunAt, at)
	}

	if removed, err := runs.PruneBefore(ctx, at.Add(-30*24*time.Hour)); err != nil || removed != 1 {
		t.Errorf("pruning removed %d runs, %v; want the old one", removed, err)
	}

	if err := jobs.Delete(ctx, job.ID); err != nil {
		t.Fatalf("deleting a job that has run: %v", err)
	}

	if left, _ := runs.ForJob(ctx, job.ID, 20); len(left) != 0 {
		t.Errorf("the deleted job's runs are still kept: %+v", left)
	}
}
