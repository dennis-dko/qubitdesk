package model

// Queue is where tickets are sorted to - a team's inbox, in KIX18's words - and
// what it decides for the tickets in it.
type Queue struct {
	ID   uint
	Name string

	// ParentID puts the queue under another, for finding it in a tree. The tree
	// decides nothing else: a queue's access is its own, not its parent's, so
	// moving a queue never widens who reaches it.
	ParentID *uint

	Description string
	FollowUp    FollowUp

	// UnlockMinutes is how long a ticket may stay locked to its owner before it
	// is free for somebody else, counted from the lock or from the last answer
	// the customer was given under it, as KIX18 counts it; zero never frees it.
	UnlockMinutes int

	// ServiceLevelID is the service level a ticket opened in the queue starts
	// with, where it names one.
	ServiceLevelID *uint

	// Portal says whether customers open tickets in the queue from the portal,
	// as the queues OTRS offers a customer to choose from: a team's inbox is not
	// every team's door to the customers.
	Portal bool

	// Valid says whether a ticket may still be put into the queue. One that is
	// not keeps the tickets already in it.
	Valid bool

	// Access is which roles reach the queue and how far. A role with no entry
	// does not reach it at all.
	Access []QueueAccess
}

// QueueAccess is how far one role reaches into a queue.
type QueueAccess struct {
	RoleID uint
	Level  AccessLevel
}

// AccessLevel is how far a role reaches into a queue: to read its tickets, or to
// work in it - create tickets there and change them - which includes reading.
type AccessLevel string

const (
	// AccessRead reaches a queue's tickets to read them.
	AccessRead AccessLevel = "read"

	// AccessWrite reaches a queue's tickets to create, change and read them.
	AccessWrite AccessLevel = "write"
)

// AccessLevels lists the levels a role can be given on a queue.
func AccessLevels() []AccessLevel {
	return []AccessLevel{AccessRead, AccessWrite}
}

// FollowUp is what an answer to a closed ticket in the queue does, as KIX18's
// queues decide it.
type FollowUp string

const (
	// FollowUpReopen opens the ticket again, which is what somebody answering
	// expects.
	FollowUpReopen FollowUp = "reopen"

	// FollowUpReject refuses the answer and tells its sender so.
	FollowUpReject FollowUp = "reject"

	// FollowUpNewTicket opens a new ticket for the answer and leaves the closed
	// one as it is.
	FollowUpNewTicket FollowUp = "new-ticket"
)

// FollowUps lists what an answer to a closed ticket can do.
func FollowUps() []FollowUp {
	return []FollowUp{FollowUpReopen, FollowUpReject, FollowUpNewTicket}
}

// MaxUnlockMinutes bounds a queue's unlock time. Chosen rather than derived: a
// week is longer than anybody leaves a ticket locked on purpose, and nothing good
// comes of a number large enough to mean never - which zero already says.
const MaxUnlockMinutes = 7 * 24 * 60
