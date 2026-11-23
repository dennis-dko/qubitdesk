//go:build integration

package integration

import (
	"net/http"
	"testing"
	"time"
)

type workOrderResponse struct {
	ID               uint       `json:"id"`
	Number           int        `json:"number"`
	Report           string     `json:"report"`
	StateID          uint       `json:"stateId"`
	NextStateIDs     []uint     `json:"nextStateIds"`
	AgentID          *uint      `json:"agentId"`
	AgentName        string     `json:"agentName"`
	ActualStartAt    *time.Time `json:"actualStartAt"`
	AccountedMinutes int        `json:"accountedMinutes"`
}

// A change's work orders are kept over HTTP on every database: added by whoever
// may work on changes and in the order of the change's plan; reported on by
// their agent, who may only see changes, and by nobody else who may only see
// them; given the moment they began as they go into progress; released when
// their agent is erased; and gone with their change.
func TestAChangesWorkOrdersAreKeptOverHTTP(t *testing.T) {
	t.Parallel()

	a, admin, agent := startWithWorker(t)
	state := func(name string) uint {
		return catalogValue(t, agent, "ITSM::ChangeManagement::WorkOrder::State", name)
	}
	plan := func(day int) time.Time { return time.Date(2026, 12, day, 8, 0, 0, 0, time.UTC) }

	var change changeResponse

	admin.must(admin.api(http.MethodPost, "/changes", map[string]any{"title": "Replace the core switch"}),
		http.StatusCreated, http.StatusOK).Data(t, &change)

	orders := path("/changes/", change.ID) + "/work-orders"

	if refused := agent.api(http.MethodPost, orders, map[string]any{"title": "Install"}); refused.Status !=
		http.StatusForbidden {
		t.Errorf("an agent adding a work order was answered %d, want 403", refused.Status)
	}

	idOf := func(c *client) uint {
		var me struct {
			User userResponse `json:"user"`
		}

		c.must(c.api(http.MethodGet, "/me", nil), http.StatusOK).Data(t, &me)

		return me.User.ID
	}

	olaf := a.signInAsAgent(admin, "Olaf", "olaf@example.com")

	var people struct {
		Items []struct {
			Name  string `json:"name"`
			Works bool   `json:"works"`
		} `json:"items"`
	}

	agent.must(agent.api(http.MethodGet, "/changes/people", nil), http.StatusOK).Data(t, &people)

	offered := map[string]bool{}
	for _, person := range people.Items {
		offered[person.Name] = person.Works
	}

	if works, listed := offered["Olaf"]; !listed || works {
		t.Errorf("the people a change can name are %v; want Olaf among them, seeing changes and not working on them",
			offered)
	}

	var review, install workOrderResponse

	admin.must(admin.api(http.MethodPost, orders, map[string]any{"title": "Review", "agentId": idOf(olaf),
		"plannedStartAt": plan(3), "plannedEndAt": plan(4)}), http.StatusCreated, http.StatusOK).Data(t, &review)
	admin.must(admin.api(http.MethodPost, orders, map[string]any{"title": "Install", "agentId": idOf(agent),
		"plannedStartAt": plan(1), "plannedEndAt": plan(2)}), http.StatusCreated, http.StatusOK).Data(t, &install)

	if install.Number != 1 || install.AgentName != "Wera" || install.StateID != state("created") ||
		len(install.NextStateIDs) != 2 {
		t.Errorf("the work order was added as %+v; want it first in the plan, Wera's, created, two ways on", install)
	}

	var listed struct {
		Items []workOrderResponse `json:"items"`
	}

	agent.must(agent.api(http.MethodGet, orders, nil), http.StatusOK).Data(t, &listed)

	if len(listed.Items) != 2 || listed.Items[0].ID != install.ID || listed.Items[1].Number != 2 {
		t.Errorf("the change's work orders read %+v, want the installation before the review", listed.Items)
	}

	var scheduled changeResponse

	agent.must(agent.api(http.MethodGet, path("/changes/", change.ID), nil), http.StatusOK).Data(t, &scheduled)

	if scheduled.WorkOrders != 2 || scheduled.PlannedStartAt == nil || !scheduled.PlannedStartAt.Equal(plan(1)) {
		t.Errorf("the change reads %+v; want two work orders and planned from the installation on", scheduled)
	}

	order := path("/work-orders/", install.ID)

	var reported workOrderResponse

	agent.must(agent.api(http.MethodPut, order, map[string]any{"report": "Racked.", "stateId": state("accepted"),
		"minutes": 30}), http.StatusOK).Data(t, &reported)

	if reported.Report != "Racked." || reported.StateID != state("accepted") || reported.AccountedMinutes != 30 {
		t.Errorf("the agent's report left %+v", reported)
	}

	for what, attempt := range map[string]struct {
		route string
		body  map[string]any
	}{
		"retitling the work order":         {order, map[string]any{"title": "Install twice"}},
		"reporting on somebody else's one": {path("/work-orders/", review.ID), map[string]any{"report": "Done."}},
	} {
		if refused := agent.api(http.MethodPut, attempt.route, attempt.body); refused.Status != http.StatusConflict ||
			refusalOf(t, refused).Code != "workOrderReportOnly" {
			t.Errorf("an agent %s was answered %d: %s", what, refused.Status, refused.Body)
		}
	}

	for _, step := range []string{"ready", "in-progress"} {
		agent.must(agent.api(http.MethodPut, order, map[string]any{"stateId": state(step)}), http.StatusOK).
			Data(t, &reported)
	}

	if reported.ActualStartAt == nil {
		t.Error("a work order gone into progress has not begun")
	}

	if refused := admin.api(http.MethodPut, order, map[string]any{"stateId": state("accepted")}); refused.Status !=
		http.StatusConflict || refusalOf(t, refused).Code != "workOrderStateNotAllowed" {
		t.Errorf("a work order in progress going back to accepted was answered %d: %s", refused.Status, refused.Body)
	}

	admin.must(admin.api(http.MethodDelete, path("/users/", idOf(olaf)), nil), http.StatusNoContent, http.StatusOK)

	var released workOrderResponse

	agent.must(agent.api(http.MethodGet, path("/work-orders/", review.ID), nil), http.StatusOK).Data(t, &released)

	if released.AgentID != nil {
		t.Errorf("after its agent was erased the work order reads %+v; want it given to nobody", released)
	}

	admin.must(admin.api(http.MethodDelete, path("/changes/", change.ID), nil), http.StatusNoContent, http.StatusOK)

	if gone := agent.api(http.MethodGet, order, nil); gone.Status != http.StatusNotFound {
		t.Errorf("the work order of a deleted change was answered %d, want 404", gone.Status)
	}
}
