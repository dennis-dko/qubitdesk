package command

import "github.com/dennis-dko/qubitdesk/internal/domain/model"

// CalendarCommand creates or changes a calendar. On a change an absent field
// stays as it was, and hours or free days that are sent replace the ones the
// calendar had - a list cannot be changed by leaving part of it out.
type CalendarCommand struct {
	Name         *string
	Description  *string
	Zone         *string
	HolidayPlace *string
	Hours        *[]model.WorkingHours
	FreeDays     *[]model.FreeDay
	Valid        *bool
}
