//go:build integration

package integration

import (
	"net/http"
	"slices"
	"testing"
)

// The knowledge base is read from outside the staff over HTTP, on every
// database: somebody signed in to nothing reads what is public, a customer what
// is meant for customers as well, and a customer whose initial password is still
// to be replaced reads what anybody reads - and an internal article is not found
// by any of them, nor its category named.
func TestTheKnowledgeBaseIsReadFromOutsideTheStaffOverHTTP(t *testing.T) {
	t.Parallel()

	a, admin, agent := startWithWorker(t)

	var printers, servers struct {
		ID uint `json:"id"`
	}

	admin.must(admin.api(http.MethodPost, "/faq/categories", map[string]any{"name": "Printers"}),
		http.StatusCreated, http.StatusOK).Data(t, &printers)
	admin.must(admin.api(http.MethodPost, "/faq/categories", map[string]any{"name": "Servers"}),
		http.StatusCreated, http.StatusOK).Data(t, &servers)

	ids := map[string]uint{}

	for _, article := range []struct {
		title, visibility string
		category          uint
	}{
		{"Paper jam", "public", printers.ID}, {"Toner swap", "external", printers.ID},
		{"Root password", "internal", servers.ID},
	} {
		var made struct {
			ID uint `json:"id"`
		}

		admin.must(admin.api(http.MethodPost, "/faq/articles", map[string]any{
			"title": article.title, "body": "How to.", "visibility": article.visibility, "categoryId": article.category,
		}), http.StatusCreated, http.StatusOK).Data(t, &made)

		ids[article.title] = made.ID
	}

	read := func(c *client) (titles, categories []string) {
		t.Helper()

		var found struct {
			Items []struct {
				Title string `json:"title"`
			} `json:"items"`
		}

		c.must(c.api(http.MethodGet, "/portal/faq/articles", nil), http.StatusOK).Data(t, &found)

		for _, article := range found.Items {
			titles = append(titles, article.Title)
		}

		var named struct {
			Items []struct {
				Name string `json:"name"`
			} `json:"items"`
		}

		c.must(c.api(http.MethodGet, "/portal/faq/categories", nil), http.StatusOK).Data(t, &named)

		for _, category := range named.Items {
			categories = append(categories, category.Name)
		}

		slices.Sort(titles)

		return titles, categories
	}

	nobody := a.newClient()

	if titles, categories := read(nobody); !slices.Equal(titles, []string{"Paper jam"}) ||
		!slices.Equal(categories, []string{"Printers"}) {
		t.Errorf("somebody signed in to nothing reads %v under %v; want the public article under Printers",
			titles, categories)
	}

	var carl struct {
		ID uint `json:"id"`
	}

	agent.must(agent.api(http.MethodPost, "/contacts", map[string]any{"name": "Carl", "email": "carl@example.com"}),
		http.StatusCreated, http.StatusOK).Data(t, &carl)
	agent.must(agent.api(http.MethodPut, path("/contacts/", carl.ID, "/portal-access"),
		map[string]any{"password": "initial-password-1"}), http.StatusOK)

	pending := a.newClient()
	pending.signIn("carl@example.com", "initial-password-1")

	if titles, _ := read(pending); !slices.Equal(titles, []string{"Paper jam"}) {
		t.Errorf("a customer whose initial password is pending reads %v; want what anybody reads", titles)
	}

	pending.must(pending.api(http.MethodPut, "/me/password", map[string]string{
		"currentPassword": "initial-password-1", "newPassword": "carls-own-password",
	}), http.StatusOK)

	customer := a.newClient()
	customer.signIn("carl@example.com", "carls-own-password")

	if titles, categories := read(customer); !slices.Equal(titles, []string{"Paper jam", "Toner swap"}) ||
		!slices.Equal(categories, []string{"Printers"}) {
		t.Errorf("a customer reads %v under %v; want what is meant for customers and what is public, under Printers",
			titles, categories)
	}

	for _, c := range []*client{nobody, customer} {
		if got := c.api(http.MethodGet, path("/portal/faq/articles/", ids["Root password"]), nil); got.Status != http.StatusNotFound {
			t.Errorf("the internal article answered %d from outside the staff, want 404", got.Status)
		}
	}

	if got := nobody.api(http.MethodGet, path("/portal/faq/articles/", ids["Toner swap"]), nil); got.Status != http.StatusNotFound {
		t.Errorf("the customers' article answered %d to somebody signed in to nothing, want 404", got.Status)
	}
}
