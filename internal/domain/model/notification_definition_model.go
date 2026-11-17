package model

// NotificationDefinition is what the installation tells the agents around a
// ticket when something happens to it - KIX18's ticket notifications: the events
// it is sent on, the tickets it is about, who is told, and what each is told in
// their language. A retired one tells nobody.
type NotificationDefinition struct {
	ID          uint
	Name        string
	Description string
	Valid       bool
	Events      []TicketEvent

	// Criteria are the tickets it is about; naming nothing is every ticket, which
	// a notification may be where a job may not, because telling somebody about a
	// ticket changes nothing in it.
	Criteria TicketCriteria

	Recipients []NotificationRecipient

	// OncePerDay tells each recipient about one ticket once a day at most.
	OncePerDay bool

	// Optional lets an agent switch it off for themselves, as KIX18's
	// VisibleForAgent does; one that is not optional reaches everybody it names.
	Optional bool

	Messages []NotificationMessage
}

// NotificationRecipient is who a notification is sent to, named as KIX18 names
// its recipients: the agent who owns the ticket, the one responsible for it,
// those watching it, those who chose its queue as one of theirs, and those whose
// role works in or reads its queue.
type NotificationRecipient string

// The recipients a notification can name.
const (
	RecipientOwner       NotificationRecipient = "AgentOwner"
	RecipientResponsible NotificationRecipient = "AgentResponsible"
	RecipientWatcher     NotificationRecipient = "AgentWatcher"
	RecipientMyQueues    NotificationRecipient = "AgentMyQueues"
	RecipientWriters     NotificationRecipient = "AgentWritePermissions"
	RecipientReaders     NotificationRecipient = "AgentReadPermissions"
)

// NotificationRecipients are every recipient a notification can name, in the
// order a form offers them.
func NotificationRecipients() []NotificationRecipient {
	return []NotificationRecipient{
		RecipientOwner, RecipientResponsible, RecipientWatcher, RecipientMyQueues, RecipientWriters, RecipientReaders,
	}
}

// NotificationMessage is what a notification says to a recipient who reads one
// language: a subject and a text, each of which may carry KIX18's placeholders
// for the ticket it is about.
type NotificationMessage struct {
	Language string
	Subject  string
	Body     string
}

// MaxNotificationBodyLength bounds what a notification says. Chosen rather than
// derived, as MaxMacroTextLength is: a notification is a paragraph pointing at a
// ticket, not the ticket.
const MaxNotificationBodyLength = 10000
