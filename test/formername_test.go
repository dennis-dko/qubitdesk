package test

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// formerName matches every name that is not this project's, however it is
// written: the two it had before it was QubitDesk - the time recording's it
// was cloned from, until 2026-11-12, and its own first one, until 2026-11-21 -
// and the name the time recording carries now, which this tree has not named
// since 2026-11-22, as Dennis decided that day. A name is its words joined by
// a hyphen, an underscore, a space or nothing, in any case, and its
// abbreviations go with it: the three letters each former name carried in its
// cookies, token prefixes, variables and sealed secrets, and the two the time
// recording's backups carry now.
//
// The three letters count as a word of their own after anything but a letter
// or a digit; glued to a word where no + or / before them could make them the
// inside of a hash; anywhere before a separator their prefixes used, which is
// how two MySQL health checks still carried them, glued to the option in front
// of a user and a password; and anywhere in the words they were glued to - a
// calendar key, a backup, a theme - which are too long to turn up inside a
// hash. A word of its own after a slash is a path, a host or a connection
// string, and the first sweep of the second rename left exactly those behind,
// and a calendar key after a slash, because it skipped them as the earlier
// version of this pattern did. All of it is spelled so that this file does not
// match it.
var formerName = regexp.MustCompile(`(?i)go[-_ ]?time[-_ ]?recording|go[-_ ]?tick[e]t[-_ ]?system|` +
	`time[-_ ]?pul[s]e|t[p](bak|backup)|(^|[^a-z0-9])g[t][rs]([^a-z0-9]|$)|(^|[^a-z0-9+/])g[t][rs]|` +
	`g[t][rs][_.:-]|g[t][rs](cal|bak|theme|backup)`)

// oldNameKeptOnPurpose are the files that may still use a name formerName
// matches, each with its reason. A row whose file has stopped using it fails
// as well, so the list cannot go on excusing what is no longer there. It is
// empty: both renames came before any release, so no database, cookie or token
// ever carried a former name for something to convert, and the audit ledger's
// quotes of a database name were brought to the present one on 2026-11-22 -
// the first rename had rewritten them already, so they quoted nothing as it
// was read.
var oldNameKeptOnPurpose = map[string]string{}

// The clone was given its own name on 2026-11-12, before anything else changed,
// and the one it carries now on 2026-11-21. Each rename looked for the
// spellings somebody thought of, and each let one past: the time recording's
// left its former name in camel case in the Android app's download keeper,
// which nothing reported because nothing reads that mark but the script that
// sets it; the second left the abbreviation in every path, host and connection
// string, where it follows a slash; and after both, two MySQL health checks
// still asked with the user and passwords of the first name, which went on
// passing because mysqladmin ping answers success to a refused password. So
// every spelling of every name is looked for here at once, in every text file
// git keeps and in the name of every file.
func TestNothingStillCarriesTheFormerName(t *testing.T) {
	// safe.directory, because in the Linux container the suite runs in here the
	// mounted repository belongs to another user and git refuses it otherwise.
	out, err := exec.Command("git", "-c", "safe.directory=*", "-C", "..", "ls-files", "--eol").Output()
	if err != nil {
		t.Fatalf("asking git which files it keeps: %v", err)
	}

	listed := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(listed) < 500 {
		t.Fatalf("git listed %d files; the reading has stopped finding them", len(listed))
	}

	usesIt := map[string]bool{}

	for _, line := range listed {
		meta, path, _ := strings.Cut(line, "\t")
		if formerName.MatchString(path) {
			t.Errorf("%s is named with a name that is not this project's", path)
		}

		if strings.HasPrefix(meta, "i/-text") {
			continue
		}

		content, err := os.ReadFile(filepath.Join("..", filepath.FromSlash(path)))
		if err != nil {
			t.Errorf("reading %s: %v", path, err)

			continue
		}

		for n, text := range strings.Split(string(content), "\n") {
			if !formerName.MatchString(text) {
				continue
			}

			usesIt[path] = true

			if _, kept := oldNameKeptOnPurpose[path]; !kept {
				t.Errorf("%s:%d names what this project is not called: %s", path, n+1, strings.TrimSpace(text))
			}
		}
	}

	for path, reason := range oldNameKeptOnPurpose {
		if !usesIt[path] {
			t.Errorf("%s no longer uses a name formerName matches, so its row (%q) excuses nothing; remove it",
				path, reason)
		}
	}
}
