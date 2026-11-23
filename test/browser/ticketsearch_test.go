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

// A search is saved under a name and run again from it: kept from the filter
// as it stands, offered among the agent's saved searches, put back into the
// filter when chosen - the further criteria folded out - and let go of again.
// Signing out leaves none of them for whoever signs in next.
func TestASearchIsSavedAndRunAgainFromTheTicketList(t *testing.T) {
	t.Parallel()

	p := open(t)
	p.readyAdmin()

	var made bool

	p.awaitJSON(`(async () => {
		const send = (path, body) => api(path, { method: 'POST', body: JSON.stringify(body) });
		const desk = (await api('/queues')).items.find((queue) => queue.name === 'Service Desk');
		await send('/tickets', { title: 'Printer jams', queueId: desk.id, body: 'The printer jams.' });
		await send('/tickets', { title: 'Paper stuck', queueId: desk.id, body: 'Paper is stuck.' });
		await refreshAll();
		return true;
	})()`, &made)

	rows := `String(document.querySelectorAll('#table-tickets tbody tr').length)`

	p.run("open the tickets", p.click(`.tab[data-view="tickets"]`),
		chromedp.WaitVisible("#table-tickets", chromedp.ByID))
	p.waitEvaluates("both tickets listed", rows, "2")
	p.run("search for the printer", p.click("#ticket-search > summary"),
		chromedp.SendKeys(`#form-ticket-filter [name="text"]`, "printer", chromedp.ByQuery))
	p.waitEvaluates("the printer alone", rows, "1")

	p.run("keep the search", chromedp.SendKeys(`#form-ticket-saved [name="name"]`, "Printers", chromedp.ByQuery),
		p.click(`#form-ticket-saved button[type="submit"]`))
	p.waitForText("#toast", "Search saved.")
	p.waitEvaluates("the search offered and chosen", `(() => {
		const select = document.querySelector('#form-ticket-saved [name="saved"]');
		return select.selectedOptions[0]?.textContent ?? '';
	})()`, "Printers")

	p.run("ask for everything again", p.click("#ticket-search-clear"))
	p.waitEvaluates("both tickets back", rows, "2")
	p.run("close the further criteria", chromedp.Evaluate(`document.querySelector('#ticket-search').open = false`, nil))

	p.run("run the saved search", chromedp.Evaluate(`(() => {
		const select = document.querySelector('#form-ticket-saved [name="saved"]');
		select.value = [...select.options].find((option) => option.textContent === 'Printers').value;
		select.dispatchEvent(new Event('change', { bubbles: true }));
		return true;
	})()`, nil))
	p.waitEvaluates("the printer alone again", rows, "1")

	var applied struct {
		Text string `json:"text"`
		Open bool   `json:"open"`
	}

	p.awaitJSON(`(async () => ({
		text: document.querySelector('#form-ticket-filter [name="text"]').value,
		open: document.querySelector('#ticket-search').open,
	}))()`, &applied)

	if applied.Text != "printer" || !applied.Open {
		t.Errorf("the saved search put the filter at %+v; want its words, the further criteria folded out", applied)
	}

	p.run("keep another", chromedp.SendKeys(`#form-ticket-saved [name="name"]`, "Paper", chromedp.ByQuery),
		p.click(`#form-ticket-saved button[type="submit"]`))
	p.waitEvaluates("two searches kept",
		`String(document.querySelectorAll('#form-ticket-saved [name="saved"] option[value]:not([value=""])').length)`, "2")

	p.run("let the chosen one go", p.click("#ticket-saved-delete"))
	p.waitForText("#toast", "Saved search deleted.")
	p.waitEvaluates("one search kept",
		`String(document.querySelectorAll('#form-ticket-saved [name="saved"] option[value]:not([value=""])').length)`, "1")

	p.run("sign out", p.click("#logout"), chromedp.WaitVisible("#form-login", chromedp.ByID))

	var left struct {
		Options int `json:"options"`
		Cached  int `json:"cached"`
	}

	p.awaitJSON(`(async () => ({
		options: document.querySelectorAll('#form-ticket-saved [name="saved"] option').length,
		cached: (cache.savedSearches ?? []).length,
	}))()`, &left)

	if left.Options != 0 || left.Cached != 0 {
		t.Errorf("after signing out the saved searches are still in the page: %+v", left)
	}
}
