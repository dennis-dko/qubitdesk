//go:build browser

package browser

import (
	"testing"

	"github.com/chromedp/chromedp"
)

// An account whose role may set communication up and nothing else finds the
// notifications and the answer templates on a screen of their own, and is
// offered the queues they name - through the rules' own choices, since it may
// not set the queues up.
func TestACommunicatorSetsCommunicationUpOnItsOwnScreen(t *testing.T) {
	t.Parallel()

	p := open(t)
	p.readyAdmin()

	var made bool

	p.awaitJSON(`(async () => {
		const send = (path, body) => api(path, { method: 'POST', body: JSON.stringify(body) });
		await send('/roles', { name: 'communicator', permissions: ['communication:write'] });
		await send('/users', { name: 'Cleo', email: 'cleo@example.com', role: 'communicator',
			password: 'cleo-password-1' });
		return true;
	})()`, &made)

	p.run("sign out", chromedp.Click("#logout", chromedp.ByID), chromedp.WaitVisible("#form-login", chromedp.ByID))
	p.signIn("cleo@example.com", "cleo-password-1")
	p.waitGone("#login-screen")
	p.settled()
	p.settleWelcome()

	if p.visible(`.tab[data-view="ticketsetup"]`) {
		t.Error("a communicator is offered the ticket setup, which it may not set up")
	}

	p.run("open the communication", p.click(`.tab[data-view="communication"]`),
		chromedp.WaitVisible("#notification-card", chromedp.ByID),
		chromedp.WaitVisible("#template-card", chromedp.ByID))
	p.run("offer a template in the desk", p.chooseOptionByText(`#form-template [name="queueId"]`, "Service Desk"))
}
