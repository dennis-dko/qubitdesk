package command

// OrganisationCommand creates an organisation or changes one. Creating needs
// Name; changing leaves a nil field as it was. The number, the address, the
// website and the note are emptied by sending them empty, since an empty value is
// a value they can hold.
type OrganisationCommand struct {
	Name    *string
	Number  *string
	Address *string
	URL     *string
	Note    *string
	Valid   *bool
}

// ContactCommand creates a contact or changes one. Creating needs Name; changing
// leaves a nil field as it was. The organisation is the one field that has to be
// emptiable and cannot say so by being empty, so ClearOrganisation says it.
type ContactCommand struct {
	Name              *string
	Email             *string
	Phone             *string
	Note              *string
	OrganisationID    *uint
	ClearOrganisation bool
	Valid             *bool
	SeesOrganisation  *bool
}
