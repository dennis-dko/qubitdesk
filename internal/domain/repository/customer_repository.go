package repository

import (
	"context"

	"github.com/dennis-dko/qubitdesk/internal/domain/model"
)

// OrganisationRepository keeps the companies tickets come from.
type OrganisationRepository interface {
	// List answers one page of the organisations a filter allows, by name, and
	// how many it allows altogether.
	List(ctx context.Context, filter CustomerFilter) ([]*model.Organisation, uint, error)

	GetByID(ctx context.Context, id uint) (*model.Organisation, error)
	Save(ctx context.Context, organisation *model.Organisation) (*model.Organisation, error)
	Update(ctx context.Context, organisation *model.Organisation) (*model.Organisation, error)
	Delete(ctx context.Context, id uint) error

	// CountUses reports how many contacts and how many tickets belong to the
	// organisation, so deleting one in use is refused rather than leaving them
	// pointing at nothing.
	CountUses(ctx context.Context, id uint) (contacts, tickets int, err error)
}

// ContactRepository keeps the people tickets come from.
type ContactRepository interface {
	// List answers one page of the contacts a filter allows, by name, and how
	// many it allows altogether.
	List(ctx context.Context, filter CustomerFilter) ([]*model.Contact, uint, error)

	GetByID(ctx context.Context, id uint) (*model.Contact, error)

	// GetByUserID is the contact that signs in to the portal with the account,
	// not found where none does.
	GetByUserID(ctx context.Context, userID uint) (*model.Contact, error)

	Save(ctx context.Context, contact *model.Contact) (*model.Contact, error)

	// Update writes what a contact says about the person; it leaves the
	// account alone, which only LinkAccount sets and the erasure takes off.
	Update(ctx context.Context, contact *model.Contact) (*model.Contact, error)

	// LinkAccount makes the account the one the contact signs in to the portal
	// with, while the contact has none; false where it already had one by then.
	LinkAccount(ctx context.Context, contactID, userID uint) (bool, error)

	// CountLinkedWithRole reports how many accounts that contacts sign in with
	// hold the role.
	CountLinkedWithRole(ctx context.Context, roleID uint) (int, error)

	// Delete removes the contact and takes it off the tickets it is on rather
	// than taking them along, in one transaction: a ticket is the organisation's
	// record, as it stays when an account goes.
	Delete(ctx context.Context, id uint) error

	// CountTickets reports how many tickets are for the contact.
	CountTickets(ctx context.Context, id uint) (int, error)
}

// CustomerFilter says which organisations or contacts a listing allows, and
// which page of them.
type CustomerFilter struct {
	// Search narrows the listing to a name - or a number, or an address - holding
	// the words.
	Search string

	// OrganisationID narrows a listing of contacts to one organisation's.
	OrganisationID *uint

	// ValidOnly leaves out what is no longer offered.
	ValidOnly bool

	// Limit and Page choose the page: Limit entries, the first page being 1.
	Limit int
	Page  int
}
