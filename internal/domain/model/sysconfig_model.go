package model

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

// ConfigType is what kind of value a configuration option holds, which decides
// how a value is checked, stored and offered for change.
type ConfigType string

// The kinds of value an option holds: a line of text, or a switch.
const (
	ConfigString ConfigType = "string"
	ConfigBool   ConfigType = "bool"
)

// ConfigAccess says who may read an option, KIX18's distinction: an external
// one every account signed in, because a screen shows it, an internal one only
// whoever may change the installation's settings, because it says how the
// installation works rather than what anybody sees.
type ConfigAccess string

// The two readerships an option can have.
const (
	ConfigExternal ConfigAccess = "external"
	ConfigInternal ConfigAccess = "internal"
)

// SysConfigPrefix is what an option's name is stored under in the settings
// table, so that no option can take the key of a setting of another kind.
const SysConfigPrefix = "sysconfig."

// ConfigOption is one option of the system configuration. It is defined here
// with its default, and an installation stores only the options it changed, so
// an option nobody touched follows the default of whichever version is running.
//
// Description is the English the page falls back to; the page looks the
// sentence up as "sysconfig." followed by the name.
type ConfigOption struct {
	Name        string
	Group       string
	Description string
	Type        ConfigType
	Access      ConfigAccess
	Default     string
	MaxLength   int
}

// The options, under KIX18's names, so that somebody who knows KIX finds each
// under the name they know.
const (
	ConfigTicketHook        = "Ticket::Hook"
	ConfigTicketHookDivider = "Ticket::HookDivider"
)

// ConfigOptions are every option there is, in the order a list shows them.
//
// An option goes in here together with the code that reads it: one nothing
// reads is a setting that does nothing, which is worse than none, because
// somebody changes it and believes it took.
func ConfigOptions() []ConfigOption {
	return []ConfigOption{
		{
			Name: ConfigTicketHook, Group: "Ticket", Type: ConfigString, Access: ConfigExternal,
			Default: "Ticket#", MaxLength: 20,
			Description: "The word written before a ticket's number, as in Ticket#2026111500000001 - " +
				"Call# or Case# where that is what the installation calls a ticket.",
		},
		{
			Name: ConfigTicketHookDivider, Group: "Ticket", Type: ConfigString, Access: ConfigExternal,
			Default: "", MaxLength: 5,
			Description: "What stands between that word and the number: nothing, or a colon and a space.",
		},
	}
}

// ConfigOptionNamed is the option of that name, if there is one.
func ConfigOptionNamed(name string) (ConfigOption, bool) {
	for _, option := range ConfigOptions() {
		if option.Name == name {
			return option, true
		}
	}

	return ConfigOption{}, false
}

// Canonical is a value sent for the option in the form it is stored in, or
// false for one the option cannot hold: text longer than allowed, text across
// lines, or anything but text; anything but true or false for a switch. Text is
// not trimmed, because a space can be the point - a colon and a space.
func (o ConfigOption) Canonical(value any) (string, bool) {
	switch o.Type {
	case ConfigString:
		text, ok := value.(string)
		if !ok || utf8.RuneCountInString(text) > o.MaxLength || strings.ContainsAny(text, "\r\n") {
			return "", false
		}

		return text, true
	case ConfigBool:
		flag, ok := value.(bool)
		if !ok {
			return "", false
		}

		return strconv.FormatBool(flag), true
	}

	return "", false
}

// Typed is a stored value in the option's own type, as the API hands it out: a
// switch as true or false, text as itself.
func (o ConfigOption) Typed(stored string) any {
	if o.Type == ConfigBool {
		return stored == "true"
	}

	return stored
}
