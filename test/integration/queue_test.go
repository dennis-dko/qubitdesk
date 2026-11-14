//go:build integration

package integration

import (
	"net/http"
	"strconv"
	"testing"
)

type queueResponse struct {
	ID       uint   `json:"id"`
	Name     string `json:"name"`
	ParentID *uint  `json:"parentId"`
	Reach    string `json:"reach"`
	Access   []struct {
		RoleID uint   `json:"roleId"`
		Level  string `json:"level"`
	} `json:"access"`
}

type queueList struct {
	Items []queueResponse `json:"items"`
}

func queuePath(id uint) string { return "/queues/" + strconv.FormatUint(uint64(id), 10) }

// An agent sees the queues their role reaches and nothing of the others: a queue
// they do not reach is not in the list and not found by its id - never refused,
// because the difference between the two is what they may not learn. They are
// shown their own line of a queue's access, and not who else reaches it.
func TestAnAgentSeesOnlyTheQueuesTheirRoleReaches(t *testing.T) {
	t.Parallel()

	a := start(t)
	admin := a.signInAsAdmin("a-much-better-password")
	agent := a.signInAsAgent(admin, "Agent", "agent@example.com")

	var second queueResponse

	admin.must(admin.api(http.MethodPost, "/queues", map[string]any{"name": "Second level"}),
		http.StatusCreated).Data(t, &second)

	var seen queueList

	agent.must(agent.api(http.MethodGet, "/queues", nil), http.StatusOK).Data(t, &seen)

	if len(seen.Items) != 1 || seen.Items[0].Name != "Service Desk" || seen.Items[0].Reach != "write" {
		t.Fatalf("an agent sees %+v; want only the Service Desk, to work in", seen.Items)
	}

	if len(seen.Items[0].Access) != 1 {
		t.Errorf("an agent is shown %d lines of the desk's access, want only their own", len(seen.Items[0].Access))
	}

	if hidden := agent.api(http.MethodGet, queuePath(second.ID), nil); hidden.Status != http.StatusNotFound {
		t.Errorf("a queue the agent does not reach was answered %d, want 404", hidden.Status)
	}

	agentRole := seen.Items[0].Access[0].RoleID

	admin.must(admin.api(http.MethodPut, queuePath(second.ID), map[string]any{
		"access": []map[string]any{{"roleId": agentRole, "level": "read"}},
	}), http.StatusOK)

	var reached queueResponse

	agent.must(agent.api(http.MethodGet, queuePath(second.ID), nil), http.StatusOK).Data(t, &reached)

	if reached.Reach != "read" {
		t.Errorf("once the agents may read the queue, an agent reaches it as %q", reached.Reach)
	}

	if refused := agent.api(http.MethodPost, "/queues", map[string]any{"name": "Mine"}); refused.Status != http.StatusForbidden {
		t.Errorf("an agent creating a queue was answered %d, want 403", refused.Status)
	}
}

// Queues form a tree that cannot loop, a queue other queues sit under is not
// deleted from under them, and every change is in the administration log.
func TestTheQueuesAreAdministeredAndLogged(t *testing.T) {
	t.Parallel()

	a := start(t)
	admin := a.signInAsAdmin("a-much-better-password")

	var queues queueList

	admin.must(admin.api(http.MethodGet, "/queues", nil), http.StatusOK).Data(t, &queues)

	if len(queues.Items) != 1 {
		t.Fatalf("a fresh installation has %d queues, want the Service Desk", len(queues.Items))
	}

	desk := queues.Items[0]

	var child queueResponse

	admin.must(admin.api(http.MethodPost, "/queues", map[string]any{
		"name": "Monitoring", "parentId": desk.ID, "followUp": "new-ticket", "unlockMinutes": 60,
	}), http.StatusCreated).Data(t, &child)

	if child.ParentID == nil || *child.ParentID != desk.ID {
		t.Errorf("the new queue sits under %v, want the desk", child.ParentID)
	}

	looped := admin.api(http.MethodPut, queuePath(desk.ID), map[string]any{"parentId": child.ID})
	if looped.Status != http.StatusBadRequest || errorCode(t, looped) != "queueUnderItself" {
		t.Errorf("putting the desk under its own child was answered %d %s, want 400 queueUnderItself",
			looped.Status, looped.Body)
	}

	parent := admin.api(http.MethodDelete, queuePath(desk.ID), nil)
	if parent.Status != http.StatusConflict || errorCode(t, parent) != "queueHasChildren" {
		t.Errorf("deleting the desk with a queue under it was answered %d %s, want 409 queueHasChildren",
			parent.Status, parent.Body)
	}

	var moved queueResponse

	admin.must(admin.api(http.MethodPut, queuePath(child.ID), map[string]any{"clearParent": true}),
		http.StatusOK).Data(t, &moved)

	if moved.ParentID != nil {
		t.Errorf("clearing the parent left the queue under %d", *moved.ParentID)
	}

	var log adminChanges

	admin.must(admin.api(http.MethodGet, "/admin/changes", nil), http.StatusOK).Data(t, &log)

	recorded := map[string]bool{}
	for _, entry := range log.Entries {
		recorded[entry.Action] = true
	}

	if !recorded["queue.created"] || !recorded["queue.updated"] {
		t.Errorf("the administration log holds %v, want the queue made and changed", recorded)
	}

	admin.must(admin.api(http.MethodDelete, queuePath(child.ID), nil), http.StatusNoContent)
}
