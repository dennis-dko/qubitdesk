//go:build integration

package integration

import (
	"net/http"
	"strconv"
	"strings"
	"testing"
)

type faqArticleResponse struct {
	ID            uint   `json:"id"`
	Title         string `json:"title"`
	Visibility    string `json:"visibility"`
	UpdatedByName string `json:"updatedByName"`
}

// The knowledge base is written and searched over HTTP and through the
// database: a category with one under it, an article for the staff found by its
// words and its category, a category holding an article or another category
// refused its deletion, the export naming what an account wrote, and the
// article staying, unsigned, once its writer is erased.
func TestTheKnowledgeBaseIsWrittenAndSearchedOverHTTP(t *testing.T) {
	t.Parallel()

	a := start(t)
	admin := a.signInAsAdmin("a-much-better-password")
	agnes := a.signInAsAgent(admin, "Agnes", "agnes@example.com")

	var printers, laser struct {
		ID uint `json:"id"`
	}

	admin.must(admin.api(http.MethodPost, "/faq/categories", map[string]any{"name": "Printers"}),
		http.StatusCreated).Data(t, &printers)
	admin.must(admin.api(http.MethodPost, "/faq/categories", map[string]any{"name": "Laser", "parentId": printers.ID}),
		http.StatusCreated).Data(t, &laser)

	var toner faqArticleResponse

	agnes.must(agnes.api(http.MethodPost, "/faq/articles", map[string]any{
		"categoryId": laser.ID, "title": "Toner smudges", "body": "Shake the cartridge.", "keywords": "toner",
	}), http.StatusCreated).Data(t, &toner)

	if toner.Visibility != "internal" || toner.UpdatedByName != "Agnes" {
		t.Fatalf("the article was written as %+v; want it for the staff, signed by Agnes", toner)
	}

	for query, want := range map[string]int{
		"?q=CARTRIDGE": 1, "?q=nothing": 0,
		"?category=" + strconv.FormatUint(uint64(laser.ID), 10):    1,
		"?category=" + strconv.FormatUint(uint64(printers.ID), 10): 0,
	} {
		var found struct {
			Items []faqArticleResponse `json:"items"`
		}

		agnes.must(agnes.api(http.MethodGet, "/faq/articles"+query, nil), http.StatusOK).Data(t, &found)

		if len(found.Items) != want {
			t.Errorf("searching %s found %d, want %d", query, len(found.Items), want)
		}
	}

	for id, want := range map[uint]string{laser.ID: "faqCategoryInUse", printers.ID: "faqCategoryHasChildren"} {
		if code := errorCode(t, admin.api(http.MethodDelete, "/faq/categories/"+strconv.FormatUint(uint64(id), 10), nil)); code != want {
			t.Errorf("deleting category %d was answered %q, want %s", id, code, want)
		}
	}

	if export := agnes.api(http.MethodGet, "/me/export", nil); export.Status != http.StatusOK ||
		!strings.Contains(string(export.Body), "Toner smudges") {
		t.Errorf("Agnes's export was answered %d without the article she wrote", export.Status)
	}

	var users listOf[userResponse]

	admin.must(admin.api(http.MethodGet, "/users", nil), http.StatusOK).Data(t, &users)

	for _, user := range users.Items {
		if user.Email == "agnes@example.com" {
			admin.must(admin.api(http.MethodDelete, "/users/"+strconv.FormatUint(uint64(user.ID), 10), nil),
				http.StatusOK, http.StatusNoContent)
		}
	}

	path := "/faq/articles/" + strconv.FormatUint(uint64(toner.ID), 10)

	admin.must(admin.api(http.MethodGet, path, nil), http.StatusOK).Data(t, &toner)

	if toner.Title != "Toner smudges" || toner.UpdatedByName != "" {
		t.Errorf("once Agnes is erased the article reads %+v; want it kept, naming nobody", toner)
	}

	admin.must(admin.api(http.MethodDelete, path, nil), http.StatusOK, http.StatusNoContent)
	admin.must(admin.api(http.MethodGet, path, nil), http.StatusNotFound)
}

// A customer does not read the knowledge base as the staff do: the portal will
// show them what is meant for them, and the staff's search refuses them.
func TestACustomerDoesNotSearchTheStaffsKnowledgeBase(t *testing.T) {
	t.Parallel()

	a := start(t)
	admin := a.signInAsAdmin("a-much-better-password")

	admin.must(admin.api(http.MethodPost, "/users", map[string]any{
		"name": "Carl", "email": "carl@example.com", "role": "customer", "password": "customer-password-1",
	}), http.StatusCreated, http.StatusOK)

	carl := a.newClient()
	carl.signIn("carl@example.com", "customer-password-1")
	carl.must(carl.api(http.MethodGet, "/faq/articles", nil), http.StatusForbidden)
}
