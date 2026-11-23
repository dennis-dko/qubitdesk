package model

import (
	"testing"
	"time"
)

// at is a moment on a November day of 2026, on the hour.
func at(day, hour int) *time.Time {
	moment := time.Date(2026, 11, day, hour, 0, 0, 0, time.UTC)

	return &moment
}

// A change's schedule is what its work orders say, as OTRS works it out:
// planned only once every work order is planned, begun with the first to
// begin, ended only once every one has ended, and the effort and the time of
// them all.
func TestAChangesScheduleIsWhatItsWorkOrdersSay(t *testing.T) {
	first := &WorkOrder{ID: 1, PlannedStartAt: at(23, 8), PlannedEndAt: at(23, 12), ActualStartAt: at(23, 9),
		ActualEndAt: at(23, 11), PlannedMinutes: 240, AccountedMinutes: 120}
	second := &WorkOrder{ID: 2, PlannedStartAt: at(24, 8), PlannedEndAt: at(25, 17), ActualStartAt: at(24, 10),
		PlannedMinutes: 600, AccountedMinutes: 30}

	if schedule := ScheduleOf(nil); schedule != (Schedule{}) {
		t.Errorf("a change without work orders is scheduled %+v", schedule)
	}

	schedule := ScheduleOf([]*WorkOrder{second, first})

	if schedule.PlannedStartAt == nil || !schedule.PlannedStartAt.Equal(*first.PlannedStartAt) ||
		schedule.PlannedEndAt == nil || !schedule.PlannedEndAt.Equal(*second.PlannedEndAt) {
		t.Errorf("the change is planned from %v to %v, want from the first start to the last end",
			schedule.PlannedStartAt, schedule.PlannedEndAt)
	}

	if schedule.ActualStartAt == nil || !schedule.ActualStartAt.Equal(*first.ActualStartAt) ||
		schedule.ActualEndAt != nil {
		t.Errorf("the change began %v and ended %v, want it begun with the first and not ended while one goes on",
			schedule.ActualStartAt, schedule.ActualEndAt)
	}

	if schedule.PlannedMinutes != 840 || schedule.AccountedMinutes != 150 || schedule.WorkOrders != 2 {
		t.Errorf("the change plans %d minutes and accounted %d over %d work orders, want 840 and 150 over two",
			schedule.PlannedMinutes, schedule.AccountedMinutes, schedule.WorkOrders)
	}

	if unplanned := ScheduleOf([]*WorkOrder{first, second, {ID: 3}}); unplanned.PlannedStartAt != nil ||
		unplanned.PlannedEndAt != nil {
		t.Errorf("a change with a work order planned for no moment is planned from %v to %v",
			unplanned.PlannedStartAt, unplanned.PlannedEndAt)
	}

	second.ActualEndAt = at(25, 16)

	if ended := ScheduleOf([]*WorkOrder{first, second}); ended.ActualEndAt == nil ||
		!ended.ActualEndAt.Equal(*second.ActualEndAt) {
		t.Errorf("the change ended %v once every work order had, want with the last", ended.ActualEndAt)
	}
}

// A change's work orders are numbered by their place in its plan: the earliest
// start first, of two starting together the one ending later, and one planned
// for no moment last.
func TestWorkOrdersAreNumberedByTheirPlaceInThePlan(t *testing.T) {
	late := &WorkOrder{ID: 1, PlannedStartAt: at(24, 8), PlannedEndAt: at(24, 9)}
	unplanned := &WorkOrder{ID: 2}
	early := &WorkOrder{ID: 3, PlannedStartAt: at(23, 8), PlannedEndAt: at(23, 9)}
	longer := &WorkOrder{ID: 4, PlannedStartAt: at(23, 8), PlannedEndAt: at(23, 17)}

	NumberWorkOrders([]*WorkOrder{late, unplanned, early, longer})

	for want, order := range map[int]*WorkOrder{1: longer, 2: early, 3: late, 4: unplanned} {
		if order.Number != want {
			t.Errorf("work order %d is number %d, want %d", order.ID, order.Number, want)
		}
	}
}
