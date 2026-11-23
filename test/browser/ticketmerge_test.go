//go:build browser

package browser

import (
	"fmt"
	"strings"
	"testing"

	"github.com/chromedp/chromedp"
)

// A ticket is merged into another from its card: the ticket to merge into is
// found by its words, the merge is confirmed, and the ticket it went into opens
// holding both tickets' articles, while the merged one says where it went and
// offers no second merge.
func TestATicketIsMergedFromItsCard(t *testing.T) {
	t.Parallel()

	p := open(t)
	p.readyAdmin()

	var made struct {
		Printer uint `json:"printer"`
		Again   uint `json:"again"`
	}

	p.awaitJSON(`(async () => {
		const send = (path, body) => api(path, { method: 'POST', body: JSON.stringify(body) });
		const desk = (await api('/queues')).items.find((queue) => queue.name === 'Service Desk');
		const printer = await send('/tickets', { title: 'Printer jams', queueId: desk.id, body: 'It jams.' });
		const again = await send('/tickets', { title: 'Paper stuck', queueId: desk.id, body: 'Still stuck.' });
		await refreshAll();
		return { printer: printer.id, again: again.id };
	})()`, &made)

	var opened bool

	p.run("open the tickets", p.click(`.tab[data-view="tickets"]`))
	p.awaitJSON(fmt.Sprintf(`(async () => { await openTicket(%d); return true; })()`, made.Again), &opened)
	p.run("find the one to merge into", chromedp.WaitVisible("#form-ticket-merge", chromedp.ByID),
		chromedp.SendKeys(`#form-ticket-merge input[name="find"]`, "Printer", chromedp.ByQuery))
	p.waitEvaluates("the printer's ticket offered", fmt.Sprintf(
		`String(Boolean(document.querySelector('#form-ticket-merge [name="intoId"] option[value="%d"]')))`,
		made.Printer), "true")
	p.run("merge it", chromedp.Evaluate(fmt.Sprintf(
		`document.querySelector('#form-ticket-merge').elements.intoId.value = '%d'`, made.Printer), nil),
		p.click(`#form-ticket-merge button[type="submit"]`),
		chromedp.WaitVisible(".confirm-card", chromedp.ByQuery),
		p.click(".confirm-card button.confirm-proceed"))
	p.waitForText("#toast", "Ticket merged")
	p.waitForText("#ticket-heading", "Printer jams")

	if articles := p.count("#ticket-articles > li"); articles != 2 {
		t.Errorf("the ticket merged into shows %d articles, want both tickets' two", articles)
	}

	p.awaitJSON(fmt.Sprintf(`(async () => { await openTicket(%d); return true; })()`, made.Again), &opened)
	p.waitForText("#ticket-heading", "Paper stuck")

	if facts := p.text("#ticket-facts"); !strings.Contains(facts, "Merged into") {
		t.Errorf("the merged ticket's facts read %q; want them to say where it went", facts)
	}

	if p.visible("#form-ticket-merge") {
		t.Error("the merged ticket offers to be merged again")
	}
}
