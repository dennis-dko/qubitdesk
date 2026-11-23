//go:build browser

package browser

import (
	"testing"

	"github.com/chromedp/chromedp"
)

// The ticket list is searched by what its tickets say: the more criteria fold
// out of the filter, words typed there narrow the list to the ticket that says
// them in an article, the order puts the most urgent first, clearing the
// criteria brings every ticket back - and signing out leaves nothing of the
// search for whoever signs in next, as it leaves nothing of any form.
func TestTheTicketListIsSearchedByWhatTicketsSay(t *testing.T) {
	t.Parallel()

	p := open(t)
	p.readyAdmin()

	var made bool

	p.awaitJSON(`(async () => {
		const send = (path, body) => api(path, { method: 'POST', body: JSON.stringify(body) });
		const desk = (await api('/queues')).items.find((queue) => queue.name === 'Service Desk');
		const urgent = (await api('/ticket-priorities')).items.find((priority) => priority.name === 'very-high');
		await send('/tickets', { title: 'Printer jams', queueId: desk.id, body: 'It jams on every page.' });
		const flooded = await send('/tickets', { title: 'Water in the cellar', queueId: desk.id,
			body: 'Somebody noticed a smell.' });
		await send('/tickets/' + flooded.id + '/articles', { body: 'The basement is flooded.' });
		await send('/tickets', { title: 'Server down', queueId: desk.id, body: 'Nothing answers.',
			priorityId: urgent.id });
		await refreshAll();
		return true;
	})()`, &made)

	p.run("open the tickets", p.click(`.tab[data-view="tickets"]`),
		chromedp.WaitVisible("#table-tickets", chromedp.ByID))
	p.waitForText("#table-tickets tbody", "Printer jams")

	if p.visible(`#form-ticket-filter [name="text"]`) {
		t.Error("the search's criteria are out before anybody asked for them")
	}

	p.run("fold the criteria out", p.click("#ticket-search > summary"),
		chromedp.WaitVisible(`#form-ticket-filter [name="text"]`, chromedp.ByQuery),
		chromedp.SendKeys(`#form-ticket-filter [name="text"]`, "basement", chromedp.ByQuery))
	p.waitEvaluates("the list narrowed to what said it",
		`String(document.querySelectorAll('#table-tickets tbody tr').length)`, "1")
	p.waitForText("#table-tickets tbody", "Water in the cellar")

	p.run("ask for every ticket, the most urgent first", p.click("#ticket-search-clear"),
		chromedp.SetValue(`#form-ticket-filter [name="sort"]`, "priority", chromedp.ByQuery),
		chromedp.Evaluate(`document.querySelector('#form-ticket-filter')
			.dispatchEvent(new Event('change'))`, nil))
	p.waitEvaluates("every ticket back, the most urgent first",
		`[...document.querySelectorAll('#table-tickets tbody tr')].map((row) => row.cells[1].textContent).join('|')`,
		"Server down|Printer jams|Water in the cellar")

	p.run("search again", chromedp.SendKeys(`#form-ticket-filter [name="text"]`, "basement", chromedp.ByQuery))
	p.waitEvaluates("the list narrowed again",
		`String(document.querySelectorAll('#table-tickets tbody tr').length)`, "1")

	p.run("sign out", chromedp.Click("#logout", chromedp.ByID), chromedp.WaitVisible("#form-login", chromedp.ByID))
	p.readyAdminAgain()

	var left struct {
		Text string `json:"text"`
		Sort string `json:"sort"`
	}

	p.awaitJSON(`(async () => {
		const filter = document.querySelector('#form-ticket-filter');
		return { text: filter.elements.text.value, sort: filter.elements.sort.value };
	})()`, &left)

	if left.Text != "" || left.Sort != "changed" {
		t.Errorf("after signing out and in, the search still asks for %+v", left)
	}
}
