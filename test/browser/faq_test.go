//go:build browser

package browser

import (
	"strings"
	"testing"

	"github.com/chromedp/chromedp"
)

// The knowledge base is written and found on its screen: a category made, an
// article written into it for customers as well, found by a word of its text
// and not by one it does not hold, opened and read, and corrected from where it
// is read; the category holding it is refused its deletion.
func TestTheKnowledgeBaseIsWrittenAndFoundOnItsScreen(t *testing.T) {
	t.Parallel()

	p := open(t)
	p.readyAdmin()

	p.run("open the knowledge base", p.click(`.tab[data-view="faq"]`),
		chromedp.WaitVisible("#form-faq-category", chromedp.ByID))

	p.run("make a category",
		chromedp.SetValue(`#form-faq-category [name="name"]`, "Printers", chromedp.ByQuery),
		p.click("#faq-category-submit"))
	p.waitForText("#table-faq-category tbody", "Printers")

	p.run("write an article",
		chromedp.SetValue(`#form-faq-article [name="title"]`, "Toner smudges", chromedp.ByQuery),
		p.chooseOptionByText(`#form-faq-article [name="categoryId"]`, "Printers"),
		p.chooseOption(`#form-faq-article [name="visibility"]`, "external"),
		chromedp.SetValue(`#form-faq-article [name="keywords"]`, "toner", chromedp.ByQuery),
		chromedp.SetValue(`#form-faq-article [name="body"]`, "Shake the cartridge gently.", chromedp.ByQuery),
		p.click("#faq-article-submit"))
	p.waitForText("#faq-article-title", "Toner smudges")

	p.run("search for a word of the text",
		chromedp.SetValue(`#form-faq-search [name="q"]`, "CARTRIDGE", chromedp.ByQuery),
		p.click(`#form-faq-search button[type="submit"]`))
	p.waitForText("#table-faq tbody", "Toner smudges")

	if row := p.text("#table-faq tbody"); !strings.Contains(row, "customers signed in, and the staff") {
		t.Errorf("the article's row reads %q; want who may read it in words", row)
	}

	p.run("search for a word it does not hold",
		chromedp.SetValue(`#form-faq-search [name="q"]`, "monitor", chromedp.ByQuery),
		p.click(`#form-faq-search button[type="submit"]`))
	p.waitForText("#table-faq tbody", "Nothing found.")

	p.run("find it again and open it",
		chromedp.SetValue(`#form-faq-search [name="q"]`, "toner", chromedp.ByQuery),
		p.click(`#form-faq-search button[type="submit"]`))
	p.waitForText("#table-faq tbody", "Toner smudges")
	p.run("open it", p.click(`#table-faq tbody td.text button.link`),
		chromedp.WaitVisible("#faq-article", chromedp.ByID))
	p.waitForText("#faq-article-body", "Shake the cartridge gently.")

	p.run("correct it from where it is read", p.click("#faq-article-edit"))
	p.waitForText("#faq-article-form-title", "Toner smudges")
	p.run("give it a better title",
		chromedp.SetValue(`#form-faq-article [name="title"]`, "Toner smudges on paper", chromedp.ByQuery),
		p.click("#faq-article-submit"))
	p.waitForText("#faq-article-title", "Toner smudges on paper")

	p.run("try to delete its category", chromedp.Evaluate(`[...document.querySelectorAll('#table-faq-category tbody tr')]
		.find((row) => row.textContent.includes('Printers')).querySelector('button.danger').click()`, nil),
		chromedp.WaitVisible(".confirm-overlay", chromedp.ByQuery),
		p.click(`.confirm-actions button.danger`))
	p.waitForText("#toast", "are filed under")

	if !strings.Contains(p.text("#table-faq-category tbody"), "Printers") {
		t.Error("the category holding an article was deleted")
	}
}
