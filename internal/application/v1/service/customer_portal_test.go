package service_test

import (
	"context"
	"testing"

	"github.com/dennis-dko/go-ticket-system/internal/application/v1/command"
	"github.com/dennis-dko/go-ticket-system/internal/application/v1/service"
	"github.com/dennis-dko/go-ticket-system/internal/domain/model"
	"github.com/dennis-dko/go-ticket-system/internal/infrastructure/persistence/memory"
	"github.com/dennis-dko/go-ticket-system/internal/support/apperror"
)

// portalFixture is the customer service able to give contacts the portal, over
// the accounts and roles of the account fixture.
type portalFixture struct {
	*fixture

	customers *service.CustomerApplicationService
}

func newPortalFixture(t *testing.T) portalFixture {
	t.Helper()

	f := newFixture(t)
	customers := service.NewCustomerApplicationService(memory.NewOrganisationRepository(), f.contactRepo).
		WithPortalAccounts(service.PortalAccounts{
			Users: f.userRepo, Roles: f.roleRepo,
			Purger: &memoryPurger{users: f.userRepo, owned: f.owned, contacts: f.contactRepo},
		})

	return portalFixture{fixture: f, customers: customers}
}

func (f portalFixture) contact(t *testing.T, name, email string) *model.Contact {
	t.Helper()

	contact, err := f.customers.CreateContact(context.Background(), command.ContactCommand{
		Name: ptr(name), Email: ptr(email),
	})
	if err != nil {
		t.Fatalf("create the contact %s: %v", name, err)
	}

	return contact
}

// account is the account the contact signs in to the portal with.
func (f portalFixture) account(t *testing.T, contactID uint) *model.User {
	t.Helper()

	contact, err := f.customers.GetContact(context.Background(), contactID)
	if err != nil {
		t.Fatalf("read the contact: %v", err)
	}

	if contact.UserID == nil {
		t.Fatalf("%s has no account", contact.Name)
	}

	account, err := f.userRepo.GetByID(context.Background(), *contact.UserID)
	if err != nil {
		t.Fatalf("read the contact's account: %v", err)
	}

	return account
}

// A contact given the portal signs in with an account of their own: their name
// and address, the shipped customer role and nothing else, and a password that
// has to be replaced at the first sign-in. Giving it again gives the same
// account a new password, as initial as the first.
func TestAContactIsGivenThePortalWithAnAccountOfTheirOwn(t *testing.T) {
	ctx := context.Background()
	f := newPortalFixture(t)
	ada := f.contact(t, "Ada Lovelace", "ada@example.com")

	given, err := f.customers.GivePortalAccess(ctx, ada.ID, "first-password-1")
	if err != nil {
		t.Fatalf("give the portal: %v", err)
	}

	if given.UserID == nil {
		t.Fatal("the contact was answered without an account")
	}

	account := f.account(t, ada.ID)
	role, err := f.roleRepo.GetByID(ctx, account.RoleID)
	if err != nil {
		t.Fatalf("read the account's role: %v", err)
	}

	if account.Name != "Ada Lovelace" || account.Email != "ada@example.com" || role.Name != model.RoleCustomer ||
		!account.MustChangePassword {
		t.Errorf("the account is %q <%s> in %q, must change its password %v; want the contact's name and address "+
			"in the customer role with an initial password", account.Name, account.Email, role.Name,
			account.MustChangePassword)
	}

	if _, err := f.sessions.Login(ctx, "ada@example.com", "first-password-1", ""); err != nil {
		t.Errorf("the password given does not sign the account in: %v", err)
	}

	if _, err := f.customers.GivePortalAccess(ctx, ada.ID, "second-password-2"); err != nil {
		t.Fatalf("give a new password: %v", err)
	}

	if again := f.account(t, ada.ID); again.ID != account.ID || !again.MustChangePassword {
		t.Errorf("giving the portal again made account %d (must change %v); want account %d with an initial password",
			again.ID, again.MustChangePassword, account.ID)
	}

	if _, err := f.sessions.Login(ctx, "ada@example.com", "second-password-2", ""); err != nil {
		t.Errorf("the new password does not sign the account in: %v", err)
	}
}

