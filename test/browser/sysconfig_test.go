//go:build browser

package browser

import (
	"strconv"
	"strings"
	"testing"

	"github.com/chromedp/chromedp"
)

// An option changed on the system configuration's card is obeyed at once: a
// ticket is named after the installation's own word for one, with a divider
// whose space is kept as typed, and putting the options back to their defaults
// names it as KIX18 does.
func TestTheSystemConfigurationNamesTheTickets(t *testing.T) {
	t.Parallel()

	p := open(t)
	p.readyAdmin()

	var made bool

	p.awaitJSON(`(async () => {
		const queue = (await api('/queues')).items[0];
		await api('/tickets', { method: 'POST', body: JSON.stringify(
			{ title: 'Printer', queueId: queue.id, body: 'It jams.' }) });
		await refreshAll();
		return true;
	})()`, &made)

	p.run("open the settings", p.click(`.tab[data-view="admin"]`),
		chromedp.WaitVisible("#table-sysconfig", chromedp.ByID))
	p.waitForText("#table-sysconfig tbody", "Ticket::Hook")

	set := func(name, value string) {
		p.run("change "+name, chromedp.Evaluate(`[...document.querySelectorAll('#table-sysconfig tbody tr')]
			.find((row) => row.querySelector('code')?.textContent === `+jsString(name)+`)
			.querySelector('button.link').click()`, nil),
			chromedp.WaitVisible("#form-sysconfig", chromedp.ByID),
			chromedp.SetValue(`#form-sysconfig [name="text"]`, value, chromedp.ByQuery),
			p.click("#sysconfig-submit"))
		p.waitGone("#form-sysconfig")
	}

	set("Ticket::Hook", "Call#")
	set("Ticket::HookDivider", ": ")
	p.waitForText("#table-sysconfig tbody", "Call# · changed")

	heading := func() string {
		p.run("open the tickets", p.click(`.tab[data-view="tickets"]`),
			chromedp.WaitVisible("#table-tickets", chromedp.ByID))
		p.waitForText("#table-tickets tbody", "Printer")
		p.run("open the printer", chromedp.Evaluate(`[...document.querySelectorAll('#table-tickets tbody tr')]
			.find((row) => row.textContent.includes('Printer')).querySelector('button.link').click()`, nil),
			chromedp.WaitVisible("#ticket-detail", chromedp.ByID))
		p.waitForText("#ticket-heading", "Printer")

		return p.text("#ticket-heading")
	}

	if got := heading(); !strings.HasPrefix(got, "Call#: 2026") {
		t.Errorf("the ticket is headed %q; want it named Call#: and its number", got)
	}

	p.run("back to the settings", p.click(`.tab[data-view="admin"]`),
		chromedp.WaitVisible("#table-sysconfig", chromedp.ByID))

	for _, name := range []string{"Ticket::Hook", "Ticket::HookDivider"} {
		p.run("put "+name+" back", chromedp.Evaluate(`[...document.querySelectorAll('#table-sysconfig tbody tr')]
			.find((row) => row.querySelector('code')?.textContent === `+jsString(name)+`)
			.querySelector('button.link').click()`, nil),
			chromedp.WaitVisible("#sysconfig-default", chromedp.ByID),
			p.click("#sysconfig-default"))
		p.waitGone("#form-sysconfig")
	}

	if changed := p.text("#table-sysconfig tbody"); strings.Contains(changed, "changed") {
		t.Errorf("after both were put back the table still says %q", changed)
	}

	if got := heading(); !strings.HasPrefix(got, "Ticket#2026") {
		t.Errorf("the ticket is headed %q; want KIX18's Ticket# and its number", got)
	}
}

// The installation's calendar is chosen on the system configuration's card
// from the calendars still offered, around the clock first, and the option's
// row then names it.
func TestTheInstallationsCalendarIsChosenOnTheCard(t *testing.T) {
	t.Parallel()

	p := open(t)
	p.readyAdmin()

	var office int

	p.awaitJSON(`(async () => {
		const made = await api('/calendars', { method: 'POST', body: JSON.stringify(
			{ name: 'Office', hours: [{ weekday: 1, from: '08:00', to: '17:00' }] }) });
		await api('/calendars', { method: 'POST', body: JSON.stringify({ name: 'Old office', valid: false }) });
		await refreshAll();
		return made.id;
	})()`, &office)

	p.run("open the settings", p.click(`.tab[data-view="admin"]`),
		chromedp.WaitVisible("#table-sysconfig", chromedp.ByID))
	p.waitForText("#table-sysconfig tbody", "TimeWorkingHours")

	row := `[...document.querySelectorAll('#table-sysconfig tbody tr')]
		.find((row) => row.querySelector('code')?.textContent === 'TimeWorkingHours')`

	if got := p.text("#table-sysconfig tbody"); !strings.Contains(got, "around the clock") {
		t.Errorf("before a calendar is named the card reads %q; want around the clock", got)
	}

	p.run("open the option", chromedp.Evaluate(row+`.querySelector('button.link').click()`, nil),
		chromedp.WaitVisible(`#form-sysconfig [name="calendar"]`, chromedp.ByQuery))

	var offered []string

	p.evalJSON(`JSON.stringify([...document.querySelectorAll('#form-sysconfig [name="calendar"] option')]
		.map((option) => option.textContent))`, &offered)

	if strings.Join(offered, "|") != "around the clock|Office" {
		t.Errorf("the calendars offered are %q; want around the clock first and the one still offered", offered)
	}

	p.run("choose the office", p.chooseOption(`#form-sysconfig [name="calendar"]`, strconv.Itoa(office)),
		p.click("#sysconfig-submit"))
	p.waitGone("#form-sysconfig")

	var named string

	p.evalJSON(`JSON.stringify(`+row+`.textContent)`, &named)

	if !strings.Contains(named, "Office · changed") {
		t.Errorf("after choosing the office the option's row reads %q", named)
	}
}
