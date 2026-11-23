package command

// TicketStateCommand creates a ticket state or changes one. Creating needs Name
// and Type; changing leaves a nil field as it was.
type TicketStateCommand struct {
	Name        *string
	Type        *string
	Description *string
	Valid       *bool

	// NextStateID is the state a waiting state moves a ticket on to, which a
	// state of the pending-auto type needs and no other may have.
	NextStateID *uint
}

// TicketPriorityCommand creates a ticket priority or changes one. Creating needs
// Name and Level; changing leaves a nil field as it was.
type TicketPriorityCommand struct {
	Name        *string
	Level       *int
	Description *string
	Valid       *bool
}

// TicketTypeCommand creates a ticket type or changes one. Creating needs Name;
// changing leaves a nil field as it was.
type TicketTypeCommand struct {
	Name        *string
	Description *string
	Valid       *bool
}
