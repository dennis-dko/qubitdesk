package service_test

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dennis-dko/qubitdesk/internal/application/v1/command"
	"github.com/dennis-dko/qubitdesk/internal/application/v1/service"
	"github.com/dennis-dko/qubitdesk/internal/domain/model"
	"github.com/dennis-dko/qubitdesk/internal/infrastructure/persistence/memory"
	"github.com/dennis-dko/qubitdesk/internal/support/apperror"
)

// A plan keeps what its kind reads, each value once and in order - a time-based
// one its weekdays and times, an event-based one its events in the order they
// are offered - and a change of kind takes the other kind's values away.
func TestAPlanKeepsWhatItsKindReads(t *testing.T) {
	ctx := context.Background()
	plans := service.NewExecPlanApplicationService(memory.NewExecPlanRepository())

	mornings, err := plans.Create(ctx, command.ExecPlanCommand{
		Name: ptr(" Weekday mornings "), Type: ptr("TimeBased"),
		Weekdays: &[]int{5, 1, 3, 1}, Times: &[]int{17 * 60, 8 * 60, 8 * 60},
	})
	if err != nil {
		t.Fatal(err)
	}

	if mornings.Name != "Weekday mornings" || !mornings.Valid ||
		!slices.Equal(mornings.Weekdays, []time.Weekday{time.Monday, time.Wednesday, time.Friday}) ||
		!slices.Equal(mornings.Times, []int{8 * 60, 17 * 60}) || len(mornings.Events) != 0 {
		t.Fatalf("the plan was kept as %+v", mornings)
	}

	arrivals, err := plans.Create(ctx, command.ExecPlanCommand{
		Name: ptr("Arrivals"), Type: ptr("EventBased"), Events: &[]string{"ArticleCreate", "TicketCreate", "ArticleCreate"},
	})
	if err != nil || !slices.Equal(arrivals.Events, []model.TicketEvent{model.EventTicketCreate, model.EventArticleCreate}) {
		t.Fatalf("the event plan was kept as %+v, %v", arrivals, err)
	}

	turned, err := plans.Update(ctx, mornings.ID, command.ExecPlanCommand{
		Type: ptr("EventBased"), Events: &[]string{"TicketStateUpdate"},
	})
	if err != nil || len(turned.Weekdays) != 0 || len(turned.Times) != 0 || len(turned.Events) != 1 {
		t.Fatalf("a plan turned event-based reads %+v, %v; want its weekdays and times gone", turned, err)
	}

	if _, err := plans.Update(ctx, arrivals.ID, command.ExecPlanCommand{Weekdays: &[]int{1}}); apperror.KindOf(err) != apperror.KindInvalid {
		t.Errorf("weekdays sent for an event plan were answered %v, want them refused", err)
	}
}

// A plan that cannot run as it says is refused, with every field at fault: no
// name or kind, a kind there is not, a day past Saturday, a minute past the
// day, more times than a plan holds, an event there is not, and a plan of
// either kind without what its kind needs.
func TestAPlanThatCannotRunAsItSaysIsRefused(t *testing.T) {
	ctx := context.Background()
	plans := service.NewExecPlanApplicationService(memory.NewExecPlanRepository())

	tooMany := make([]int, model.MaxPlanTimes+1)
	for i := range tooMany {
		tooMany[i] = i
	}

	for name, refused := range map[string]struct {
		sent   command.ExecPlanCommand
		fields []string
	}{
		"nothing":            {command.ExecPlanCommand{}, []string{"name", "type"}},
		"a kind there isn't": {command.ExecPlanCommand{Name: ptr("x"), Type: ptr("Hourly")}, []string{"type"}},
		"a day past Saturday": {command.ExecPlanCommand{Name: ptr("x"), Type: ptr("TimeBased"),
			Weekdays: &[]int{7}, Times: &[]int{60}}, []string{"weekdays"}},
		"a minute past the day": {command.ExecPlanCommand{Name: ptr("x"), Type: ptr("TimeBased"),
			Weekdays: &[]int{1}, Times: &[]int{model.MinutesPerDay}}, []string{"times"}},
		"too many times": {command.ExecPlanCommand{Name: ptr("x"), Type: ptr("TimeBased"),
			Weekdays: &[]int{1}, Times: &tooMany}, []string{"times"}},
		"no weekday and no time": {command.ExecPlanCommand{Name: ptr("x"), Type: ptr("TimeBased")},
			[]string{"weekdays", "times"}},
		"an event there isn't": {command.ExecPlanCommand{Name: ptr("x"), Type: ptr("EventBased"),
			Events: &[]string{"TicketExplode"}}, []string{"events"}},
		"no event": {command.ExecPlanCommand{Name: ptr("x"), Type: ptr("EventBased")}, []string{"events"}},
		"times for events": {command.ExecPlanCommand{Name: ptr("x"), Type: ptr("EventBased"),
			Events: &[]string{"TicketCreate"}, Times: &[]int{60}}, []string{"times"}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := plans.Create(ctx, refused.sent)

			detail, ok := apperror.Detail(err)
			if !ok || strings.Join(detail.Fields, ",") != strings.Join(refused.fields, ",") {
				t.Errorf("the plan was answered %v; want %s refused", err, fmt.Sprint(refused.fields))
			}
		})
	}
}
