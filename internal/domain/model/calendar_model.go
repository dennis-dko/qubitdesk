package model

import (
	"slices"
	"time"
)

// Calendar is when a service is given: the hours of each weekday in a zone,
// less the public holidays of a place and the days an installation keeps free
// of its own. A service level's minutes are counted in one, as KIX18 counts
// them.
//
// Zone and HolidayPlace may be left empty, and then the calendar follows the
// installation's own zone and public holidays - the one place either is
// decided - rather than a copy of them that goes stale when they change.
type Calendar struct {
	ID           uint
	Name         string
	Description  string
	Zone         string
	HolidayPlace string
	Hours        []WorkingHours
	FreeDays     []FreeDay
	Valid        bool
}

// WorkingHours is one stretch of a weekday in which the service is given, in
// minutes after midnight in the calendar's zone: 480 to 1020 is eight to five.
// A day with a break has two.
type WorkingHours struct {
	Weekday time.Weekday
	From    int
	To      int
}

// FreeDay is a day the service is not given beyond the public holidays: once,
// or every year on that date where it recurs. Day is a calendar day.
type FreeDay struct {
	Day       time.Time
	Recurring bool
	Name      string
}

// MinutesPerDay bounds a stretch of working hours, which ends at midnight at
// the latest; one that runs on is two stretches, one either side.
const MinutesPerDay = 24 * 60

// deadlineHorizon bounds how far a deadline is searched for: a calendar whose
// working hours never come round - Sundays only, every Sunday a free day -
// would otherwise be searched for ever.
const deadlineHorizon = 10 * 366

// ValidHours says whether every stretch lies within its day, starts before it
// ends and overlaps no other stretch of the same weekday.
func ValidHours(hours []WorkingHours) bool {
	sorted := slices.Clone(hours)
	slices.SortFunc(sorted, func(a, b WorkingHours) int {
		if a.Weekday != b.Weekday {
			return int(a.Weekday) - int(b.Weekday)
		}

		return a.From - b.From
	})

	for i, stretch := range sorted {
		if stretch.Weekday < time.Sunday || stretch.Weekday > time.Saturday ||
			stretch.From < 0 || stretch.To > MinutesPerDay || stretch.From >= stretch.To {
			return false
		}

		if i > 0 && sorted[i-1].Weekday == stretch.Weekday && sorted[i-1].To > stretch.From {
			return false
		}
	}

	return true
}

// Deadline is the moment by which minutes of service time have passed after
// start, counted in the calendar's working hours only, in the zone given -
// the calendar's own, or the installation's where it names none. A start
// outside the working hours waits for the next stretch. ok is false for a
// calendar in which the time never passes: no working hours, or none that come
// round within ten years.
//
// holidayPlace is the place whose public holidays are free, resolved the same
// way as the zone.
func (c Calendar) Deadline(start time.Time, minutes int, zone *time.Location, holidayPlace string) (time.Time, bool) {
	if minutes <= 0 {
		return start, true
	}

	remaining := time.Duration(minutes) * time.Minute
	cursor := start.In(zone)
	day := time.Date(cursor.Year(), cursor.Month(), cursor.Day(), 0, 0, 0, 0, zone)
	free := freeDays{calendar: c, place: holidayPlace}

	for range deadlineHorizon {
		if !free.holds(day) {
			for _, stretch := range c.stretchesOf(day.Weekday()) {
				from := wallClock(day, stretch.From, zone)
				to := wallClock(day, stretch.To, zone)

				if !to.After(cursor) {
					continue
				}

				if from.Before(cursor) {
					from = cursor
				}

				available := to.Sub(from)
				if available >= remaining {
					return from.Add(remaining), true
				}

				remaining -= available
			}
		}

		day = time.Date(day.Year(), day.Month(), day.Day()+1, 0, 0, 0, 0, zone)
	}

	return time.Time{}, false
}

// stretchesOf is a weekday's working hours, earliest first.
func (c Calendar) stretchesOf(weekday time.Weekday) []WorkingHours {
	var stretches []WorkingHours

	for _, stretch := range c.Hours {
		if stretch.Weekday == weekday {
			stretches = append(stretches, stretch)
		}
	}

	slices.SortFunc(stretches, func(a, b WorkingHours) int { return a.From - b.From })

	return stretches
}

// wallClock is the moment a number of minutes after midnight reads on the wall
// that day, which on the day the clocks change is not midnight plus that many
// minutes - eight o'clock is eight o'clock either side of it.
func wallClock(day time.Time, minutes int, zone *time.Location) time.Time {
	if minutes >= MinutesPerDay {
		return time.Date(day.Year(), day.Month(), day.Day()+1, 0, 0, 0, 0, zone)
	}

	return time.Date(day.Year(), day.Month(), day.Day(), minutes/60, minutes%60, 0, 0, zone)
}

// freeDays answers whether a day is free in a calendar: one of its own free
// days, or a public holiday of its place. The holidays are worked out a year at
// a time, once.
type freeDays struct {
	calendar Calendar
	place    string
	years    map[int]map[time.Time]bool
}

func (f *freeDays) holds(day time.Time) bool {
	date := CalendarDay(day)

	for _, free := range f.calendar.FreeDays {
		if free.Day.Equal(date) ||
			(free.Recurring && free.Day.Month() == date.Month() && free.Day.Day() == date.Day()) {
			return true
		}
	}

	if f.years == nil {
		f.years = map[int]map[time.Time]bool{}
	}

	holidays, known := f.years[date.Year()]
	if !known {
		holidays = map[time.Time]bool{}
		for _, holiday := range HolidaysIn(f.place, date.Year()) {
			holidays[holiday.Day] = true
		}

		f.years[date.Year()] = holidays
	}

	return holidays[date]
}
