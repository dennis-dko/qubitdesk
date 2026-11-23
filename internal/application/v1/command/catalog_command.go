package command

// CatalogItemCommand makes a value of the general catalog or changes one.
// Making one needs Class and Name; changing leaves a nil field as it was, and
// never moves a value to another class.
type CatalogItemCommand struct {
	Class         *string
	Name          *string
	Functionality *string
	Comment       *string
	Valid         *bool
}
