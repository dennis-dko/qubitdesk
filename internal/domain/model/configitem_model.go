package model

import "time"

// ConfigItem is one configuration item, as KIX18 keeps an asset: a numbered
// thing of a class, with where in its life it is and how it is doing.
//
// What it holds lives in its versions, each written under the definition its
// class had then. The item itself carries what the newest version says, so a
// list of items reads one row each - its name and its two states are copied
// from that version in the same transaction that writes it.
type ConfigItem struct {
	ID      uint
	Number  string
	ClassID uint

	Name              string
	DeploymentStateID uint
	IncidentStateID   uint

	// VersionID is the newest version's, and VersionCount how many there are.
	VersionID    uint
	VersionCount int

	CreatedAt time.Time
	ChangedAt time.Time

	// CreatedBy and ChangedBy are ids without a key, read as names when shown:
	// an item outlives the accounts that wrote it.
	CreatedBy uint
	ChangedBy uint
}

// ConfigItemVersion is what an item said at one moment: its name, its states
// and its attributes' values, under the definition its class had then.
//
// A version is never changed. A change to an item is its next version, so what
// it was before stays readable under the definition it was written with.
type ConfigItemVersion struct {
	ID           uint
	ItemID       uint
	DefinitionID uint

	Name              string
	DeploymentStateID uint
	IncidentStateID   uint

	// Data holds the values by the keys of the definition's attributes.
	Data AttributeData

	CreatedAt time.Time
	CreatedBy uint
}

// AttributeData is the values of one level of a definition: for each attribute
// key, its occurrences in order.
//
// The shape follows the definition rather than flattening it: an attribute
// that occurs three times has three entries, and each entry carries its own
// sub-attributes - the IP addresses of one network interface, not of all of
// them.
type AttributeData map[string][]AttributeValue

// AttributeValue is one occurrence of an attribute: its value, written as a
// string whatever its type - a text as it is, a date as 2006-01-02, a moment in
// RFC 3339, and what a list, a reference, a contact or an organisation names
// by its id - and the values of the attributes beneath it.
//
// A section holds no value of its own, only what is beneath it.
type AttributeValue struct {
	Value string        `json:"value,omitempty"`
	Sub   AttributeData `json:"sub,omitempty"`
}

// The bounds an item is held to beside its definition's.
const (
	MaxConfigItemNameLength = 255
	MaxAttributeValueLength = 50000
)
