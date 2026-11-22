//go:build browser

package browser

import (
	"testing"

	"github.com/chromedp/chromedp"
)

// An account whose role may set the automation up and nothing else is offered
// every priority and every account a step can name, through the automation's
// own choices: read through the rights of the tickets and the accounts, it was
// offered nothing to choose while the server took any record that is there.
func TestAnAutomatorIsOfferedWhatAStepCanName(t *testing.T) {
	t.Parallel()

	p := open(t)
	p.readyAdmin()

	var made bool

	p.awaitJSON(`(async () => {
		const send = (path, body) => api(path, { method: 'POST', body: JSON.stringify(body) });
		await send('/roles', { name: 'automator', permissions: ['automation:write'] });
		await send('/users', { name: 'Otto', email: 'otto@example.com', role: 'automator',
			password: 'otto-password-1' });
		await send('/users', { name: 'Agnes', email: 'agnes@example.com', role: 'agent',
			password: 'agnes-password-1' });
		return true;
	})()`, &made)

	p.run("sign out", chromedp.Click("#logout", chromedp.ByID), chromedp.WaitVisible("#form-login", chromedp.ByID))
	p.signIn("otto@example.com", "otto-password-1")
	p.waitGone("#login-screen")
	p.settled()
	p.settleWelcome()

	p.run("open the automation", p.click(`.tab[data-view="automation"]`),
		chromedp.WaitVisible("#form-macro", chromedp.ByID))
	p.run("raise the priority", p.chooseOptionByText("#macro-step-kind", "Set the priority"),
		p.click("#macro-add-step"),
		p.chooseOptionByText(`#macro-steps > li:nth-child(1) [data-parameter="priorityId"]`, "very high"))
	p.run("give it to Agnes", p.chooseOptionByText("#macro-step-kind", "Set the owner"),
		p.click("#macro-add-step"),
		p.chooseOptionByText(`#macro-steps > li:nth-child(2) [data-parameter="ownerId"]`, "Agnes"))
}
