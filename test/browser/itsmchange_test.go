//go:build browser

package browser

import (
	"strings"
	"testing"

	"github.com/chromedp/chromedp"
)

// A change is added on the Changes screen, moved on as its states allow and
// given a work order; the agent the work order is given to, who may only see
// changes, opens the same screen with nothing to change but the report, and
// reports.
func TestAChangeIsCarriedOutInWorkOrdersOnTheChangesScreen(t *testing.T) {
	t.Parallel()

	p := open(t)
	p.readyAdmin()
	p.createOrdinaryAccount(t, workerEmail, workerPassword)

	var refreshed bool

	p.awaitJSON(`(async () => { await refreshAll(); return true; })()`, &refreshed)

	p.run("open the changes", p.click(`.tab[data-view="changes"]`),
		chromedp.WaitVisible("#itsmchange-card", chromedp.ByID))
	p.run("add a change", p.click("#itsmchange-new"), chromedp.WaitVisible("#itsmchange-detail", chromedp.ByID))
	p.run("name it", chromedp.Evaluate(`(() => {
		const title = document.querySelector('#form-itsmchange').elements.title;
		title.value = 'Replace the core switch';
		title.dispatchEvent(new Event('input', { bubbles: true }));
	})()`, nil), p.click("#itsmchange-submit"))
	p.waitForText("#toast", "Change added")
	p.waitForText("#itsmchange-standing", "State: requested")

	p.run("move it on", p.chooseOptionByText(`#form-itsmchange-move [name="stateId"]`, "pending approval"),
		p.click(`#form-itsmchange-move button[type="submit"]`))
	p.waitForText("#toast", "Change moved on")
	p.waitForText("#itsmchange-standing", "State: pending approval")

	p.run("add a work order", p.click("#workorder-new"), chromedp.WaitVisible("#form-workorder", chromedp.ByID))
	p.run("plan it for the agent", p.chooseOptionByText(`#form-workorder [name="agentId"]`, "Erika"),
		chromedp.Evaluate(`(() => {
			const form = document.querySelector('#form-workorder');
			form.elements.title.value = 'Install the switch';
			form.elements.title.dispatchEvent(new Event('input', { bubbles: true }));
			setDateField(form.elements.plannedStartDay, '2026-12-01');
			form.elements.plannedStartTime.value = '08:00';
			setDateField(form.elements.plannedEndDay, '2026-12-01');
			form.elements.plannedEndTime.value = '12:00';
		})()`, nil), p.click("#workorder-submit"))
	p.waitForText("#toast", "Work order added")
	p.waitForText("#table-workorder tbody", "Install the switch")

	if standing := p.text("#itsmchange-standing"); strings.Contains(standing, "Planned: –") {
		t.Errorf("the change stands as %q; want it planned once its one work order is", standing)
	}

	p.run("sign out", chromedp.Click("#logout", chromedp.ByID), chromedp.WaitVisible("#form-login", chromedp.ByID))

	// Nothing of the change stays in the document under the sign-in screen,
	// hidden or not, for whoever signs in next.
	if left := p.text("#itsmchange-detail"); strings.Contains(left, "Replace the core switch") ||
		strings.Contains(left, "pending approval") {
		t.Errorf("after the sign-out the change's card still holds %q", left)
	}

	p.signIn(workerEmail, workerPassword)
	p.waitGone("#login-screen")
	p.settled()
	p.settleWelcome()

	p.run("open the changes as the agent", p.click(`.tab[data-view="changes"]`),
		chromedp.WaitVisible("#itsmchange-card", chromedp.ByID))
	p.run("open the change", p.click(`#table-itsmchange tbody button.link`),
		chromedp.WaitVisible("#itsmchange-detail", chromedp.ByID))
	p.run("open the work order", p.click(`#table-workorder tbody button.link`),
		chromedp.WaitVisible("#form-workorder", chromedp.ByID))

	var reader struct {
		Change    bool     `json:"change"`
		Plan      bool     `json:"plan"`
		Report    bool     `json:"report"`
		Pressable []string `json:"pressable"`
	}

	p.evalJSON(`JSON.stringify({
		change: document.querySelector('#itsmchange-fields').disabled,
		plan: document.querySelector('#workorder-plan').disabled,
		report: document.querySelector('#workorder-report').disabled,
		pressable: ['#itsmchange-new', '#itsmchange-submit', '#itsmchange-delete', '#form-itsmchange-move',
			'#workorder-new', '#workorder-delete', '#workorder-submit']
			.filter((id) => document.querySelector(id).checkVisibility()),
	})`, &reader)

	if !reader.Change || !reader.Plan || reader.Report || len(reader.Pressable) != 1 ||
		reader.Pressable[0] != "#workorder-submit" {
		t.Errorf("the agent opens the work order as %+v; want the change and the plan read only, the report open, "+
			"and only its own save to press", reader)
	}

	p.run("report on it", p.chooseOptionByText(`#form-workorder [name="stateId"]`, "accepted"),
		chromedp.Evaluate(`(() => {
			const form = document.querySelector('#form-workorder');
			form.elements.report.value = 'Racked and cabled.';
			form.elements.report.dispatchEvent(new Event('input', { bubbles: true }));
			form.elements.minutes.value = '45';
		})()`, nil), p.click("#workorder-submit"))
	p.waitForText("#toast", "Work order saved")
	p.waitForText("#table-workorder tbody", "45 minutes")
	p.waitForText("#table-workorder tbody", "accepted")
}
