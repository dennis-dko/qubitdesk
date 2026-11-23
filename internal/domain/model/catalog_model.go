package model

import "slices"

// CatalogItem is a value of the general catalog, as KIX18 keeps the lists the
// configuration items and the changes pick from: a class naming the list, and
// the value's name in it.
//
// A value is retired - made invalid - and never deleted, as KIX18 allows: the
// configuration items and the changes that picked it go on naming it.
type CatalogItem struct {
	ID    uint
	Class string
	Name  string

	// Functionality is what the code reads in a value of a class whose values
	// mean something to it - a deployment state, an incident state - and is
	// empty in every other class; see Functionalities.
	Functionality string

	Comment string
	Valid   bool
}

// The classes of the general catalog the code reads, under KIX18's names.
const (
	// CatalogDeploymentState lists where in its life a configuration item is,
	// each value saying whether that is before, during or after its use.
	CatalogDeploymentState = "ITSM::ConfigItem::DeploymentState"

	// CatalogIncidentState lists how a configuration item is doing, each value
	// saying whether that is working, warned of or broken.
	CatalogIncidentState = "ITSM::Core::IncidentState"
)

// MaxCatalogClassLength bounds a class's name, as the column does.
const MaxCatalogClassLength = 100

// Functionalities lists what a value of the class may mean to the code, as
// KIX18's Functionality preference does - and nothing for a class whose values
// mean nothing to it, where a functionality is refused rather than kept as a
// word nothing reads.
func Functionalities(class string) []string {
	switch class {
	case CatalogDeploymentState:
		return []string{"preproductive", "productive", "postproductive"}
	case CatalogIncidentState:
		return []string{"operational", "warning", "incident"}
	}

	return nil
}

// FunctionalityOf reports whether the functionality is one a value of the class
// may have: one of its list where the class has one, and none where it has not.
func FunctionalityOf(class, functionality string) bool {
	offered := Functionalities(class)
	if offered == nil {
		return functionality == ""
	}

	return slices.Contains(offered, functionality)
}
