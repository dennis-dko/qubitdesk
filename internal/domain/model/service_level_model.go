package model

// ServiceLevel is what a ticket is owed: how long until the first answer, how
// long between answers, and how long until it is solved, each in minutes of
// service time counted in a calendar - around the clock where it names none -
// and zero where that promise is not made.
type ServiceLevel struct {
	ID                   uint
	Name                 string
	Description          string
	CalendarID           *uint
	FirstResponseMinutes int
	UpdateMinutes        int
	SolutionMinutes      int
	Valid                bool
}

// MaxServiceMinutes bounds a promise to a year of minutes; a longer one is a
// typing slip, and a deadline searched for across it would only be found by
// accident.
const MaxServiceMinutes = 366 * 24 * 60
