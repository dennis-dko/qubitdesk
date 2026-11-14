package model

// TicketType is a kind of ticket an installation tells apart - nothing behaves
// differently by type, it is a word to sort, filter and count by. Valid and
// IsSystem mean what they mean for a TicketState.
type TicketType struct {
	ID          uint
	Name        string
	Description string
	Valid       bool
	IsSystem    bool
}

// TypeUnclassified is the type a ticket has while nobody has said what it is,
// by the name the migration seeds it under. It is marked is_system, so it can be
// neither renamed nor deleted, and the name goes on finding it.
const TypeUnclassified = "unclassified"
