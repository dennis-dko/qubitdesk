package config_test

import (
	"testing"

	"github.com/dennis-dko/qubitdesk/internal/infrastructure/config"
	"github.com/dennis-dko/qubitdesk/internal/support/apperror"
)

// A connection is read back the way the driver will read it, and refused only
// where that differs from what was typed.
//
// Each misreading below was measured with the driver's own parser before this
// existed: an empty PostgreSQL password took "dbname=qubitdesk" for itself and left the
// database unnamed, a backslash vanished, a space and a leading quote were refused
// in the parser's English, and MySQL split a user at its colon, cut a database's
// name at a question mark and dialled "[::1:3306]:3306" for "::1".
func TestAConnectionIsReadBackTheWayTheDriverReadsIt(t *testing.T) {
	postgres := func(user, password, name string) config.Datasource {
		return config.Datasource{Dialect: "postgres", Host: "db", User: user, Password: password, Name: name}
	}

	mysql := func(user, password, host, name string) config.Datasource {
		return config.Datasource{Dialect: "mysql", Host: host, User: user, Password: password, Name: name}
	}

	for _, c := range []struct {
		what string
		ds   config.Datasource
		code string
	}{
		{"an ordinary PostgreSQL connection", postgres("qubitdesk", "secret", "qubitdesk"), ""},
		{"a PostgreSQL password with an equals sign", postgres("qubitdesk", "pa=ss", "qubitdesk"), ""},
		{"a PostgreSQL password with a quote inside", postgres("qubitdesk", "it's", "qubitdesk"), ""},
		{"no PostgreSQL password", postgres("postgres", "", "qubitdesk"), "passwordSwallowsName"},
		{"a PostgreSQL password with a space", postgres("qubitdesk", "two words", "qubitdesk"), "postgresMisread"},
		{"a PostgreSQL password with a backslash", postgres("qubitdesk", `back\slash`, "qubitdesk"), "postgresMisread"},
		{"a PostgreSQL password opening with a quote", postgres("qubitdesk", "'quoted", "qubitdesk"), "postgresMisread"},
		{"a PostgreSQL password carrying a setting", postgres("qubitdesk", "x sslmode=require", "qubitdesk"), "postgresMisread"},
		{"a PostgreSQL database name with a space", postgres("qubitdesk", "secret", "time sheets"), "postgresMisread"},
		{"an ordinary MySQL connection", mysql("qubitdesk", "secret", "db", "qubitdesk"), ""},
		{"a MySQL password of every awkward kind", mysql("qubitdesk", `p@ss:w/rd?)\ x`, "db", "qubitdesk"), ""},
		{"no MySQL password", mysql("qubitdesk", "", "db", "qubitdesk"), ""},
		{"a bracketed IPv6 address for MySQL", mysql("qubitdesk", "secret", "[::1]", "qubitdesk"), ""},
		{"a bare IPv6 address for MySQL", mysql("qubitdesk", "secret", "::1", "qubitdesk"), "mysqlMisread"},
		{"a MySQL user with a colon", mysql("ti:me", "secret", "db", "qubitdesk"), "mysqlMisread"},
		{"a MySQL database name with a question mark", mysql("qubitdesk", "secret", "db", "time?rec"), "mysqlMisread"},
		{"SQLite", config.Datasource{Dialect: "sqlite", Name: "qubitdesk"}, ""},
	} {
		err := config.Misreading(c.ds)

		got := ""
		if detail, ours := apperror.Detail(err); ours {
			got = detail.Code
		} else if err != nil {
			got = "an uncoded " + err.Error()
		}

		if got != c.code {
			t.Errorf("%s: got %q, want %q", c.what, got, c.code)
		}
	}
}

// Where the environment rather than what was typed decides, the driver is left to
// say so.
//
// lib/pq refuses a handful of PG* variables, and the real connection fails on them
// with words naming the variable; a PGDATABASE supplies the name an empty password
// swallows, so that server does open the database on the form.
func TestTheEnvironmentIsNotBlamedOnWhatWasTyped(t *testing.T) {
	unnamed := config.Datasource{Dialect: "postgres", Host: "db", User: "postgres", Name: "qubitdesk"}

	if err := config.Misreading(unnamed); err == nil {
		t.Fatal("this case starts from an empty password being refused, and it was not")
	}

	t.Run("a variable lib/pq refuses", func(t *testing.T) {
		t.Setenv("PGGSSENCMODE", "disable")

		if err := config.Misreading(config.Datasource{
			Dialect: "postgres", Host: "db", User: "qubitdesk", Password: "two words", Name: "qubitdesk",
		}); err != nil {
			t.Errorf("the connection was called misread: %v", err)
		}
	})

	t.Run("PGDATABASE naming the database", func(t *testing.T) {
		t.Setenv("PGDATABASE", "qubitdesk")

		if err := config.Misreading(unnamed); err != nil {
			t.Errorf("an empty password was refused: %v", err)
		}
	})
}
