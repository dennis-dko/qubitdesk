//go:build browser

package browser

import (
	"strings"
	"testing"

	"github.com/chromedp/chromedp"
)

// A service level is made on the setup screen, the queue's form names it for
// the tickets the queue opens, and a ticket opened there says which level it is
// owed under and by when its first answer, its next answer and its solution
// are owed.
func TestAServiceLevelIsMadeNamedByAQueueAndShownOnATicket(t *testing.T) {
	t.Parallel()

	p := open(t)
	p.readyAdmin()

	p.run("open the setup", p.click(`.tab[data-view="ticketsetup"]`),
		chromedp.WaitVisible("#form-sla", chromedp.ByID))

	p.run("make Gold",
		chromedp.SetValue(`#form-sla [name="name"]`, "Gold", chromedp.ByQuery),
		chromedp.SetValue(`#form-sla [name="firstResponseMinutes"]`, "60", chromedp.ByQuery),
		chromedp.SetValue(`#form-sla [name="updateMinutes"]`, "240", chromedp.ByQuery),
		chromedp.SetValue(`#form-sla [name="solutionMinutes"]`, "480", chromedp.ByQuery),
		p.click("#sla-submit"))
	p.waitForText("#table-sla tbody", "Gold")

	if row := p.text("#table-sla tbody"); !strings.Contains(row, "around the clock") {
		t.Errorf("a level with no calendar reads %q; want it counting around the clock", row)
	}

	p.run("correct the desk", chromedp.Evaluate(`[...document.querySelectorAll('#table-queue tbody tr')]
		.find((row) => row.textContent.includes('Service Desk')).querySelector('button.link:not(.danger)').click()`, nil))
	p.waitForText("#queue-form-title", "Service Desk")
	p.run("name Gold for it",
		p.chooseOptionByText(`#form-queue [name="serviceLevelId"]`, "Gold"),
		p.click("#queue-submit"))
	p.waitForText("#queue-form-title", "Add a queue")

	var opened bool

	p.awaitJSON(`(async () => {
		const queues = await api('/queues');
		const desk = queues.items.find((queue) => queue.name === 'Service Desk');
		if (!desk.serviceLevelId) return false;
		await api('/tickets', { method: 'POST', body: JSON.stringify(
			{ title: 'Printer', queueId: desk.id, body: 'It jams.' }) });
		await refreshAll();
		return true;
	})()`, &opened)

	if !opened {
		t.Fatal("the desk's form did not name Gold for the desk")
	}

	p.run("open the tickets", p.click(`.tab[data-view="tickets"]`),
		chromedp.WaitVisible("#table-tickets", chromedp.ByID))
	p.waitForText("#table-tickets tbody", "Printer")
	p.run("open the printer", chromedp.Evaluate(`[...document.querySelectorAll('#table-tickets tbody tr')]
		.find((row) => row.textContent.includes('Printer')).querySelector('button.link').click()`, nil),
		chromedp.WaitVisible("#ticket-detail", chromedp.ByID))
	p.waitForText("#ticket-facts", "Gold")

	if facts := p.text("#ticket-facts"); !strings.Contains(facts, "First answer owed by") ||
		!strings.Contains(facts, "Next answer owed by") || !strings.Contains(facts, "Solution owed by") {
		t.Errorf("the ticket's facts read %q; want when its first answer, its next answer and its solution are owed",
			facts)
	}
}
