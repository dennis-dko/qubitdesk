package model

import "time"

// Notification is what one agent was told about one ticket: the words a
// notification had for them in their language when it was sent - a
// notification changed or deleted later does not change what somebody was
// told - and whether they have read it.
type Notification struct {
	ID           uint
	UserID       uint
	TicketID     uint
	DefinitionID uint
	Subject      string
	Body         string
	CreatedAt    time.Time
	ReadAt       *time.Time
}

// MaxNotificationsListed bounds how many of an agent's notifications a listing
// hands out, the latest first. Chosen rather than derived: what somebody was
// told longer ago than the last hundred times is in the tickets' history.
const MaxNotificationsListed = 100
