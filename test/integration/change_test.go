//go:build integration

package integration

import (
	"net/http"
	"testing"
	"time"
)

type changeResponse struct {
	ID           uint   `json:"id"`
	Number       string `json:"number"`
	Title        string `json:"title"`
	ManagerID    *uint  `json:"managerId"`
	ManagerName  string `json:"managerName"`
	CategoryID   uint   `json:"categoryId"`
	ImpactID     uint   `json:"impactId"`
	PriorityID   uint   `json:"priorityId"`
	StateID      uint   `json:"stateId"`
	NextStateIDs []uint `json:"nextStateIds"`

	PlannedStartAt *time.Time `json:"plannedStartAt"`
	WorkOrders     int        `json:"workOrders"`
}

// catalogValue is the id of the value a list of the general catalog ships under
// the name given, as a client reads it.
func catalogValue(t *testing.T, c *client, class, name string) uint {
	t.Helper()

	var values struct {
		Items []catalogItem `json:"items"`
	}

	c.must(c.api(http.MethodGet, "/general-catalog?class="+class, nil), http.StatusOK).Data(t, &values)

	for _, entry := range values.Items {
		if entry.Name == name {
			return entry.ID
		}
	}

	t.Fatalf("the catalog ships no %s %s", class, name)

	return 0
}

// Changes are kept over HTTP on every database, as OTRS::ITSM keeps them: read
// with changes:read, which agents hold, and written with changes:write, which
// the administrator does; started requested and ranked by OTRS's matrix; moved
// only as its state machine allows; their manager released when that account
// is erased; and linked to a ticket by an agent who works the ticket.
func TestChangesAreKeptOverHTTP(t *testing.T) {
	t.Parallel()

	a, admin, agent := startWithWorker(t)

	value := func(class, name string) uint { return catalogValue(t, agent, class, name) }

	state := func(name string) uint { return value("ITSM::ChangeManagement::Change::State", name) }
	request := map[string]any{"title": "Replace the core switch",
		"categoryId": value("ITSM::ChangeManagement::Category", "5-very-high"),
		"impactId":   value("ITSM::ChangeManagement::Impact", "4-high")}

	if refused := agent.api(http.MethodPost, "/changes", request); refused.Status != http.StatusForbidden {
		t.Errorf("an agent making a change was answered %d, want 403", refused.Status)
	}

	var change changeResponse

	admin.must(admin.api(http.MethodPost, "/changes", request), http.StatusCreated, http.StatusOK).Data(t, &change)

	if change.Number == "" || change.StateID != state("requested") || len(change.NextStateIDs) != 4 ||
		change.PriorityID != value("ITSM::ChangeManagement::Priority", "5-very-high") {
		t.Errorf("the change was made as %+v; want it requested, with four ways on, ranked very high", change)
	}

	route := path("/changes/", change.ID)

	if refused := admin.api(http.MethodPut, route, map[string]any{"stateId": state("approved")}); refused.Status !=
		http.StatusConflict || refusalOf(t, refused).Code != "changeStateNotAllowed" {
		t.Errorf("approving a change nobody asked to approve was answered %d: %s", refused.Status, refused.Body)
	}

	admin.must(admin.api(http.MethodPut, route, map[string]any{"stateId": state("pending-approval")}),
		http.StatusOK)

	granted := a.signInAsGrantedAdministrator(admin, "Gerd", "gerd@example.com")

	var me struct {
		User userResponse `json:"user"`
	}

	granted.must(granted.api(http.MethodGet, "/me", nil), http.StatusOK).Data(t, &me)

	var managed changeResponse

	admin.must(admin.api(http.MethodPut, route, map[string]any{"managerId": me.User.ID}), http.StatusOK).
		Data(t, &managed)

	if managed.ManagerID == nil || managed.ManagerName != "Gerd" {
		t.Errorf("the change is managed by %+v", managed)
	}

	admin.must(admin.api(http.MethodDelete, path("/users/", me.User.ID), nil), http.StatusNoContent, http.StatusOK)

	var released changeResponse

	agent.must(agent.api(http.MethodGet, route, nil), http.StatusOK).Data(t, &released)

	if released.ManagerID != nil || released.StateID != state("pending-approval") {
		t.Errorf("after its manager was erased the change reads %+v; want it unmanaged and still waiting", released)
	}

	var ticket ticketResponse

	admin.must(admin.api(http.MethodPost, "/tickets", map[string]any{
		"title": "The switch drops packets", "queueId": theDesk(t, admin), "body": "Since Monday.",
	}), http.StatusCreated).Data(t, &ticket)

	agent.must(agent.api(http.MethodPost, "/links", map[string]any{
		"sourceObject": "ticket", "sourceId": ticket.ID, "targetObject": "change", "targetId": change.ID,
		"type": "Normal",
	}), http.StatusCreated, http.StatusOK)

	var links struct {
		Items []linkedRecord `json:"items"`
	}

	agent.must(agent.api(http.MethodGet, "/links?object=change&record="+path(change.ID), nil), http.StatusOK).
		Data(t, &links)

	if len(links.Items) != 1 || links.Items[0].ObjectID != ticket.ID {
		t.Errorf("the change is linked to %+v, want the ticket", links.Items)
	}

	if refused := agent.api(http.MethodDelete, route, nil); refused.Status != http.StatusForbidden {
		t.Errorf("an agent deleting a change was answered %d, want 403", refused.Status)
	}

	admin.must(admin.api(http.MethodDelete, route, nil), http.StatusNoContent, http.StatusOK)
}
