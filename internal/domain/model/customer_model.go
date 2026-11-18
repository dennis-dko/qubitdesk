package model

import "time"

// Organisation is a company tickets come from.
type Organisation struct {
	ID   uint
	Name string

	// Number is the organisation's customer number, unique where it is given and
	// empty where the installation keeps none.
	Number string

	Address string

	// URL is the organisation's website, shown as a link and therefore only ever
	// an http or https address.
	URL string

	Note string

	// Valid says whether the organisation is offered for a ticket or a contact
	// from now on; one that is not keeps what it already has.
	Valid bool

	CreatedAt time.Time
}

// Contact is a person tickets come from.
type Contact struct {
	ID uint

	// OrganisationID is the company the contact belongs to, which a ticket for
	// the contact is for unless it says otherwise.
	OrganisationID *uint

	Name string

	// Email is unique where it is given: an arriving message finds its sender by
	// it, and an address that were two contacts would have no answer to whose.
	Email string

	Phone string
	Note  string

	// Valid means what it means for an organisation.
	Valid bool

	// UserID is the account the contact signs in to the portal with, made for
	// the contact when somebody keeping the customers gives them the portal; the
	// erasure of that account takes it off.
	UserID *uint

	// SeesOrganisation lets the contact follow their organisation's tickets in
	// the portal as well as their own. Off unless somebody says otherwise, as
	// KIX18's customer role ships the organisation's line switched off: a
	// colleague's ticket is not news to everybody at the same company.
	SeesOrganisation bool

	CreatedAt time.Time
}

// Follows reports whether the portal shows the contact the ticket: one for
// them, or one for their organisation where they see its tickets.
func (c *Contact) Follows(ticket *Ticket) bool {
	if ticket.ContactID != nil && *ticket.ContactID == c.ID {
		return true
	}

	return c.SeesOrganisation && c.OrganisationID != nil && ticket.OrganisationID != nil &&
		*ticket.OrganisationID == *c.OrganisationID
}
