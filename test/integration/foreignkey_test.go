//go:build integration

package integration

import (
	"os"
	"strings"
	"testing"

	"github.com/dennis-dko/qubitdesk/test/harness"
)

// Only PostgreSQL refuses an entry that points at an account that is not there.
//
// Every foreign key in the migration chain is declared on its column - user_id
// INT NOT NULL REFERENCES users(id) - and the engines disagree about what that
// means. PostgreSQL makes a key of it and enforces it. MySQL parses it and makes
// nothing: measured on 8.4.11, information_schema lists no key for any table the
// chain created, and the row below is stored, because only the table-level
// FOREIGN KEY clause creates one there. SQLite keeps the declaration but enforces
// it only on a connection that asks, and whether the application's does is not
// something this suite's own connection can answer, so that leg is skipped.
//
// What rests on this is prose. Several comments, and CLAUDE.md, reason about
// what happens when a table is left out of the account purge, and each of them
// names the engines that refuse. They were written saying PostgreSQL and MySQL; they say PostgreSQL
// now, and this case is what they are written against. It fails the moment an
// engine starts or stops refusing, which is the moment they stop being true.
func TestOnlyPostgreSQLRefusesAnEntryPointingAtNobody(t *testing.T) {
	t.Parallel()

	dsn := os.Getenv(harness.DSNEnv)
	if dsn == "" {
		t.Skip("SQLite enforces a foreign key per connection, and this suite's " +
			"connection cannot speak for the application's")
	}

	a := start(t)

	db := a.DB(t)
	if db == nil {
		t.Fatal("this instance has no database")
	}

	postgres := strings.Contains(dsn, "postgres")

	// The queue is the shipped one, so the only key the row can break is the
	// account's - and a refusal is held to being that key's, because a table
	// that is not there is refused too: this case once wrote to the time
	// recording's timesheets after they had gone, and PostgreSQL's refusal of a
	// missing table read as the refusal it was checking for.
	insert := "INSERT INTO user_queues (user_id, queue_id) SELECT ?, id FROM queues WHERE name = 'Service Desk'"
	if postgres {
		insert = strings.Replace(insert, "?", "$1", 1)
	}

	_, err := db.Exec(insert, 987654321)

	switch {
	case postgres && err == nil:
		t.Error("PostgreSQL stored an entry for an account that does not exist; " +
			"the comments saying it refuses one are wrong now")
	case postgres && !strings.Contains(err.Error(), "foreign key"):
		t.Errorf("PostgreSQL refused the entry for another reason than its key (%v); this case is "+
			"measuring nothing", err)
	case !postgres && err != nil:
		t.Errorf("this engine refused an entry for an account that does not exist (%v); "+
			"the comments saying only PostgreSQL does are wrong now", err)
	}
}
