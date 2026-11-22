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

// changeWorld is the change catalog as it ships, OTRS's state machine and its
// matrix, an administrator who may work on changes and an agent who may only
// read them.
type changeWorld struct {
	changes *service.ChangeApplicationService
	repo    *memory.ChangeRepository
	users   *memory.UserRepository
	roles   *memory.RoleRepository
	catalog *memory.GeneralCatalogRepository
	value   func(class, name string) uint
	admin   *model.User
	agent   *model.User

	// adminCaller is the administrator as a request arrives, holding what the
	// administrator's role holds.
	adminCaller *service.Principal
}

func newChangeWorld(t *testing.T) changeWorld {
	t.Helper()

	ctx := context.Background()
	catalog := memory.NewGeneralCatalogRepository()
	ids := map[string]uint{}
	scale := []string{"1-very-low", "2-low", "3-normal", "4-high", "5-very-high"}

	for class, names := range map[string][]string{
		model.CatalogChangeCategory: scale, model.CatalogChangeImpact: scale, model.CatalogChangePriority: scale,
		model.CatalogChangeState: {"requested", "pending-approval", "rejected", "approved", "in-progress",
			"pending-pir", "successful", "failed", "canceled", "retracted"},
	} {
		for _, name := range names {
			entry, err := catalog.Save(ctx, &model.CatalogItem{Class: class, Name: name, Valid: true})
			if err != nil {
				t.Fatal(err)
			}

			ids[class+"/"+name] = entry.ID
		}
	}

	value := func(class, name string) uint { return ids[class+"/"+name] }
	state := func(name string) uint { return value(model.CatalogChangeState, name) }

	transitions := []model.StateTransition{{ToID: state("requested")}}
	for from, tos := range map[string][]string{
		"requested":        {"rejected", "retracted", "pending-approval", "in-progress"},
		"pending-approval": {"rejected", "retracted", "approved"},
		"approved":         {"retracted", "in-progress"},
		"in-progress":      {"pending-pir", "retracted", "failed", "successful", "canceled"},
		"pending-pir":      {"failed", "successful"},
	} {
		for _, to := range tos {
			id := state(from)
			transitions = append(transitions, model.StateTransition{FromID: &id, ToID: state(to)})
		}
	}

	matrix := [5][5]int{{0, 0, 1, 1, 2}, {0, 1, 1, 2, 3}, {1, 1, 2, 3, 3}, {1, 2, 3, 3, 4}, {2, 3, 3, 4, 4}}

	var cip []model.CIPEntry

	for impact, row := range matrix {
		for category, priority := range row {
			cip = append(cip, model.CIPEntry{CategoryID: value(model.CatalogChangeCategory, scale[category]),
				ImpactID:   value(model.CatalogChangeImpact, scale[impact]),
				PriorityID: value(model.CatalogChangePriority, scale[priority])})
		}
	}

	users := memory.NewUserRepository()
	roles := memory.NewRoleRepository(users)
	adminRole, _ := roles.GetByName(ctx, model.RoleAdmin)
	agentRole, _ := roles.GetByName(ctx, model.RoleAgent)
	admin, _ := users.Save(ctx, &model.User{Name: "Ada Admin", Email: "ada@example.com", RoleID: adminRole.ID})
	agent, _ := users.Save(ctx, &model.User{Name: "Agnes Agent", Email: "agnes@example.com", RoleID: agentRole.ID})
	now := time.Date(2026, 11, 21, 23, 30, 0, 0, time.UTC)
	repo := memory.NewChangeRepository(transitions, cip)

	return changeWorld{
		changes: service.NewChangeApplicationService(repo, catalog, users, roles,
			func(context.Context) string { return "Europe/Berlin" }).WithClock(func() time.Time { return now }),
		repo: repo, users: users, roles: roles, catalog: catalog, value: value, admin: admin, agent: agent,
		adminCaller: &service.Principal{User: admin, Permissions: model.AdminPermissions()},
	}
}

func (w changeWorld) state(name string) uint { return w.value(model.CatalogChangeState, name) }

// A change starts in the state the machine starts with, numbered by the day in
// the installation's zone, ranked normal where nobody says otherwise, and given
// the priority OTRS's matrix gives its category and impact - unless it is given
// one.
func TestAChangeStartsRequestedAndRankedByTheMatrix(t *testing.T) {
	ctx := context.Background()
	w := newChangeWorld(t)

	plain, err := w.changes.Create(ctx, w.adminCaller, service.ChangeCommand{Title: ptr("Replace the core switch")})
	if err != nil {
		t.Fatalf("making a change: %v", err)
	}

	normal := func(class string) uint { return w.value(class, "3-normal") }

	if plain.StateID != w.state("requested") || plain.Number != "2026112200000001" ||
		plain.CategoryID != normal(model.CatalogChangeCategory) || plain.ImpactID != normal(model.CatalogChangeImpact) ||
		plain.PriorityID != normal(model.CatalogChangePriority) || plain.CreatedBy != w.admin.ID {
		t.Errorf("the change was made as %+v", plain)
	}

	urgent, err := w.changes.Create(ctx, w.adminCaller, service.ChangeCommand{Title: ptr("Patch the firewall"),
		CategoryID: ptr(w.value(model.CatalogChangeCategory, "5-very-high")),
		ImpactID:   ptr(w.value(model.CatalogChangeImpact, "4-high"))})
	if err != nil || urgent.PriorityID != w.value(model.CatalogChangePriority, "5-very-high") {
		t.Errorf("a very high category of high impact was ranked %+v (%v), want very high as the matrix says",
			urgent, err)
	}

	chosen, err := w.changes.Create(ctx, w.adminCaller, service.ChangeCommand{Title: ptr("Tidy the wiring"),
		CategoryID: ptr(w.value(model.CatalogChangeCategory, "5-very-high")),
		PriorityID: ptr(w.value(model.CatalogChangePriority, "1-very-low"))})
	if err != nil || chosen.PriorityID != w.value(model.CatalogChangePriority, "1-very-low") {
		t.Errorf("a change given its priority was ranked %+v (%v), want the one given", chosen, err)
	}

	if _, err := w.changes.Create(ctx, w.adminCaller, service.ChangeCommand{Title: ptr("  ")}); apperror.KindOf(err) !=
		apperror.KindInvalid {
		t.Errorf("a change without a title was answered %v, want it refused", err)
	}
}

