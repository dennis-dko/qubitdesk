package service_test

import (
	"context"
	"slices"
	"testing"

	"github.com/dennis-dko/qubitdesk/internal/application/v1/command"
	"github.com/dennis-dko/qubitdesk/internal/application/v1/service"
	"github.com/dennis-dko/qubitdesk/internal/infrastructure/persistence/memory"
)

// customerFixture is the customer service over empty in-memory tables.
type customerFixture struct {
	customers     *service.CustomerApplicationService
	organisations *memory.OrganisationRepository
	contacts      *memory.ContactRepository
}

func newCustomerFixture() customerFixture {
	f := customerFixture{
		organisations: memory.NewOrganisationRepository(),
		contacts:      memory.NewContactRepository(),
	}

	f.customers = service.NewCustomerApplicationService(f.organisations, f.contacts)

	return f
}

// An organisation contacts or tickets belong to is refused deletion with how
// many, and can be retired instead; once nothing belongs to it, it can go.
func TestAnOrganisationInUseIsRetiredRatherThanDeleted(t *testing.T) {
	ctx := context.Background()
	f := newCustomerFixture()

	acme, err := f.customers.CreateOrganisation(ctx, command.OrganisationCommand{Name: ptr("Acme")})
	if err != nil {
		t.Fatalf("create an organisation: %v", err)
	}

	f.organisations.UseIn(acme.ID, 2, 5)

	detail := refusal(t, f.customers.DeleteOrganisation(ctx, acme.ID), "organisationInUse")
	if !slices.Equal(detail.Values, []any{"Acme", 2, 5}) {
		t.Errorf("the refusal says %v, want the name, the contacts and the tickets", detail.Values)
	}

	retired, err := f.customers.UpdateOrganisation(ctx, acme.ID, command.OrganisationCommand{Valid: ptr(false)})
	if err != nil || retired.Valid || retired.Name != "Acme" {
		t.Fatalf("retiring the organisation answered %+v, %v", retired, err)
	}

	f.organisations.UseIn(acme.ID, 0, 0)

	if err := f.customers.DeleteOrganisation(ctx, acme.ID); err != nil {
		t.Fatalf("deleting an organisation nothing belongs to: %v", err)
	}
}

// An organisation's website is shown as a link, so only an http or https address
// is one: anything else would be a link that runs what somebody typed.
func TestAnOrganisationsWebsiteIsAWebAddress(t *testing.T) {
	ctx := context.Background()
	f := newCustomerFixture()

	for _, website := range []string{"javascript:alert(1)", "data:text/html,<b>x</b>", "ftp://files.example.com", "example.com"} {
		_, err := f.customers.CreateOrganisation(ctx, command.OrganisationCommand{Name: ptr("Linked"), URL: ptr(website)})
		invalidFields(t, err, "url")
	}

	linked, err := f.customers.CreateOrganisation(ctx, command.OrganisationCommand{
		Name: ptr("Linked"), URL: ptr("https://example.com/about"),
	})
	if err != nil || linked.URL != "https://example.com/about" {
		t.Fatalf("an https website answered %+v, %v", linked, err)
	}
}

// A contact's address is checked and kept in one spelling where it is given, is
// unique, and may be absent for any number of contacts.
func TestAContactsAddressIsUniqueWhereItIsGiven(t *testing.T) {
	ctx := context.Background()
	f := newCustomerFixture()

	_, err := f.customers.CreateContact(ctx, command.ContactCommand{Name: ptr("Ann"), Email: ptr("not an address")})
	invalidFields(t, err, "email")

	ann, err := f.customers.CreateContact(ctx, command.ContactCommand{Name: ptr("Ann"), Email: ptr("  Ann@Example.com ")})
	if err != nil || ann.Email != "ann@example.com" {
		t.Fatalf("a contact with an address answered %+v, %v; want the address in one spelling", ann, err)
	}

	_, err = f.customers.CreateContact(ctx, command.ContactCommand{Name: ptr("Another Ann"), Email: ptr("ann@example.com")})
	refusedWith(t, err, "contactEmailTaken")

	for _, name := range []string{"Bob", "Carl"} {
		if _, err := f.customers.CreateContact(ctx, command.ContactCommand{Name: ptr(name)}); err != nil {
			t.Errorf("a second contact with no address was refused: %v", err)
		}
	}
}

