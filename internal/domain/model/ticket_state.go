package model

// StateType is what a ticket state does. The states themselves are rows an
// installation names and adds to; the types are constants, because each of them
// is something the code does: a new ticket waits for its first answer, a pending
// one comes back at a moment, a closed one owes nobody anything. A type that
// existed only in the database would be a word with no behaviour behind it.
type StateType string

const (
	// StateTypeNew is a ticket nobody has worked on yet.
	StateTypeNew StateType = "new"

	// StateTypeOpen is a ticket somebody is working on.
	StateTypeOpen StateType = "open"

	// StateTypePendingReminder waits for something - the customer, a delivery, a
	// date - and reminds its owner when the moment it waits until has come.
	StateTypePendingReminder StateType = "pending-reminder"

	// StateTypePendingAuto waits until a moment and then moves by itself - into
	// the closed state, as an installation ships - unless its customer writes
	// first.
	StateTypePendingAuto StateType = "pending-auto"

	// StateTypeClosed is a ticket that is done.
	StateTypeClosed StateType = "closed"

	// StateTypeRemoved is a ticket that should never have been one, kept rather
	// than deleted because deleting cannot be taken back.
	StateTypeRemoved StateType = "removed"

	// StateTypeMerged is a ticket folded into another, which is where it is
	// carried on. It is reached by merging and by nothing else.
	StateTypeMerged StateType = "merged"
)

// StateTypes lists every type a state can have, in the order a ticket usually
// passes through them. A state of any other type is refused, because nothing
// would know what to do with a ticket in it.
func StateTypes() []StateType {
	return []StateType{
		StateTypeNew, StateTypeOpen,
		StateTypePendingReminder, StateTypePendingAuto,
		StateTypeClosed, StateTypeRemoved, StateTypeMerged,
	}
}

// FinishedStateTypes lists the types of state a ticket is in once nobody has to
// do anything about it any more.
func FinishedStateTypes() []StateType {
	return []StateType{StateTypeClosed, StateTypeRemoved, StateTypeMerged}
}

// TicketState is one of the states a ticket can be in: a name, and the type that
// says what the code does with a ticket in it.
type TicketState struct {
	ID          uint
	Name        string
	Type        StateType
	Description string

	// Valid says whether the state is offered for a ticket from now on. One that
	// is not stays on the tickets already in it, which is why a state in use is
	// retired rather than deleted.
	Valid bool

	// IsSystem marks a state the code moves tickets into by itself. It stays as
	// it shipped: not renamed, retyped, retired or deleted.
	IsSystem bool
}

// The states the code moves tickets into by itself, by the names the migration
// seeds them under: a ticket starts new, an answer from its customer opens it
// again, a pending close ends closed, and a merge leaves it merged. Each is
// marked is_system, so it can be neither renamed nor deleted, and the name goes
// on finding it.
const (
	StateNew    = "new"
	StateOpen   = "open"
	StateClosed = "closed"
	StateMerged = "merged"
)
