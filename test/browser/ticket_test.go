//go:build browser

package browser

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

// A ticket is opened from the screen, opens itself once it is there, and is
// worked where it is shown: written in, locked - which makes the one who locked
// it its owner - and its history tells what happened.
func TestATicketIsOpenedAndWorkedOnTheScreen(t *testing.T) {
	t.Parallel()

	p := open(t)
	p.readyAdmin()

	p.run("open the tickets", p.click(`.tab[data-view="tickets"]`),
		chromedp.WaitVisible("#form-ticket", chromedp.ByID))
	p.waitForValue(`#form-ticket [name="queueId"]`)

	p.run("describe a ticket",
		chromedp.SetValue(`#form-ticket [name="title"]`, "Printer jams on every page", chromedp.ByQuery),
		chromedp.SetValue(`#form-ticket [name="body"]`, "The printer on the second floor jams.", chromedp.ByQuery),
		p.click("#ticket-submit"))

	p.run("the ticket opens", chromedp.WaitVisible("#ticket-detail", chromedp.ByID))
	p.waitForText("#ticket-heading", "Printer jams on every page")
	p.waitForText("#ticket-articles", "The printer on the second floor jams.")

	if got := p.text("#ticket-form-title"); !strings.Contains(got, strings.Fields(p.text("#ticket-heading"))[0]) {
		t.Errorf("the form reads %q while the ticket is open; want it holding the ticket for correction", got)
	}

	if p.visible(`#form-ticket [name="body"]`) {
		t.Error("the form offers the text a ticket is opened with while it corrects an open one")
	}

	p.run("write in it",
		chromedp.SetValue(`#form-article [name="body"]`, "Replaced the roller.", chromedp.ByQuery),
		chromedp.SetValue(`#form-article [name="minutes"]`, "15", chromedp.ByQuery),
		p.click(`#form-article button[type="submit"]`))
	p.waitForText("#ticket-articles", "Replaced the roller.")
	p.waitForText("#ticket-facts", "15")

	p.run("lock it", p.click("#ticket-lock"))
	p.waitForText("#ticket-lock", "Unlock")

	if facts := p.text("#ticket-facts"); strings.Contains(facts, "nobody") && !strings.Contains(facts, "Admin") {
		t.Errorf("locking a ticket nobody owned left it without an owner: %q", facts)
	}

	p.run("read its history", p.click("#ticket-history summary"))

	for _, want := range []string{"Opened the ticket", "Wrote a note", "Noted 15 minutes", "Locked it"} {
		p.waitForText("#ticket-history-list", want)
	}

	p.run("back to the list", p.click("#ticket-close"))

	if p.visible("#ticket-detail") {
		t.Error("going back to the list left the ticket open")
	}

	if got := p.text("#table-tickets tbody"); !strings.Contains(got, "Printer jams on every page") {
		t.Errorf("the list does not hold the ticket just opened: %q", got)
	}
}

// A moment to wait for is typed as a day and a time in the reader's zone and sent
// as the moment it names, in winter and in summer alike - the zone moves its
// clocks, and the offset that is right in January is an hour wrong in July.
func TestAMomentToWaitForIsReadInTheReadersZone(t *testing.T) {
	t.Parallel()

	p := open(t)
	p.readyAdmin()

	var moments struct {
		Winter    string `json:"winter"`
		Summer    string `json:"summer"`
		BackDay   string `json:"backDay"`
		BackTime  string `json:"backTime"`
		NewYorker string `json:"newYorker"`
	}

	p.evalJSON(`JSON.stringify({
		winter: momentIn('2026-01-15', '09:00', 'Europe/Berlin'),
		summer: momentIn('2026-07-15', '09:00', 'Europe/Berlin'),
		backDay: dayAndTimeIn('2026-07-15T07:00:00.000Z', 'Europe/Berlin').day,
		backTime: dayAndTimeIn('2026-07-15T07:00:00.000Z', 'Europe/Berlin').time,
		newYorker: momentIn('2026-07-15', '09:00', 'America/New_York'),
	})`, &moments)

	for name, pair := range map[string][2]string{
		"nine in a Berlin winter":            {moments.Winter, "2026-01-15T08:00:00.000Z"},
		"nine in a Berlin summer":            {moments.Summer, "2026-07-15T07:00:00.000Z"},
		"nine in a New York summer":          {moments.NewYorker, "2026-07-15T13:00:00.000Z"},
		"seven UTC read in Berlin, the day":  {moments.BackDay, "2026-07-15"},
		"seven UTC read in Berlin, the time": {moments.BackTime, "09:00"},
	} {
		if pair[0] != pair[1] {
			t.Errorf("%s came out as %q, want %q", name, pair[0], pair[1])
		}
	}
}

