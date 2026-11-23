//go:build browser

package browser

import (
	"strings"
	"testing"

	"github.com/chromedp/chromedp"
)

// The overview counts the agent's own work, each queue's and the last two
// weeks', and every number leads somewhere: the day's tooltip says what its
// columns show, and a tile opens the ticket list searched for what it counts.
func TestTheOverviewCountsAnAgentsWorkAndOpensIt(t *testing.T) {
	t.Parallel()

	p := open(t)
	p.readyAdmin()

	var made bool

	p.awaitJSON(`(async () => {
		const send = (path, body) => api(path, { method: 'POST', body: JSON.stringify(body) });
		const desk = (await api('/queues')).items.find((queue) => queue.name === 'Service Desk');
		await send('/tickets', { title: 'Printer jams', queueId: desk.id, body: 'It jams.', ownerId: me.user.id });
		await send('/tickets', { title: 'Paper stuck', queueId: desk.id, body: 'Still stuck.' });
		await refreshAll();
		return true;
	})()`, &made)

	p.run("open the overview", p.click(`.tab[data-view="overview"]`),
		chromedp.WaitVisible("#overview-tiles", chromedp.ByID))
	p.waitEvaluates("the agent's own tickets counted",
		`document.querySelector('.overview-tile[data-tile="owned"] .overview-value').textContent`, "1")
	p.waitForText("#table-overview-queues tbody", "Service Desk")

	if desk := p.text("#table-overview-queues tbody tr:first-child"); !strings.Contains(desk, "2") {
		t.Errorf("the desk's row reads %q; want its two new tickets", desk)
	}

	p.waitEvaluates("the columns drawn", `String(document.querySelectorAll('#overview-chart .chart-bar').length > 0)`,
		"true")
	p.run("look at today", chromedp.Evaluate(
		`document.querySelector('#overview-chart .chart-hit:last-of-type').dispatchEvent(new Event('focus'))`, nil))
	p.waitEvaluates("today's numbers shown", `String(!document.querySelector('#overview-chart .chart-tip').hidden)`,
		"true")

	if tip := p.text("#overview-chart .chart-tip"); !strings.Contains(tip, "2") || !strings.Contains(tip, "Opened") {
		t.Errorf("today's tooltip says %q; want the two tickets opened", tip)
	}

	p.run("bring the chart into view", chromedp.ScrollIntoView("#overview-days-card", chromedp.ByID))
	p.saveShot(t, "overview.png")

	p.run("open what is mine", p.click(`.overview-tile[data-tile="owned"]`),
		chromedp.WaitVisible("#table-tickets", chromedp.ByID))
	p.waitEvaluates("the list narrowed to the agent's own",
		`String(document.querySelectorAll('#table-tickets tbody tr').length)`, "1")
	p.waitForText("#table-tickets tbody", "Printer jams")

	var mine bool

	p.run("is the list asking for mine", chromedp.Evaluate(
		`document.querySelector('#form-ticket-filter').elements.mine.checked`, &mine))

	if !mine {
		t.Error("the tile opened the ticket list without asking for the agent's own tickets")
	}
}
