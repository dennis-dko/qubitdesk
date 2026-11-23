//go:build browser

package browser

import (
	"strings"
	"testing"

	"github.com/chromedp/chromedp"
)

// The statistics count the span's tickets as figures, columns and rows: two
// tickets opened and one of them finished show as two and one, the rows follow
// what they are counted by, and the span can be chosen day by day.
func TestTheStatisticsCountTheSpansTickets(t *testing.T) {
	t.Parallel()

	p := open(t)
	p.readyAdmin()

	var made bool

	p.awaitJSON(`(async () => {
		const send = (path, body, method = 'POST') => api(path, { method, body: JSON.stringify(body) });
		const desk = (await api('/queues')).items.find((queue) => queue.name === 'Service Desk');
		const closed = (await api('/ticket-states')).items.find((state) => state.name === 'closed');
		const done = await send('/tickets', { title: 'Printer jams', queueId: desk.id, body: 'It jams.' });
		await send('/tickets', { title: 'Paper stuck', queueId: desk.id, body: 'Still stuck.' });
		await send('/tickets/' + done.id, { stateId: closed.id }, 'PUT');
		await refreshAll();
		return true;
	})()`, &made)

	p.run("open the overview", p.click(`.tab[data-view="overview"]`),
		chromedp.WaitVisible("#overview-tabs", chromedp.ByID))
	p.showTabOf("#report-card")
	p.run("wait for the statistics", chromedp.WaitVisible("#report-tiles", chromedp.ByID))
	p.waitEvaluates("the span's figures",
		`[...document.querySelectorAll('#report-tiles .report-tile')].slice(0, 2)
			.map((tile) => tile.querySelector('.overview-value').textContent).join('|')`, "2|1")
	p.waitForText("#table-report-rows tbody", "Service Desk")
	p.waitEvaluates("the columns drawn", `String(document.querySelectorAll('#report-chart .chart-bar').length > 0)`,
		"true")

	p.run("bring the columns into view", chromedp.ScrollIntoView("#report-days-card", chromedp.ByID))
	p.saveShot(t, "report.png")

	p.run("count by priority", chromedp.SetValue(`#form-report [name="by"]`, "priority", chromedp.ByQuery),
		chromedp.Evaluate(`document.querySelector('#form-report').dispatchEvent(new Event('change'))`, nil))
	p.waitEvaluates("the column headed by the priority", `document.querySelector('#report-rows-by').textContent`,
		"Priority")

	if rows := p.text("#table-report-rows tbody"); !strings.Contains(strings.ToLower(rows), "normal") {
		t.Errorf("counted by priority the rows read %q; want the normal priority's", rows)
	}

	// Over a quarter the columns are weeks, counted back from today; over a year,
	// the calendar months the span touches.
	for _, period := range []struct{ value, rows string }{
		{"90", "13"},
		{"year", `${new Set(reportShown.days.map((day) => day.day.slice(0, 7))).size}`},
	} {
		p.run("choose "+period.value, chromedp.SetValue(`#form-report [name="period"]`, period.value, chromedp.ByQuery),
			chromedp.Evaluate(`document.querySelector('#form-report').dispatchEvent(new Event('change'))`, nil))
		p.waitEvaluates("the span in "+period.value+"'s buckets",
			"String(document.querySelectorAll('#table-report-days tbody tr').length === Number(`"+period.rows+"`))",
			"true")
	}

	p.run("choose the days", chromedp.SetValue(`#form-report [name="period"]`, "custom", chromedp.ByQuery),
		chromedp.Evaluate(`document.querySelector('#form-report').dispatchEvent(new Event('change'))`, nil))
	p.waitShown(`#form-report [name="from"]`)
}