// A ticket the reader's roles do not let them change is shown and not offered
// for change: an agent opens a ticket of a queue the agents only read.
func TestATicketInAQueueOnlyReadIsShownAndNotOfferedForChange(t *testing.T) {
	t.Parallel()

	p := open(t)
	p.readyAdmin()

	var opened bool

	p.awaitJSON(`(async () => {
		const send = (path, body) => api(path, { method: 'POST', body: JSON.stringify(body) });
		const roles = await api('/roles');
		const agent = roles.items.find((role) => role.name === 'agent');
		const queue = await send('/queues', { name: 'Finance', access: [{ roleId: agent.id, level: 'read' }] });
		await send('/tickets', { title: 'An invoice', queueId: queue.id, body: 'Please pay.' });
		return true;
	})()`, &opened)

	p.becomeWorker()

	p.run("open the tickets", p.click(`.tab[data-view="tickets"]`),
		chromedp.WaitVisible("#table-tickets", chromedp.ByID))
	p.waitForText("#table-tickets tbody", "An invoice")

	p.run("open the invoice", chromedp.Evaluate(`[...document.querySelectorAll('#table-tickets tbody tr')]
		.find((row) => row.textContent.includes('An invoice')).querySelector('button.link').click()`, nil))
	p.run("it opens", chromedp.WaitVisible("#ticket-detail", chromedp.ByID))

	deadline := time.Now().Add(waitPatience)
	for time.Now().Before(deadline) && !strings.Contains(p.text("#ticket-heading"), "An invoice") {
		time.Sleep(200 * time.Millisecond)
	}

	if !strings.Contains(p.text("#ticket-articles"), "Please pay.") {
		t.Errorf("the agent cannot read what was written in a ticket of a queue they read: %q",
			p.text("#ticket-articles"))
	}

	for _, control := range []string{"#ticket-lock", "#form-article"} {
		if p.visible(control) {
			t.Errorf("%s is offered on a ticket of a queue the agents only read, and the server refuses it", control)
		}
	}

	if got := p.text("#ticket-form-title"); strings.Contains(got, "An invoice") || strings.Contains(got, "Correct") {
		t.Errorf("the form holds a ticket of a queue the agents only read: %q", got)
	}
}

// A correction that has been saved leaves the form free to follow the ticket.
//
// The form is protected while somebody fills it in, so that reading the ticket
// again does not write over their work - and the correction's save did not end
// that, so a saved form stayed protected. Locking the ticket next made the one
// who locked it its owner on the server and left the form saying nobody, and
// the next correction sent that nobody back: the owner the lock had just set
// was taken off again.
func TestASavedCorrectionLetsTheFormFollowTheTicket(t *testing.T) {
	t.Parallel()

	p := open(t)
	p.readyAdmin()

	var made struct {
		Ticket uint `json:"ticket"`
		Me     uint `json:"me"`
	}

	p.awaitJSON(`(async () => {
		const queue = (await api('/queues')).items[0];
		const ticket = await api('/tickets', { method: 'POST', body: JSON.stringify(
			{ title: 'Scanner', queueId: queue.id, body: 'It scans blank pages.' }) });
		await refreshAll();
		return { ticket: ticket.id, me: me.user.id };
	})()`, &made)

	p.run("open the tickets", p.click(`.tab[data-view="tickets"]`),
		chromedp.WaitVisible("#table-tickets", chromedp.ByID))
	p.waitForText("#table-tickets tbody", "Scanner")
	p.run("open the scanner", chromedp.Evaluate(`[...document.querySelectorAll('#table-tickets tbody tr')]
		.find((row) => row.textContent.includes('Scanner')).querySelector('button.link').click()`, nil),
		chromedp.WaitVisible("#ticket-detail", chromedp.ByID))
	p.waitForValue(`#form-ticket [name="id"]`)

	p.run("correct its title",
		chromedp.SetValue(`#form-ticket [name="title"]`, "Scanner scans blank pages", chromedp.ByQuery),
		p.click("#ticket-submit"))
	p.waitForText("#ticket-heading", "Scanner scans blank pages")

	p.run("lock it", p.click("#ticket-lock"))
	p.waitForText("#ticket-lock", "Unlock")

	var owner string

	for deadline := time.Now().Add(waitPatience); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		if owner = p.value(`#form-ticket [name="ownerId"]`); owner != "" {
			break
		}
	}

	if owner == "" {
		t.Error("the form went on saying nobody owns the ticket after locking it made its locker the owner")
	}

	var priority string

	p.run("correct its priority", chromedp.Evaluate(`(() => {
		const select = document.querySelector('#form-ticket [name="priorityId"]');
		select.value = [...select.options].find((option) => option.value !== select.value).value;
		select.dispatchEvent(new Event('change', { bubbles: true }));
		return select.value;
	})()`, &priority), p.click("#ticket-submit"))

	var saved struct {
		PriorityID uint  `json:"priorityId"`
		OwnerID    *uint `json:"ownerId"`
	}

	for deadline := time.Now().Add(waitPatience); ; time.Sleep(300 * time.Millisecond) {
		p.awaitJSON(`api('/tickets/`+fmt.Sprint(made.Ticket)+`')`, &saved)

		if fmt.Sprint(saved.PriorityID) == priority || time.Now().After(deadline) {
			break
		}
	}

	if fmt.Sprint(saved.PriorityID) != priority {
		t.Fatalf("the corrected priority never reached the server: it reads %d, want %s", saved.PriorityID, priority)
	}

	if saved.OwnerID == nil || *saved.OwnerID != made.Me {
		t.Errorf("correcting the priority after locking left the owner %v, want %d", saved.OwnerID, made.Me)
	}
}
