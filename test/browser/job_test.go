//go:build browser

package browser

import (
	"strings"
	"testing"

	"github.com/chromedp/chromedp"
)

// A job is set up on the automation screen - the tickets it takes and the macro
// it runs on them, with no plan to start it - and run from its row: the run says
// what it did, the ticket it took is changed, and the run is shown below with
// how it was started.
func TestAJobIsSetUpAndRunFromTheScreen(t *testing.T) {
	t.Parallel()

	p := open(t)
	p.readyAdmin()

	var made bool

	p.awaitJSON(`(async () => {
		const send = (path, body) => api(path, { method: 'POST', body: JSON.stringify(body) });
		const priorities = (await api('/ticket-priorities')).items;
		const desk = (await api('/queues')).items.find((queue) => queue.name === 'Service Desk');
		await send('/tickets', { title: 'Printer', queueId: desk.id, body: 'It jams.' });
		await send('/macros', { name: 'Raise', actions: [{ type: 'PrioritySet',
			parameters: { priorityId: String(priorities.find((p) => p.name === 'very-high').id) } }] });
		await refreshAll();
		return true;
	})()`, &made)

	if !made {
		t.Fatal("could not open the ticket and make the macro")
	}

	p.run("open the automation", p.click(`.tab[data-view="automation"]`),
		chromedp.WaitVisible("#form-job", chromedp.ByID))

	p.run("set the job up",
		chromedp.SetValue(`#form-job [name="name"]`, "Raise the desk", chromedp.ByQuery),
		chromedp.Evaluate(`[...document.querySelectorAll('#job-queues label')]
			.find((label) => label.textContent.includes('Service Desk')).querySelector('input').click()`, nil),
		p.chooseOptionByText("#job-macro-kind", "Raise"), p.click("#job-add-macro"),
		p.click("#job-submit"))
	p.waitForText("#table-job tbody", "Raise the desk")

	if row := p.text("#table-job tbody"); !strings.Contains(row, "by hand only") {
		t.Errorf("a job no plan starts reads %q; want it run by hand only", row)
	}

	p.run("run it", runTheJob("Raise the desk"))
	p.waitForText("#toast", "The job ran on 1 ticket(s): 1 changed, 0 refused.")
	p.waitForText("#table-run tbody", "by hand")

	var raised bool

	p.awaitJSON(`(async () => {
		const ticket = (await api('/tickets')).items.find((one) => one.title === 'Printer');
		const veryHigh = (await api('/ticket-priorities')).items.find((one) => one.name === 'very-high');
		return ticket.priorityId === veryHigh.id;
	})()`, &raised)

	if !raised {
		t.Error("the job ran and the ticket it took is not very high")
	}
}

// A step a job could not take is shown among its run's lines in the reader's
// words, the value its refusal names written as the screen writes it, and the
// ticket it could not take it on is a press away.
func TestARunSaysWhatItCouldNotDoInTheReadersWords(t *testing.T) {
	t.Parallel()

	p := open(t)
	p.readyAdmin()

	var made bool

	p.awaitJSON(`(async () => {
		const send = (path, body) => api(path, { method: 'POST', body: JSON.stringify(body) });
		const desk = (await api('/queues')).items.find((queue) => queue.name === 'Service Desk');
		const urgent = await send('/ticket-priorities', { name: 'Urgent', level: 1 });
		await send('/tickets', { title: 'Printer', queueId: desk.id, body: 'It jams.' });
		const macro = await send('/macros', { name: 'Make it urgent',
			actions: [{ type: 'PrioritySet', parameters: { priorityId: String(urgent.id) } }] });
		await send('/jobs', { name: 'Urgent desk', criteria: { queueIds: [desk.id] }, macroIds: [macro.id] });
		await api('/ticket-priorities/' + urgent.id, { method: 'PUT', body: JSON.stringify({ valid: false }) });
		await refreshAll();
		return true;
	})()`, &made)

	if !made {
		t.Fatal("could not set up a job whose step will be refused")
	}

	p.chooseLanguage("de")

	p.run("open the automation", p.click(`.tab[data-view="automation"]`),
		chromedp.WaitVisible("#table-job", chromedp.ByID))
	p.waitForText("#table-job tbody", "Urgent desk")
	p.run("run it", runTheJob("Urgent desk"))
	p.waitForText("#toast", "Der Job lief über 1 Ticket(s): 0 geändert, 1 abgelehnt.")
	p.waitForText("#table-run tbody", "Die Priorität „Urgent“ wird für Tickets nicht mehr angeboten.")

	if lines := p.text("#table-run tbody .run-lines"); !strings.Contains(lines, "Make it urgent · Schritt 1") {
		t.Errorf("the run's line reads %q; want the macro and the step it could not take", lines)
	}

	p.run("open the ticket", p.click("#table-run tbody .run-lines button.link"),
		chromedp.WaitVisible("#ticket-detail", chromedp.ByID))
	p.waitForText("#ticket-detail", "Printer")
}

// runTheJob presses the run button in a job's row.
func runTheJob(name string) chromedp.Action {
	return chromedp.Evaluate(`[...document.querySelectorAll('#table-job tbody tr')]
		.find((row) => row.textContent.includes(`+jsString(name)+`))
		.querySelector('td.actions button.link:nth-child(2)').click()`, nil)
}
