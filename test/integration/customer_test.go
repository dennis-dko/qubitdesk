//go:build integration

package integration

import (
	"net/http"
	"strconv"
	"testing"
)

type organisationResponse struct {
	ID     uint   `json:"id"`
	Name   string `json:"name"`
	Number string `json:"number"`
}

type contactResponse struct {
	ID             uint   `json:"id"`
	Name           string `json:"name"`
	Email          string `json:"email"`
	Phone          string `json:"phone"`
	OrganisationID *uint  `json:"organisationId"`
}

func organisationPath(id uint) string { return "/organisations/" + strconv.FormatUint(uint64(id), 10) }

func contactPath(id uint) string { return "/contacts/" + strconv.FormatUint(uint64(id), 10) }

// Organisations and their contacts are made, found, changed and deleted over
// HTTP; an organisation a contact belongs to is not deleted from under it, and a
// website that is not a web address is refused.
func TestCustomersAreAdministeredOverHTTP(t *testing.T) {
	t.Parallel()

	a := start(t)
	admin := a.signInAsAdmin("a-much-better-password")

	var acme organisationResponse

	admin.must(admin.api(http.MethodPost, "/organisations", map[string]any{
		"name": "Acme", "number": "K-100", "url": "https://acme.example",
	}), http.StatusCreated).Data(t, &acme)

	if unsafe := admin.api(http.MethodPost, "/organisations", map[string]any{
		"name": "Unsafe", "url": "javascript:alert(1)",
	}); unsafe.Status != http.StatusBadRequest {
		t.Errorf("an organisation with a javascript: website was answered %d, want 400", unsafe.Status)
	}

	var ann contactResponse

	admin.must(admin.api(http.MethodPost, "/contacts", map[string]any{
		"name": "Ann Anders", "email": "Ann@Example.com", "organisationId": acme.ID,
	}), http.StatusCreated).Data(t, &ann)

	if ann.Email != "ann@example.com" || ann.OrganisationID == nil || *ann.OrganisationID != acme.ID {
		t.Errorf("the new contact reads as %+v", ann)
	}

	var ofAcme struct {
		Items      []contactResponse `json:"items"`
		TotalCount uint              `json:"totalCount"`
	}

	admin.must(admin.api(http.MethodGet, "/contacts?organisation="+strconv.FormatUint(uint64(acme.ID), 10), nil),
		http.StatusOK).Data(t, &ofAcme)

	if ofAcme.TotalCount != 1 || ofAcme.Items[0].ID != ann.ID {
		t.Errorf("Acme's contacts read as %+v", ofAcme)
	}

	var changed contactResponse

	admin.must(admin.api(http.MethodPut, contactPath(ann.ID), map[string]any{"phone": "+49 30 123"}),
		http.StatusOK).Data(t, &changed)

	if changed.Phone != "+49 30 123" || changed.OrganisationID == nil {
		t.Errorf("changing Ann's phone left her as %+v", changed)
	}

	inUse := admin.api(http.MethodDelete, organisationPath(acme.ID), nil)
	if inUse.Status != http.StatusConflict || errorCode(t, inUse) != "organisationInUse" {
		t.Errorf("deleting Acme with Ann at it was answered %d %s, want 409 organisationInUse", inUse.Status, inUse.Body)
	}

	admin.must(admin.api(http.MethodDelete, contactPath(ann.ID), nil), http.StatusNoContent)
	admin.must(admin.api(http.MethodDelete, organisationPath(acme.ID), nil), http.StatusNoContent)
}

// An agent works customers, which a ticket needs; a customer's own account, which
// reaches the portal and nothing else, does not.
func TestAnAgentWorksCustomersAndACustomerAccountDoesNot(t *testing.T) {
	t.Parallel()

	a := start(t)
	admin := a.signInAsAdmin("a-much-better-password")
	agent := a.signInAsAgent(admin, "Agent", "agent@example.com")

	agent.must(agent.api(http.MethodPost, "/organisations", map[string]any{"name": "Bolt"}), http.StatusCreated)

	admin.must(admin.api(http.MethodPost, "/users", map[string]any{
		"name": "Customer", "email": "customer@example.com", "role": "customer", "password": "another-password-1",
	}), http.StatusCreated, http.StatusOK)

	customer := a.newClient()
	customer.signIn("customer@example.com", "another-password-1")

	if refused := customer.api(http.MethodGet, "/contacts", nil); refused.Status != http.StatusForbidden {
		t.Errorf("a customer's account listing contacts was answered %d, want 403", refused.Status)
	}
}