// The portal is not given to a contact who is no longer offered, nor to one
// with no address to sign in with, nor without a password the accounts accept;
// and an address an account already signs in with is not given a second one,
// without an account left behind by the attempt.
func TestThePortalIsGivenOnlyWhereAnAccountCanBeMade(t *testing.T) {
	ctx := context.Background()
	f := newPortalFixture(t)

	retired := f.contact(t, "Retired", "retired@example.com")
	if _, err := f.customers.UpdateContact(ctx, retired.ID, command.ContactCommand{Valid: ptr(false)}); err != nil {
		t.Fatalf("retire the contact: %v", err)
	}

	_, err := f.customers.GivePortalAccess(ctx, retired.ID, "a-good-password")
	refusedWith(t, err, "contactRetired")

	nameless := f.contact(t, "No Address", "")
	_, err = f.customers.GivePortalAccess(ctx, nameless.ID, "a-good-password")
	refusedWith(t, err, "portalNeedsAnAddress")

	ada := f.contact(t, "Ada", "ada@example.com")
	_, err = f.customers.GivePortalAccess(ctx, ada.ID, "")
	requireKind(t, err, apperror.KindInvalid)

	_, err = f.customers.GivePortalAccess(ctx, ada.ID, "short")
	refusedWith(t, err, "passwordTooShort")

	twin := f.contact(t, "Dennis's Twin", "dennis@example.com")
	before, _ := f.userRepo.GetAll(ctx)

	_, err = f.customers.GivePortalAccess(ctx, twin.ID, "a-good-password")
	refusedWith(t, err, "emailTaken")

	if after, _ := f.userRepo.GetAll(ctx); len(after) != len(before) {
		t.Errorf("a refused attempt left %d accounts where there were %d", len(after), len(before))
	}
}

// Taking the portal away erases the account and leaves the contact without
// one; taking it from a contact who has none changes nothing.
func TestTakingThePortalAwayErasesTheAccount(t *testing.T) {
	ctx := context.Background()
	f := newPortalFixture(t)
	ada := f.contact(t, "Ada", "ada@example.com")

	if _, err := f.customers.GivePortalAccess(ctx, ada.ID, "a-good-password"); err != nil {
		t.Fatalf("give the portal: %v", err)
	}

	account := f.account(t, ada.ID)

	if err := f.customers.TakePortalAccess(ctx, ada.ID); err != nil {
		t.Fatalf("take the portal away: %v", err)
	}

	if _, err := f.userRepo.GetByID(ctx, account.ID); apperror.KindOf(err) != apperror.KindNotFound {
		t.Errorf("the account is still there after the portal was taken away (%v)", err)
	}

	if contact, _ := f.customers.GetContact(ctx, ada.ID); contact.UserID != nil {
		t.Errorf("the contact still names account %d", *contact.UserID)
	}

	if err := f.customers.TakePortalAccess(ctx, ada.ID); err != nil {
		t.Errorf("taking it from a contact without one answered %v", err)
	}
}

// A contact's account follows the contact's name and address, because it signs
// in with that address: emptying the address is refused while the contact has
// the portal, and so is one another account signs in with - with neither the
// contact nor the account changed.
func TestAContactsAccountFollowsTheContact(t *testing.T) {
	ctx := context.Background()
	f := newPortalFixture(t)
	ada := f.contact(t, "Ada", "ada@example.com")

	if _, err := f.customers.GivePortalAccess(ctx, ada.ID, "a-good-password"); err != nil {
		t.Fatalf("give the portal: %v", err)
	}

	if _, err := f.customers.UpdateContact(ctx, ada.ID, command.ContactCommand{
		Name: ptr("Ada King"), Email: ptr("countess@example.com"),
	}); err != nil {
		t.Fatalf("rename the contact: %v", err)
	}

	if account := f.account(t, ada.ID); account.Name != "Ada King" || account.Email != "countess@example.com" {
		t.Errorf("the account is %q <%s>; want it to follow the contact", account.Name, account.Email)
	}

	_, err := f.customers.UpdateContact(ctx, ada.ID, command.ContactCommand{Email: ptr("")})
	refusedWith(t, err, "portalNeedsAnAddress")

	_, err = f.customers.UpdateContact(ctx, ada.ID, command.ContactCommand{
		Name: ptr("Somebody Else"), Email: ptr("dennis@example.com"),
	})
	refusedWith(t, err, "emailTaken")

	contact, _ := f.customers.GetContact(ctx, ada.ID)
	account := f.account(t, ada.ID)

	if contact.Name != "Ada King" || contact.Email != "countess@example.com" || account.Email != "countess@example.com" {
		t.Errorf("a refused change left the contact %q <%s> and the account <%s>", contact.Name, contact.Email,
			account.Email)
	}
}

// Deleting a contact erases the account they signed in to the portal with,
// which would otherwise reach nothing and be found by nobody.
func TestDeletingAContactErasesTheirPortalAccount(t *testing.T) {
	ctx := context.Background()
	f := newPortalFixture(t)
	ada := f.contact(t, "Ada", "ada@example.com")

	if _, err := f.customers.GivePortalAccess(ctx, ada.ID, "a-good-password"); err != nil {
		t.Fatalf("give the portal: %v", err)
	}

	account := f.account(t, ada.ID)

	if err := f.customers.DeleteContact(ctx, ada.ID, false); err != nil {
		t.Fatalf("delete the contact: %v", err)
	}

	if _, err := f.userRepo.GetByID(ctx, account.ID); apperror.KindOf(err) != apperror.KindNotFound {
		t.Errorf("the contact's account outlived the contact (%v)", err)
	}
}

