//go:build browser

package browser

import (
	"testing"

	"github.com/chromedp/chromedp"
)

// An agent reads on the Tickets screen what the notifications told them, the
// ones not read yet standing out: opening a ticket from there opens it and
// reads its notification, and the rest are marked read at once.
func TestAnAgentReadsWhatTheyWereToldOnTheTicketsScreen(t *testing.T) {
	t.Parallel()

	p := open(t)
	p.readyAdmin()

	// The agent is there before the tickets are opened, because a notification is
	// sent to whoever is around the ticket when it happens.
	p.createOrdinaryAccount(t, workerEmail, workerPassword)

	var made bool

	p.awaitJSON(`(async () => {
		const send = (path, body) => api(path, { method: 'POST', body: JSON.stringify(body) });
		await send('/notification-definitions', { name: 'Opened', events: ['TicketCreate'],
			recipients: ['AgentWritePermissions'],
			messages: [{ language: 'en', subject: 'New: <KIX_TICKET_Title>', body: 'Opened at the desk.' }] });
		const desk = (await api('/queues')).items.find((queue) => queue.name === 'Service Desk');
		for (const title of ['Printer', 'Scanner']) {
			await send('/tickets', { title, queueId: desk.id, body: 'It jams.' });
		}
		return true;
	})()`, &made)

	if !made {
		t.Fatal("could not set the notification up and open the tickets")
	}

	p.run("sign out", chromedp.Click("#logout", chromedp.ByID), chromedp.WaitVisible("#form-login", chromedp.ByID))
	p.signIn(workerEmail, workerPassword)
	p.waitGone("#login-screen")
	p.settled()
	p.settleWelcome()

	p.run("open the tickets", p.click(`.tab[data-view="tickets"]`), chromedp.WaitVisible("#told-card", chromedp.ByID))
	p.waitForText("#table-told tbody", "New: Scanner")

	if unread := p.count("#table-told tbody tr.unread"); unread != 2 || !p.visible("#told-all-read") {
		t.Fatalf("the agent is shown %d unread, the button to read them all shown %v; want two and the button",
			unread, p.visible("#told-all-read"))
	}

	p.run("open the printer from what was said", chromedp.Evaluate(`[...document.querySelectorAll('#table-told tbody tr')]
		.find((row) => row.textContent.includes('New: Printer')).querySelector('td.actions button.link').click()`, nil),
		chromedp.WaitVisible("#ticket-detail", chromedp.ByID))
	p.waitForText("#ticket-heading", "Printer")

	if unread := p.count("#table-told tbody tr.unread"); unread != 1 {
		t.Errorf("opening the printer from its notification leaves %d unread, want the scanner's alone", unread)
	}

	p.run("read the rest", p.click("#told-all-read"))
	p.waitGone("#table-told tbody tr.unread")

	if p.visible("#told-all-read") {
		t.Error("with everything read the button to read everything is still offered")
	}
}
