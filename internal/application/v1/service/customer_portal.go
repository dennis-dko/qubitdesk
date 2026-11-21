package service

import (
	"context"
	"errors"
	"slices"

	"github.com/dennis-dko/qubitdesk/internal/domain/model"
	"github.com/dennis-dko/qubitdesk/internal/domain/repository"
	"github.com/dennis-dko/qubitdesk/internal/support/apperror"
	"github.com/dennis-dko/qubitdesk/internal/support/security"
)

// PortalAccounts is what giving a contact the portal takes: the accounts and
// the roles they hold, and the erasure that takes an account away again.
type PortalAccounts struct {
	Users  repository.UserRepository
	Roles  repository.RoleRepository
	Purger UserPurger
}

// WithPortalAccounts lets the service give contacts the portal, take it away
// again, and keep a contact's account in step with the contact. Without it the
// contacts are kept and nobody is given the portal.
func (s *CustomerApplicationService) WithPortalAccounts(accounts PortalAccounts) *CustomerApplicationService {
	s.accounts = &accounts

	return s
}

// GivePortalAccess makes the contact an account under their name and address,
// holding the shipped customer role, or gives the account they already have a
// new password - in both cases one to be replaced at the first sign-in, so that
// what somebody keeping the customers typed does not stay standing as a password
// a second person knows.
//
// Whoever keeps the customers may do this without administering accounts, and
// that is only safe because of what the account can be: the customer role is the
// portal and nothing else, and no installation can change it. So the role is not
// a choice, and an account that holds anything of the staff side is refused
// rather than given a password by somebody who could then sign in as it.
func (s *CustomerApplicationService) GivePortalAccess(
	ctx context.Context,
	id uint,
	password string,
) (*model.Contact, error) {
	if s.accounts == nil {
		return nil, apperror.Internal(errors.New("the portal's accounts are not wired"))
	}

	contact, err := s.GetContact(ctx, id)
	if err != nil {
		return nil, err
	}

	if password == "" {
		return nil, apperror.InvalidFields("password")
	}

	if !contact.Valid {
		return nil, apperror.Conflictf("the contact %q is no longer offered", contact.Name).
			WithCode("contactRetired", contact.Name)
	}

	hash, err := security.HashPassword(password)
	if err != nil {
		return nil, passwordError(err)
	}

	if contact.UserID != nil {
		account, err := s.portalAccount(ctx, contact, "directoryAccountReadOnly")
		if err != nil {
			return nil, err
		}

		account.PasswordHash = hash
		account.MustChangePassword = true

		if _, err := s.accounts.Users.Update(ctx, account); err != nil {
			return nil, err
		}

		return contact, nil
	}

	if contact.Email == "" {
		return nil, portalNeedsAnAddress(contact)
	}

	role, err := s.accounts.Roles.GetByName(ctx, model.RoleCustomer)
	if err != nil {
		return nil, err
	}

	account, err := s.accounts.Users.Save(ctx, &model.User{
		Name: contact.Name, Email: contact.Email, RoleID: role.ID,
		PasswordHash: hash, MustChangePassword: true,
	})
	if err != nil {
		return nil, err
	}

	// The account is made before it is linked, and the link holds only while
	// the contact has none: somebody else giving the same contact the portal in
	// the same moment wins, and the account made here goes again rather than
	// standing beside theirs reaching nothing.
	linked, err := s.contacts.LinkAccount(ctx, contact.ID, account.ID)
	if err != nil || !linked {
		if purgeErr := s.accounts.Purger.PurgeUser(ctx, account.ID); purgeErr != nil {
			return nil, errors.Join(err, purgeErr)
		}

		if err != nil {
			return nil, err
		}

		return nil, apperror.Conflictf("%q was given the portal by somebody else in the same moment", contact.Name).
			WithCode("portalAccessExists", contact.Name)
	}

	return s.GetContact(ctx, id)
}

// TakePortalAccess erases the account the contact signs in with, together with
// everything that is the account's own; the tickets stay the contact's. A
// contact without one is left as it is.
func (s *CustomerApplicationService) TakePortalAccess(ctx context.Context, id uint) error {
	if s.accounts == nil {
		return apperror.Internal(errors.New("the portal's accounts are not wired"))
	}

	contact, err := s.GetContact(ctx, id)
	if err != nil {
		return err
	}

	if contact.UserID == nil {
		return nil
	}

	account, err := s.portalAccount(ctx, contact, "directoryAccountUndeletable")
	if err != nil {
		return err
	}

	return s.accounts.Purger.PurgeUser(ctx, account.ID)
}

