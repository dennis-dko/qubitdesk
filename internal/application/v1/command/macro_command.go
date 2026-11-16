package command

// MacroCommand creates or changes a macro. On a change an absent field stays as
// it was, and Actions, where sent, takes the place of every step the macro had:
// a macro's steps are one ordered list, and changing one of them in place
// would leave the order to whoever sent the change last.
type MacroCommand struct {
	Name        *string
	Description *string
	Valid       *bool
	Actions     *[]MacroActionCommand
}

// MacroActionCommand is one step of a macro as it was sent: its type, and its
// parameters by the names that type defines.
type MacroActionCommand struct {
	Type       string
	Parameters map[string]string
}
