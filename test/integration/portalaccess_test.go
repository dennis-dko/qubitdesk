//go:build integration

package integration

import (
	"net/http"
	"testing"
)

type portalContact struct {
	ID               uint   `json:"id"`
	Email            string `json:"email"`
	HasPortalAccount bool   `json:"hasPortalAccount"`
	SeesOrganisation bool   `json:"seesOrganisation"`
}

// A contact is given the portal by an agent, on every database: the account it
// makes signs in with the contact's address, holds the customer role and has to
// replace its password first; it is given no role that administers, by either
// way an account is moved or by a right ticked on the role it holds; it follows
// the contact's address; a new password ends the sessions it has open; the
// administration log names the contact; and taking the portal away, or deleting
// the contact, erases the account.
func TestAContactIsGivenThePortalOverHTTP(t *testing.T) {
	t.Parallel()

	a, admin, agent := startWithWorker(t)

	var ada portalContact

	agent.must(agent.api(http.MethodPost, "/contacts", map[string]any{
		"name": "Ada", "email": "ada@example.com", "seesOrganisation": true,
	}), http.StatusCreated, http.StatusOK).Data(t, &ada)

	if !ada.SeesOrganisation || ada.HasPortalAccount {
		t.Fatalf("the new contact reads %+v, want following the organisation and without the portal", ada)
	}

	agent.must(agent.api(http.MethodPut, path("/contacts/", ada.ID, "/portal-access"),
		map[string]any{"password": "first-password-1"}), http.StatusOK).Data(t, &ada)

	if !ada.HasPortalAccount {
		t.Fatalf("the contact given the portal reads %+v", ada)
	}

	customer := a.newClient()
	account := customer.signIn("ada@example.com", "first-password-1")

	if account.Role != "customer" || !account.MustChangePassword {
		t.Errorf("the contact's account is %+v, want the customer role with an initial password", account)
	}

	customer.must(customer.api(http.MethodPut, "/me/password", map[string]string{
		"currentPassword": "first-password-1", "newPassword": "adas-own-password",
	}), http.StatusOK)

	for _, attempt := range []struct {
		name string
		r    response
	}{
		{"the account form", admin.api(http.MethodPut, path("/users/", account.ID), map[string]any{"role": "admin"})},
		{"the role route", admin.api(http.MethodPut, path("/users/", account.ID, "/role"), map[string]any{"role": "admin"})},
	} {
		if attempt.r.Status != http.StatusConflict || refusalOf(t, attempt.r).Code != "portalAccountAdministers" {
			t.Errorf("moving the contact's account to admin through %s answered %d: %s", attempt.name,
				attempt.r.Status, attempt.r.Body)
		}
	}

	var plus struct {
		ID uint `json:"id"`
	}

	admin.must(admin.api(http.MethodPost, "/roles", map[string]any{
		"name": "customer plus", "permissions": []string{"portal:use"},
	}), http.StatusCreated, http.StatusOK).Data(t, &plus)
	admin.must(admin.api(http.MethodPut, path("/users/", account.ID, "/role"), map[string]any{"role": "customer plus"}),
		http.StatusOK)

	ticked := admin.api(http.MethodPut, path("/roles/", plus.ID), map[string]any{
		"permissions": []string{"portal:use", "settings:manage"},
	})
	if ticked.Status != http.StatusConflict || refusalOf(t, ticked).Code != "roleHeldByPortalAccounts" {
		t.Errorf("ticking settings:manage on the role a contact's account holds answered %d: %s", ticked.Status,
			ticked.Body)
	}

	var log adminChanges

	admin.must(admin.api(http.MethodGet, "/admin/changes", nil), http.StatusOK).Data(t, &log)

	given := false

	for _, entry := range log.Entries {
		if entry.Action == "contact.portalGiven" && entry.Target != nil && entry.Target.ID == ada.ID &&
			entry.Target.Name == "Ada" {
			given = true
		}
	}

	if !given {
		t.Errorf("the administration log does not name the contact given the portal: %+v", log.Entries)
	}

	agent.must(agent.api(http.MethodPut, path("/contacts/", ada.ID), map[string]any{"email": "countess@example.com"}),
		http.StatusOK)

	countess := a.newClient()
	countess.signIn("countess@example.com", "adas-own-password")

	agent.must(agent.api(http.MethodPut, path("/contacts/", ada.ID, "/portal-access"),
		map[string]any{"password": "reset-password-3"}), http.StatusOK)

	if ended := countess.api(http.MethodGet, "/me", nil); ended.Status != http.StatusUnauthorized {
		t.Errorf("a session of the contact's account outlived a new password: %d %s", ended.Status, ended.Body)
	}

	a.newClient().signIn("countess@example.com", "reset-password-3")

	agent.must(agent.api(http.MethodDelete, path("/contacts/", ada.ID, "/portal-access"), nil),
		http.StatusOK, http.StatusNoContent)

	if refused := a.newClient().api(http.MethodPost, "/auth/login", map[string]string{
		"email": "countess@example.com", "password": "reset-password-3",
	}); refused.Status != http.StatusUnauthorized {
		t.Errorf("the account taken away still signs in: %d %s", refused.Status, refused.Body)
	}

	agent.must(agent.api(http.MethodGet, path("/contacts/", ada.ID), nil), http.StatusOK).Data(t, &ada)

	if ada.HasPortalAccount {
		t.Errorf("the contact still has the portal after it was taken away: %+v", ada)
	}

	agent.must(agent.api(http.MethodPut, path("/contacts/", ada.ID, "/portal-access"),
		map[string]any{"password": "second-password-2"}), http.StatusOK)
	agent.must(agent.api(http.MethodDelete, path("/contacts/", ada.ID), nil), http.StatusOK, http.StatusNoContent)

	if refused := a.newClient().api(http.MethodPost, "/auth/login", map[string]string{
		"email": "countess@example.com", "password": "second-password-2",
	}); refused.Status != http.StatusUnauthorized {
		t.Errorf("the account of a deleted contact still signs in: %d %s", refused.Status, refused.Body)
	}
}
