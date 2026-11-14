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

// A ticket is for a contact and their organisation, says so by name, is found by
// either, and outlives the contact: deleting a contact tickets are for asks
// first, and once confirmed the ticket stays without the contact.
func TestATicketIsForACustomerAndOutlivesTheContact(t *testing.T) {
	t.Parallel()

	a := start(t)
	admin := a.signInAsAdmin("a-much-better-password")
	desk := theDesk(t, admin)

	var acme organisationResponse

	admin.must(admin.api(http.MethodPost, "/organisations", map[string]any{"name": "Acme"}),
		http.StatusCreated).Data(t, &acme)

	var ann contactResponse

	admin.must(admin.api(http.MethodPost, "/contacts", map[string]any{"name": "Ann", "organisationId": acme.ID}),
		http.StatusCreated).Data(t, &ann)

	var ticket struct {
		ID               uint   `json:"id"`
		ContactID        *uint  `json:"contactId"`
		ContactName      string `json:"contactName"`
		OrganisationName string `json:"organisationName"`
	}

	admin.must(admin.api(http.MethodPost, "/tickets", map[string]any{
		"title": "Printer", "queueId": desk, "body": "It jams.", "contactId": ann.ID,
	}), http.StatusCreated).Data(t, &ticket)

	if ticket.ContactName != "Ann" || ticket.OrganisationName != "Acme" {
		t.Errorf("a ticket for Ann reads as %+v; want it naming Ann and Acme", ticket)
	}

	var hers ticketList

	admin.must(admin.api(http.MethodGet, "/tickets?contact="+strconv.FormatUint(uint64(ann.ID), 10), nil),
		http.StatusOK).Data(t, &hers)

	if hers.TotalCount != 1 {
		t.Errorf("Ann's tickets number %d, want the one", hers.TotalCount)
	}

	asked := admin.api(http.MethodDelete, contactPath(ann.ID), nil)
	if asked.Status != http.StatusConflict || errorCode(t, asked) != "contactDeletionNeedsConfirming" {
		t.Fatalf("deleting Ann with a ticket for her was answered %d %s, want 409 contactDeletionNeedsConfirming",
			asked.Status, asked.Body)
	}

	admin.must(admin.api(http.MethodDelete, contactPath(ann.ID)+"?confirm=true", nil), http.StatusNoContent)

	admin.must(admin.api(http.MethodGet, ticketPath(ticket.ID), nil), http.StatusOK).Data(t, &ticket)

	if ticket.ContactID != nil || ticket.OrganisationName != "Acme" {
		t.Errorf("after Ann was deleted her ticket reads as %+v; want it kept for Acme without a contact", ticket)
	}
}
