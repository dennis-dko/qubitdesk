//go:build browser

package browser

import (
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

// The question is asked when the installation is being put out of service, not
// whenever it already is.
//
// The comment over it says which: "Asked about only when switching it on.
// Turning it off needs no confirmation: that is the direction that ends an
// outage." The code asks whenever the box is ticked, which is not the same
// thing - an administrator who is out of service and wants to change the notice
// they are showing is asked whether to do the thing that has already been done.
//
// And the cancel path then puts the control somewhere it has never been. It sets
// the box to false, on the reasoning that the click which opened the question had
// just ticked it - true when the installation was in service, and wrong when it
// was not: the form is left saying "in service" over an installation that is out
// of it, until some other load puts it back.
func TestMaintenanceOnlyAsksWhenItIsBeingSwitchedOn(t *testing.T) {
	t.Parallel()

	p := open(t)
	p.readyAdmin()

	p.run("open the card", p.click(`.tab[data-view="admin"]`),
		p.click(`#admin-tabs [data-card-tab="operation"]`),
		chromedp.WaitVisible("#form-maintenance", chromedp.ByID))

	// Out of service, answering the question the first time - which is the time it
	// is meant to be asked.
	p.run("switch it on", chromedp.Evaluate(`(() => {
		const box = document.querySelector('#form-maintenance').elements.enabled;

		box.checked = true;
		box.dispatchEvent(new Event('change', { bubbles: true }));

		return true;
	})()`, nil))

	p.run("save", p.click(`#form-maintenance button[type="submit"]`),
		chromedp.WaitVisible(".confirm-card", chromedp.ByQuery),
		p.click(".confirm-card button.confirm-proceed"))

	p.waitShown("#maintenance-banner")

	// The administrator keeps their session - they are the one who ends this - so
	// the card is still there to save again.
	p.run("change only the notice", chromedp.Evaluate(`(() => {
		const form = document.querySelector('#form-maintenance');

		form.elements.message.value = 'Back at 14:00';
		form.elements.message.dispatchEvent(new Event('input', { bubbles: true }));

		return form.elements.enabled.checked;
	})()`, nil))

	p.run("save again", p.click(`#form-maintenance button[type="submit"]`))

	// Long enough for a dialog to have appeared if one was coming.
	time.Sleep(1500 * time.Millisecond)

	if p.visible(".confirm-card") {
		var asked string

		p.run("what it asks", chromedp.Evaluate(
			`document.querySelector('.confirm-card p').textContent.trim()`, &asked))

		t.Errorf("saving a changed notice on an installation that is already out of "+
			"service asked %q. The comment over the question says it is for "+
			"switching it on", asked)

		// Left in a state the rest of the case can read.
		p.run("dismiss", p.click(".confirm-card button.confirm-proceed"))
	}

	var checked bool

	p.run("is it still out of service", chromedp.Evaluate(
		`document.querySelector('#form-maintenance').elements.enabled.checked`,
		&checked))

	if !checked {
		t.Error("the box now says the installation is in service, and it is not")
	}
}

// What a save switched on is remembered as what the server says, even when
// somebody starts typing the notice before the reload after it has answered.
//
// The form learnt what the server last said only from a load, and a load leaves
// a form alone while somebody types in it. So a load triggered by the save's own
// announcement drew the banner, the administrator began the notice, and the
// reload the save itself ran found the form being typed in and left it: the form
// went on remembering "in service", and the next save asked whether to put the
// installation out of service - the full suite lost that race once. The order is
// forced here, by typing as that reload starts, rather than waited for.
func TestMaintenanceRemembersWhatItSavedWhileSomebodyTypes(t *testing.T) {
	t.Parallel()

	p := open(t)
	p.readyAdmin()

	p.run("open the card", p.click(`.tab[data-view="admin"]`),
		p.click(`#admin-tabs [data-card-tab="operation"]`),
		chromedp.WaitVisible("#form-maintenance", chromedp.ByID))

	p.run("type the notice as the reload after the save starts", chromedp.Evaluate(`(() => {
		const original = loadMaintenance;

		window.loadMaintenance = (...args) => {
			window.loadMaintenance = original;

			const form = document.querySelector('#form-maintenance');
			form.elements.message.value = 'Back at 14:00';
			form.elements.message.dispatchEvent(new Event('input', { bubbles: true }));

			return original(...args).finally(() => { window.reloadAfterTheSave = 'done'; });
		};

		const box = document.querySelector('#form-maintenance').elements.enabled;
		box.checked = true;
		box.dispatchEvent(new Event('change', { bubbles: true }));

		return true;
	})()`, nil))

	p.run("switch it on", p.click(`#form-maintenance button[type="submit"]`),
		chromedp.WaitVisible(".confirm-card", chromedp.ByQuery),
		p.click(".confirm-card button.confirm-proceed"))

	p.waitShown("#maintenance-banner")
	p.waitEvaluates("the reload after the save has run", `String(window.reloadAfterTheSave)`, "done")

	p.run("save the notice", p.click(`#form-maintenance button[type="submit"]`))

	// Long enough for a dialog to have appeared if one was coming.
	time.Sleep(1500 * time.Millisecond)

	if p.visible(".confirm-card") {
		t.Error("saving the notice asked again whether to put the installation out of service, " +
			"which the save before had already done")

		p.run("dismiss", p.click(".confirm-card button.confirm-proceed"))
	}
}
