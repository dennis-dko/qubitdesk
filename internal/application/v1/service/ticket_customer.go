package service

import (
	"context"
	"strconv"

	"github.com/dennis-dko/go-ticket-system/internal/domain/model"
	"github.com/dennis-dko/go-ticket-system/internal/domain/repository"
	"github.com/dennis-dko/go-ticket-system/internal/support/apperror"
)

// TicketCustomerRepositories are who tickets come from, handed over together
// because a ticket names both at once.
type TicketCustomerRepositories struct {
	Contacts      repository.ContactRepository
	Organisations repository.OrganisationRepository
}

// customerChoice is what a request says about who a ticket is for.
type customerChoice struct {
	contactID         *uint
	clearContact      bool
	organisationID    *uint
	clearOrganisation bool
}

// chooseCustomer applies what a request says about who a ticket is for, writing
// a line of history for each that changed where there is a history to write.
//
// A contact newly chosen has to exist and still be offered, and brings its own
// organisation along unless the request names one; an organisation newly chosen
// has to exist and still be offered. What the ticket already names stays,
// retired or not, as a state does.
func (s *TicketApplicationService) chooseCustomer(
	ctx context.Context,
	ticket *model.Ticket,
	choice customerChoice,
	change *history,
) error {
	if choice.clearContact && choice.contactID != nil {
		return apperror.InvalidFields("contactId")
	}

	if choice.clearOrganisation && choice.organisationID != nil {
		return apperror.InvalidFields("organisationId")
	}

	contact := ticket.ContactID
	organisation := ticket.OrganisationID

	switch {
	case choice.clearContact:
		contact = nil
	case choice.contactID != nil && !sameID(choice.contactID, ticket.ContactID):
		chosen, err := s.contacts.GetByID(ctx, *choice.contactID)
		if apperror.KindOf(err) == apperror.KindNotFound {
			return apperror.InvalidFields("contactId")
		}

		if err != nil {
			return err
		}

		if !chosen.Valid {
			return apperror.Conflictf("the contact %q is no longer offered", chosen.Name).
				WithCode("contactRetired", chosen.Name)
		}

		contact = &chosen.ID

		if choice.organisationID == nil && !choice.clearOrganisation {
			organisation = chosen.OrganisationID
		}
	}

	switch {
	case choice.clearOrganisation:
		organisation = nil
	case choice.organisationID != nil && !sameID(choice.organisationID, ticket.OrganisationID):
		chosen, err := s.organisations.GetByID(ctx, *choice.organisationID)
		if apperror.KindOf(err) == apperror.KindNotFound {
			return apperror.InvalidFields("organisationId")
		}

		if err != nil {
			return err
		}

		if !chosen.Valid {
			return apperror.Conflictf("the organisation %q is no longer offered", chosen.Name).
				WithCode("organisationRetired", chosen.Name)
		}

		organisation = &chosen.ID
	}

	if change != nil {
		change.add(model.HistoryContact, s.contactName(ctx, ticket.ContactID), s.contactName(ctx, contact))
		change.add(model.HistoryOrganisation, s.organisationName(ctx, ticket.OrganisationID),
			s.organisationName(ctx, organisation))
	}

	ticket.ContactID = contact
	ticket.OrganisationID = organisation

	return nil
}

// CustomerNames are the names of the contacts and organisations tickets name,
// for a reader who may not list the customers and still has to see who a ticket
// is for. One that is gone has none.
func (s *TicketApplicationService) CustomerNames(
	ctx context.Context,
	tickets []*model.Ticket,
) (map[uint]string, map[uint]string, error) {
	contacts := map[uint]string{}
	organisations := map[uint]string{}

	for _, ticket := range tickets {
		if ticket.ContactID != nil {
			if _, known := contacts[*ticket.ContactID]; !known {
				contact, err := s.contacts.GetByID(ctx, *ticket.ContactID)
				if err != nil && apperror.KindOf(err) != apperror.KindNotFound {
					return nil, nil, err
				}

				if contact != nil {
					contacts[contact.ID] = contact.Name
				}
			}
		}

		if ticket.OrganisationID != nil {
			if _, known := organisations[*ticket.OrganisationID]; !known {
				organisation, err := s.organisations.GetByID(ctx, *ticket.OrganisationID)
				if err != nil && apperror.KindOf(err) != apperror.KindNotFound {
					return nil, nil, err
				}

				if organisation != nil {
					organisations[organisation.ID] = organisation.Name
				}
			}
		}
	}

	return contacts, organisations, nil
}

// contactName is a contact's name for the history, or nothing for nobody.
func (s *TicketApplicationService) contactName(ctx context.Context, id *uint) string {
	if id == nil {
		return ""
	}

	contact, err := s.contacts.GetByID(ctx, *id)
	if err != nil {
		return "#" + strconv.FormatUint(uint64(*id), 10)
	}

	return contact.Name
}

// organisationName is an organisation's name for the history, or nothing for
// nobody.
func (s *TicketApplicationService) organisationName(ctx context.Context, id *uint) string {
	if id == nil {
		return ""
	}

	organisation, err := s.organisations.GetByID(ctx, *id)
	if err != nil {
		return "#" + strconv.FormatUint(uint64(*id), 10)
	}

	return organisation.Name
}
