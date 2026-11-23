//go:build browser

package browser

import (
	"strings"
	"testing"

	"github.com/chromedp/chromedp"
)

// An answer template is set up on the ticket setup screen for a queue, and
// whoever writes in a ticket of that queue starts an article from it: its
// subject where the article has none, its text after what is written already,
// the ticket's placeholders filled. A template of another queue is not offered.
func TestAnArticleStartsFromAnAnswerTemplate(t *testing.T) {
	t.Parallel()

	p := open(t)
	p.readyAdmin()

	var made bool

	p.awaitJSON(`(async () => {
		const send = (path, body) => api(path, { method: 'POST', body: JSON.stringify(body) });
		const hardware = await send('/queues', { name: 'Hardware' });
		await send('/response-templates', { name: 'Hardware only', body: 'Not for the desk.', queueId: hardware.id });
		const desk = (await api('/queues')).items.find((queue) => queue.name === 'Service Desk');
		await send('/tickets', { title: 'Printer', queueId: desk.id, body: 'It jams.' });
		await refreshAll();
		return true;
	})()`, &made)

	if !made {
		t.Fatal("could not make the other queue's template and open the ticket")
	}

	p.run("open the communication", p.click(`.tab[data-view="communication"]`),
		chromedp.WaitVisible("#form-template", chromedp.ByID))
	p.run("set the desk's template up",
		chromedp.SetValue(`#form-template [name="name"]`, "Desk thanks", chromedp.ByQuery),
		p.chooseOptionByText(`#form-template [name="queueId"]`, "Service Desk"),
		chromedp.SetValue(`#form-template [name="subject"]`, "Re: <KIX_TICKET_Title>", chromedp.ByQuery),
		chromedp.SetValue(`#form-template [name="body"]`, "We have <KIX_TICKET_Title> in hand.", chromedp.ByQuery),
		p.click("#template-submit"))
	p.waitForText("#table-template tbody", "Desk thanks")

	if row := p.text("#table-template tbody"); !strings.Contains(row, "Service Desk") || !strings.Contains(row, "Hardware") {
		t.Errorf("the templates read %q; want each with its queue", row)
	}

	p.run("open the tickets", p.click(`.tab[data-view="tickets"]`), chromedp.WaitVisible("#table-tickets", chromedp.ByID))
	p.waitForText("#table-tickets tbody", "Printer")
	p.run("open the printer", chromedp.Evaluate(`[...document.querySelectorAll('#table-tickets tbody tr')]
		.find((row) => row.textContent.includes('Printer')).querySelector('button.link').click()`, nil),
		chromedp.WaitVisible("#ticket-detail", chromedp.ByID))
	p.waitForNode(`#form-article [name="template"] option[value]:not([value=""])`)

	var offered []string

	p.evalJSON(`JSON.stringify([...document.querySelectorAll('#form-article [name="template"] option')]
		.filter((option) => option.value).map((option) => option.textContent))`, &offered)

	if len(offered) != 1 || offered[0] != "Desk thanks" {
		t.Fatalf("the article offers the templates %v; want the desk's alone", offered)
	}

	// Chosen from script rather than through SetValue, which reads the value back:
	// choosing a template puts the choice back to none once it is used, which is
	// the point, and SetValue takes that for a value it could not set.
	p.run("write a greeting and start from the template",
		chromedp.SetValue(`#form-article [name="body"]`, "Hello.", chromedp.ByQuery),
		chromedp.Evaluate(`(() => {
			const choice = document.querySelector('#form-article [name="template"]');
			choice.value = [...choice.options].find((option) => option.textContent === 'Desk thanks').value;
			choice.dispatchEvent(new Event('change', { bubbles: true }));
		})()`, nil))

	if choice := p.value(`#form-article [name="template"]`); choice != "" {
		t.Errorf("the choice of template stays at %q once used; want it back at none", choice)
	}

	if subject := p.value(`#form-article [name="subject"]`); subject != "Re: Printer" {
		t.Errorf("the article's subject reads %q, want the template's, filled", subject)
	}

	if body := p.value(`#form-article [name="body"]`); body != "Hello.\n\nWe have Printer in hand." {
		t.Errorf("the article reads %q; want the greeting and then the template's text, filled", body)
	}
}
