package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/dennis-dko/qubitdesk/internal/application/v1/service"
	"github.com/dennis-dko/qubitdesk/internal/domain/model"
	"github.com/dennis-dko/qubitdesk/internal/infrastructure/persistence/memory"
	"github.com/dennis-dko/qubitdesk/internal/support/apperror"
)

// workOrderWorld is a change world with OTRS's work order states, types and
// state machine, a change to carry out, a clock a test moves, and beside the
// agent a colleague who may see changes as well and a customer who may not.
type workOrderWorld struct {
	changeWorld
	orders    *service.WorkOrderApplicationService
	ids       map[string]uint
	change    *model.Change
	clock     *time.Time
	colleague *model.User
	customer  *model.User
}

func newWorkOrderWorld(t *testing.T) workOrderWorld {
	t.Helper()

	ctx := context.Background()
	w := workOrderWorld{changeWorld: newChangeWorld(t), ids: map[string]uint{}}

	for class, names := range map[string][]string{
		model.CatalogWorkOrderState: {"created", "accepted", "ready", "in-progress", "closed", "canceled"},
		model.CatalogWorkOrderType:  {"approval", "workorder", "backout", "decision", "pir"},
	} {
		for _, name := range names {
			entry, err := w.catalog.Save(ctx, &model.CatalogItem{Class: class, Name: name, Valid: true})
			if err != nil {
				t.Fatal(err)
			}

			w.ids[class+"/"+name] = entry.ID
		}
	}

	transitions := []model.StateTransition{{ToID: w.state("created")}}
	for from, tos := range map[string][]string{
		"created": {"accepted", "canceled"}, "accepted": {"ready", "canceled"},
		"ready": {"in-progress", "canceled"}, "in-progress": {"closed", "canceled"},
	} {
		for _, to := range tos {
			id := w.state(from)
			transitions = append(transitions, model.StateTransition{FromID: &id, ToID: w.state(to)})
		}
	}

	agentRole, _ := w.roles.GetByName(ctx, model.RoleAgent)
	customerRole, _ := w.roles.GetByName(ctx, model.RoleCustomer)
	w.colleague, _ = w.users.Save(ctx, &model.User{Name: "Carl Colleague", Email: "carl@example.com",
		RoleID: agentRole.ID})
	w.customer, _ = w.users.Save(ctx, &model.User{Name: "Cora Customer", Email: "cora@example.com",
		RoleID: customerRole.ID})

	clock := time.Date(2026, 11, 23, 9, 0, 0, 0, time.UTC)
	w.clock = &clock
	w.orders = service.NewWorkOrderApplicationService(memory.NewWorkOrderRepository(transitions), w.repo,
		w.catalog, w.users, w.roles).WithClock(func() time.Time { return *w.clock })

	change, err := w.changes.Create(ctx, w.adminCaller, service.ChangeCommand{Title: ptr("Replace the core switch")})
	if err != nil {
		t.Fatal(err)
	}

	w.change = change

	return w
}

// state is the id of a work order state; the change world's own state method
// answers a change's.
func (w workOrderWorld) state(name string) uint { return w.ids[model.CatalogWorkOrderState+"/"+name] }

func (w workOrderWorld) kind(name string) uint { return w.ids[model.CatalogWorkOrderType+"/"+name] }

// caller is an account as a request arrives from it, holding what an agent's
// role holds.
func (w workOrderWorld) caller(user *model.User) *service.Principal {
	return &service.Principal{User: user, Permissions: model.AgentPermissions()}
}

// create adds a work order of nothing but a title to the change.
func (w workOrderWorld) create(t *testing.T, title string) *model.WorkOrder {
	t.Helper()

	order, err := w.orders.Create(context.Background(), w.adminCaller, w.change.ID,
		service.WorkOrderCommand{Title: ptr(title)})
	if err != nil {
		t.Fatalf("adding the work order %q: %v", title, err)
	}

	return order
}

// moved moves a work order along the states named, by the caller.
func (w workOrderWorld) moved(t *testing.T, order *model.WorkOrder, steps ...string) *model.WorkOrder {
	t.Helper()

	for _, step := range steps {
		var err error
		if order, err = w.orders.Update(context.Background(), w.adminCaller, order.ID,
			service.WorkOrderCommand{StateID: ptr(w.state(step))}); err != nil {
			t.Fatalf("moving the work order to %s: %v", step, err)
		}
	}

	return order
}

