package service_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/dennis-dko/go-ticket-system/internal/application/v1/command"
	"github.com/dennis-dko/go-ticket-system/internal/application/v1/service"
	"github.com/dennis-dko/go-ticket-system/internal/domain/model"
	"github.com/dennis-dko/go-ticket-system/internal/infrastructure/persistence/memory"
	"github.com/dennis-dko/go-ticket-system/internal/support/apperror"
)

// jobFixture is the job service over in-memory tables holding a queue, a
// macro and a plan offered, and one of each retired.
type jobFixture struct {
	jobs     *service.JobApplicationService
	macros   *memory.MacroRepository
	plans    *memory.ExecPlanRepository
	macroSvc *service.MacroApplicationService
	planSvc  *service.ExecPlanApplicationService
	desk     uint
	escalate uint
	oldMacro uint
	mornings uint
	oldPlan  uint
}

func newJobFixture(t *testing.T) jobFixture {
	t.Helper()

	ctx := context.Background()
	queues := memory.NewQueueRepository()
	macros := memory.NewMacroRepository()
	plans := memory.NewExecPlanRepository()
	refs := service.MacroReferences{
		Catalog: service.TicketCatalogRepositories{
			States: memory.NewTicketStateRepository(), Priorities: memory.NewTicketPriorityRepository(),
			Types: memory.NewTicketTypeRepository(),
		},
		Queues: queues,
	}

	desk, _ := queues.Save(ctx, &model.Queue{Name: "Service Desk", Valid: true, FollowUp: model.FollowUpReopen})
	escalate, _ := macros.Save(ctx, &model.Macro{Name: "Escalate", Valid: true})
	oldMacro, _ := macros.Save(ctx, &model.Macro{Name: "Old way"})
	mornings, _ := plans.Save(ctx, &model.ExecPlan{Name: "Mornings", Valid: true, Type: model.PlanEventBased,
		Events: []model.TicketEvent{model.EventTicketCreate}})
	oldPlan, _ := plans.Save(ctx, &model.ExecPlan{Name: "Old plan", Type: model.PlanEventBased,
		Events: []model.TicketEvent{model.EventTicketCreate}})

	return jobFixture{
		jobs:     service.NewJobApplicationService(memory.NewJobRepository(), macros, plans, refs),
		macros:   macros,
		plans:    plans,
		macroSvc: service.NewMacroApplicationService(macros, refs),
		planSvc:  service.NewExecPlanApplicationService(plans),
		desk:     desk.ID, escalate: escalate.ID, oldMacro: oldMacro.ID, mornings: mornings.ID, oldPlan: oldPlan.ID,
	}
}

// A job is kept with its criteria, its macros in the order given - one may come
// twice - and its plans each once, and a change that sends none of them leaves
// them as they were.
func TestAJobIsKeptWithWhatItNames(t *testing.T) {
	ctx := context.Background()
	f := newJobFixture(t)

	made, err := f.jobs.Create(ctx, command.JobCommand{
		Name:     ptr(" Escalate the desk "),
		Criteria: &command.JobCriteriaCommand{QueueIDs: []uint{f.desk, f.desk}},
		MacroIDs: &[]uint{f.escalate, f.escalate},
		PlanIDs:  &[]uint{f.mornings, f.mornings},
	})
	if err != nil {
		t.Fatal(err)
	}

	if made.Name != "Escalate the desk" || !made.Valid || !slices.Equal(made.Criteria.QueueIDs, []uint{f.desk}) ||
		!slices.Equal(made.MacroIDs, []uint{f.escalate, f.escalate}) || !slices.Equal(made.PlanIDs, []uint{f.mornings}) ||
		made.LastRunAt != nil {
		t.Fatalf("the job was kept as %+v", made)
	}

	renamed, err := f.jobs.Update(ctx, made.ID, command.JobCommand{Name: ptr("Escalate")})
	if err != nil || len(renamed.MacroIDs) != 2 || len(renamed.PlanIDs) != 1 || len(renamed.Criteria.QueueIDs) != 1 {
		t.Errorf("a change sending only a name left the job as %+v, %v", renamed, err)
	}
}

