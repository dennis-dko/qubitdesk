//go:build integration

package integration

import (
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// catalogID is the id of the value a ticket catalog names name.
func catalogID(t *testing.T, c *client, path, name string) uint {
	t.Helper()

	var values struct {
		Items []struct {
			ID   uint   `json:"id"`
			Name string `json:"name"`
		} `json:"items"`
	}

	c.must(c.api(http.MethodGet, path, nil), http.StatusOK).Data(t, &values)

	for _, value := range values.Items {
		if value.Name == name {
			return value.ID
		}
	}

	t.Fatalf("%s has no %q", path, name)

	return 0
}

// searched is the titles a search lists, in the order it lists them.
func searched(t *testing.T, c *client, query url.Values) []string {
	t.Helper()

	var page ticketList

	c.must(c.api(http.MethodGet, "/tickets?"+query.Encode(), nil), http.StatusOK).Data(t, &page)

	titles := make([]string, 0, len(page.Items))
	for _, ticket := range page.Items {
		titles = append(titles, ticket.Title)
	}

	return titles
}

// The ticket search narrows the listing by every criterion it is given over
// HTTP - a priority, a type, the caller as responsible and as watcher, a lock,
// words said in an article, the days a ticket was opened on - and orders it by
// urgency when asked, on every database the application runs on.
func TestTheTicketSearchNarrowsByEveryCriterion(t *testing.T) {
	t.Parallel()

	a := start(t)
	admin := a.signInAsAdmin("a-much-better-password")
	desk := theDesk(t, admin)

	var me struct {
		User userResponse `json:"user"`
	}

	admin.must(admin.api(http.MethodGet, "/me", nil), http.StatusOK).Data(t, &me)

	urgent := catalogID(t, admin, "/ticket-priorities", "very-high")
	incident := catalogID(t, admin, "/ticket-types", "incident")

	openTicket(t, admin, desk, "Plain", nil)
	openTicket(t, admin, desk, "Urgent incident", map[string]any{"priorityId": urgent, "typeId": incident})
	held := openTicket(t, admin, desk, "Held", map[string]any{"responsibleId": me.User.ID})
	watched := openTicket(t, admin, desk, "Watched", nil)
	said := openTicket(t, admin, desk, "Said", nil)

	admin.must(admin.api(http.MethodPut, ticketPath(held.ID), map[string]any{"locked": true}), http.StatusOK)
	admin.must(admin.api(http.MethodPut, ticketPath(watched.ID, "watch"), nil), http.StatusOK)
	admin.must(admin.api(http.MethodPost, ticketPath(said.ID, "articles"), map[string]any{
		"body": "The basement is flooded again.",
	}), http.StatusCreated)

	day := func(days int) string { return time.Now().AddDate(0, 0, days).Format(time.DateOnly) }
	everything := []string{"Said", "Held", "Watched", "Urgent incident", "Plain"}

	for name, tc := range map[string]struct {
		query url.Values
		want  []string
	}{
		"a priority":         {url.Values{"priority": {strconv.FormatUint(uint64(urgent), 10)}}, []string{"Urgent incident"}},
		"a type":             {url.Values{"type": {strconv.FormatUint(uint64(incident), 10)}}, []string{"Urgent incident"}},
		"me as responsible":  {url.Values{"responsible": {"me"}}, []string{"Held"}},
		"me as watcher":      {url.Values{"watched": {"true"}}, []string{"Watched"}},
		"a lock":             {url.Values{"locked": {"true"}}, []string{"Held"}},
		"no lock":            {url.Values{"locked": {"false"}}, []string{"Said", "Watched", "Urgent incident", "Plain"}},
		"words in a note":    {url.Values{"text": {"BASEMENT flooded"}}, []string{"Said"}},
		"days around now":    {url.Values{"createdFrom": {day(-2)}, "createdTo": {day(2)}}, everything},
		"days long gone":     {url.Values{"createdTo": {"2026-01-01"}}, nil},
		"nothing overdue":    {url.Values{"overdue": {"true"}}, nil},
		"the most urgent":    {url.Values{"sort": {"priority"}, "limit": {"1"}}, []string{"Urgent incident"}},
		"the oldest first":   {url.Values{"sort": {"created"}, "order": {"asc"}, "limit": {"1"}}, []string{"Plain"}},
		"changed days ahead": {url.Values{"changedFrom": {day(2)}}, nil},
	} {
		// Compared as sets: MySQL keeps these moments to the second, so two
		// tickets touched within one are a tie the id breaks, and which tickets
		// a criterion takes is the question here. The order has cases of its own,
		// each asking for one ticket nothing ties with.
		got, want := searched(t, admin, tc.query), slices.Clone(tc.want)
		slices.Sort(got)
		slices.Sort(want)

		if !slices.Equal(got, want) && len(got)+len(want) > 0 {
			t.Errorf("searching by %s lists %q, want %q", name, got, want)
		}
	}
}

// A search the application cannot answer is refused with 400, naming what it
// could not read, rather than answered with a list that ignores it.
func TestATicketSearchItCannotAnswerIsRefused(t *testing.T) {
	t.Parallel()

	a := start(t)
	admin := a.signInAsAdmin("a-much-better-password")

	for name, tc := range map[string]struct {
		query url.Values
		code  string
		param string
	}{
		"a lock it does not know":         {url.Values{"locked": {"maybe"}}, "invalidFields", "locked"},
		"a watch it does not know":        {url.Values{"watched": {"yes"}}, "invalidFields", "watched"},
		"an overdue it does not know":     {url.Values{"overdue": {"1"}}, "invalidFields", "overdue"},
		"an order of nothing":             {url.Values{"sort": {"size"}}, "invalidFields", "sort"},
		"a direction it does not know":    {url.Values{"order": {"up"}}, "invalidFields", "order"},
		"a day that is no day":            {url.Values{"createdFrom": {"2026-13-01"}}, "dateFormat", ""},
		"a span that runs backwards":      {url.Values{"changedFrom": {"2026-11-21"}, "changedTo": {"2026-11-20"}}, "invalidFields", "changedTo"},
		"more words than it looks for":    {url.Values{"text": {strings.Repeat("word ", 11)}}, "invalidFields", "text"},
		"a priority that is not a number": {url.Values{"priority": {"urgent"}}, "", ""},
	} {
		got := admin.api(http.MethodGet, "/tickets?"+tc.query.Encode(), nil)
		if got.Status != http.StatusBadRequest {
			t.Errorf("searching with %s answered %d, want 400", name, got.Status)

			continue
		}

		var body struct {
			Error struct {
				Code  string   `json:"code"`
				Param []string `json:"param"`
			} `json:"error"`
		}

		if err := json.Unmarshal(got.Body, &body); err != nil {
			t.Fatalf("searching with %s: the refusal is not JSON: %v\n%s", name, err, got.Body)
		}

		if tc.code != "" && body.Error.Code != tc.code {
			t.Errorf("searching with %s was refused with %q, want %q: %s", name, body.Error.Code, tc.code, got.Body)
		}

		if tc.param != "" && !slices.Equal(body.Error.Param, []string{tc.param}) {
			t.Errorf("searching with %s named %q, want %q", name, body.Error.Param, tc.param)
		}
	}
}

// An agent's overview counts their own work, each queue's unfinished tickets
// and the tickets opened today, over HTTP and on every database.
func TestTheOverviewCountsAnAgentsWorkOverHTTP(t *testing.T) {
	t.Parallel()

	a := start(t)
	admin := a.signInAsAdmin("a-much-better-password")
	desk := theDesk(t, admin)

	var me struct {
		User userResponse `json:"user"`
	}

	admin.must(admin.api(http.MethodGet, "/me", nil), http.StatusOK).Data(t, &me)

	openTicket(t, admin, desk, "Owned", map[string]any{"ownerId": me.User.ID})
	openTicket(t, admin, desk, "Nobody's", nil)

	var overview struct {
		Owned  uint `json:"owned"`
		Queues []struct {
			QueueID uint `json:"queueId"`
			New     uint `json:"new"`
		} `json:"queues"`
		Days []struct {
			Day    string `json:"day"`
			Opened uint   `json:"opened"`
		} `json:"days"`
	}

	admin.must(admin.api(http.MethodGet, "/tickets/overview", nil), http.StatusOK).Data(t, &overview)

	if overview.Owned != 1 {
		t.Errorf("the overview counts %d tickets owned, want 1", overview.Owned)
	}

	deskNew := uint(0)
	for _, queue := range overview.Queues {
		if queue.QueueID == desk {
			deskNew = queue.New
		}
	}

	if deskNew != 2 {
		t.Errorf("the desk counts %d new tickets, want 2: %+v", deskNew, overview.Queues)
	}

	if len(overview.Days) != 14 || overview.Days[13].Opened != 2 {
		t.Errorf("the overview's days are %+v; want fourteen, the two tickets opened on the last", overview.Days)
	}
}

// The statistics count the span's tickets over HTTP, by what they are asked to,
// refuse what they cannot count, and are read only with reports:read.
func TestTheStatisticsCountASpanOverHTTP(t *testing.T) {
	t.Parallel()

	a := start(t)
	admin := a.signInAsAdmin("a-much-better-password")
	desk := theDesk(t, admin)

	openTicket(t, admin, desk, "Printer jams", nil)
	openTicket(t, admin, desk, "Paper stuck", nil)

	var report struct {
		By    string `json:"by"`
		Total struct {
			Opened uint `json:"opened"`
		} `json:"total"`
		Rows []struct {
			Name   string `json:"name"`
			Opened uint   `json:"opened"`
		} `json:"rows"`
		Days []struct {
			Day string `json:"day"`
		} `json:"days"`
	}

	admin.must(admin.api(http.MethodGet, "/reports/tickets?by=priority", nil), http.StatusOK).Data(t, &report)

	if report.By != "priority" || report.Total.Opened != 2 || len(report.Rows) != 1 ||
		report.Rows[0].Name != "normal" || report.Rows[0].Opened != 2 || len(report.Days) != 30 {
		t.Errorf("the last thirty days by priority read %+v; want both tickets under normal", report)
	}

	today := time.Now().Format(time.DateOnly)

	for name, tc := range map[string]struct {
		query string
		param string
	}{
		"an unknown dimension":    {"by=size", "by"},
		"a span running backward": {"from=" + today + "&to=2026-01-01", "to"},
		"a span too long":         {"from=2020-01-01&to=" + today, "from"},
		"a day that is no day":    {"from=2026-13-01", ""},
	} {
		got := admin.api(http.MethodGet, "/reports/tickets?"+tc.query, nil)
		if got.Status != http.StatusBadRequest {
			t.Errorf("statistics with %s answered %d, want 400", name, got.Status)

			continue
		}

		var body struct {
			Error struct {
				Param []string `json:"param"`
			} `json:"error"`
		}

		if err := json.Unmarshal(got.Body, &body); err != nil {
			t.Fatalf("statistics with %s: the refusal is not JSON: %v", name, err)
		}

		if tc.param != "" && !slices.Equal(body.Error.Param, []string{tc.param}) {
			t.Errorf("statistics with %s named %q, want %q", name, body.Error.Param, tc.param)
		}
	}

	admin.must(admin.api(http.MethodPost, "/roles", map[string]any{"name": "reader",
		"permissions": []string{"tickets:read"}}), http.StatusCreated, http.StatusOK)
	admin.must(admin.api(http.MethodPost, "/users", map[string]any{"name": "Reader", "email": "reader@example.com",
		"role": "reader", "password": "reader-password-1"}), http.StatusCreated, http.StatusOK)

	reader := a.newClient()
	reader.signIn("reader@example.com", "reader-password-1")

	if refused := reader.api(http.MethodGet, "/reports/tickets", nil); refused.Status != http.StatusForbidden {
		t.Errorf("an account without reports:read read the statistics with %d, want 403", refused.Status)
	}
}