// A change moves only as the state machine allows, and a state that ends it
// leads nowhere.
func TestAChangeMovesOnlyAsTheStateMachineAllows(t *testing.T) {
	ctx := context.Background()
	w := newChangeWorld(t)

	change, err := w.changes.Create(ctx, w.adminCaller, service.ChangeCommand{Title: ptr("Move the server room")})
	if err != nil {
		t.Fatal(err)
	}

	next, err := w.changes.NextStates(ctx, change)
	if err != nil || len(next) != 4 {
		t.Errorf("a requested change may move to %v (%v), want OTRS's four", next, err)
	}

	_, err = w.changes.Update(ctx, w.adminCaller, change.ID, service.ChangeCommand{StateID: ptr(w.state("approved"))})
	if detail, ok := apperror.Detail(err); !ok || detail.Code != "changeStateNotAllowed" {
		t.Errorf("approving a change nobody asked to approve was answered %v, want changeStateNotAllowed", err)
	}

	for _, step := range []string{"pending-approval", "approved", "in-progress", "successful"} {
		if change, err = w.changes.Update(ctx, w.adminCaller, change.ID,
			service.ChangeCommand{StateID: ptr(w.state(step))}); err != nil {
			t.Fatalf("moving the change to %s: %v", step, err)
		}
	}

	if next, _ := w.changes.NextStates(ctx, change); len(next) != 0 {
		t.Errorf("a successful change may still move to %v", next)
	}

	_, err = w.changes.Update(ctx, w.adminCaller, change.ID, service.ChangeCommand{StateID: ptr(w.state("failed"))})
	if detail, ok := apperror.Detail(err); !ok || detail.Code != "changeStateNotAllowed" {
		t.Errorf("a successful change failing after all was answered %v, want changeStateNotAllowed", err)
	}
}

// A change's manager and builder are accounts that may work on changes, and
// either can be taken away again.
func TestAChangeIsManagedByAnAccountThatMayWorkOnIt(t *testing.T) {
	ctx := context.Background()
	w := newChangeWorld(t)

	_, err := w.changes.Create(ctx, w.adminCaller, service.ChangeCommand{Title: ptr("Renew the certificates"),
		ManagerID: &w.agent.ID})
	if detail, ok := apperror.Detail(err); !ok || detail.Code != "changeManagerCannotWork" {
		t.Errorf("an agent who only reads changes as the manager was answered %v, want changeManagerCannotWork", err)
	}

	change, err := w.changes.Create(ctx, w.adminCaller, service.ChangeCommand{Title: ptr("Renew the certificates"),
		ManagerID: &w.admin.ID, BuilderID: &w.admin.ID})
	if err != nil || change.ManagerID == nil || *change.ManagerID != w.admin.ID {
		t.Fatalf("the administrator managing the change: %+v (%v)", change, err)
	}

	_, err = w.changes.Update(ctx, w.adminCaller, change.ID, service.ChangeCommand{BuilderID: &w.agent.ID})
	if detail, ok := apperror.Detail(err); !ok || detail.Code != "changeBuilderCannotWork" {
		t.Errorf("an agent who only reads changes as the builder was answered %v, want changeBuilderCannotWork", err)
	}

	cleared, err := w.changes.Update(ctx, w.adminCaller, change.ID, service.ChangeCommand{ClearManager: true})
	if err != nil || cleared.ManagerID != nil || cleared.BuilderID == nil {
		t.Errorf("taking the manager away left %+v (%v), want no manager and the builder still there", cleared, err)
	}

	if _, err := w.changes.Create(ctx, w.adminCaller, service.ChangeCommand{Title: ptr("Ghost"),
		ManagerID: ptr(uint(999))}); apperror.KindOf(err) != apperror.KindInvalid {
		t.Errorf("a manager nobody is was answered %v, want it refused", err)
	}
}

// A retired value is not chosen for a change, and stays on one that already
// names it.
func TestARetiredRankIsNotChosenForAChange(t *testing.T) {
	ctx := context.Background()
	w := newChangeWorld(t)
	high := w.value(model.CatalogChangeCategory, "4-high")

	change, err := w.changes.Create(ctx, w.adminCaller, service.ChangeCommand{Title: ptr("Upgrade the mail server"),
		CategoryID: &high})
	if err != nil {
		t.Fatal(err)
	}

	entry, _ := w.catalog.GetByID(ctx, high)
	entry.Valid = false

	if _, err := w.catalog.Update(ctx, entry); err != nil {
		t.Fatal(err)
	}

	if _, err := w.changes.Update(ctx, w.adminCaller, change.ID,
		service.ChangeCommand{Title: ptr("Upgrade the mail servers")}); err != nil {
		t.Errorf("correcting a change ranked by a category retired since was refused: %v", err)
	}

	_, err = w.changes.Create(ctx, w.adminCaller, service.ChangeCommand{Title: ptr("Another"), CategoryID: &high})
	if detail, ok := apperror.Detail(err); !ok || detail.Code != "catalogValueRetired" {
		t.Errorf("a new change of a retired category was answered %v, want catalogValueRetired", err)
	}
}
