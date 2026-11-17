package command

// JobCommand creates or changes a job. On a change an absent field stays as it
// was; Criteria, MacroIDs and PlanIDs, where sent, take the place of what the
// job had, because each is one list whose order or whole is the point.
type JobCommand struct {
	Name        *string
	Description *string
	Valid       *bool
	Criteria    *JobCriteriaCommand
	MacroIDs    *[]uint
	PlanIDs     *[]uint
}

// JobCriteriaCommand is what a ticket has to be for the job to take it, as it
// was sent.
type JobCriteriaCommand struct {
	QueueIDs    []uint
	StateIDs    []uint
	PriorityIDs []uint
	TypeIDs     []uint
	Locked      *bool
}
