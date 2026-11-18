//go:build browser

package browser

import (
	"strings"
	"testing"

	"github.com/chromedp/chromedp"
)

// A contact is given the portal from their row on the Customers screen: the
// switch for their organisation's tickets is kept with the contact, the dialog
// names the address they sign in with and generates a password the account then
// signs in with, a new password is given the same way, and taking the access away
// says so in the row.
func TestAContactIsGivenThePortalFromTheirRow(t *testing.T) {
	t.Parallel()

	p := open(t)
	p.readyAdmin()

	p.run("open the customers", p.click(`.tab[data-view="customers"]`),
		chromedp.WaitVisible("#form-contact", chromedp.ByID))

	p.run("make a contact who sees the organisation's tickets",
		chromedp.SetValue(`#form-contact [name="name"]`, "Ann Anders", chromedp.ByQuery),
		chromedp.SetValue(`#form-contact [name="email"]`, "ann@acme.example", chromedp.ByQuery),
		chromedp.Click(`#form-contact [name="seesOrganisation"]`, chromedp.ByQuery),
		p.click("#contact-submit"))
	p.waitForText("#table-contact tbody", "Ann Anders")

	p.run("open her for correcting", p.click(`#table-contact tbody td.actions button:first-child`))
	p.waitForText("#contact-form-title", "Ann Anders")

	var sees bool

	p.run("read the switch", chromedp.Evaluate(
		`document.querySelector('#form-contact [name="seesOrganisation"]').checked`, &sees))

	if !sees {
		t.Error("the contact opened for correcting does not see the organisation's tickets any more")
	}

	p.run("leave her as she is", p.click("#contact-cancel"))

	p.run("give her the portal", p.click(`#table-contact tbody button[data-action="portal-give"]`),
		chromedp.WaitVisible("#portal-password-field", chromedp.ByID))

	if asked := p.text(".confirm-card"); !strings.Contains(asked, "Ann Anders") ||
		!strings.Contains(asked, "ann@acme.example") {
		t.Errorf("the question does not name the contact and the address she signs in with: %q", asked)
	}

	var generated string

	p.run("let it choose a password", p.click("#portal-password-generate"),
		chromedp.Value("#portal-password-field", &generated, chromedp.ByID))
	p.run("give it", p.click(".confirm-card button.confirm-proceed"))
	p.waitForText("#toast", "Portal access given")
	p.run("her row offers a new password",
		chromedp.WaitVisible(`#table-contact tbody button[data-action="portal-password"]`, chromedp.ByQuery))

	p.run("give her a new one", p.click(`#table-contact tbody button[data-action="portal-password"]`),
		chromedp.WaitVisible("#portal-password-field", chromedp.ByID))
	p.run("let it choose another", p.click("#portal-password-generate"),
		chromedp.Value("#portal-password-field", &generated, chromedp.ByID))
	p.run("set it", p.click(".confirm-card button.confirm-proceed"))
	p.waitForText("#toast", "New portal password set")

	p.run("take it away", p.click(`#table-contact tbody button[data-action="portal-take"]`),
		chromedp.WaitVisible(".confirm-overlay", chromedp.ByQuery),
		p.click(".confirm-actions button.danger"))
	p.waitForText("#toast", "Portal access taken away")
	p.run("her row offers the portal again",
		chromedp.WaitVisible(`#table-contact tbody button[data-action="portal-give"]`, chromedp.ByQuery))

	p.run("sign out", chromedp.Click("#logout", chromedp.ByID),
		chromedp.WaitVisible("#form-login", chromedp.ByID))
	p.signIn("ann@acme.example", generated)
	p.run("she is turned away", chromedp.WaitVisible("#login-error", chromedp.ByID))

	if !p.visible("#login-screen") {
		t.Error("the account whose portal access was taken away still signs in")
	}
}
