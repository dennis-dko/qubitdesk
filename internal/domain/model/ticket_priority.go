package model

// TicketPriority is how urgent a ticket is. Valid and IsSystem mean what they
// mean for a TicketState.
type TicketPriority struct {
	ID   uint
	Name string

	// Level orders the priorities, 1 the most urgent, because a name says
	// nothing about where it belongs.
	Level int

	Description string
	Valid       bool
	IsSystem    bool
}

// MaxPriorityLevel bounds a priority's level. Chosen rather than derived: two
// digits leave room to put a priority between any two of the five that ship.
const MaxPriorityLevel = 99

// PriorityNormal is the priority a ticket starts with when nobody says
// otherwise, by the name the migration seeds it under. It is marked is_system,
// so it can be neither renamed nor deleted, and the name goes on finding it.
const PriorityNormal = "normal"
