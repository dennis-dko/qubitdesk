package web_test

import (
	"regexp"
	"testing"
)

// No function is declared twice in a script the page loads.
//
// A script is one scope, and of two function declarations under one name the
// later one wins, silently, wherever either is called. The general catalog's
// screen declared resetCatalogForm beside the ticket catalog's function of the
// same name further down, and every call meant for the new one ran the old one
// with no card to name: the catalog's form was never put back, and a sign-out
// threw before it had handed the screen back, so it never finished. Nothing
// reports this - the ESLint run here asks only for names that are missing or
// unused, and both of these were used.
func TestNoFunctionIsDeclaredTwiceInAScript(t *testing.T) {
	declared := regexp.MustCompile(`(?m)^(?:async\s+)?function\s*\*?\s*([A-Za-z_$][A-Za-z0-9_$]*)\s*\(`)

	// theme.js keeps its functions inside the one it runs at once, where a
	// declaration is not at the top level of the file and this does not look.
	for _, script := range []string{"/app.js", "/sw.js", "/api-docs.js"} {
		seen := map[string]int{}

		for _, found := range declared.FindAllStringSubmatch(asset(t, script), -1) {
			seen[found[1]]++
		}

		if script == "/app.js" && len(seen) < 100 {
			t.Errorf("app.js declares %d functions at the top level, so this has stopped reading it", len(seen))
		}

		for name, count := range seen {
			if count > 1 {
				t.Errorf("%s declares %s %d times; only the last of them is ever called", script, name, count)
			}
		}
	}
}