// Deleting a contact tickets are for asks first, with how many, and once
// confirmed the contact goes; with no tickets it simply goes.
func TestDeletingAContactTicketsAreForAsksFirst(t *testing.T) {
	ctx := context.Background()
	f := newCustomerFixture()

	ann, _ := f.customers.CreateContact(ctx, command.ContactCommand{Name: ptr("Ann")})
	f.contacts.UseInTickets(ann.ID, 3)

	detail := refusal(t, f.customers.DeleteContact(ctx, ann.ID, false), "contactDeletionNeedsConfirming")
	if !slices.Equal(detail.Values, []any{"Ann", 3}) {
		t.Errorf("the refusal says %v, want the name and how many tickets", detail.Values)
	}

	if err := f.customers.DeleteContact(ctx, ann.ID, true); err != nil {
		t.Fatalf("a confirmed deletion: %v", err)
	}

	bob, _ := f.customers.CreateContact(ctx, command.ContactCommand{Name: ptr("Bob")})
	if err := f.customers.DeleteContact(ctx, bob.ID, false); err != nil {
		t.Fatalf("deleting a contact no ticket is for: %v", err)
	}
}

// A contact is put with an organisation still offered; one it already belongs
// to stays, retired or not, and saying so out loud takes it off.
func TestAContactBelongsToAnOrganisationStillOffered(t *testing.T) {
	ctx := context.Background()
	f := newCustomerFixture()

	acme, _ := f.customers.CreateOrganisation(ctx, command.OrganisationCommand{Name: ptr("Acme")})
	ann, err := f.customers.CreateContact(ctx, command.ContactCommand{Name: ptr("Ann"), OrganisationID: &acme.ID})
	if err != nil || ann.OrganisationID == nil {
		t.Fatalf("a contact of Acme answered %+v, %v", ann, err)
	}

	if _, err := f.customers.UpdateOrganisation(ctx, acme.ID, command.OrganisationCommand{Valid: ptr(false)}); err != nil {
		t.Fatalf("retire Acme: %v", err)
	}

	_, err = f.customers.CreateContact(ctx, command.ContactCommand{Name: ptr("Bob"), OrganisationID: &acme.ID})
	refusedWith(t, err, "organisationRetired")

	kept, err := f.customers.UpdateContact(ctx, ann.ID, command.ContactCommand{Phone: ptr("+49 30 123")})
	if err != nil || kept.OrganisationID == nil || *kept.OrganisationID != acme.ID {
		t.Fatalf("changing Ann's phone answered %+v, %v; want her kept at Acme", kept, err)
	}

	missing := uint(999)
	_, err = f.customers.UpdateContact(ctx, ann.ID, command.ContactCommand{OrganisationID: &missing})
	invalidFields(t, err, "organisationId")

	alone, err := f.customers.UpdateContact(ctx, ann.ID, command.ContactCommand{ClearOrganisation: true})
	if err != nil || alone.OrganisationID != nil {
		t.Fatalf("taking Ann's organisation off answered %+v, %v", alone, err)
	}
}

// A listing of customers is one page and the true total, with the same bound as
// a page of tickets.
func TestAListingOfCustomersIsOnePage(t *testing.T) {
	ctx := context.Background()
	f := newCustomerFixture()

	for _, name := range []string{"Acme", "Bolt", "Cogs"} {
		if _, err := f.customers.CreateOrganisation(ctx, command.OrganisationCommand{Name: ptr(name)}); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}

	page, total, err := f.customers.ListOrganisations(ctx, service.CustomerListFilter{Limit: 2})
	if err != nil || total != 3 || len(page) != 2 || page[0].Name != "Acme" {
		t.Fatalf("the first page holds %d of %d, %v; want two of three, by name", len(page), total, err)
	}

	found, total, _ := f.customers.ListOrganisations(ctx, service.CustomerListFilter{Search: "olt"})
	if total != 1 || found[0].Name != "Bolt" {
		t.Errorf("searching for olt found %d", total)
	}

	_, _, err = f.customers.ListContacts(ctx, service.CustomerListFilter{Limit: service.MaxTicketPage + 1})
	invalidFields(t, err, "limit")
}
