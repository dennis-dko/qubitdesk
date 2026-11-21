package service

import (
	"context"

	"github.com/dennis-dko/qubitdesk/internal/domain/model"
	"github.com/dennis-dko/qubitdesk/internal/domain/repository"
	"github.com/dennis-dko/qubitdesk/internal/support/apperror"
)

// roleForArrival is the role an account the directory brings in starts with.
//
// One function for both ways an account arrives, a first sign-in and a
// synchronisation, because each used to decide this for itself and both decided
// it the same wrong way: the everyday role fixed at start-up, whatever the
// directory settings said.
//
// The directory's default role where it names a role that exists and does not
// administer the installation; fallback otherwise. A role that has gone since it
// was chosen is not a reason to turn somebody away at the door, and a role that
// administers is never handed out this way - sign-in refuses to let the directory
// claim an administering account, and a default that administers would give every
// entry anybody writes to the directory the installation itself. The settings
// refuse to store one; this catches a role given those rights afterwards.
func roleForArrival(
	ctx context.Context,
	roles repository.RoleRepository,
	configured, fallback string,
) (*model.Role, error) {
	if configured != "" && configured != fallback {
		role, err := roles.GetByName(ctx, configured)

		switch {
		case err == nil && !role.Administers():
			return role, nil
		case err != nil && apperror.KindOf(err) != apperror.KindNotFound:
			return nil, err
		}
	}

	return roles.GetByName(ctx, fallback)
}
