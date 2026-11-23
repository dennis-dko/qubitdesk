//go:build browser

package browser

import (
	"strconv"
	"strings"
	"testing"

	"github.com/chromedp/chromedp"

	"github.com/dennis-dko/qubitdesk/test/harness"
)

// The knowledge base is read from outside the staff: somebody on the sign-in
// screen is offered it, finds what is public and nothing else, reads it and
// goes back to signing in; a customer finds in the portal what is meant for
// customers as well - and neither ever finds what is the staff's alone.
func TestTheKnowledgeBaseIsReadFromOutsideTheStaff(t *testing.T) {
	t.Parallel()

	p := open(t)
	p.readyAdmin()

	var printers struct {
		ID uint `json:"id"`
	}

	p.awaitJSON(`api('/faq/categories', { method: 'POST', body: JSON.stringify({ name: 'Printers' }) })`, &printers)

	for title, visibility := range map[string]string{
		"Toner stock": "internal", "Toner swap": "external", "Paper jam": "public",
	} {
		p.awaitJSON(`api('/faq/articles', { method: 'POST', body: JSON.stringify({
			categoryId: `+strconv.FormatUint(uint64(printers.ID), 10)+`, title: '`+title+`',
			body: 'How to deal with `+strings.ToLower(title)+`.', visibility: '`+visibility+`' }) })`, nil)
	}

	p.run("sign out", chromedp.Click("#logout", chromedp.ByID),
		chromedp.WaitVisible("#form-login", chromedp.ByID))
	p.run("look something up", chromedp.WaitVisible("#login-faq-open", chromedp.ByID), p.click("#login-faq-open"),
		chromedp.WaitVisible("#public-faq", chromedp.ByID))
	p.waitForText("#public-faq-list", "Paper jam")

	if found := p.text("#public-faq-list"); strings.Contains(found, "Toner") {
		t.Errorf("somebody signed in to nothing finds %q; want what is public alone", found)
	}

	p.run("read it", p.click(`#public-faq-list button.link`), chromedp.WaitVisible("#public-faq-article", chromedp.ByID))
	p.waitForText("#public-faq-article-body", "How to deal with paper jam.")
	p.waitForText("#public-faq-article-facts", "Printers")

	p.run("back to signing in", p.click("#public-faq-close"), chromedp.WaitVisible("#form-login", chromedp.ByID))

	if p.visible("#public-faq") {
		t.Error("going back to signing in left the knowledge base on the screen")
	}

	p.readyAdminAgain()
	p.becomeCustomer()

	p.run("open his tickets", p.click(`.tab[data-view="portal"]`),
		chromedp.WaitVisible("#portal-faq-card", chromedp.ByID))
	p.waitForText("#portal-faq-list", "Toner swap")
	p.waitForText("#portal-faq-list", "Paper jam")

	if found := p.text("#portal-faq-list"); strings.Contains(found, "Toner stock") {
		t.Errorf("a customer finds %q; want nothing that is the staff's alone", found)
	}
}

// readyAdminAgain signs the administrator in once more, with the password
// readyAdmin gave the account.
func (p *page) readyAdminAgain() {
	p.t.Helper()

	p.signIn(harness.AdminEmail, adminPassword)
	p.waitGone("#login-screen")
	p.settled()
	p.settleWelcome()
}
