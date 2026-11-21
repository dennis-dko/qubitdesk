//go:build integration

package integration

import (
	"net/http"
	"testing"
)

// Choosing the demo on the installer fills the new database: signed in, the
// built-in administrator finds the demo's tickets, written in the language the
// installer was read in, and somebody signed in to nothing finds the knowledge
// base's public article.
//
// Asked of the binary as somebody meets it - the installer answered over HTTP,
// the application starting in the same process, the database migrated - because
// the demo is put in during that start, after the built-in administrator is
// made, and only a real start has those in that order.
func TestTheInstallerCanFillTheNewDatabaseWithDemoData(t *testing.T) {
	t.Parallel()

	c := openInstaller(t)

	answer := map[string]any{"dialect": "sqlite", "name": "chosen", "demo": true, "language": "de"}
	if status, body := c.post("/install/save", c.token, answer); status != http.StatusOK {
		t.Fatalf("saving a working connection with demo data: got %d, %v", status, body)
	}

	waitForTheApplication(t, c)

	a := &app{App: c.app, t: t}
	admin := a.signInAsAdmin("a-much-better-password")

	var tickets listOf[ticketResponse]

	admin.must(admin.api(http.MethodGet, "/tickets", nil), http.StatusOK).Data(t, &tickets)

	titles := map[string]bool{}
	for _, ticket := range tickets.Items {
		titles[ticket.Title] = true
	}

	if tickets.TotalCount != 8 || !titles["Der Drucker im zweiten Stock druckt leere Seiten"] {
		t.Fatalf("the administrator finds the tickets %v, want the demo's eight in German\n%s",
			titles, c.app.Log())
	}

	var items listOf[configItem]

	admin.must(admin.api(http.MethodGet, "/cmdb/items", nil), http.StatusOK).Data(t, &items)

	named := map[string]bool{}
	for _, item := range items.Items {
		named[item.Name] = true
	}

	if items.TotalCount != 3 || !named["Laptop von Anna Becker"] {
		t.Errorf("the administrator finds the items %v, want the demo's three in German\n%s", named, c.app.Log())
	}

	var published listOf[faqArticleResponse]

	nobody := a.newClient()
	nobody.must(nobody.api(http.MethodGet, "/portal/faq/articles", nil), http.StatusOK).Data(t, &published)

	if len(published.Items) != 1 || published.Items[0].Title != "Wie setze ich mein Passwort zurück?" {
		t.Errorf("somebody signed in to nothing reads %+v, want the demo's one public article", published.Items)
	}
}

// Without the demo chosen the new database is as empty as it ever was.
func TestTheInstallerFillsNothingUnasked(t *testing.T) {
	t.Parallel()

	c := openInstaller(t)

	if status, body := c.post("/install/save", c.token, sqliteDatasource()); status != http.StatusOK {
		t.Fatalf("saving a working connection: got %d, %v", status, body)
	}

	waitForTheApplication(t, c)

	admin := (&app{App: c.app, t: t}).signInAsAdmin("a-much-better-password")

	var tickets listOf[ticketResponse]

	admin.must(admin.api(http.MethodGet, "/tickets", nil), http.StatusOK).Data(t, &tickets)

	if tickets.TotalCount != 0 {
		t.Errorf("an installation that asked for nothing holds %d tickets", tickets.TotalCount)
	}
}
