package command

// NotificationDefinitionCommand creates or changes a notification. On a change
// an absent field stays as it was, and Events, Criteria, Recipients and
// Messages, where sent, take the place of what the notification had.
type NotificationDefinitionCommand struct {
	Name        *string
	Description *string
	Valid       *bool
	Events      *[]string
	Criteria    *JobCriteriaCommand
	Recipients  *[]string
	OncePerDay  *bool
	Optional    *bool
	Messages    *[]NotificationMessageCommand
}

// NotificationMessageCommand is what a notification says in one language, as
// it was sent.
type NotificationMessageCommand struct {
	Language string
	Subject  string
	Body     string
}