// An account linked to a contact that works on the staff side as well is the
// administrators' and not the contact keeper's: it is given no password and not
// erased from the customers' side, it keeps its own name and address, and it
// stays when the contact goes.
func TestAContactsAccountOnTheStaffSideIsLeftToWhoeverAdministersAccounts(t *testing.T) {
	ctx := context.Background()
	f := newPortalFixture(t)
	ada := f.contact(t, "Ada", "ada@example.com")

	if _, err := f.customers.GivePortalAccess(ctx, ada.ID, "a-good-password"); err != nil {
		t.Fatalf("give the portal: %v", err)
	}

	account := f.account(t, ada.ID)
	if _, err := f.userDomain.AssignRoleToUser(ctx, account.ID, model.RoleAgent); err != nil {
		t.Fatalf("make the account an agent: %v", err)
	}

	_, err := f.customers.GivePortalAccess(ctx, ada.ID, "another-password")
	refusedWith(t, err, "portalAccountOnTheStaffSide")

	refusedWith(t, f.customers.TakePortalAccess(ctx, ada.ID), "portalAccountOnTheStaffSide")

	if _, err := f.customers.UpdateContact(ctx, ada.ID, command.ContactCommand{Name: ptr("Ada King")}); err != nil {
		t.Fatalf("rename the contact: %v", err)
	}

	if kept := f.account(t, ada.ID); kept.Name != "Ada" {
		t.Errorf("the agent's account was renamed %q from the customers' side", kept.Name)
	}

	if err := f.customers.DeleteContact(ctx, ada.ID, false); err != nil {
		t.Fatalf("delete the contact: %v", err)
	}

	if _, err := f.userRepo.GetByID(ctx, account.ID); err != nil {
		t.Errorf("the agent's account went with the contact: %v", err)
	}
}

// An account a contact signs in with is given no role that administers the
// installation, whichever way it would get there: moved to one, by either of
// the two ways an account is moved, or holding one that has such a right ticked
// on it. A right that does not administer is ticked as ever.
func TestAContactsAccountIsGivenNoRoleThatAdministers(t *testing.T) {
	ctx := context.Background()
	f := newPortalFixture(t)
	ada := f.contact(t, "Ada", "ada@example.com")

	if _, err := f.customers.GivePortalAccess(ctx, ada.ID, "a-good-password"); err != nil {
		t.Fatalf("give the portal: %v", err)
	}

	account := f.account(t, ada.ID)

	_, err := f.users.UpdateUser(ctx, command.UpdateUserCommand{ID: account.ID, Role: ptr(model.RoleAdmin)})
	refusedWith(t, err, "portalAccountAdministers")

	_, err = f.userDomain.AssignRoleToUser(ctx, account.ID, model.RoleAdmin)
	refusedWith(t, err, "portalAccountAdministers")

	plus, err := f.roles.CreateRole(ctx, "customer plus", "", []string{model.PermPortalUse})
	if err != nil {
		t.Fatalf("make a role: %v", err)
	}

	if _, err := f.userDomain.AssignRoleToUser(ctx, account.ID, plus.Name); err != nil {
		t.Fatalf("move the account to a role that does not administer: %v", err)
	}

	_, err = f.roles.UpdateRole(ctx, plus.ID, nil, nil, []string{model.PermPortalUse, model.PermUserWrite})
	refusedWith(t, err, "roleHeldByPortalAccounts")

	if _, err := f.roles.UpdateRole(ctx, plus.ID, nil, nil,
		[]string{model.PermPortalUse, model.PermFAQRead}); err != nil {
		t.Errorf("a right that does not administer was refused: %v", err)
	}

	if _, err := f.users.UpdateUser(ctx, command.UpdateUserCommand{
		ID: f.userID, Role: ptr(model.RoleAdmin),
	}); err != nil {
		t.Errorf("an account no contact signs in with was refused the admin role: %v", err)
	}
}

// Whether a contact follows their organisation's tickets is kept as said, and
// off unless somebody says otherwise.
func TestAContactFollowsTheirOrganisationOnlyWhenSaid(t *testing.T) {
	ctx := context.Background()
	f := newPortalFixture(t)
	ada := f.contact(t, "Ada", "ada@example.com")

	if ada.SeesOrganisation {
		t.Error("a new contact follows their organisation's tickets without anybody saying so")
	}

	seeing, err := f.customers.UpdateContact(ctx, ada.ID, command.ContactCommand{SeesOrganisation: ptr(true)})
	if err != nil || !seeing.SeesOrganisation {
		t.Fatalf("saying so was answered %v, %v", seeing, err)
	}

	untouched, err := f.customers.UpdateContact(ctx, ada.ID, command.ContactCommand{Phone: ptr("123")})
	if err != nil || !untouched.SeesOrganisation {
		t.Errorf("a change that did not mention it answered %v, %v", untouched, err)
	}
}
