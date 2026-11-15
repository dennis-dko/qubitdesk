package model

import (
	"regexp"
	"testing"
)

// Every option can hold its own default, fits the settings table's key, has a
// name a path can carry, and is the only option of that name.
func TestEveryConfigOptionIsWellFormed(t *testing.T) {
	plain := regexp.MustCompile(`^[A-Za-z][A-Za-z0-9:]*$`)
	seen := map[string]bool{}

	for _, option := range ConfigOptions() {
		if seen[option.Name] {
			t.Errorf("%s is defined twice", option.Name)
		}

		seen[option.Name] = true

		if !plain.MatchString(option.Name) {
			t.Errorf("%s has a character a path cannot carry plainly; '#' would end the path at it", option.Name)
		}

		if len(SysConfigPrefix+option.Name) > 64 {
			t.Errorf("%s does not fit the settings table's key of 64 characters", option.Name)
		}

		var sent any = option.Default
		if option.Type == ConfigBool {
			sent = option.Default == "true"
		}

		if canonical, ok := option.Canonical(sent); !ok || canonical != option.Default {
			t.Errorf("%s cannot hold its own default %q", option.Name, option.Default)
		}

		if option.Group == "" || option.Description == "" {
			t.Errorf("%s has no group or no description", option.Name)
		}

		if option.Access != ConfigExternal && option.Access != ConfigInternal {
			t.Errorf("%s has the access %q", option.Name, option.Access)
		}
	}
}

// A value is checked against the option it is sent for: text by its length and
// its lines, a switch by being true or false.
func TestAConfigValueIsCheckedAgainstItsOption(t *testing.T) {
	hook, _ := ConfigOptionNamed(ConfigTicketHook)
	divider, _ := ConfigOptionNamed(ConfigTicketHookDivider)
	unlock := ConfigOption{Name: "Test::Switch", Type: ConfigBool, Default: "true"}

	for _, tc := range []struct {
		name   string
		option ConfigOption
		value  any
		want   string
		ok     bool
	}{
		{"text", hook, "Call#", "Call#", true},
		{"text at the limit", hook, "abcdefghijklmnopqrst", "abcdefghijklmnopqrst", true},
		{"text over the limit", hook, "abcdefghijklmnopqrstu", "", false},
		{"text across lines", hook, "Call\n#", "", false},
		{"not text", hook, 5.0, "", false},
		{"a space is kept", divider, ": ", ": ", true},
		{"spaces count", divider, "      ", "", false},
		{"a switch", unlock, false, "false", true},
		{"a switch spelled as text", unlock, "false", "", false},
	} {
		got, ok := tc.option.Canonical(tc.value)
		if got != tc.want || ok != tc.ok {
			t.Errorf("%s: %q, %t; want %q, %t", tc.name, got, ok, tc.want, tc.ok)
		}
	}

	if typed := unlock.Typed("false"); typed != false {
		t.Errorf("a switch stored as false reads as %v", typed)
	}
}
