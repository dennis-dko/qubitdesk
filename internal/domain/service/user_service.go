package service

import (
	"context"

	"github.com/dennis-dko/go-ticket-system/internal/domain/model"
	"github.com/dennis-dko/go-ticket-system/internal/domain/repository"
	"github.com/dennis-dko/go-ticket-system/internal/support/apperror"
)

// UserDomainService holds the rules about moving an account between roles: the
// built-in administrator keeps a role that can still administer, and an account
// a contact signs in to the portal with is given none that does.
type UserDomainService struct {
	userRepository    repository.UserRepository
	roleRepository    repository.RoleRepository
	contactRepository repository.ContactRepository
}

// NewUserDomainService needs the roles to see what the one an account is moved
// to may do, and the contacts to see whether the account is one of theirs.
func NewUserDomainService(
	userRepo repository.UserRepository,
	roleRepo repository.RoleRepository,
	contactRepo repository.ContactRepository,
) *UserDomainService {
	return &UserDomainService{
		userRepository:    userRepo,
		roleRepository:    roleRepo,
		contactRepository: contactRepo,
	}
}

// RefuseAdministeringContact refuses a role holding rights over the
// installation for an account a contact signs in to the portal with.
//
// Such an account is reached from the customers' side: whoever keeps the
// contacts gives it its password, and so knows a password that opens it. That is
// harmless while the account can do nothing but follow the contact's tickets,
// and it is the installation once the account administers it. So the rights
// that amount to the installation are never handed to a contact's account, as
// they are never handed to the role every directory entry arrives with.
func RefuseAdministeringContact(
	ctx context.Context,
	contacts repository.ContactRepository,
	user *model.User,
	role *model.Role,
) error {
	if !role.Administers() {
		return nil
	}

	_, err := contacts.GetByUserID(ctx, user.ID)

	switch {
	case err == nil:
		return apperror.Conflictf("%q signs in to the portal as a contact and is not given %q, which "+
			"administers the installation", user.Email, role.Name).
			WithCode("portalAccountAdministers", user.Email, role.Name)
	case apperror.KindOf(err) == apperror.KindNotFound:
		return nil
	default:
		return err
	}
}

// AssignRoleToUser moves a user to the named role.
func (s *UserDomainService) AssignRoleToUser(
	ctx context.Context,
	userID uint,
	roleName string,
) (*model.User, error) {
	user, err := s.userRepository.GetByID(ctx, userID)
	if err != nil {
		return nil, err
	}

	role, err := s.roleRepository.GetByName(ctx, roleName)
	if err != nil {
		return nil, err
	}

	// The built-in administrator must keep a role that can still administer,
	// otherwise an installation can be locked out of its own user management.
	if user.IsSystem && !role.Has(model.PermRoleWrite) {
		return nil, apperror.Conflictf(
			"the built-in administrator cannot be moved to role %q, which lacks %q",
			role.Name, model.PermRoleWrite).
			WithCode("adminRoleMustAdminister", role.Name, model.PermRoleWrite)
	}

	if err := RefuseAdministeringContact(ctx, s.contactRepository, user, role); err != nil {
		return nil, err
	}

	user.RoleID = role.ID

	updatedUser, err := s.userRepository.Update(ctx, user)
	if err != nil {
		return nil, err
	}

	return updatedUser, nil
}
