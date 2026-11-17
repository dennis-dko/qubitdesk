package model

import (
	"testing"
	"time"

	_ "time/tzdata"
)

// officeHours is Monday to Friday, eight to twelve and one to five.
func officeHours() []WorkingHours {
	var hours []WorkingHours

	for day := time.Monday; day <= time.Friday; day++ {
		hours = append(hours, WorkingHours{Weekday: day, From: 8 * 60, To: 12 * 60},
			WorkingHours{Weekday: day, From: 13 * 60, To: 17 * 60})
	}

	return hours
}

// A deadline is counted in working hours only: past the lunch break, the
// weekend, the public holidays of the calendar's place and its own free days,
// once or every year, with a start outside the hours waiting for the next.
func TestADeadlineIsCountedInWorkingHoursOnly(t *testing.T) {
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatal(err)
	}

	at := func(month time.Month, day, hour, minute int) time.Time {
		return time.Date(2026, month, day, hour, minute, 0, 0, berlin)
	}

	calendar := Calendar{Hours: officeHours(), FreeDays: []FreeDay{
		{Day: time.Date(2026, time.November, 17, 0, 0, 0, 0, time.UTC), Name: "Works outing"},
		{Day: time.Date(2020, time.December, 31, 0, 0, 0, 0, time.UTC), Recurring: true, Name: "New Year's Eve"},
	}}

	for _, tc := range []struct {
		name    string
		start   time.Time
		minutes int
		place   string
		want    time.Time
	}{
		{"within the morning", at(time.November, 16, 10, 0), 60, "", at(time.November, 16, 11, 0)},
		{"past the lunch break", at(time.November, 16, 11, 30), 60, "", at(time.November, 16, 13, 30)},
		{"before the hours", at(time.November, 16, 6, 0), 30, "", at(time.November, 16, 8, 30)},
		{"past a free day of its own", at(time.November, 16, 16, 30), 60, "", at(time.November, 18, 8, 30)},
		{"past the weekend", at(time.November, 20, 16, 0), 120, "", at(time.November, 23, 9, 0)},
		{"on a Saturday", at(time.November, 21, 10, 0), 30, "", at(time.November, 23, 8, 30)},
		{"past Christmas in Bavaria", at(time.December, 24, 16, 0), 120, HolidayPlace("DE", "BY"),
			at(time.December, 28, 9, 0)},
		{"past a free day every year", time.Date(2027, time.December, 30, 16, 30, 0, 0, berlin), 60, "",
			time.Date(2028, time.January, 3, 8, 30, 0, 0, berlin)},
		{"no time at all", at(time.November, 16, 10, 0), 0, "", at(time.November, 16, 10, 0)},
	} {
		got, ok := calendar.Deadline(tc.start, tc.minutes, berlin, tc.place)
		if !ok || !got.Equal(tc.want) {
			t.Errorf("%s: %v, %t; want %v", tc.name, got.In(berlin), ok, tc.want)
		}
	}
}

// The clocks changing do not move the working hours, which are read off the
// wall; around the clock, the hour that does not exist is not counted.
func TestADeadlineReadsTheHoursOffTheWall(t *testing.T) {
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatal(err)
	}

	office := Calendar{Hours: officeHours()}

	// Friday the 27th of March 2026, an hour before five; the clocks go forward
	// on the Sunday, and Monday still starts at eight on the wall.
	got, ok := office.Deadline(time.Date(2026, time.March, 27, 16, 0, 0, 0, berlin), 120, berlin, "")
	if want := time.Date(2026, time.March, 30, 9, 0, 0, 0, berlin); !ok || !got.Equal(want) {
		t.Errorf("across the change of clocks: %v, %t; want %v", got.In(berlin), ok, want)
	}

	// Eight o'clock on the very Sunday the clocks go forward is eight on the
	// wall, which is seven hours after midnight rather than eight.
	sundays := Calendar{Hours: []WorkingHours{{Weekday: time.Sunday, From: 8 * 60, To: 17 * 60}}}

	got, ok = sundays.Deadline(time.Date(2026, time.March, 29, 7, 0, 0, 0, berlin), 60, berlin, "")
	if want := time.Date(2026, time.March, 29, 9, 0, 0, 0, berlin); !ok || !got.Equal(want) {
		t.Errorf("on the day of the change: %v, %t; want %v", got.In(berlin), ok, want)
	}

	var always []WorkingHours
	for day := time.Sunday; day <= time.Saturday; day++ {
		always = append(always, WorkingHours{Weekday: day, From: 0, To: MinutesPerDay})
	}

	// Three hours from eleven on the Saturday night are three real hours, the
	// night the clocks go forward: three in the morning on the wall.
	got, ok = Calendar{Hours: always}.Deadline(time.Date(2026, time.March, 28, 23, 0, 0, 0, berlin), 180, berlin, "")
	if want := time.Date(2026, time.March, 29, 3, 0, 0, 0, berlin); !ok || !got.Equal(want) {
		t.Errorf("around the clock: %v, %t; want %v", got.In(berlin), ok, want)
	}
}

