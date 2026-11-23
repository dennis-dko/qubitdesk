package model

import "time"

// CatalogCIClass lists the classes a configuration item can be of, as KIX18
// keeps them in the general catalog: computer, hardware, location and the rest.
// Each class has a definition of its own - see ClassDefinition.
const CatalogCIClass = "ITSM::ConfigItem::Class"

// ClassDefinition is what a configuration item of a class holds beside its name
// and its two states: an ordered list of attributes, as KIX18 defines a class.
//
// A definition is never changed in place. A change is a new version, numbered
// on from the last, because an item is written under the version that stood when
// it was written and has to be read under that one: an attribute taken out of the
// definition later is still in the items that have it.
type ClassDefinition struct {
	ID      uint
	ClassID uint

	// Version counts from 1 for each class.
	Version int

	Attributes []ClassAttribute

	CreatedAt time.Time

	// CreatedBy is the account that wrote this version, an id with no key - read
	// as a name when shown, so an erased account's versions remain - and 0 for a
	// version the installation was shipped with.
	CreatedBy uint
}

// AttributeType is what one attribute of a class holds, by KIX18's names for its
// input types.
type AttributeType string

// The attribute types a definition may use.
const (
	// AttributeSection is KIX18's Dummy: a heading over its sub-attributes,
	// holding no value of its own, so a long definition reads in parts.
	AttributeSection AttributeType = "Section"

	AttributeText     AttributeType = "Text"
	AttributeTextArea AttributeType = "TextArea"
	AttributeDate     AttributeType = "Date"
	AttributeDateTime AttributeType = "DateTime"

	// AttributeGeneralCatalog picks a value of the general catalog class the
	// attribute names in Catalog.
	AttributeGeneralCatalog AttributeType = "GeneralCatalog"

	// AttributeCIClassReference names another configuration item, of one of the
	// classes the attribute lists in Classes.
	AttributeCIClassReference AttributeType = "CIClassReference"

	AttributeContact      AttributeType = "Contact"
	AttributeOrganisation AttributeType = "Organisation"
)

// AttributeTypes are the types a definition may use, in the order a screen
// offers them.
func AttributeTypes() []AttributeType {
	return []AttributeType{
		AttributeSection, AttributeText, AttributeTextArea, AttributeDate, AttributeDateTime,
		AttributeGeneralCatalog, AttributeCIClassReference, AttributeContact, AttributeOrganisation,
	}
}

// ClassAttribute is one attribute of a class, as KIX18 writes it.
//
// How often it occurs in an item is three numbers: at least CountMin, at most
// CountMax, and CountDefault where a new item is begun - a computer starts with
// one network interface and may have ten. A section occurs once.
type ClassAttribute struct {
	// Key names the attribute among its siblings, and is what an item's values
	// are kept under: a letter, then letters and digits.
	Key string `json:"key"`

	// Name is what a screen calls it.
	Name string `json:"name"`

	Type AttributeType `json:"type"`

	CountMin     int `json:"countMin"`
	CountMax     int `json:"countMax"`
	CountDefault int `json:"countDefault"`

	// Required says that a value given may not be empty, as KIX18's
	// Input.Required does; how many there must be is CountMin's.
	Required bool `json:"required,omitempty"`

	// Searchable offers the attribute to a search for items.
	Searchable bool `json:"searchable,omitempty"`

	// MaxLength bounds a text, and is left at 0 for every other type.
	MaxLength int `json:"maxLength,omitempty"`

	// Catalog is the general catalog class a GeneralCatalog attribute picks from.
	Catalog string `json:"catalog,omitempty"`

	// Classes are the classes a CIClassReference attribute may name an item of,
	// by their names in the general catalog.
	Classes []string `json:"classes,omitempty"`

	// Sub are the attributes that belong to each occurrence of this one: the
	// IP addresses of one network interface, the version of one product.
	Sub []ClassAttribute `json:"sub,omitempty"`
}

// The bounds a definition is held to: generous for anything a class describes,
// and short of what would make one item's form a page nobody can work through.
const (
	MaxClassAttributes  = 300
	MaxAttributeDepth   = 4
	MaxAttributeCount   = 100
	MaxAttributeKeyLen  = 60
	MaxAttributeTextLen = 10000
)