// portalAccount is the account the contact signs in with, refused where it is
// more than the contact's: one whose role reaches anything of the staff side is
// changed where accounts are administered, and one the directory owns is
// changed in the directory - directoryCode says which refusal that is.
func (s *CustomerApplicationService) portalAccount(
	ctx context.Context,
	contact *model.Contact,
	directoryCode string,
) (*model.User, error) {
	account, err := s.accounts.Users.GetByID(ctx, *contact.UserID)
	if err != nil {
		return nil, err
	}

	role, err := s.accounts.Roles.GetByID(ctx, account.RoleID)
	if err != nil {
		return nil, err
	}

	if !onlyThePortal(role) {
		return nil, apperror.Conflictf("%q works on the staff side as well and is changed where accounts are",
			account.Email).WithCode("portalAccountOnTheStaffSide", account.Email)
	}

	if account.IsExternal {
		return nil, apperror.Conflictf("%q comes from the directory and is changed there", account.Email).
			WithCode(directoryCode, account.Email)
	}

	return account, nil
}

// accountInStep is the contact's account with the name and the address the
// contact now has, where the contact's change touched either and the account is
// the contact's alone; nil where nothing about the account has to change. The
// account signs in with that address, so taking it away is refused, and so is
// an address another account already signs in with - asked before anything is
// written, so that neither table is changed without the other.
//
// An account that works on the staff side as well keeps its own name and
// address: it is administered where accounts are, by somebody who may.
func (s *CustomerApplicationService) accountInStep(
	ctx context.Context,
	was, contact *model.Contact,
) (*model.User, error) {
	if s.accounts == nil || contact.UserID == nil || (was.Name == contact.Name && was.Email == contact.Email) {
		return nil, nil
	}

	account, err := s.accounts.Users.GetByID(ctx, *contact.UserID)
	if err != nil {
		return nil, err
	}

	role, err := s.accounts.Roles.GetByID(ctx, account.RoleID)
	if err != nil {
		return nil, err
	}

	if !onlyThePortal(role) || account.IsExternal {
		return nil, nil
	}

	if contact.Email == "" {
		return nil, portalNeedsAnAddress(contact)
	}

	if contact.Email != account.Email {
		other, err := s.accounts.Users.GetByEmail(ctx, contact.Email)

		switch {
		case err == nil && other.ID != account.ID:
			return nil, apperror.Conflictf("an account with the address %q already exists", contact.Email).
				WithCode("emailTaken", contact.Email)
		case err != nil && apperror.KindOf(err) != apperror.KindNotFound:
			return nil, err
		}
	}

	account.Name = contact.Name
	account.Email = contact.Email

	return account, nil
}

// eraseWithContact erases the account a contact being deleted signs in with,
// where the account is the contact's alone: without the contact it would reach
// nothing, and an account nobody can find to remove is one that is never
// removed. One that works on the staff side as well stays, and so does one the
// directory owns, which the next synchronisation would make again.
func (s *CustomerApplicationService) eraseWithContact(ctx context.Context, contact *model.Contact) error {
	if s.accounts == nil || contact.UserID == nil {
		return nil
	}

	account, err := s.accounts.Users.GetByID(ctx, *contact.UserID)
	if apperror.KindOf(err) == apperror.KindNotFound {
		return nil
	}

	if err != nil {
		return err
	}

	role, err := s.accounts.Roles.GetByID(ctx, account.RoleID)
	if err != nil {
		return err
	}

	if !onlyThePortal(role) || account.IsExternal {
		return nil
	}

	return s.accounts.Purger.PurgeUser(ctx, account.ID)
}

// onlyThePortal reports whether a role reaches nothing of the staff side - the
// portal at most. An account holding one is its contact's alone, and whoever
// keeps the contacts keeps it.
func onlyThePortal(role *model.Role) bool {
	return !slices.ContainsFunc(role.Permissions, func(right string) bool { return right != model.PermPortalUse })
}

func portalNeedsAnAddress(contact *model.Contact) error {
	return apperror.Conflictf("%q has no address to sign in to the portal with", contact.Name).
		WithCode("portalNeedsAnAddress", contact.Name)
}
