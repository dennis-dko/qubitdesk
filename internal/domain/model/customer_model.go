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

	// UserID is the account the contact signs in to the portal with. Nothing sets
	// it before the portal does; the erasure of that account takes it off.
	UserID *uint

	CreatedAt time.Time
}