// A calendar with no working hours has no deadline, rather than one searched
// for until the horizon gives out and then made up.
func TestACalendarWithoutHoursHasNoDeadline(t *testing.T) {
	if got, ok := (Calendar{}).Deadline(time.Now(), 60, time.UTC, ""); ok {
		t.Errorf("a calendar without hours answered %v", got)
	}
}

// A stretch lies within its day, starts before it ends and overlaps no other
// stretch of its weekday; touching is not overlapping.
func TestWorkingHoursAreCheckedStretchByStretch(t *testing.T) {
	for _, tc := range []struct {
		name  string
		hours []WorkingHours
		valid bool
	}{
		{"office hours", officeHours(), true},
		{"a break of no length", []WorkingHours{
			{Weekday: time.Monday, From: 480, To: 720}, {Weekday: time.Monday, From: 720, To: 1020}}, true},
		{"until midnight", []WorkingHours{{Weekday: time.Monday, From: 1200, To: MinutesPerDay}}, true},
		{"overlapping", []WorkingHours{
			{Weekday: time.Monday, From: 480, To: 720}, {Weekday: time.Monday, From: 700, To: 1020}}, false},
		{"backwards", []WorkingHours{{Weekday: time.Monday, From: 720, To: 480}}, false},
		{"past midnight", []WorkingHours{{Weekday: time.Monday, From: 1200, To: MinutesPerDay + 60}}, false},
		{"no weekday", []WorkingHours{{Weekday: 7, From: 480, To: 720}}, false},
	} {
		if got := ValidHours(tc.hours); got != tc.valid {
			t.Errorf("%s: valid %t, want %t", tc.name, got, tc.valid)
		}
	}
}

// Before counts backwards as Deadline counts forwards: from the end, back
// through the working hours only - past the lunch break, the weekend, the
// public holidays and the calendar's own free days - to the latest moment from
// which that much service time has passed by the end.
func TestBeforeCountsTheWorkingHoursBackwards(t *testing.T) {
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatal(err)
	}

	at := func(month time.Month, day, hour, minute int) time.Time {
		return time.Date(2026, month, day, hour, minute, 0, 0, berlin)
	}

	calendar := Calendar{Hours: officeHours(), FreeDays: []FreeDay{
		{Day: time.Date(2026, time.November, 17, 0, 0, 0, 0, time.UTC), Name: "Works outing"},
	}}

	for _, tc := range []struct {
		name    string
		end     time.Time
		minutes int
		place   string
		want    time.Time
	}{
		{"within the morning", at(time.November, 16, 11, 0), 60, "", at(time.November, 16, 10, 0)},
		{"back past the lunch break", at(time.November, 16, 13, 30), 60, "", at(time.November, 16, 11, 30)},
		{"during the lunch break", at(time.November, 16, 12, 30), 60, "", at(time.November, 16, 11, 0)},
		{"back past the weekend", at(time.November, 23, 8, 30), 60, "", at(time.November, 20, 16, 30)},
		{"back past a free day of its own", at(time.November, 18, 8, 30), 60, "", at(time.November, 16, 16, 30)},
		{"back past Christmas in Bavaria", at(time.December, 28, 9, 0), 120, HolidayPlace("DE", "BY"),
			at(time.December, 24, 16, 0)},
		{"no time at all", at(time.November, 16, 10, 0), 0, "", at(time.November, 16, 10, 0)},
	} {
		got, ok := calendar.Before(tc.end, tc.minutes, berlin, tc.place)
		if !ok || !got.Equal(tc.want) {
			t.Errorf("%s: %v, %t; want %v", tc.name, got.In(berlin), ok, tc.want)
		}
	}
}

// Before and Deadline agree on every moment: the deadline from the moment
// Before finds is no later than the end, and from a minute after it, later.
func TestBeforeIsTheDeadlineTurnedRound(t *testing.T) {
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatal(err)
	}

	calendar := Calendar{Hours: officeHours()}

	for hour := 0; hour < 24*9; hour += 5 {
		end := time.Date(2026, time.March, 23, 0, 7, 0, 0, berlin).Add(time.Duration(hour) * time.Hour)

		for _, minutes := range []int{1, 45, 240, 600} {
			start, ok := calendar.Before(end, minutes, berlin, "")
			if !ok {
				t.Fatalf("no moment %d minutes before %v", minutes, end)
			}

			if due, _ := calendar.Deadline(start, minutes, berlin, ""); due.After(end) {
				t.Errorf("%d minutes from %v are due at %v, after %v", minutes, start.In(berlin), due.In(berlin), end.In(berlin))
			}

			if due, _ := calendar.Deadline(start.Add(time.Minute), minutes, berlin, ""); !due.After(end) {
				t.Errorf("%d minutes from a minute after %v are due at %v, not after %v",
					minutes, start.In(berlin), due.In(berlin), end.In(berlin))
			}
		}
	}

	if got, ok := (Calendar{}).Before(time.Now(), 60, time.UTC, ""); ok {
		t.Errorf("a calendar without hours answered %v", got)
	}
}
