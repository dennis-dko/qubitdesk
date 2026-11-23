package model

import (
	"cmp"
	"slices"
	"time"
)

// The general catalog's classes a work order picks from, as OTRS::ITSM ships
// them.
const (
	// CatalogWorkOrderState lists where a work order stands; which state may
	// follow which is a StateTransition's.
	CatalogWorkOrderState = "ITSM::ChangeManagement::WorkOrder::State"

	// CatalogWorkOrderType lists what a work order is for.
	CatalogWorkOrderType = "ITSM::ChangeManagement::WorkOrder::Type"
)

// WorkOrderDefaultType is the type a work order is given where nobody says,
// OTRS's ITSMWorkOrder::Type::Default. WorkOrderStartedState is the state whose
// arrival says a work order has begun, so that it is given the moment it began
// where nobody has said one - OTRS's ITSMWorkOrder::ActualStartTimeSet::States.
const (
	WorkOrderDefaultType  = "workorder"
	WorkOrderStartedState = "in-progress"
)

// MaxWorkOrderPlannedMinutes bounds the effort a work order plans: a year round
// the clock is more than one step of a change plans, and a typo of an extra
// digit or two is caught here rather than in a plan nobody believes.
const MaxWorkOrderPlannedMinutes = 366 * 24 * 60

// WorkOrder is one step of carrying out a change, as OTRS::ITSM keeps one: what
// is to be done and what was, by whom, when it is planned and when it happened,
// the effort planned and the time accounted. Its number is its place in the
// change's plan, given by NumberWorkOrders whenever a change's work orders are
// read.
type WorkOrder struct {
	ID               uint
	ChangeID         uint
	Number           int
	Title            string
	Instruction      string
	Report           string
	StateID          uint
	TypeID           uint
	AgentID          *uint
	PlannedStartAt   *time.Time
	PlannedEndAt     *time.Time
	ActualStartAt    *time.Time
	ActualEndAt      *time.Time
	PlannedMinutes   int
	AccountedMinutes int
	CreatedAt        time.Time
	ChangedAt        time.Time
	CreatedBy        uint
	ChangedBy        uint
}

// NumberWorkOrders sorts a change's work orders into their place in its plan
// and numbers them from one, as OTRS renumbers them after every change to one:
// the earliest planned start first, of two starting together the one ending
// later, and by id where nothing tells them apart. A work order planned for no
// moment comes after every planned one, as OTRS's empty moment sorts. OTRS
// stores the number and writes it again on every change; worked out on reading
// it is the same number without the writes.
func NumberWorkOrders(orders []*WorkOrder) {
	slices.SortStableFunc(orders, func(a, b *WorkOrder) int {
		return cmp.Or(compareMoments(a.PlannedStartAt, b.PlannedStartAt),
			-compareMoments(a.PlannedEndAt, b.PlannedEndAt), cmp.Compare(a.ID, b.ID))
	})

	for i, order := range orders {
		order.Number = i + 1
	}
}

// compareMoments orders two moments, an absent one after every moment there is.
func compareMoments(a, b *time.Time) int {
	switch {
	case a == nil && b == nil:
		return 0
	case a == nil:
		return 1
	case b == nil:
		return -1
	}

	return a.Compare(*b)
}

// Schedule is what a change's work orders say of the change, as OTRS works it
// out in WorkOrderChangeTimeGet and WorkOrderChangeEffortsGet: planned from the
// earliest planned start to the latest planned end, but only once every work
// order is planned; begun when the first began; ended when the last ended, and
// only once every one has; the effort planned and the time accounted of them
// all; and how many there are.
type Schedule struct {
	PlannedStartAt   *time.Time
	PlannedEndAt     *time.Time
	ActualStartAt    *time.Time
	ActualEndAt      *time.Time
	PlannedMinutes   int
	AccountedMinutes int
	WorkOrders       int
}

// ScheduleOf is the schedule a change's work orders give it.
func ScheduleOf(orders []*WorkOrder) Schedule {
	schedule := Schedule{WorkOrders: len(orders)}
	planned, ended := len(orders) > 0, len(orders) > 0

	for _, order := range orders {
		schedule.PlannedMinutes += order.PlannedMinutes
		schedule.AccountedMinutes += order.AccountedMinutes

		planned = planned && order.PlannedStartAt != nil && order.PlannedEndAt != nil
		ended = ended && order.ActualEndAt != nil

		if compareMoments(order.ActualStartAt, schedule.ActualStartAt) < 0 {
			schedule.ActualStartAt = order.ActualStartAt
		}
	}

	if planned {
		for _, order := range orders {
			if compareMoments(order.PlannedStartAt, schedule.PlannedStartAt) < 0 {
				schedule.PlannedStartAt = order.PlannedStartAt
			}

			if schedule.PlannedEndAt == nil || order.PlannedEndAt.After(*schedule.PlannedEndAt) {
				schedule.PlannedEndAt = order.PlannedEndAt
			}
		}
	}

	if ended {
		for _, order := range orders {
			if schedule.ActualEndAt == nil || order.ActualEndAt.After(*schedule.ActualEndAt) {
				schedule.ActualEndAt = order.ActualEndAt
			}
		}
	}

	return schedule
}
