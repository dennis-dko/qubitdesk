package model

import "time"

// ExecPlan says when the jobs that name it run: at times of day on the
// weekdays it names, or when something happens to a ticket - KIX18's two kinds
// of execution plan. A retired plan runs nothing, and is kept by the jobs that
// name it rather than taken from them.
type ExecPlan struct {
	ID          uint
	Name        string
	Description string
	Valid       bool
	Type        ExecPlanType

	// Weekdays and Times are a time-based plan's: the days it runs on, and the
	// minutes after midnight it runs at on each, read off the wall in the
	// installation's zone.
	Weekdays []time.Weekday
	Times    []int

	// Events are an event-based plan's: what happening to a ticket runs it.
	Events []TicketEvent
}

// ExecPlanType is whether a plan runs at times or on events, named as KIX18
// names its two.
type ExecPlanType string

// The two kinds of plan.
const (
	PlanTimeBased  ExecPlanType = "TimeBased"
	PlanEventBased ExecPlanType = "EventBased"
)

// TicketEvent is something that happens to a ticket, named as KIX18 names its
// ticket events, so that a plan reads the same to somebody coming from KIX.
type TicketEvent string

// The events an event-based plan can run on. TicketCustomerUpdate is a change
// of the contact or the organisation a ticket is for, which KIX18 calls its
// customer.
const (
	EventTicketCreate            TicketEvent = "TicketCreate"
	EventTicketTitleUpdate       TicketEvent = "TicketTitleUpdate"
	EventTicketQueueUpdate       TicketEvent = "TicketQueueUpdate"
	EventTicketStateUpdate       TicketEvent = "TicketStateUpdate"
	EventTicketPriorityUpdate    TicketEvent = "TicketPriorityUpdate"
	EventTicketTypeUpdate        TicketEvent = "TicketTypeUpdate"
	EventTicketOwnerUpdate       TicketEvent = "TicketOwnerUpdate"
	EventTicketResponsibleUpdate TicketEvent = "TicketResponsibleUpdate"
	EventTicketCustomerUpdate    TicketEvent = "TicketCustomerUpdate"
	EventTicketLockUpdate        TicketEvent = "TicketLockUpdate"
	EventTicketPendingTimeUpdate TicketEvent = "TicketPendingTimeUpdate"
	EventArticleCreate           TicketEvent = "ArticleCreate"
)

// TicketEvents are every event a plan can run on, in the order a form offers
// them.
func TicketEvents() []TicketEvent {
	return []TicketEvent{
		EventTicketCreate, EventTicketTitleUpdate, EventTicketQueueUpdate, EventTicketStateUpdate,
		EventTicketPriorityUpdate, EventTicketTypeUpdate, EventTicketOwnerUpdate, EventTicketResponsibleUpdate,
		EventTicketCustomerUpdate, EventTicketLockUpdate, EventTicketPendingTimeUpdate, EventArticleCreate,
	}
}

// MaxPlanTimes bounds the times of day a plan runs at. Chosen rather than
// derived: every quarter of an hour is ninety-six, and a job wanted more often
// than that is a job that should not be time-based.
const MaxPlanTimes = 96
