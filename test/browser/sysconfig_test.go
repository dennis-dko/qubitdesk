//go:build browser

package browser

import (
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