// A work order starts created and of OTRS's default type, takes its place in
// its change's plan, and is planned from a start to a later end or for no
// moment at all.
func TestAWorkOrderStartsCreatedAndTakesItsPlaceInThePlan(t *testing.T) {
	ctx := context.Background()
	w := newWorkOrderWorld(t)
	on := func(hour int) *time.Time {
		moment := time.Date(2026, 11, 24, hour, 0, 0, 0, time.UTC)

		return &moment
	}

	review, err := w.orders.Create(ctx, w.adminCaller, w.change.ID, service.WorkOrderCommand{Title: ptr("Review"),
		TypeID: ptr(w.kind("pir")), PlannedStartAt: on(16), PlannedEndAt: on(17)})
	if err != nil {
		t.Fatal(err)
	}

	install, err := w.orders.Create(ctx, w.adminCaller, w.change.ID, service.WorkOrderCommand{
		Title: ptr("Install"), PlannedStartAt: on(8), PlannedEndAt: on(12), PlannedMinutes: ptr(240)})
	if err != nil {
		t.Fatal(err)
	}

	if install.StateID != w.state("created") || install.TypeID != w.kind("workorder") || install.Number != 1 ||
		install.CreatedBy != w.admin.ID {
		t.Errorf("the work order was added as %+v; want it created, a plain work order, first in the plan", install)
	}

	orders, err := w.orders.Of(ctx, w.change.ID)
	if err != nil || len(orders) != 2 || orders[0].ID != install.ID || orders[1].ID != review.ID ||
		orders[1].Number != 2 {
		t.Errorf("the change's work orders read %+v (%v), want the installation before the review", orders, err)
	}

	for name, cmd := range map[string]service.WorkOrderCommand{
		"no title":                      {Title: ptr("  ")},
		"a start without an end":        {Title: ptr("Half"), PlannedStartAt: on(8)},
		"an end before its start":       {Title: ptr("Backwards"), PlannedStartAt: on(12), PlannedEndAt: on(8)},
		"an actual end without a start": {Title: ptr("Ended"), ActualEndAt: on(9)},
		"a type of another list":        {Title: ptr("Odd"), TypeID: ptr(w.state("ready"))},
	} {
		if _, err := w.orders.Create(ctx, w.adminCaller, w.change.ID, cmd); apperror.KindOf(err) !=
			apperror.KindInvalid {
			t.Errorf("a work order with %s was answered %v, want it refused", name, err)
		}
	}

	if _, err := w.orders.Of(ctx, w.change.ID+99); apperror.KindOf(err) != apperror.KindNotFound {
		t.Errorf("the work orders of a change there is not were answered %v, want not found", err)
	}

	schedules, err := w.orders.Schedules(ctx, []uint{w.change.ID})
	if schedule := schedules[w.change.ID]; err != nil || schedule.WorkOrders != 2 ||
		schedule.PlannedStartAt == nil || !schedule.PlannedStartAt.Equal(*on(8)) || schedule.PlannedMinutes != 240 {
		t.Errorf("the change is scheduled %+v (%v), want it planned from the installation on", schedule, err)
	}
}

