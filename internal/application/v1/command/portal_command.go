package command

// PortalTicketCommand opens a ticket from the portal: what it is about, what the
// customer writes, and the queue, which only has to be named where the portal
// offers more than one.
type PortalTicketCommand struct {
	Title   *string
	Body    *string
	QueueID *uint
}

// PortalAnswerCommand is a customer's answer to one of their tickets. Its subject
// is the ticket's title where none is given.
type PortalAnswerCommand struct {
	Subject *string
	Body    *string
}
