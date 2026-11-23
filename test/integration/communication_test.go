//go:build integration

package integration

import (
	"net/http"
	"testing"
)

// Communication is set up with a right of its own, as KIX18 keeps it in an area
// of its own: an account holding communication:write alone makes notifications
// and answer templates and reads what their criteria and queues can name, and
// the right to set the queues up no longer does either.
func TestCommunicationIsSetUpWithARightOfItsOwn(t *testing.T) {
	t.Parallel()

	a, admin, _ := startWithWorker(t)
	signedIn := map[string]*client{}

	for role, permission := range map[string]string{"communicator": "communication:write", "queuekeeper": "queues:write"} {
		admin.must(admin.api(http.MethodPost, "/roles", map[string]any{"name": role,
			"permissions": []string{permission}}), http.StatusCreated, http.StatusOK)
		admin.must(admin.api(http.MethodPost, "/users", map[string]any{"name": role, "email": role + "@example.com",
			"role": role, "password": role + "-password-1"}), http.StatusCreated, http.StatusOK)

		signedIn[role] = a.newClient()
		signedIn[role].signIn(role+"@example.com", role+"-password-1")
	}

	made := map[string]map[string]any{
		"/notification-definitions": {"name": "Opened", "events": []string{"TicketCreate"},
			"recipients": []string{"AgentOwner"},
			"messages":   []map[string]any{{"language": "en", "subject": "New", "body": "Opened."}}},
		"/response-templates": {"name": "Thanks", "body": "Thank you."},
	}

	communicator, keeper := signedIn["communicator"], signedIn["queuekeeper"]

	for path, body := range made {
		communicator.must(communicator.api(http.MethodPost, path, body), http.StatusCreated, http.StatusOK)

		if refused := keeper.api(http.MethodPost, path, body); refused.Status != http.StatusForbidden {
			t.Errorf("the right to set the queues up wrote %s with %d, want 403", path, refused.Status)
		}
	}

	communicator.must(communicator.api(http.MethodGet, "/automation/choices", nil), http.StatusOK)
}
