package web_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/dennis-dko/go-ticket-system/internal/domain/model"
)

// ticketStates reads the states a ticket ships with, out of the migration that
// seeds them: they are rows now, and what an installation starts with is what
// that migration writes.
func ticketStates(t *testing.T) map[string]struct{} {
	return seededNames(t, "seedTicketStates", `\{"([a-z-]+)", "[a-z-]+", (?:true|false)\}`)
}

// shippedPriorities reads the priorities that ship, as ticketStates reads the
// states.
func shippedPriorities(t *testing.T) map[string]struct{} {
	return seededNames(t, "seedTicketPriorities", `"([a-z-]+)"`)
}

// shippedTicketTypes reads the ticket types that ship, as ticketStates reads the
// states.
func shippedTicketTypes(t *testing.T) map[string]struct{} {
	return seededNames(t, "seedTicketTypes", `"([a-z-]+)"`)
}

// seededNames reads the names one seeding migration writes, with the pattern
// that picks a name out of its list - and stops the test where the migration has
// gone, rather than holding the words to nothing.
func seededNames(t *testing.T, function, name string) map[string]struct{} {
	t.Helper()

	source, err := os.ReadFile(filepath.Join("..", "..", "..",
		"internal", "infrastructure", "persistence", "migration", "migration.go"))
	if err != nil {
		t.Fatalf("reading the migrations: %v", err)
	}

	seed := regexp.MustCompile(`(?s)func ` + function + `\(.*?\n}\n`).Find(source)
	if seed == nil {
		t.Fatalf("the migrations no longer seed in %s", function)
	}

	// Only the list being walked: a statement above it has quoted words too.
	list := regexp.MustCompile(`(?s)range \[\](?:string|struct \{.*?\})\{(.*?)\s*\} \{`).FindSubmatch(seed)
	if list == nil {
		t.Fatalf("%s no longer walks a list of what it seeds", function)
	}

	names := map[string]struct{}{}

	for _, match := range regexp.MustCompile(name).FindAllSubmatch(list[1], -1) {
		names[string(match[1])] = struct{}{}
	}

	if len(names) == 0 {
		t.Fatalf("read no names out of %s", function)
	}

	return names
}

// scriptTitles reads one of the script's tables of English titles, by its name.
func scriptTitles(t *testing.T, table string) map[string]struct{} {
	t.Helper()

	body := regexp.MustCompile(`(?s)const ` + table + ` = \{(.*?)\n\};`).FindStringSubmatch(asset(t, "/app.js"))
	if body == nil {
		t.Fatalf("app.js has no %s", table)
	}

	titles := map[string]struct{}{}

	for _, match := range regexp.MustCompile(`(?m)^\s*'?([a-z-]+)'?:`).FindAllStringSubmatch(body[1], -1) {
		titles[match[1]] = struct{}{}
	}

	return titles
}

// Every state, priority and ticket type that ships has a German word and an
// English title, and neither table names one that does not ship.
//
// The badge and the tables are drawn as t(`status.${status}`) and the like from
// whatever the server sent, so no literal key appears in the source and the
// coverage tests cannot see these. The shipped names are identifiers, so without
// both a reader is shown "very-high" - which is how "active" once sat in the
// middle of an otherwise German table - and a title left behind by a value that
// went is a word nobody notices is wrong. A value an installation adds is shown
// as it was typed, the only sensible answer for a word this application has never
// seen. The types a state can have are held the same way, to model.StateTypes,
// because the form offers them from the script's table - and so are what an
// answer to a closed ticket does and how far a role reaches into a queue, to
// model.FollowUps and model.AccessLevels.
func TestEveryShippedValueAndStateTypeIsNamedInBothLanguages(t *testing.T) {
	dict, ok := dictionaries(t)["de"]
	if !ok {
		t.Fatal("there is no German dictionary")
	}

	stateTypes := map[string]struct{}{}
	for _, kind := range model.StateTypes() {
		stateTypes[string(kind)] = struct{}{}
	}

	followUps := map[string]struct{}{}
	for _, followUp := range model.FollowUps() {
		followUps[string(followUp)] = struct{}{}
	}

	levels := map[string]struct{}{}
	for _, level := range model.AccessLevels() {
		levels[string(level)] = struct{}{}
	}

	channels := map[string]struct{}{}
	for _, channel := range model.AgentChannels() {
		channels[string(channel)] = struct{}{}
	}

	for _, family := range []struct {
		prefix, titles string
		shipped        map[string]struct{}
	}{
		{"status.", "SHIPPED_STATE_TITLES", ticketStates(t)},
		{"priority.", "SHIPPED_PRIORITY_TITLES", shippedPriorities(t)},
		{"tickettype.", "SHIPPED_TYPE_TITLES", shippedTicketTypes(t)},
		{"statetype.", "STATE_TYPE_TITLES", stateTypes},
		{"followup.", "FOLLOW_UP_TITLES", followUps},
		{"queueaccess.", "ACCESS_TITLES", levels},
		{"channel.", "CHANNEL_TITLES", channels},
	} {
		english := scriptTitles(t, family.titles)

		for name := range family.shipped {
			if _, found := dict[family.prefix+name]; !found {
				t.Errorf("%s%s has no German word", family.prefix, name)
			}

			if _, found := english[name]; !found {
				t.Errorf("%s has no English title for %q", family.titles, name)
			}
		}

		for name := range english {
			if _, ships := family.shipped[name]; !ships {
				t.Errorf("%s has a title for %q, which does not ship", family.titles, name)
			}
		}

		for key := range dict {
			if name, inFamily := strings.CutPrefix(key, family.prefix); inFamily {
				if _, ships := family.shipped[name]; !ships {
					t.Errorf("the dictionary has %s, and %q does not ship", key, name)
				}
			}
		}
	}
}

// Every option of the system configuration is described in German, and the
// dictionary describes no option there is not.
//
// The card draws an option as t(`sysconfig.${name}`, description) from the
// listing the server sends, the English being the model's own, so no literal key
// appears in the source and the coverage tests cannot see these: an option added
// without its German would stand in English in an otherwise German card, and a
// sentence left behind by an option that went describes nothing.
func TestEveryConfigOptionIsDescribedInGerman(t *testing.T) {
	dict, ok := dictionaries(t)["de"]
	if !ok {
		t.Fatal("there is no German dictionary")
	}

	for _, option := range model.ConfigOptions() {
		if _, found := dict["sysconfig."+option.Name]; !found {
			t.Errorf("sysconfig.%s has no German description", option.Name)
		}
	}

	for key := range dict {
		if name, isOption := strings.CutPrefix(key, "sysconfig."); isOption && strings.Contains(name, "::") {
			if _, known := model.ConfigOptionNamed(name); !known {
				t.Errorf("the dictionary describes %s, and there is no such option", name)
			}
		}
	}
}
