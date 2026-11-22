package test

import (
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// Everything erased with an account, or taken off it, is handed over with it.
//
// Somebody asking for everything held about them (GDPR articles 15 and 20) is
// asking for what PurgeUser touches: the two are the same list read in two
// directions. A table added to the erasure and not to the export is data the
// person can lose without ever having been shown it; a table in the export and
// not in the erasure is data that outlives the account. Both lists are written
// out as statements, so they are compared as statements: the tables purge.go
// deletes from or sets the account's column to NULL in, and the tables
// export.go reads from.
//
// The export reads one kind of table the erasure leaves alone on purpose, named
// in writtenAndKept with the reason.
func TestEveryTableErasedWithAnAccountIsInItsExport(t *testing.T) {
	sqldb := filepath.Join("..", "internal", "infrastructure", "persistence", "sqldb")
	purge := read(t, filepath.Join(sqldb, "purge.go"))

	touched := append(tablesIn(purge, `DELETE FROM (\w+) WHERE`),
		tablesIn(purge, `UPDATE (\w+) SET [^"]*= NULL`)...)
	slices.Sort(touched)
	touched = slices.Compact(touched)

	exported := tablesIn(read(t, filepath.Join(sqldb, "export.go")),
		`FROM (\w+)(?: \w+)? (?:LEFT JOIN \w+ \w+ ON [\w.]+ = [\w.]+ )?WHERE`)

	if len(touched) < 10 {
		t.Fatalf("only %d tables were found in purge.go, so the scan has stopped reading it: %v", len(touched), touched)
	}

	for _, table := range touched {
		if !slices.Contains(exported, table) {
			t.Errorf("PurgeUser erases %s or takes the account off it, and the export does not hand it over", table)
		}
	}

	for _, table := range exported {
		if _, kept := writtenAndKept[table]; kept {
			continue
		}

		if !slices.Contains(touched, table) {
			t.Errorf("the export hands over %s, and PurgeUser does not erase it or take the account off it", table)
		}
	}

	for table := range writtenAndKept {
		if !slices.Contains(exported, table) {
			t.Errorf("writtenAndKept names %s, which the export no longer reads; drop the row", table)
		}

		if slices.Contains(touched, table) {
			t.Errorf("writtenAndKept names %s, which PurgeUser touches after all; drop the row", table)
		}
	}
}

// writtenAndKept are the tables the export reads and the erasure leaves as they
// are, each with the reason.
var writtenAndKept = map[string]string{
	"articles": "what an agent wrote in a ticket is the ticket's record and stays, signed by an id " +
		"that names nobody once the account is gone; the export says where and when it was written",
	"time_accounting": "the time an agent noted on a ticket is the organisation's record of the work, " +
		"and stays with an id that names nobody once the account is gone",
	"faq_articles": "what an agent last wrote into the knowledge base is the organisation's record and stays, " +
		"signed by an id that names nobody once the account is gone; the export says which and when",
	"config_item_versions": "a version of a configuration item is the organisation's inventory as it stood, " +
		"signed by an id that names nobody once the account is gone; the export says which and when",
	"links": "a link between two records belongs to the records, signed by an id that names nobody once the " +
		"account is gone; the export says which and when",
	"admin_events": "the administration log keeps who changed how the installation runs and outlives the " +
		"account that did, on purpose; the export says when the account did what, not to whom or with which " +
		"detail, which name others",
	"ticket_history": "a line of a ticket's history is the ticket's record of what happened to it, signed by " +
		"an id that names nobody once the account is gone; the export says where, when and what kind of thing",
	"config_items": "a configuration item is the organisation's inventory, saying who made it and who last " +
		"changed it by ids that name nobody once the account is gone; the export says which and when",
	"ci_class_definitions": "a version of a class's definition is what the organisation's items are read " +
		"under, signed by an id that names nobody once the account is gone; the export says which and when",
}

// Everything an account wrote is handed over with it, not only what is erased
// with it.
//
// A column naming an account without a key - who opened a ticket, who moved it
// on, who last corrected a configuration item - outlives the account and names
// nobody afterwards, so the erasure leaves it alone and the comparison above
// never meets it. While the account exists it is data about them all the same,
// and somebody asking for everything held about them is asking for it: the
// export read fewer than half of these columns when this was written, while its
// own comment promised what the account wrote. Such a column is called by one
// of the names in authorColumn, and is read by a statement of the export or
// named in notHandedOver with the reason.
func TestEverythingAnAccountWroteIsInItsExport(t *testing.T) {
	persistence := filepath.Join("..", "internal", "infrastructure", "persistence")
	columns := authorColumns(read(t, filepath.Join(persistence, "migration", "migration.go")))

	// A statement is a string written over several lines, so its pieces are
	// joined before the statements are taken apart.
	export := regexp.MustCompile(`"\s*\+\s*"`).ReplaceAllString(
		read(t, filepath.Join(persistence, "sqldb", "export.go")), "")
	statements := regexp.MustCompile(`"SELECT [^"]*"`).FindAllString(export, -1)

	if len(columns) < 8 {
		t.Fatalf("only %d tables naming an account without a key were found, so the scan has stopped reading "+
			"the chain: %v", len(columns), columns)
	}

	for table, names := range columns {
		for column := range names {
			named := table + "." + column
			from := regexp.MustCompile(`\bFROM ` + table + `\b`)
			where := regexp.MustCompile(`\b(?:\w+\.)?` + column + ` = \?`)
			reads := slices.ContainsFunc(statements, func(statement string) bool {
				return from.MatchString(statement) && where.MatchString(statement)
			})

			_, excused := notHandedOver[named]

			switch {
			case reads && excused:
				t.Errorf("notHandedOver names %s, which the export reads after all; drop the row", named)
			case !reads && !excused:
				t.Errorf("%s names the account that wrote a row, and the export does not hand those rows over",
					named)
			}
		}
	}

	for named := range notHandedOver {
		if table, column, _ := strings.Cut(named, "."); !columns[table][column] {
			t.Errorf("notHandedOver names %s, which the chain does not have; drop the row", named)
		}
	}
}

// notHandedOver are the columns naming an account without a key that the export
// leaves out, each with the reason. It is empty: the last, the administration
// log's actor, has been handed over since 2026-11-22.
var notHandedOver = map[string]string{}

// authorColumn is what a column naming an account without a key is called. A
// column without a key says nothing else about what it holds, so the name is
// what is read.
const authorColumn = `[a-z]+_by|author_id|actor_id`

// authorColumns maps each table onto its columns naming an account without a
// key, read like referencesToUsers reads the keyed ones, with renames applied.
func authorColumns(chain string) map[string]map[string]bool {
	found := map[string]map[string]bool{}

	add := func(table, column string) {
		if found[table] == nil {
			found[table] = map[string]bool{}
		}

		found[table][column] = true
	}

	created := regexp.MustCompile(`CREATE TABLE (?:IF NOT EXISTS )?([a-z_]+)`)
	inTable := regexp.MustCompile(`(?m)^\s*(` + authorColumn + `)\s+.*$`)

	for _, at := range inTable.FindAllStringSubmatchIndex(chain, -1) {
		if strings.Contains(chain[at[0]:at[1]], "REFERENCES") {
			continue
		}

		if tables := created.FindAllStringSubmatchIndex(chain[:at[0]], -1); len(tables) > 0 {
			last := tables[len(tables)-1]
			add(chain[last[2]:last[3]], chain[at[2]:at[3]])
		}
	}

	altered := regexp.MustCompile(`ALTER TABLE ([a-z_]+) ADD COLUMN (` + authorColumn + `)\b([^"` + "`" + `]*)`)

	for _, m := range altered.FindAllStringSubmatch(chain, -1) {
		if !strings.Contains(m[3], "REFERENCES") {
			add(m[1], m[2])
		}
	}

	return withRenames(found, chain)
}

// tablesIn returns every table a pattern's first group names in a source, sorted
// and once each.
func tablesIn(source, pattern string) []string {
	var tables []string

	for _, match := range regexp.MustCompile(pattern).FindAllStringSubmatch(source, -1) {
		if !slices.Contains(tables, match[1]) {
			tables = append(tables, match[1])
		}
	}

	slices.Sort(tables)

	return tables
}
