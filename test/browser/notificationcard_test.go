//go:build browser

package browser

import (
	"slices"
	"strings"
	"testing"

	"github.com/chromedp/chromedp"
)

// A notification is set up on the ticket setup screen: the events and the
// recipients it names, the tickets it is about, and what it says in the
// languages it speaks. A language left empty is not spoken, and one half
// written asks for the rest rather than being sent to be refused. Opened again,
// the form shows the notification as it was saved.
func TestANotificationIsSetUpOnTheTicketSetupScreen(t *testing.T) {
	t.Parallel()

	p := open(t)
	p.readyAdmin()

	p.run("open the ticket setup", p.click(`.tab[data-view="ticketsetup"]`),
		chromedp.WaitVisible("#form-notification", chromedp.ByID))

	const english = `#notification-messages [data-language="en"]`

	const german = `#notification-messages [data-language="de"]`

	p.run("name it and say when and to whom",
		chromedp.SetValue(`#form-notification [name="name"]`, "Opened at the desk", chromedp.ByQuery),
		chromedp.Evaluate(`document.querySelector('#notification-events [data-event="TicketCreate"]').click();
			document.querySelector('#notification-recipients [data-recipient="AgentWritePermissions"]').click();
			[...document.querySelectorAll('#notification-queues label')]
				.find((label) => label.textContent.includes('Service Desk')).querySelector('input').click();`, nil),
		chromedp.SetValue(english+`[data-part="subject"]`, "New: <KIX_TICKET_Title>", chromedp.ByQuery),
		chromedp.SetValue(english+`[data-part="body"]`, "Opened.", chromedp.ByQuery))

	required := func(selector string) bool {
		var asked bool

		p.evalJSON(`JSON.stringify(document.querySelector('`+selector+`').required)`, &asked)

		return asked
	}

	if required(german + `[data-part="body"]`) {
		t.Error("the German text is asked for while nothing is written in German")
	}

	p.run("begin the German", chromedp.SetValue(german+`[data-part="subject"]`, "Neu", chromedp.ByQuery))

	if !required(german + `[data-part="body"]`) {
		t.Error("a German subject without a text does not ask for the text, which the server refuses")
	}

	// Emptied from script, because chromedp's SetValue takes an empty value for one
	// it could not set; the input event is the one typing raises.
	p.run("leave the German after all",
		chromedp.Evaluate(`(() => {
			const subject = document.querySelector('`+german+`[data-part="subject"]');
			subject.value = '';
			subject.dispatchEvent(new Event('input', { bubbles: true }));
		})()`, nil),
		p.click("#notification-submit"))
	p.waitForText("#table-notification tbody", "Opened at the desk")

	if row := p.text("#table-notification tbody"); !strings.Contains(row, "Ticket opened") ||
		!strings.Contains(row, "Those who work in its queue") {
		t.Errorf("the notification's row reads %q; want its event and its recipients in words", row)
	}

	var saved struct {
		Languages []string `json:"languages"`
		Queues    []uint   `json:"queues"`
		Desk      uint     `json:"desk"`
	}

	p.awaitJSON(`(async () => {
		const made = (await api('/notification-definitions')).items.find((n) => n.name === 'Opened at the desk');
		const desk = (await api('/queues')).items.find((queue) => queue.name === 'Service Desk');
		return { languages: made.messages.map((m) => m.language), queues: made.criteria.queueIds, desk: desk.id };
	})()`, &saved)

	if !slices.Equal(saved.Languages, []string{"en"}) || !slices.Equal(saved.Queues, []uint{saved.Desk}) {
		t.Errorf("the server keeps the notification speaking %v about the queues %v; want English about the desk",
			saved.Languages, saved.Queues)
	}

	p.run("open it again", chromedp.Evaluate(`[...document.querySelectorAll('#table-notification tbody tr')]
		.find((row) => row.textContent.includes('Opened at the desk')).querySelector('button.link:not(.danger)').click()`, nil))
	p.waitForText("#notification-form-title", "Opened at the desk")

	var ticked bool

	p.evalJSON(`JSON.stringify(document.querySelector('#notification-events [data-event="TicketCreate"]').checked)`, &ticked)

	if !ticked || p.value(english+`[data-part="subject"]`) != "New: <KIX_TICKET_Title>" {
		t.Errorf("the opened notification shows its event %v and its subject %q", ticked,
			p.value(english+`[data-part="subject"]`))
	}
}
