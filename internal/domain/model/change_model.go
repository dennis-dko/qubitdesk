package model

import "time"

// The general catalog's classes change management picks from, as OTRS::ITSM
// ships them.
const (
	// CatalogChangeCategory, CatalogChangeImpact and CatalogChangePriority are a
	// change's three scales of five.
	CatalogChangeCategory = "ITSM::ChangeManagement::Category"
	CatalogChangeImpact   = "ITSM::ChangeManagement::Impact"
	CatalogChangePriority = "ITSM::ChangeManagement::Priority"

	// CatalogChangeState lists where a change stands; which state may follow
	// which is StateTransition's.
	CatalogChangeState = "ITSM::ChangeManagement::Change::State"
)

// MaxChangeTitleLength and MaxChangeTextLength bound what a change and its work
// orders say: a title, and a description, a justification, an instruction or a
// report each.
const (
	MaxChangeTitleLength = 250
	MaxChangeTextLength  = 50000
)

// Change is a change to what the organisation runs, as OTRS::ITSM's change
// management keeps one: what it is and why, who manages and who builds it, how
// it is ranked, where it stands, and when it is asked for. Its number is the
// day in the installation's zone and its id, written in the transaction that
// makes the row, as a ticket's is.
type Change struct {
	ID            uint
	Number        string
	Title         string
	Description   string
	Justification string
	ManagerID     *uint
	BuilderID     *uint
	CategoryID    uint
	ImpactID      uint
	PriorityID    uint
	StateID       uint
	RequestedAt   *time.Time
	CreatedAt     time.Time
	ChangedAt     time.Time
	CreatedBy     uint
	ChangedBy     uint
}

// StateTransition is one step one of OTRS's state machines allows, a change's
// or a work order's: from a state, or from nowhere for the state one starts in,
// to the next. A state nothing leads out of ends the change or the work order.
type StateTransition struct {
	FromID *uint
	ToID   uint
}

// CIPEntry is one cell of OTRS's matrix of category and impact: the priority a
// change of that category and impact is given unless it is given another.
type CIPEntry struct {
	CategoryID, ImpactID, PriorityID uint
}
