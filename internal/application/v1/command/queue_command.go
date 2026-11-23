package command

// QueueCommand creates a queue or changes one. Creating needs Name; changing
// leaves a nil field as it was.
//
// The parent is the one field that has to be emptiable, so its third state is
// said out loud: ClearParent moves the queue to the top, and a nil ParentID
// means only that the request said nothing about it.
type QueueCommand struct {
	Name          *string
	ParentID      *uint
	ClearParent   bool
	Description   *string
	FollowUp      *string
	UnlockMinutes *int
	Valid         *bool
	Portal        *bool

	// ServiceLevelID names the service level a new ticket in the queue starts
	// with; ClearServiceLevel takes it off, for the reason ClearParent is said.
	ServiceLevelID    *uint
	ClearServiceLevel bool

	// Access replaces the roles that reach the queue when it is not nil; an empty
	// list leaves the queue to whoever holds queues:write.
	Access *[]QueueAccessCommand
}

// QueueAccessCommand gives one role a level of access to a queue.
type QueueAccessCommand struct {
	RoleID uint
	Level  string
}