// A job that could not run as it says is refused, with every field at fault:
// no name, criteria or macro; criteria naming nothing, which would take every
// ticket there is, or a record that is not there; a macro or a plan that is not
// there, or retired and named anew; more macros than a job runs.
func TestAJobThatCouldNotRunAsItSaysIsRefused(t *testing.T) {
	ctx := context.Background()
	f := newJobFixture(t)

	tooMany := make([]uint, model.MaxJobMacros+1)
	for i := range tooMany {
		tooMany[i] = f.escalate
	}

	desk := &command.JobCriteriaCommand{QueueIDs: []uint{f.desk}}
	escalate := &[]uint{f.escalate}

	for name, refused := range map[string]struct {
		sent   command.JobCommand
		fields []string
	}{
		"nothing":                 {command.JobCommand{}, []string{"name", "criteria", "macroIds"}},
		"criteria naming nothing": {command.JobCommand{Name: ptr("x"), Criteria: &command.JobCriteriaCommand{}, MacroIDs: escalate}, []string{"criteria"}},
		"a queue there is not": {command.JobCommand{Name: ptr("x"), MacroIDs: escalate,
			Criteria: &command.JobCriteriaCommand{QueueIDs: []uint{99}}}, []string{"criteria"}},
		"no macro":             {command.JobCommand{Name: ptr("x"), Criteria: desk, MacroIDs: &[]uint{}}, []string{"macroIds"}},
		"a macro there is not": {command.JobCommand{Name: ptr("x"), Criteria: desk, MacroIDs: &[]uint{99}}, []string{"macroIds"}},
		"a retired macro":      {command.JobCommand{Name: ptr("x"), Criteria: desk, MacroIDs: &[]uint{f.oldMacro}}, []string{"macroIds"}},
		"too many macros":      {command.JobCommand{Name: ptr("x"), Criteria: desk, MacroIDs: &tooMany}, []string{"macroIds"}},
		"a plan there is not":  {command.JobCommand{Name: ptr("x"), Criteria: desk, MacroIDs: escalate, PlanIDs: &[]uint{99}}, []string{"planIds"}},
		"a retired plan":       {command.JobCommand{Name: ptr("x"), Criteria: desk, MacroIDs: escalate, PlanIDs: &[]uint{f.oldPlan}}, []string{"planIds"}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := f.jobs.Create(ctx, refused.sent)

			detail, ok := apperror.Detail(err)
			if !ok || strings.Join(detail.Fields, ",") != strings.Join(refused.fields, ",") {
				t.Errorf("the job was answered %v; want %v refused", err, refused.fields)
			}
		})
	}
}

// A macro or a plan a job came to name before it was retired stays named; a
// macro or a plan a job uses is not deleted from under it, and the refusal
// says how many jobs use it.
func TestWhatAJobUsesIsRetiredRatherThanDeleted(t *testing.T) {
	ctx := context.Background()
	f := newJobFixture(t)

	job, err := f.jobs.Create(ctx, command.JobCommand{
		Name: ptr("Escalate"), Criteria: &command.JobCriteriaCommand{QueueIDs: []uint{f.desk}},
		MacroIDs: &[]uint{f.escalate}, PlanIDs: &[]uint{f.mornings},
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := f.macroSvc.Update(ctx, f.escalate, command.MacroCommand{Valid: ptr(false)}); err != nil {
		t.Fatal(err)
	}

	if _, err := f.jobs.Update(ctx, job.ID, command.JobCommand{MacroIDs: &[]uint{f.escalate}}); err != nil {
		t.Errorf("keeping a macro retired since it was named was answered %v", err)
	}

	f.macros.UseInJobs(f.escalate, 1)
	f.plans.UseInJobs(f.mornings, 2)

	if detail, ok := apperror.Detail(f.macroSvc.Delete(ctx, f.escalate)); !ok || detail.Code != "macroInUse" ||
		detail.Values[0] != "Escalate" || detail.Values[1] != 1 {
		t.Errorf("deleting a macro a job runs was answered %+v", detail)
	}

	if detail, ok := apperror.Detail(f.planSvc.Delete(ctx, f.mornings)); !ok || detail.Code != "planInUse" ||
		detail.Values[1] != 2 {
		t.Errorf("deleting a plan two jobs run on was answered %+v", detail)
	}
}
