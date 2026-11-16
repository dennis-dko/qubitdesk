package command

// ServiceLevelCommand creates or changes a service level. On a change an absent
// field stays as it was; ClearCalendar takes the calendar off, so that the
// level counts around the clock, because an id sent as nothing cannot be told
// apart from one that was not sent.
type ServiceLevelCommand struct {
	Name                 *string
	Description          *string
	CalendarID           *uint
	ClearCalendar        bool
	FirstResponseMinutes *int
	UpdateMinutes        *int
	SolutionMinutes      *int
	Valid                *bool
}