// A work order moves as OTRS's machine allows, and is given the moment it began
// once it is in progress and the moment it ended once its state ends it -
// unless somebody has said them.
func TestAWorkOrderMovesAsTheMachineAllowsAndIsTimedAsOTRSTimesIt(t *testing.T) {
	ctx := context.Background()
	w := newWorkOrderWorld(t)
	order := w.create(t, "Install")

	_, err := w.orders.Update(ctx, w.adminCaller, order.ID,
		service.WorkOrderCommand{StateID: ptr(w.state("in-progress"))})
	if detail, ok := apperror.Detail(err); !ok || detail.Code != "workOrderStateNotAllowed" {
		t.Errorf("a work order nobody accepted going into progress was answered %v, want workOrderStateNotAllowed",
			err)
	}

	if order = w.moved(t, order, "accepted", "ready"); order.ActualStartAt != nil {
		t.Errorf("a work order that is ready began at %v", order.ActualStartAt)
	}

	begun := *w.clock

	if order = w.moved(t, order, "in-progress"); order.ActualStartAt == nil || !order.ActualStartAt.Equal(begun) ||
		order.ActualEndAt != nil {
		t.Errorf("a work order going into progress began at %v and ended at %v, want it begun now and going on",
			order.ActualStartAt, order.ActualEndAt)
	}

	*w.clock = w.clock.Add(3 * time.Hour)

	if order = w.moved(t, order, "closed"); order.ActualEndAt == nil ||
		!order.ActualEndAt.Equal(w.clock.Add(time.Second)) || !order.ActualStartAt.Equal(begun) {
		t.Errorf("a closed work order ran from %v to %v, want from when it began to a second after now",
			order.ActualStartAt, order.ActualEndAt)
	}

	if next, err := w.orders.NextStates(ctx, order); err != nil || len(next) != 0 {
		t.Errorf("a closed work order may still move to %v (%v)", next, err)
	}

	if abandoned := w.moved(t, w.create(t, "Abandoned"), "canceled"); abandoned.ActualStartAt == nil ||
		abandoned.ActualEndAt == nil || !abandoned.ActualStartAt.Before(*abandoned.ActualEndAt) {
		t.Errorf("a work order canceled at once ran from %v to %v, want it begun just before it ended",
			abandoned.ActualStartAt, abandoned.ActualEndAt)
	}

	said := w.moved(t, w.create(t, "Said"), "accepted", "ready")
	early := w.clock.Add(-2 * time.Hour)

	said, err = w.orders.Update(ctx, w.adminCaller, said.ID,
		service.WorkOrderCommand{StateID: ptr(w.state("in-progress")), ActualStartAt: &early})
	if err != nil || said.ActualStartAt == nil || !said.ActualStartAt.Equal(early) {
		t.Errorf("a work order said to have begun earlier began at %v (%v), want the moment said",
			said.ActualStartAt, err)
	}
}

// Without the right to work on changes a work order takes only a report, and
// only from its agent - who is an account that may see changes.
func TestOnlyAWorkOrdersAgentReportsOnItWithoutTheRightToWorkOnChanges(t *testing.T) {
	ctx := context.Background()
	w := newWorkOrderWorld(t)

	_, err := w.orders.Create(ctx, w.adminCaller, w.change.ID,
		service.WorkOrderCommand{Title: ptr("Install"), AgentID: &w.customer.ID})
	if detail, ok := apperror.Detail(err); !ok || detail.Code != "workOrderAgentCannotSee" {
		t.Errorf("a customer as the agent was answered %v, want workOrderAgentCannotSee", err)
	}

	order, err := w.orders.Create(ctx, w.adminCaller, w.change.ID,
		service.WorkOrderCommand{Title: ptr("Install"), AgentID: &w.agent.ID})
	if err != nil {
		t.Fatal(err)
	}

	reported, err := w.orders.Update(ctx, w.caller(w.agent), order.ID, service.WorkOrderCommand{
		Report: ptr("Racked and cabled."), StateID: ptr(w.state("accepted")), Minutes: 45})
	if err != nil || reported.Report != "Racked and cabled." || reported.StateID != w.state("accepted") ||
		reported.AccountedMinutes != 45 || reported.ChangedBy != w.agent.ID {
		t.Fatalf("the agent's report left %+v (%v)", reported, err)
	}

	if reported, err = w.orders.Update(ctx, w.caller(w.agent), order.ID,
		service.WorkOrderCommand{Minutes: 30}); err != nil || reported.AccountedMinutes != 75 {
		t.Errorf("a second report accounted %d minutes in all (%v), want the two added up", reported.AccountedMinutes,
			err)
	}

	for name, attempt := range map[string]struct {
		caller *service.Principal
		cmd    service.WorkOrderCommand
	}{
		"its agent retitling it":      {w.caller(w.agent), service.WorkOrderCommand{Title: ptr("Install twice")}},
		"its agent handing it on":     {w.caller(w.agent), service.WorkOrderCommand{AgentID: &w.colleague.ID}},
		"its agent replanning it":     {w.caller(w.agent), service.WorkOrderCommand{ClearPlanned: true}},
		"a colleague reporting on it": {w.caller(w.colleague), service.WorkOrderCommand{Report: ptr("Done, I think.")}},
	} {
		_, err := w.orders.Update(ctx, attempt.caller, order.ID, attempt.cmd)
		if detail, ok := apperror.Detail(err); !ok || detail.Code != "workOrderReportOnly" {
			t.Errorf("%s was answered %v, want workOrderReportOnly", name, err)
		}
	}

	if _, err := w.orders.Update(ctx, w.caller(w.agent), order.ID,
		service.WorkOrderCommand{Minutes: model.MaxArticleMinutes + 1}); apperror.KindOf(err) != apperror.KindInvalid {
		t.Errorf("a report of more than a day's minutes was answered %v, want it refused", err)
	}
}
