package model

import "time"

// LinkObject is a kind of record a link joins: a ticket and a configuration
// item, and as the modules come, a change and an article of the knowledge base.
type LinkObject string

const (
	// LinkTicket is a ticket, reached through its queue.
	LinkTicket LinkObject = "ticket"

	// LinkConfigItem is a configuration item.
	LinkConfigItem LinkObject = "configitem"

	// LinkChange is a change.
	LinkChange LinkObject = "change"
)

// LinkObjects are the kinds a link may join.
func LinkObjects() []LinkObject {
	return []LinkObject{LinkTicket, LinkConfigItem, LinkChange}
}

// LinkType is how two records are joined, by KIX18's names. A Normal link reads
// the same from either end; ParentChild and DependsOn have a direction, read one
// way from the link's source and the other from its target.
type LinkType string

const (
	// LinkNormal is "relevant to", the same from both ends.
	LinkNormal LinkType = "Normal"

	// LinkParentChild is "parent of" from its source and "child of" from its
	// target.
	LinkParentChild LinkType = "ParentChild"

	// LinkDependsOn is "depends on" from its source and "required for" from its
	// target.
	LinkDependsOn LinkType = "DependsOn"
)

// LinkTypes are the ways two records may be joined.
func LinkTypes() []LinkType {
	return []LinkType{LinkNormal, LinkParentChild, LinkDependsOn}
}

// Link joins two records. Two records are joined once, whichever way round and
// in whichever type: a second link between them would say two things about one
// relation.
type Link struct {
	ID           uint
	SourceObject LinkObject
	SourceID     uint
	TargetObject LinkObject
	TargetID     uint
	Type         LinkType
	CreatedAt    time.Time
	CreatedBy    uint
}

// Joins reports whether the link has the record at one of its ends.
func (l Link) Joins(object LinkObject, id uint) bool {
	return (l.SourceObject == object && l.SourceID == id) || (l.TargetObject == object && l.TargetID == id)
}
