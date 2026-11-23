package command

// ExecPlanCommand creates or changes an execution plan. On a change an absent
// field stays as it was; Times are minutes after midnight, read off the wall in
// the installation's zone.
type ExecPlanCommand struct {
	Name        *string
	Description *string
	Valid       *bool
	Type        *string
	Weekdays    *[]int
	Times       *[]int
	Events      *[]string
}
